// Package script parses and formats board scripts: the small, line-based
// language agents use to describe what happens. A script says what, never
// where: the kernel places every process and grows every body.
//
// The parser never gives up on a whole script. A bad line is reported with a
// stable error code and skipped, and every valid line still loads. [Format]
// is canonical, and Parse(Format(s)) == s.
package script

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Op is the kind of a statement.
type Op string

// Statements. The vocabulary is closed on purpose: few choices give valid,
// predictable scripts.
const (
	Blank   Op = ""        // empty line
	Comment Op = "comment" // # text
	World   Op = "world"   // world 24 12
	Kind    Op = "kind"    // kind db color=green states=idle,busy,down
	Proc    Op = "proc"    // proc api "API gateway" kind=service at 4 6
	Send    Op = "send"    // api -> db : SELECT cart
	State   Op = "state"   // db = busy
	Note    Op = "note"    // note db "slow disk"
	Body    Op = "body"    // body db 4
	Wait    Op = "wait"    // wait 3
	End     Op = "end"     // end api/cache
	Cap     Op = "cap"     // cap db 1
	On      Op = "on"      // on db SELECT: db -> $from : rows
	Every   Op = "every"   // every 3: client -> api : GET
	Click   Op = "click"   // click vote/a: vote/a -> tally : A
	Tap     Op = "tap"     // tap vote/a 12 vote/b 5  (people tapped; written by the server)
	Add     Op = "add"     // add tally/a 1
	Focus   Op = "focus"   // focus results  (every screen looks there)
	Set     Op = "set"     // set total harga * jumlah
	Calc    Op = "calc"    // calc total = harga * jumlah  (kept up to date)
	Move    Op = "move"    // move api 4 6  (a person dragged it; written by the server)
)

// Skins say how a block looks and what people can do with it.
var Skins = []string{"box", "number", "text", "button", "slider", "input", "toggle", "bar"}

// Controls are skins people set by hand; their value is a starting value
// (proc ... value V), so changing it replays the whole board.
var Controls = []string{"slider", "input", "toggle"}

// From is the only variable: inside an `on` rule it names the sender of the
// message being handled, inside a `click` rule the process that was tapped.
const From = "$from"

// Limits that keep one board small enough to stream and render.
const (
	MaxLine  = 400
	MaxLabel = 80
	MaxBody  = 64
	MaxWait  = 500
	MaxTaps  = 10_000 // most taps of one block in one tap line
)

// Tapped is one block in a tap line and how many people tapped it.
type Tapped struct {
	ID string `json:"id"`
	N  int    `json:"n"`
}

// Colors is the fixed palette a kind or process may name. Renderers decide
// the actual shades.
var Colors = []string{"blue", "red", "green", "amber", "purple", "teal", "pink", "gray"}

// Stmt is one statement. Only the fields of its Op are set.
type Stmt struct {
	Line    int      `json:"line,omitempty"`    // 1-based source line (0 inside a rule)
	Op      Op       `json:"op"`                //
	Par     bool     `json:"par,omitempty"`     // "&": starts with the previous statement
	ID      string   `json:"id,omitempty"`      // proc, target of state/note/body/end/cap/on, kind name
	To      string   `json:"to,omitempty"`      // send target
	Label   string   `json:"label,omitempty"`   // proc label, message label
	Kind    string   `json:"kind,omitempty"`    // kind of a proc
	Color   string   `json:"color,omitempty"`   // palette name
	States  []string `json:"states,omitempty"`  // states a kind declares
	State   string   `json:"state,omitempty"`   // new state
	Text    string   `json:"text,omitempty"`    // note or comment
	Dims    []int    `json:"dims,omitempty"`    // world sizes, or a proc's fixed position
	N       int      `json:"n,omitempty"`       // body, wait, cap, every, add (may be negative)
	Rel     bool     `json:"rel,omitempty"`     // body +N / -N: change the size instead of setting it
	Taps    []Tapped `json:"taps,omitempty"`    // tap: blocks and counts, in order
	Skin    string   `json:"skin,omitempty"`    // proc: how it looks (see Skins)
	Min     *float64 `json:"min,omitempty"`     // proc: lowest value of a slider or bar
	Max     *float64 `json:"max,omitempty"`     // proc: highest value of a slider or bar
	Value   *Value   `json:"value,omitempty"`   // proc: starting value
	X       *Expr    `json:"x,omitempty"`       // set, calc, add, and cap/body with a formula
	Match   string   `json:"match,omitempty"`   // on: first word of the label, or "*"
	Actions []Stmt   `json:"actions,omitempty"` // on, every
}

