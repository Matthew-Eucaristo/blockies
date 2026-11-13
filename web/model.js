// The timeline from the kernel, and questions about it at any moment.
// Renderers only ask this module; they never look at the script.

/**
 * @typedef {{id: string, label: string, kind?: string, color?: string, parent?: string,
 *   at: number[], start: number, end: number, line: number, tap?: boolean, skin?: string, min?: number, max?: number}} Proc
 * @typedef {{from: number, to: number, label: string, sent: number, arrive: number,
 *   handled: number, line: number}} Msg
 * @typedef {{t: number, e: 'state' | 'note' | 'body' | 'value' | 'tap' | 'focus' | 'move', p: number, state?: string,
 *   text?: string, cells?: number[][], value?: number, line?: number}} Ev
 * @typedef {{line: number, code: string, detail: string}} Err
 * @typedef {{name: string, color?: string, states?: string[]}} Kind
 * @typedef {{version: number, world: number[], end: number, kinds: Kind[], procs: Proc[],
 *   msgs: Msg[], events: Ev[], errors: Err[]}} Timeline
 * @typedef {{board: string, rev: number, script: string, timeline: Timeline}} Frame
 * @typedef {'good' | 'busy' | 'wait' | 'bad' | 'plain'} Tone
 */

/** @type {Record<string, string>} */
export const PALETTE = {
  blue: '#3b6ff5',
  red: '#e0524f',
  green: '#2e9e6e',
  amber: '#e29a2d',
  purple: '#8b5cf6',
  teal: '#14a3a3',
  pink: '#e05297',
  gray: '#8a90a0',
};
const AUTO = ['blue', 'red', 'green', 'purple', 'amber', 'teal', 'pink'];

/** @type {Record<string, Tone>} */
const TONES = {};
for (const w of ['ok', 'done', 'ready', 'up', 'healthy', 'success', 'pass', 'passed', 'green', 'served', 'hit']) TONES[w] = 'good';
for (const w of ['busy', 'run', 'running', 'working', 'active', 'load', 'loading', 'thinking', 'writing', 'reading', 'sending', 'hot']) TONES[w] = 'busy';
for (const w of ['wait', 'waiting', 'blocked', 'queued', 'pending', 'plan', 'planning', 'sleep', 'sleeping', 'idle', 'paused', 'cold', 'miss']) TONES[w] = 'wait';
for (const w of ['fail', 'failed', 'error', 'down', 'dead', 'crash', 'crashed', 'timeout', 'red', 'broken', 'lost']) TONES[w] = 'bad';

/** A number the way people read it: separators, at most 2 decimals. @param {number} n */
export function formatNum(n) {
  return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
}

/** @param {string} state @returns {Tone} */
export function tone(state) {
  return TONES[state.toLowerCase()] ?? 'plain';
}

export class Model {
  /** @param {Timeline} tl */
  constructor(tl) {
    this.tl = tl;
    this.procs = tl.procs;
    this.msgs = tl.msgs;
    this.end = tl.end;
    this.world = tl.world;
    /** @type {Ev[][]} */ this.states = tl.procs.map(() => []);
    /** @type {Ev[][]} */ this.notes = tl.procs.map(() => []);
    /** @type {Ev[][]} */ this.bodies = tl.procs.map(() => []);
    /** @type {Ev[][]} */ this.values = tl.procs.map(() => []);
    /** @type {Ev[]} */ this.taps = [];
    /** @type {Ev[]} */ this.focus = [];
    /** @type {Ev[][]} */ this.moves = tl.procs.map(() => []);
    for (const e of tl.events) {
      if (e.e === 'tap') this.taps.push(e);
      else if (e.e === 'focus') this.focus.push(e);
      else ({ state: this.states, note: this.notes, body: this.bodies, value: this.values, move: this.moves })[e.e]?.[e.p].push(e);
    }
    /** @type {Map<string, number>} */
    this.byId = new Map();
    tl.procs.forEach((p, i) => this.byId.set(p.id + '@' + p.start, i));

    // Colors: the script's color, else the kind's, else the parent's, else
    // the next automatic one per top-level process.
    const kindColor = new Map(tl.kinds.map((k) => [k.name, k.color]));
    /** @type {string[]} */
    this.colors = [];
    let auto = 0;
    tl.procs.forEach((p, i) => {
      let name = p.color || (p.kind && kindColor.get(p.kind));
      if (!name && p.parent) {
        const parent = this.parentOf(i);
        if (parent >= 0) {
          this.colors.push(this.colors[parent]);
          return;
        }
      }
      if (!name) name = AUTO[auto++ % AUTO.length];
      this.colors.push(PALETTE[name] ?? PALETTE.gray);
    });

    // Tree order: each process right after its parent. Used by the sequence
    // view and the process list.
    /** @type {number[]} */
    this.order = [];
    /** @type {number[]} */
    this.depth = tl.procs.map((p) => (p.parent ? p.id.split('/').length - 1 : 0));
    const kids = tl.procs.map(() => /** @type {number[]} */ ([]));
    const roots = [];
    for (let i = 0; i < tl.procs.length; i++) {
      const parent = this.parentOf(i);
      if (parent >= 0) kids[parent].push(i);
      else roots.push(i);
    }
    /** @param {number} i */
    const walk = (i) => {
      this.order.push(i);
      kids[i].forEach(walk);
    };
    roots.forEach(walk);

    // The part of the world that is ever used, so views can frame it.
    const lo = [Infinity, Infinity, Infinity];
    const hi = [-Infinity, -Infinity, -Infinity];
    /** @param {number[]} c */
    const grow = (c) => {
      for (let a = 0; a < 3; a++) {
        lo[a] = Math.min(lo[a], c[a] ?? 0);
        hi[a] = Math.max(hi[a], c[a] ?? 0);
      }
    };
    tl.procs.forEach((p) => grow(p.at));
    tl.events.forEach((e) => e.cells?.forEach(grow)); // bodies and moves
    if (!tl.procs.length) {
      lo.fill(0);
      hi.fill(0);
    }
    /** @type {{lo: number[], hi: number[]}} */
    this.bounds = { lo, hi };

    // First tick at which each script line does something, for jumping.
    /** @type {Map<number, number>} */
    this.lineTick = new Map();
    /** @param {number} line @param {number} t */
    const mark = (line, t) => {
      const seen = this.lineTick.get(line);
      if (line && (seen === undefined || t < seen)) this.lineTick.set(line, t);
    };
    tl.procs.forEach((p) => mark(p.line, p.start));
    tl.msgs.forEach((m) => mark(m.line, m.sent));
    tl.events.forEach((e) => mark(e.line ?? 0, e.t));
  }

