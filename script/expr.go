package script

import (
	"math"
	"strconv"
	"strings"
)

// Expr is a small formula: numbers, "text", process ids (meaning their
// value), $from, + - * / %, comparisons, and, or, not, parentheses, and the
// functions min, max, round, floor, ceil, abs, clamp and if. Put spaces
// around - and / (a-b and a/b are ids).
type Expr struct {
	src  string
	root *node
}

// Value is a number or a text. A process without a value reads as 0.
type Value struct {
	Num   float64
	Text  string
	IsTxt bool
}

// Num makes a number value.
func Num(f float64) Value { return Value{Num: f} }

// Txt makes a text value.
func Txt(s string) Value { return Value{Text: s, IsTxt: true} }

func (v Value) String() string {
	if v.IsTxt {
		return quote(v.Text)
	}
	return FormatNum(v.Num)
}

// MaxNum bounds every number in a board: bigger ones are errors, never
// infinities or NaN, so every run stays well defined on every machine.
const MaxNum = 1e15

// FormatNum writes a number in scripts: the shortest decimal that reads
// back as the same number, without an exponent.
func FormatNum(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// finite checks a computed number.
func finite(f float64) (Value, *Error) {
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > MaxNum {
		return Value{}, fail("bad_number", "a number went past %g", MaxNum)
	}
	return Num(f), nil
}

// ParseExpr reads a formula.
func ParseExpr(src string) (*Expr, *Error) {
	p := &exprParser{s: strings.TrimSpace(src)}
	if p.s == "" {
		return nil, fail("bad_expr", "a formula is empty")
	}
	n, err := p.or()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i < len(p.s) {
		return nil, fail("bad_expr", "unexpected %q in %q", p.s[p.i:], p.s)
	}
	return &Expr{src: p.s, root: n}, nil
}

// String is the formula as written (trimmed). Parse(String()) gives the same formula.
func (e *Expr) String() string {
	if e == nil {
		return ""
	}
	return e.src
}

// MarshalJSON writes the formula as its text.
func (e *Expr) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(e.String())), nil }

// Refs lists the process ids the formula reads, in order of appearance.
func (e *Expr) Refs() []string {
	var out []string
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		if n.op == "ref" && n.name != From {
			out = append(out, n.name)
		}
		for _, k := range n.kids {
			walk(k)
		}
	}
	walk(e.root)
	return out
}

// Eval computes the formula. get returns a process's value (ok false: no
// such process). A failure is an error with a stable code.
func (e *Expr) Eval(get func(id string) (Value, bool)) (Value, *Error) {
	return eval(e.root, get)
}

type node struct {
	op   string // num, txt, ref, neg, not, call, or a binary operator
	num  float64
	txt  string
	name string // ref id, or function name
	kids []*node
}

type exprParser struct {
	s string
	i int
}

func (p *exprParser) space() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *exprParser) take(op string) bool {
	p.space()
	if !strings.HasPrefix(p.s[p.i:], op) {
		return false
	}
	// Words must stand alone: "and" is not the start of "android".
	if isLetter(op[0]) {
		end := p.i + len(op)
		if end < len(p.s) && isIDChar(p.s[end]) {
			return false
		}
	}
	p.i += len(op)
	return true
}

func (p *exprParser) binary(next func() (*node, *Error), ops ...string) (*node, *Error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for {
		matched := ""
		for _, op := range ops {
			save := p.i
			if p.take(op) {
				// "<" must not eat the start of "<=", and so on.
				if (op == "<" || op == ">" || op == "=") && p.i < len(p.s) && p.s[p.i] == '=' {
					p.i = save
					continue
				}
				matched = op
				break
			}
		}
		if matched == "" {
			return left, nil
		}
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = &node{op: matched, kids: []*node{left, right}}
	}
}

func (p *exprParser) or() (*node, *Error)  { return p.binary(p.and, "or") }
func (p *exprParser) and() (*node, *Error) { return p.binary(p.not, "and") }

// not binds looser than comparisons: not x > 3 is not (x > 3).
func (p *exprParser) not() (*node, *Error) {
	if p.take("not") {
		n, err := p.not()
		if err != nil {
			return nil, err
		}
		return &node{op: "not", kids: []*node{n}}, nil
	}
	return p.cmp()
}
func (p *exprParser) cmp() (*node, *Error) {
	return p.binary(p.sum, "<=", ">=", "==", "!=", "<", ">")
}
func (p *exprParser) sum() (*node, *Error)  { return p.binary(p.prod, "+", "-") }
func (p *exprParser) prod() (*node, *Error) { return p.binary(p.unary, "*", "/", "%") }

