package kernel_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Matthew-Eucaristo/blockies/examples"
	"github.com/Matthew-Eucaristo/blockies/kernel"
	"github.com/Matthew-Eucaristo/blockies/lattice"
	"github.com/Matthew-Eucaristo/blockies/script"
)

func TestExamplesRunWithoutErrors(t *testing.T) {
	for _, name := range examples.Names() {
		src, _ := examples.Get(name)
		tl := kernel.Compile(src)
		if len(tl.Errors) > 0 {
			t.Errorf("%s: %v", name, tl.Errors)
		}
		if again := kernel.Compile(src); again.Hash() != tl.Hash() {
			t.Errorf("%s: two runs differ", name)
		}
		checkMoat(t, name, tl)
	}
}

// Golden hashes: a change here means the kernel's behaviour changed. Update
// them on purpose, never to make a test pass.
func TestGoldenHashes(t *testing.T) {
	want := map[string]string{
		"checkout": "860ae2249c80e17c",
		"backlog":  "502f97a94796ed80",
		"agent":    "6845b6d381576f44",
		"line":     "630ae3d18f1061aa",
	}
	for name, h := range want {
		src, _ := examples.Get(name)
		if got := kernel.Compile(src).Hash(); got != h {
			t.Errorf("%s: hash %s, want %s", name, got, h)
		}
	}
}

func TestSequenceTiming(t *testing.T) {
	tl := kernel.Compile("proc a\nproc b\nproc c\na -> b : one\n& a -> c : same time\nb -> a : two\nwait 3\n")
	if len(tl.Errors) > 0 {
		t.Fatal(tl.Errors)
	}
	got := [][2]int{}
	for _, m := range tl.Msgs {
		got = append(got, [2]int{m.Sent, m.Handled})
	}
	want := [][2]int{{0, 1}, {0, 1}, {1, 2}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("messages (sent, handled) = %v, want %v", got, want)
		}
	}
	if tl.End != 5 {
		t.Fatalf("end = %d, want 5", tl.End)
	}
}

func TestBacklogBecomesABody(t *testing.T) {
	src, _ := examples.Get("backlog")
	tl := kernel.Compile(src)
	db := -1
	for i, p := range tl.Procs {
		if p.ID == "db" {
			db = i
		}
	}
	biggest := 0
	for _, e := range tl.Events {
		if e.E == "body" && e.P == db {
			biggest = max(biggest, len(e.Cells))
		}
	}
	if biggest < 3 {
		t.Fatalf("db body peaked at %d cells, want a visible backlog", biggest)
	}
}

func TestRuntimeErrorsKeepTheRest(t *testing.T) {
	tl := kernel.Compile("proc a\nproc a\na -> ghost : hi\nproc x/y\n$from -> a : no\nproc b\na -> b : ok\n")
	codes := map[int]string{}
	for _, e := range tl.Errors {
		codes[e.Line] = e.Code
	}
	want := map[int]string{2: "dup_proc", 3: "unknown_proc", 4: "no_parent", 5: "bad_ref"}
	for line, code := range want {
		if codes[line] != code {
			t.Errorf("line %d: %q, want %q (all: %v)", line, codes[line], code, tl.Errors)
		}
	}
	if len(tl.Msgs) != 1 || tl.Msgs[0].Label != "ok" {
		t.Fatalf("valid message lost: %+v", tl.Msgs)
	}
}

func TestDeclaredStatesAreEnforced(t *testing.T) {
	tl := kernel.Compile("kind db states=idle,busy\nproc d kind=db\nd = busy\nd = sleepy\n")
	if len(tl.Errors) != 1 || tl.Errors[0].Code != "unknown_state" || tl.Errors[0].Line != 4 {
		t.Fatal(tl.Errors)
	}
}

func TestEndingAParentEndsItsChildren(t *testing.T) {
	tl := kernel.Compile("proc a\nproc a/b\nproc a/b/c\nwait 1\nend a\nwait 1\n")
	for _, p := range tl.Procs {
		if p.End != 1 {
			t.Fatalf("%s ended at %d, want 1", p.ID, p.End)
		}
	}
}

