// Package boards is the one place where boards change. The HTTP API, the MCP
// server and the CLI are thin adapters over [API]: there is no second path.
//
// A board is its script. Every change replaces or extends the script, the
// kernel compiles it into a timeline, and every viewer receives the new
// timeline. Compiling is pure and fast, so nothing ticks on the server.
package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Matthew-Eucaristo/blockies/kernel"
	"github.com/Matthew-Eucaristo/blockies/script"
)

// MaxScript bounds one board's script.
const MaxScript = 256 << 10

// API is what agents and people can do with boards. [Service] implements it
// in process; the client package implements it over HTTP.
type API interface {
	// Write replaces a board's whole script, creating the board if needed.
	Write(ctx context.Context, id, src string) (Result, error)
	// Append adds lines to the end of a board, creating it if needed. Lines
	// with errors are not added; they come back in Result.Rejected.
	Append(ctx context.Context, id, lines string) (Result, error)
	// Read returns a board's script and its errors.
	Read(ctx context.Context, id string) (Result, error)
	// List returns every board.
	List(ctx context.Context) ([]Result, error)
	// Delete removes a board.
	Delete(ctx context.Context, id string) error
}

// Result describes a board after a call. Line numbers count from the top of
// the whole script.
type Result struct {
	Board    string         `json:"board"`
	Rev      int            `json:"rev"`
	URL      string         `json:"url"`
	Summary  string         `json:"summary"`
	Errors   []script.Error `json:"errors"`
	Rejected []script.Error `json:"rejected,omitempty"`
	Script   string         `json:"script,omitempty"`
}

// Error is an API error with a stable code: bad_board, not_found, too_big.
type Error struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// ValidID reports whether id can name a board: 1 to 64 of a-z, 0-9, - and _.
func ValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Frame is what viewers receive whenever a board changes.
type Frame struct {
	Board    string           `json:"board"`
	Rev      int              `json:"rev"`
	Script   string           `json:"script"`
	Timeline *kernel.Timeline `json:"timeline"`
}

// Service holds boards in a Store and fans changes out to viewers.
type Service struct {
	store Store
	base  string

	mu     sync.Mutex
	boards map[string]*board
}

type board struct {
	rev   int
	src   string
	tl    *kernel.Timeline
	frame []byte
	subs  map[chan []byte]struct{}
}

// NewService serves boards from store. base is the public URL of the viewer
// (for example http://127.0.0.1:4318), used to build board links.
func NewService(store Store, base string) *Service {
	return &Service{store: store, base: strings.TrimRight(base, "/"), boards: map[string]*board{}}
}

// URL is the viewer link for a board.
func (s *Service) URL(id string) string { return s.base + "/b/" + id }

func (s *Service) Write(ctx context.Context, id, src string) (Result, error) {
	if err := check(id, src); err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load(ctx, id, true)
	if err != nil {
		return Result{}, err
	}
	if err := s.commit(ctx, id, b, src); err != nil {
		return Result{}, err
	}
	return s.result(id, b, nil), nil
}

func (s *Service) Append(ctx context.Context, id, lines string) (Result, error) {
	if err := check(id, lines); err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load(ctx, id, true)
	if err != nil {
		return Result{}, err
	}
	base := b.src
	if base != "" && !strings.HasSuffix(base, "\n") {
		base += "\n"
	}
	first := strings.Count(base, "\n") + 1 // line number of the first new line
	added := strings.Split(strings.TrimRight(lines, "\n"), "\n")
	if len(base)+len(lines) > MaxScript {
		return Result{}, &Error{"too_big", fmt.Sprintf("a board script holds at most %d bytes", MaxScript)}
	}

	// Drop new lines that fail, then try again: removing a line can only
	// remove errors that depended on it.
	var rejected []script.Error
	keep := make([]bool, len(added))
	for i := range keep {
		keep[i] = true
	}
	for range 4 {
		var kept []string
		lineOf := []int{} // kept index -> index in added
		for i, l := range added {
			if keep[i] {
				kept = append(kept, l)
				lineOf = append(lineOf, i)
			}
		}
		tl := kernel.Compile(base + strings.Join(kept, "\n") + "\n")
		bad := false
		for _, e := range tl.Errors {
			if k := e.Line - first; k >= 0 && k < len(lineOf) {
				i := lineOf[k]
				keep[i] = false
				e.Line = first + i
				rejected = append(rejected, e)
				bad = true
			}
		}
		if !bad {
			break
		}
	}
	var kept []string
	for i, l := range added {
		if keep[i] {
			kept = append(kept, l)
		}
	}
	src := base
	if len(kept) > 0 {
		src += strings.Join(kept, "\n") + "\n"
	}
	if src != b.src {
		if err := s.commit(ctx, id, b, src); err != nil {
			return Result{}, err
		}
	}
	return s.result(id, b, rejected), nil
}

