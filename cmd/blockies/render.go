package main

import (
	"fmt"
	"strings"

	"github.com/Matthew-Eucaristo/blockies/kernel"
)

// render draws a timeline as text. A one-dimensional world is drawn with
// time going down, which makes it a sequence diagram; any other world is
// drawn at one tick (axes 0 and 1, layer 0 of axis 2).
func render(tl *kernel.Timeline, tick int) string {
	letters := make([]byte, len(tl.Procs))
	for i := range tl.Procs {
		letters[i] = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"[i%26]
	}
	var b strings.Builder
	if len(tl.World) == 1 {
		renderLine(&b, tl, letters)
	} else {
		if tick < 0 || tick > tl.End {
			tick = tl.End
		}
		renderGrid(&b, tl, letters, tick)
	}
	fmt.Fprintf(&b, "\n%s\n", tl.Summary())
	for _, e := range tl.Errors {
		fmt.Fprintf(&b, "line %d: %s: %s\n", e.Line, e.Code, e.Detail)
	}
	return b.String()
}

func renderLine(b *strings.Builder, tl *kernel.Timeline, letters []byte) {
	w := tl.World[0]
	for i, p := range tl.Procs {
		fmt.Fprintf(b, "%c %s\n", letters[i], p.Label)
	}
	b.WriteString("\n")
	for t := 0; t <= tl.End; t++ {
		row := []byte(strings.Repeat(" ", w))
		for i, p := range tl.Procs {
			if p.Start <= t && (p.End == -1 || t < p.End) {
				row[p.At[0]] = '|'
				if t == p.Start {
					row[p.At[0]] = letters[i]
				}
			}
		}
		var labels []string
		for _, m := range tl.Msgs {
			if m.Sent != t {
				continue
			}
			a, z := tl.Procs[m.From].At[0], tl.Procs[m.To].At[0]
			lo, hi := min(a, z), max(a, z)
			for x := lo + 1; x < hi; x++ {
				row[x] = '-'
			}
			if z > a {
				row[z-1] = '>'
			} else if z < a {
				row[z+1] = '<'
			}
			if m.Label != "" {
				labels = append(labels, m.Label)
			}
		}
		fmt.Fprintf(b, "%3d %s  %s\n", t, row, strings.Join(labels, ", "))
	}
}

func renderGrid(b *strings.Builder, tl *kernel.Timeline, letters []byte, tick int) {
	w, h := tl.World[0], tl.World[1]
	grid := make([][]byte, h)
	for y := range grid {
		grid[y] = []byte(strings.Repeat(".", w))
	}
	onLayer := func(c []int) bool { return len(c) < 3 || c[2] == 0 }
	state := make([]string, len(tl.Procs))
	note := make([]string, len(tl.Procs))
	body := make([][][]int, len(tl.Procs))
	for _, e := range tl.Events {
		if e.T > tick {
			continue
		}
		switch e.E {
		case "state":
			state[e.P] = e.State
		case "note":
			note[e.P] = e.Text
		case "body":
			body[e.P] = e.Cells
		}
	}
	var legend []string
	for i, p := range tl.Procs {
		if p.Start > tick || (p.End != -1 && tick >= p.End) {
			continue
		}
		for _, c := range body[i] {
			if onLayer(c) {
				grid[c[1]][c[0]] = letters[i] + 'a' - 'A'
			}
		}
		if onLayer(p.At) {
			grid[p.At[1]][p.At[0]] = letters[i]
		}
		line := fmt.Sprintf("%c %s", letters[i], p.Label)
		if state[i] != "" {
			line += " (" + state[i] + ")"
		}
		if note[i] != "" {
			line += " \"" + note[i] + "\""
		}
		legend = append(legend, line)
	}
	fmt.Fprintf(b, "tick %d of %d\n", tick, tl.End)
	for _, row := range grid {
		b.WriteString(strings.Join(strings.Split(string(row), ""), " "))
		b.WriteString("\n")
	}
	for _, m := range tl.Msgs {
		if m.Sent <= tick && tick < m.Arrive {
			legend = append(legend, fmt.Sprintf("  %s -> %s : %s", tl.Procs[m.From].ID, tl.Procs[m.To].ID, m.Label))
		}
	}
	b.WriteString(strings.Join(legend, "\n"))
	b.WriteString("\n")
}
