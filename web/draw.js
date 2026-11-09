// Small drawing helpers shared by the views.

/** @param {string} name */
export function cssVar(name) {
  const dark = matchMedia('(prefers-color-scheme: dark)').matches;
  if (dark !== cache.dark) {
    cache.vars.clear();
    cache.dark = dark;
  }
  let v = cache.vars.get(name);
  if (!v) {
    v = getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888888';
    cache.vars.set(name, v);
  }
  return v;
}
const cache = { dark: /** @type {boolean | null} */ (null), vars: new Map() };

/** @type {Map<string, number[]>} */
const rgbCache = new Map();
/** @param {string} hex @returns {number[]} */
export function rgb(hex) {
  let v = rgbCache.get(hex);
  if (v) return v;
  const h = hex.replace('#', '');
  const n = parseInt(h.length === 3 ? h.split('').map((c) => c + c).join('') : h, 16);
  v = [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  rgbCache.set(hex, v);
  return v;
}

/** Mix two colors (hex) and return a css color. @param {string} a @param {string} b @param {number} t */
export function mix(a, b, t) {
  const x = rgb(a);
  const y = rgb(b);
  return `rgb(${(x[0] + (y[0] - x[0]) * t) | 0},${(x[1] + (y[1] - x[1]) * t) | 0},${(x[2] + (y[2] - x[2]) * t) | 0})`;
}

/** @param {string} hex @param {number} a */
export function alpha(hex, a) {
  const c = rgb(hex);
  return `rgba(${c[0]},${c[1]},${c[2]},${a})`;
}

/** @param {string} hex @param {number} k */
export function shade(hex, k) {
  const c = rgb(hex);
  return `rgb(${(c[0] * k) | 0},${(c[1] * k) | 0},${(c[2] * k) | 0})`;
}

/** Tone colors, from the page's CSS variables. @param {import('./model.js').Tone} t */
export function toneColor(t) {
  return cssVar(t === 'plain' ? '--muted' : '--' + t);
}

/** Diagonal hatching inside the current path. @param {CanvasRenderingContext2D} g @param {number} x @param {number} y @param {number} w @param {number} h @param {string} color */
export function hatch(g, x, y, w, h, color) {
  g.save();
  g.clip();
  g.strokeStyle = color;
  g.globalAlpha *= 0.4;
  g.lineWidth = 1;
  g.beginPath();
  for (let k = -h; k < w; k += 5) {
    g.moveTo(x + k, y + h);
    g.lineTo(x + k + h, y);
  }
  g.stroke();
  g.restore();
}

/** Text that fits in w, with an ellipsis. @param {CanvasRenderingContext2D} g @param {string} text @param {number} w */
export function fit(g, text, w) {
  if (g.measureText(text).width <= w) return text;
  let t = text;
  while (t.length > 1 && g.measureText(t + '…').width > w) t = t.slice(0, -1);
  return t + '…';
}

/** A label on a paper-colored pill, centered at x. @param {CanvasRenderingContext2D} g @param {string} text @param {number} x @param {number} y @param {{size?: number, color?: string, bg?: string, max?: number}} [o] */
export function pill(g, text, x, y, o = {}) {
  const size = o.size ?? 12;
  g.font = `${size}px ui-sans-serif, system-ui, sans-serif`;
  const t = fit(g, text, o.max ?? 160);
  const w = g.measureText(t).width + 8;
  const h = size + 6;
  g.fillStyle = o.bg ?? alpha(cssVar('--paper'), 0.9);
  g.beginPath();
  g.roundRect(x - w / 2, y - h / 2, w, h, h / 2);
  g.fill();
  g.fillStyle = o.color ?? cssVar('--ink');
  g.textAlign = 'center';
  g.textBaseline = 'middle';
  g.fillText(t, x, y + 0.5);
}

/** Size a canvas to its box at device resolution. @param {HTMLCanvasElement} canvas */
export function fitCanvas(canvas) {
  const r = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  const w = Math.max(1, Math.round(r.width * dpr));
  const h = Math.max(1, Math.round(r.height * dpr));
  if (canvas.width !== w || canvas.height !== h) {
    canvas.width = w;
    canvas.height = h;
  }
  const g = /** @type {CanvasRenderingContext2D} */ (canvas.getContext('2d'));
  g.setTransform(dpr, 0, 0, dpr, 0, 0);
  g.clearRect(0, 0, r.width, r.height);
  return { g, W: r.width, H: r.height };
}

// Springs (K, D as in the explorations) for things appearing and leaving.
const K = 190;
const D = 21;

/** Scales that spring toward 1 when present and 0 when gone, keyed by identity. */
export class Springs {
  constructor() {
    /** @type {Map<string, {s: number, v: number, on: boolean}>} */
    this.m = new Map();
  }

  /** Mark everything absent; then call set() for what is present. */
  begin() {
    for (const a of this.m.values()) a.on = false;
  }

  /** @param {string} id @returns {number} current scale */
  set(id) {
    let a = this.m.get(id);
    if (!a) {
      a = { s: 0, v: 0, on: true };
      this.m.set(id, a);
    }
    a.on = true;
    return a.s;
  }

  /** @param {string} id */
  get(id) {
    return this.m.get(id)?.s ?? 0;
  }

  /** Ids that are leaving but still visible. */
  *leaving() {
    for (const [id, a] of this.m) if (!a.on && a.s > 0.02) yield id;
  }

  /** @param {number} dt seconds */
  step(dt) {
    const n = Math.max(1, Math.ceil(dt / (1 / 120)));
    const h = Math.min(dt, 0.1) / n;
    for (const [id, a] of this.m) {
      const target = a.on ? 1 : 0;
      for (let k = 0; k < n; k++) {
        a.v += (K * (target - a.s) - D * a.v) * h;
        a.s += a.v * h;
      }
      if (!a.on && a.s < 0.02 && Math.abs(a.v) < 0.05) this.m.delete(id);
    }
  }

  /** Jump everything to its target (for scrubbing). */
  settle() {
    for (const a of this.m.values()) {
      a.s = a.on ? 1 : 0;
      a.v = 0;
    }
  }
}
