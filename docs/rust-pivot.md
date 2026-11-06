# Rust pivot (research, proposal)

Status: **accepted 2026-11-07.** Research notes from 2026-11-06; the implementation lives in `crates/` and is described in `architecture.md`. A Go vs Rust benchmark (same spec in both languages) backed the decision; see "Benchmark" below.

## Why the pivot

From Edward's brainstorm (voice note and chat, 2026-11-06):

- **Scale changed.** The target is millions to tens of millions of entities in one view (one pixel = one block), inspired by Noita, where every pixel is simulated. Blockies goes one level up: some pixels can think.
- **No garbage collector.** GC pauses are non-deterministic. Fine for most backends, not for a real-time engine at this scale.
- **Rust over C, C++ or Zig** because its strict compiler (borrow checker, types, clippy) catches mistakes at compile time. That makes it a better feedback loop for agentic development.
- **One language for the engine everywhere.** Engine and rendering logic in Rust, compiled to WASM for the web. A Go client would mean doing the work twice.
- **TypeScript only at the edges:** the JS SDK (many apps are JS, so it is required) and the web app shell (Svelte-style reactivity ideas, possibly Foldkit).
- **Three clients:** web, native (TUI), and a CLI. The CLI is how agents drive boards (`blockies move a b`); every client sees the change live. MCP comes later, possibly metered or paid like Figma's.

## The consequence that shapes everything: lockstep, not state streaming

A server cannot stream millions of cells to clients. 1M cells × 1 byte is 1 MB per frame, about 60 MB/s per client at 60 fps. The first terminal spike sent about 2 KB per tick only because it moved about 1,800 blocks.

So the core becomes **deterministic lockstep**:

1. The board actor on the server **orders intents** and broadcasts `(tick, intents)`.
2. Every client runs the **same Rust sim** (native or WASM) and gets the same result.
3. Clients send a **state hash** periodically; on mismatch or join, they get a compressed snapshot.
4. Semantic patches (pieces, states, steps: the `kinetic.html` layer) stay as small state patches.

This makes the existing rule "`[c]` must be deterministic" non-negotiable:

- Integer or fixed-point math in the sim (`fixed` crate if fractions are needed); `clippy::float_arithmetic` denied in the sim crate.
- No `HashMap` iteration order (use `BTreeMap` or a fixed hasher), no relaxed SIMD, no clock.
- Own small PRNG (SplitMix or PCG); per-cell randomness from `hash(seed, tick, x, y)`, so results do not depend on thread scheduling.
- `[d]` springs are client-only and cosmetic, so `f32` is fine there.
- CI runs the same sim natively and in WASM for N ticks and compares hashes.

## Simulating millions

How Noita does it (GDC 2019, Petri Purho): the world is split into 64×64 chunks, each with a dirty rect; threads update chunks in a checkerboard so parallel chunks never write the same cells. **Cost scales with active cells, not total cells.**

- **CPU first.** Flat structure-of-arrays per chunk (for example `[u8; 4096]` per field), `rayon` for the checkerboard passes. Estimate: 1M active cells in about 1–2 ms on 8 cores. 10M cells that are *all* active hits memory bandwidth; fine when most are dormant.
- **No ECS for cells.** An ECS (bevy_ecs, hecs) only for the few "thinking" pieces, if at all.
- **GPU (wgpu compute) later**, only for cosmetic effects. An authoritative GPU sim would need GPUs on the server and bit-exact WGSL rules.
- **Prior art:** Sandspiel (Rust → WASM sim, WebGL renders a texture straight from WASM memory, 4 bytes per cell, no copies), Astratomic (Noita-like in Bevy, chunks, active-only updates).

## Rendering per runtime

- **Web:** one texture per view (one pixel per cell, pan and zoom in the shader). WebGL2 baseline, WebGPU as an upgrade. WebGPU is default in Chrome (desktop, Android), Firefox (Windows, macOS) and Safari 26, but not on most Linux setups, so the fallback stays. A small TS WebGL2 blit keeps the WASM small.
- **Terminal:** a 200×60 terminal is only 24k half-block pixels, so the TUI is always a **downsampled viewport** plus a minimap. `ratatui` 0.30 redraws only changed cells. `ratatui-image` (sixel, kitty, iTerm2) gives real pixels in Ghostty or kitty; animation frame rate unverified, treat as a hi-res mode.
- `libghostty` is for *emulating* a terminal, not for drawing a TUI, so it does not apply here.

## Stack per layer

