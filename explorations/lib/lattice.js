// A lattice of any rank. Nothing here knows about x, y or z: a coordinate is
// an integer array, and a direction is one step along one axis. Renderers
// decide how axes become pixels, cubes or characters.

export class Lattice {
  constructor(shape) {
    this.shape = shape.slice();
    this.rank = shape.length;
    this.strides = [];
    let s = 1;
    for (let a = 0; a < this.rank; a++) {
      this.strides.push(s);
      s *= shape[a];
    }
    this.size = s;
    // Face directions: +1 and -1 along every axis (2 * rank of them).
    this.dirs = [];
    for (let a = 0; a < this.rank; a++) {
      for (const sign of [1, -1]) {
        const d = new Array(this.rank).fill(0);
        d[a] = sign;
        this.dirs.push(d);
      }
    }
  }

  inside(c) {
    for (let a = 0; a < this.rank; a++) if (c[a] < 0 || c[a] >= this.shape[a]) return false;
    return true;
  }

  index(c) {
    let i = 0;
    for (let a = 0; a < this.rank; a++) i += c[a] * this.strides[a];
    return i;
  }

  coord(i) {
    const c = [];
    for (let a = 0; a < this.rank; a++) {
      c.push(i % this.shape[a]);
      i = Math.floor(i / this.shape[a]);
    }
    return c;
  }

  neighbors(c) {
    return this.dirs.map((d) => add(c, d)).filter((n) => this.inside(n));
  }
}

export const add = (a, b) => a.map((v, i) => v + (b[i] ?? 0));
export const sub = (a, b) => a.map((v, i) => v - (b[i] ?? 0));
export const scale = (a, k) => a.map((v) => v * k);
export const eq = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);
export const key = (c) => c.join(',');
export const unkey = (k) => k.split(',').map(Number);
export const manhattan = (a, b) => a.reduce((s, v, i) => s + Math.abs(v - b[i]), 0);
export const pad = (c, rank) => {
  const out = c.slice(0, rank);
  while (out.length < rank) out.push(0);
  return out;
};

// Axis-by-axis path from a to b (both ends included). Works at any rank.
export function path(a, b) {
  const out = [a.slice()];
  const c = a.slice();
  for (let ax = 0; ax < c.length; ax++) {
    while (c[ax] !== b[ax]) {
      c[ax] += Math.sign(b[ax] - c[ax]);
      out.push(c.slice());
    }
  }
  return out;
}

// Deterministic hash, the only source of randomness (same idea as the Rust sim).
export function mix(...ns) {
  let h = 0x9e3779b9 >>> 0;
  for (const n of ns) {
    h ^= (n | 0) + 0x7f4a7c15 + ((h << 6) >>> 0) + (h >>> 2);
    h = Math.imul(h ^ (h >>> 16), 0x85ebca6b) >>> 0;
    h = Math.imul(h ^ (h >>> 13), 0xc2b2ae35) >>> 0;
    h = (h ^ (h >>> 16)) >>> 0;
  }
  return h;
}

// Calls a "pure" function twice on cloned input and reports if the results
// differ (a cheap purity check: catches Math.random, Date.now, hidden state).
export function pure(fn, args, onImpure) {
  const a = fn(...structuredClone(args));
  const b = fn(...structuredClone(args));
  if (JSON.stringify(a) !== JSON.stringify(b)) onImpure?.();
  return a;
}
