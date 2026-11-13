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

// Limits on what the service keeps in memory.
const (
	maxCached      = 256  // boards kept compiled; idle ones beyond this are dropped
	maxSubscribers = 1024 // live streams across all boards
)

// Service holds boards in a Store and fans changes out to viewers.
//
// Each board has its own lock, so compiling one board never stalls another.
// The service lock only guards the map of cached boards.
type Service struct {
	store Store
	base  string

	mu     sync.Mutex
	boards map[string]*board
	revs   map[string]int // last rev of boards dropped from the cache, so revs never go back
	nsubs  int
}

type board struct {
	mu     sync.Mutex
	loaded bool
	gone   bool // dropped from the cache; holders must look the board up again
	rev    int
	src    string
	tl     *kernel.Timeline
	frame  []byte
	subs   map[chan []byte]struct{}
}

// NewService serves boards from store. base is the public URL of the viewer
// (for example http://127.0.0.1:4318), used to build board links.
func NewService(store Store, base string) *Service {
	return &Service{store: store, base: strings.TrimRight(base, "/"), boards: map[string]*board{}, revs: map[string]int{}}
}

// URL is the viewer link for a board.
func (s *Service) URL(id string) string { return s.base + "/b/" + id }

func (s *Service) Write(ctx context.Context, id, src string) (Result, error) {
	if err := check(id, src); err != nil {
		return Result{}, err
	}
	b, err := s.acquire(ctx, id, true)
	if err != nil {
		return Result{}, err
	}
	defer b.mu.Unlock()
	if err := s.commit(ctx, id, b, src); err != nil {
		return Result{}, err
	}
	return s.result(id, b, nil), nil
}

func (s *Service) Append(ctx context.Context, id, lines string) (Result, error) {
	if err := check(id, lines); err != nil {
		return Result{}, err
	}
	b, err := s.acquire(ctx, id, true)
	if err != nil {
		return Result{}, err
	}
	defer b.mu.Unlock()
	base := b.src
	if base != "" && !strings.HasSuffix(base, "\n") {
		base += "\n"
	}
	first := strings.Count(base, "\n") + 1 // line number of the first new line
	added := strings.Split(strings.TrimRight(lines, "\n"), "\n")
	if len(base)+len(lines) > MaxScript {
		return Result{}, &Error{"too_big", fmt.Sprintf("a board script holds at most %d bytes", MaxScript)}
	}

	// New lines that make an existing line fail (for example a rule that
	// now reaches a size budget) are rejected together: no single new line
	// is to blame, and keeping some of them would leave a broken board.
	if e, ok := newOldError(b.tl, kernel.Compile(base+strings.Join(added, "\n")+"\n"), first); ok {
		rejected := []script.Error{{Line: first, Code: e.Code, Detail: fmt.Sprintf("these lines make line %d fail: %s", e.Line, e.Detail)}}
		return s.result(id, b, rejected), nil
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
	// The retries above give up after a few rounds. If what is left still
	// fails (for example several new rules that each reach a size budget),
	// nothing is added: lines with errors are never kept.
	if src != b.src {
		final := kernel.Compile(src)
		for _, e := range final.Errors {
			if e.Line >= first {
				return s.result(id, b, []script.Error{{Line: first, Code: e.Code, Detail: fmt.Sprintf("these lines fail together (line %d: %s); nothing was added", e.Line, e.Detail)}}), nil
			}
		}
		if e, ok := newOldError(b.tl, final, first); ok {
			return s.result(id, b, []script.Error{{Line: first, Code: e.Code, Detail: fmt.Sprintf("these lines make line %d fail: %s", e.Line, e.Detail)}}), nil
		}
	}
	if src != b.src {
		if err := s.commit(ctx, id, b, src); err != nil {
			return Result{}, err
		}
	}
	return s.result(id, b, rejected), nil
}

// newOldError returns an error on a line before first that after has and
// before does not.
func newOldError(before, after *kernel.Timeline, first int) (script.Error, bool) {
	had := map[string]bool{}
	for _, e := range before.Errors {
		had[fmt.Sprint(e.Line, e.Code)] = true
	}
	for _, e := range after.Errors {
		if e.Line < first && !had[fmt.Sprint(e.Line, e.Code)] {
			return e, true
		}
	}
	return script.Error{}, false
}

func (s *Service) Read(ctx context.Context, id string) (Result, error) {
	if !ValidID(id) {
		return Result{}, badID(id)
	}
	b, err := s.acquire(ctx, id, false)
	if err != nil {
		return Result{}, err
	}
	defer b.mu.Unlock()
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
	for _, id := range ids {
		b, err := s.acquire(ctx, id, false)
		if err != nil {
			continue
		}
		out = append(out, s.result(id, b, nil))
		b.mu.Unlock()
	}
	return out, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if !ValidID(id) {
		return badID(id)
	}
	b, err := s.acquire(ctx, id, true)
	if err != nil {
		return err
	}
	defer b.mu.Unlock()
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	for ch := range b.subs {
		close(ch)
		delete(b.subs, ch)
		s.nsubs--
	}
	s.drop(id, b)
	delete(s.revs, id)
	s.mu.Unlock()
	return nil
}

// Proc returns the latest process with this id on a board, if it is still
// running at the end of the timeline. Like Update, it is an in-process
// helper for the server's own adapters, not part of API.
func (s *Service) Proc(ctx context.Context, id, proc string) (kernel.Proc, bool, error) {
	if !ValidID(id) {
		return kernel.Proc{}, false, badID(id)
	}
	b, err := s.acquire(ctx, id, false)
	if err != nil {
		return kernel.Proc{}, false, err
	}
	defer b.mu.Unlock()
	for i := len(b.tl.Procs) - 1; i >= 0; i-- {
		if p := b.tl.Procs[i]; p.ID == proc {
			return p, p.End == -1, nil
		}
	}
	return kernel.Proc{}, false, nil
}

// Update changes a board's script with fn while holding the board, so no
// other change lands between reading and writing. It is an in-process
// helper (the server uses it for control values), not part of API.
func (s *Service) Update(ctx context.Context, id string, fn func(src string) (string, error)) (Result, error) {
	if !ValidID(id) {
		return Result{}, badID(id)
	}
	b, err := s.acquire(ctx, id, false)
	if err != nil {
		return Result{}, err
	}
	defer b.mu.Unlock()
	src, err := fn(b.src)
	if err != nil {
		return Result{}, err
	}
	if err := check(id, src); err != nil {
		return Result{}, err
	}
	if src != b.src {
		if err := s.commit(ctx, id, b, src); err != nil {
			return Result{}, err
		}
	}
	return s.result(id, b, nil), nil
}

// Frame returns the current frame of a board (JSON).
func (s *Service) Frame(ctx context.Context, id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, badID(id)
	}
	b, err := s.acquire(ctx, id, false)
	if err != nil {
		return nil, err
	}
	defer b.mu.Unlock()
	return b.frame, nil
}