// Error is a problem with one line. Codes are stable API for agents.
type Error struct {
	Line   int    `json:"line"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (e Error) Error() string { return fmt.Sprintf("line %d: %s: %s", e.Line, e.Code, e.Detail) }

// Script is a parsed board script, one statement per source line.
type Script struct {
	Stmts []Stmt `json:"stmts"`
}

// Parse reads a whole script. Every line becomes one statement; bad lines are
// returned as errors and left out.
func Parse(src string) (*Script, []Error) {
	s := &Script{}
	var errs []Error
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		st, err := ParseLine(raw)
		if err != nil {
			err.Line = i + 1
			errs = append(errs, *err)
			continue
		}
		st.Line = i + 1
		s.Stmts = append(s.Stmts, st)
	}
	// A trailing newline is not a statement.
	if n := len(s.Stmts); n > 0 && s.Stmts[n-1].Op == Blank && strings.HasSuffix(src, "\n") {
		s.Stmts = s.Stmts[:n-1]
	}
	return s, errs
}

// ParseLine reads one line. The returned error has Line 0.
func ParseLine(raw string) (Stmt, *Error) {
	if len(raw) > MaxLine {
		return Stmt{}, fail("too_long", "a line holds at most %d characters", MaxLine)
	}
	line := strings.TrimSpace(raw)
	if line == "" {
		return Stmt{Op: Blank}, nil
	}
	if strings.HasPrefix(line, "#") {
		return Stmt{Op: Comment, Text: strings.TrimSpace(line[1:])}, nil
	}
	return parseStmt(line, true)
}

func parseStmt(line string, top bool) (Stmt, *Error) {
	par := false
	if rest, ok := strings.CutPrefix(line, "&"); ok {
		par = true
		line = strings.TrimSpace(rest)
	}
	// Keywords come first; otherwise a line is a message (it has an arrow)
	// or a state change (it has an equals sign).
	word, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	op := Op(word)
	// A process may be named like a keyword (proc add, proc note): a line
	// that continues with -> or = is then a message or a state change.
	if strings.HasPrefix(rest, "->") || strings.HasPrefix(rest, "=") {
		op = ""
	}
	var st Stmt
	var err *Error
	switch op {
	case World:
		st, err = parseWorld(rest)
	case Kind:
		st, err = parseKind(rest)
	case Proc:
		st, err = parseProc(rest)
	case Note:
		st, err = parseNote(rest)
	case Body, Cap:
		st, err = parseIDNumber(Op(word), rest)
	case Wait:
		st, err = parseWait(rest)
	case End:
		st, err = parseEnd(rest)
	case On:
		st, err = parseOn(rest)
	case Every:
		st, err = parseEvery(rest)
	case Click:
		st, err = parseClick(rest)
	case Tap:
		st, err = parseTap(rest)
	case Focus:
		st, err = parseRef(Op(word), rest)
	case Add, Set:
		st, err = parseIDExpr(op, rest)
	case Calc:
		st, err = parseCalc(rest)
	case Move:
		st, err = parseMove(rest)
	default:
		if strings.Contains(line, "->") {
			st, err = parseSend(line)
		} else if id, state, ok := strings.Cut(line, "="); ok {
			st, err = parseState(strings.TrimSpace(id), strings.TrimSpace(state))
		} else {
			return Stmt{}, fail("syntax", "unknown statement %q (expected world, kind, proc, a -> b : label, a = state, note, body, set, add, calc, wait, end, cap, on, every, click, tap, focus, move)", word)
		}
	}
	if err != nil {
		return Stmt{}, err
	}
	if !top && (!timed(st.Op) || st.Op == Tap || st.Op == Move) {
		return Stmt{}, fail("bad_action", "%s cannot be used inside a rule", st.Op)
	}
	if par {
		if !timed(st.Op) {
			return Stmt{}, fail("syntax", "& only goes before something that happens in time")
		}
		st.Par = true
	}
	return st, nil
}

// timed reports whether a statement happens at a moment in time.
func timed(op Op) bool {
	switch op {
	case Proc, Send, State, Note, Body, End, Tap, Add, Focus, Set, Move:
		return true
	}
	return false
}

func parseWorld(rest string) (Stmt, *Error) {
	f := strings.Fields(rest)
	if len(f) < 1 || len(f) > 4 {
		return Stmt{}, fail("bad_world", "world takes 1 to 4 sizes, for example: world 24 12")
	}
	dims, err := ints(f)
	if err != nil {
		return Stmt{}, err
	}
	for _, d := range dims {
		if d < 1 || d > 256 {
			return Stmt{}, fail("bad_world", "each size must be 1 to 256")
		}
	}
	return Stmt{Op: World, Dims: dims}, nil
}

func parseKind(rest string) (Stmt, *Error) {
	f := strings.Fields(rest)
	if len(f) == 0 {
		return Stmt{}, fail("syntax", "kind needs a name, for example: kind db color=green states=idle,busy")
	}
	if !isName(f[0]) {
		return Stmt{}, fail("bad_id", "%q is not a valid kind name (letters, digits, _ and -)", f[0])
	}
	st := Stmt{Op: Kind, ID: f[0]}
	for _, kv := range f[1:] {
		k, v, ok := strings.Cut(kv, "=")
		switch {
		case ok && k == "color":
			if !slices.Contains(Colors, v) {
				return Stmt{}, fail("bad_color", "color must be one of %s", strings.Join(Colors, ", "))
			}
			st.Color = v
		case ok && k == "states":
			for s := range strings.SplitSeq(v, ",") {
				if !isName(s) {
					return Stmt{}, fail("bad_state", "%q is not a valid state name", s)
				}
				if slices.Contains(st.States, s) {
					return Stmt{}, fail("bad_state", "state %q is listed twice", s)
				}
				st.States = append(st.States, s)
			}
		default:
			return Stmt{}, fail("syntax", "kind takes color=... and states=a,b,c, not %q", kv)
		}
	}
	return st, nil
}

func parseProc(rest string) (Stmt, *Error) {
	toks, err := tokens(rest)
	if err != nil {
		return Stmt{}, err
	}
	if len(toks) == 0 {
		return Stmt{}, fail("syntax", "proc needs an id, for example: proc api \"API\" kind=service")
	}
	if !isID(toks[0].s) || toks[0].quoted {
		return Stmt{}, fail("bad_id", "%q is not a valid id (letters, digits, _ and -, with / for children)", toks[0].s)
	}
	st := Stmt{Op: Proc, ID: toks[0].s}
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t.quoted:
			if st.Label != "" {
				return Stmt{}, fail("syntax", "proc takes one label")
			}
			if len(t.s) > MaxLabel {
				return Stmt{}, fail("too_long", "a label holds at most %d characters", MaxLabel)
			}
			st.Label = t.s
		case strings.HasPrefix(t.s, "kind="):
			st.Kind = strings.TrimPrefix(t.s, "kind=")
			if !isName(st.Kind) {
				return Stmt{}, fail("bad_id", "%q is not a valid kind name", st.Kind)
			}
		case strings.HasPrefix(t.s, "color="):
			st.Color = strings.TrimPrefix(t.s, "color=")
			if !slices.Contains(Colors, st.Color) {
				return Stmt{}, fail("bad_color", "color must be one of %s", strings.Join(Colors, ", "))
			}
		case strings.HasPrefix(t.s, "skin="):
			st.Skin = strings.TrimPrefix(t.s, "skin=")
			if !slices.Contains(Skins, st.Skin) {
				return Stmt{}, fail("bad_skin", "skin must be one of %s", strings.Join(Skins, ", "))
			}
		case strings.HasPrefix(t.s, "min=") || strings.HasPrefix(t.s, "max="):
			f, err := strconv.ParseFloat(t.s[4:], 64)
			if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) > MaxNum {
				return Stmt{}, fail("bad_number", "%q is not a number up to %g", t.s[4:], MaxNum)
			}
			if t.s[:3] == "min" {
				st.Min = &f
			} else {
				st.Max = &f
			}
		case t.s == "value":
			if i+1 >= len(toks) {
				return Stmt{}, fail("syntax", "value takes a number or \"text\"")
			}
			i++
			v, err := literal(toks[i])
			if err != nil {
				return Stmt{}, err
			}
			st.Value = &v
		case t.s == "at":
			var nums []string
			for i+1 < len(toks) && !toks[i+1].quoted && isNumber(toks[i+1].s) {
				i++
				nums = append(nums, toks[i].s)
			}
			if len(nums) == 0 || len(nums) > 4 {
				return Stmt{}, fail("syntax", "at takes 1 to 4 numbers")
			}
			st.Dims, _ = ints(nums)
		default:
			return Stmt{}, fail("syntax", "proc takes \"label\", kind=..., color=..., skin=..., min=, max=, value V and at x y, not %q", t.s)
		}
	}
	return st, nil
}

func parseSend(line string) (Stmt, *Error) {
	head, label, _ := strings.Cut(line, ":")
	from, to, _ := strings.Cut(head, "->")
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	label = strings.TrimSpace(label)
	if !isRef(from) || !isRef(to) {
		return Stmt{}, fail("syntax", "a message is: from -> to : label")
	}
	if len(label) > MaxLabel {
		return Stmt{}, fail("too_long", "a label holds at most %d characters", MaxLabel)
	}
	return Stmt{Op: Send, ID: from, To: to, Label: label}, nil
}

func parseState(id, state string) (Stmt, *Error) {
	if !isRef(id) {
		return Stmt{}, fail("bad_id", "%q is not a valid id", id)
	}
	if !isName(state) {
		return Stmt{}, fail("bad_state", "a state is one word, for example: db = busy")
	}
	return Stmt{Op: State, ID: id, State: state}, nil
}

func parseNote(rest string) (Stmt, *Error) {
	id, text, _ := strings.Cut(rest, " ")
	if !isRef(id) {
		return Stmt{}, fail("bad_id", "note needs an id, for example: note db \"slow disk\"")
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, `"`) {
		toks, err := tokens(text)
		if err != nil {
			return Stmt{}, err
		}
		if len(toks) != 1 || !toks[0].quoted {
			return Stmt{}, fail("syntax", "note takes one quoted text")
		}
		text = toks[0].s
	}
	if len(text) > MaxLabel {
		return Stmt{}, fail("too_long", "a note holds at most %d characters", MaxLabel)
	}
	return Stmt{Op: Note, ID: id, Text: text}, nil
}

