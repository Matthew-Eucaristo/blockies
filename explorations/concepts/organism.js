// Concept 1: Organism. Every computer is a pure function with a body.
// State flows down as input (parent → child) and up as output (child →
// parent), like props and return values. The body is a set of [d] cells
// relative to the computer, computed from its state: the shape IS the state.

import { mix, add, key } from '../lib/lattice.js';

const helpers = {
  // A line of n cells from the computer along a direction.
  bar(n, dir) {
    const out = [];
    for (let i = 1; i <= Math.max(0, Math.floor(n)); i++) out.push(dir.map((d) => d * i));
    return out;
  },
  // n cells grown around the origin in a deterministic order (a blob).
  blob(n, seed = 1, rank = 2) {
    const seen = new Set([key(new Array(rank).fill(0))]);
    const out = [];
    let front = [new Array(rank).fill(0)];
    while (out.length < n && front.length) {
      const cand = [];
      for (const c of front)
        for (let a = 0; a < rank; a++)
          for (const s of [1, -1]) {
            const n2 = c.slice();
            n2[a] += s;
            if (!seen.has(key(n2))) {
              seen.add(key(n2));
              cand.push(n2);
            }
          }
      cand.sort((p, q) => mix(seed, ...p) - mix(seed, ...q));
      for (const c of cand) if (out.length < n) out.push(c);
      front = cand;
    }
    return out;
  },
};