func (s *Service) Read(ctx context.Context, id string) (Result, error) {
	if !ValidID(id) {
		return Result{}, badID(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load(ctx, id, false)
	if err != nil {
		return Result{}, err
	}
	r := s.result(id, b, nil)
	r.Script = b.src
	return r, nil
}

func (s *Service) List(ctx context.Context) ([]Result, error) {
	ids, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(ids))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		b, err := s.load(ctx, id, false)
		if err != nil {
			continue
		}
		out = append(out, s.result(id, b, nil))
	}
	return out, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if !ValidID(id) {
		return badID(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	if b := s.boards[id]; b != nil {
		for ch := range b.subs {
			close(ch)
			delete(b.subs, ch)
		}
		delete(s.boards, id)
	}
	return nil
}

// Frame returns the current frame of a board (JSON).
func (s *Service) Frame(ctx context.Context, id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, badID(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return b.frame, nil
}

// Subscribe returns a channel that receives the board's current frame now
// and the latest frame after every change. Slow viewers skip to the latest
// frame instead of slowing the board. The channel closes when the board is
// deleted; call cancel when done. Watching a board that does not exist yet
// is allowed: the first write shows up.
func (s *Service) Subscribe(ctx context.Context, id string) (<-chan []byte, func(), error) {
	if !ValidID(id) {
		return nil, nil, badID(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load(ctx, id, true)
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan []byte, 1)
	ch <- b.frame
	b.subs[ch] = struct{}{}
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
	}
	return ch, cancel, nil
}

// load returns a cached board, reading it from the store if needed. With
// create, a missing board is an empty one; otherwise it is not_found.
func (s *Service) load(ctx context.Context, id string, create bool) (*board, error) {
	if b := s.boards[id]; b != nil {
		return b, nil
	}
	src, err := s.store.Get(ctx, id)
	switch {
	case errors.Is(err, ErrNotFound) && create:
		src = ""
	case errors.Is(err, ErrNotFound):
		return nil, &Error{"not_found", fmt.Sprintf("no board %q", id)}
	case err != nil:
		return nil, err
	}
	b := &board{subs: map[chan []byte]struct{}{}}
	b.set(id, src)
	s.boards[id] = b
	return b, nil
}

func (s *Service) commit(ctx context.Context, id string, b *board, src string) error {
	if err := s.store.Put(ctx, id, src); err != nil {
		return err
	}
	b.rev++
	b.set(id, src)
	for ch := range b.subs {
		select {
		case <-ch: // drop the stale frame: viewers only need the latest
		default:
		}
		ch <- b.frame
	}
	return nil
}

func (b *board) set(id, src string) {
	b.src = src
	b.tl = kernel.Compile(src)
	b.frame, _ = json.Marshal(Frame{Board: id, Rev: b.rev, Script: src, Timeline: b.tl})
}

func (s *Service) result(id string, b *board, rejected []script.Error) Result {
	return Result{Board: id, Rev: b.rev, URL: s.URL(id), Summary: b.tl.Summary(), Errors: b.tl.Errors, Rejected: rejected}
}

func check(id, src string) error {
	if !ValidID(id) {
		return badID(id)
	}
	if len(src) > MaxScript {
		return &Error{"too_big", fmt.Sprintf("a board script holds at most %d bytes", MaxScript)}
	}
	return nil
}

func badID(id string) error {
	return &Error{"bad_board", fmt.Sprintf("%q is not a board name: use 1 to 64 of a-z, 0-9, - and _", id)}
}