func parseIDNumber(op Op, rest string) (Stmt, *Error) {
	f := strings.Fields(rest)
	if len(f) >= 2 && isRef(f[0]) && !isNumber(f[1]) && len(f) > 1 && !(op == Body && len(f) == 2 && (strings.HasPrefix(f[1], "+") || strings.HasPrefix(f[1], "-")) && isNumber(f[1][1:])) {
		// A formula: cap db kapasitas, body tank level / 10.
		_, src, _ := strings.Cut(rest, " ")
		x, err := ParseExpr(src)
		if err != nil {
			return Stmt{}, err
		}
		return Stmt{Op: op, ID: f[0], X: x}, nil
	}
	if len(f) != 2 || !isRef(f[0]) {
		return Stmt{}, fail("syntax", "%s takes an id and a number or formula, for example: %s db 2", op, op)
	}
	rel := op == Body && (strings.HasPrefix(f[1], "+") || strings.HasPrefix(f[1], "-"))
	n, err := strconv.Atoi(f[1])
	if err != nil || n < 0 && !rel {
		return Stmt{}, fail("bad_number", "%q is not a whole number", f[1])
	}
	if op == Body && (n > MaxBody || n < -MaxBody) {
		return Stmt{}, fail("bad_number", "a body holds at most %d cells", MaxBody)
	}
	if rel {
		return Stmt{Op: op, ID: f[0], N: n, Rel: true}, nil
	}
	if op == Cap && n < 1 {
		return Stmt{}, fail("bad_number", "cap must be at least 1")
	}
	return Stmt{Op: op, ID: f[0], N: n}, nil
}