| Layer | Pick | Notes |
|---|---|---|
| Async runtime, HTTP, WebSocket | tokio 1.54, axum 0.8 (`ws`), tower-http | |
| Board actor | plain `tokio::spawn` task per board, `mpsc` inbox, `broadcast` out | kameo or ractor later if supervision is needed; avoid actix (no release since 2024) |
| Store | rusqlite 0.41 behind a `Store` trait | Append-only intent log + snapshots gives replay for free; sqlx only for Postgres |
| Wire format | serde_json (agents, debug), postcard (binary), zstd/lz4 for snapshots | **Not bincode**: archived, flagged unmaintained (RUSTSEC-2025-0141) |
| CLI | clap 4 | `--json` output, stable exit codes, JSON errors |
| MCP (later) | rmcp 3.9 (official Rust SDK) | stdio + streamable HTTP mounted on the same axum app |
| TUI | ratatui 0.30, optional ratatui-image | |
| WASM | wasm-bindgen 0.2.130, wasm-pack 0.15 (maintained again under the `wasm-bindgen` org) | Few hundred KB target with `opt-level="z"` + `wasm-opt -Oz` |
| Node | same WASM package; napi-rs only if native threads are needed | |

### CLI first, MCP second

Edward's instinct is right for coding agents: a CLI runs through the shell and costs no context until used. MCP matters for clients without a shell (Claude Desktop, web) and for hosted, metered access. Both should be thin adapters over one shared operations crate so they never drift.

### Web shell: Foldkit?

Foldkit (foldkit.dev) is an Elm-architecture TypeScript framework on Effect 4. MIT, about 1.2k stars, pre-1.0 (0.170.0 on 2026-10-29, breaking changes in minor releases). Its tests are Story tests (state machine) and Scene tests (view via accessible locators, no DOM), which are **not** real browser end-to-end tests. It does support embedding (Mounts, Ports, `Runtime.embed`).

Reasonable for the app shell **if** the engine sits behind a framework-neutral boundary: the JS SDK exposes `mount(canvas, opts)`, `send(intent)`, `on(event)`, and the shell only talks through that. Keep Playwright for real end-to-end tests.

## Proposed workspace

```
crates/
  blockies-model     ids, pieces, declared states, materials (no_std, serde)
  blockies-sim       [c] runtime: chunked grid, dirty rects, checkerboard; integer-only; `parallel` = rayon
  blockies-protocol  intents, tick batches, semantic patches, snapshots, version tag
  blockies-dsl       line DSL parser (per-line errors, keeps valid lines)
  blockies-anim      [d] springs and interpolation (f32, client-only)
  blockies-ops       shared command layer for CLI, MCP and server
  blockies-store     Store trait + rusqlite
  blockies-server    axum WebSocket, board actor, Transport trait, /mcp mount
  blockies-tui       ratatui viewport and downsampler
  blockies-cli       the `blockies` binary: serve, tui, mcp, and agent commands (--json)
  blockies-wasm      wasm-bindgen facade exposing the RGBA framebuffer
packages/
  sdk-js             TS wrapper + WebGL2/WebGPU texture renderer
  web                app shell (Foldkit or other) using sdk-js through mount()
```

`blockies-model`, `blockies-sim` and `blockies-protocol` stay free of I/O and `std::time` so they compile to `wasm32-unknown-unknown` and stay deterministic.

## Local setup (macOS, Apple Silicon)

Use **rustup**, the official installer. It gives toolchain switching, per-project pinning (`rust-toolchain.toml`), extra targets like WASM, and components. Do **not** `brew install rust`: it is one fixed toolchain without `rustup target add`, and it silently shadows rustup if it comes first in `PATH`. `brew install rustup` works too, but it is keg-only and needs a manual `PATH` step plus `rustup default stable`.

```bash
xcode-select --install
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --component clippy,rustfmt,rust-analyzer
. "$HOME/.cargo/env"
rustup target add wasm32-unknown-unknown
cargo install --locked bacon cargo-nextest cargo-deny cargo-sweep
```

- **Size:** the toolchain is roughly 0.5–1 GB with the minimal profile (docs alone are about 600 MB, which minimal skips); Go is about 150–200 MB installed. The real cost is `target/`, which can reach 5–30 GB per workspace. Mitigate with `[profile.dev] debug = "line-tables-only"`, no debug info for dependencies, and `cargo sweep --time 14`.
- **Current versions:** Rust 1.99.1 (2026-10-15), edition 2024, `resolver = "3"` for the workspace. macOS links with Apple's ld-prime, already fast.
- **Loop:** `cargo check` while editing, `bacon` as the watcher (cargo-watch is deprecated), `cargo nextest run`, clippy pedantic as warnings at workspace level, `cargo fmt --check`, `cargo-deny` for licenses and advisories.
- **Editor:** rust-analyzer (VS Code extension, built into Zed).
- Pin the toolchain in `rust-toolchain.toml` (with `profile = "minimal"`, otherwise rustup installs docs anyway).

