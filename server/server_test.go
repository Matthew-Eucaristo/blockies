package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/Matthew-Eucaristo/blockies/agent"
	"github.com/Matthew-Eucaristo/blockies/boards"
	"github.com/Matthew-Eucaristo/blockies/client"
	"github.com/Matthew-Eucaristo/blockies/examples"
	"github.com/Matthew-Eucaristo/blockies/internal/fakemodel"
	"github.com/Matthew-Eucaristo/blockies/mcpserver"
	"github.com/Matthew-Eucaristo/blockies/server"
)

func start(t *testing.T) (*httptest.Server, *client.Client) {
	t.Helper()
	svc := boards.NewService(boards.NewMemStore(), "http://viewer")
	ts := httptest.NewServer(server.Handler(svc, server.Options{MCP: mcpserver.New(svc, "test")}))
	t.Cleanup(ts.Close)
	return ts, client.New(ts.URL)
}

func TestHTTPRoundTrip(t *testing.T) {
	_, c := start(t)
	ctx := context.Background()
	r, err := c.Write(ctx, "demo", "proc a\nproc b\n")
	if err != nil || r.Rev != 1 || r.URL != "http://viewer/b/demo" {
		t.Fatalf("write: %+v %v", r, err)
	}
	r, err = c.Append(ctx, "demo", "a -> b : hi\nnot a line")
	if err != nil || len(r.Rejected) != 1 || r.Rejected[0].Line != 4 {
		t.Fatalf("append: %+v %v", r, err)
	}
	r, err = c.Read(ctx, "demo")
	if err != nil || r.Script != "proc a\nproc b\na -> b : hi\n" {
		t.Fatalf("read: %+v %v", r, err)
	}
	list, err := c.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	var e *boards.Error
	if _, err := c.Read(ctx, "missing"); !asCode(err, &e, "not_found") {
		t.Fatalf("missing: %v", err)
	}
	if err := c.Delete(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestStreamSendsFramesOnChange(t *testing.T) {
	ts, c := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/boards/live/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	frames := make(chan boards.Frame, 4)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var f boards.Frame
				if json.Unmarshal([]byte(data), &f) == nil {
					frames <- f
				}
			}
		}
	}()
	if f := <-frames; f.Rev != 0 {
		t.Fatalf("first frame rev %d", f.Rev)
	}
	if _, err := c.Write(ctx, "live", "proc a\nproc b\na -> b : hi\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-frames:
		if f.Rev != 1 || len(f.Timeline.Msgs) != 1 {
			t.Fatalf("frame %+v", f)
		}
	case <-ctx.Done():
		t.Fatal("no frame after a change")
	}
}

