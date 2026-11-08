// Concept 6: Sheet. Every computer is a formula over other computers, like a
// spreadsheet cell placed in space. Dependencies are tracked automatically
// (whatever the formula reads). Values map to declared, finite states, and
// each state has a look. A change spreads one hop per tick as a visible
// pulse, so you can watch cause and effect.

import { add } from '../lib/lattice.js';

export default {
  title: 'Sheet',
  tagline: 'a spreadsheet in space: formulas, declared states, and changes that travel as visible pulses',
  rate: 4,
  idea: `
<h3>The idea</h3>
<p>The most successful programming model in history is the spreadsheet: cells, pure formulas, automatic recomputation. Here every computer is such a cell, placed in the world.</p>
<ul>
<li><b>Inputs</b> hold a number that people (or agents) change. Click one to raise it, Shift-click to lower it.</li>
<li><b>Formulas</b> are pure functions of other computers, for example <code>f: (v) =&gt; v.traffic / (v.servers * 20)</code>. Whatever a formula reads becomes a dependency; nothing is wired by hand.</li>
<li><b>States</b> are declared and finite (<code>ok | busy | overloaded</code>), chosen by threshold. A state picks the colour, and an optional <b>bar</b> of <code>[d]</code> blocks shows the size of the value.</li>
<li>A change does not apply everywhere at once: it travels <b>one hop per tick as a pulse</b>, so you can follow cause and effect.</li>
</ul>
<h3>Try</h3>
<ul>
<li>Click <b>traffic</b> a few times and follow the pulses into load, then latency, then happy.</li>
<li>Click <b>servers</b> to add capacity and watch cost rise while latency falls.</li>
</ul>`,
  notes: `
<h3>What worked</h3>
<ul>
<li><b>Cause and effect are visible.</b> Mid-wave you can catch load already red while latency is still green, and a pulse is on its way. "Why is happy red?" can be answered by following the pulses backwards.</li>
<li><b>The lowest authoring cost per idea.</b> An agent writes formulas, which LLMs do well. Dependencies are discovered from what a formula reads, so nothing is wired by hand.</li>
<li>Declared finite states (<code>ok | busy | overloaded</code>) fit here naturally, which keeps the original Blockies principle alive.</li>
</ul>
<h3>What broke</h3>
<ul>
<li><b>Nothing is really a process.</b> Formulas are timeless functions, so "computer = running process" does not hold. The process bar shows them mostly idle. It is a great <i>derived view</i>, not a world.</li>
<li><b>Global reads break locality.</b> Any cell can read any other by name. That is easy in one browser, but costly when worlds are distributed or huge.</li>
<li><b>A one-cell block cannot carry meaning.</b> The value label is barely readable and there is no room for the name. Semantic boards need bigger blocks (a block spanning several cells) or labels drawn outside.</li>
</ul>`,
  spec: `
{
  world: [19, 9],
  cells: {
    traffic: { at: [0, 1], input: 40, step: 20, min: 0, max: 200, bar: [1, 0], per: 20 },
    servers: { at: [0, 7], input: 2, step: 1, min: 1, max: 8, bar: [1, 0], per: 1 },
    load: {
      at: [7, 4], f: (v) => v.traffic / (v.servers * 20),
      states: { ok: 0, busy: 0.7, overloaded: 1 }, show: (x) => Math.round(x * 100) + '%',
    },
    latency: {
      at: [11, 1], f: (v) => Math.round(20 + 300 * Math.max(0, v.load - 0.6) ** 2),
      states: { fast: 0, slow: 80, timeout: 250 }, bar: [1, 0], per: 40, show: (x) => x + 'ms',
    },
    cost: {
      at: [11, 7], f: (v) => v.servers * 40,
      states: { cheap: 0, pricey: 200 }, bar: [1, 0], per: 40, show: (x) => '$' + x,
    },
    happy: {
      at: [18, 4], f: (v) => v.latency < 80 && v.cost < 240,
      states: { no: false, yes: true },
    },
  },
  colors: { ok: '#2e9e6e', fast: '#2e9e6e', cheap: '#2e9e6e', yes: '#2e9e6e',
            busy: '#f2a33a', slow: '#f2a33a', pricey: '#f2a33a',
            overloaded: '#e0524f', timeout: '#e0524f', no: '#e0524f' },
}`,

  create(spec) {
    const impure = new Set();
    const names = Object.keys(spec.cells);
    const cells = new Map(
      names.map((n) => {
        const d = spec.cells[n];
        return [n, { name: n, def: d, value: d.input, deps: new Set(), shown: d.input }];
      }),
    );
    let time = 0;
    let pulses = []; // { id, from, to, age }
    let seq = 0;
    let pending = new Set(); // cells to recompute this tick

    function evaluate(c) {
      const deps = new Set();
      const proxy = new Proxy(
        {},
        {
          get(_, k) {
            if (typeof k !== 'string') return undefined;
            deps.add(k);
            return cells.get(k)?.value;
          },
        },
      );
      const a = c.def.f(proxy);
      const b = c.def.f(proxy);
      if (JSON.stringify(a) !== JSON.stringify(b)) impure.add(c.name);
      c.deps = deps;
      return a;
    }

    // Settle everything once at the start (no pulses).
    for (let pass = 0; pass < names.length; pass++) for (const c of cells.values()) if (c.def.f) c.value = evaluate(c);

    function stateOf(c) {
      const st = c.def.states;
      if (!st) return null;
      let best = null;
      for (const [name, th] of Object.entries(st)) {
        if (typeof th === 'boolean') {
          if (c.value === th) best = name;
        } else if (c.value >= th) best = name;
      }
      return best;
    }

    function changed(name) {
      for (const c of cells.values()) {
        if (c.deps.has(name)) {
          pulses.push({ id: 'p' + seq++, from: cells.get(name).def.at, to: c.def.at, target: c.name, age: 0 });
        }
      }
    }

    return {
      shape: spec.world,
      impure,
      get time() {
        return time;
      },
      tick() {
        // Pulses that were launched last tick arrive now and trigger recomputation.
        const arrive = pulses.filter((p) => p.age >= 1);
        pulses = pulses.filter((p) => p.age < 1);
        for (const p of pulses) p.age++;
        for (const p of arrive) pending.add(p.target);
        const now = [...pending];
        pending = new Set();
        for (const n of now) {
          const c = cells.get(n);
          const v = evaluate(c);
          if (JSON.stringify(v) !== JSON.stringify(c.value)) {
            c.value = v;
            changed(n);
          }
        }
        time++;
      },
      items() {
        const items = [];
        for (const c of cells.values()) {
          const st = stateOf(c);
          const color = c.def.input !== undefined ? '#3b6ff5' : spec.colors?.[st] ?? '#8a90a0';
          const shown = c.def.show ? c.def.show(c.value) : typeof c.value === 'number' ? String(Math.round(c.value * 10) / 10) : String(c.value);
          items.push({ id: c.name, owner: c.name, at: c.def.at, kind: 'c', color, label: shown });
          if (c.def.bar && typeof c.value === 'number') {
            const n = Math.min(6, Math.round(c.value / (c.def.per ?? 1)));
            for (let i = 1; i <= n; i++) items.push({ id: c.name + '#' + i, owner: c.name, at: add(c.def.at, c.def.bar.map((d) => d * i)), kind: 'd', color });
          }
        }
        for (const p of pulses) items.push({ id: p.id, at: p.age === 0 ? p.from : p.to, kind: 'token', color: '#ffcc33' });
        return items;
      },
      processes() {
        return [...cells.values()].map((c) => ({ id: c.name, label: c.name, status: pending.has(c.name) || pulses.some((p) => p.target === c.name) ? 'run' : 'wait', note: stateOf(c) ?? (c.def.input !== undefined ? 'input' : '') }));
      },
      click(at, mods) {
        for (const c of cells.values()) {
          if (c.def.input === undefined || !c.def.at.every((v, i) => v === at[i])) continue;
          const step = (c.def.step ?? 1) * (mods.shift || mods.alt ? -1 : 1);
          c.value = Math.max(c.def.min ?? -Infinity, Math.min(c.def.max ?? Infinity, c.value + step));
          changed(c.name);
        }
      },
      status() {
        return `${pulses.length} pulses travelling · dependencies are discovered by what each formula reads`;
      },
    };
  },
};
