// Spike: can a Bubble Tea v2 TUI animate ~2,000 [d] blocks driven by a [c] program,
// using half-block pixels (two pixels per terminal cell)? Measures sim time, tick rate
// and bytes written to the terminal.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/color"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/harmonica"
)

/* ---------- colors ---------- */

type rgb [3]float64

func hex(s string) rgb {
	v, _ := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	return rgb{float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v & 255)}
}

func mix(a, b rgb, t float64) rgb {
	t = math.Max(0, math.Min(1, t))
	return rgb{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

var (
	bg     = hex("#101013")
	ink    = hex("#e8e6df")
	muted  = hex("#7d7a73")
	accent = hex("#6f8bff")
)

/* ---------- [c] programs: params -> cells ---------- */

type param struct {
	name           string
	min, max, step float64
	val            float64
}

type cellT struct {
	x, y int
	c    rgb
}

type canvas struct {
	cells map[string]cellT
	order []string
}

func (cv *canvas) fill(x, y int, c rgb, id string) {
	if id == "" {
		id = strconv.Itoa(x) + "," + strconv.Itoa(y)
	}
	if _, ok := cv.cells[id]; !ok {
		cv.order = append(cv.order, id)
	}
	cv.cells[id] = cellT{x, y, c}
}

type bar struct{ h, id int }

type memory struct {
	bars         []bar
	i, j         int
	last, doneAt float64
}

type program struct {
	name   string
	params []*param
	run    func(p []*param, t float64, m *memory, cv *canvas)
}

func iabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func programs() []*program {
	bronze, gold, antenna := hex("#b8742f"), hex("#f4d49a"), hex("#ffcf6e")
	red, green, grey := hex("#ff6b6f"), hex("#33c58a"), hex("#8b90a8")
	blue, teal := hex("#4f7cff"), hex("#3fe0c5")
	return []*program{
		{
			name:   "eiffel",
			params: []*param{{"height", 16, 72, 1, 44}, {"spread", 0.6, 1.6, 0.05, 1}},
			run: func(p []*param, _ float64, _ *memory, cv *canvas) {
				H, S := int(p[0].val), p[1].val
				deck1, deck2 := int(math.Round(float64(H)*0.16)), int(math.Round(float64(H)*0.42))
				for y := 0; y < H; y++ {
					t := float64(y) / float64(H)
					w := int(math.Round(math.Pow(1-t, 2.3)*20*S)) + 1
					arch := 0.0
					if y < deck1 {
						arch = float64(w) * 0.62 * math.Sqrt(1-float64(y)/float64(deck1))
					}
					for x := -w; x <= w; x++ {
						if math.Abs(float64(x)) < arch {
							continue
						}
						edge := iabs(x) >= w-1
						lattice := (x+y)%4 == 0 || (x-y)%4 == 0
						if edge || lattice || y == deck1 || y == deck2 {
							cv.fill(x, y, mix(bronze, gold, t), "")
						}
					}
				}
				for j := 0; j < 4; j++ {
					cv.fill(0, H+j, antenna, "")
				}
			},
		},
		{
			name:   "sort",
			params: []*param{{"bars", 8, 36, 1, 22}, {"speed", 1, 30, 1, 10}},
			run: func(p []*param, t float64, m *memory, cv *canvas) {
				n, speed := int(p[0].val), p[1].val
				if len(m.bars) != n {
					m.bars = make([]bar, n)
					for k := range m.bars {
						m.bars[k] = bar{3 + rand.IntN(38), k}
					}
					m.i, m.j, m.last, m.doneAt = 0, 0, t, 0
				}
				a := m.bars
				m.last = math.Max(m.last, t-1)
				for m.i < n-1 && t-m.last > 1/speed {
					m.last += 1 / speed
					if a[m.j].h > a[m.j+1].h {
						a[m.j], a[m.j+1] = a[m.j+1], a[m.j]
					}
					m.j++
					if m.j >= n-1-m.i {
						m.j, m.i = 0, m.i+1
					}
				}
				if m.i >= n-1 {
					if m.doneAt == 0 {
						m.doneAt = t
					}
					if t-m.doneAt > 2 {
						m.bars = nil
						return
					}
				}
				w := max(1, 76/n-1)
				for k, b := range a {
					c := grey
					if m.i < n-1 && (k == m.j || k == m.j+1) {
						c = red
					} else if k >= n-m.i {
						c = green
					}
					x0 := k*(w+1) - n*(w+1)/2
					for i := 0; i < w; i++ {
						for j := 0; j < b.h; j++ {
							cv.fill(x0+i, j, c, "bar"+strconv.Itoa(b.id)+":"+strconv.Itoa(i)+","+strconv.Itoa(j))
						}
					}
				}
			},
		},
		{
			name:   "wave",
			params: []*param{{"amplitude", 2, 22, 1, 12}, {"frequency", 1, 8, 0.25, 3}, {"trails", 1, 6, 1, 4}},
			run: func(p []*param, t float64, _ *memory, cv *canvas) {
				A, F, T := p[0].val, p[1].val, int(p[2].val)
				for x := -40; x <= 40; x++ {
					for k := 0; k < T; k++ {
						y := int(math.Round(28 + A*math.Sin(float64(x)*F/25+t*2-float64(k)*0.3)))
						cv.fill(x, y, mix(blue, teal, float64(k)/float64(T)), "w"+strconv.Itoa(x)+":"+strconv.Itoa(k))
					}
					if x%2 == 0 {
						cv.fill(x, 0, grey, "")
					}
				}
			},
		},
	}
}

/* ---------- [d] blocks ---------- */

type block struct {
	x, y, vx, vy, tx, ty, a, ta float64
	c, tc                       rgb
	spring                      int
	home                        bool
}

type sim struct {
	blocks  []block
	springs []harmonica.Spring
	owner   map[string]int
	ox, oy  float64 // screen pixel of grid origin
	resX    float64 // reserve location (screen pixels)
	resY    float64
}

func newSim(n, fps int) *sim {
	s := &sim{blocks: make([]block, n), owner: map[string]int{}}
	for i := 0; i < 8; i++ {
		s.springs = append(s.springs, harmonica.NewSpring(harmonica.FPS(fps), 6+float64(i)*0.6, 0.55+float64(i%4)*0.06))
	}
	for i := range s.blocks {
		s.blocks[i].spring = rand.IntN(len(s.springs))
		s.blocks[i].c, s.blocks[i].tc = muted, muted
	}
	return s
}

func (s *sim) reserve() (float64, float64) {
	return s.resX - s.ox + (rand.Float64()-0.5)*2, s.oy - s.resY + (rand.Float64()-0.5)*3
}

func (s *sim) place(bi int, c cellT) {
	b := &s.blocks[bi]
	b.tx, b.ty, b.ta, b.tc, b.home = float64(c.x), float64(c.y), 1, c.c, false
}

// assign keeps identity per cell key, reuses freed visible blocks in column order,
// and pulls the rest out of the [c] reserve.
func (s *sim) assign(cv *canvas) {
	n := len(s.blocks)
	used := make([]bool, n)
	type kc struct {
		key string
		c   cellT
	}
	var fresh []kc
	for _, key := range cv.order {
		c := cv.cells[key]
		if bi, ok := s.owner[key]; ok && !used[bi] {
			used[bi] = true
			s.place(bi, c)
		} else {
			fresh = append(fresh, kc{key, c})
		}
	}
	for key, bi := range s.owner {
		if _, ok := cv.cells[key]; !ok || !used[bi] {
			delete(s.owner, key)
		}
	}
	free := make([]int, 0, n)
	visible := 0
	for i := range s.blocks {
		if !used[i] {
			free = append(free, i)
			if s.blocks[i].a > .1 {
				visible++
			}
		}
	}
	sort.Slice(free, func(p, q int) bool {
		bp, bq := &s.blocks[free[p]], &s.blocks[free[q]]
		vp, vq := bp.a > .1, bq.a > .1
		if vp != vq {
			return vp
		}
		if bp.x != bq.x {
			return bp.x < bq.x
		}
		return bp.y < bq.y
	})
	sort.Slice(fresh, func(p, q int) bool {
		if fresh[p].c.x != fresh[q].c.x {
			return fresh[p].c.x < fresh[q].c.x
		}
		return fresh[p].c.y < fresh[q].c.y
	})
	r := 0
	for k, f := range fresh {
		if r >= len(free) {
			break
		}
		bi := free[r]
		r++
		used[bi] = true
		s.owner[f.key] = bi
		if k >= visible && s.blocks[bi].a < .05 {
			s.blocks[bi].x, s.blocks[bi].y = s.reserve()
		}
		s.place(bi, f.c)
	}
	for i := range s.blocks {
		if !used[i] {
			b := &s.blocks[i]
			b.ta = 0
			if b.a > .05 && !b.home {
				b.tx, b.ty = s.reserve()
				b.home = true
			}
		}
	}
}

func (s *sim) step(dt float64) {
	for i := range s.blocks {
		b := &s.blocks[i]
		sp := s.springs[b.spring]
		b.x, b.vx = sp.Update(b.x, b.vx, b.tx)
		b.y, b.vy = sp.Update(b.y, b.vy, b.ty)
		rate := 5.0
		if b.ta > 0 {
			rate = 10
		}
		b.a += (b.ta - b.a) * math.Min(1, dt*rate)
		b.c = mix(b.c, b.tc, math.Min(1, dt*6))
	}
}

/* ---------- counting terminal output ---------- */

type countingFile struct {
	*os.File
	n atomic.Int64
}

func (f *countingFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	f.n.Add(int64(n))
	return n, err
}

/* ---------- Bubble Tea model ---------- */

const panelW = 24

type tickMsg time.Time

type stats struct {
	Cols, Rows     int
	Seconds        float64
	Ticks          int
	TicksPerSec    float64
	BytesOut       int64
	BytesPerSec    float64
	BytesPerTick   float64
	AvgSimMs       float64
	MaxSimMs       float64
	AvgViewMs      float64
	MaxBlocks      int
	TargetFPS      int
	Programs       []string
	FullRepaintKiB float64 // size of one View() string, i.e. a worst-case full repaint
}

type model struct {
	progs      []*program
	cur, sel   int
	mem        memory
	sim        *sim
	w, h       int
	start      time.Time
	last       time.Time
	fps        int
	out        *countingFile
	demo       bool
	duration   time.Duration
	lastCount  int
	computeAcc float64

	// stats
	ticks               int
	simTotal, simMax    time.Duration
	viewTotal           time.Duration
	viewCount           int
	maxBlocks           int
	viewBytes           int
	secTicks, secBytes0 int64
	secStart            time.Time
	liveFPS, liveKBs    float64
	seen                map[string]bool
}

func (m *model) Init() tea.Cmd { return tick(m.fps) }

func tick(fps int) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(fps), func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) layout() {
	pxH := float64(m.h * 2)
	m.sim.ox = float64(panelW) + float64(m.w-panelW)/2
	m.sim.oy = pxH - 3
	m.sim.resX, m.sim.resY = panelW-2, 4
}

func (m *model) setProg(i int) {
	m.cur, m.sel, m.mem = i, 0, memory{}
	m.seen[m.progs[i].name] = true
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
	case tea.KeyPressMsg:
		p := m.progs[m.cur].params
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "1", "2", "3":
			m.setProg(int(msg.String()[0] - '1'))
		case "tab", "right":
			m.sel = (m.sel + 1) % len(p)
		case "shift+tab", "left":
			m.sel = (m.sel + len(p) - 1) % len(p)
		case "up", "k":
			p[m.sel].val = math.Min(p[m.sel].max, p[m.sel].val+p[m.sel].step)
		case "down", "j":
			p[m.sel].val = math.Max(p[m.sel].min, p[m.sel].val-p[m.sel].step)
		}
	case tea.MouseClickMsg:
		m.drag(msg.X)
	case tea.MouseMotionMsg:
		if msg.Button == tea.MouseLeft {
			m.drag(msg.X)
		}
	case tickMsg:
		now := time.Time(msg)
		dt := math.Min(0.05, now.Sub(m.last).Seconds())
		m.last = now
		t := now.Sub(m.start).Seconds()
		if m.duration > 0 && now.Sub(m.start) >= m.duration {
			return m, tea.Quit
		}
		if m.demo {
			m.autopilot(t)
		}
		s0 := time.Now()
		m.computeAcc += dt
		if m.computeAcc >= 1.0/30 {
			m.computeAcc = 0
			cv := &canvas{cells: map[string]cellT{}}
			pr := m.progs[m.cur]
			pr.run(pr.params, t, &m.mem, cv)
			m.sim.assign(cv)
			m.lastCount = min(len(cv.order), len(m.sim.blocks))
			m.maxBlocks = max(m.maxBlocks, m.lastCount)
		}
		m.sim.step(dt)
		el := time.Since(s0)
		m.simTotal += el
		m.simMax = max(m.simMax, el)
		m.ticks++
		m.secTicks++
		if now.Sub(m.secStart) >= time.Second {
			sec := now.Sub(m.secStart).Seconds()
			b := m.out.n.Load()
			m.liveFPS = float64(m.secTicks) / sec
			m.liveKBs = float64(b-m.secBytes0) / 1024 / sec
			m.secTicks, m.secBytes0, m.secStart = 0, b, now
		}
		return m, tick(m.fps)
	}
	return m, nil
}