func TestViewerPagesAndMCPAreServed(t *testing.T) {
	ts, _ := start(t)
	for _, path := range []string{"/", "/b/anything", "/web/app.js", "/web/style.css", "/api/guide", "/healthz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %s", path, resp.Status)
		}
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"name":"blockies"`) {
		t.Fatalf("mcp initialize: %s %s", resp.Status, b)
	}
}

func asCode(err error, e **boards.Error, code string) bool {
	return errors.As(err, e) && (*e).Code == code
}

func TestTalkStreamsEventsAndUndoes(t *testing.T) {
	model := fakemodel.New(t,
		fakemodel.Reply{{Text: "Two parts. "}, {Tool: "draw", Input: `{"lines": "proc a \"A\"\nproc b \"B\"\na -> b : hi\n"}`}},
		fakemodel.Reply{{Text: "Watch a talk to b."}},
	)
	svc := boards.NewService(boards.NewMemStore(), "http://viewer")
	ag := agent.New(svc, agent.Config{Ready: func() bool { return true }, Options: []option.RequestOption{option.WithBaseURL(model.URL), option.WithAPIKey("test"), option.WithMaxRetries(0)}})
	ts := httptest.NewServer(server.Handler(svc, server.Options{Agent: ag}))
	t.Cleanup(ts.Close)

	res, err := http.Post(ts.URL+"/api/boards/talk/talk", "application/json", strings.NewReader(`{"text": "show me"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", res.Header.Get("Content-Type"))
	}
	var types []string
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev agent.Event
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatal(err)
			}
			types = append(types, ev.Type)
		}
	}
	if len(types) < 4 || types[0] != "say" || types[len(types)-1] != "done" || !strings.Contains(strings.Join(types, " "), "draw") {
		t.Fatalf("events = %v", types)
	}
	if r, _ := svc.Read(context.Background(), "talk"); r.Script != "proc a \"A\"\nproc b \"B\"\na -> b : hi\n" {
		t.Fatalf("script = %q", r.Script)
	}

	var turns struct {
		Turns []agent.Turn `json:"turns"`
	}
	get(t, ts.URL+"/api/boards/talk/talk", &turns)
	if len(turns.Turns) != 2 || turns.Turns[1].Lines != 3 {
		t.Fatalf("turns = %+v", turns.Turns)
	}

	undo := func() int {
		res, err := http.Post(ts.URL+"/api/boards/talk/talk/undo", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if c := undo(); c != http.StatusOK {
		t.Fatalf("undo: %d", c)
	}
	if c := undo(); c != http.StatusConflict {
		t.Fatalf("second undo: %d", c)
	}

	// Another site cannot make the server talk (and spend tokens), nor
	// write to boards with a form or a no-cors fetch.
	req, _ := http.NewRequest("POST", ts.URL+"/api/boards/talk/talk", strings.NewReader(`{"text": "hi"}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site talk: %d", res2.StatusCode)
	}
}

func TestTalkWithoutAgent(t *testing.T) {
	ts, _ := start(t)
	res, err := http.Get(ts.URL + "/api/boards/x/talk")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func get(t *testing.T, url string, v any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestHostAndOriginGuards(t *testing.T) {
	ts, _ := start(t)
	do := func(method, path, body string, hdr map[string]string) int {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if c := do("GET", "/api/boards", "", map[string]string{"Host": "evil.example:4318"}); c != http.StatusForbidden {
		t.Fatalf("rebound host: %d", c)
	}
	if c := do("POST", "/api/boards/x/lines", "proc a", map[string]string{"Content-Type": "text/plain", "Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}); c != http.StatusForbidden {
		t.Fatalf("cross-site append: %d", c)
	}
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/boards/x", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res0, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var e boards.Error
	_ = json.NewDecoder(res0.Body).Decode(&e)
	_ = res0.Body.Close()
	if res0.StatusCode != http.StatusForbidden || e.Code != "cross_origin" {
		t.Fatalf("cross-site delete: %d %+v", res0.StatusCode, e)
	}
	if c := do("POST", "/api/boards/x/lines", "proc a", nil); c != http.StatusOK {
		t.Fatalf("CLI append: %d", c)
	}
	if c := do("GET", "/api/boards/x", "", map[string]string{"Host": "localhost:4318"}); c != http.StatusOK {
		t.Fatalf("localhost: %d", c)
	}
	res, err := http.DefaultClient.Do(func() *http.Request {
		r, _ := http.NewRequest("GET", ts.URL+"/studio/x", nil)
		return r
	}())
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.Request.URL.Path != "/b/x" {
		t.Fatalf("studio redirect landed on %s", res.Request.URL.Path)
	}
}

func tapOn(t *testing.T, base, board, id string) (int, string) {
	t.Helper()
	res, err := http.Post(base+"/api/boards/"+board+"/tap", "application/json", strings.NewReader(`{"id":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var e boards.Error
	_ = json.NewDecoder(res.Body).Decode(&e)
	return res.StatusCode, e.Code
}

func TestTapsBecomeLines(t *testing.T) {
	ts, c := start(t)
	ctx := context.Background()
	src, _ := examples.Get("vote")
	if _, err := c.Write(ctx, "v", src); err != nil {
		t.Fatal(err)
	}
	if code, _ := tapOn(t, ts.URL, "v", "vote/b"); code != http.StatusAccepted {
		t.Fatalf("tap: %d", code)
	}
	if code, e := tapOn(t, ts.URL, "v", "vote/a"); code != http.StatusTooManyRequests || e != "slow_down" {
		t.Fatalf("second tap at once: %d %s", code, e)
	}
	if code, e := tapOn(t, ts.URL, "v", "stage"); code != http.StatusBadRequest || e != "no_click" {
		t.Fatalf("tap on a plain block: %d %s", code, e)
	}
	if code, e := tapOn(t, ts.URL, "v", "x\nend stage"); code != http.StatusBadRequest || e != "bad_request" {
		t.Fatalf("injected line: %d %s", code, e)
	}
	if code, e := tapOn(t, ts.URL, strings.Repeat("z", 5000), "vote/a"); code != http.StatusBadRequest || e != "bad_board" {
		t.Fatalf("huge board name: %d %s", code, e)
	}
	time.Sleep(tapWindowForTest)
	r, _ := c.Read(ctx, "v")
	if !strings.HasSuffix(r.Script, "\ntap vote/b\n") || strings.Contains(r.Script, "end stage") || len(r.Errors) != 0 {
		t.Fatalf("script tail %q, errors %v", r.Script[len(r.Script)-40:], r.Errors)
	}
}

// A refused tap must not shift the next one into the past (the old design
// joined taps with "&" to whatever line came before).
func TestTapsNeverLandInThePast(t *testing.T) {
	ts, c := start(t)
	ctx := context.Background()
	if _, err := c.Write(ctx, "p", "proc a\nproc b\nclick a: a = on\nwait 10\n"); err != nil {
		t.Fatal(err)
	}
	if code, _ := tapOn(t, ts.URL, "p", "b"); code != http.StatusBadRequest {
		t.Fatalf("tap b: %d", code)
	}
	if code, _ := tapOn(t, ts.URL, "p", "a"); code != http.StatusAccepted {
		t.Fatalf("tap a: %d", code)
	}
	time.Sleep(tapWindowForTest)
	f, err := c.Read(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.Script, "wait 10\ntap a\n") {
		t.Fatalf("script = %q", f.Script)
	}
}

func TestASteadyStreamOfTapsMovesTime(t *testing.T) {
	ts, c := start(t)
	ctx := context.Background()
	src, _ := examples.Get("vote")
	if _, err := c.Write(ctx, "s", src); err != nil {
		t.Fatal(err)
	}
	before, _ := c.Read(ctx, "s")
	for i := range 10 {
		if code, _ := tapOn(t, ts.URL, "s", "vote/a"); code != http.StatusAccepted {
			t.Fatalf("tap %d: %d", i, code)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(tapWindowForTest)
	after, _ := c.Read(ctx, "s")
	added := strings.Split(strings.TrimSpace(strings.TrimPrefix(after.Script, before.Script)), "\n")
	total := 0
	for _, l := range added {
		f := strings.Fields(l)
		n := 1
		if len(f) == 3 {
			n, _ = strconv.Atoi(f[2])
		}
		total += n
	}
	if total != 10 || len(added) < 3 || len(added) > 6 {
		t.Fatalf("10 taps over 1.5 s made %d lines with %d taps: %q", len(added), total, added)
	}
}

const tapWindowForTest = 600 * time.Millisecond

func TestControlsSetStartingValues(t *testing.T) {
	ts, c := start(t)
	ctx := context.Background()
	src := "proc jumlah \"Jumlah\" skin=slider min=0 max=100 value 3\nproc aktif skin=toggle\nproc nama skin=input\nproc total skin=number\ncalc total = jumlah * 2\n"
	if _, err := c.Write(ctx, "c", src); err != nil {
		t.Fatal(err)
	}
	set := func(id string, v any) (int, string) {
		b, _ := json.Marshal(map[string]any{"id": id, "value": v})
		res, err := http.Post(ts.URL+"/api/boards/c/input", "application/json", strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var e boards.Error
		_ = json.NewDecoder(res.Body).Decode(&e)
		return res.StatusCode, e.Code
	}
	for _, step := range []struct {
		id   string
		v    any
		code int
		err  string
	}{
		{"jumlah", 40, http.StatusAccepted, ""},
		{"jumlah", 150, http.StatusAccepted, ""}, // last one wins, clamped to max
		{"aktif", true, http.StatusAccepted, ""},
		{"nama", `Budi "B" Santoso`, http.StatusAccepted, ""},
		{"total", 5, http.StatusBadRequest, "no_control"},
		{"jumlah", "lots", http.StatusBadRequest, "bad_value"},
		{"nama", strings.Repeat("x", 200), http.StatusBadRequest, "bad_value"},
		{"nama", "a\u0001b", http.StatusBadRequest, "bad_value"},
	} {
		if code, e := set(step.id, step.v); code != step.code || e != step.err {
			t.Fatalf("set %s %v: %d %s", step.id, step.v, code, e)
		}
	}
	time.Sleep(tapWindowForTest)
	r, err := c.Read(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	want := "proc jumlah \"Jumlah\" skin=slider min=0 max=100 value 100\nproc aktif skin=toggle value 1\nproc nama skin=input value \"Budi \\\"B\\\" Santoso\"\nproc total skin=number\ncalc total = jumlah * 2\n"
	if r.Script != want || len(r.Errors) != 0 {
		t.Fatalf("script:\n%s\nwant\n%s\nerrors %v", r.Script, want, r.Errors)
	}
}
