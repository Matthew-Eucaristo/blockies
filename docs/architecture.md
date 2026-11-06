# Architecture (v0.1, Rust)

Status: implemented in `crates/`, 2026-11-07. Why Rust and the lockstep model: [rust-pivot.md](rust-pivot.md).

## The two rules

1. **The owner holds state, runtimes render it.** Every change goes through one board actor that validates it and broadcasts the result. Runtimes animate toward that state with springs, and skip whatever they cannot draw.
2. **The block field runs in lockstep, it is never streamed.** The owner orders paints into ticks and broadcasts `(tick, paints)`. Every client runs the same deterministic sim and reports a hash now and then. A mismatch or a join is answered with a compressed snapshot.

```
  agents ── blockies CLI ── HTTP ─┐
                                  ▼
  TUI ─┐                  ┌───────────────────────┐   SQLite
  web ─┼── WebSocket v1 ──│ board actor (1/board) │── snapshot + op log
  ... ─┘   ops ▲ ▼ patches│  Doc (ops) + World    │   (writer thread)
               ticks      └───────────────────────┘
```

## Crates

| Crate | Responsibility | I/O |
|---|---|---|
| `blockies-model` | The vocabulary: 8 piece kinds, states, links, steps, `sim` spec, materials, state tones | none (WASM-safe) |
| `blockies-dsl` | Line DSL parser (per-line errors, valid lines still load), canonical formatter, agent prompt, examples | none |
| `blockies-ops` | **The one command layer**: `Op` → validate → apply → `Patch`es. Links, cumulative steps, token moves, stable error codes | none |
| `blockies-sim` | `[c]` block field: integer-only Margolus sand/water, 64×64 tiles, exact chunk sleeping, rayon tile rows (`parallel` feature) | none |
| `blockies-anim` | `[d]` springs (`f32`, client-only) and struct-of-arrays bodies | none |
| `blockies-protocol` | Wire messages (JSON, tag `t`), LZ4 snapshots, HTTP view types | none |
| `blockies-store` | `Store` trait, with SQLite as its implementation: one snapshot row per board plus an append-only op log | SQLite |
| `blockies-server` | axum: one tokio task per board, WebSocket clients, lockstep clock, REST for agents | network |
| `blockies-tui` | ratatui runtime: boards with springs, the field as half-block pixels, local or live | terminal |
| `blockies-cli` | The `blockies` binary: `serve`, `tui`, and agent commands with `--json` | all |
| `blockies-wasm` | wasm-bindgen facade, the base for the JS SDK: `Field` (lockstep, `pixels()` pointer for WebGL), `check`, `formatDsl` | JS |

The first five crates hold no I/O and no clock, so they compile to `wasm32-unknown-unknown` and stay deterministic.

## Protocol v1

JSON text frames, one message per frame. Board names: 1-64 of `a-z 0-9 - _`.

| Direction | Message | Purpose |
|---|---|---|
| server → client, on join | `welcome {protocol, board, you, rev, dsl, states, step, sim?}` | Full board, plus a field snapshot if the board has one |
| client → server | `op {id, op}` | Any `blockies_ops::Op` (`set`, `move`, `cycle`, `step`, `load`, `paint`) |
| server → sender | `ack {id, rev, warnings?}` / `reject {id, code, detail}` | Stable codes: `unknown_piece`, `unknown_state`, `driven`, `not_on_route`, `no_steps`, `bad_step`, `bad_board`, `no_sim` |
| server → all | `patch {rev, by, patches}` | `state`, `step` or `board` (full replace) |
| server → all | `tick {tick, paints}` | Apply the paints in order, then step once (before: `world.tick() == tick`) |
| client → server | `hash {tick, hash}` every 30 ticks | Hex string (u64 does not fit a JS number) |
| client → server / server → client | `resync` / `snapshot {sim}` | LZ4 + base64 cells at a tick |

HTTP (agents, CLI): `GET /api/boards`, `GET|PUT /api/boards/{board}` (PUT body = DSL), `POST /api/boards/{board}/ops` (JSON op). Errors are `{code, detail}` with 4xx.

## Board actor

- Owns a `Doc` (pieces and states) and, if the DSL has `sim W H`, a `World`. No locks: it handles one command at a time from a bounded mailbox (4096).
- **Fan-out:** each message is serialised once, and subscribers share it by refcount. A subscriber whose 512-frame outbox is full is dropped instead of slowing the board; it reconnects and resyncs.
- **Lockstep clock:** 33 ms by default. It runs only while someone is watching a board that has a field. The owner keeps the last 64 hashes to check clients.
- **Persistence:** every accepted non-paint op is committed (snapshot + log row) by one writer thread, in order. Actors never wait on disk. The field itself is not stored; the DSL plus seed define how it starts.

## Determinism rules (`blockies-sim`)

- Integer math only (`clippy::float_arithmetic` is denied in the crate), no clock, no `HashMap` iteration, randomness only from `mix(x, y, tick, seed)`.
- The sleeping and parallel paths must give the same result as the serial, everything-awake path. Tests check this, including with paints.
- Golden hashes match the Go/Rust benchmark spec. `scripts/wasm-determinism.mjs` checks that WASM matches native.

## Measured (Apple M4, release build)

| What | Number |
|---|---|
| Field, everything moving, 17M cells, 10 threads | 0.64 ms/tick (26 G cells/s); serial 3.1 ms |
| Field, settled world, 17M cells | 0.13 ms/tick (3% of tiles awake) |
| TUI frame (update + draw), 200×60 | board 0.1 ms, field 0.23 ms |
| WASM bundle (`release-wasm`, before `wasm-opt`) | 309 KB |
| Live board, 100 clients × 10 ops/s (100k patches/s) | ack p50 15 ms, p99 29 ms, no drops |
| Live board, 200 clients × 10 ops/s (400k patches/s) | saturates (p99 0.46 s, drops): macOS loopback limit, see rust-pivot.md |

Reproduce with:

```bash
cargo run --release -p blockies-sim --example bench
```

```bash
cargo run --release -p blockies-tui --example render_bench
```

```bash
cargo run --release -p blockies-server --example load
```

## Next

1. **Patch batching per tick** (one frame per board per tick instead of one per op). In the benchmark this raised the ceiling 4×.
2. **Web runtime:** `packages/sdk-js` (`mount(canvas)`, `send(op)`, `on(event)`) over `blockies-wasm`, with a WebGL2 texture straight from `Field::pixels()`, and the app shell on top.
3. **MCP:** `rmcp`, a thin adapter over `blockies-ops` mounted on the same axum app (same codes as the CLI).
4. **Thinking pieces:** `[c]` programs that drive `[d]` bodies (`blockies-anim::Bodies`) with identity, as in `prototypes/blocks.html`.
5. **Kani** proofs for sim invariants (mass conservation, walls never move) in CI.