export default {
  title: 'Organism',
  tagline: 'every computer is a pure function with a body; the shape is the state',
  rate: 4,
  helpers,
  idea: `
<h3>The idea</h3>
<p>A computer <code>[c]</code> is one block running a pure function. Each tick it gets an <b>input</b> from its parent and returns a new state. From that state it computes two things:</p>
<ul>
<li><b>output</b>, which its parent reads (like a return value);</li>
<li><b>body</b>, a set of <code>[d]</code> cells relative to itself. The body is the visualisation: a queue grows a tail, a cache fills a box.</li>
</ul>
<p>Children extend a computer: the parent decides each child's input (<code>feed</code>) and reads each child's output. Siblings never talk directly; they talk through their parent.</p>
<h3>Try</h3>
<ul>
<li>Click the <b>traffic</b> block to cause a spike, and watch the API's queue tail grow and turn red.</li>
<li>Edit <code>capacity</code> in the Spec tab and press Apply.</li>
<li>Switch to <b>Space-time</b> to see the queue's history as a sculpture.</li>
</ul>
<h3>One tick</h3>
<ol>
<li>Every computer computes its <code>output(state)</code> (bottom-up).</li>
<li>Every parent computes <code>feed(state, outputs)</code>, the input for each child (top-down).</li>
<li>Every computer runs <code>step(state, input, outputs)</code>. Everything is pure, so the tick is deterministic.</li>
</ol>`,
  notes: `
<h3>What worked</h3>
<ul>
<li><b>"The shape is the state" reads at a glance.</b> The queue tail grows and turns red during a spike, so you see the backlog without reading a number. This is the strongest idea here.</li>
<li><b>Space-time view:</b> the traffic spike shows up as a wall and the database as a tapering tower. History becomes an object you can look at.</li>
<li>Purity is easy to check: run each function twice and compare.</li>
</ul>
<h3>What broke</h3>
<ul>
<li><b>Bodies collide.</b> The API's tail grew through the database. In a world where blocks occupy cells, someone has to own a cell. Should bodies push, clip, route around, or fail?</li>
<li><b>Wiring lives in the parent.</b> <code>main.feed</code> routes traffic → api → db by hand. That is fine for 3 nodes but becomes a switchboard for 30.</li>
<li><b>All-orange bodies</b> (as in the sketch) hid ownership. Tinting the body by its computer's colour fixed it. Question for Edward: is orange the <i>kind</i> or the <i>owner</i>?</li>
</ul>
<h3>Agent cost</h3>
<p>About 1.8k chars for 5 computers. Readable, but the agent must write JS functions.</p>`,
  spec: `
{
  world: [26, 16],
  name: 'main', at: [0, 0], color: '#8a90a0',
  state: { spike: 0 },
  // The user's click on 'traffic' arrives here as an event.
  step: (s, input) => ({ spike: input.click === 'traffic' ? 12 : Math.max(0, s.spike - 1) }),
  feed: (s, out) => ({
    traffic: { spike: s.spike > 0 },
    api: { requests: out.traffic.requests },
    db: { writes: out.api.served },
  }),
  children: {
    traffic: {
      at: [2, 3], color: '#3b6ff5',
      state: { t: 0 },
      step: (s, input) => ({ t: s.t + 1, spike: input.spike }),
      output: (s) => ({ requests: s.spike ? 9 : 2 + (s.t % 8 < 4 ? 1 : 0) }),
      body: (s, out) => bar(out.requests, [1, 0]),
    },
    api: {
      at: [6, 8], color: '#e0524f', capacity: 4,
      state: { queue: 0, served: 0 },
      step: (s, input, out, self) => {
        const cap = self.capacity + out.cache.hits;
        const total = s.queue + input.requests;
        const served = Math.min(cap, total);
        return { queue: total - served, served };
      },
      output: (s) => ({ served: s.served, queue: s.queue }),
      feed: (s) => ({ cache: { load: s.served } }),
      body: (s) => bar(Math.min(s.queue, 18), [1, 0])
        .map((c, i) => ({ at: c, color: i >= 6 ? '#e0524f' : '#f2a33a' })), // the tail turns red past 6
      children: {
        cache: {
          at: [0, 3], color: '#7a5af5',
          state: { warm: 0 },
          step: (s, input) => ({ warm: Math.min(6, s.warm + (input.load > 0 ? 1 : 0)) }),
          output: (s) => ({ hits: Math.floor(s.warm / 3) }),
          body: (s) => blob(s.warm, 3),
        },
      },
    },
    db: {
      at: [20, 4], color: '#2e9e6e',
      state: { rows: 0 },
      step: (s, input) => ({ rows: s.rows + input.writes }),
      body: (s) => blob(Math.min(40, Math.floor(s.rows / 6)), 7),
    },
  },
}`,

  create(spec) {
    const impure = new Set();
    const shape = spec.world;
    const rank = shape.length;
    // Flatten the tree into nodes with absolute positions.
    const nodes = [];
    const walk = (def, name, parent, base) => {
      const at = add(base, def.at ?? new Array(rank).fill(0));
      const node = { def, name, path: parent ? parent.path + '/' + name : name, parent, at, state: structuredClone(def.state ?? {}), kids: [] };
      nodes.push(node);
      if (parent) parent.kids.push(node);
      for (const [k, d] of Object.entries(def.children ?? {})) walk(d, k, node, at);
      return node;
    };
    const root = walk(spec, spec.name ?? 'main', null, new Array(rank).fill(0));
    let time = 0;
    let event = {};
    const outputs = new Map();
    const P = (fn, args, who) => pure(fn, args, () => impure.add(who));

    function computeOutputs() {
      for (const n of [...nodes].reverse()) {
        outputs.set(n, n.def.output ? P(n.def.output, [n.state], n.name) : {});
      }
    }
    computeOutputs();

    return {
      shape,
      impure,
      get time() {
        return time;
      },
      tick() {
        computeOutputs();
        const inputs = new Map([[root, event]]);
        for (const n of nodes) {
          const kidsOut = Object.fromEntries(n.kids.map((k) => [k.name, outputs.get(k)]));
          if (n.def.feed) {
            const fed = P(n.def.feed, [n.state, kidsOut], n.name) ?? {};
            for (const k of n.kids) inputs.set(k, fed[k.name] ?? {});
          }
        }
        for (const n of nodes) {
          const kidsOut = Object.fromEntries(n.kids.map((k) => [k.name, outputs.get(k)]));
          if (n.def.step) n.state = P(n.def.step, [n.state, inputs.get(n) ?? {}, kidsOut, selfOf(n.def)], n.name);
        }
        event = {};
        time++;
        computeOutputs();
      },
      items() {
        const items = [];
        for (const n of nodes) {
          if (n === root && !spec.showRoot) continue;
          items.push({ id: n.path, owner: n.path, at: n.at, kind: 'c', color: n.def.color ?? '#e0524f', label: n.name });
          if (!n.def.body) continue;
          const body = P(n.def.body, [n.state, outputs.get(n)], n.name) ?? [];
          body.forEach((b, i) => {
            const off = Array.isArray(b) ? b : b.at;
            items.push({ id: n.path + '#' + i, owner: n.path, at: add(n.at, off), kind: 'd', color: (b && b.color) || n.def.bodyColor || n.def.color || '#f2a33a' });
          });
        }
        return items;
      },
      processes() {
        return nodes
          .filter((n) => n !== root || spec.showRoot)
          .map((n) => ({ id: n.path, label: n.path.replace(/^main\//, ''), status: 'run', note: brief(outputs.get(n)) }));
      },
      click(c) {
        for (const n of nodes) if (n.at.every((v, i) => v === c[i])) event = { click: n.name };
      },
      status() {
        return `${nodes.length} computers · state flows down by feed(), up by output()`;
      },
    };
  },
};

function selfOf(def) {
  const out = {};
  for (const [k, v] of Object.entries(def)) if (typeof v !== 'function' && k !== 'children' && k !== 'state') out[k] = v;
  return out;
}

function brief(o) {
  if (!o) return '';
  const s = Object.entries(o)
    .map(([k, v]) => `${k} ${typeof v === 'number' ? v : v ? '✓' : '·'}`)
    .join(' ');
  return s.length > 28 ? s.slice(0, 27) + '…' : s;
}

function pure(fn, args, onImpure) {
  const a = fn(...structuredClone(args));
  const b = fn(...structuredClone(args));
  if (JSON.stringify(a) !== JSON.stringify(b)) onImpure();
  return a;
}