// drag maps the pointer x across the canvas to the selected param's range.
func (m *model) drag(x int) {
	if x < panelW || m.w <= panelW {
		return
	}
	p := m.progs[m.cur].params[m.sel]
	f := float64(x-panelW) / float64(m.w-panelW-1)
	v := p.min + f*(p.max-p.min)
	p.val = math.Max(p.min, math.Min(p.max, math.Round(v/p.step)*p.step))
}

// autopilot cycles programs and sweeps params so measurements include heavy motion.
func (m *model) autopilot(t float64) {
	phase := int(t / 6)
	if want := phase % len(m.progs); want != m.cur {
		m.setProg(want)
	}
	p := m.progs[m.cur].params[0]
	f := 0.5 + 0.5*math.Sin(t*1.3)
	p.val = math.Round((p.min+f*(p.max-p.min))/p.step) * p.step
}

func sgrFg(sb *strings.Builder, c rgb) {
	fmt.Fprintf(sb, "\x1b[38;2;%d;%d;%dm", int(c[0]), int(c[1]), int(c[2]))
}

func sgrBg(sb *strings.Builder, c rgb) {
	fmt.Fprintf(sb, "\x1b[48;2;%d;%d;%dm", int(c[0]), int(c[1]), int(c[2]))
}

func (m *model) panel() []string {
	pr := m.progs[m.cur]
	lines := []string{
		"", " [c] compute", " ──────────────────", " program  " + pr.name, "",
	}
	for i, p := range pr.params {
		mark := "  "
		if i == m.sel {
			mark = "▸ "
		}
		lines = append(lines, fmt.Sprintf(" %s%-10s%6s", mark, p.name, strconv.FormatFloat(p.val, 'f', -1, 64)))
	}
	lines = append(lines, "",
		fmt.Sprintf(" [d] %d blocks", m.lastCount),
		fmt.Sprintf(" tick %5.1f /s", m.liveFPS),
		fmt.Sprintf(" out %6.1f KB/s", m.liveKBs),
		fmt.Sprintf(" sim %6.2f ms", float64(m.simMax.Microseconds())/1000),
		"",
		" 1 2 3   program",
		" ←→ tab  param",
		" ↑↓      value",
		" drag    set value",
		" q       quit",
	)
	return lines
}

