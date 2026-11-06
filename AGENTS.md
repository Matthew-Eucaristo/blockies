# Blockies (working name)

A living whiteboard: every visual is made of pieces with a finite, declared set of states, and everything animates between states. Humans draw and drag; AI agents author boards through a deliberately small vocabulary.

Co-created by Matthew and Edward Manhattan (original idea).

## Philosophy (invariants)

- **Few choices, on purpose.** Agents get a small, closed set of primitives. Fewer choices means valid, predictable output and no "illusion of choice". New primitives must justify themselves.
- **State is the unit of everything.** An element = a list of declared states + how to draw each state. Animation, dragging, linking and playback all derive from that (inspired by Declarative Dragging, Blinn/Horowitz/Heer, UIST 2026).
- **[c] computes, [d] composes.** Compute blocks run logic (math, simulations, processes) and only emit targets; drawing blocks are dumb bodies that travel/fade/recolor toward those targets with springs. Identity (cell ids) makes motion meaningful.
- **Lightweight.** Zero-dependency, single-file prototypes until a foundation is chosen. No build step yet.

## Current prototypes

- `kinetic.html`: semantic board. 8 pieces (box, arrow, token, switch, slider, bar, card, note), line-based DSL, `link` (proportional state following), `step` (cumulative, slide-like playback), generic drag = project pointer onto the path through each state's handle position. "Agent prompt" button copies the grammar for any LLM.
- `blocks.html`: Edward's [c]/[d] idea. One compute program (`param()`, `g.fill(x, y, color, id?)`, `g.rect(...)`, `time`, `mem`) drives a pool of 2600 pixel blocks. Cells keep identity by id (or position), freed blocks are reused nearby, the rest return into the [c] block.

## Commands

- Serve locally: `python3 -m http.server 4317` (also in `.claude/launch.json`), then open `/kinetic.html` or `/blocks.html`.

## Verification

- Open each changed page in a browser, check light and dark mode, check the browser console for errors.
- For DSL/parser changes: paste a board containing one invalid line; the error must be listed and every valid line must still render.

## Direction (proposal, not final)

See `docs/architecture.md`. In short: Go core, one actor (goroutine) per board, server-authoritative intents → versioned patches, JSON protocol, SQLite store; runtimes (Bubble Tea TUI first, web, MCP) animate locally and may skip what they cannot render. `[c]` compute must stay deterministic (integer math, seeded PRNG, no clock). Keep `Transport` and `Store` behind interfaces.
