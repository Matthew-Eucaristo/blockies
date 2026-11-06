# Architecture (proposal, v0)

Status: research notes and a proposed direction, checked 2026-11-06. Nothing here is final.

## The one rule

**The core owns state. Runtimes render it.**

The core holds small, authoritative board state and validates every change. It never sends frames or pixels. Each runtime (terminal, web, agent) receives target states and animates toward them however it can. A runtime that cannot animate something simply jumps to the next state, and the end result is still correct.

```
            intents (user dragged X to state 2, set height=70)
  ┌────────┐  ─────────────────────────────────►  ┌──────────────────────┐
  │ TUI    │                                      │ core                 │
  │ Web    │  ◄─────────────────────────────────  │  board actor (1/board)│
  │ MCP    │      patches (X: state 2, rev 41)    │  validate, apply, log │
  └────────┘                                      │  store (SQLite)       │
   animate locally (springs)                      └──────────────────────┘
```

## What the server actually does

1. **Holds each board in one "actor".** An actor is a single owner of one board's state that processes messages one at a time. Two users can never write the same board simultaneously, so there are no race conditions and no merge logic. In Go, this is one goroutine per board reading from a channel.
2. **Validates intents.** "Move token `req` to state 9" is rejected if the route has 7 stops. This is also what keeps AI agents honest.
3. **Broadcasts patches** with a revision number (`rev`) to everyone connected to that board.
4. **Persists** a snapshot plus a short op log, with writes merged (about once per second and on idle), not one write per message.
5. **Replays on reconnect.** A client says "I last saw rev 38" and gets patches 39 to 41, or a fresh snapshot.

What it does **not** do: animation, layout, rendering, per-frame work.

## Durable Objects and Rivet, simply

- **Cloudflare Durable Objects (DO)** are this same actor idea as a hosted product: one object per board, single-threaded, with its own SQLite storage and WebSockets, running at the edge.
  - DO classes must be written in JS/TS, Python or Rust. Go only fits as a TinyGo WASM module inside a TypeScript object.
  - Cost for 1,000 boards × 3 users × 5 msg/s × 1 h/day is roughly $20–250/month if writes are merged, and over $1.7k if every message is written.
  - Self-hosting via `workerd` exists but its disk storage and cluster mode are marked experimental.
- **Rivet** is an open-source (Apache-2.0) actor platform: actors with state, SQLite, WebSockets and scheduling. It shipped a major 3.0 rewrite days ago (about 2026-11-03).
  - Actors are TypeScript or Rust. There is no Go SDK or client.
  - It adds a control plane you have to run. Rivet Cloud starts at $10/month plus nodes.

**Verdict:** both implement the model we want, but neither fits a Go-first, self-hostable core today. We get the same model from plain Go: one goroutine per board, a channel as its mailbox, SQLite for storage. DO stays an option for a later hosted edge adapter that speaks the same protocol.

**Do we need CRDTs (Yjs, Automerge)?** No. State is small, finite and has one authority. CRDTs only become useful if we later want offline editing on several devices that merges without a server.

## Proposed shape

One Go module, one binary, several commands:

```
blockies serve   # core + WebSocket + embedded web UI (go:embed) + MCP over HTTP
blockies tui     # terminal runtime; embeds the core in-process for local boards, or connects to a server
blockies mcp     # MCP over stdio for Claude Code and other agents
```

Packages (sketch):

| Package | Responsibility |
|---|---|
| `core/board` | Pieces, declared states, links, steps; pure state transitions, no I/O |
| `core/compute` | Deterministic `[c]` programs: params → cells `{x, y, color, id}` |
| `core/actor` | One goroutine per board: apply intents, emit patches, assign `rev` |
| `protocol` | Message types and JSON Schema; transport-neutral |
| `transport/ws`, `transport/inproc` | WebSocket and in-memory transports behind one interface |
| `store/sqlite` | Snapshot + op log, merged writes; Postgres later for hosted |
| `runtime/tui` | Bubble Tea app |
| `mcp` | MCP server; just another client of the intent API |
| `web/` | Web runtime, built and embedded into the binary |

`Transport` and `Store` are interfaces from day one, so DO, NATS or a hosted cloud sync can be added later without touching the core.

## Protocol v0 (draft)

| Direction | Message | Purpose |
|---|---|---|
| client → core | `hello{protocol, caps[], lastRev}` | Join a board; declare what this runtime can render |
| core → client | `welcome{snapshot \| patches, rev}` | Full state, or what was missed since `lastRev` |
| client → core | `intent{id, op, args, baseRev}` | "Set piece X to state k", "set param height=70" |
| core → client | `ack{id, rev}` / `reject{id, code, detail}` | Clear, specific errors so agents can self-correct |
| core → all | `patch{rev, ops[]}` | State transitions only, never interpolated positions |
| both | `presence` | Who is here, cursors |

Encoding: JSON first (agents write it, JSON Schema validates it, easy to debug). Add CBOR or msgpack only if profiling demands it. Prior art: tldraw sync (server-authoritative, `push` → `commit | discard | rebase`), Figma (server-authoritative, per-property last-writer-wins), Colyseus (snapshot then patches).

## Where `[c]` compute runs

Do not stream pixels. A 2,000-cell board at 30 Hz is wasteful to send and expensive on any hosted actor. Sync only the params and the program; compute runs where it is rendered, or on the server when a thin client cannot.

