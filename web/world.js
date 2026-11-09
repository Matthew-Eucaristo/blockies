// The world view: the lattice itself, flat (axes 0 and 1 on screen) or as
// voxels (axis 2 is height). Cores, bodies and messages in flight.

import { Springs, alpha, cssVar, hatch, mix, pill, shade, toneColor } from './draw.js';
import { tone } from './model.js';

/** @typedef {import('./model.js').Model} Model */
/** @typedef {{mode: 'flat' | 'voxel', hover: number, selected: number, clock: number}} Opts */

export class WorldView {
  constructor() {
    this.springs = new Springs();
    this.slice = 0;
    /** @type {{i: number, x: number, y: number, r: number}[]} */
    this.hits = [];
    /** @type {Map<string, {at: number[], color: string, core: boolean}>} */
    this.seen = new Map();
  }

  /** @param {number} dt */
  step(dt) {
    this.springs.step(dt);
  }

  /**
   * @param {CanvasRenderingContext2D} g
   * @param {number} W @param {number} H
   * @param {Model} m
   * @param {number} t playhead in ticks
   * @param {Opts} o
   */
  draw(g, W, H, m, t, o) {
    const k = Math.min(m.end, Math.floor(t));
    const sp = this.springs;
    sp.begin();
    /** @type {{id: string, i: number, at: number[], color: string, core: boolean, s: number}[]} */
    const things = [];
    for (let i = 0; i < m.procs.length; i++) {
      if (!m.alive(i, k)) continue;
      const p = m.procs[i];
      const color = m.colors[i];
      m.bodyAt(i, k).forEach((c, j) => {
        const id = `b${i}.${j}`;
        things.push({ id, i, at: c, color, core: false, s: sp.set(id) });
        this.seen.set(id, { at: c, color, core: false });
      });
      const id = `p${i}`;
      things.push({ id, i, at: p.at, color, core: true, s: sp.set(id) });
      this.seen.set(id, { at: p.at, color, core: true });
    }
    for (const id of sp.leaving()) {
      const s = this.seen.get(id);
      if (s) things.push({ id, i: Number(id.slice(1).split('.')[0]), at: s.at, color: s.color, core: s.core, s: sp.get(id) });
    }
    this.hits = [];
    if (o.mode === 'voxel') this.voxel(g, W, H, m, t, k, things, o);
    else this.flat(g, W, H, m, t, k, things, o);
  }

  /** @param {number[]} at */
  ghost(at) {
    return (at[2] ?? 0) !== this.slice;
  }

