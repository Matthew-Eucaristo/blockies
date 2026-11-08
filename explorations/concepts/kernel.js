// Concept 7: Kernel (synthesis). What survived the first six experiments,
// in one small model:
//
// - Computer = process = one block (Mind). Kinds are types, spawned many times.
// - Pure step(state, io) → { state, send, spawn, wait, done, fail }.
// - Sharing is only by messages (Messages). People talk to computers the same
//   way: a click is a message. Delivery is next tick; distance is not latency.
// - The shape is the state (Organism), but the agent only says HOW MUCH body
//   (a count), never WHERE. The lattice grows the body through free cells
//   (Rules: first come, first served), so bodies flow around each other
//   and never overlap. Agents say what; the world decides where.
// - Children are placed in the nearest free cell around their parent.

import { Lattice, key } from '../lib/lattice.js';

export default {
  title: 'Kernel',
  tagline: 'the synthesis: processes with bodies, talking by messages; agents say what, the lattice decides where',
  rate: 5,
  idea: `
<h3>What this keeps from each experiment</h3>
<ul>
<li><b>Mind:</b> a computer is a process and always one block. Kinds are types; <code>spawn</code> makes children; waiting is hatched.</li>
<li><b>Messages:</b> computers share nothing but messages: <code>send: [[to, payload]]</code>. A click from a person is just another message, so people and agents use the same door.</li>
<li><b>Organism:</b> the shape is the state. <code>body(state)</code> returns <i>how many</i> <code>[d]</code> blocks to show.</li>
<li><b>Rules:</b> space is first come, first served. The engine grows each body through free cells, so bodies never overlap; they flow around each other.</li>
<li><b>Signals:</b> agents are bad at coordinates. So the agent never places a <code>[d]</code> block, and children are placed automatically too.</li>
</ul>
<h3>The demo: autoscaling</h3>
<p>The API keeps a queue (its body). When the queue is long it <b>spawns workers</b>: children that extend what it can do. Each worker asks the database, then answers the client. Idle workers exit, so the system grows and shrinks as a shape.</p>
<h3>Try</h3>
<ul>
<li>Click <b>client</b> to toggle a burst. Watch the queue grow, workers appear, the database queue grow, then everything shrink back.</li>
<li>Shift-click any process to pause it.</li>
<li>Try Space-time during a burst.</li>
</ul>`,
  notes: `
<h3>What worked</h3>
<ul>
<li><b>Autoscaling became a shape.</b> During a burst the API's queue swells and workers appear around it as children ("children extend functionality", as Edward put it). After the burst, idle workers exit one by one. In Space-time the whole episode is one sculpture: a bulge that grows and shrinks.</li>
<li><b>The agent never wrote a coordinate</b> for a body cell or a child. Only the three starting computers have positions. This removes the class of mistakes seen in Signals.</li>
<li><b>A click is a message.</b> People and agents use the same door, and the step function cannot tell them apart. That is a good property for "agent-driven, with people steering by voice".</li>
</ul>
<h3>What broke, and the rule it produced</h3>
<ul>
<li><b>The first version buried its own workers:</b> the queue body flowed around them until they disappeared inside it. Fixed with a <b>moat rule</b>: a body may never touch another computer's core. Each computer stays visible, and growth routes around others. This is a candidate core rule for "how blockies interact visually".</li>
<li>Addressing by kind name (<code>'db'</code>) is ambiguous when there are many of a kind; ids work but are not readable. Names, ids and roles need one clear story.</li>
<li><b>Highest agent cost</b> (about 2.9k chars of JS). A real agent-facing vocabulary must be much smaller (see the recommendations on the index page).</li>
<li>Still no nesting: a computer cannot yet <i>contain</i> a world.</li>
</ul>`,
  variants: {
    autoscaling: `
{
  world: [21, 13],
  start: [
    { kind: 'client', at: [2, 6] },
    { kind: 'api', at: [9, 6] },
    { kind: 'db', at: [17, 6] },
  ],
  kinds: {
    client: {
      color: '#3b6ff5', state: { t: 0, burst: false, ok: 0 },
      step: (s, io) => {
        const burst = io.inbox.some((m) => m.type === 'click') ? !s.burst : s.burst;
        const ok = s.ok + io.inbox.filter((m) => m.type === 'page').length;
        const n = burst ? (s.t % 2 === 0 ? 2 : 1) : s.t % 3 === 0 ? 1 : 0;
        return { state: { t: s.t + 1, burst, ok }, send: Array.from({ length: n }, () => ['api', { type: 'GET' }]) };
      },
      body: (s) => (s.burst ? 4 : 1),
      note: (s) => 'ok ' + s.ok,
    },
    api: {
      color: '#e0524f', state: { queue: [], busy: [] },
      step: (s, io) => {
        let queue = [...s.queue, ...io.inbox.filter((m) => m.type === 'GET').map((m) => m.from)];
        let busy = s.busy.filter((id) => !io.inbox.some((m) => m.type === 'free' && m.from === id));
        busy = busy.filter((id) => io.children.some((c) => c.id === id));
        const send = [];
        for (const w of io.children) {
          if (w.kind !== 'worker' || busy.includes(w.id) || !queue.length) continue;
          send.push([w.id, { type: 'job', client: queue[0] }]);
          busy = [...busy, w.id];
          queue = queue.slice(1);
        }
        const spawn = queue.length > 3 && io.children.length < 6 ? [{ kind: 'worker' }] : [];
        return { state: { queue, busy }, send, spawn, wait: !queue.length && !busy.length };
      },
      body: (s) => ({ n: s.queue.length, color: s.queue.length > 8 ? '#e0524f' : '#f2a33a' }),
      note: (s) => 'queue ' + s.queue.length,
    },
    worker: {
      color: '#f2a33a', state: { job: null, phase: 'idle', t: 0, idle: 0 },
      step: (s, io) => {
        const job = io.inbox.find((m) => m.type === 'job');
        if (job) return { state: { ...s, job: job.client, phase: 'db', idle: 0 }, send: [['db', { type: 'query' }]] };
        if (s.phase === 'db') return io.inbox.some((m) => m.type === 'rows') ? { state: { ...s, phase: 'work', t: 0 } } : { state: s, wait: true };
        if (s.phase === 'work') return s.t < 2 ? { state: { ...s, t: s.t + 1 } }
          : { state: { ...s, phase: 'idle', job: null }, send: [[s.job, { type: 'page' }], ['parent', { type: 'free' }]] };
        return s.idle > 12 ? { state: s, done: 'scaled down' } : { state: { ...s, idle: s.idle + 1 }, wait: true };
      },
      body: (s) => (s.phase === 'work' ? 2 : s.phase === 'db' ? 1 : 0),
    },
    db: {
      color: '#2e9e6e', state: { q: [] },
      step: (s, io) => {
        const q = [...s.q, ...io.inbox.filter((m) => m.type === 'query').map((m) => m.from)];
        return { state: { q: q.slice(2) }, send: q.slice(0, 2).map((id) => [id, { type: 'rows' }]), wait: !q.length };
      },
      body: (s) => s.q.length,
      note: (s) => 'queue ' + s.q.length,
    },
  },
}`,
  },

  create(spec) {
    const lat = new Lattice(spec.world);
    const impure = new Set();
    const procs = [];
    const byId = new Map();
    const occupied = new Map(); // cell -> { p, body: bool }
    let seq = 0;
    let time = 0;
    let flying = [];
    let fseq = 0;
    const LINGER = spec.linger ?? 6;
    const P = (fn, args, who) => {
      const a = fn(...structuredClone(args));
      const b = fn(...structuredClone(args));
      if (JSON.stringify(a) !== JSON.stringify(b)) impure.add(who);
      return a;
    };

    // A body may not touch another process's core: every computer keeps a
    // moat, so a growing body can never bury it (first version did exactly that).
    const moatOk = (c, owner) => lat.neighbors(c).every((n) => {
      const o = occupied.get(key(n));
      return !o || o.body || o.p === owner;
    });

    function nearestFree(froms, ok = () => true) {
      const seen = new Set(froms.map(key));
      let front = froms;
      while (front.length) {
        const next = [];
        for (const c of front)
          for (const n of lat.neighbors(c)) {
            const k = key(n);
            if (seen.has(k)) continue;
            seen.add(k);
            if (!occupied.has(k) && ok(n)) return n;
            next.push(n);
          }
        front = next;
      }
      return null;
    }

    function spawn(kind, label, parent, at, args) {
      const def = spec.kinds[kind];
      if (!def) throw new Error(`unknown kind '${kind}'`);
      const cell = at ?? nearestFree([parent.at], (n) => moatOk(n, null));
      if (!cell || occupied.has(key(cell))) return null;
      const p = { id: seq++, kind, def, label: label ?? kind, parent, at: cell, args: args ?? {}, state: structuredClone(def.state ?? {}), status: 'run', inbox: [], next: [], body: [], kids: [], ended: null, result: undefined, paused: false };
      occupied.set(key(cell), { p, body: false });
      procs.push(p);
      byId.set(p.id, p);
      if (parent) parent.kids.push(p);
      return p;
    }

    for (const s of spec.start) spawn(s.kind, s.label, null, s.at, s.args);

    function resolve(to, from) {
      if (to === 'parent') return from.parent;
      if (typeof to === 'number') return byId.get(to);
      const live = (p) => p.ended === null;
      return from.kids.find((k) => live(k) && (k.label === to || k.kind === to)) ?? procs.find((p) => live(p) && (p.label === to || p.kind === to));
    }

    function grow(p, want) {
      want = Math.max(0, Math.min(24, want | 0));
      while (p.body.length > want) occupied.delete(key(p.body.pop()));
      while (p.body.length < want) {
        const c = nearestFree([p.at, ...p.body], (n) => moatOk(n, p));
        if (!c) break;
        occupied.set(key(c), { p, body: true });
        p.body.push(c);
      }
    }

    return {
      shape: spec.world,
      impure,
      get time() {
        return time;
      },
      tick() {
        flying = flying.filter((f) => f.age < 1);
        for (const f of flying) f.age++;
        for (const p of [...procs]) {
          if (p.ended !== null || p.paused) continue;
          const results = {};
          let running = 0;
          for (const k of p.kids) k.ended === null ? running++ : (results[k.label] = k.result);
          const children = p.kids.filter((k) => k.ended === null).map((k) => ({ id: k.id, kind: k.kind, label: k.label, status: k.status }));
          const io = { time, id: p.id, args: p.args, inbox: p.inbox, results, running, children };
          p.inbox = [];
          const r = P(p.def.step, [p.state, io], p.kind) ?? {};
          if (r.state !== undefined) p.state = r.state;
          for (const [to, payload] of r.send ?? []) {
            const q = resolve(to, p);
            if (!q || q.ended !== null) continue;
            q.next.push({ ...payload, from: p.id });
            flying.push({ id: 'f' + fseq++, from: p.at, to: q.at, age: 0, color: p.def.color });
          }
          for (const s of r.spawn ?? []) spawn(s.kind, s.label, p, null, s.args);
          if (r.done !== undefined || r.fail !== undefined) {
            p.status = r.fail !== undefined ? 'fail' : 'done';
            p.result = r.fail ?? r.done;
            p.ended = time;
          } else p.status = r.wait ? 'wait' : 'run';
        }
        for (const p of procs) {
          p.inbox.push(...p.next);
          p.next = [];
        }
        for (const p of procs) {
          let want = 0;
          if (p.ended === null && p.def.body) {
            const b = P(p.def.body, [p.state], p.kind);
            want = typeof b === 'number' ? b : b?.n ?? 0;
            p.bodyColor = typeof b === 'object' && b ? b.color : null;
          }
          grow(p, want);
        }
        for (let i = procs.length - 1; i >= 0; i--) {
          const p = procs[i];
          if (p.ended !== null && time - p.ended > LINGER) {
            grow(p, 0);
            occupied.delete(key(p.at));
            procs.splice(i, 1);
            byId.delete(p.id);
            if (p.parent) p.parent.kids = p.parent.kids.filter((k) => k !== p);
          }
        }
        time++;
      },
      items() {
        const items = [];
        for (const p of procs) {
          const color = p.status === 'fail' ? '#e0524f' : p.paused ? '#8a90a0' : p.def.color;
          items.push({ id: 'p' + p.id, owner: 'p' + p.id, at: p.at, kind: 'c', color, label: p.label, hatch: p.status === 'wait' || p.paused, fade: p.status === 'done' ? 0.35 : 1 });
          p.body.forEach((c, i) => items.push({ id: 'p' + p.id + '#' + i, owner: 'p' + p.id, at: c, kind: 'd', color: p.bodyColor ?? p.def.bodyColor ?? p.def.color }));
        }
        for (const f of flying) items.push({ id: f.id, at: f.age === 0 ? f.from : f.to, kind: 'token', color: f.color });
        return items;
      },
      processes() {
        return procs.map((p) => ({ id: 'p' + p.id, label: p.label, status: p.paused ? 'wait' : p.status, note: p.paused ? 'paused' : p.def.note ? p.def.note(p.state) : p.status }));
      },
      click(c, mods) {
        const o = occupied.get(key(c));
        if (!o) return;
        if (mods.shift || mods.alt) o.p.paused = !o.p.paused;
        else o.p.inbox.push({ type: 'click', from: 'person' });
      },
      status() {
        const live = procs.filter((p) => p.ended === null).length;
        const cells = [...occupied.values()].filter((o) => o.body).length;
        return `${live} processes · ${cells} body cells · bodies never overlap · a click is a message`;
      },
    };
  },
};
