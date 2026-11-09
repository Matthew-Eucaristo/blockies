package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Matthew-Eucaristo/blockies/boards"
	"github.com/Matthew-Eucaristo/blockies/client"
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
