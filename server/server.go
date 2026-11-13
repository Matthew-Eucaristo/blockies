// Package server serves boards over HTTP: the viewer pages, a small REST API
// for agents and scripts, a server-sent event stream per board, and MCP.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Matthew-Eucaristo/blockies/agent"
	"github.com/Matthew-Eucaristo/blockies/boards"
	"github.com/Matthew-Eucaristo/blockies/kernel"
	"github.com/Matthew-Eucaristo/blockies/script"
	"github.com/Matthew-Eucaristo/blockies/web"
)

// Options configure [Handler].
type Options struct {
	MCP   *mcp.Server  // nil: no /mcp endpoint
	Agent *agent.Agent // nil: the studio cannot talk (no_agent)
	Web   fs.FS        // nil: the viewer embedded in the binary
	Log   *slog.Logger // nil: slog.Default()
	// Hosts the server answers to, besides localhost, 127.0.0.1 and ::1
	// (for example the host of --url, or the IP it listens on). Requests
	// for any other Host are refused, which stops DNS rebinding.
	Hosts []string
}

// Handler returns the whole HTTP surface for svc. Every request must name an
// allowed Host, and state-changing requests from browsers must come from the
// same origin (cross-site forms and scripts get 403).
func Handler(svc *boards.Service, o Options) http.Handler {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	static := o.Web
	if static == nil {
		static = web.Files
	}
	h := &handler{svc: svc, agent: o.Agent, log: o.Log, taps: newTapper(svc, o.Log)}
	mux := http.NewServeMux()

	mux.Handle("GET /web/", http.StripPrefix("/web/", noCache(http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", page(static, "index.html"))
	mux.HandleFunc("GET /b/{board}", page(static, "board.html"))
	mux.HandleFunc("GET /studio/{board}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/b/"+url.PathEscape(r.PathValue("board")), http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })

	mux.HandleFunc("GET /api/guide", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		io.WriteString(w, script.Guide)
	})
	mux.HandleFunc("GET /api/boards", h.list)
	mux.HandleFunc("GET /api/boards/{board}", h.read)
	mux.HandleFunc("PUT /api/boards/{board}", h.write)
	mux.HandleFunc("POST /api/boards/{board}/lines", h.append)
	mux.HandleFunc("DELETE /api/boards/{board}", h.delete)
	mux.HandleFunc("GET /api/boards/{board}/frame", h.frame)
	mux.HandleFunc("GET /api/boards/{board}/stream", h.stream)

	// The studio's conversation with the agent.
	mux.HandleFunc("GET /api/boards/{board}/talk", h.turns)
	mux.HandleFunc("POST /api/boards/{board}/talk", h.talk)
	mux.HandleFunc("POST /api/boards/{board}/talk/undo", h.undo)
	mux.HandleFunc("DELETE /api/boards/{board}/talk", h.reset)

	// People tapping blocks (a vote, a button): each tap becomes a `tap`
	// line through boards.API, so every screen replays the same run.
	mux.HandleFunc("POST /api/boards/{board}/tap", h.tap)
	mux.HandleFunc("POST /api/boards/{board}/input", h.input)

	if o.MCP != nil {
		mh := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return o.MCP }, nil)
		mux.Handle("/mcp", mh)
	}
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, &boards.Error{Code: "cross_origin", Detail: "changes must come from this server's own pages, the CLI or an MCP client"})
	}))
	return hostGuard(o.Hosts, csrf.Handler(mux))
}