Determinism matters once compute runs in more than one place:

- JS `Math.sin` is not specified to match across engines, and Go may fuse multiply-add on arm64.
- Use integer or fixed-point math, a seeded PRNG passed in as a param, and no clock or `Math.random` in `[c]`.

Options, in order of preference:

1. **A small integer-only DSL interpreted in Go**, also compiled to WASM for the web when offline mode is needed. One implementation, best determinism.
2. **Sandboxed JS run by `goja`** (pure-Go JS engine) on the server or in the TUI process. LLMs write JS fluently; the browser just renders results.

## Runtimes

### TUI first (Go, Bubble Tea v2)

A strong first-party runtime. It proves "runtimes degrade gracefully" from day one, and it fits the Go preference.

- Bubble Tea v2.1.1 (`charm.land/bubbletea/v2`): up to 120 fps, synchronized output, mouse drag support (`MouseModeCellMotion`).
- Harmonica: Charm's spring library, the same model as the web prototypes.
- Pixels as **half-blocks** (`▀` with truecolor foreground/background) give two pixels per cell and survive tmux and SSH. Kitty/Sixel graphics are sharper but are stripped by tmux.
- Risk: bytes written to the terminal. A full truecolor repaint of 160×50 cells is about 300 KB. Render a raw cell buffer with diffing, not one Lip Gloss style per cell. **Spike this first.**

### Web

- Lightest option: Vite + vanilla TS or Preact, Canvas2D for blocks (switch to PixiJS beyond about 10k blocks).
- React + TanStack Router only if the web app grows real multi-page routing (accounts, galleries).
- Built assets are embedded into the Go binary with `embed.FS`, so self-hosting stays a single file.

### Agents (MCP)

MCP is how tools like Claude Code call external programs. Our binary exposes tools such as `board_create`, `piece_add`, `compute_define`, `piece_set_state`, `step` and `board_snapshot`. Each tool call becomes an `intent` against the core, so agents get the same validation as humans, and anyone with the TUI or web open watches the board change live.

- Go SDK: `github.com/modelcontextprotocol/go-sdk` (official, v1.8.0).
- Connect: `claude mcp add --transport stdio blockies -- blockies mcp`.
- Later: MCP Apps (`ui://` resources) can show the web board inline in hosts that support it (Claude, Claude Desktop, VS Code). Claude Code is not listed as supporting it yet.

## Open-source and hosted

- OSS: core, protocol, TUI, web runtime, MCP server. Self-host = one binary + SQLite.
- Hosted (possible paid tier): same binary with Postgres, sticky routing by board ID; NATS for board ownership and routing once one node is not enough; accounts and cloud sync of local boards.
- A license has not been chosen yet. It decides what others may do with a hosted clone.

## Spikes before committing

1. ~~TUI throughput~~ Done, see `spikes/tui/README.md`: about 1,800 blocks at 57 ticks/s, 0.2 ms sim per tick, about 2 KB per tick to the terminal. Still to check by eye in Ghostty, iTerm2 and over SSH.
2. Actor + WebSocket + SQLite round trip with two clients and an MCP client on one board.
3. `[c]` determinism: the same program in the DSL (or goja) producing identical cells on amd64, arm64 and WASM.
4. Go → WASM bundle size for the core (brotli), if we want offline web.

## Sources

- Durable Objects: [concepts](https://developers.cloudflare.com/durable-objects/concepts/what-are-durable-objects/), [pricing](https://developers.cloudflare.com/durable-objects/platform/pricing/), [WebSockets](https://developers.cloudflare.com/durable-objects/best-practices/websockets/), [limits](https://developers.cloudflare.com/durable-objects/platform/limits/), [workerd](https://github.com/cloudflare/workerd), [workers-go](https://github.com/syumai/workers-go)
- Rivet: [repo](https://github.com/rivet-dev/rivet), [pricing](https://www.rivet.dev/pricing)
- Go real-time: [centrifuge](https://github.com/centrifugal/centrifuge), [NATS](https://github.com/nats-io/nats-server)
- Protocols: [tldraw sync protocol](https://github.com/tldraw/tldraw/blob/main/packages/sync-core/src/lib/protocol.ts), [Figma multiplayer](https://www.figma.com/blog/how-figmas-multiplayer-technology-works/), [Liveblocks conflicts](https://development.liveblocks.io/docs/guides/how-conflict-resolution-works-in-liveblocks-sync)
- Go/WASM: [Go wiki](https://go.dev/wiki/WebAssembly), [wasmexport](https://go.dev/blog/wasmexport), [float rules](https://go.dev/ref/spec#Floating_point_operators)
- TUI: [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Harmonica](https://github.com/charmbracelet/harmonica)
- MCP: [Go SDK](https://github.com/modelcontextprotocol/go-sdk), [Claude Code MCP](https://code.claude.com/docs/en/mcp), [MCP Apps](https://modelcontextprotocol.io/extensions/apps/overview)
- Web: [PixiJS ParticleContainer](https://pixijs.com/blog/particlecontainer-v8), [Go embed](https://pkg.go.dev/embed)

Not verified yet: DO duration billing between frequent messages, `go:wasmexport` under `GOOS=js`, Bubble Tea throughput at 2k animated cells, MCP Apps in Claude Code.