## The compiler as an agent feedback loop

Mostly practitioner evidence, not rigorous studies: compiler, clippy and test errors act as back pressure that lets agents correct themselves. Counter-evidence: Rust costs more iterations and tokens per change than Python. For a deterministic core, the trade is worth it.

Formal verification, if wanted: **Kani** (bounded model checking, runs in CI) is the practical choice for proving `blockies-sim` invariants. Verus is heavier; Creusot and Prusti remain research-grade.

## Zig?

Zig 0.17 (no 1.0 date) breaks code every minor release, and its WASM and WebGPU ecosystem is thin. Ghostty is a great Zig project, but for a deterministic core shared by TUI, server, WASM and CLI, Rust is the safer choice.

## Benchmark (2026-11-07)

A separate repo, `blockies-bench` (local, not published yet), implements one spec in Go and Rust with the same algorithms, capacities and thread counts. Both produce identical sim hashes. Results on an Apple M4:

- **Blocks sim:** Rust was 1.2-1.35x faster at 1M-67M cells. Settled worlds tied (chunk sleeping dominates).
- **Springs:** Rust was 3.5-4x faster on one core (auto-vectorisation).
- **Server memory:** Rust used 3-7x less (117 MB vs 513 MB for 10k idle WebSockets), with 18-27% less CPU at the same load.
- **Go GC:** pauses were 0.1-0.5 ms normally, 21 ms under memory pressure.
- **Where Go won or tied:** build time (4 s vs 23 s clean), and 200 writers contending on one tokio mpsc (Go was 1.6x faster).
- **Network ceiling:** on macOS loopback both servers hit the same limit, about 300k packets/s. Per-tick batching raised it 4x for both.

The Go code is gone from this repo. `blockies-bench` keeps both implementations for future comparisons.

## First Rust spikes (done)

1. **1M-cell chunked sim:** done in `blockies-sim`. Its native and WASM hashes are equal (`scripts/wasm-determinism.mjs`).
2. **Ratatui viewport:** done in `blockies-tui`. It takes 0.1-0.25 ms per frame at 200×60.
3. **Web texture renderer:** next. `blockies-wasm` already exposes `Field::pixels()` (RGBA in WASM memory).

## Sources

- Rust install: [rust-lang.org/tools/install](https://www.rust-lang.org/tools/install), [rustup: already installed Rust](https://rust-lang.github.io/rustup/installation/already-installed-rust.html), [rustup profiles](https://rust-lang.github.io/rustup/concepts/profiles.html), [brew rust](https://formulae.brew.sh/formula/rust), [brew rustup](https://formulae.brew.sh/formula/rustup), [cargo profiles](https://doc.rust-lang.org/cargo/reference/profiles.html), [releases](https://blog.rust-lang.org/releases/)
- Noita: [GDC 2019 talk](https://gdcvault.com/play/1025695/Exploring-the-Tech-and-Design), [video](https://www.youtube.com/watch?v=DCAMRAbMq7c); prior art: [Sandspiel](https://github.com/MaxBittker/sandspiel), [Astratomic](https://github.com/spicylobstergames/astratomic)
- WebGPU: [implementation status](https://github.com/gpuweb/gpuweb/wiki/Implementation-Status)
- Crates: [ratatui-image](https://github.com/ratatui/ratatui-image), [rmcp](https://github.com/modelcontextprotocol/rust-sdk), [wasm-bindgen org move](https://blog.rust-lang.org/inside-rust/2025/07/21/sunsetting-the-rustwasm-github-org), [bincode advisory](https://osv.dev/vulnerability/RUSTSEC-2025-0141), [float semantics RFC 3514](https://rust-lang.github.io/rfcs/3514-float-semantics.html)
- Foldkit: [foldkit.dev](https://foldkit.dev), [repo](https://github.com/foldkit/foldkit)
- Agents and verification: [Rust feedback loop post](https://marclove.pages.dev/blog/2025-12-13-rust-feedback-loop-catches-claude-code-hallucinations-dead-code-bugs), [Kani std verification](https://github.com/model-checking/verify-rust-std), [Verus at Amazon](https://amazon.science/blog/developing-provably-correct-rust-code-with-verus)
- Zig: [downloads](https://ziglang.org/download/)

Unverified: Noita's exact pass details, ratatui-image animation frame rate, wgpu-in-WASM size, the CPU ns-per-cell estimates (spike 1 settles this).
