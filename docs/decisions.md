# Decisions

Short records of choices that shape the code. Newest first.

## 2026-11-13: Live blocks: values, formulas, controls, moving

Matthew: Blockies is not only for votes or replacing Canva or Kahoot. The canvas is a program; it is like Figma, but you are looking at a codebase as an artist: you do not code, yet everything you do is codified and can become anything. Research (Smalltalk and HyperCard to Figma code layers, tldraw computer, Croquet, Eve, Ink & Switch) found nobody combining agent-authored blocks, many people interacting, and deterministic replay of every action, and found why end-user programming usually stays niche: visual code does not scale, you cannot see why something happened, somebody has to write the first program, and nothing can be shared. Winners keep state visible, a closed forgiving vocabulary, optional programming that grows from data, and remix.

- **Keep the core, widen the block.** The kernel, the script as log, taps as lines and one path through `boards.API` are exactly what nobody else has; nothing was rewritten.
- **Values and skins, not a bigger language.** A block holds one value (number or text) and a skin (number, text, bar, button, slider, input, toggle). Structure comes from children, so a record is a block with child blocks, as a sheet is cells.
- **Formulas like a spreadsheet** (`calc`, recalculated until stable; `set`; formulas in `cap` and `body`), the most successful end-user programming there is. Written in Go in `script/expr.go`, pure, so the kernel still builds for WebAssembly.
- **Two kinds of human input.** Controls are parameters: changing one rewrites the block's starting value and replays the whole board, so a slider answers "what if". Taps are events added at the end. Both are script.
- **People may say where.** Agents still never place things, but a person dragging a block writes `move ID x y`; the kernel keeps the margin and the moat.
- **The script is editable in the page** (two-way: canvas and code are views of one board).
- **Not yet, on purpose:** blocks containing worlds (Nest), effect ports for AI and the web (results logged as lines, so replay holds), sandboxed code blocks (QuickJS or WASM, metered) as an escape hatch, records with several fields, branching edits. Eve's lesson: a general core, but polish a few boards (vote, calculator, what-if) before widening further.

## 2026-11-12: People take part: taps, numbers, focus

Edward's vision (11 Nov) widened Blockies from "agents explain, people watch" to worlds people use: a talk where the audience votes from their phones and the screen animates the result, brainstorming with instant feedback, and agents that build the working "framework" once instead of spending tokens on every step. Matthew asked to build what fits best now, keep Go, and make the page light and simple for everyone, not only programmers.

