package server

import (
	"strings"
	"testing"

	"github.com/Matthew-Eucaristo/blockies/script"
)

func TestSetStartRewritesTheLiveControlOnly(t *testing.T) {
	// Line 3 tries to start a second a while the first still runs: it is
	// refused, so the control people see is still the one on line 1.
	src := "proc a skin=slider value 1\nwait 1\nproc a skin=slider value 5\n"
	got := setStart(src, map[string]script.Value{"a": script.Num(9)})
	want := "proc a skin=slider value 9\nwait 1\nproc a skin=slider value 5\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetStartKeepsALineThatWouldNotReadBack(t *testing.T) {
	label := strings.Repeat("x", script.MaxLabel)
	src := "proc a \"" + label + "\" skin=input\n"
	got := setStart(src, map[string]script.Value{"a": script.Txt(strings.Repeat("😀", script.MaxLabel))})
	if got != src {
		t.Fatalf("a line past %d bytes was written: %q", script.MaxLine, got)
	}
}
