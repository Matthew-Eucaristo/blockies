// Package kernel runs a board script and returns its timeline.
//
// The model is small on purpose:
//
//   - A process is a computer: exactly one block (its core) on the lattice.
//     Children (api/cache) are placed next to their parent.
//   - Processes share nothing. They talk only by messages, which take one tick
//     to arrive. A person clicking and an agent typing use the same door.
//   - A process may have a body: [d] cells the kernel grows through free space
//     around its core. Its size is what the script asks for plus the messages
//     waiting in its mailbox, so a backlog is visible as a shape.
//   - Agents say what, never where. The kernel places every core and body,
//     and bodies never touch another computer's core (the moat rule).
//   - Time is one more axis. The timeline holds every message and state
//     change, so a renderer can show the world, a sequence diagram or a
//     Gantt chart of the same run.
//
// Compile is pure and deterministic: no clock, no randomness, and maps are
// only used for lookups, never iterated into the output.
package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Matthew-Eucaristo/blockies/lattice"
	"github.com/Matthew-Eucaristo/blockies/script"
)

// Version of the timeline format. Bump it when the JSON changes shape.
const Version = 1

// Limits per board.
const (
	MaxTicks = 2000
	MaxProcs = 500
	MaxMsgs  = 20000
)

// Timeline is everything a renderer needs to show a run at any tick.
type Timeline struct {
	Version int            `json:"version"`
	World   []int          `json:"world"`
	End     int            `json:"end"` // last tick
	Kinds   []Kind         `json:"kinds"`
	Procs   []Proc         `json:"procs"`
	Msgs    []Msg          `json:"msgs"`
	Events  []Event        `json:"events"`
	Errors  []script.Error `json:"errors"`
}

// Kind is a declared kind of process.
type Kind struct {
	Name   string   `json:"name"`
	Color  string   `json:"color,omitempty"`
	States []string `json:"states,omitempty"`
}

// Proc is one process, from the tick it started to the tick it ended.
type Proc struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Kind   string `json:"kind,omitempty"`
	Color  string `json:"color,omitempty"`
	Parent string `json:"parent,omitempty"`
	At     []int  `json:"at"`
	Start  int    `json:"start"`
	End    int    `json:"end"` // -1 while it runs to the end of the timeline
	Line   int    `json:"line"`
}

// Msg is one message. Handled is -1 if it was never handled (its receiver
// ended, or the timeline stopped first).
type Msg struct {
	From    int    `json:"from"` // index into Procs
	To      int    `json:"to"`
	Label   string `json:"label"`
	Sent    int    `json:"sent"`
	Arrive  int    `json:"arrive"`
	Handled int    `json:"handled"`
	Line    int    `json:"line"`
}

// Event is a change to one process at one tick.
type Event struct {
	T     int     `json:"t"`
	E     string  `json:"e"` // state, note, body
	P     int     `json:"p"` // index into Procs
	State string  `json:"state,omitempty"`
	Text  string  `json:"text,omitempty"`
	Cells [][]int `json:"cells,omitempty"`
	Line  int     `json:"line,omitempty"` // script line, for state and note
}