func TestEveryRankPlacesWithoutCoordinates(t *testing.T) {
	for _, world := range []string{"world 30", "world 12 9", "world 8 8 6", "world 5 5 4 3"} {
		src := world + "\nproc a\nproc b\nproc a/x\nproc a/y\nbody a 6\nbody b 4\nwait 1\n"
		tl := kernel.Compile(src)
		if len(tl.Errors) > 0 {
			t.Fatalf("%s: %v", world, tl.Errors)
		}
		checkMoat(t, world, tl)
	}
}

// checkMoat replays the timeline and checks that no body cell ever touches
// another process's core, and that no two things share a cell.
func checkMoat(t *testing.T, name string, tl *kernel.Timeline) {
	t.Helper()
	shape, err := lattice.New(tl.World...)
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[int][][]int{}
	for tick := 0; tick <= tl.End; tick++ {
		for _, e := range tl.Events {
			if e.T == tick && e.E == "body" {
				bodies[e.P] = e.Cells
			}
		}
		cores := map[lattice.Coord]int{}
		taken := map[lattice.Coord]int{}
		for i, p := range tl.Procs {
			if p.Start <= tick && (p.End == -1 || tick < p.End) {
				c := lattice.C(p.At...)
				cores[c] = i
				if _, dup := taken[c]; dup {
					t.Fatalf("%s: two things at %v", name, c)
				}
				taken[c] = i
			}
		}
		for owner, cells := range bodies {
			for _, cell := range cells {
				c := lattice.C(cell...)
				if _, dup := taken[c]; dup {
					t.Fatalf("%s tick %d: body of %d overlaps at %v", name, tick, owner, c)
				}
				taken[c] = owner
				for _, n := range shape.Neighbors(c) {
					if o, ok := cores[n]; ok && o != owner {
						t.Fatalf("%s tick %d: body of %s touches core of %s", name, tick, tl.Procs[owner].ID, tl.Procs[o].ID)
					}
				}
			}
		}
	}
}

// The example in the guide is what agents copy first, so it must run.
func TestGuideExampleRuns(t *testing.T) {
	_, rest, _ := strings.Cut(script.Guide, "## Example")
	_, code, _ := strings.Cut(rest, "```\n")
	code, _, _ = strings.Cut(code, "```")
	tl := kernel.Compile(code)
	if len(tl.Errors) > 0 || len(tl.Msgs) == 0 {
		t.Fatalf("guide example: %v", tl.Errors)
	}
}

func BenchmarkCompile(b *testing.B) {
	for _, name := range []string{"checkout", "backlog"} {
		src, _ := examples.Get(name)
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				kernel.Compile(src)
			}
		})
	}
}

func TestBudgetStopsRunawayRules(t *testing.T) {
	var b strings.Builder
	b.WriteString("proc a\n")
	rule := "every 1: a = x" + strings.Repeat("; a = y", 54)
	for range 40 {
		b.WriteString(rule + "\n")
	}
	b.WriteString(strings.Repeat("wait 499\n", 4))
	start := time.Now()
	tl := kernel.Compile(b.String())
	took := time.Since(start)
	if len(tl.Events) > kernel.MaxEvents {
		t.Fatalf("events = %d", len(tl.Events))
	}
	n := 0
	for _, e := range tl.Errors {
		if e.Code == "too_big" {
			n++
		}
	}
	if n != 1 || tl.End >= kernel.MaxTicks-1 {
		t.Fatalf("too_big errors = %d, end = %d, errors %v", n, tl.End, tl.Errors)
	}
	js, _ := json.Marshal(tl)
	if len(js) > 8<<20 || took > 500*time.Millisecond {
		t.Fatalf("frame %d bytes, compile %v", len(js), took)
	}
}

