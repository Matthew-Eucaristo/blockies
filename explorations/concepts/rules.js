// Concept 4: Rules. A computer is a list of rewrite rules ("WBB" → "WAW"),
// not code. A rule is a line of symbols and matches along any axis in either
// direction, so one rule works at any rank. Shapes grow out of the computer
// block cell by cell. Rules are data, so they are pure by construction.

import { Lattice, add, key, mix } from '../lib/lattice.js';

const PALETTE = { B: '#1d2230', W: '#e8ebf2', A: '#b9c1d0', R: '#e0524f', G: '#2e9e6e', U: '#3b6ff5', P: '#7a5af5', S: '#d9b56b', Y: '#ffcc33' };

export default {
  title: 'Rules',
  tagline: 'a computer is a list of rewrite rules; shapes grow by rules, and the same rule works in 2D and 3D',
  rate: 12,
  idea: `
<h3>The idea</h3>
<p>A computer holds an ordered list of <b>rules</b>. A rule is a line of symbols, for example <code>WBB → WAW</code>, meaning "a W next to two empties becomes W, A, W". Each tick the computer applies its first rule that matches somewhere in its region (a seeded pick among the matches), a few times.</p>
<ul>
<li>A rule is written once and matches <b>along every axis, in both directions</b>. The same text grows a maze in 2D and a coral in 3D.</li>
<li>Rules are data, not code. That makes them pure by construction, tiny for an agent to write, and diffable.</li>
<li>The computer block is the seed; everything it grows is its body (<code>[d]</code>).</li>
</ul>
<p>This is close to <i>MarkovJunior</i> (Maxim Gumin) and to the falling-sand engine already in <code>blockies-sim</code>.</p>
<h3>Try</h3>
<ul>
<li>Watch two processes compete for space: a maze and a coral.</li>
<li>Click an empty cell to drop a new seed for the coral.</li>
<li>Open the 3D variant: the same maze rule makes a 3D labyrinth.</li>
</ul>`,
  notes: `
<h3>What worked</h3>
<ul>
<li><b>One line of data makes a maze.</b> <code>['WBB','WAW']</code> is the whole program. The agent cost is the smallest of all concepts (about 300 chars).</li>
<li><b>The same rule really works in 3D</b> and makes a labyrinth of cubes. Nothing in the rule mentions an axis.</li>
<li><b>Two processes competed for space without any extra design.</b> Rules only write into empty cells, so the maze and the coral share the world first come, first served. This is a simple answer to "who owns a cell?" that Organism and Messages lacked.</li>
<li>Pure by construction: rules are data, so there is nothing to check.</li>
</ul>
<h3>What broke</h3>
<ul>
<li><b>Good for growing things, poor for explaining things.</b> Rules speak about space, not meaning. A web architecture as rewrite rules would be a puzzle, not an explanation.</li>
<li><b>Hard to debug.</b> "Why did this cell become red?" has no answer without a trace of which rule fired where. The renderer could show the last rewrite per cell.</li>
<li>A dense 3D result is unreadable as voxels. It needs slicing, cutaways or transparency, which again is a renderer concern.</li>
<li>The naive matcher scans the whole region every step; a real engine needs incremental matching (as MarkovJunior does).</li>
</ul>`,
  variants: {
    'maze and coral': `
{
  world: [41, 25],
  computers: {
    maze: {
      at: [1, 1], color: '#e8ebf2', seedSymbol: 'W', speed: 3,
      rules: [ ['WBB', 'WAW'] ],
    },
    coral: {
      at: [32, 18], color: '#e0524f', seedSymbol: 'R', speed: 2, radius: 9,
      rules: [ ['RB', 'RR'] ],
      chance: 0.35,
    },
  },
}`,
    '3D maze': `
// The maze rule from the 2D variant, unchanged, in a rank-3 world.
{
  world: [13, 13, 7],
  view: 'voxel',
  computers: {
    maze: { at: [1, 1, 1], color: '#e8ebf2', seedSymbol: 'W', speed: 4, rules: [ ['WBB', 'WAW'] ] },
  },
}`,
    'growth and decay': `
// Two processes that disagree: one grows green, one turns old green into
// sand that falls (a rule restricted to one direction: down along axis 1).
{
  world: [40, 24],
  computers: {
    grow: { at: [20, 12], color: '#2e9e6e', seedSymbol: 'G', speed: 6, rules: [ ['GB', 'GG'] ], chance: 0.5 },
    decay: { at: [20, 0], color: '#d9b56b', seedSymbol: 'S', speed: 8,
      rules: [ ['SB', 'BS', { dirs: [[0, 1]] }], ['GGG', 'GSG'] ] },
  },
}`,
  },

  create(spec) {
    const lat = new Lattice(spec.world);
    const rank = lat.rank;
    const cells = new Map(); // key -> symbol (empty = 'B', not stored)
    const owner = new Map(); // key -> computer name (who wrote it last)
    const comps = Object.entries(spec.computers).map(([name, d]) => {
      cells.set(key(d.at), d.seedSymbol ?? 'W');
      owner.set(key(d.at), name);
      return { name, def: d, at: d.at, applied: 0, idle: 0 };
    });
    let time = 0;
    const get = (c) => (lat.inside(c) ? cells.get(key(c)) ?? 'B' : null);

    function matches(comp, rule) {
      const [pin, , opts] = rule;
      const dirs = opts?.dirs ? opts.dirs.map((d) => d.slice(0, rank)) : lat.dirs;
      const out = [];
      const r = comp.def.radius;
      for (let i = 0; i < lat.size; i++) {
        const c = lat.coord(i);
        if (r && c.some((v, a) => Math.abs(v - comp.at[a]) > r)) continue;
        if (get(c) !== pin[0]) continue;
        for (const d of dirs) {
          let ok = true;
          let p = c;
          for (let k = 1; k < pin.length && ok; k++) {
            p = add(p, d);
            if (get(p) !== pin[k]) ok = false;
          }
          if (ok) out.push([c, d]);
        }
      }
      return out;
    }

    function step(comp) {
      for (const rule of comp.def.rules) {
        const ms = matches(comp, rule);
        if (!ms.length) continue;
        const [c, d] = ms[mix(time, comp.applied, comp.name.length) % ms.length];
        const chance = comp.def.chance ?? 1;
        if (chance < 1 && (mix(time, comp.applied, 7) % 1000) / 1000 > chance) {
          comp.applied++;
          return true;
        }
        let p = c;
        for (let k = 0; k < rule[1].length; k++) {
          const sym = rule[1][k];
          if (sym === 'B') {
            cells.delete(key(p));
            owner.delete(key(p));
          } else {
            cells.set(key(p), sym);
            owner.set(key(p), comp.name);
          }
          p = add(p, d);
        }
        comp.applied++;
        return true;
      }
      return false;
    }

    return {
      shape: spec.world,
      get time() {
        return time;
      },
      tick() {
        for (const comp of comps) {
          let did = false;
          for (let k = 0; k < (comp.def.speed ?? 1); k++) did = step(comp) || did;
          comp.idle = did ? 0 : comp.idle + 1;
        }
        time++;
      },
      items() {
        const items = [];
        for (const [k, sym] of cells) {
          const c = k.split(',').map(Number);
          items.push({ id: k, owner: owner.get(k), at: c, kind: 'cell', color: PALETTE[sym] ?? '#888' });
        }
        for (const comp of comps) items.push({ id: 'c:' + comp.name, owner: comp.name, at: comp.at, kind: 'c', color: comp.def.color, label: comp.name });
        return items;
      },
      processes() {
        return comps.map((c) => ({ id: c.name, label: c.name, status: c.idle > 3 ? 'done' : 'run', note: `${c.applied} rewrites` }));
      },
      click(c) {
        if (get(c) === 'B') {
          const target = comps.find((x) => x.def.clickSeed !== false && x.name !== 'maze') ?? comps[0];
          cells.set(key(c), target.def.seedSymbol ?? 'R');
          owner.set(key(c), target.name);
          target.idle = 0;
        }
      },
      status() {
        return `${cells.size} cells written · rules are data, so purity needs no check`;
      },
    };
  },
};