  /**
   * @param {CanvasRenderingContext2D} g @param {number} W @param {number} H @param {Model} m
   * @param {number} t @param {number} k
   * @param {{id: string, i: number, at: number[], color: string, core: boolean, s: number}[]} things
   * @param {Opts} o
   */
  flat(g, W, H, m, t, k, things, o) {
    const [w, h] = [m.world[0], m.world[1] ?? 1];
    // Frame the part of the world the run uses (plus a cell of room), not
    // the whole world: agents rarely size worlds tightly.
    const { lo, hi } = m.bounds;
    const fx0 = Math.max(0, lo[0] - 1);
    const fx1 = Math.min(w, hi[0] + 2);
    const fy0 = Math.max(0, lo[1] - 1);
    const fy1 = Math.min(h, hi[1] + 2);
    const margin = 40;
    const cs = Math.max(3, Math.floor(Math.min((W - 2 * margin) / (fx1 - fx0), (H - 2 * margin) / (fy1 - fy0), 84)));
    const ox = Math.floor(W / 2 - cs * (fx0 + fx1) / 2);
    const oy = Math.floor(H / 2 - cs * (fy0 + fy1) / 2);
    /** @param {number[]} at */
    const center = (at) => [ox + (at[0] + 0.5) * cs, oy + ((at[1] ?? 0) + 0.5) * cs];
    const paper = cssVar('--paper');

    g.fillStyle = paper;
    g.fillRect(ox, oy, cs * w, cs * h);
    if (cs >= 10) {
      g.strokeStyle = cssVar('--grid');
      g.lineWidth = 1;
      g.setLineDash([3, 4]);
      g.beginPath();
      for (let x = 1; x < w; x++) {
        g.moveTo(ox + x * cs + 0.5, oy);
        g.lineTo(ox + x * cs + 0.5, oy + h * cs);
      }
      for (let y = 1; y < h; y++) {
        g.moveTo(ox, oy + y * cs + 0.5);
        g.lineTo(ox + w * cs, oy + y * cs + 0.5);
      }
      g.stroke();
      g.setLineDash([]);
    }
    g.strokeStyle = cssVar('--frame');
    g.lineWidth = 1.5;
    g.strokeRect(ox + 0.5, oy + 0.5, cs * w, cs * h);

    // Parent and child.
    g.strokeStyle = cssVar('--line');
    g.lineWidth = Math.max(1, cs * 0.06);
    for (const th of things) {
      if (!th.core) continue;
      const parent = m.parentOf(th.i);
      if (parent < 0 || !m.alive(parent, k)) continue;
      const [x1, y1] = center(m.procs[parent].at);
      const [x2, y2] = center(th.at);
      g.globalAlpha = th.s;
      g.beginPath();
      g.moveTo(x1, y1);
      g.lineTo(x2, y2);
      g.stroke();
    }
    g.globalAlpha = 1;

    // Bodies, then cores on top.
    for (const th of things) {
      if (th.core) continue;
      const [cx, cy] = center(th.at);
      const sz = cs * 0.78 * th.s;
      g.globalAlpha = this.ghost(th.at) ? 0.15 : 1;
      g.fillStyle = mix(th.color, paper, 0.55);
      g.beginPath();
      g.roundRect(cx - sz / 2, cy - sz / 2, sz, sz, Math.min(4, cs * 0.12));
      g.fill();
      g.strokeStyle = alpha(th.color, 0.8);
      g.lineWidth = 1;
      g.stroke();
    }
    g.globalAlpha = 1;
    for (const th of things) {
      if (!th.core) continue;
      const [cx, cy] = center(th.at);
      const sz = cs * 0.9 * th.s;
      const state = m.stateAt(th.i, k);
      const tn = tone(state);
      const x = cx - sz / 2;
      const y = cy - sz / 2;
      g.globalAlpha = this.ghost(th.at) ? 0.18 : 1;
      if (tn === 'busy') {
        const pulse = 0.25 + 0.2 * Math.sin(o.clock * 6);
        g.fillStyle = alpha(toneColor('busy'), pulse);
        g.beginPath();
        g.roundRect(x - 4, y - 4, sz + 8, sz + 8, 8);
        g.fill();
      }
      g.fillStyle = mix(th.color, paper, 0.72);
      g.beginPath();
      g.roundRect(x, y, sz, sz, Math.min(7, cs * 0.16));
      g.fill();
      if (tn === 'wait') {
        hatch(g, x, y, sz, sz, th.color);
        g.beginPath();
        g.roundRect(x, y, sz, sz, Math.min(7, cs * 0.16));
      }
      g.strokeStyle = tn === 'bad' ? toneColor('bad') : th.color;
      g.lineWidth = tn === 'bad' ? 3 : 2;
      g.stroke();
      if (th.i === o.selected || th.i === o.hover) {
        g.strokeStyle = cssVar('--accent');
        g.lineWidth = 2.5;
        g.beginPath();
        g.roundRect(x - 4, y - 4, sz + 8, sz + 8, 9);
        g.stroke();
      }
      if (state && cs >= 12) {
        g.fillStyle = toneColor(tn);
        g.beginPath();
        g.arc(x + sz - 1, y + 1, Math.max(3, cs * 0.1), 0, Math.PI * 2);
        g.fill();
        g.strokeStyle = paper;
        g.lineWidth = 1.5;
        g.stroke();
      }
      g.globalAlpha = 1;
      this.hits.push({ i: th.i, x: cx, y: cy, r: cs / 2 });
    }

    // Labels under cores, notes above them; messages draw on top.
    for (const th of things) {
      if (!th.core || th.s < 0.5 || this.ghost(th.at)) continue;
      const [cx, cy] = center(th.at);
      const p = m.procs[th.i];
      const state = m.stateAt(th.i, k);
      const label = state ? `${p.label} · ${state}` : p.label;
      pill(g, label, cx, cy + cs * 0.45 + 11, { size: 12, max: Math.max(90, cs * 3) });
      const note = m.noteAt(th.i, k);
      if (note) pill(g, note, cx, cy - cs * 0.45 - 12, { size: 11, color: cssVar('--muted'), max: 200 });
      // Messages in flight.
    const fly = m.flying(t);
    for (const { m: msg, k: p } of fly) {
      const a = center(m.procs[msg.from].at);
      const b = center(m.procs[msg.to].at);
      const e = ease(p);
      const x = a[0] + (b[0] - a[0]) * e;
      const y = a[1] + (b[1] - a[1]) * e;
      const hl = o.selected >= 0 && (msg.from === o.selected || msg.to === o.selected);
      g.strokeStyle = alpha(m.colors[msg.from], 0.35);
      g.lineWidth = 2;
      g.setLineDash([2, 4]);
      g.beginPath();
      g.moveTo(a[0], a[1]);
      g.lineTo(x, y);
      g.stroke();
      g.setLineDash([]);
      g.fillStyle = m.colors[msg.from];
      g.beginPath();
      g.arc(x, y, Math.max(4, cs * 0.16) * (hl ? 1.3 : 1), 0, Math.PI * 2);
      g.fill();
      g.strokeStyle = cssVar('--paper');
      g.lineWidth = 2;
      g.stroke();
      if (msg.label && fly.length <= 14) pill(g, msg.label, x, y - Math.max(12, cs * 0.35), { size: 11, max: 180 });
    }

  }
  }

