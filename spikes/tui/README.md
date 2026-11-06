# Spike: TUI runtime (Go + Bubble Tea v2)

Question: can a terminal runtime animate ~2,000 `[d]` blocks driven by a `[c]` program, smoothly and cheaply enough to be the first-party runtime?

**Answer: yes.** At 60 fps the simulation takes about 0.2 ms per tick and the terminal receives about 2 KB per tick. A static board costs almost nothing.

## What it does

- Ports the three `[c]` programs from `blocks.html` (Eiffel tower, bubble sort, waves) to Go.
- A pool of 2,600 `[d]` blocks, each on a Harmonica spring (8 stiffness variants for an organic feel). Same identity-keeping assignment as the web prototype: cells keep their block, freed blocks are reused nearby, the rest fly out of the `[c]` panel.
- Half-block pixels: `▀`/`▄`/`█` with truecolor foreground and background give two square-ish pixels per cell.
- Bubble Tea v2 (`charm.land/bubbletea/v2` v2.1.1) renders and diffs cells; output goes through a byte counter.

## Run

```bash
go run .
```

Keys: `1` `2` `3` program, `←` `→` / `tab` param, `↑` `↓` value, drag on the canvas to set the value, `q` quit.

Flags: `-fps 60`, `-blocks 2600`, `-prog 1`, `-demo` (autopilot: cycles programs and sweeps params), `-duration 18s`, `-stats out.json`.

```bash
./bench.sh 18
```

Runs the autopilot in a pseudo-terminal at several sizes and prints the measurements.

## Results (Apple Silicon, Go 1.27.3, 18 s autopilot per run, all three programs)

| Terminal | Target | Ticks/s | Sim avg | View build avg | Output | Per tick | Peak blocks |
|---|---|---|---|---|---|---|---|
| 120×40 | 60 fps | 57.6 | 0.17 ms | 0.12 ms | 111 KB/s | 1.9 KB | 1,778 |
| 200×60 | 60 fps | 57.4 | 0.19 ms | 0.16 ms | 133 KB/s | 2.3 KB | 1,806 |
| 200×60 | 30 fps | 29.2 | 0.21 ms | 0.13 ms | 69 KB/s | 2.4 KB | 1,568 |

- A full repaint string is 28–40 KB, but Bubble Tea's cell diffing sends only changed cells: about 2 KB per tick in motion and about 0.1 KB/s when the board is still.
- Even at 60 fps the stream is about 1 Mbit/s, which is fine over SSH. 30 fps halves it.
- One 45 ms sim outlier was seen in a single run (likely GC or scheduling while the pty was busy); typical max is about 1 ms.
- Visual check in a real terminal: the Eiffel tower renders correctly in half-blocks and animates.

## Caveats

- `script` into `/dev/null` is a fast consumer. Real terminal emulators parse slower; check Ghostty, iTerm2 and Terminal.app by eye before relying on 60 fps.
- Colors fade toward a fixed dark background (`View.BackgroundColor`), so light terminal themes are overridden while running.
- Mouse drag here only sets a param by x position; dragging pieces between declared states (Kinetic-style) is not in this spike.

## Takeaways for the core

- The TUI can be a first-class runtime: no special graphics protocol needed, works over SSH and in tmux.
- Runtimes should own interpolation (springs) and only receive targets, which this spike confirms is cheap.
- `[c]` output with stable cell ids is what makes motion meaningful in the terminal too.