func (m *model) View() tea.View {
	v0 := time.Now()
	if m.w == 0 || m.h == 0 {
		return tea.NewView("")
	}
	W, pxH := m.w, m.h*2
	type px struct {
		c  rgb
		on bool
	}
	fb := make([]px, W*pxH)
	for i := range m.sim.blocks {
		b := &m.sim.blocks[i]
		if b.a < .05 {
			continue
		}
		X := int(math.Round(m.sim.ox + b.x))
		Y := int(math.Round(m.sim.oy - b.y))
		if X < panelW || X >= W || Y < 0 || Y >= pxH {
			continue
		}
		fb[Y*W+X] = px{mix(bg, b.c, b.a), true}
	}
	pl := m.panel()
	var sb strings.Builder
	sb.Grow(W * m.h * 12)
	for row := 0; row < m.h; row++ {
		text := ""
		if row < len(pl) {
			text = pl[row]
		}
		runes := []rune(text)
		if len(runes) > panelW-1 {
			runes = runes[:panelW-1]
		}
		sb.WriteString("\x1b[0m")
		if row == 1 {
			sgrFg(&sb, accent)
		} else {
			sgrFg(&sb, ink)
		}
		sb.WriteString(string(runes))
		sb.WriteString(strings.Repeat(" ", panelW-1-len(runes)))
		sgrFg(&sb, muted)
		sb.WriteString("│")
		// pixels: two vertical pixels per cell via half blocks
		var curFg, curBg rgb
		fgSet, bgSet := false, false
		for x := panelW; x < W; x++ {
			top, bot := fb[(row*2)*W+x], fb[(row*2+1)*W+x]
			switch {
			case !top.on && !bot.on:
				if bgSet {
					sb.WriteString("\x1b[49m")
					bgSet = false
				}
				sb.WriteByte(' ')
				continue
			case top.on && bot.on && top.c == bot.c:
				if !fgSet || curFg != top.c {
					sgrFg(&sb, top.c)
					curFg, fgSet = top.c, true
				}
				if bgSet {
					sb.WriteString("\x1b[49m")
					bgSet = false
				}
				sb.WriteString("█")
				continue
			}
			ch, fg := "▀", top.c
			if !top.on {
				ch, fg = "▄", bot.c
			}
			if !fgSet || curFg != fg {
				sgrFg(&sb, fg)
				curFg, fgSet = fg, true
			}
			if top.on && bot.on {
				if !bgSet || curBg != bot.c {
					sgrBg(&sb, bot.c)
					curBg, bgSet = bot.c, true
				}
			} else if bgSet {
				sb.WriteString("\x1b[49m")
				bgSet = false
			}
			sb.WriteString(ch)
		}
		sb.WriteString("\x1b[0m")
		if row < m.h-1 {
			sb.WriteByte('\n')
		}
	}
	s := sb.String()
	m.viewBytes = max(m.viewBytes, len(s))
	v := tea.NewView(s)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.BackgroundColor = color.RGBA{uint8(bg[0]), uint8(bg[1]), uint8(bg[2]), 255}
	v.WindowTitle = "blockies tui spike"
	m.viewTotal += time.Since(v0)
	m.viewCount++
	return v
}

