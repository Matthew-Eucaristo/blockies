// Package server serves boards over HTTP: the viewer pages, a small REST API
// for agents and scripts, a server-sent event stream per board, and MCP.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Matthew-Eucaristo/blockies/boards"
	"github.com/Matthew-Eucaristo/blockies/script"
	"github.com/Matthew-Eucaristo/blockies/web"
)

// Options configure [Handler].
type Options struct {
	MCP *mcp.Server  // nil: no /mcp endpoint
	Web fs.FS        // nil: the viewer embedded in the binary
	Log *slog.Logger // nil: slog.Default()
}

// Handler returns the whole HTTP surface for svc.
func Handler(svc *boards.Service, o Options) http.Handler {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	static := o.Web
	if static == nil {
		static = web.Files
	}
	h := &handler{svc: svc, log: o.Log}
	mux := http.NewServeMux()

	mux.Handle("GET /web/", http.StripPrefix("/web/", noCache(http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", page(static, "index.html"))
	mux.HandleFunc("GET /b/{board}", page(static, "board.html"))
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

	if o.MCP != nil {
		mh := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return o.MCP }, nil)
		mux.Handle("/mcp", http.NewCrossOriginProtection().Handler(mh))
	}
	return mux
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
	svc *boards.Service
	log *slog.Logger
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

func (h *handler) body(w http.ResponseWriter, r *http.Request) (string, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, boards.MaxScript+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, &boards.Error{Code: "too_big", Detail: "a board script holds at most 256 KiB"})
		} else {
			writeJSON(w, http.StatusBadRequest, &boards.Error{Code: "bad_request", Detail: err.Error()})
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