func TestTapsRunClickRules(t *testing.T) {
	tl := kernel.Compile(`proc tally "Tally"
proc vote/a "A"
`)
	if len(tl.Errors) == 0 || tl.Errors[0].Code != "no_parent" {
		t.Fatalf("want no_parent first, got %v", tl.Errors)
	}
	tl = kernel.Compile(`proc tally "Tally"
proc vote "Vote"
proc vote/a "A"
click vote/a: vote/a -> tally : vote; note $from "tapped"
on tally vote: add tally 1; body tally +2
focus tally
tap vote/a
& tap vote/a
tap vote/a
tap tally
`)
	if len(tl.Errors) != 1 || tl.Errors[0].Code != "no_click" || tl.Errors[0].Line != 10 {
		t.Fatalf("errors = %v", tl.Errors)
	}
	var taps, values, focus []kernel.Event
	for _, e := range tl.Events {
		switch e.E {
		case "tap":
			taps = append(taps, e)
		case "value":
			values = append(values, e)
		case "focus":
			focus = append(focus, e)
		}
	}
	if len(taps) != 3 || taps[0].T != taps[1].T || taps[2].T != taps[0].T+1 {
		t.Fatalf("taps = %+v", taps)
	}
	if len(values) != 3 || *values[2].Value != 3 {
		t.Fatalf("values = %+v", values)
	}
	if len(focus) != 1 || tl.Procs[focus[0].P].ID != "tally" || !tl.Procs[2].Tap || tl.Procs[0].Tap {
		t.Fatalf("focus %+v, tap flags %v %v", focus, tl.Procs[2].Tap, tl.Procs[0].Tap)
	}
	// Three votes made the tally's body 6 cells by the end.
	var cells int
	for _, e := range tl.Events {
		if e.E == "body" && tl.Procs[e.P].ID == "tally" {
			cells = len(e.Cells)
		}
	}
	if cells != 6 {
		t.Fatalf("tally body = %d cells", cells)
	}
}

func TestVoteExampleIsDeterministic(t *testing.T) {
	src, ok := examples.Get("vote")
	if !ok {
		t.Fatal("no vote example")
	}
	a, b := kernel.Compile(src), kernel.Compile(src)
	if a.Hash() != b.Hash() || len(a.Errors) != 0 {
		t.Fatalf("errors %v", a.Errors)
	}
}

func TestTapCountsRunTheRuleThatOften(t *testing.T) {
	tl := kernel.Compile("proc tally\nproc a\nproc b\nclick a: add tally 1\nclick b: add tally 10\ntap a 3 b 2\n")
	if len(tl.Errors) != 0 {
		t.Fatal(tl.Errors)
	}
	last := -1
	taps := 0
	for _, e := range tl.Events {
		if e.E == "value" {
			last = int(*e.Value)
		}
		if e.E == "tap" {
			taps += int(*e.Value)
		}
	}
	if last != 23 || taps != 5 || tl.End != 1 {
		t.Fatalf("tally %d, taps %d, end %d", last, taps, tl.End)
	}
}

func valueOf(tl *kernel.Timeline, id string) any {
	var last any
	for _, e := range tl.Events {
		if e.E == "value" && tl.Procs[e.P].ID == id {
			if e.Text != nil {
				last = *e.Text
			} else {
				last = *e.Value
			}
		}
	}
	return last
}

func TestValuesAndFormulas(t *testing.T) {
	tl := kernel.Compile(`proc harga "Harga" skin=input value 12000
proc jumlah "Jumlah" skin=slider min=1 max=100 value 3
proc diskon skin=slider min=0 max=50
proc nama skin=input value "Budi"
proc total "Total" skin=number
proc salam skin=text
calc total = harga * jumlah * (100 - diskon) / 100
calc salam = "Halo " + nama
set jumlah 5
`)
	if len(tl.Errors) != 0 {
		t.Fatal(tl.Errors)
	}
	if v := valueOf(tl, "total"); v != 60000.0 {
		t.Fatalf("total = %v", v)
	}
	if v := valueOf(tl, "salam"); v != "Halo Budi" {
		t.Fatalf("salam = %v", v)
	}
	if v := valueOf(tl, "diskon"); v != 0.0 {
		t.Fatalf("slider without a value starts at min: %v", v)
	}
	if tl.Procs[1].Skin != "slider" || *tl.Procs[1].Max != 100 {
		t.Fatalf("proc skin %+v", tl.Procs[1])
	}
}

func TestFormulaErrorsAndCycles(t *testing.T) {
	tl := kernel.Compile("proc a\nproc b\ncalc a = b + 1\ncalc b = a + 1\nproc c\nset c 1 / 0\n")
	codes := map[string]bool{}
	for _, e := range tl.Errors {
		codes[e.Code] = true
	}
	if !codes["cycle"] || !codes["div_zero"] {
		t.Fatalf("errors = %v", tl.Errors)
	}
}

