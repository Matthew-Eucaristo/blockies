// The sequence view: the same run with time as an axis. One lifeline per
// process (children next to their parent), messages as arrows, and each
// lifeline colored by state, so it is a sequence diagram and a Gantt chart
// at once. Nobody draws it: it is the history of the world.

import { alpha, cssVar, hatch, pill, toneColor } from './draw.js';
import { tone } from './model.js';

/** @typedef {import('./model.js').Model} Model */

export class SequenceView {
  constructor() {
    /** @type {{i: number, x0: number, x1: number}[]} */
    this.cols = [];
    this.scroll = 0;
  }

  step() {}

  /**
   * @param {CanvasRenderingContext2D} g @param {number} W @param {number} H
   * @param {Model} m @param {number} t
   * @param {{hover: number, selected: number}} o
   */
  draw(g, W, H, m, t, o) {
    const n = m.order.length;
    const gutter = 44;
    const head = 46;
    const colW = Math.max(64, Math.min(190, (W - gutter - 16) / Math.max(1, n)));
    const x0 = gutter + Math.max(0, (W - gutter - 16 - colW * n) / 2);
    const ticks = m.end + 2;
    const avail = H - head - 16;
    const rowH = Math.max(16, Math.min(44, avail / ticks));
    const total = ticks * rowH;
    // Keep the playhead in view on long runs.
    const want = Math.max(0, Math.min(total - avail, t * rowH - avail * 0.6));
    this.scroll += (want - this.scroll) * 0.2;
    if (Math.abs(want - this.scroll) < 0.5) this.scroll = want;
    /** @param {number} tick */
    const Y = (tick) => head + 8 + tick * rowH - this.scroll;
    /** @type {Map<number, number>} */
    const X = new Map();
    m.order.forEach((i, c) => X.set(i, x0 + (c + 0.5) * colW));
    const ink = cssVar('--ink');
    const muted = cssVar('--muted');
    const paper = cssVar('--paper');

    g.fillStyle = paper;
    g.fillRect(0, 0, W, H);
    g.save();
    g.beginPath();
    g.rect(0, head, W, H - head);
    g.clip();

    // Tick grid.
    g.font = '10px ui-sans-serif, system-ui, sans-serif';
    g.textAlign = 'right';
    g.textBaseline = 'middle';
    const every = rowH >= 24 ? 1 : rowH >= 14 ? 2 : 5;
    for (let k = 0; k < ticks; k += every) {
      const y = Y(k);
      if (y < head - rowH || y > H + rowH) continue;
      g.strokeStyle = cssVar('--grid');
      g.lineWidth = 1;
      g.beginPath();
      g.moveTo(gutter - 4, Math.round(y) + 0.5);
      g.lineTo(W - 8, Math.round(y) + 0.5);
      g.stroke();
      g.fillStyle = muted;
      g.fillText(String(k), gutter - 10, y);
    }

    // Lifelines with state bars.
    for (const i of m.order) {
      const p = m.procs[i];
      const x = /** @type {number} */ (X.get(i));
      const end = p.end === -1 ? m.end + 1 : p.end;
      const sel = i === o.selected || i === o.hover;
      g.strokeStyle = sel ? cssVar('--accent') : cssVar('--line');
      g.lineWidth = sel ? 2 : 1;
      g.setLineDash([4, 4]);
      g.beginPath();
      g.moveTo(x, Y(p.start));
      g.lineTo(x, Y(end));
      g.stroke();
      g.setLineDash([]);
      const states = m.states[i];
      for (let s = 0; s < states.length; s++) {
        const a = states[s].t;
        const b = s + 1 < states.length ? states[s + 1].t : end;
        if (b <= a) continue;
        const tn = tone(states[s].state ?? '');
        const y0 = Y(a);
        const y1 = Y(b);
        const past = Math.min(1, Math.max(0, (t - a) / (b - a)));
        g.fillStyle = alpha(rgbHex(toneColor(tn)), 0.25);
        g.fillRect(x - 6, y0, 12, y1 - y0);
        g.fillStyle = toneColor(tn);
        g.fillRect(x - 6, y0, 12, (y1 - y0) * past);
        if (tn === 'wait') {
          g.beginPath();
          g.rect(x - 6, y0, 12, y1 - y0);
          hatch(g, x - 6, y0, 12, y1 - y0, ink);
        }
        if (rowH * (b - a) >= 16 && colW >= 80) {
          g.fillStyle = muted;
          g.font = '10px ui-sans-serif, system-ui, sans-serif';
          g.textAlign = 'left';
          g.textBaseline = 'top';
          g.globalAlpha = t >= a ? 1 : 0.4;
          g.fillText(states[s].state ?? '', x + 9, y0 + 2);
          g.globalAlpha = 1;
        }
      }
      // A process that starts later gets its name where it starts, and an
      // ended one a cross.
      if (p.start > 0) {
        g.globalAlpha = t >= p.start ? 1 : 0.35;
        pill(g, p.label, x, Y(p.start) - 1, { size: 11, max: colW - 8, bg: alpha(rgbHex(m.colors[i]), 0.18) });
        g.globalAlpha = 1;
      }
      if (p.end !== -1) {
        const y = Y(p.end);
        g.strokeStyle = t >= p.end ? ink : alpha(rgbHex(muted), 0.4);
        g.lineWidth = 2;
        g.beginPath();
        g.moveTo(x - 6, y - 6);
        g.lineTo(x + 6, y + 6);
        g.moveTo(x + 6, y - 6);
        g.lineTo(x - 6, y + 6);
        g.stroke();
      }
    }

    // Messages: arrows from send to arrival.
    const labelled = m.msgs.length <= 400;
    for (const msg of m.msgs) {
      const xa = X.get(msg.from);
      const xb = X.get(msg.to);
      if (xa === undefined || xb === undefined) continue;
      const ya = Y(msg.sent);
      const yb = Y(msg.arrive);
      if (Math.max(ya, yb) < head - 20 || Math.min(ya, yb) > H + 20) continue;
      const future = t < msg.sent;
      const prog = Math.min(1, Math.max(0, (t - msg.sent) / (msg.arrive - msg.sent)));
      const sel = o.selected >= 0 && (msg.from === o.selected || msg.to === o.selected);
      const color = m.colors[msg.from];
      g.globalAlpha = future ? 0.16 : o.selected >= 0 && !sel ? 0.35 : 1;
      g.strokeStyle = color;
      g.fillStyle = color;
      g.lineWidth = sel ? 2.5 : 1.6;
      if (msg.from === msg.to) {
        const w = Math.min(28, colW * 0.3);
        g.beginPath();
        g.moveTo(xa, ya);
        g.bezierCurveTo(xa + w * 1.6, ya, xa + w * 1.6, yb, xa + 6, yb);
        g.stroke();
        arrowHead(g, xa + 6, yb, xa + w, yb);
      } else {
        const p = future ? 1 : prog;
        const xe = xa + (xb - xa) * p;
        const ye = ya + (yb - ya) * p;
        const dir = Math.sign(xb - xa);
        g.beginPath();
        g.moveTo(xa + dir * 7, ya);
        g.lineTo(xe - dir * 7 * p, ye);
        g.stroke();
        if (p >= 1 || future) arrowHead(g, xb - dir * 7, yb, xa, ya);
        else {
          g.beginPath();
          g.arc(xe, ye, 4.5, 0, Math.PI * 2);
          g.fill();
        }
      }
      if (labelled && msg.label && (rowH >= 16 || sel)) {
        const mx = msg.from === msg.to ? xa + Math.min(30, colW * 0.35) + 4 : (xa + xb) / 2;
        const my = (ya + yb) / 2 - 9;
        pill(g, msg.label, mx, my, { size: 11, max: Math.max(70, Math.abs(xb - xa) - 16) });
      }
      g.globalAlpha = 1;
    }

    // Notes, to the right of their lifeline.
    for (const i of m.order) {
      const x = /** @type {number} */ (X.get(i));
      for (const e of m.notes[i]) {
        if (!e.text) continue;
        g.globalAlpha = t >= e.t ? 1 : 0.3;
        g.font = 'italic 11px ui-sans-serif, system-ui, sans-serif';
        const w = Math.min(colW * 1.6, g.measureText(e.text).width + 10);
        pill(g, e.text, x + 10 + w / 2, Y(e.t) + rowH * 0.5, { size: 11, color: muted, max: colW * 1.6 });
        g.globalAlpha = 1;
      }
    }

    // Playhead.
    const yp = Y(t);
    g.strokeStyle = cssVar('--accent');
    g.lineWidth = 1.5;
    g.beginPath();
    g.moveTo(gutter - 4, yp);
    g.lineTo(W - 8, yp);
    g.stroke();
    g.restore();

    // Sticky header with the process names.
    g.fillStyle = paper;
    g.fillRect(0, 0, W, head);
    g.strokeStyle = cssVar('--line');
    g.beginPath();
    g.moveTo(0, head + 0.5);
    g.lineTo(W, head + 0.5);
    g.stroke();
    this.cols = [];
    m.order.forEach((i, c) => {
      const p = m.procs[i];
      const x = x0 + (c + 0.5) * colW;
      const live = m.alive(i, Math.floor(Math.min(t, m.end)));
      const sel = i === o.selected || i === o.hover;
      g.globalAlpha = live ? 1 : 0.45;
      const indent = m.depth[i] > 0 ? '↳ ' : '';
      pill(g, indent + p.label, x, head / 2, {
        size: 12,
        max: colW - 6,
        bg: sel ? alpha(rgbHex(cssVar('--accent')), 0.25) : alpha(rgbHex(m.colors[i]), 0.2),
      });
      g.globalAlpha = 1;
      this.cols.push({ i, x0: x0 + c * colW, x1: x0 + (c + 1) * colW });
    });
    g.fillStyle = muted;
    g.font = '10px ui-sans-serif, system-ui, sans-serif';
    g.textAlign = 'right';
    g.fillText('tick', gutter - 10, head / 2);
  }

  /** @param {number} x @param {number} _y */
  pick(x, _y) {
    for (const c of this.cols) if (x >= c.x0 && x < c.x1) return c.i;
    return -1;
  }
}

/** @param {CanvasRenderingContext2D} g @param {number} x @param {number} y @param {number} fx @param {number} fy */
function arrowHead(g, x, y, fx, fy) {
  const a = Math.atan2(y - fy, x - fx);
  g.beginPath();
  g.moveTo(x, y);
  g.lineTo(x - 9 * Math.cos(a - 0.4), y - 9 * Math.sin(a - 0.4));
  g.lineTo(x - 9 * Math.cos(a + 0.4), y - 9 * Math.sin(a + 0.4));
  g.closePath();
  g.fill();
}

/** Any css color the page gives us, as #hex. @param {string} c */
function rgbHex(c) {
  if (c.startsWith('#')) return c.length === 4 ? '#' + [...c.slice(1)].map((d) => d + d).join('') : c;
  const n = c.match(/\d+/g)?.map(Number) ?? [128, 128, 128];
  return '#' + n.slice(0, 3).map((v) => v.toString(16).padStart(2, '0')).join('');
}
