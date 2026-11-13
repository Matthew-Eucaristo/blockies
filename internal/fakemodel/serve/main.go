// Command serve is a scripted stand-in for the Anthropic Messages API, for
// trying the studio without credentials or spending tokens. It is a
// development tool and is not part of the blockies binary.
//
//	go run ./internal/fakemodel/serve &
//	ANTHROPIC_BASE_URL=http://127.0.0.1:4399 ANTHROPIC_API_KEY=fake go run ./cmd/blockies serve --memory
//
// Words in what you say pick a mode: #401, #429 and #500 answer with that
// HTTP error, #refuse declines, #slow streams slowly, #long draws 100
// lines. Otherwise it plays a short scripted conversation: a checkout flow,
// then a database that cannot keep up, then an undo.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	fm "github.com/Matthew-Eucaristo/blockies/internal/fakemodel"
)

type turn struct{ intro, lines, outro string }

var script = []turn{
	{"Here is a checkout: the browser asks the API, the API asks the database. ",
		"proc browser \"Browser\"\nproc api \"API\"\nkind store color=green states=idle,busy\nproc db \"Postgres\" kind=store\nbrowser -> api : POST /checkout\napi -> db : SELECT cart\ndb = busy\nwait 1\ndb -> api : 3 rows\ndb = idle\napi -> browser : 200 OK\n",
		"Watch the database turn busy before it answers."},
	{"Now the database handles one query per tick while requests arrive twice as fast. ",
		"cap db 1\nnote db \"one query per tick\"\nevery 1: api -> db : SELECT stock\nevery 1: api -> db : SELECT price\non db SELECT: db -> $from : rows\nwait 8\n",
		"See its body grow: that is the queue waiting."},
}

func main() {
	addr := flag.String("addr", "127.0.0.1:4399", "listen address")
	flag.Parse()
	log.Printf("fake Messages API on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(serve)))
}

func serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	persons, said := 0, ""
	for _, m := range req.Messages {
		for _, c := range m.Content {
			if _, after, ok := strings.Cut(c.Text, "The person says: "); ok && c.Type == "text" {
				persons++
				said = after
			}
		}
	}
	last := req.Messages[len(req.Messages)-1]
	afterTool := len(last.Content) > 0 && last.Content[0].Type == "tool_result"
	for _, code := range []int{401, 429, 500} {
		if strings.Contains(said, fmt.Sprintf("#%d", code)) {
			http.Error(w, fmt.Sprintf(`{"type":"error","error":{"type":"api_error","message":"scripted %d"}}`, code), code)
			return
		}
	}
	delay := 25 * time.Millisecond
	if strings.Contains(said, "#slow") {
		delay = 150 * time.Millisecond
	}
	switch {
	case strings.Contains(said, "#refuse"):
		fm.Stream(w, req.Model, fm.Reply{{Text: ""}}, 6, delay, "refusal")
	case afterTool && strings.Contains(said, "#long"):
		fm.Stream(w, req.Model, fm.Reply{{Text: "That is a hundred lines."}}, 6, delay, "")
	case strings.Contains(said, "#long"):
		var b strings.Builder
		b.WriteString("proc hub \"Hub\"\n")
		for i := range 99 {
			fmt.Fprintf(&b, "hub -> hub : tick %d\n", i)
		}
		fm.Stream(w, req.Model, fm.Reply{{Text: "Drawing a long board. "}, draw(b.String())}, 24, 5*time.Millisecond, "")
	case strings.Contains(strings.ToLower(said), "undo") || strings.Contains(strings.ToLower(said), "balik"):
		if afterTool {
			fm.Stream(w, req.Model, fm.Reply{{Text: "Back to how it was."}}, 6, delay, "")
		} else {
			fm.Stream(w, req.Model, fm.Reply{{Tool: "undo", Input: "{}"}}, 6, delay, "")
		}
	default:
		t := script[(persons-1+len(script))%len(script)]
		if afterTool {
			fm.Stream(w, req.Model, fm.Reply{{Text: t.outro}}, 6, delay, "")
		} else {
			fm.Stream(w, req.Model, fm.Reply{{Text: t.intro}, draw(t.lines)}, 6, delay, "")
		}
	}
}

func draw(lines string) fm.Block {
	b, _ := json.Marshal(map[string]string{"lines": lines})
	return fm.Block{Tool: "draw", Input: string(b)}
}
