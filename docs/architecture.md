# Architecture

Status: implemented in Go, 2026-11-10. Why it looks like this: [decisions.md](decisions.md).

## Flow

```
  agent ── MCP (stdio or /mcp) ─┐
  agent ── CLI ── HTTP ─────────┤
  person ── board page ── /talk ── agent.Agent (Claude) ─┼──▶ boards.Service ──▶ Store (files or memory)
  person ── board page ◀── SSE ──┘         │
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
| `agent` | The studio's harness: one conversation per board, Claude draws through `boards.API` while it answers | network |
| `internal/fakemodel` | A scripted stand-in for the Messages API, for tests | none |
| `web` | The board page and home page, embedded | none |
| `cmd/blockies` | `serve`, `mcp`, board commands, offline `check` and `render` | all |

`lattice`, `script` and `kernel` never import the clock, randomness, files or the network, and build for `js/wasm` and `wasip1/wasm` (checked in `scripts/check.sh`). A browser or another host can run the kernel itself later.

## Kernel semantics

- **Time.** Lines happen in order at a cursor. A message is sent at the cursor and arrives one tick later; the cursor moves to its arrival. A `tap` also takes one tick. `&` starts a line at the same tick as the line above. `wait N` moves the cursor. Everything else happens at the cursor.
- **Each tick:** scripted lines for this tick (in order), then `every` rules that are due, then delivery of arriving messages into mailboxes, then handling (up to `cap` messages per process, all if no cap), firing matching `on` rules, then bodies.
- **Rules** belong to a process (`on db ...` to db; `every N: a -> ...` to a) and stop when it ends. Rule actions happen at the handling tick; their messages arrive one tick later. `$from` is the sender of the message being handled. A rule matches the first word of a message's label, or `*`.
- **Placement.** Top-level processes are spread along axis 0 in declaration order, centered on the other axes (so the world view and the sequence view agree on order). Children go to the nearest free cell around their parent. A core needs every cell around it (diagonals included) free of other cores. Explicit `at` is allowed but discouraged.
- **Bodies.** Size = `body N` plus messages waiting in the mailbox, at most 64. Cells grow breadth-first from the core and the body, never touching another process's core; they shrink from the newest cell.
- **Errors** carry the line and a stable code. Parse codes: `syntax`, `bad_number`, `bad_id`, `bad_world`, `bad_color`, `bad_state`, `bad_action`, `too_long`. Run codes: `unknown_proc`, `dup_proc`, `no_parent`, `unknown_state`, `bad_ref`, `bad_at`, `no_room`, `dup_world`, `dup_kind`, `no_click`, `too_big`, `too_long`. A rule that fails every tick reports once.
- **Limits:** 2000 ticks, 500 processes, 20000 messages, 64 body cells, 256 per world axis, 2^20 cells per world, 256 KiB per script.
- Without a `world` line the world is `4n+1 × 15` for `n` top-level processes; viewers frame the part that is used.

- **People taking part.** `click ID: actions` makes ID a button (`Proc.tap`, true for the whole life of a process that ever gets a click rule). When people tap it, the server appends `tap ID N` to the board; the kernel runs ID's click rules at that tick with `$from` = ID, and refuses a tap on a block without one (`no_click`). Taps from a live audience stay deterministic: they are lines of the script, so every screen and every replay agrees.
- **Values and formulas.** Every process can hold a value, a number or a text (`value` events carry `value` or `text`). `set`, `add` and `calc` take formulas (script/expr.go: arithmetic, comparisons, and/or/not, min/max/round/floor/ceil/abs/clamp/if, text joining); `calc` formulas are re-evaluated after every value change until nothing changes (formulas that feed each other forever are a `cycle`). `cap` and `body` take formulas too. Numbers are finite and at most 1e15 in size (`bad_number` past that); a run computes at most 200000 formulas (`too_big`, and the run stops there). A formula may name a block that starts later; `unknown_proc` is reported only if the name is still unknown at the end. Errors: `bad_expr`, `bad_number`, `div_zero`, `cycle`, `unknown_proc`.
- **Skins and controls.** `skin=` (box, number, text, button, slider, input, toggle, bar) with `min=`/`max=` and a starting `value` say how a block looks. Controls (slider, input, toggle) are parameters: when a person sets one, the server rewrites that block's `value` on its proc line and the whole run replays (a what-if). Taps are events added at the end.
- **People may say where.** Dragging a block writes `move ID x y`; the kernel moves its core if the cell is free and keeps its margin (`bad_at`, `no_room`), and its body regrows there. Agents still never place things.
- **Focus.** `focus ID` is a `focus` event: screens frame ID and its children from that tick, like a presenter moving the slide.

## Timeline (version 2)

```json
{
  "version": 2, "world": [17, 15], "end": 9,
  "kinds":  [{"name": "store", "color": "green", "states": ["idle", "busy"]}],
  "procs":  [{"id": "api", "label": "API", "kind": "", "color": "red", "parent": "", "at": [6, 7], "start": 0, "end": -1, "line": 6}],
  "msgs":   [{"from": 0, "to": 1, "label": "GET /cart", "sent": 0, "arrive": 1, "handled": 1, "line": 10}],
  "events": [{"t": 1, "e": "state", "p": 1, "state": "busy", "line": 11}, {"t": 4, "e": "body", "p": 3, "cells": [[19, 7]]},
             {"t": 5, "e": "tap", "p": 4, "line": 20}, {"t": 6, "e": "value", "p": 2, "value": 3, "line": 13}, {"t": 6, "e": "focus", "p": 0, "line": 15}],
  "errors": [{"line": 7, "code": "unknown_proc", "detail": "..."}]
}
```

Version 2 added the `tap`, `value`, `focus` and `move` events, text values (`text` on a value event), and `procs[].tap`, `skin`, `min`, `max`. `from`, `to` and `p` index `procs`. A process `end` of -1 means it runs to the end; `handled` of -1 means the message was never handled. The same id can appear twice if a process ended and started again.

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
| `GET /studio/{board}` | Redirects to `/b/{board}` (old links) |
| `GET /api/boards/{board}/talk` | `{model, ready, turns}`: the conversation, and whether the server has credentials |
| `POST /api/boards/{board}/talk` | Body `{text}`. One turn, streamed as server-sent `data:` events: `say` (reply text), `draw` (lines added, with any rejected), `redraw`, `undo`, `error` (`code`, `detail`), `done` |
| `POST /api/boards/{board}/tap` | Body `{id}`. A person taps a block; 202 once queued. Taps on a board are collected for 400 ms and written as one line through `boards.API`, `tap vote/a 12 vote/b 5` (one tick per window). 400 `no_click` if the block is not a button now, 429 `slow_down` for more than one tap per 120 ms from one client address (behind a tunnel everyone shares one address) |
| `POST /api/boards/{board}/input` | Body `{id, value}`. A person sets a control: 202 once queued; within 400 ms the latest value per control replaces its starting `value` (`boards.Service.Update`, atomic). 400 `no_control` if the block is not a slider, input or toggle, `bad_value` if the value does not fit (sliders are clamped to min/max; input text holds at most 80 characters and no control characters). The rewritten line is the proc line of the block running at the end, and is kept as it was if it would not read back |
| `POST /api/boards/{board}/talk/undo` | Put the board back to before the last change made by talking. `no_history` (409) if none, `conflict` (409) if someone else changed the board since |
| `DELETE /api/boards/{board}/talk` | Forget the conversation (the board stays) |
| `/mcp` | MCP, streamable HTTP |

`Result` is `{board, rev, url, summary, errors, rejected?, script?}`. API errors are `{code, detail}` with codes `bad_board` (400), `not_found` (404), `too_big` (413), `bad_request`, `internal`; talking adds `busy`, `no_history` and `conflict` (409), `no_agent` (503), and in its stream `agent_auth`, `agent_limit`, `agent_failed`, `refusal`, `cancelled`; streams can answer `busy` when 1024 are open. Board names are 1 to 64 of `a-z 0-9 - _`.

Every request must name an allowed Host (`localhost`, `127.0.0.1`, `::1`, the `--url` host, or a literal `--addr` IP; others get 403 `bad_host`), which stops DNS rebinding. Every unsafe method goes through `http.CrossOriginProtection` (403 `cross_origin`), so a web page cannot write to boards or make the agent spend tokens; the CLI and MCP clients send no browser headers and are unaffected. Errors never carry raw internal text.

## MCP tools

`board_write {board, script}`, `board_append {board, lines}`, `board_read {board}`, `board_list {}`, `board_guide {}`. The server's instructions carry the whole guide, so most clients never need `board_guide`. `blockies mcp` (stdio) becomes a client of a running server, or starts one in the same process so the links work.

## Talking (the agent)

A person talks; the agent draws while it answers. Each turn sends the person's words (and the board's script inside `<board>` tags, when it changed since the model last saw it; board text is data, never instructions) to Claude with three tools: `draw` (append lines), `redraw` (replace the board) and `undo`. Tool input streams with `eager_input_streaming`, and `agent` decodes the partial JSON, so every finished line goes to `boards.API` (and to every viewer) while the model is still writing. Rejected lines go back to the model in the tool result.

- **History** is append-only (thinking blocks must go back unchanged): only whole turns join it (a refusal or a reply cut off at `max_tokens` is dropped, and a cut-off reply also starts a fresh thread); past 120 messages, or after the API rejects a request, the next turn starts a fresh thread and the board carries the context. The conversation prefix is cached.
- **Undo** keeps the board as it was before each change made by talking, in memory. If anyone else changed the board since (an MCP agent, another tab), undo answers `conflict` instead of erasing their work; the check is not atomic against a write landing at the same moment.
- **Readiness** follows the SDK's credential order (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_PROFILE`, workload identity federation, the `ant auth login` profile) and is checked on each request, so logging in later needs no restart. Without credentials a turn answers `agent_auth` without calling the API.
- **Limits:** 8 model calls per turn, 50 undo steps, 200 shown turns, 256 boards with a conversation.