func (p *exprParser) unary() (*node, *Error) {
	if p.take("-") {
		n, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &node{op: "neg", kids: []*node{n}}, nil
	}
	return p.atom()
}

var funcs = map[string][2]int{ // name -> min, max arguments (-1: any)
	"min": {1, -1}, "max": {1, -1}, "round": {1, 2}, "floor": {1, 1}, "ceil": {1, 1},
	"abs": {1, 1}, "clamp": {3, 3}, "if": {3, 3},
}

func (p *exprParser) atom() (*node, *Error) {
	p.space()
	if p.i >= len(p.s) {
		return nil, fail("bad_expr", "%q ends too early", p.s)
	}
	c := p.s[p.i]
	switch {
	case c == '(':
		p.i++
		n, err := p.or()
		if err != nil {
			return nil, err
		}
		if !p.take(")") {
			return nil, fail("bad_expr", "a ( is not closed in %q", p.s)
		}
		return n, nil
	case c == '"':
		toks, err := tokens(p.s[p.i:])
		if err != nil || len(toks) == 0 || !toks[0].quoted {
			return nil, fail("bad_expr", "a quote is not closed in %q", p.s)
		}
		// Find where the quoted text ends in the source.
		j := p.i + 1
		for j < len(p.s) && p.s[j] != '"' {
			if p.s[j] == '\\' {
				j++
			}
			j++
		}
		p.i = j + 1
		return &node{op: "txt", txt: toks[0].s}, nil
	case c >= '0' && c <= '9' || c == '.':
		j := p.i
		for j < len(p.s) && (p.s[j] >= '0' && p.s[j] <= '9' || p.s[j] == '.' || p.s[j] == '_') {
			j++
		}
		// An exponent: 1e5, 2.5E-3.
		if j < len(p.s) && (p.s[j] == 'e' || p.s[j] == 'E') {
			k := j + 1
			if k < len(p.s) && (p.s[k] == '+' || p.s[k] == '-') {
				k++
			}
			if k < len(p.s) && p.s[k] >= '0' && p.s[k] <= '9' {
				for k < len(p.s) && p.s[k] >= '0' && p.s[k] <= '9' {
					k++
				}
				j = k
			}
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(p.s[p.i:j], "_", ""), 64)
		if err != nil || math.IsInf(f, 0) || math.Abs(f) > MaxNum {
			return nil, fail("bad_number", "%q is not a number up to %g", p.s[p.i:j], MaxNum)
		}
		p.i = j
		return &node{op: "num", num: f}, nil
	case c == '$':
		if strings.HasPrefix(p.s[p.i:], From) {
			p.i += len(From)
			return &node{op: "ref", name: From}, nil
		}
	case isLetter(c):
		j := p.i
		for j < len(p.s) {
			ch := p.s[j]
			if isLetter(ch) || ch >= '0' && ch <= '9' || ch == '_' {
				j++
				continue
			}
			// - and / continue an id only when a letter follows (a-b, api/db).
			if (ch == '-' || ch == '/') && j+1 < len(p.s) && isLetter(p.s[j+1]) {
				j++
				continue
			}
			break
		}
		name := p.s[p.i:j]
		p.i = j
		p.space()
		if p.i < len(p.s) && p.s[p.i] == '(' {
			arity, ok := funcs[name]
			if !ok {
				return nil, fail("bad_expr", "%s is not a function (min, max, round, floor, ceil, abs, clamp, if)", name)
			}
			p.i++
			var args []*node
			if !p.take(")") {
				for {
					a, err := p.or()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.take(")") {
						break
					}
					if !p.take(",") {
						return nil, fail("bad_expr", "expected , or ) in %q", p.s)
					}
				}
			}
			if len(args) < arity[0] || arity[1] >= 0 && len(args) > arity[1] {
				return nil, fail("bad_expr", "%s takes %d to %d values", name, arity[0], arity[1])
			}
			return &node{op: "call", name: name, kids: args}, nil
		}
		if !isID(name) {
			return nil, fail("bad_expr", "%q is not a process id", name)
		}
		return &node{op: "ref", name: name}, nil
	}
	return nil, fail("bad_expr", "unexpected %q in %q", string(c), p.s)
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIDChar(c byte) bool {
	return isLetter(c) || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '/'
}

func truth(v Value) bool {
	if v.IsTxt {
		return v.Text != ""
	}
	return v.Num != 0
}

func boolNum(b bool) Value {
	if b {
		return Num(1)
	}
	return Num(0)
}

func eval(n *node, get func(string) (Value, bool)) (Value, *Error) {
	v, err := evalRaw(n, get)
	if err == nil && !v.IsTxt {
		return finite(v.Num)
	}
	return v, err
}

func evalRaw(n *node, get func(string) (Value, bool)) (Value, *Error) {
	switch n.op {
	case "num":
		return Num(n.num), nil
	case "txt":
		return Txt(n.txt), nil
	case "ref":
		v, ok := get(n.name)
		if !ok {
			return Value{}, fail("unknown_proc", "no running process %q in a formula", n.name)
		}
		return v, nil
	case "call":
		if n.name == "if" {
			c, err := eval(n.kids[0], get)
			if err != nil {
				return Value{}, err
			}
			if truth(c) {
				return eval(n.kids[1], get)
			}
			return eval(n.kids[2], get)
		}
		args := make([]float64, len(n.kids))
		for i, k := range n.kids {
			v, err := eval(k, get)
			if err != nil {
				return Value{}, err
			}
			if v.IsTxt {
				return Value{}, fail("bad_expr", "%s needs numbers", n.name)
			}
			args[i] = v.Num
		}
		switch n.name {
		case "min", "max":
			r := args[0]
			for _, a := range args[1:] {
				if n.name == "min" {
					r = math.Min(r, a)
				} else {
					r = math.Max(r, a)
				}
			}
			return Num(r), nil
		case "round":
			if len(args) == 2 {
				p := math.Pow(10, math.Round(args[1]))
				return Num(math.Round(args[0]*p) / p), nil
			}
			return Num(math.Round(args[0])), nil
		case "floor":
			return Num(math.Floor(args[0])), nil
		case "ceil":
			return Num(math.Ceil(args[0])), nil
		case "abs":
			return Num(math.Abs(args[0])), nil
		case "clamp":
			return Num(math.Max(args[1], math.Min(args[2], args[0]))), nil
		}
	case "neg", "not":
		v, err := eval(n.kids[0], get)
		if err != nil {
			return Value{}, err
		}
		if n.op == "not" {
			return boolNum(!truth(v)), nil
		}
		if v.IsTxt {
			return Value{}, fail("bad_expr", "cannot negate text")
		}
		return Num(-v.Num), nil
	}
	a, err := eval(n.kids[0], get)
	if err != nil {
		return Value{}, err
	}
	// and/or look at the right side only when needed.
	switch n.op {
	case "and":
		if !truth(a) {
			return Num(0), nil
		}
		b, err := eval(n.kids[1], get)
		if err != nil {
			return Value{}, err
		}
		return boolNum(truth(b)), nil
	case "or":
		if truth(a) {
			return Num(1), nil
		}
		b, err := eval(n.kids[1], get)
		if err != nil {
			return Value{}, err
		}
		return boolNum(truth(b)), nil
	}
	b, err := eval(n.kids[1], get)
	if err != nil {
		return Value{}, err
	}
	if n.op == "==" || n.op == "!=" {
		eq := a.IsTxt == b.IsTxt && a.Text == b.Text && a.Num == b.Num
		return boolNum(eq == (n.op == "==")), nil
	}
	if n.op == "+" && (a.IsTxt || b.IsTxt) {
		s := Txt(plain(a) + plain(b))
		if len(s.Text) > MaxLabel*4 {
			return Value{}, fail("too_long", "a text value holds at most %d characters", MaxLabel*4)
		}
		return s, nil
	}
	if a.IsTxt || b.IsTxt {
		return Value{}, fail("bad_expr", "%s needs numbers", n.op)
	}
	x, y := a.Num, b.Num
	var r float64
	switch n.op {
	case "+":
		r = x + y
	case "-":
		r = x - y
	case "*":
		r = x * y
	case "/", "%":
		if y == 0 {
			return Value{}, fail("div_zero", "division by zero")
		}
		if n.op == "/" {
			r = x / y
		} else {
			r = math.Mod(x, y)
		}
	case "<":
		return boolNum(x < y), nil
	case "<=":
		return boolNum(x <= y), nil
	case ">":
		return boolNum(x > y), nil
	case ">=":
		return boolNum(x >= y), nil
	default:
		return Value{}, fail("bad_expr", "unknown operator %s", n.op)
	}
	return finite(r)
}

// plain is a value as text, without quotes.
func plain(v Value) string {
	if v.IsTxt {
		return v.Text
	}
	return FormatNum(v.Num)
}
