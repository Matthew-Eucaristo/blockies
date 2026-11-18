# Explorations

Eight playable versions of the Blockies core idea, built to help Matthew and Edward decide on the core architecture. These are JavaScript sketches, not the core. The direction chosen from them (Kernel, with Nest and the views) is now the Go core at the repository root.

```bash
python3 explorations/serve.py
```

Then open `http://localhost:4319/explorations/`. The index page has the cards, the discoveries, a comparison, questions for Edward, and ideas not built yet.

| # | Concept | One line |
|---|---|---|
| 1 | Organism | Every computer is a pure function with a body; the shape is the state |
| 2 | Messages | Actors; messages walk the lattice, so backlog and latency are visible |
| 3 | Signals | Blocks only sense what they touch; the ASCII picture is the program |
| 4 | Rules | A computer is a list of rewrite rules (data); the same rule works in 2D and 3D |
| 5 | Mind | An agent's work as a living process tree, one block per process |
| 6 | Sheet | A spreadsheet in space; changes travel as pulses |
| 7 | Kernel | Synthesis: processes with bodies, talking only by messages |
| 8 | Nest | A computer can contain a world; complexity comes from depth |

## Discoveries

1. **Diagrams are views of running worlds.** A 1D world stacked over time is a sequence diagram (Messages), a timing diagram (Signals) or a Gantt chart (Mind). None of them was drawn.
2. **Agents should say what, never where.** Coordinates are where authoring fails. Kernel lets the lattice place bodies and children.
3. **Space must be owned, and cores need a moat.** Bodies collide unless cells have owners; a body must never touch another computer's core.

## Layout

- `lib/lattice.js`: a lattice of any rank. Nothing in it names x, y or z. The Go package `lattice` is the same idea in the real core (rank 1 to 4, deterministic, WASM-safe).
- `lib/stage.js`: springs by identity plus renderers: flat, voxel (iso) and space-time (time as an axis; a 1D world draws as a 2D diagram).
- `lib/shell.js`: page, clock, process bar, editable spec, and a purity check (runs each function twice).
- `concepts/*.js`: one module per concept, with `idea`, `notes`, `spec` or `variants`, and `create(spec)`.