## The board page

Plain ES modules (`web/*.js`), no build step, type-checked with `tsc --checkJs`. The page is a full-window canvas with floating panels: board name and menu (top left), views (top center), share and the drawer toggle (top right), playback, captions and the talk dock (bottom center), shortcuts (bottom right), and a drawer with tabs (Chat, Processes, Script, Errors). On phones the drawer is a bottom sheet and the dock spans the width.

| Module | Role |
|---|---|
| `app.js` | Boots the page: live frames over SSE, playback, drawing only while something moves, menu, shortcuts dialog, share, theme |
| `actions.js` | Registry of actions: label, keys, menu entry, when enabled. The keyboard, the menu and the `?` dialog all read it |
| `views.js` | Registry of views (World, Voxel, Sequence). A view implements `draw`, `step`, `pick`, `settle`, `animating` over the area the panels leave free |
| `panels.js` | Registry of drawer tabs: `mount` once, keep yourself current, optional badge |
| `talk.js` | The dock, captions, conversation panel and talking actions; phases idle, listening, thinking, drawing, speaking, setup |
| `speech.js` | Speech in (`listen`) and out (`Speaker`) behind small interfaces; Web Speech API today |
| `model.js`, `world.js`, `sequence.js`, `draw.js` | The timeline model and the drawing code |
| `ui.js` | Element helper, storage that never throws, toast, event bus (`frame`, `tick`) |