// Hash is a stable fingerprint of the timeline, for determinism tests.
func (tl *Timeline) Hash() string {
	b, _ := json.Marshal(tl)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Summary is one line for agents and logs.
func (tl *Timeline) Summary() string {
	return fmt.Sprintf("%d processes, %d messages, %d ticks, %d errors", len(tl.Procs), len(tl.Msgs), tl.End+1, len(tl.Errors))
}

// Compile parses and runs a script.
func Compile(src string) *Timeline {
	s, errs := script.Parse(src)
	return Run(s, errs)
}

// Run runs a parsed script. Parse errors are carried into the timeline.
func Run(s *script.Script, parseErrs []script.Error) *Timeline {
	e := newEngine(s)
	e.errs = append(e.errs, parseErrs...)
	e.run()
	slices.SortStableFunc(e.errs, func(a, b script.Error) int { return a.Line - b.Line })
	e.tl.Errors = e.errs
	if e.tl.Errors == nil {
		e.tl.Errors = []script.Error{}
	}
	return e.tl
}

type proc struct {
	idx    int
	id     string
	kind   *Kind
	parent *proc
	core   lattice.Coord
	body   []lattice.Coord
	want   int
	cap    int
	inbox  []int
	alive  bool
}

type scheduled struct {
	t  int
	st script.Stmt
}

// A rule belongs to a process: an `on` rule to its receiver, an `every` rule
// to the first process its first action names. Ending that process stops
// the rule, and a process started again under the same id starts clean.
type rule struct {
	start int
	line  int
	st    script.Stmt
	owner *proc // nil: runs to the end
}

type engine struct {
	shape   lattice.Shape
	kinds   map[string]*Kind
	live    map[string]*proc
	order   []*proc
	cells   map[lattice.Coord]*proc // core or body
	cores   map[lattice.Coord]bool
	plan    []scheduled
	on      []rule
	every   []rule
	pending []int
	slots   map[string]int
	nslots  int
	tl      *Timeline
	errs    []script.Error
	seen    map[string]bool
}

func newEngine(s *script.Script) *engine {
	e := &engine{
		kinds: map[string]*Kind{},
		live:  map[string]*proc{},
		cells: map[lattice.Coord]*proc{},
		cores: map[lattice.Coord]bool{},
		slots: map[string]int{},
		seen:  map[string]bool{},
		tl:    &Timeline{Version: Version, Kinds: []Kind{}, Procs: []Proc{}, Msgs: []Msg{}, Events: []Event{}},
	}
	var world *script.Stmt
	cursor, prev := 0, 0
	for _, st := range s.Stmts {
		switch st.Op {
		case script.World:
			if world != nil {
				e.fail(st.Line, "dup_world", "the world is already set on line %d", world.Line)
				continue
			}
			world = &st
		case script.Kind:
			if _, ok := e.kinds[st.ID]; ok {
				e.fail(st.Line, "dup_kind", "kind %q is already declared", st.ID)
				continue
			}
			k := Kind{Name: st.ID, Color: st.Color, States: st.States}
			e.tl.Kinds = append(e.tl.Kinds, k)
			e.kinds[st.ID] = &e.tl.Kinds[len(e.tl.Kinds)-1]
		case script.Wait:
			if cursor+st.N > MaxTicks {
				e.fail(st.Line, "too_long", "a board lasts at most %d ticks", MaxTicks)
				continue
			}
			cursor += st.N
		case script.On, script.Every, script.Cap:
			e.plan = append(e.plan, scheduled{cursor, st})
		case script.Blank, script.Comment:
		default: // timed
			start := cursor
			if st.Par {
				start = prev
			}
			if st.Op == script.Proc && !strings.Contains(st.ID, "/") && len(st.Dims) == 0 {
				if _, ok := e.slots[st.ID]; !ok {
					e.slots[st.ID] = e.nslots
					e.nslots++
				}
			}
			if st.Op == script.Send {
				if start+1 > MaxTicks {
					e.fail(st.Line, "too_long", "a board lasts at most %d ticks", MaxTicks)
					continue
				}
				cursor = max(cursor, start+1)
			}
			e.plan = append(e.plan, scheduled{start, st})
			prev = start
		}
	}
	// Point at the final slice: appending may have moved it.
	e.kinds = map[string]*Kind{}
	for i := range e.tl.Kinds {
		e.kinds[e.tl.Kinds[i].Name] = &e.tl.Kinds[i]
	}
	slices.SortStableFunc(e.plan, func(a, b scheduled) int { return a.t - b.t })
	e.tl.End = cursor

	dims := []int{max(9, min(61, 4*e.nslots+1)), 15}
	if world != nil {
		dims = world.Dims
	}
	shape, err := lattice.New(dims...)
	if err != nil {
		e.fail(world.Line, "bad_world", "%v", err)
		shape, _ = lattice.New(13, 13)
	}
	e.shape = shape
	e.tl.World = shape.Dims()
	return e
}

func (e *engine) run() {
	next := 0
	for t := 0; t <= e.tl.End; t++ {
		for ; next < len(e.plan) && e.plan[next].t == t; next++ {
			e.exec(t, e.plan[next].st, e.plan[next].st.Line, nil)
		}
		for _, r := range e.every {
			if r.owner != nil && !r.owner.alive {
				continue
			}
			if t >= r.start && (t-r.start)%r.st.N == 0 {
				for _, a := range r.st.Actions {
					e.exec(t, a, r.line, nil)
				}
			}
		}
		e.deliver(t)
		e.handle(t)
		e.grow(t)
	}
}

func (e *engine) deliver(t int) {
	keep := e.pending[:0]
	for _, mi := range e.pending {
		m := &e.tl.Msgs[mi]
		if m.Arrive != t {
			keep = append(keep, mi)
			continue
		}
		if p := e.order[m.To]; p.alive {
			p.inbox = append(p.inbox, mi)
		}
	}
	e.pending = keep
}

func (e *engine) handle(t int) {
	for i := 0; i < len(e.order); i++ {
		p := e.order[i]
		for n := 0; p.alive && len(p.inbox) > 0 && (p.cap == 0 || n < p.cap); n++ {
			mi := p.inbox[0]
			p.inbox = p.inbox[1:]
			m := &e.tl.Msgs[mi]
			m.Handled = t
			from := e.order[m.From]
			first, _, _ := strings.Cut(m.Label, " ")
			for _, r := range e.on {
				if r.start > t || r.owner != p || !(r.st.Match == "*" || r.st.Match == first) {
					continue
				}
				for _, a := range r.st.Actions {
					e.exec(t, a, r.line, from)
				}
			}
		}
	}
}

func (e *engine) grow(t int) {
	for _, p := range e.order {
		if !p.alive {
			continue
		}
		want := min(script.MaxBody, p.want+len(p.inbox))
		changed := false
		for len(p.body) > want {
			delete(e.cells, p.body[len(p.body)-1])
			p.body = p.body[:len(p.body)-1]
			changed = true
		}
		for len(p.body) < want {
			c, ok := e.shape.NearestFree(append([]lattice.Coord{p.core}, p.body...), e.taken, func(c lattice.Coord) bool { return e.moat(c, p) })
			if !ok {
				break
			}
			e.cells[c] = p
			p.body = append(p.body, c)
			changed = true
		}
		if changed {
			cells := make([][]int, len(p.body))
			for i, c := range p.body {
				cells[i] = e.out(c)
			}
			e.tl.Events = append(e.tl.Events, Event{T: t, E: "body", P: p.idx, Cells: cells})
		}
	}
}

func (e *engine) taken(c lattice.Coord) bool { return e.cells[c] != nil }

// moat: a body cell may touch body cells and its own core, never another core.
func (e *engine) moat(c lattice.Coord, owner *proc) bool {
	for _, n := range e.shape.Neighbors(c) {
		if e.cores[n] && e.cells[n] != owner {
			return false
		}
	}
	return true
}

// exec runs one timed statement or registers a rule. from is the sender of
// the message being handled, inside an `on` rule.
func (e *engine) exec(t int, st script.Stmt, line int, from *proc) {
	ref := func(id string) (*proc, bool) {
		if id == script.From {
			if from == nil {
				e.fail(line, "bad_ref", "$from only works inside an on rule")
				return nil, false
			}
			if !from.alive {
				return nil, false
			}
			return from, true
		}
		p := e.live[id]
		if p == nil {
			e.fail(line, "unknown_proc", "no running process %q at tick %d", id, t)
			return nil, false
		}
		return p, true
	}
	switch st.Op {
	case script.On:
		if p, ok := ref(st.ID); ok {
			e.on = append(e.on, rule{t, line, st, p})
		}
	case script.Every:
		var owner *proc
		if a := st.Actions[0]; a.Op != script.Proc && a.ID != script.From {
			p, ok := ref(a.ID)
			if !ok {
				return
			}
			owner = p
		}
		e.every = append(e.every, rule{t, line, st, owner})
	case script.Proc:
		e.spawn(t, st, line)
	case script.End:
		if p, ok := ref(st.ID); ok {
			e.end(t, p)
		}
	case script.Send:
		a, ok1 := ref(st.ID)
		b, ok2 := ref(st.To)
		if !ok1 || !ok2 {
			return
		}
		if len(e.tl.Msgs) >= MaxMsgs {
			e.fail(line, "too_big", "a board holds at most %d messages", MaxMsgs)
			return
		}
		e.tl.Msgs = append(e.tl.Msgs, Msg{From: a.idx, To: b.idx, Label: st.Label, Sent: t, Arrive: t + 1, Handled: -1, Line: line})
		e.pending = append(e.pending, len(e.tl.Msgs)-1)
	case script.State:
		p, ok := ref(st.ID)
		if !ok {
			return
		}
		if p.kind != nil && len(p.kind.States) > 0 && !slices.Contains(p.kind.States, st.State) {
			e.fail(line, "unknown_state", "kind %s has states %s, not %q", p.kind.Name, strings.Join(p.kind.States, ", "), st.State)
			return
		}
		e.tl.Events = append(e.tl.Events, Event{T: t, E: "state", P: p.idx, State: st.State, Line: line})
	case script.Note:
		if p, ok := ref(st.ID); ok {
			e.tl.Events = append(e.tl.Events, Event{T: t, E: "note", P: p.idx, Text: st.Text, Line: line})
		}
	case script.Body:
		if p, ok := ref(st.ID); ok {
			p.want = st.N
		}
	case script.Cap:
		if p, ok := ref(st.ID); ok {
			p.cap = st.N
		}
	}
}

func (e *engine) spawn(t int, st script.Stmt, line int) {
	if _, ok := e.live[st.ID]; ok {
		e.fail(line, "dup_proc", "process %q is already running", st.ID)
		return
	}
	if len(e.order) >= MaxProcs {
		e.fail(line, "too_big", "a board holds at most %d processes", MaxProcs)
		return
	}
	var parent *proc
	if i := strings.LastIndex(st.ID, "/"); i >= 0 {
		parent = e.live[st.ID[:i]]
		if parent == nil {
			e.fail(line, "no_parent", "start %q before %q", st.ID[:i], st.ID)
			return
		}
	}
	kind := e.kinds[st.Kind]
	core, ok := e.place(st, parent, line)
	if !ok {
		return
	}
	p := &proc{idx: len(e.order), id: st.ID, kind: kind, parent: parent, core: core, alive: true}
	e.order = append(e.order, p)
	e.live[st.ID] = p
	e.cells[core] = p
	e.cores[core] = true

	label := st.Label
	if label == "" {
		label = st.ID[strings.LastIndex(st.ID, "/")+1:]
	}
	color := st.Color
	if color == "" && kind != nil {
		color = kind.Color
	}
	pp := Proc{ID: st.ID, Label: label, Kind: st.Kind, Color: color, At: e.out(core), Start: t, End: -1, Line: line}
	if parent != nil {
		pp.Parent = parent.id
	}
	e.tl.Procs = append(e.tl.Procs, pp)
}

func (e *engine) place(st script.Stmt, parent *proc, line int) (lattice.Coord, bool) {
	// Cores keep one free cell around them in every direction, diagonals
	// included, so labels and bodies have room.
	coreOK := func(c lattice.Coord) bool {
		for _, n := range e.shape.Around(c) {
			if e.cores[n] {
				return false
			}
		}
		return true
	}
	if len(st.Dims) > 0 {
		c := lattice.C(st.Dims...)
		if len(st.Dims) != e.shape.Rank() || !e.shape.Contains(c) {
			e.fail(line, "bad_at", "at %v is outside the world %v", st.Dims, e.shape.Dims())
			return c, false
		}
		if e.cells[c] != nil {
			e.fail(line, "bad_at", "at %v is already taken", st.Dims)
			return c, false
		}
		return c, true
	}
	var from lattice.Coord
	switch {
	case parent != nil:
		from = parent.core
	default:
		from = e.shape.Center()
		if slot, ok := e.slots[st.ID]; ok && e.nslots > 0 {
			w := e.shape.Dims()[0]
			from[0] = (2*slot + 1) * w / (2 * e.nslots)
		}
		if !e.taken(from) && coreOK(from) {
			return from, true
		}
	}
	c, ok := e.shape.NearestFree([]lattice.Coord{from}, e.taken, coreOK)
	if !ok {
		e.fail(line, "no_room", "the world %v has no free cell for %q", e.shape.Dims(), st.ID)
	}
	return c, ok
}

func (e *engine) end(t int, p *proc) {
	for _, q := range e.order {
		if q.alive && (q == p || strings.HasPrefix(q.id, p.id+"/")) {
			q.alive = false
			e.tl.Procs[q.idx].End = t
			delete(e.live, q.id)
			delete(e.cells, q.core)
			delete(e.cores, q.core)
			for _, c := range q.body {
				delete(e.cells, c)
			}
			if len(q.body) > 0 {
				e.tl.Events = append(e.tl.Events, Event{T: t, E: "body", P: q.idx})
			}
			q.body, q.inbox = nil, nil
		}
	}
}

func (e *engine) out(c lattice.Coord) []int { return append([]int(nil), c[:e.shape.Rank()]...) }

// fail records an error once per line and code, so a rule that fails every
// tick reports once.
func (e *engine) fail(line int, code, format string, args ...any) {
	key := fmt.Sprint(line, code)
	if e.seen[key] {
		return
	}
	e.seen[key] = true
	e.errs = append(e.errs, script.Error{Line: line, Code: code, Detail: fmt.Sprintf(format, args...)})
}
