# Blockies (working name)

Live boards that agents write and people watch. A board is a small world of processes (computers) that talk only by messages. An agent writes what happens in a tiny line-based script; the kernel places everything and runs it; the viewer shows the same run as a living world, as voxels, and as a sequence diagram with a timeline.

Co-created by Matthew and Edward Manhattan (original idea).

## Product direction

- **Agent-driven first (2026-11-07).** The main user is an AI agent (for example Claude Code) showing a person what it means, at any complexity. People take part mostly by voice, through the agent.
- **Commercial shape:** a hosted service agents connect to with API keys or OAuth (MCP and HTTP), returning a link the person opens to watch live. Humans editing directly comes later, through the same `boards.API`.
- **Core before apps ("libghostty before ghostty").** The core packages (`lattice`, `script`, `kernel`) come before infra (auth, hosting).
- **Direction and stack (2026-11-10).** Matthew chose the Kernel direction from `explorations/` (processes with bodies, messages only, agents say what not where, diagrams are views of a run) and moved the project to Go. The agent acts as CTO for stack and design choices. Changes to the script vocabulary or the core model are proposed with trade-offs to Matthew and Edward, who must understand the core fully.

## Invariants

- **Agents say what, never where.** Only the world size may have numbers in space. The kernel places cores (spread by declaration order, children next to their parent) and grows bodies.
- **Few choices, on purpose.** The statement list in `script` is closed. A new statement must justify itself against what agents get wrong.
- **Processes share nothing.** They talk only by messages, which take one tick. Rules belong to a process and stop when it ends.
- **Time is an axis; diagrams are views.** The kernel emits one timeline; renderers decide how to show it. Never add a diagram type to the language.
- **Space is owned.** Cores keep one free cell around them (diagonals included); a body never touches another process's core (the moat).
- **The script is the board.** Stores keep scripts only. The server compiles on change and streams whole frames; nothing ticks on the server.

## Stack

Go 1.27, one module. MCP: `github.com/modelcontextprotocol/go-sdk` (stdio and streamable HTTP). HTTP: stdlib `net/http` with server-sent events. Viewer: plain ES modules with JSDoc, type-checked by TypeScript 7 (`tsc --checkJs`), embedded with `go:embed`, no build step. Lint: golangci-lint v2 (`.golangci.yml`). The last Rust version is tagged `rust-final`.

## Rules for changes

- Every board change goes through `boards.API` (implemented by `boards.Service` in process and `client.Client` over HTTP). `server`, `mcpserver` and the CLI are thin adapters; never add a second path. Error `code`s (script errors and API errors) are stable API for agents.
- `lattice`, `script` and `kernel` stay pure: no clock, randomness, files or network, and they build for WebAssembly. `scripts/check.sh` enforces both.
- Determinism: maps are for lookups only, never iterated into output. If kernel behaviour changes on purpose, update the golden hashes in `kernel/kernel_test.go`.
- Timeline JSON changes bump `kernel.Version` and update `web/model.js` and `docs/architecture.md`.
- Script changes: the parser reports a bad line and keeps every valid line; `Parse(Format(s)) == s`; update `script/guide.md` (agents read it through MCP and the CLI), and test a script with one invalid line.
- Viewer: keep `tsc -p web` clean; no bundler or framework without a reason recorded in `docs/decisions.md`.

## Commands

- Full check, the same as CI: `scripts/check.sh` (gofmt, vet, golangci-lint if installed, race tests, core purity and WASM builds, viewer type check; needs Node).
- Server: `go run ./cmd/blockies serve` (port 4318, boards in `~/.blockies/boards`; `--memory` keeps nothing; `--web web` serves the viewer from disk while editing it).
- Examples: `go run ./cmd/blockies demo` loads `examples/*.blk` into the running server.
- Offline: `go run ./cmd/blockies check FILE`, `render FILE [--tick N]`, `guide`.
- Agent: `claude mcp add blockies -- blockies mcp` (uses a running server, or starts one for the session).
- Explorations of the core idea (JS sketches, not the core): `python3 explorations/serve.py`, then `http://localhost:4319/explorations/`.

## Verification

- Run `scripts/check.sh` after changes.
- Viewer changes: look at the examples in a browser in all three views (World, Voxel, Sequence), light and dark, and at phone width.
- Server or MCP changes: `server/server_test.go` covers HTTP, the event stream and MCP over HTTP; also drive `blockies mcp` over stdio once.