func parseWait(rest string) (Stmt, *Error) {
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || n < 1 || n > MaxWait {
		return Stmt{}, fail("bad_number", "wait takes a number of ticks, 1 to %d", MaxWait)
	}
	return Stmt{Op: Wait, N: n}, nil
}

func parseEnd(rest string) (Stmt, *Error) {
	id := strings.TrimSpace(rest)
	if !isRef(id) {
		return Stmt{}, fail("bad_id", "end needs an id, for example: end api/cache")
	}
	return Stmt{Op: End, ID: id}, nil
}

// parseRef reads statements that take only an id: tap, focus.
func parseRef(op Op, rest string) (Stmt, *Error) {
	id := strings.TrimSpace(rest)
	if !isRef(id) {
		return Stmt{}, fail("bad_id", "%s needs an id, for example: %s vote/a", op, op)
	}
	return Stmt{Op: op, ID: id}, nil
}

// parseTap reads tap ID [N] [ID [N]]...: people tapping blocks at one tick.
func parseTap(rest string) (Stmt, *Error) {
	f := strings.Fields(rest)
	if len(f) == 0 {
		return Stmt{}, fail("bad_id", "tap needs an id, for example: tap vote/a or tap vote/a 3 vote/b 2")
	}
	st := Stmt{Op: Tap}
	for i := 0; i < len(f); i++ {
		id := f[i]
		if !isID(id) {
			return Stmt{}, fail("bad_id", "%q is not a valid id", id)
		}
		n := 1
		if i+1 < len(f) && isNumber(f[i+1]) {
			n, _ = strconv.Atoi(f[i+1])
			if n < 1 || n > MaxTaps {
				return Stmt{}, fail("bad_number", "a tap count is 1 to %d", MaxTaps)
			}
			i++
		}
		st.Taps = append(st.Taps, Tapped{ID: id, N: n})
	}
	return st, nil
}

