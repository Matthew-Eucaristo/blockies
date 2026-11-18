// Concept 5: Mind. Processes are born, wait, finish and fail, and every
// live process is exactly one block (Edward's rule). A process is a pure step
// function that can spawn children and read their results. Children are
// placed in the nearest free cell around their parent, so a process tree
// grows into a shape. Kinds are defined once ("types of blockies") and
// instantiated many times.

import { Lattice, key } from '../lib/lattice.js';

export default {
  title: 'Mind',
  tagline: 'an agent’s work as a living process tree: one block per process, children grow around their parent',
  rate: 5,
  idea: `
<h3>The idea</h3>
<p>This one targets the product directly: an agent (for example Claude Code) shows its work as a world of computers.</p>
<ul>
<li>A <b>kind</b> is a type of blockie: a colour and a pure <code>step(state, io) → { state, spawn?, wait?, done?, fail? }</code>.</li>
<li>A <b>process</b> is one running instance of a kind, and it is always exactly one block. Running blocks are solid, <b>waiting blocks are hatched</b> (like block 4 in the sketch), finished ones fade and then leave, failed ones turn red.</li>
<li><code>spawn</code> starts children. A child is placed in the nearest free cell around its parent, so the tree of work grows into a visible shape. A parent reads finished children through <code>io.results</code>.</li>
</ul>
<h3>Try</h3>
<ul>
<li>Watch the task search, read files, edit, fail a test, edit again and pass.</li>
<li>Click a process to <b>pause</b> it (human in the loop). Its parent keeps waiting.</li>
<li>Open the 1D variant: Space-time turns the same run into a <b>Gantt chart</b>.</li>
</ul>`,
  notes: `
<h3>What worked</h3>
<ul>
<li><b>One process = one block made the agent legible.</b> You see the task wait (hatched) while three reads run in parallel, a test turn red, a second edit start and the task finish. A text log of the same run would be 20 lines you skim.</li>
<li><b>Third free diagram: a Gantt chart.</b> The 1D variant in Space-time shows the root's long waiting stretches (light), the parallel reads and the two test runs. The critical path is visible without anyone drawing it.</li>
<li><b>Kinds are "types of blockies"</b>, defined once and spawned many times. This answers one of Edward's open questions with something concrete.</li>
<li><b>Pause is human-in-the-loop for free:</b> a paused block simply stops stepping, and its parent keeps waiting.</li>
</ul>
<h3>What broke</h3>
<ul>
<li>"Nearest free cell" placement scatters siblings once the area fills up. Layout needs a policy (rings, rows, or keeping children inside the parent's area), which brings us back to "who owns space".</li>
<li>Results keyed by name are awkward when the same kind is spawned twice (edit, then edit again).</li>
<li>Nesting is still flat: zooming into a process to see <i>its</i> world (a computer containing a lattice) is the obvious next step for "any complexity".</li>
</ul>
<h3>Why this matters for the product</h3>
<p>A real agent emits these events already: tool calls, subagents, waits and failures. Mapping them 1:1 onto processes gives a live view of an agent, and the step functions above are only a simulator of one.</p>`,
  variants: {
    'agent fixes a bug': `
{
  world: [11, 7],
  root: { kind: 'task', label: 'fix login bug', at: [5, 3] },
  kinds: {
    task: {
      color: '#3b6ff5', state: { phase: 'search', tries: 0 },
      step: (s, io) => {
        switch (s.phase) {
          case 'search': return { state: { ...s, phase: 'searching' }, spawn: [{ kind: 'search', label: 'grep login' }] };
          case 'searching': return io.results.search ? { state: { ...s, phase: 'edit' } } : { state: s, wait: true };
          case 'edit': return { state: { ...s, phase: 'editing', tries: s.tries + 1 }, spawn: [{ kind: 'edit', label: 'auth.ts', args: { try: s.tries + 1 } }] };
          case 'editing': return io.results.edit ? { state: { ...s, phase: 'test' } } : { state: s, wait: true };
          case 'test': return { state: { ...s, phase: 'testing' }, spawn: [{ kind: 'test', label: 'npm test', args: { try: s.tries } }] };
          case 'testing':
            if (!io.results.test) return { state: s, wait: true };
            return io.results.test.pass ? { state: s, done: 'fixed' } : { state: { ...s, phase: 'edit' } };
        }
      },
    },
    search: {
      color: '#7a5af5', state: { started: false },
      step: (s, io) => !s.started
        ? { state: { started: true }, spawn: ['auth.ts', 'session.ts', 'login.tsx'].map((f) => ({ kind: 'read', label: f })) }
        : io.running > 0 ? { state: s, wait: true } : { state: s, done: '3 files' },
    },
    read: { color: '#2e9e6e', state: { t: 0 }, step: (s, io) => s.t >= 3 + (io.id % 4) ? { state: s, done: 'ok' } : { state: { t: s.t + 1 } } },
    edit: { color: '#f2a33a', state: { t: 0 }, step: (s) => s.t >= 4 ? { state: s, done: 'patched' } : { state: { t: s.t + 1 } } },
    test: {
      color: '#e0524f', state: { t: 0 },
      step: (s, io) => s.t < 5 ? { state: { t: s.t + 1 } }
        : io.args.try < 2 ? { state: s, fail: { pass: false } } : { state: s, done: { pass: true } },
    },
  },
}`,
    'same run, 1D (Gantt)': `
// The same kinds on a 1-dimensional world. Space-time draws the run as a
// Gantt chart: each column is a process slot, time goes down.
{
  world: [16],
  view: 'spacetime',
  root: { kind: 'task', label: 'fix login bug', at: [7] },
  kinds: {
    task: {
      color: '#3b6ff5', state: { phase: 'search', tries: 0 },
      step: (s, io) => {
        switch (s.phase) {
          case 'search': return { state: { ...s, phase: 'searching' }, spawn: [{ kind: 'search', label: 'grep login' }] };
          case 'searching': return io.results.search ? { state: { ...s, phase: 'edit' } } : { state: s, wait: true };
          case 'edit': return { state: { ...s, phase: 'editing', tries: s.tries + 1 }, spawn: [{ kind: 'edit', label: 'auth.ts', args: { try: s.tries + 1 } }] };
          case 'editing': return io.results.edit ? { state: { ...s, phase: 'test' } } : { state: s, wait: true };
          case 'test': return { state: { ...s, phase: 'testing' }, spawn: [{ kind: 'test', label: 'npm test', args: { try: s.tries } }] };
          case 'testing':
            if (!io.results.test) return { state: s, wait: true };
            return io.results.test.pass ? { state: s, done: 'fixed' } : { state: { ...s, phase: 'edit' } };
        }
      },
    },
    search: {
      color: '#7a5af5', state: { started: false },
      step: (s, io) => !s.started
        ? { state: { started: true }, spawn: ['auth.ts', 'session.ts', 'login.tsx'].map((f) => ({ kind: 'read', label: f })) }
        : io.running > 0 ? { state: s, wait: true } : { state: s, done: '3 files' },
    },
    read: { color: '#2e9e6e', state: { t: 0 }, step: (s, io) => s.t >= 3 + (io.id % 4) ? { state: s, done: 'ok' } : { state: { t: s.t + 1 } } },
    edit: { color: '#f2a33a', state: { t: 0 }, step: (s) => s.t >= 4 ? { state: s, done: 'patched' } : { state: { t: s.t + 1 } } },
    test: {
      color: '#e0524f', state: { t: 0 },
      step: (s, io) => s.t < 5 ? { state: { t: s.t + 1 } }
        : io.args.try < 2 ? { state: s, fail: { pass: false } } : { state: s, done: { pass: true } },
    },
  },
}`,
  },

  create(spec) {
    const lat = new Lattice(spec.world);
    const impure = new Set();
    const procs = [];
    const occupied = new Map(); // cell key -> process
    let seq = 0;
    let time = 0;
    const LINGER = spec.linger ?? 10;

    function place(near) {
      // Nearest free cell, breadth-first over lattice neighbours (rank-agnostic).
      const seen = new Set([key(near)]);
      let front = [near];
      while (front.length) {
        const next = [];
        for (const c of front) {
          for (const n of lat.neighbors(c)) {
            const k = key(n);
            if (seen.has(k)) continue;
            seen.add(k);
            if (!occupied.has(k)) return n;
            next.push(n);
          }
        }
        front = next;
      }
      return null;
    }

    function spawn(kind, label, parent, at, args, as) {
      const def = spec.kinds[kind];
      if (!def) throw new Error(`unknown kind '${kind}'`);
      const cell = at ?? place(parent.at);
      if (!cell) return null;
      const p = { id: seq++, kind, def, label: label ?? kind, as: as ?? kind, parent, args: args ?? {}, state: structuredClone(def.state ?? {}), status: 'run', at: cell, kids: [], born: time, ended: null, result: undefined, paused: false };
      occupied.set(key(cell), p);
      procs.push(p);
      if (parent) parent.kids.push(p);
      return p;
    }

    spawn(spec.root.kind, spec.root.label, null, spec.root.at);

    return {
      shape: spec.world,
      impure,
      get time() {
        return time;
      },
      tick() {
        for (const p of [...procs]) {
          if (p.ended !== null || p.paused) continue;
          const results = {};
          let running = 0;
          for (const k of p.kids) {
            if (k.ended === null) running++;
            else results[k.as] = k.result;
          }
          const io = { time, id: p.id, args: p.args, results, running };
          const r = pure(p.def.step, [p.state, io], () => impure.add(p.kind)) ?? {};
          if (r.state !== undefined) p.state = r.state;
          for (const s of r.spawn ?? []) {
            // A new child replaces the previous result under the same name.
            for (const k of p.kids) if (k.as === (s.as ?? s.kind)) k.as = null;
            spawn(s.kind, s.label, p, null, s.args, s.as);
          }
          if (r.done !== undefined || r.fail !== undefined) {
            p.status = r.fail !== undefined ? 'fail' : 'done';
            p.result = r.fail !== undefined ? r.fail : r.done;
            p.ended = time;
          } else p.status = r.wait ? 'wait' : 'run';
        }
        // Finished processes leave after a while, freeing their cell.
        for (let i = procs.length - 1; i >= 0; i--) {
          const p = procs[i];
          if (p.ended !== null && time - p.ended > LINGER && p.parent) {
            occupied.delete(key(p.at));
            procs.splice(i, 1);
          }
        }
        time++;
      },
      items() {
        return procs.map((p) => ({
          id: 'p' + p.id,
          owner: 'p' + p.id,
          at: p.at,
          kind: 'c',
          color: p.status === 'fail' ? '#e0524f' : p.paused ? '#8a90a0' : p.def.color,
          label: p.label,
          hatch: p.status === 'wait' || p.paused,
          fade: p.status === 'done' ? 0.35 : 1,
        }));
      },
      processes() {
        return procs.map((p) => ({ id: 'p' + p.id, label: p.label, status: p.paused ? 'wait' : p.status, note: p.paused ? 'paused' : p.status === 'run' ? p.kind : p.status }));
      },
      click(c) {
        const p = occupied.get(key(c));
        if (p && p.ended === null) p.paused = !p.paused;
      },
      status() {
        const live = procs.filter((p) => p.ended === null).length;
        const root = procs[0];
        return `${live} live processes · root: ${root.status}${root.result ? ' (' + JSON.stringify(root.result) + ')' : ''}`;
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