  /** The process that was the parent of i when i started, or -1. @param {number} i */
  parentOf(i) {
    const p = this.procs[i];
    if (!p.parent) return -1;
    for (let j = i - 1; j >= 0; j--) {
      const q = this.procs[j];
      if (q.id === p.parent && q.start <= p.start && (q.end === -1 || q.end >= p.start)) return j;
    }
    return -1;
  }

  /** @param {number} i @param {number} t */
  alive(i, t) {
    const p = this.procs[i];
    return p.start <= t && (p.end === -1 || t < p.end);
  }

  /** @param {Ev[]} list @param {number} t */
  last(list, t) {
    let found = null;
    for (const e of list) {
      if (e.t > t) break;
      found = e;
    }
    return found;
  }

  /** @param {number} i @param {number} t */
  stateAt(i, t) {
    return this.last(this.states[i], t)?.state ?? '';
  }

  /** @param {number} i @param {number} t */
  noteAt(i, t) {
    return this.last(this.notes[i], t)?.text ?? '';
  }

  /** @param {number} i @param {number} t @returns {number[][]} */
  bodyAt(i, t) {
    return this.last(this.bodies[i], t)?.cells ?? [];
  }

  /** A process's value at tick t (a number or a text), or undefined. @param {number} i @param {number} t */
  valueAt(i, t) {
    const e = this.last(this.values[i], t);
    return e ? (e.text ?? e.value) : undefined;
  }

  /** Where a process's core is at tick t (people can move blocks). @param {number} i @param {number} t */
  atAt(i, t) {
    return this.last(this.moves[i], t)?.cells?.[0] ?? this.procs[i].at;
  }

  /** The process every screen should look at, at tick t (focus), or -1. @param {number} t */
  focusAt(t) {
    return this.last(this.focus, t)?.p ?? -1;
  }

  /** Taps that happened within the last tick before t, with age 0..1. @param {number} t */
  tapsAt(t) {
    return this.taps.filter((e) => e.t <= t && t - e.t < 1).map((e) => ({ p: e.p, k: t - e.t }));
  }

  /** The id of process i and every process under it. @param {number} i @returns {number[]} */
  subtree(i) {
    const id = this.procs[i].id;
    const out = [];
    for (let j = 0; j < this.procs.length; j++) {
      const q = this.procs[j].id;
      if (q === id || q.startsWith(id + '/')) out.push(j);
    }
    return out;
  }

  /** The part of the world a group of processes ever uses. @param {number[]} list */
  boundsOf(list) {
    const lo = [Infinity, Infinity, Infinity];
    const hi = [-Infinity, -Infinity, -Infinity];
    /** @param {number[]} c */
    const grow = (c) => {
      for (let a = 0; a < 3; a++) {
        lo[a] = Math.min(lo[a], c[a] ?? 0);
        hi[a] = Math.max(hi[a], c[a] ?? 0);
      }
    };
    for (const i of list) {
      grow(this.procs[i].at);
      for (const e of this.bodies[i]) e.cells?.forEach(grow);
      for (const e of this.moves[i]) e.cells?.forEach(grow);
    }
    if (!list.length) return this.bounds;
    return { lo, hi };
  }

  /** The latest process with this id, or -1. @param {string} id */
  find(id) {
    for (let i = this.procs.length - 1; i >= 0; i--) if (this.procs[i].id === id) return i;
    return -1;
  }

  /** Messages between sent and arrival at time t, with progress 0..1. @param {number} t */
  flying(t) {
    /** @type {{m: Msg, i: number, k: number}[]} */
    const out = [];
    this.msgs.forEach((m, i) => {
      if (m.sent <= t && t < m.arrive) out.push({ m, i, k: (t - m.sent) / (m.arrive - m.sent) });
    });
    return out;
  }

  /** Messages waiting in i's mailbox at tick t. @param {number} i @param {number} t */
  queued(i, t) {
    let n = 0;
    for (const m of this.msgs) if (m.to === i && m.arrive <= t && (m.handled === -1 ? true : t < m.handled)) n++;
    return n;
  }

  /** Script lines that act at tick t. @param {number} t */
  linesAt(t) {
    /** @type {Set<number>} */
    const lines = new Set();
    for (const p of this.procs) if (p.start === t) lines.add(p.line);
    for (const m of this.msgs) if (m.sent === t) lines.add(m.line);
    for (const e of this.tl.events) if (e.t === t && e.line) lines.add(e.line);
    return lines;
  }
}
