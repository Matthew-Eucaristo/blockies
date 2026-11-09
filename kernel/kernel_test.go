package kernel_test

import (
	"strings"
	"testing"

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
		"checkout": "3f7c199c3031923a",
		"backlog":  "da73d4adb0c373c0",
		"agent":    "e2979399c17cc288",
		"line":     "8d53c6946f8856d6",
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
