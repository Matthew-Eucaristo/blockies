# Decisions

Short records of choices that shape the code. Newest first.

## 2026-11-10: Go instead of Rust

Rust was chosen on 2026-11-07 for a different goal: millions of simulated cells in one view (Noita-like), lockstep simulation in every client through WASM, and no garbage collector. The Kernel direction changed the load: worlds hold tens to thousands of cells, the server owns the state, and viewers only render. Go fits that better:

- The product is a network service (HTTP, streams, MCP, auth later). Go's standard library and the official MCP Go SDK cover it with one dependency.
- Matthew and Edward must understand the core fully. Go is easier to read and review than Rust with lifetimes and ownership.
- Fast compiles and simple tooling keep the loop short for people and agents.
- Garbage collection pauses are irrelevant at this size, and compiling a board is pure, so determinism does not depend on the runtime.

If massive cellular simulation comes back, that part can be written in Rust or Zig, compiled to WASM, behind the same timeline format. The Rust code is tagged `rust-final`.

## 2026-11-10: The Kernel model

From the eight playable concepts in `explorations/`, Matthew chose Kernel with Nest and the views. What it keeps: a process is one block; processes share nothing and talk by messages; agents say what, never where; space is owned (cores keep a margin, bodies never touch other cores); diagrams are views of a run, not things to draw. What it adds over the sketch: a line-based script instead of JavaScript functions (about ten statements), rules owned by processes, and backlog as body.

## 2026-11-10: Scripts are the state; the server does not tick

The board is its script, and compiling is pure and cheap, so the server stores text, compiles on change and sends whole frames. Playback, scrubbing and speed belong to each viewer. This removes server clocks, lockstep and snapshots, makes boards diffable files, and keeps hosting cheap. Revisit when boards get large enough that whole frames cost too much (send diffs then).

## 2026-11-10: Server-sent events, not WebSocket

Updates go one way (server to viewer). SSE is plain HTTP, works through proxies, reconnects by itself, and needs no library. Viewer input (later: clicks as messages) can be plain POSTs.

## 2026-11-10: Viewer without a build step

The viewer is a few ES modules served as they are and embedded in the binary. JSDoc types checked by `tsc --checkJs` give type safety without a bundler, so the files you edit are the files that ship and `go install` needs no Node. Add a bundler only for a real need.

## 2026-11-10: Files before a database

`DirStore` keeps one `.blk` file per board: readable, diffable, and enough for local use. The `Store` interface leaves room for SQLite (`modernc.org/sqlite`, no cgo) or Postgres when hosting needs it.