// Subscribe returns a channel that receives the board's current frame now
// and the latest frame after every change. Slow viewers skip to the latest
// frame instead of slowing the board. The channel closes when the board is
// deleted; call cancel when done. Watching a board that does not exist yet
// is allowed: the first write shows up. At most 1024 streams are open at
// once (error code busy).
func (s *Service) Subscribe(ctx context.Context, id string) (<-chan []byte, func(), error) {
	if !ValidID(id) {
		return nil, nil, badID(id)
	}
	b, err := s.acquire(ctx, id, true)
	if err != nil {
		return nil, nil, err
	}
	defer b.mu.Unlock()
	s.mu.Lock()
	if s.nsubs >= maxSubscribers {
		s.mu.Unlock()
		return nil, nil, &Error{"busy", "too many live viewers; try again later"}
	}
	s.nsubs++
	s.mu.Unlock()
	ch := make(chan []byte, 1)
	ch <- b.frame
	b.subs[ch] = struct{}{}
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
			s.mu.Lock()
			s.nsubs--
			s.mu.Unlock()
		}
	}
	return ch, cancel, nil
}

// acquire returns the board locked, loading it from the store if needed.
// With create, a missing board is an empty one; otherwise it is not_found.
// Lock order: a board lock may be held while taking the service lock; the
// service lock never waits for a board lock (eviction only tries).
func (s *Service) acquire(ctx context.Context, id string, create bool) (*board, error) {
	for {
		s.mu.Lock()
		b := s.boards[id]
		if b == nil {
			b = &board{subs: map[chan []byte]struct{}{}}
			s.boards[id] = b
			s.evict(id)
		}
		s.mu.Unlock()

		b.mu.Lock()
		if b.gone {
			b.mu.Unlock()
			continue
		}
		if b.loaded {
			return b, nil
		}
		src, err := s.store.Get(ctx, id)
		switch {
		case errors.Is(err, ErrNotFound) && create:
			src = ""
		case err != nil:
			s.mu.Lock()
			s.drop(id, b)
			s.mu.Unlock()
			b.mu.Unlock()
			if errors.Is(err, ErrNotFound) {
				return nil, &Error{"not_found", fmt.Sprintf("no board %q", id)}
			}
			return nil, err
		}
		s.mu.Lock()
		b.rev = s.revs[id]
		s.mu.Unlock()
		b.set(id, src)
		b.loaded = true
		return b, nil
	}
}

// evict drops idle boards (no viewers, not in use) while the cache is over
// its limit. Callers hold s.mu.
func (s *Service) evict(keep string) {
	for id, b := range s.boards {
		if len(s.boards) <= maxCached {
			return
		}
		if id == keep || !b.mu.TryLock() {
			continue
		}
		if len(b.subs) == 0 {
			s.drop(id, b)
		}
		b.mu.Unlock()
	}
}

// drop removes b from the cache. Callers hold s.mu and b.mu.
func (s *Service) drop(id string, b *board) {
	b.gone = true
	if b.loaded && b.rev > 0 { // boards only watched, never written, leave nothing behind
		s.revs[id] = b.rev
	}
	if s.boards[id] == b {
		delete(s.boards, id)
	}
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
