// The drawing side ([d]): takes a list of items from a runtime, animates every
// item by identity with springs, and lets a renderer decide how the lattice
// becomes pixels. The runtime never knows which renderer is on screen.
//
// item = { id, at: [..any rank..], kind: 'c' | 'd' | 'cell' | 'token' | 'wire',
//          color, ring?, label?, hatch?, glow?: 0..1, size?: 0..1, fade?: 0..1 }

const K = 190;
const D = 21;

export class Stage {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.anim = new Map();
    this.mode = 'flat';
    this.shape = [8, 8];
    this.slice = 0;
    this.history = [];
    this.historyMax = 36;
    this.highlight = null;
    this.hover = null;
  }

  setShape(shape) {
    this.shape = shape.slice();
    this.slice = Math.min(this.slice, (shape[2] ?? 1) - 1);
    this.history = [];
    this.anim.clear();
  }

  setItems(items) {
    const seen = new Set();
    for (const it of items) {
      seen.add(it.id);
      const p = pad3(it.at);
      const col = rgb(it.color || '#888');
      let a = this.anim.get(it.id);
      if (!a) {
        a = { p: p.slice(), v: [0, 0, 0], s: 0, vs: 0, col: col.slice() };
        this.anim.set(it.id, a);
      }
      a.item = it;
      a.tp = p;
      a.ts = it.size ?? 1;
      a.tcol = col;
      a.gone = false;
    }
    for (const [id, a] of this.anim) {
      if (!seen.has(id)) {
        a.ts = 0;
        a.gone = true;
      }
    }
  }

  // Remember the settled targets of this tick, for the space-time renderer.
  record() {
    const snap = [];
    for (const a of this.anim.values()) {
      if (a.gone) continue;
      snap.push({ at: a.tp.slice(), color: a.item.color, kind: a.item.kind, hatch: a.item.hatch, label: a.item.label });
    }
    this.history.push(snap);
    if (this.history.length > this.historyMax) this.history.shift();
  }

  update(dt) {
    const steps = Math.max(1, Math.ceil(dt / (1 / 120)));
    const h = Math.min(dt, 0.1) / steps;
    for (const [id, a] of this.anim) {
      for (let k = 0; k < steps; k++) {
        for (let i = 0; i < 3; i++) {
          a.v[i] += (K * (a.tp[i] - a.p[i]) - D * a.v[i]) * h;
          a.p[i] += a.v[i] * h;
        }
        a.vs += (K * (a.ts - a.s) - D * a.vs) * h;
        a.s += a.vs * h;
      }
      const t = Math.min(1, dt * 10);
      for (let i = 0; i < 3; i++) a.col[i] += (a.tcol[i] - a.col[i]) * t;
      if (a.gone && a.s < 0.03) this.anim.delete(id);
    }
  }

  resize() {
    const r = this.canvas.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const w = Math.max(1, Math.round(r.width * dpr));
    const h = Math.max(1, Math.round(r.height * dpr));
    if (this.canvas.width !== w || this.canvas.height !== h) {
      this.canvas.width = w;
      this.canvas.height = h;
    }
    this.dpr = dpr;
    this.W = r.width;
    this.H = r.height;
  }

  render() {
    this.resize();
    const g = this.ctx;
    g.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    g.clearRect(0, 0, this.W, this.H);
    if (this.mode === 'flat') this.renderFlat(g);
    else if (this.mode === 'voxel') this.renderIso(g, false);
    else if ((this.shape[1] ?? 1) === 1 && (this.shape[2] ?? 1) === 1) this.renderTimeFlat(g);
    else this.renderIso(g, true);
  }

  // ---- flat: axes 0 and 1 on screen; a rank-3 world shows one slice ----

  flatLayout() {
    const [W, H] = [this.shape[0], this.shape[1] ?? 1];
    const m = 18;
    const cs = Math.max(2, Math.floor(Math.min((this.W - 2 * m) / W, (this.H - 2 * m) / H)));
    const ox = Math.floor((this.W - cs * W) / 2);
    const oy = Math.floor((this.H - cs * H) / 2);
    return { W, H, cs, ox, oy };
  }

  renderFlat(g) {
    const { W, H, cs, ox, oy } = this.flatLayout();
    g.fillStyle = cssVar('--paper');
    g.fillRect(ox, oy, cs * W, cs * H);
    if (cs >= 8) {
      g.strokeStyle = cssVar('--grid');
      g.lineWidth = 1;
      g.setLineDash([4, 4]);
      g.beginPath();
      for (let x = 1; x < W; x++) {
        g.moveTo(ox + x * cs + 0.5, oy);
        g.lineTo(ox + x * cs + 0.5, oy + H * cs);
      }
      for (let y = 1; y < H; y++) {
        g.moveTo(ox, oy + y * cs + 0.5);
        g.lineTo(ox + W * cs, oy + y * cs + 0.5);
      }
      g.stroke();
      g.setLineDash([]);
    }
    g.strokeStyle = cssVar('--frame');
    g.lineWidth = 1.5;
    g.strokeRect(ox + 0.5, oy + 0.5, cs * W, cs * H);

    const rank3 = (this.shape[2] ?? 1) > 1;
    const list = [...this.anim.values()].sort((a, b) => order(a.item.kind) - order(b.item.kind));
    for (const a of list) {
      let alpha = a.item.fade ?? 1;
      if (rank3 && Math.round(a.tp[2]) !== this.slice) {
        if (a.item.kind === 'cell') continue;
        alpha *= 0.12;
      }
      const x = ox + a.p[0] * cs;
      const y = oy + a.p[1] * cs;
      this.drawTile(g, a, x, y, cs, alpha);
    }
  }

  drawTile(g, a, x, y, cs, alpha) {
    const it = a.item;
    const s = Math.max(0, a.s);
    if (s <= 0.01) return;
    const col = a.col;
    g.globalAlpha = alpha;
    if (it.kind === 'cell') {
      const d = (cs * (1 - s)) / 2;
      g.fillStyle = css(col);
      g.fillRect(x + d, y + d, cs * s + 0.5, cs * s + 0.5);
      g.globalAlpha = 1;
      return;
    }
    if (it.kind === 'token') {
      const r = cs * 0.22 * s;
      g.fillStyle = css(col);
      g.beginPath();
      g.arc(x + cs / 2, y + cs / 2, r, 0, Math.PI * 2);
      g.fill();
      if (it.label && cs >= 18) {
        g.fillStyle = cssVar('--ink');
        g.font = `${Math.max(9, cs * 0.26)}px ui-sans-serif, system-ui`;
        g.textAlign = 'center';
        g.fillText(it.label, x + cs / 2, y - 2);
      }
      g.globalAlpha = 1;
      return;
    }
    const inset = it.kind === 'wire' ? cs * 0.3 : cs * 0.06;
    const w = (cs - inset * 2) * s;
    const cx = x + cs / 2;
    const cy = y + cs / 2;
    const rx = cx - w / 2;
    const ry = cy - w / 2;
    if (it.kind === 'wire') {
      const glow = it.glow ?? 0;
      g.fillStyle = css(lerp3(rgb(cssVar('--wire')), col, glow));
      g.fillRect(rx, ry, w, w);
      g.globalAlpha = 1;
      return;
    }
    // Tint toward the paper colour, so labels stay readable in light and dark.
    g.fillStyle = css(lerp3(col, rgb(cssVar('--paper')), it.kind === 'c' ? 0.72 : 0.8));
    roundRect(g, rx, ry, w, w, Math.min(5, cs * 0.12));
    g.fill();
    if (it.hatch) {
      hatch(g, rx, ry, w, w, css(col));
      roundRect(g, rx, ry, w, w, Math.min(5, cs * 0.12));
    }
    g.strokeStyle = it.ring || css(col);
    g.lineWidth = it.kind === 'c' ? 2 : 1.5;
    g.stroke();
    if (it.glow) {
      g.fillStyle = `rgba(${col[0] | 0},${col[1] | 0},${col[2] | 0},${0.35 * it.glow})`;
      g.fill();
    }
    if (it.inner && it.innerShape && w > 12) {
      // A computer that contains a world shows a live miniature of it.
      const [iw, ih] = [it.innerShape[0], it.innerShape[1] ?? 1];
      const m = w * 0.12;
      const ics = (w - 2 * m) / Math.max(iw, ih);
      const ix = rx + (w - ics * iw) / 2;
      const iy = ry + (w - ics * ih) / 2 + (it.label ? w * 0.12 : 0);
      for (const c of it.inner) {
        g.fillStyle = c.color;
        g.fillRect(ix + c.at[0] * ics + 0.5, iy + (c.at[1] ?? 0) * ics + 0.5, Math.max(1, ics - 1), Math.max(1, ics - 1));
      }
      roundRect(g, rx, ry, w, w, Math.min(5, cs * 0.12));
    }
    if (this.highlight != null && this.highlight === it.owner) {
      g.strokeStyle = cssVar('--accent');
      g.lineWidth = 3;
      roundRect(g, rx - 3, ry - 3, w + 6, w + 6, 6);
      g.stroke();
    }
    if (it.label && cs >= 14) {
      g.fillStyle = cssVar('--ink');
      g.font = `${Math.max(9, Math.min(15, cs * 0.3))}px ui-sans-serif, system-ui`;
      g.textAlign = 'center';
      g.textBaseline = 'middle';
      g.fillText(fit(g, it.label, w - 4), cx, it.inner && w > 12 ? ry + Math.min(14, w * 0.16) : cy + 0.5);
    }
    g.globalAlpha = 1;
  }

  // ---- rank-1 world + time = a 2D picture: time goes down the screen ----
  // With actors on a line and messages as tokens, this is a sequence diagram
  // that nobody drew: it is just the history of the world.

  renderTimeFlat(g) {
    const W = this.shape[0];
    const T = this.historyMax;
    const m = 24;
    const cw = Math.max(2, Math.min(64, (this.W - 2 * m) / W));
    const ch = Math.max(2, Math.min(cw, (this.H - 2 * m - 40) / T));
    const ox = (this.W - cw * W) / 2;
    const oy = m + 10;
    g.fillStyle = cssVar('--muted');
    g.font = '12px ui-sans-serif, system-ui';
    g.textAlign = 'left';
    g.fillText('time ↓ (each row is one tick of a 1D world)', ox, m - 4);
    const n = this.history.length;
    const last = this.history[n - 1] || [];
    g.strokeStyle = cssVar('--grid');
    g.setLineDash([3, 4]);
    for (const it of last) {
      if (it.kind !== 'c') continue;
      const x = ox + (it.at[0] + 0.5) * cw;
      g.beginPath();
      g.moveTo(x, oy);
      g.lineTo(x, oy + T * ch);
      g.stroke();
    }
    g.setLineDash([]);
    const labels = new Map();
    this.history.forEach((snap, t) => {
      const y = oy + t * ch;
      for (const it of snap) {
        const x = ox + it.at[0] * cw;
        const col = rgb(it.color);
        if (it.kind === 'token') {
          g.fillStyle = css(col);
          g.beginPath();
          g.arc(x + cw / 2, y + ch / 2, Math.max(1.5, Math.min(cw, ch) * 0.3), 0, Math.PI * 2);
          g.fill();
        } else if (it.kind === 'c') {
          g.fillStyle = css(lerp3(col, [255, 255, 255], it.hatch ? 0.7 : 0.35));
          g.fillRect(x + cw * 0.15, y, cw * 0.7, ch + 0.5);
          if (it.label) labels.set(it.at[0], { label: it.label, y: y + ch });
        } else {
          g.fillStyle = css(lerp3(col, [255, 255, 255], 0.3));
          g.globalAlpha = 0.7;
          g.fillRect(x + 1, y + 1, cw - 2, ch - 2);
          g.globalAlpha = 1;
        }
      }
    });
    g.fillStyle = cssVar('--ink');
    g.font = '11px ui-sans-serif, system-ui';
    for (const [x, { label, y }] of labels) {
      g.save();
      g.translate(ox + (x + 0.5) * cw + 4, Math.min(y + 4, oy + T * ch + 4));
      g.rotate(Math.PI / 4);
      g.textAlign = 'left';
      g.fillText(label, 0, 0);
      g.restore();
    }
  }

  // ---- iso: voxels. Axis 0 and 1 lie on the floor, axis 2 is height. ----
  // In space-time mode a rank-2 world (or one slice of a rank-3 world)
  // is stacked by tick: time becomes the height axis.

  isoLayout(Z) {
    const [W, H] = [this.shape[0], this.shape[1] ?? 1];
    // Unit tile: width 2, height 1, cube height 1.15 (in units of th).
    const spanX = W + H;
    const spanY = (W + H) / 2 + Z * 1.15;
    const th = Math.max(2, Math.min((this.W - 40) / spanX, (this.H - 40) / spanY));
    const tw = th * 2;
    const tz = th * 1.15;
    const ox = this.W / 2 - ((W - H) * tw) / 4;
    const oy = (this.H - spanY * th) / 2 + Z * tz;
    return { W, H, tw, th, tz, ox, oy };
  }

  renderIso(g, spacetime) {
    const rank3 = (this.shape[2] ?? 1) > 1;
    const Z = spacetime ? this.historyMax : rank3 ? this.shape[2] : 1;
    const L = this.isoLayout(Z);
    const P = (x, y, z) => [L.ox + ((x - y) * L.tw) / 2, L.oy + ((x + y) * L.th) / 2 - z * L.tz];
    // Floor.
    g.strokeStyle = cssVar('--grid');
    g.setLineDash([4, 4]);
    g.beginPath();
    const c = [P(0, 0, 0), P(L.W, 0, 0), P(L.W, L.H, 0), P(0, L.H, 0)];
    g.moveTo(...c[0]);
    for (const p of c.slice(1)) g.lineTo(...p);
    g.closePath();
    g.stroke();
    g.setLineDash([]);

    const cubes = [];
    if (spacetime) {
      const n = this.history.length;
      this.history.forEach((snap, t) => {
        const age = n - 1 - t;
        for (const it of snap) {
          if (rank3 && it.at[2] !== this.slice) continue;
          if (it.kind === 'wire' && age > 0) continue;
          const tok = it.kind === 'token';
          cubes.push({ x: it.at[0], y: it.at[1] ?? 0, z: t, s: tok ? 0.4 : 1, col: rgb(it.color), alpha: age === 0 ? 1 : 0.25 + 0.6 * (1 - age / n), kind: tok ? 'd' : it.kind });
        }
      });
    } else {
      for (const a of this.anim.values()) {
        if (a.s <= 0.02) continue;
        const col = a.item.kind === 'wire' ? lerp3(rgb(cssVar('--wire')), a.col, a.item.glow ?? 0) : a.col;
        cubes.push({ x: a.p[0], y: a.p[1], z: rank3 ? a.p[2] : 0, s: a.s, col, alpha: a.item.fade ?? 1, kind: a.item.kind, label: a.item.label, hl: this.highlight != null && this.highlight === a.item.owner });
      }
    }
    cubes.sort((a, b) => a.x + a.y - (b.x + b.y) || a.z - b.z);
    if (!spacetime && rank3) {
      g.fillStyle = 'rgba(0,0,0,0.18)';
      for (const q of cubes) {
        if (q.z < 0.5) continue;
        poly(g, [P(q.x, q.y, 0), P(q.x + 1, q.y, 0), P(q.x + 1, q.y + 1, 0), P(q.x, q.y + 1, 0)], 'rgba(0,0,0,0.18)');
        const [sx, sy] = P(q.x + 0.5, q.y + 0.5, 0);
        const [tx, ty] = P(q.x + 0.5, q.y + 0.5, q.z);
        g.strokeStyle = 'rgba(128,128,128,0.35)';
        g.setLineDash([2, 3]);
        g.beginPath();
        g.moveTo(sx, sy);
        g.lineTo(tx, ty);
        g.stroke();
        g.setLineDash([]);
      }
    }
    for (const q of cubes) {
      g.globalAlpha = q.alpha;
      if (q.kind === 'token') {
        const [sx, sy] = P(q.x + 0.5, q.y + 0.5, q.z + 0.6);
        g.fillStyle = css(q.col);
        g.beginPath();
        g.arc(sx, sy, L.th * 0.45 * q.s, 0, Math.PI * 2);
        g.fill();
        continue;
      }
      const h = q.kind === 'wire' ? 0.25 : q.kind === 'cell' ? 1 : 1;
      cube(g, P, q.x, q.y, q.z, q.s, h, q.col, q.kind === 'c', q.hl);
      if (q.label && L.th > 9 && !spacetime) {
        const [sx, sy] = P(q.x + 0.5, q.y + 0.5, q.z + h * q.s);
        g.fillStyle = cssVar('--ink');
        g.font = `${Math.min(13, L.th * 0.8)}px ui-sans-serif, system-ui`;
        g.textAlign = 'center';
        g.textBaseline = 'middle';
        g.fillText(q.label.slice(0, 6), sx, sy);
      }
    }
    g.globalAlpha = 1;
    if (spacetime) {
      g.fillStyle = cssVar('--muted');
      g.font = '12px ui-sans-serif, system-ui';
      g.textAlign = 'left';
      g.fillText(`time ↑  (${this.history.length} ticks, newest on top)`, 12, 18);
    }
  }

  // Screen point to lattice coordinate (floor plane, or the current slice).
  pick(px, py) {
    if (this.mode === 'flat') {
      const { W, H, cs, ox, oy } = this.flatLayout();
      const x = Math.floor((px - ox) / cs);
      const y = Math.floor((py - oy) / cs);
      if (x < 0 || y < 0 || x >= W || y >= H) return null;
      const c = [x, y];
      if (this.shape.length > 2) c.push(this.slice);
      return c.slice(0, this.shape.length);
    }
    const rank3 = (this.shape[2] ?? 1) > 1;
    const Z = this.mode === 'spacetime' ? this.historyMax : rank3 ? this.shape[2] : 1;
    const L = this.isoLayout(Z);
    const z = this.mode === 'spacetime' ? Math.max(0, this.history.length - 1) + 1 : 0;
    const a = (px - L.ox) / (L.tw / 2);
    const b = (py - L.oy + z * L.tz) / (L.th / 2);
    const x = Math.floor((a + b) / 2);
    const y = Math.floor((b - a) / 2);
    if (x < 0 || y < 0 || x >= L.W || y >= L.H) return null;
    const c = [x, y];
    if (this.shape.length > 2) c.push(rank3 && this.mode === 'voxel' ? 0 : this.slice);
    return c.slice(0, this.shape.length);
  }
}