- **Input is script.** A tap is a `tap ID` line appended through `boards.API`, so live audience input keeps everything else true: one path for changes, determinism, replay, the script as the board, no server clock. The alternative, a server-side event stream beside the script, would split the model in two.
- **Five small additions, closed vocabulary kept.** `click` (a rule that makes a block a button), `tap` (a person's tap; one tick, like a message), `add` (a number on a block), `body +N` (relative growth), `focus` (where screens look). Each one is needed by the vote example; nothing else was added.
- **Frames are processes, not coordinates.** Edward's sketch draws the presenter's frame as a box on the grid. Agents still say what, never where: `focus stage` and `?view=vote` name a process, and the camera frames it and its children, gliding between them.
- **Ticks for live input** are set by the server: it collects a board's taps for 400 ms and writes them as one line with counts (`tap vote/a 12 vote/b 5`), so a steady stream advances time at about the playback speed, the script grows per window and not per tap, and no line depends on the one before it. A first design joined each tap to the line above with `&`; review showed a refused tap could then put the next one in the past. One tap per client per 120 ms.
- **Later:** spreadsheet-like tables and formulas (one process per cell does not scale past the 500-process limit; they need a view of their own), hosting on Durable Objects (one object per board serialises appends, like the per-board lock; the kernel already builds for WebAssembly), and authentication for audiences outside a local network.
- **Light by default**, simpler chrome for people who are not programmers.
- **Size budget raised** from 110 KB / 35 KB to 130 KB / 40 KB (JS+CSS, unminified / gzipped): taps, numbers, focus, phone views and the camera added about 10 KB. Still no dependencies or build step, and nothing redraws while idle.

## 2026-11-10: Calling Claude the recommended way

The agent calls Claude through Anthropic's official Go SDK (`anthropic-sdk-go/v2`), following Anthropic's guidance for the current models, rather than through a multi-provider harness (Pi, LangChain and the like) or raw HTTP:

- Model `claude-opus-5-5` with adaptive thinking left at its default and effort `low`, which Anthropic recommends for chat; `--model` and `--effort` change both.
- Streaming, with `eager_input_streaming` on the tools so board lines arrive while the model writes them; the tool input is checked in Go before use, as the guidance requires when eager streaming is on.
- Prompt caching: a breakpoint on the system prompt (tools and system are stable and come first) plus automatic caching of the conversation. In 6 real model calls across 3 turns measured on 2026-11-10 (after the system prompt was already cached), each call read 2,081 to 2,987 tokens from the cache and sent 2 to 4 uncached input tokens; the very first call of a fresh server writes the cache instead. The server logs input, cache write, cache read and output tokens for every call.
- History is append-only, as preserved thinking requires (editing earlier turns would be rejected); only complete turns join it (a refused or cut-off reply is dropped, never sent back), and a long conversation starts a fresh thread instead, with the board carrying the context. `max_tokens` is 64,000, the streaming default.
- Server-side refusal fallbacks (`fallbacks: "default"`), typed API errors mapped to stable codes, and no forced `tool_choice` (not allowed on this model).

A manual tool loop is used instead of the SDK's tool runner because lines must reach the board while a tool call is still streaming. Other providers, if wanted later, would get their own implementation behind the same three tools with that provider's official SDK; a generic harness would hide exactly the provider features (caching, fallbacks, eager streaming) that make this fast and cheap.

## 2026-11-10: One app with floating panels; security posture

Matthew asked for a base that feels like Excalidraw: light, pleasant, easy to extend. Decided with him: "like Excalidraw" means the feel and the extension points, not drawing by hand (agents still say what, never where), and the viewer and the studio become one page.

- **One board page.** `/b/{board}` is the viewer and the place to talk; `?watch` hides talking; `/studio/{board}` redirects. One page means one place to extend and no drift between two copies.
- **Canvas first.** A full-window canvas with small floating panels in the corners (views at the top center, where Excalidraw puts its toolbar), a drawer for the text views, a `?` dialog. The views draw inside the area the panels leave free.
- **Registries, not a framework.** Views, actions (keys, menu, shortcuts dialog) and drawer panels are small registries in plain ES modules, after the extension points of Excalidraw (child components) and tldraw (components, overrides, ShapeUtil). A framework would add a build step and weight for little gain at this size.
- **Light means:** no dependencies or build step, nothing redrawn while nothing moves, about 31 KB gzipped. The first target, 100 KB unminified, was set before the registries, menu, shortcuts and speech interface were in; the page measures 105 KB unminified, and the budget is now 110 KB unminified and 35 KB gzipped (recorded as D10 in the review).
- **Security, local first.** A Host allowlist (DNS rebinding), cross-origin protection on every unsafe method (no-cors writes), kernel budgets for events and body cells (a 12 KB script could otherwise allocate gigabytes), per-board locks, capped caches, sessions and streams, an undo that refuses to erase other writers' work, board text treated as data in prompts. Real authentication comes with hosting.

## 2026-11-10: The studio: a Go harness, speech in the browser

To show people what Blockies is, the product needs a place where you talk and the board answers, not only an MCP server for coding agents. The studio is that place.

- **Harness in Go, inside the binary.** It is one more client of `boards.API`, so it changes nothing about the core and ships with `go install`. Python was considered for its speech libraries, but the speech that matters now runs in the browser, and a second runtime would split the project.
- **Claude through the official Go SDK**, beta Messages API: streaming, eager tool input (lines reach the board while the model writes), server-side refusal fallbacks. Default `claude-opus-5-5` at `low` effort for short spoken turns; `--model` and `--effort` change it.
- **Speech in the browser first.** The Web Speech API gives live partial transcripts with no server work (Chrome and Safari; Edge and Firefox are not reliable yet), and any dictation (fn twice on a Mac) works in the text box. Local Whisper (`whisper.cpp`) comes when privacy or quality needs it.
- **Undo lives in the agent, not in `boards.API`.** A turn appends many lines; undo should remove the turn, which only the studio knows. History is in memory: enough for a conversation.

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
