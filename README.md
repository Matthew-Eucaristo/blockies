# Blockies

**Organic living blocks.** A whiteboard where every drawing is alive. Elements have a small, declared set of states, and everything animates between them. Compute blocks `[c]` run the logic; drawing blocks `[d]` compose themselves into whatever the computer asks for.

> Status: v0.1 foundation in Rust. The board engine, the block field, the server, the terminal runtime, the CLI and the WASM core all work. The web runtime is next.

## Ideas

- **Few choices, on purpose.** There are 8 pieces and 3 statements, so humans and AI agents produce valid, predictable boards.
- **State is the unit of everything.** An element is a list of states plus how to draw each one. Dragging, links, step playback and animation all come from that.
- **`[c]` computes, `[d]` composes.** Compute blocks emit targets; drawing blocks travel, fade and recolour toward them.
- **The owner holds state, runtimes render it.** The terminal, the web and agents each animate what they can and skip what they can't. The block field runs in deterministic lockstep everywhere.

## Try it

You need Rust via [rustup](https://rustup.rs). `rust-toolchain.toml` pins the version.

Run a board locally in the terminal:

```bash
cargo run --release -p blockies-cli -- tui --example web
```

Try the block field: press `tab`, then drag to paint.

```bash
cargo run --release -p blockies-cli -- tui --example sandbox
```

Or go live, so several terminals and agents share one board. Start the server:

```bash
cargo run --release -p blockies-cli -- serve
```

Load the example board onto it:

```bash
cargo run --release -p blockies-cli -- new web crates/blockies-dsl/examples/web.blk
```

Open the board in a terminal:

```bash
cargo run --release -p blockies-cli -- tui web
```

Then change it from another shell, as an agent would; every open terminal animates the change:

```bash
cargo run --release -p blockies-cli -- move web req db
```

`blockies prompt` prints the whole vocabulary for any LLM. `blockies check board.blk` reports problems line by line. Add `--json` to any command for machine-readable output.

## The DSL

```text
box api "API server" at 720 220 states idle|busy|overloaded
arrow a1 browser -> api states off|on
token req "GET /home" on browser, api, browser
slider load "Traffic" at 640 430 states quiet|normal|spike
link load -> api
step "API is busy" req=1 a1=on
sim 512 256 scene calm seed 7
```

The pieces are box, arrow, token, switch, slider, bar, card and note. The statements are `link` (targets follow a source proportionally), `step` (cumulative, played like slides) and `sim` (a block field of sand, water and walls). See [crates/blockies-dsl/examples](crates/blockies-dsl/examples).

## Layout

Rust workspace in [crates/](crates), described in [docs/architecture.md](docs/architecture.md). The original single-file prototypes are in [prototypes/](prototypes): `kinetic.html` and `blocks.html`. Serve them with `python3 -m http.server 4317`.

```bash
scripts/check.sh
```

This runs formatting, clippy, every test, and the native vs WASM determinism check.

## Credits

Original idea by Edward Manhattan. Built with Matthew Eucaristo.
