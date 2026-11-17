# Blockies (working name)

## Philosophy (invariants)

- **Few choices, on purpose.** Agents get a small, closed set of primitives: 8 pieces and the statements `link`, `step` and `sim`. Fewer choices means valid, predictable output and no "illusion of choice". New primitives must justify themselves.

## Rules for changes

- Every change to a board goes through `blockies_ops::Op` → `Doc::apply`. Do not add a second path in the server, the CLI, the TUI or MCP. Error `code`s are stable API for agents.
- The sim is integer-only (`clippy::float_arithmetic` is denied there): no clock, no `HashMap` iteration, randomness only from `mix()`. If a rule changes, update the golden hashes in `blockies-sim` tests and check the WASM determinism script.
- Protocol changes bump `PROTOCOL` in `blockies-protocol` and update the table in `docs/architecture.md`.
- DSL changes: the parser reports a bad line and keeps every valid line. `format()` must round-trip (`parse(format(b)) == b`).

## Verification

- For TUI changes, also run the TUI in a real terminal, and render tests with `ratatui::backend::TestBackend` at small and large sizes (no panics at 1×1).
- For DSL changes, include a board with one invalid line in a test. The error must be listed and every valid line must still load.
