# Blockies (working name)

A living whiteboard: every visual is made of pieces with a finite, declared set of states, and everything animates between states. Humans draw and drag; AI agents author boards through a deliberately small vocabulary.

Co-created by Matthew and Edward Manhattan (original idea).

## Product direction (decided 2026-11-07)

- **Agent-driven first.** The main user of Blockies is an AI agent (for example Claude Code) that shows a person what it means by building and driving a board, at any complexity. People take part mostly by voice, through the agent, including while brainstorming.
- **Commercial shape:** a hosted service that agents connect to with API keys or OAuth (MCP and HTTP), returning a link the person opens to watch the board live.
- **Humans editing directly** (an easy, intuitive UI) comes later and must use the same `Op` layer as agents.
- **Core before apps ("libghostty before ghostty").** The core crates (`blockies-model`, `-dsl`, `-ops`, `-sim`, `-anim`) come before infra such as MCP, auth and hosting. The core concepts (kinds of blockies, how they share state, how they interact visually) are decided by Matthew and Edward and must be fully understood by them. AI may brainstorm options and explain trade-offs, but must not settle core design on its own.
- After that, prefer work that makes agents fast and correct: small incremental ops instead of whole-board rewrites, stable error codes, and a viewer that needs no install.

## Philosophy (invariants)

- **Few choices, on purpose.** Agents get a small, closed set of primitives: 8 pieces and the statements `link`, `step` and `sim`. Fewer choices means valid, predictable output and no "illusion of choice". New primitives must justify themselves.
- **State is the unit of everything.** An element is a list of declared states plus how to draw each state. Animation, dragging, linking and playback all derive from that (inspired by Declarative Dragging, Blinn/Horowitz/Heer, UIST 2026).
- **[c] computes, [d] composes.** Compute (`blockies-sim`, later programs) runs logic and only emits targets. Drawing (`blockies-anim`) moves bodies toward those targets with springs. Identity makes motion meaningful.
- **The owner holds state, runtimes render it.** Runtimes may skip what they cannot draw. The block field runs in deterministic lockstep and is never streamed.

## Stack (decided 2026-11-07)

Full Rust, one Cargo workspace (`crates/`, see `docs/architecture.md`). The Go direction is gone; the reasons and the Go vs Rust benchmark are in `docs/rust-pivot.md`. TypeScript is allowed only at the edges: the JS SDK and the web app shell.

## Rules for changes

- Every change to a board goes through `blockies_ops::Op` → `Doc::apply`. Do not add a second path in the server, the CLI, the TUI or MCP. Error `code`s are stable API for agents.
- `blockies-model`, `-dsl`, `-ops`, `-sim` and `-protocol` stay free of I/O and clocks, so they still compile for `wasm32-unknown-unknown`.
- The sim is integer-only (`clippy::float_arithmetic` is denied there): no clock, no `HashMap` iteration, randomness only from `mix()`. If a rule changes, update the golden hashes in `blockies-sim` tests and check the WASM determinism script.
- Protocol changes bump `PROTOCOL` in `blockies-protocol` and update the table in `docs/architecture.md`.
- `unsafe_code = "forbid"` applies workspace-wide, except `blockies-wasm`, where wasm-bindgen's glue needs it; that crate itself writes no unsafe code. Clippy runs with `-D warnings`.
- DSL changes: the parser reports a bad line and keeps every valid line. `format()` must round-trip (`parse(format(b)) == b`).

## Commands

- Full check, the same as CI: `scripts/check.sh` (fmt, clippy native and wasm32, tests, native vs WASM hashes; needs Node).
- Terminal, local: `cargo run -p blockies-cli -- tui --example web` (also `sandbox`, `water`, `sprint`) or `tui path/to/board.blk`.
- Live: `cargo run -p blockies-cli -- serve` (port 4318, `blockies.db`; `--memory` keeps nothing), then `blockies tui <board>`, `blockies new <board> <file>`, `blockies set|move|cycle|step|paint ...` with `--json`.
- Benchmarks: `cargo run --release -p blockies-sim --example bench`, `-p blockies-tui --example render_bench`, `-p blockies-server --example load`.
- Prototypes (reference only): `python3 -m http.server 4317`, then `/prototypes/kinetic.html` or `/prototypes/blocks.html`.

## Verification

- Run `scripts/check.sh` after Rust changes.
- For TUI changes, also run the TUI in a real terminal, and render tests with `ratatui::backend::TestBackend` at small and large sizes (no panics at 1×1).
- For server or protocol changes, the integration tests in `crates/blockies-server/tests` must stay green: HTTP plus two WebSocket clients, and lockstep with a late joiner.
- For DSL changes, include a board with one invalid line in a test. The error must be listed and every valid line must still load.