State words map to tones (busy, wait, good, bad) by a fixed word list; colors come from a fixed palette of eight. The page is light by default (dark and system in the menu). `?watch` hides talking; Space then plays and pauses. Clicking a slider, input or toggle opens its control next to it (everywhere, phones included). Dragging a block in the flat world (while editing) moves it. The Script tab can be edited in place (Edit, then Apply), the same board seen as code. On watch links and phones, tapping a button block sends a tap for everyone; while editing, a click selects it and a double click taps it. The sequence view never taps. The camera follows the board's `focus` (W shows the whole board); `?view=ID` locks a page onto one part with almost no chrome, for phones in an audience. With a block selected, the menu can move every screen's focus there or copy a phone link to it.

### Extending

- **A view:** `addView({id, label, title, impl})` in `views.js`; it gets a button, a number key and a shortcuts entry.
- **An action:** `addAction({id, label, group, keys, menu, when, run})`; it shows up in the keyboard handler, the menu and the `?` dialog.
- **A panel:** `addPanel({id, label, order, mount, badge, when})`; listen on the bus (`on('frame')`, `on('tick')`) to stay current.
- **A speech engine:** implement `listen(o)` and the `Speaker` methods in `speech.js` (for example local Whisper).
- **An agent tool:** add it to `tools()` in `agent/prompt.go` and handle it in `turn.tool`; it must change boards through `boards.API`.

## Next

1. **Hosted service:** authentication before anything else (today the server trusts whoever can reach it), accounts, API keys for agents, OAuth for remote MCP, boards owned by an account, a public base URL.
2. **People at scale:** taps are one line per half second today; a large audience over a long talk will reach the board budgets (2000 ticks is about 13 minutes of continuous tapping, and every tap window resends the whole frame to every phone). Incremental frames and folding old taps into counts come next.
3. **Nest:** a process that contains a world, shown as a live miniature that you can enter.
4. **Incremental frames** (send only what changed) once boards get large; SQLite store (`modernc.org/sqlite`) when files stop being enough.
5. **Export:** a board as a static page, SVG sequence diagram, or GIF for docs and PRs.