func main() {
	fps := flag.Int("fps", 60, "simulation and render rate")
	n := flag.Int("blocks", 2600, "size of the [d] block pool")
	prog := flag.Int("prog", 1, "starting program: 1 eiffel, 2 sort, 3 wave")
	demo := flag.Bool("demo", false, "autopilot: cycle programs and sweep params")
	dur := flag.Duration("duration", 0, "quit after this long (0 = run until q)")
	statsPath := flag.String("stats", "", "write measurements as JSON here on exit")
	flag.Parse()

	out := &countingFile{File: os.Stdout}
	now := time.Now()
	m := &model{
		progs: programs(), sim: newSim(*n, *fps), fps: *fps, out: out, demo: *demo, duration: *dur,
		start: now, last: now, secStart: now, seen: map[string]bool{},
	}
	m.setProg(max(0, min(len(m.progs)-1, *prog-1)))

	p := tea.NewProgram(m, tea.WithOutput(out), tea.WithFPS(*fps))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if *statsPath != "" {
		secs := time.Since(m.start).Seconds()
		bytes := out.n.Load()
		var progs []string
		for _, pr := range m.progs {
			if m.seen[pr.name] {
				progs = append(progs, pr.name)
			}
		}
		st := stats{
			Cols: m.w, Rows: m.h, Seconds: secs, Ticks: m.ticks, TicksPerSec: float64(m.ticks) / secs,
			BytesOut: bytes, BytesPerSec: float64(bytes) / secs, BytesPerTick: float64(bytes) / float64(max(1, m.ticks)),
			AvgSimMs:  float64(m.simTotal.Microseconds()) / 1000 / float64(max(1, m.ticks)),
			MaxSimMs:  float64(m.simMax.Microseconds()) / 1000,
			AvgViewMs: float64(m.viewTotal.Microseconds()) / 1000 / float64(max(1, m.viewCount)),
			MaxBlocks: m.maxBlocks, TargetFPS: m.fps, Programs: progs,
			FullRepaintKiB: float64(m.viewBytes) / 1024,
		}
		j, _ := json.MarshalIndent(st, "", "  ")
		_ = os.WriteFile(*statsPath, j, 0o644)
	}
}