func TestFormulasDriveTheRun(t *testing.T) {
	// A what-if: the database's capacity comes from a slider.
	src := func(capacity int) string {
		return fmt.Sprintf(`proc kapasitas skin=slider min=1 max=4 value %d
proc web
proc db
cap db kapasitas
every 1: web -> db : GET; web -> db : GET
wait 10
`, capacity)
	}
	waiting := func(tl *kernel.Timeline) int {
		n := 0
		for _, m := range tl.Msgs {
			if m.Handled == -1 {
				n++
			}
		}
		return n
	}
	slow, fast := kernel.Compile(src(1)), kernel.Compile(src(2))
	if len(slow.Errors)+len(fast.Errors) != 0 || waiting(slow) <= waiting(fast) {
		t.Fatalf("capacity 1 left %d waiting, capacity 2 left %d; errors %v %v", waiting(slow), waiting(fast), slow.Errors, fast.Errors)
	}
}

func TestMoveRelocatesABlock(t *testing.T) {
	tl := kernel.Compile("world 20 9\nproc a\nproc b\nbody a 2\nwait 1\nmove a 2 2\nwait 1\nmove a 14 4\n")
	var moves []kernel.Event
	codes := map[string]bool{}
	for _, e := range tl.Events {
		if e.E == "move" {
			moves = append(moves, e)
		}
	}
	for _, e := range tl.Errors {
		codes[e.Code] = true
	}
	if len(moves) != 1 || moves[0].Cells[0][0] != 2 {
		t.Fatalf("moves %+v errors %v", moves, tl.Errors)
	}
	// The second move lands next to b, which keeps its space.
	if !codes["no_room"] && !codes["bad_at"] {
		t.Fatalf("errors = %v", tl.Errors)
	}
}

func TestFormulaBudgetStopsTheRun(t *testing.T) {
	// Many formulas recomputed on every one of many taps: bounded, and fast.
	var b strings.Builder
	b.WriteString("proc n value 0\nclick n: add n 1\n")
	for i := range 40 {
		fmt.Fprintf(&b, "proc c%d\ncalc c%d = n + %d\n", i, i, i)
	}
	for range 20 {
		b.WriteString("tap n 10000\n")
	}
	start := time.Now()
	tl := kernel.Compile(b.String())
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	found := false
	for _, e := range tl.Errors {
		found = found || e.Code == "too_big"
	}
	if !found {
		t.Fatalf("errors = %v", tl.Errors)
	}
}

func TestFormulaMayNameABlockThatStartsLater(t *testing.T) {
	tl := kernel.Compile("proc total\ncalc total = price * 2\nwait 1\nproc price value 5\n")
	if len(tl.Errors) != 0 {
		t.Fatalf("errors = %v", tl.Errors)
	}
	tl = kernel.Compile("proc total\ncalc total = nope * 2\n")
	if len(tl.Errors) != 1 || tl.Errors[0].Code != "unknown_proc" || tl.Errors[0].Line != 2 {
		t.Fatalf("errors = %v", tl.Errors)
	}
}

func TestCycleNamesAFormulaInTheCycle(t *testing.T) {
	tl := kernel.Compile("proc x\nproc a\nproc b\ncalc x = 1\ncalc a = b + 1\ncalc b = a + 1\n")
	for _, e := range tl.Errors {
		if e.Code == "cycle" && e.Line != 5 && e.Line != 6 {
			t.Fatalf("cycle on line %d", e.Line)
		}
	}
}

func TestMoveKeepsTheMoat(t *testing.T) {
	// b's body grows around b; a may not land touching it.
	// b's core is at 15 4 and its body reaches 13 4.
	tl := kernel.Compile("world 20 9\nproc a\nproc b\nbody b 8\nwait 12\nmove a 12 4\nwait 1\nmove a 12 1\n")
	var moves []kernel.Event
	for _, e := range tl.Events {
		if e.E == "move" {
			moves = append(moves, e)
		}
	}
	if len(moves) != 1 || moves[0].Cells[0][1] != 1 || len(tl.Errors) != 1 || tl.Errors[0].Code != "no_room" {
		t.Fatalf("moves %+v errors %v", moves, tl.Errors)
	}
}