// parseIDExpr reads add ID formula and set ID formula.
func parseIDExpr(op Op, rest string) (Stmt, *Error) {
	id, src, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if !isRef(id) || strings.TrimSpace(src) == "" {
		return Stmt{}, fail("syntax", "%s takes an id and a number or formula, for example: %s total harga * jumlah", op, op)
	}
	x, err := ParseExpr(src)
	if err != nil {
		return Stmt{}, err
	}
	return Stmt{Op: op, ID: id, X: x}, nil
}

func parseCalc(rest string) (Stmt, *Error) {
	id, src, ok := strings.Cut(rest, "=")
	id = strings.TrimSpace(id)
	if !ok || !isID(id) {
		return Stmt{}, fail("syntax", "a formula is: calc total = harga * jumlah")
	}
	x, err := ParseExpr(src)
	if err != nil {
		return Stmt{}, err
	}
	return Stmt{Op: Calc, ID: id, X: x}, nil
}

func parseMove(rest string) (Stmt, *Error) {
	f := strings.Fields(rest)
	if len(f) < 2 || len(f) > 5 || !isID(f[0]) {
		return Stmt{}, fail("syntax", "move takes an id and 1 to 4 numbers, for example: move api 4 6")
	}
	dims, err := ints(f[1:])
	if err != nil {
		return Stmt{}, err
	}
	return Stmt{Op: Move, ID: f[0], Dims: dims}, nil
}