// hostGuard refuses requests whose Host is not allowed.
func hostGuard(extra []string, next http.Handler) http.Handler {
	allowed := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range extra {
		allowed[strings.ToLower(strings.Trim(h, "[]"))] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !allowed[strings.ToLower(strings.Trim(host, "[]"))] {
			writeJSON(w, http.StatusForbidden, &boards.Error{Code: "bad_host", Detail: "this server does not answer to that host name; start it with --url to add one"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewHTTPServer wraps a handler with timeouts that suit long-lived streams:
// no write timeout (each stream manages its own), bounded reads.
func NewHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

type handler struct {
	svc   *boards.Service
	agent *agent.Agent
	log   *slog.Logger

	taps *tapper
}

// tap queues a person's tap on a block; see tapper. It answers 202 once the
// tap is queued: the board changes within half a second for every viewer.
func (h *handler) tap(w http.ResponseWriter, r *http.Request) {
	board := r.PathValue("board")
	var in struct {
		ID string `json:"id"`
	}
	if !boards.ValidID(board) {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_board", Detail: "not a board name"})
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil || !script.IsID(in.ID) {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_request", Detail: `send {"id": "process id"}`})
		return
	}
	p, alive, err := h.svc.Proc(r.Context(), board, in.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if !alive || !p.Tap {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "no_click", Detail: in.ID + " is not a button on this board"})
		return
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !h.taps.tap(board, in.ID, peer) {
		writeJSON(w, http.StatusTooManyRequests, &boards.Error{Code: "slow_down", Detail: "one tap at a time"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	rs, err := h.svc.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": rs})
}

func (h *handler) read(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Read(r.Context(), r.PathValue("board"))
	h.reply(w, res, err)
}

func (h *handler) write(w http.ResponseWriter, r *http.Request) {
	body, ok := h.body(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Write(r.Context(), r.PathValue("board"), body)
	h.reply(w, res, err)
}

func (h *handler) append(w http.ResponseWriter, r *http.Request) {
	body, ok := h.body(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Append(r.Context(), r.PathValue("board"), body)
	h.reply(w, res, err)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), r.PathValue("board")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) frame(w http.ResponseWriter, r *http.Request) {
	f, err := h.svc.Frame(r.Context(), r.PathValue("board"))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(f)
}

// stream sends the board's frame now and after every change, as server-sent
// events. EventSource reconnects on its own, and every frame is the whole
// board, so a reconnect needs no catch-up protocol.
func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	frames, cancel, err := h.svc.Subscribe(r.Context(), r.PathValue("board"))
	if err != nil {
		h.fail(w, err)
		return
	}
	defer cancel()
	// Streams outlive the server's read timeout; lift both deadlines.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		var chunk []byte
		select {
		case <-r.Context().Done():
			return
		case f, ok := <-frames:
			if !ok {
				io.WriteString(w, "event: deleted\ndata: {}\n\n")
				_ = rc.Flush()
				return
			}
			chunk = append(append([]byte("event: frame\ndata: "), f...), '\n', '\n')
		case <-ping.C:
			chunk = []byte(": ping\n\n")
		}
		if _, err := w.Write(chunk); err != nil {
			return
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func (h *handler) turns(w http.ResponseWriter, r *http.Request) {
	if !h.hasAgent(w) {
		return
	}
	turns := h.agent.Turns(r.PathValue("board"))
	if turns == nil {
		turns = []agent.Turn{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": h.agent.Model(), "ready": h.agent.Ready(), "turns": turns})
}

// talk runs one studio turn and streams its events (say, draw, redraw, undo,
// error, done) as server-sent events in the response. The board itself
// changes through boards.API, so viewers see it on their own stream.
func (h *handler) talk(w http.ResponseWriter, r *http.Request) {
	if !h.hasAgent(w) {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_request", Detail: `send {"text": "..."}`})
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(e agent.Event) {
		b, _ := json.Marshal(e)
		w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
		_ = rc.Flush()
	}
	if err := h.agent.Say(r.Context(), r.PathValue("board"), in.Text, send); err != nil {
		var e *boards.Error
		if !errors.As(err, &e) {
			h.log.Error("talk failed", "err", err)
			e = &boards.Error{Code: "internal", Detail: "the server failed; see its log"}
		}
		send(agent.Event{Type: "error", Code: e.Code, Detail: e.Detail})
	}
}

// input sets a control (slider, input, toggle) to a new starting value; see
// tapper. 202 once queued.
func (h *handler) input(w http.ResponseWriter, r *http.Request) {
	board := r.PathValue("board")
	var in struct {
		ID    string `json:"id"`
		Value any    `json:"value"`
	}
	if !boards.ValidID(board) {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_board", Detail: "not a board name"})
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil || !script.IsID(in.ID) {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_request", Detail: `send {"id": "process id", "value": number or text}`})
		return
	}
	p, alive, err := h.svc.Proc(r.Context(), board, in.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if !alive || !slices.Contains(script.Controls, p.Skin) {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "no_control", Detail: in.ID + " is not a slider, input or toggle on this board"})
		return
	}
	v, ok := controlValue(p, in.Value)
	if !ok {
		writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_value", Detail: "that value does not fit this control"})
		return
	}
	h.taps.set(board, in.ID, v)
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

// controlValue checks a person's value for a control: a number within a
// slider's range, 0 or 1 for a toggle, short text (or a number) for an input.
func controlValue(p kernel.Proc, raw any) (script.Value, bool) {
	switch p.Skin {
	case "slider", "toggle":
		f, ok := raw.(float64)
		if b, isBool := raw.(bool); isBool {
			f, ok = map[bool]float64{true: 1, false: 0}[b], true
		}
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return script.Value{}, false
		}
		if p.Skin == "toggle" {
			return script.Num(map[bool]float64{true: 1, false: 0}[f != 0]), true
		}
		if p.Min != nil {
			f = math.Max(*p.Min, f)
		}
		if p.Max != nil {
			f = math.Min(*p.Max, f)
		}
		f = math.Max(-script.MaxNum, math.Min(script.MaxNum, f))
		return script.Num(math.Round(f*1e6) / 1e6), true
	case "input":
		switch v := raw.(type) {
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > script.MaxNum {
				return script.Value{}, false
			}
			return script.Num(v), true
		case string:
			v = strings.TrimSpace(v)
			if utf8.RuneCountInString(v) > script.MaxLabel || strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return script.Value{}, false
			}
			if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && math.Abs(f) <= script.MaxNum {
				return script.Num(f), true
			}
			return script.Txt(v), true
		}
	}
	return script.Value{}, false
}

func (h *handler) undo(w http.ResponseWriter, r *http.Request) {
	if !h.hasAgent(w) {
		return
	}
	res, err := h.agent.Undo(r.Context(), r.PathValue("board"))
	h.reply(w, res, err)
}

func (h *handler) reset(w http.ResponseWriter, r *http.Request) {
	if !h.hasAgent(w) {
		return
	}
	if err := h.agent.Reset(r.PathValue("board")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) hasAgent(w http.ResponseWriter) bool {
	if h.agent == nil {
		writeJSON(w, http.StatusServiceUnavailable, &boards.Error{Code: "no_agent", Detail: "this server runs without the studio agent"})
	}
	return h.agent != nil
}

func (h *handler) body(w http.ResponseWriter, r *http.Request) (string, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, boards.MaxScript+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, &boards.Error{Code: "too_big", Detail: "a board script holds at most 256 KiB"})
		} else {
			writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_request", Detail: "the request body could not be read"})
		}
		return "", false
	}
	return string(b), true
}

func (h *handler) reply(w http.ResponseWriter, res boards.Result, err error) {
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	var e *boards.Error
	if !errors.As(err, &e) {
		h.log.Error("request failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, &boards.Error{Code: "internal", Detail: "the server failed; see its log"})
		return
	}
	status := http.StatusBadRequest
	switch e.Code {
	case "not_found":
		status = http.StatusNotFound
	case "too_big":
		status = http.StatusRequestEntityTooLarge
	case "busy", "no_history", "conflict":
		status = http.StatusConflict
	case "no_agent":
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func page(files fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(files, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	}
}

// noCache makes browsers revalidate viewer files, so a new binary is picked
// up on reload.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}
