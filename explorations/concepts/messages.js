// Concept 2: Messages. Computers are actors: a pure handler
// on(state, message) → { state, send }. Messages are real things in the
// world: tokens that travel cell by cell, so distance is latency. Each
// computer handles at most `rate` messages per tick; the rest wait in its
// mailbox, drawn as a tail. Overload is not scripted, it emerges.

import { Lattice, path } from '../lib/lattice.js';

export default {
  title: 'Messages',
  tagline: 'computers are actors; messages travel through space, so distance is latency and backlog is visible',
  rate: 5,
  helpers: {
    to: (target, payload) => [target, payload],
    reply: (m, payload) => [m.from, payload],
  },
  idea: `
<h3>The idea</h3>
<p>Every computer is an actor with one pure handler: <code>on(state, msg) → { state, send }</code>. It never touches another computer's state. The only way to share is to send a message.</p>
<ul>
<li>Messages are <b>tokens in the world</b>. They walk the lattice one cell per tick, so far-away computers are slower to reach. Layout becomes meaningful.</li>
<li>Each computer handles <code>rate</code> messages per tick. Extra messages wait in its <b>mailbox</b>, drawn as a tail of <code>[d]</code> blocks. Overload is never scripted; it appears when traffic exceeds capacity.</li>
<li>Every tick, each computer also gets one <code>{type:'tick'}</code> message, so it can act on its own (a clock).</li>
</ul>
<h3>Try</h3>
<ul>
<li>Click <b>browser</b> to toggle a traffic burst. The load balancer spreads the requests; watch both APIs' mailboxes grow.</li>
<li>Click <b>api2</b> to take it down (hatched). Its mail is dropped and the browser starts counting errors.</li>
<li>In Spec, move <code>db</code> far away and see the latency grow.</li>
</ul>`,
  notes: `
<h3>Discovery: diagrams are views of running worlds</h3>
<p>Put the same actors on a <b>1-dimensional</b> world (variant "sequence") and open Space-time. Each row is one tick, so you get a <b>sequence diagram that nobody drew</b>: lifelines, message arrows as diagonal dots, latency as slope. The engine did not change; only the world's rank and the renderer did.</p>
<p>This generalises. Many diagrams developers draw by hand are projections of <i>world × time</i>:</p>
<ul>
<li>sequence and Lamport diagrams (1D world + time);</li>
<li>Gantt and waterfall charts (processes on a line + time);</li>
<li>flame graphs (a call tree + time);</li>
<li>git graphs (branches + commits).</li>
</ul>
<p>So "the renderer decides" is bigger than 2D vs voxel: one running world can be seen as a board, a sculpture or a classic diagram.</p>
<h3>What worked</h3>
<ul>
<li><b>Overload emerges, it is not scripted.</b> The API handles one message per tick, so its mailbox tail grows and turns red. Taking api2 down drops mail and the browser counts errors. You can see <i>why</i> it is slow.</li>
<li>Shared routes become visible highways of tokens.</li>
<li>The model is honest: computers never touch each other's state, so it maps to real systems (Erlang, actors, microservices, agents and subagents).</li>
</ul>
<h3>What broke</h3>
<ul>
<li><b>Distance = latency makes layout semantic.</b> Moving a block changes behaviour. That is powerful (Dynamicland-like) but surprising when an agent places things carelessly. Maybe latency should be opt-in.</li>
<li>Message labels overlap into noise. Show them on hover or zoom.</li>
<li>The mailbox tail collided with routes, which is the same "who owns a cell" problem as Organism.</li>
</ul>`,
  variants: {
'web service': `
{
  world: [30, 15],
  computers: {
    browser: {
      at: [2, 7], color: '#3b6ff5',
      state: { t: 0, burst: false, ok: 0, errors: 0, waiting: 0 },
      on: (s, m) => {
        if (m.type === 'tick') {
          const n = s.burst ? 3 : (s.t % 3 === 0 ? 1 : 0);
          const timedOut = s.waiting > 25 ? 1 : 0;
          return {
            state: { ...s, t: s.t + 1, waiting: s.waiting + n - timedOut, errors: s.errors + timedOut },
            send: Array.from({ length: n }, (_, i) => to('lb', { type: 'GET', n: s.t * 4 + i })),
          };
        }
        if (m.type === 'click') return { state: { ...s, burst: !s.burst } };
        if (m.type === 'page') return { state: { ...s, ok: s.ok + 1, waiting: s.waiting - 1 } };
        return { state: s };
      },
      note: (s) => 'ok ' + s.ok + ' err ' + s.errors,
    },
    lb: {
      at: [9, 7], color: '#8a90a0', rate: 6,
      state: { i: 0 },
      on: (s, m) => m.type === 'GET'
        ? { state: { i: s.i + 1 }, send: [to(s.i % 2 ? 'api2' : 'api1', { ...m, client: m.from })] }
        : { state: s },
    },
    api1: {
      at: [15, 3], color: '#e0524f', rate: 1,
      state: { served: 0 },
      on: (s, m) => m.type === 'GET' ? { state: s, send: [to('db', { type: 'query', client: m.client })] }
        : m.type === 'rows' ? { state: { served: s.served + 1 }, send: [to(m.client, { type: 'page' })] }
        : { state: s },
    },
    api2: {
      at: [15, 11], color: '#e0524f', rate: 1,
      state: { served: 0 },
      on: (s, m) => m.type === 'GET' ? { state: s, send: [to('db', { type: 'query', client: m.client })] }
        : m.type === 'rows' ? { state: { served: s.served + 1 }, send: [to(m.client, { type: 'page' })] }
        : { state: s },
    },
    db: {
      at: [25, 7], color: '#2e9e6e', rate: 2,
      state: { queries: 0 },
      on: (s, m) => m.type === 'query'
        ? { state: { queries: s.queries + 1 }, send: [reply(m, { type: 'rows', client: m.client })] }
        : { state: s },
    },
  },
}`,
'sequence (1D world)': `
// The same model in a 1-dimensional world. Nothing in the engine changes.
// Space-time stacks the line of computers by tick: you get a sequence
// diagram that nobody drew.
{
  world: [25],
  view: 'spacetime',
  computers: {
    client: {
      at: [1], color: '#3b6ff5',
      state: { t: 0 },
      on: (s, m) => m.type === 'tick'
        ? { state: { t: s.t + 1 }, send: s.t % 14 === 0 ? [to('api', { type: 'login' })] : [] }
        : { state: s },
    },
    api: {
      at: [8], color: '#e0524f',
      state: {},
      on: (s, m) =>
        m.type === 'login' ? { state: s, send: [to('auth', { type: 'check', client: m.from })] }
        : m.type === 'ok' ? { state: s, send: [to('db', { type: 'load', client: m.client })] }
        : m.type === 'rows' ? { state: s, send: [to(m.client, { type: 'page' })] }
        : { state: s },
    },
    auth: {
      at: [14], color: '#7a5af5',
      state: {},
      on: (s, m) => m.type === 'check' ? { state: s, send: [reply(m, { type: 'ok', client: m.client })] } : { state: s },
    },
    db: {
      at: [23], color: '#2e9e6e',
      state: {},
      on: (s, m) => m.type === 'load' ? { state: s, send: [reply(m, { type: 'rows', client: m.client })] } : { state: s },
    },
  },
}`,
},

  create(spec) {
    const lat = new Lattice(spec.world);
    const impure = new Set();
    const names = Object.keys(spec.computers);
    const actors = names.map((name) => {
      const d = spec.computers[name];
      return { name, def: d, at: d.at, state: structuredClone(d.state ?? {}), mailbox: [], down: false, handled: 0 };
    });
    const byName = new Map(actors.map((a) => [a.name, a]));
    let flying = [];
    let seq = 0;
    let time = 0;
    let dropped = 0;

    const handle = (a, msg) => {
      const r = pure(a.def.on, [a.state, msg], () => impure.add(a.name)) ?? {};
      if (r.state !== undefined) a.state = r.state;
      for (const [target, payload] of r.send ?? []) {
        const b = byName.get(target);
        if (!b) continue;
        flying.push({ id: 'm' + seq++, from: a.name, to: target, payload: { ...payload, from: a.name }, route: path(a.at, b.at), i: 0, color: a.def.color });
      }
    };

    return {
      shape: spec.world,
      impure,
      get time() {
        return time;
      },
      tick() {
        // 1. Messages move one cell; arrivals go into mailboxes.
        const still = [];
        for (const m of flying) {
          m.i++;
          if (m.i >= m.route.length - 1) {
            const b = byName.get(m.to);
            if (b.down) dropped++;
            else b.mailbox.push(m.payload);
          } else still.push(m);
        }
        flying = still;
        // 2. Each computer: its clock tick, then up to `rate` messages.
        for (const a of actors) {
          if (a.down) {
            dropped += a.mailbox.length;
            a.mailbox = [];
            continue;
          }
          handle(a, { type: 'tick', time });
          const n = Math.min(a.def.rate ?? 2, a.mailbox.length);
          for (let k = 0; k < n; k++) handle(a, a.mailbox.shift());
          a.handled += n;
        }
        time++;
      },
      items() {
        const items = [];
        for (const a of actors) {
          items.push({ id: a.name, owner: a.name, at: a.at, kind: 'c', color: a.down ? '#8a90a0' : a.def.color, label: a.name, hatch: a.down });
          const q = Math.min(a.mailbox.length, 12);
          for (let i = 0; i < q; i++) {
            const c = a.at.slice();
            c[1] = c[1] - 1 - i; // the mailbox tail grows "up" along axis 1
            if (!lat.inside(c)) break;
            items.push({ id: a.name + '#q' + i, owner: a.name, at: c, kind: 'd', color: i >= 5 ? '#e0524f' : '#f2a33a' });
          }
        }
        for (const m of flying) {
          items.push({ id: m.id, owner: m.from, at: m.route[m.i], kind: 'token', color: m.color, label: m.payload.type });
        }
        return items;
      },
      processes() {
        return actors.map((a) => ({
          id: a.name,
          label: a.name,
          status: a.down ? 'fail' : a.mailbox.length ? 'run' : 'wait',
          note: a.def.note ? a.def.note(a.state) : `mail ${a.mailbox.length}`,
        }));
      },
      click(c) {
        const a = actors.find((x) => x.at.every((v, i) => v === c[i]));
        if (!a) return;
        if (a.name === 'browser' || a.def.clickable) handle(a, { type: 'click' });
        else a.down = !a.down;
      },
      status() {
        return `${flying.length} messages in flight · ${dropped} dropped`;
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