// literal reads a number or a quoted text.
func literal(t token) (Value, *Error) {
	if t.quoted {
		if utf8.RuneCountInString(t.s) > MaxLabel {
			return Value{}, fail("too_long", "a text value holds at most %d characters", MaxLabel)
		}
		return Txt(t.s), nil
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(t.s, "_", ""), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) > MaxNum {
		return Value{}, fail("bad_number", "%q is not a number", t.s)
	}
	return Num(f), nil
}

func parseClick(rest string) (Stmt, *Error) {
	head, body, ok := strings.Cut(rest, ":")
	id := strings.TrimSpace(head)
	if !ok || !isID(id) {
		return Stmt{}, fail("syntax", "a click rule is: click vote/a: vote/a -> tally : A")
	}
	acts, err := parseActions(body)
	if err != nil {
		return Stmt{}, err
	}
	return Stmt{Op: Click, ID: id, Actions: acts}, nil
}

func parseOn(rest string) (Stmt, *Error) {
	head, body, ok := strings.Cut(rest, ":")
	f := strings.Fields(head)
	if !ok || len(f) != 2 || !isID(f[0]) || !(f[1] == "*" || isWord(f[1])) {
		return Stmt{}, fail("syntax", "a rule is: on db SELECT: db -> $from : rows (the word matches the first word of a message, * matches any)")
	}
	acts, err := parseActions(body)
	if err != nil {
		return Stmt{}, err
	}
	return Stmt{Op: On, ID: f[0], Match: f[1], Actions: acts}, nil
}

func parseEvery(rest string) (Stmt, *Error) {
	head, body, ok := strings.Cut(rest, ":")
	n, err := strconv.Atoi(strings.TrimSpace(head))
	if !ok || err != nil || n < 1 || n > MaxWait {
		return Stmt{}, fail("syntax", "a timer is: every 3: client -> api : GET")
	}
	acts, perr := parseActions(body)
	if perr != nil {
		return Stmt{}, perr
	}
	return Stmt{Op: Every, N: n, Actions: acts}, nil
}

func parseActions(body string) ([]Stmt, *Error) {
	var acts []Stmt
	for part := range strings.SplitSeq(body, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		st, err := parseStmt(part, false)
		if err != nil {
			return nil, err
		}
		if st.Par {
			return nil, fail("bad_action", "& has no meaning inside a rule")
		}
		acts = append(acts, st)
	}
	if len(acts) == 0 {
		return nil, fail("bad_action", "a rule needs at least one action after the colon")
	}
	return acts, nil
}

// Format writes the canonical form of a script. Parse(Format(s)) == s.
func Format(s *Script) string {
	var b strings.Builder
	for _, st := range s.Stmts {
		b.WriteString(FormatStmt(st))
		b.WriteByte('\n')
	}
	return b.String()
}