  /**
   * @param {CanvasRenderingContext2D} g @param {number} W @param {number} H @param {Model} m
   * @param {number} t @param {number} k
   * @param {{id: string, i: number, at: number[], color: string, core: boolean, s: number}[]} things
   * @param {Opts} o
   */
  voxel(g, W, H, m, t, k, things, o) {
    const [w, h] = [m.world[0], m.world[1] ?? 1];
    const LAYER = 1.6;
    const { lo, hi } = m.bounds;
    const fx0 = Math.max(0, lo[0] - 1);
    const fx1 = Math.min(w, hi[0] + 2);
    const fy0 = Math.max(0, lo[1] - 1);
    const fy1 = Math.min(h, hi[1] + 2);
    const top = hi[2] * LAYER + 3;
    const spanX = fx1 - fx0 + (fy1 - fy0);
    const spanY = (fx1 - fx0 + (fy1 - fy0)) / 2 + top;
    const th = Math.max(3, Math.min((W - 80) / spanX, (H - 80) / spanY, 40));
    const tw = th * 2;
    const tz = th * 1.1;
    // Center the framed region: its floor diamond plus the height above it.
    const cx = ((fx0 + fx1) / 2 - (fy0 + fy1) / 2) * (tw / 2);
    const cy = ((fx0 + fx1) / 2 + (fy0 + fy1) / 2) * (th / 2);
    const ox = W / 2 - cx;
    const oy = H / 2 - cy + (top * tz) / 2;
    /** @param {number} x @param {number} y @param {number} z @returns {[number, number]} */
    const P = (x, y, z) => [ox + ((x - y) * tw) / 2, oy + ((x + y) * th) / 2 - z * tz];

    // Floor.
    g.fillStyle = cssVar('--paper');
    g.strokeStyle = cssVar('--frame');
    g.lineWidth = 1;
    const c = [P(0, 0, 0), P(w, 0, 0), P(w, h, 0), P(0, h, 0)];
    g.beginPath();
    g.moveTo(...c[0]);
    for (const p of c.slice(1)) g.lineTo(...p);
    g.closePath();
    g.fill();
    g.stroke();
    if (th >= 8) {
      g.strokeStyle = cssVar('--grid');
      g.setLineDash([3, 4]);
      g.beginPath();
      for (let x = 1; x < w; x++) {
        g.moveTo(...P(x, 0, 0));
        g.lineTo(...P(x, h, 0));
      }
      for (let y = 1; y < h; y++) {
        g.moveTo(...P(0, y, 0));
        g.lineTo(...P(w, y, 0));
      }
      g.stroke();
      g.setLineDash([]);
    }

    const cubes = things.map((q) => ({ ...q, z: (q.at[2] ?? 0) * LAYER }));
    cubes.sort((a, b) => a.at[0] + (a.at[1] ?? 0) - (b.at[0] + (b.at[1] ?? 0)) || a.z - b.z);
    for (const q of cubes) {
      const x = q.at[0];
      const y = q.at[1] ?? 0;
      const hgt = (q.core ? 1.3 : 0.55) * q.s;
      const inset = q.core ? 0.06 : 0.14;
      const state = q.core ? m.stateAt(q.i, k) : '';
      const tn = tone(state);
      const base = q.core ? mix(q.color, '#ffffff', 0.15) : mix(q.color, cssVar('--paper'), 0.35);
      if (q.z > 0) {
        // A shadow on the floor and a drop line, so height reads clearly.
        g.fillStyle = 'rgba(0,0,0,0.12)';
        const s = [P(x + inset, y + inset, 0), P(x + 1 - inset, y + inset, 0), P(x + 1 - inset, y + 1 - inset, 0), P(x + inset, y + 1 - inset, 0)];
        g.beginPath();
        g.moveTo(...s[0]);
        for (const p of s.slice(1)) g.lineTo(...p);
        g.fill();
      }
      cube(g, P, x + inset, y + inset, q.z, 1 - 2 * inset, hgt, base, q.core);
      if (q.core && tn !== 'plain') {
        const [sx, sy] = P(x + 0.5, y + 0.5, q.z + hgt);
        g.fillStyle = toneColor(tn);
        g.beginPath();
        g.ellipse(sx, sy, th * 0.42, th * 0.21, 0, 0, Math.PI * 2);
        g.fill();
      }
      if (q.core && (q.i === o.selected || q.i === o.hover)) {
        const [sx, sy] = P(x + 0.5, y + 0.5, q.z + hgt);
        g.strokeStyle = cssVar('--accent');
        g.lineWidth = 2.5;
        g.beginPath();
        g.ellipse(sx, sy, th * 1.1, th * 0.55, 0, 0, Math.PI * 2);
        g.stroke();
      }
      if (q.core) {
        const [sx, sy] = P(x + 0.5, y + 0.5, q.z + hgt / 2);
        this.hits.push({ i: q.i, x: sx, y: sy, r: th * 1.1 });
      }
    }

    for (const q of cubes) {
      if (!q.core || q.s < 0.5) continue;
      const p = m.procs[q.i];
      const [sx, sy] = P(q.at[0] + 0.5, (q.at[1] ?? 0) + 0.5, q.z + 1.3);
      const state = m.stateAt(q.i, k);
      pill(g, state ? `${p.label} · ${state}` : p.label, sx, sy - 12, { size: 12, max: 160 });
      const note = m.noteAt(q.i, k);
      if (note) pill(g, note, sx, sy - 31, { size: 11, color: cssVar('--muted'), max: 200 });
      // Messages arc over the world.
    const fly = m.flying(t);
    for (const { m: msg, k: p } of fly) {
      const a = m.procs[msg.from].at;
      const b = m.procs[msg.to].at;
      const e = ease(p);
      const x = a[0] + (b[0] - a[0]) * e + 0.5;
      const y = (a[1] ?? 0) + ((b[1] ?? 0) - (a[1] ?? 0)) * e + 0.5;
      const z0 = (a[2] ?? 0) * LAYER + (((b[2] ?? 0) - (a[2] ?? 0)) * LAYER) * e;
      const z = z0 + 1.4 + Math.sin(Math.PI * e) * 1.2;
      const [sx, sy] = P(x, y, z);
      const [fx, fy] = P(x, y, z0);
      g.fillStyle = 'rgba(0,0,0,0.15)';
      g.beginPath();
      g.ellipse(fx, fy, th * 0.3, th * 0.15, 0, 0, Math.PI * 2);
      g.fill();
      g.fillStyle = m.colors[msg.from];
      g.beginPath();
      g.arc(sx, sy, Math.max(3.5, th * 0.32), 0, Math.PI * 2);
      g.fill();
      g.strokeStyle = cssVar('--paper');
      g.lineWidth = 1.5;
      g.stroke();
      if (msg.label && fly.length <= 14) pill(g, msg.label, sx, sy - th * 0.9 - 6, { size: 11, max: 160 });
    }

  }
  }

