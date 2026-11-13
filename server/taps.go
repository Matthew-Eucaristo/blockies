package server

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Matthew-Eucaristo/blockies/boards"
	"github.com/Matthew-Eucaristo/blockies/kernel"
	"github.com/Matthew-Eucaristo/blockies/script"
)

// People's input is collected per board for a short window and written in
// one go:
//
//   - Controls (slider, input, toggle) are starting values: the last value
//     set in the window replaces `value` on the block's proc line, and the
//     whole board replays as if it had always been so (a what-if).
//   - Taps are events: one line per window, `tap vote/a 12 vote/b 5`,
//     appended at the end, one tick per window.
//
// Both go through boards.Service, the one place boards change.
const (
	tapWindow = 400 * time.Millisecond
	tapEvery  = 120 * time.Millisecond // per client and board, for taps
	maxPeers  = 10000                  // rate-limit entries kept before starting over
)

type tapper struct {
	svc *boards.Service
	log *slog.Logger

	mu      sync.Mutex
	pending map[string]*window
	peers   map[string]time.Time // board + client -> last tap
}

type window struct {
	taps   map[string]int          // block -> taps
	values map[string]script.Value // control -> last value
}

func newTapper(svc *boards.Service, log *slog.Logger) *tapper {
	return &tapper{svc: svc, log: log, pending: map[string]*window{}, peers: map[string]time.Time{}}
}

// open returns the board's window, starting one. Callers hold t.mu.
func (t *tapper) open(board string) *window {
	w := t.pending[board]
	if w == nil {
		w = &window{taps: map[string]int{}, values: map[string]script.Value{}}
		t.pending[board] = w
		time.AfterFunc(tapWindow, func() { t.flush(board) })
	}
	return w
}

// tap queues one tap. It returns false when the client taps too fast.
func (t *tapper) tap(board, id, peer string) bool {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.peers) > maxPeers {
		t.peers = map[string]time.Time{}
	}
	key := board + " " + peer
	if now.Sub(t.peers[key]) < tapEvery {
		return false
	}
	t.peers[key] = now
	t.open(board).taps[id]++
	return true
}

// set queues a control's new value; the last one in a window wins.
func (t *tapper) set(board, id string, v script.Value) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.open(board).values[id] = v
}

func (t *tapper) flush(board string) {
	t.mu.Lock()
	w := t.pending[board]
	delete(t.pending, board)
	t.mu.Unlock()
	if w == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if len(w.values) > 0 {
		if _, err := t.svc.Update(ctx, board, func(src string) (string, error) { return setStart(src, w.values), nil }); err != nil {
			t.log.Warn("control values not written", "board", board, "err", err)
		}
	}
	if len(w.taps) == 0 {
		return
	}
	ids := make([]string, 0, len(w.taps))
	for id := range w.taps {
		ids = append(ids, id)
	}
	slices.Sort(ids) // the same taps always make the same line
	parts := []string{"tap"}
	for _, id := range ids {
		parts = append(parts, id)
		if w.taps[id] != 1 {
			parts = append(parts, strconv.Itoa(w.taps[id]))
		}
	}
	res, err := t.svc.Append(ctx, board, strings.Join(parts, " "))
	switch {
	case err != nil:
		t.log.Warn("taps not written", "board", board, "err", err)
	case len(res.Rejected) > 0:
		t.log.Warn("taps refused", "board", board, "code", res.Rejected[0].Code, "detail", res.Rejected[0].Detail)
	}
}

// setStart rewrites the proc line of each control that is running at the
// end of the board with its new starting value. Other lines stay exactly as
// they were, and a line that would no longer read back stays as it was.
func setStart(src string, values map[string]script.Value) string {
	s, _ := script.Parse(src)
	byLine := map[int]script.Stmt{}
	for _, st := range s.Stmts {
		if st.Op == script.Proc {
			byLine[st.Line] = st
		}
	}
	live := map[string]int{} // id -> line of the proc that runs at the end
	for _, p := range kernel.Run(s, nil).Procs {
		if p.End == -1 {
			live[p.ID] = p.Line
		}
	}
	lines := strings.Split(src, "\n")
	for id, v := range values {
		st, ok := byLine[live[id]]
		if !ok || st.Line < 1 || st.Line > len(lines) {
			continue
		}
		st.Value = &v
		line := script.FormatStmt(st)
		if back, err := script.ParseLine(line); err != nil || len(line) > script.MaxLine || back.Value == nil || *back.Value != v {
			continue
		}
		lines[st.Line-1] = line
	}
	return strings.Join(lines, "\n")
}
