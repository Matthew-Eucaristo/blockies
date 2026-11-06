# Blockies

**Organic living blocks.** A whiteboard where every drawing is alive: elements have a small, declared set of states, and everything animates between them. Compute blocks `[c]` run the logic; drawing blocks `[d]` compose themselves into whatever the computer asks for.

> Status: early exploration. Expect everything to change.

## Ideas

- **Few choices, on purpose.** A small, closed vocabulary of pieces, so humans and AI agents produce valid, predictable boards.
- **State is the unit of everything.** An element is a list of states plus how to draw each one. Dragging, linking, step playback and animation all come from that.
- **`[c]` computes, `[d]` composes.** Compute blocks emit targets; drawing blocks travel, fade and recolor toward them.
- **Core emits state, runtimes render.** A headless core holds board state; each runtime (terminal, web, agents) animates what it can and skips what it can't.

## Prototypes

Zero-dependency, single-file HTML:

- `kinetic.html`: semantic board with 8 pieces (box, arrow, token, switch, slider, bar, card, note), a line-based DSL, links, steps, and an agent prompt.
- `blocks.html`: a `[c]` program (Eiffel tower, bubble sort, waves) driving a pool of pixel blocks.

```bash
python3 -m http.server 4317
```

Then open http://localhost:4317/kinetic.html or http://localhost:4317/blocks.html.

## Credits

Original idea by Edward Manhattan. Built with Matthew Eucaristo.