// ---- helpers ----

function cube(g, P, x, y, z, s, h, col, strong, hl) {
  const d = (1 - s) / 2;
  const x0 = x + d;
  const y0 = y + d;
  const x1 = x + 1 - d;
  const y1 = y + 1 - d;
  const z0 = z;
  const z1 = z + h * s;
  const top = [P(x0, y0, z1), P(x1, y0, z1), P(x1, y1, z1), P(x0, y1, z1)];
  const left = [P(x0, y1, z1), P(x1, y1, z1), P(x1, y1, z0), P(x0, y1, z0)];
  const right = [P(x1, y0, z1), P(x1, y1, z1), P(x1, y1, z0), P(x1, y0, z0)];
  poly(g, left, css(shade(col, 0.72)));
  poly(g, right, css(shade(col, 0.86)));
  poly(g, top, css(strong ? lerp3(col, [255, 255, 255], 0.35) : col));
  if (strong || hl) {
    g.strokeStyle = hl ? cssVar('--accent') : css(shade(col, 0.55));
    g.lineWidth = hl ? 2.5 : 1;
    g.beginPath();
    g.moveTo(...top[0]);
    for (const p of top.slice(1)) g.lineTo(...p);
    g.closePath();
    g.stroke();
  }
}

function poly(g, pts, fill) {
  g.fillStyle = fill;
  g.beginPath();
  g.moveTo(...pts[0]);
  for (const p of pts.slice(1)) g.lineTo(...p);
  g.closePath();
  g.fill();
}

