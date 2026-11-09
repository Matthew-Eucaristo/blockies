# Architecture

Status: implemented in Go, 2026-11-10. Why it looks like this: [decisions.md](decisions.md).

## Flow

```
  agent ── MCP (stdio or /mcp) ─┐
  agent ── CLI ── HTTP ─────────┼──▶ boards.Service ──▶ Store (files or memory)
  person ── viewer ◀── SSE ─────┘         │
                                          ▼
                              kernel.Compile(script) ─▶ Timeline ─▶ every viewer
```

A board is its script. Every change replaces or extends the script; the service compiles it (pure; about 0.07 ms for the examples on an M4, `go test ./kernel -bench .`) and pushes the whole new frame to every viewer. The server keeps no running simulation: playback happens in the viewer.

## Packages

| Package | Responsibility | I/O |
|---|---|---|
| `lattice` | Shapes of rank 1 to 4, coordinates, face and Moore neighbours, deterministic nearest-free search | none |
| `script` | Statements, parser (per-line errors, valid lines still load), canonical formatter, the agent guide (`guide.md`) | none |
| `kernel` | Runs a script into a `Timeline`: schedule, rules, delivery, caps, placement, bodies | none |
| `boards` | `API` (the one path for changes), `Service`, `Store` (`DirStore`, `MemStore`), live frames | store |
| `server` | HTTP API, viewer pages, server-sent events, MCP over streamable HTTP | network |
| `mcpserver` | MCP tools over `boards.API` | none |
| `client` | `boards.API` over HTTP | network |
| `web` | Viewer files, embedded | none |
| `cmd/blockies` | `serve`, `mcp`, board commands, offline `check` and `render` | all |

`lattice`, `script` and `kernel` never import the clock, randomness, files or the network, and build for `js/wasm` and `wasip1/wasm` (checked in `scripts/check.sh`). A browser or another host can run the kernel itself later.

## Kernel semantics

- **Time.** Lines happen in order at a cursor. A message is sent at the cursor and arrives one tick later; the cursor moves to its arrival. `&` starts a line at the same tick as the line above. `wait N` moves the cursor. Everything else happens at the cursor.
- **Each tick:** scripted lines for this tick (in order), then `every` rules that are due, then delivery of arriving messages into mailboxes, then handling (up to `cap` messages per process, all if no cap), firing matching `on` rules, then bodies.
- **Rules** belong to a process (`on db ...` to db; `every N: a -> ...` to a) and stop when it ends. Rule actions happen at the handling tick; their messages arrive one tick later. `$from` is the sender of the message being handled. A rule matches the first word of a message's label, or `*`.
- **Placement.** Top-level processes are spread along axis 0 in declaration order, centered on the other axes (so the world view and the sequence view agree on order). Children go to the nearest free cell around their parent. A core needs every cell around it (diagonals included) free of other cores. Explicit `at` is allowed but discouraged.
- **Bodies.** Size = `body N` plus messages waiting in the mailbox, at most 64. Cells grow breadth-first from the core and the body, never touching another process's core; they shrink from the newest cell.
- **Errors** carry the line and a stable code. Parse codes: `syntax`, `bad_number`, `bad_id`, `bad_world`, `bad_color`, `bad_state`, `bad_action`, `too_long`. Run codes: `unknown_proc`, `dup_proc`, `no_parent`, `unknown_state`, `bad_ref`, `bad_at`, `no_room`, `dup_world`, `dup_kind`, `too_big`. A rule that fails every tick reports once.
- **Limits:** 2000 ticks, 500 processes, 20000 messages, 64 body cells, 256 per world axis, 2^20 cells per world, 256 KiB per script.
- Without a `world` line the world is `4n+1 × 15` for `n` top-level processes; viewers frame the part that is used.

## Timeline (version 1)

```json
{
  "version": 1, "world": [17, 15], "end": 9,
  "kinds":  [{"name": "store", "color": "green", "states": ["idle", "busy"]}],
  "procs":  [{"id": "api", "label": "API", "kind": "", "color": "red", "parent": "", "at": [6, 7], "start": 0, "end": -1, "line": 6}],
  "msgs":   [{"from": 0, "to": 1, "label": "GET /cart", "sent": 0, "arrive": 1, "handled": 1, "line": 10}],
  "events": [{"t": 1, "e": "state", "p": 1, "state": "busy", "line": 11}, {"t": 4, "e": "body", "p": 3, "cells": [[19, 7]]}],
  "errors": [{"line": 7, "code": "unknown_proc", "detail": "..."}]
}
```

`from`, `to` and `p` index `procs`. A process `end` of -1 means it runs to the end; `handled` of -1 means the message was never handled. The same id can appear twice if a process ended and started again.

## HTTP

| Method and path | Purpose |
|---|---|
| `GET /` , `GET /b/{board}` | Board list and the viewer |
| `GET /api/guide` | The language, as Markdown |
| `GET /api/boards` | `{boards: [Result]}` |
| `GET /api/boards/{board}` | `Result` with `script` |
| `PUT /api/boards/{board}` | Body: the whole script. Creates the board |
| `POST /api/boards/{board}/lines` | Body: lines to append. Bad lines come back in `rejected` and are not added |
| `DELETE /api/boards/{board}` | Remove |
| `GET /api/boards/{board}/frame` | `{board, rev, script, timeline}` |
| `GET /api/boards/{board}/stream` | Server-sent events: `frame` now and after each change, `deleted` |
| `/mcp` | MCP, streamable HTTP |

`Result` is `{board, rev, url, summary, errors, rejected?, script?}`. API errors are `{code, detail}` with codes `bad_board` (400), `not_found` (404), `too_big` (413), `bad_request`, `internal`. Board names are 1 to 64 of `a-z 0-9 - _`.

## MCP tools

`board_write {board, script}`, `board_append {board, lines}`, `board_read {board}`, `board_list {}`, `board_guide {}`. The server's instructions carry the whole guide, so most clients never need `board_guide`. `blockies mcp` (stdio) becomes a client of a running server, or starts one in the same process so the links work.

## Viewer

Plain ES modules (`web/*.js`), no build step, type-checked with `tsc --checkJs`. `model.js` answers questions about a timeline at any time; `world.js` draws the flat and voxel views (springs for things appearing and leaving, the camera framing the used part of the world); `sequence.js` draws lifelines, state bars and message arrows; `app.js` handles playback, live frames, panels and input. State words map to tones (busy, wait, good, bad) by a fixed word list; colors come from a fixed palette of eight.

## Next

1. **Hosted service:** accounts, API keys for agents, OAuth for remote MCP, boards owned by an account, a public base URL.
2. **People steering:** clicks become messages to processes (the same door agents use), so a person can poke a running board.
3. **Nest:** a process that contains a world, shown as a live miniature that you can enter.
4. **Incremental frames** (send only what changed) once boards get large; SQLite store (`modernc.org/sqlite`) when files stop being enough.
5. **Export:** a board as a static page, SVG sequence diagram, or GIF for docs and PRs.
