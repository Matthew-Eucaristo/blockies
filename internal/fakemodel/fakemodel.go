// Package fakemodel is a stand-in for the Anthropic Messages API in tests: it
// streams scripted replies the way the real API does, in small pieces.
package fakemodel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Block is one content block of a reply: text, or a tool call with its
// input as raw JSON.
type Block struct {
	Text  string
	Tool  string
	Input string
}

// Reply is one model response. It stops for tools if it has a tool call.
type Reply []Block

// Server serves replies in order, one per request.
type Server struct {
	*httptest.Server
	// Chunk is how many bytes of text or tool input go in one delta.
	Chunk int

	mu       sync.Mutex
	stop     string
	replies  []Reply
	requests []map[string]any
}

// New starts a server; it closes when the test ends.
func New(t *testing.T, replies ...Reply) *Server {
	s := &Server{Chunk: 7, replies: replies}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// SetStop makes every following reply end with this stop reason (for
// example refusal or max_tokens); "" goes back to the natural one.
func (s *Server) SetStop(stop string) {
	s.mu.Lock()
	s.stop = stop
	s.mu.Unlock()
}

// Add queues more replies.
func (s *Server) Add(replies ...Reply) {
	s.mu.Lock()
	s.replies = append(s.replies, replies...)
	s.mu.Unlock()
}

// Requests returns the request bodies received so far.
func (s *Server) Requests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	s.mu.Lock()
	s.requests = append(s.requests, req)
	if len(s.replies) == 0 {
		s.mu.Unlock()
		http.Error(w, `{"type":"error","error":{"type":"api_error","message":"no reply scripted"}}`, http.StatusInternalServerError)
		return
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	stop := s.stop
	s.mu.Unlock()

	Stream(w, fmt.Sprint(req["model"]), reply, s.Chunk, 0, stop)
}

// Stream writes reply as the Messages API's server-sent events, chunk bytes
// per delta, pausing delay between deltas. stop overrides the stop reason
// ("" picks tool_use or end_turn).
func Stream(w http.ResponseWriter, model string, reply Reply, chunk int, delay time.Duration, stop string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if fl != nil {
			fl.Flush()
		}
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_fake", "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
	}})
	auto := "end_turn"
	for i, b := range reply {
		start := map[string]any{"type": "text", "text": ""}
		piece, kind, field := b.Text, "text_delta", "text"
		if b.Tool != "" {
			start = map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_%d", i), "name": b.Tool, "input": map[string]any{}}
			piece, kind, field = b.Input, "input_json_delta", "partial_json"
			auto = "tool_use"
		}
		send("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": start})
		for j := 0; j < len(piece); j += chunk {
			part := piece[j:min(len(piece), j+chunk)]
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": kind, field: part}})
			time.Sleep(delay)
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	if stop == "" {
		stop = auto
	}
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 5}})
	send("message_stop", map[string]any{"type": "message_stop"})
}