function roundRect(g, x, y, w, h, r) {
  g.beginPath();
  g.roundRect(x, y, w, h, r);
}

function hatch(g, x, y, w, h, color) {
  g.save();
  g.clip();
  g.strokeStyle = color;
  g.globalAlpha *= 0.45;
  g.lineWidth = 1;
  g.beginPath();
  for (let k = -h; k < w; k += 4) {
    g.moveTo(x + k, y + h);
    g.lineTo(x + k + h, y);
  }
  g.stroke();
  g.restore();
}

function fit(g, text, w) {
  if (g.measureText(text).width <= w) return text;
  let t = text;
  while (t.length > 1 && g.measureText(t + '…').width > w) t = t.slice(0, -1);
  return t + '…';
}

const order = (k) => ({ cell: 0, wire: 1, d: 2, c: 3, token: 4 })[k] ?? 2;
const pad3 = (c) => [c[0] ?? 0, c[1] ?? 0, c[2] ?? 0];
const lerp3 = (a, b, t) => [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t];
const shade = (c, k) => [c[0] * k, c[1] * k, c[2] * k];
const css = (c) => `rgb(${c[0] | 0},${c[1] | 0},${c[2] | 0})`;

const rgbCache = new Map();
export function rgb(hex) {
  let v = rgbCache.get(hex);
  if (v) return v;
  const h = hex.replace('#', '');
  const n = parseInt(h.length === 3 ? h.split('').map((c) => c + c).join('') : h, 16);
  v = [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  rgbCache.set(hex, v);
  return v;
}

const varCache = new Map();
let varScheme = null;
function cssVar(name) {
  const scheme = matchMedia('(prefers-color-scheme: dark)').matches;
  if (scheme !== varScheme) {
    varCache.clear();
    varScheme = scheme;
  }
  let v = varCache.get(name);
  if (!v) {
    v = getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888';
    varCache.set(name, v);
  }
  return v;
}