// FormatStmt writes one statement in canonical form.
func FormatStmt(st Stmt) string {
	var out string
	switch st.Op {
	case Blank:
		return ""
	case Comment:
		return strings.TrimSpace("# " + st.Text)
	case World:
		out = "world " + joinInts(st.Dims)
	case Kind:
		out = "kind " + st.ID
		if st.Color != "" {
			out += " color=" + st.Color
		}
		if len(st.States) > 0 {
			out += " states=" + strings.Join(st.States, ",")
		}
	case Proc:
		out = "proc " + st.ID
		if st.Label != "" {
			out += " " + quote(st.Label)
		}
		if st.Kind != "" {
			out += " kind=" + st.Kind
		}
		if st.Color != "" {
			out += " color=" + st.Color
		}
		if st.Skin != "" {
			out += " skin=" + st.Skin
		}
		if st.Min != nil {
			out += " min=" + FormatNum(*st.Min)
		}
		if st.Max != nil {
			out += " max=" + FormatNum(*st.Max)
		}
		if st.Value != nil {
			out += " value " + st.Value.String()
		}
		if len(st.Dims) > 0 {
			out += " at " + joinInts(st.Dims)
		}
	case Send:
		out = st.ID + " -> " + st.To
		if st.Label != "" {
			out += " : " + st.Label
		}
	case State:
		out = st.ID + " = " + st.State
	case Note:
		out = "note " + st.ID + " " + quote(st.Text)
	case Body, Cap:
		if st.X != nil {
			out = fmt.Sprintf("%s %s %s", st.Op, st.ID, st.X)
		} else if st.Rel {
			out = fmt.Sprintf("%s %s %+d", st.Op, st.ID, st.N)
		} else {
			out = fmt.Sprintf("%s %s %d", st.Op, st.ID, st.N)
		}
	case Add, Set:
		out = fmt.Sprintf("%s %s %s", st.Op, st.ID, st.X)
	case Calc:
		out = fmt.Sprintf("calc %s = %s", st.ID, st.X)
	case Move:
		out = "move " + st.ID + " " + joinInts(st.Dims)
	case Focus:
		out = "focus " + st.ID
	case Tap:
		parts := []string{"tap"}
		for _, t := range st.Taps {
			parts = append(parts, t.ID)
			if t.N != 1 {
				parts = append(parts, strconv.Itoa(t.N))
			}
		}
		out = strings.Join(parts, " ")
	case Click:
		out = fmt.Sprintf("click %s: %s", st.ID, formatActions(st.Actions))
	case Wait:
		out = fmt.Sprintf("wait %d", st.N)
	case End:
		out = "end " + st.ID
	case On:
		out = fmt.Sprintf("on %s %s: %s", st.ID, st.Match, formatActions(st.Actions))
	case Every:
		out = fmt.Sprintf("every %d: %s", st.N, formatActions(st.Actions))
	}
	if st.Par {
		out = "& " + out
	}
	return out
}

func formatActions(acts []Stmt) string {
	parts := make([]string, len(acts))
	for i, a := range acts {
		parts[i] = FormatStmt(a)
	}
	return strings.Join(parts, "; ")
}

// IsID reports whether s is a valid process id: names joined by /, for
// example api or eng/api/login.
func IsID(s string) bool { return isID(s) }

// Valid ids are paths of names: api, api/cache, eng/api/login.
func isID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for part := range strings.SplitSeq(s, "/") {
		if !isName(part) {
			return false
		}
	}
	return true
}

// isRef is an id, or $from inside a rule.
func isRef(s string) bool { return s == From || isID(s) }

func isName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (i == 0 || !(r >= '0' && r <= '9' || r == '_' || r == '-')) {
			return false
		}
	}
	return true
}

func isWord(s string) bool { return s != "" && !strings.ContainsAny(s, " :;\"") }

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func ints(f []string) ([]int, *Error) {
	out := make([]int, len(f))
	for i, s := range f {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fail("bad_number", "%q is not a whole number", s)
		}
		out[i] = n
	}
	return out, nil
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, " ")
}

type token struct {
	s      string
	quoted bool
}

// tokens splits on spaces, keeping "double quoted" text (with \" and \\) whole.
// Control characters other than tab are refused, so a line stays one line.
func tokens(s string) ([]token, *Error) {
	for _, r := range s {
		if r != '\t' && unicode.IsControl(r) {
			return nil, fail("syntax", "a line cannot hold control characters")
		}
	}
	var out []token
	for i := 0; i < len(s); {
		switch s[i] {
		case ' ', '\t':
			i++
		case '"':
			var b strings.Builder
			i++
			for {
				if i >= len(s) {
					return nil, fail("syntax", "a quote is not closed")
				}
				if s[i] == '\\' && i+1 < len(s) {
					b.WriteByte(s[i+1])
					i += 2
					continue
				}
				if s[i] == '"' {
					i++
					break
				}
				b.WriteByte(s[i])
				i++
			}
			out = append(out, token{b.String(), true})
		default:
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' {
				j++
			}
			out = append(out, token{s[i:j], false})
			i = j
		}
	}
	return out, nil
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}
