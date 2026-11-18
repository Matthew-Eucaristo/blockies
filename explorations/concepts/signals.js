// Concept 3: Signals. No names, no links: blocks only talk to the blocks
// they touch. Wires ([d]) carry a level 0..15 that fades by one per cell.
// A computer reads the levels around it and drives one neighbour (its `out`).
// The world is drawn as ASCII art, one picture per layer, which is also a
// test of whether an agent can author by drawing.

import { Lattice, add, key } from '../lib/lattice.js';

const helpers = {
  E: [1, 0, 0], W: [-1, 0, 0], S: [0, 1, 0], N: [0, -1, 0], U: [0, 0, 1], D: [0, 0, -1],
  at: (i, dir) => i.dirs[dir.filter((_, k) => k < i.rank).join(',')] ?? 0,
};

export default {
  title: 'Signals',
  tagline: 'blocks only talk to the blocks they touch; wiring is the drawing itself',
  rate: 8,
  drag: false,
  helpers,
  idea: `
<h3>The idea</h3>
<p>There are no names and no links. A block can only sense the blocks it <b>touches</b>, so the picture <i>is</i> the program.</p>
<ul>
<li><b>Wire</b> <code>-</code> is a <code>[d]</code> block that carries a level 0..15, one less per cell. You can watch the signal travel.</li>
<li><b>Lamp</b> <code>o</code> is a <code>[d]</code> block that lights up when a neighbour carries a signal.</li>
<li><b>Computers</b> (letters) run a pure <code>fn(inputs, state) → { out, state }</code>. They read the levels on every face and drive one neighbour (<code>out</code>). The same letter can appear many times, and each copy is its own process.</li>
</ul>
<p>Neighbours are "one step along any axis", so the same rules work in 1D, 2D or 3D. The 3D variant stacks layers, and a wire climbs between them.</p>
<h3>Try</h3>
<ul>
<li>Click a <b>P</b> (lever) to switch it. The AND gate only passes the clock while the lever is on.</li>
<li>The <b>T</b> blocks are toggles: a chain of them counts in binary on the lamps.</li>
<li>Open the 3D variant and use Voxel. In Space-time, a pulse becomes a diagonal streak.</li>
</ul>`,
  notes: `
<h3>What worked</h3>
<ul>
<li><b>The picture is the program.</b> No names, no links: the counter is exactly what you see. It also showed real ripple-carry delay, because each stage reads its neighbour one tick late.</li>
<li><b>The space-time view of the counter is a timing diagram</b>, like a logic analyzer: each lamp's column blinks at half the rate of the one before. This is the second "free diagram" after the sequence diagram in Messages.</li>
<li>Rank-agnostic for real: the 3D tower uses the same code, and the wire climbs through the U/D neighbours.</li>
</ul>
<h3>What broke (and what that teaches)</h3>
<ul>
<li><b>Echoes.</b> The first version relaxed each wire from last tick's neighbours, so a loop of wire kept itself powered and the counter froze at 111. Recomputing levels from the sources every tick fixed it. Local "physics" rules need careful semantics; Minecraft redstone had to add repeaters for the same reason.</li>
<li><b>I authored it wrong twice.</b> A lever faced the wrong way, and a diagonal gap broke the 3D wire. If an LLM makes these mistakes, agents will too. A purely spatial model needs a checker ("this wire touches nothing") or a helper such as <code>wire(from, to)</code>.</li>
<li><b>Isometric 3D is ambiguous without a camera.</b> High cubes look like near ones. A voxel renderer needs orbit and rotation. That is a renderer concern; the core did not care.</li>
</ul>
<h3>Agent cost</h3>
<p>Small (about 1.3k chars), but every character is a position, and positions are where agents fail.</p>`,
  variants: {
    'clock, gate, counter': `
{
  world: [28, 13],
  layers: [\`
............................
.K---A-----T----T----T---...
.....|.....|....|....|......
.P----.....o....o....o......
............................
.K2--------------o..........
............................
....o-----P.................
............................
\`],
  computers: {
    // clock: on for 4 ticks, off for 4
    K: { state: { t: 0 }, out: E, fn: (i, s) => ({ state: { t: s.t + 1 }, out: s.t % 8 < 4 ? 15 : 0 }) },
    // a slower clock
    '2': { state: { t: 0 }, out: E, fn: (i, s) => ({ state: { t: s.t + 1 }, out: s.t % 24 < 2 ? 15 : 0 }) },
    // AND: west input and south input
    A: { state: {}, out: E, fn: (i, s) => ({ state: s, out: at(i, W) && at(i, S) ? 15 : 0 }) },
    // lever: click to switch; drives every wire it touches
    P: { state: { on: true }, out: 'all', click: (s) => ({ on: !s.on }), fn: (i, s) => ({ state: s, out: s.on ? 15 : 0 }) },
    // toggle: flips when its west input falls (a ripple counter), shows on the south lamp
    T: { state: { q: false, was: false }, out: E,
         fn: (i, s) => {
           const hi = at(i, W) > 0;
           const q = !hi && s.was ? !s.q : s.q;
           return { state: { q, was: hi }, out: q ? 15 : 0, side: { S: q ? 15 : 0 } };
         } },
  },
}`,
    '3D tower': `
// The same rules in a rank-3 world. Each layer is one picture; layers stack
// along axis 2. A wire climbs one layer per cell (the U/D neighbours).
{
  world: [10, 8, 4],
  view: 'voxel',
  layers: [\`
..........
.K----....
.....-....
.....-....
..........
..........
..........
..........
\`, \`
..........
..........
..........
.....-....
.....-....
..........
..........
..........
\`, \`
..........
..........
..........
..........
.....-....
.....-....
..........
..........
\`, \`
..........
..........
..........
..........
..........
.....-oo..
......o...
..........
\`],
  computers: {
    K: { state: { t: 0 }, out: E, fn: (i, s) => ({ state: { t: s.t + 1 }, out: s.t % 10 < 5 ? 15 : 0 }) },
  },
}`,
  },

  create(spec) {
    const lat = new Lattice(spec.world);
    const rank = lat.rank;
    const impure = new Set();
    const kind = new Map(); // key -> 'wire' | 'lamp' | letter
    const comps = [];
    spec.layers.forEach((pic, z) => {
      const rows = pic.split('\n').filter((r) => r.trim().length);
      rows.forEach((row, y) => {
        [...row].forEach((ch, x) => {
          const c = rank === 3 ? [x, y, z] : rank === 2 ? [x, y] : [x];
          if (!lat.inside(c) || ch === '.' || ch === ' ') return;
          if (ch === '-' || ch === '|' || ch === '+') kind.set(key(c), 'wire');
          else if (ch === 'o') kind.set(key(c), 'lamp');
          else if (spec.computers[ch]) {
            kind.set(key(c), ch);
            const d = spec.computers[ch];
            comps.push({ id: ch + '@' + c.join(','), ch, def: d, at: c, state: structuredClone(d.state ?? {}), out: 0, side: {} });
          }
        });
      });
    });
    let sig = new Map();
    let time = 0;
    const dirKey = (d) => d.slice(0, rank).join(',');
    const named = { E: helpers.E, W: helpers.W, S: helpers.S, N: helpers.N, U: helpers.U, D: helpers.D };

    return {
      shape: spec.world,
      impure,
      get time() {
        return time;
      },
      tick() {
        // 1. Computers read the current levels around them (pure).
        for (const c of comps) {
          const dirs = {};
          let max = 0;
          for (const d of lat.dirs) {
            const n = add(c.at, d);
            const v = lat.inside(n) && kind.get(key(n)) === 'wire' ? sig.get(key(n)) ?? 0 : 0;
            dirs[d.join(',')] = v;
            max = Math.max(max, v);
          }
          const r = pure(c.def.fn, [{ dirs, max, rank }, c.state], () => impure.add(c.ch));
          c.state = r.state;
          c.out = r.out ?? 0;
          c.side = r.side ?? {};
        }
        // 2. Wires take the strongest neighbour minus one, plus what computers drive into them.
        const inject = new Map();
        const push = (cell, v) => {
          if (!lat.inside(cell)) return;
          const k = key(cell);
          inject.set(k, Math.max(inject.get(k) ?? 0, v));
        };
        for (const c of comps) {
          if (c.out && c.def.out === 'all') for (const n of lat.neighbors(c.at)) push(n, c.out);
          else if (c.out) push(add(c.at, c.def.out ?? helpers.E), c.out);
          for (const [dn, v] of Object.entries(c.side)) if (v) push(add(c.at, named[dn]), v);
        }
        // Levels are recomputed from the sources every tick (strongest path,
        // minus one per cell). An earlier version relaxed from last tick's
        // levels instead, and signals echoed: a loop of wire kept itself on.
        const next = new Map();
        const buckets = Array.from({ length: 16 }, () => []);
        for (const [k, v] of inject) if (kind.get(k) === 'wire') buckets[Math.min(15, v)].push(k);
        for (let lv = 15; lv > 0; lv--) {
          for (const k of buckets[lv]) {
            if ((next.get(k) ?? 0) >= lv) continue;
            next.set(k, lv);
            for (const n of lat.neighbors(k.split(',').map(Number))) {
              const nk = key(n);
              if (kind.get(nk) === 'wire' && (next.get(nk) ?? 0) < lv - 1) buckets[lv - 1].push(nk);
            }
          }
        }
        for (const [k, t] of kind) {
          if (t !== 'lamp') continue;
          const c = k.split(',').map(Number);
          let lit = (inject.get(k) ?? 0) > 0;
          for (const n of lat.neighbors(c)) if ((next.get(key(n)) ?? 0) > 0) lit = true;
          if (lit) next.set(k, 15);
        }
        sig = next;
        time++;
      },
      items() {
        const items = [];
        for (const [k, t] of kind) {
          const c = k.split(',').map(Number);
          const v = sig.get(k) ?? 0;
          if (t === 'wire') items.push({ id: k, at: c, kind: 'wire', color: '#36c5f0', glow: v / 15 });
          else if (t === 'lamp') items.push({ id: k, at: c, kind: 'd', color: v ? '#ffcc33' : '#5a6070', glow: v ? 1 : 0 });
        }
        for (const c of comps) {
          items.push({ id: c.id, owner: c.id, at: c.at, kind: 'c', color: c.out ? '#e0524f' : '#8a90a0', label: c.ch, hatch: c.def.click && !c.state.on });
        }
        return items;
      },
      processes() {
        return comps.map((c) => ({ id: c.id, label: c.ch + ' ' + c.at.join(','), status: c.out ? 'run' : 'wait' }));
      },
      click(at) {
        const c = comps.find((x) => x.at.every((v, i) => v === at[i]));
        if (c && c.def.click) c.state = c.def.click(c.state);
      },
      status() {
        return `${comps.length} computers · ${[...kind.values()].filter((t) => t === 'wire').length} wire cells · ${sig.size} powered`;
      },
    };
  },
};

function pure(fn, args, onImpure) {
  const a = fn(...structuredClone(args));
  const b = fn(...structuredClone(args));
  if (JSON.stringify(a) !== JSON.stringify(b)) onImpure();
  return a;
}