  /** @param {number} x @param {number} y */
  pick(x, y) {
    let best = -1;
    let bd = Infinity;
    for (const h of this.hits) {
      const d = Math.hypot(h.x - x, h.y - y);
      if (d <= h.r && d < bd) {
        bd = d;
        best = h.i;
      }
    }
    return best;
  }
}

/** @param {number} p */
function ease(p) {
  return p < 0.5 ? 2 * p * p : 1 - (-2 * p + 2) ** 2 / 2;
}

/**
 * @param {CanvasRenderingContext2D} g
 * @param {(x: number, y: number, z: number) => [number, number]} P
 * @param {number} x @param {number} y @param {number} z @param {number} s @param {number} h
 * @param {string} color @param {boolean} strong
 */
function cube(g, P, x, y, z, s, h, color, strong) {
  const x1 = x + s;
  const y1 = y + s;
  const z1 = z + h;
  /** @param {[number, number][]} pts @param {string} fill */
  const face = (pts, fill) => {
    g.fillStyle = fill;
    g.beginPath();
    g.moveTo(...pts[0]);
    for (const p of pts.slice(1)) g.lineTo(...p);
    g.closePath();
    g.fill();
  };
  const hex = toHex(color);
  face([P(x, y1, z1), P(x1, y1, z1), P(x1, y1, z), P(x, y1, z)], shade(hex, 0.72));
  face([P(x1, y, z1), P(x1, y1, z1), P(x1, y1, z), P(x1, y, z)], shade(hex, 0.86));
  face([P(x, y, z1), P(x1, y, z1), P(x1, y1, z1), P(x, y1, z1)], color);
  if (strong) {
    g.strokeStyle = shade(hex, 0.55);
    g.lineWidth = 1;
    g.beginPath();
    const top = [P(x, y, z1), P(x1, y, z1), P(x1, y1, z1), P(x, y1, z1)];
    g.moveTo(...top[0]);
    for (const p of top.slice(1)) g.lineTo(...p);
    g.closePath();
    g.stroke();
  }
}

/** rgb(r,g,b) or #hex to #hex. @param {string} c */
function toHex(c) {
  if (c.startsWith('#')) return c;
  const n = c.match(/\d+/g)?.map(Number) ?? [128, 128, 128];
  return '#' + n.slice(0, 3).map((v) => v.toString(16).padStart(2, '0')).join('');
}
