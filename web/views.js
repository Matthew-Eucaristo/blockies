// The view registry: each view draws the same timeline its own way. Add a
// view with addView(); it gets a button, a number key and a place in the
// shortcuts dialog without touching the rest of the page.

import { SequenceView } from './sequence.js';
import { WorldView } from './world.js';

/**
 * What every view implements. All coordinates are CSS pixels inside the
 * area the page gives the view (the canvas minus the floating panels).
 * @typedef {object} ViewImpl
 * @property {(g: CanvasRenderingContext2D, W: number, H: number, m: import('./model.js').Model, t: number, o: DrawOpts) => void} draw
 * @property {(dt: number) => void} step  advance animations by dt seconds
 * @property {(x: number, y: number) => number} pick  index of the process at a point, or -1
 * @property {() => void} settle  jump animations to their end (after a scrub)
 * @property {() => boolean} animating  whether another frame would look different
 * @property {(i: number) => {x: number, y: number, r: number} | null} [where]  where process i is drawn
 * @property {(x: number, y: number) => number[] | null} [cellAt]  the world cell under a point (flat world only)
 *
 * @typedef {{hover: number, selected: number, clock: number, motion: boolean, playing: boolean, slice: number, frame?: {lo: number[], hi: number[]} | null}} DrawOpts
 *
 * @typedef {object} ViewEntry
 * @property {string} id
 * @property {string} label
 * @property {string} title
 * @property {ViewImpl} impl
 */

/** @type {ViewEntry[]} */
export const views = [];

/** @param {ViewEntry} v */
export function addView(v) {
  views.push(v);
}

/** The world drawn flat or as voxels. @param {'flat' | 'voxel'} mode @returns {ViewImpl} */
function world(mode) {
  const w = new WorldView();
  return {
    draw(g, W, H, m, t, o) {
      w.slice = o.slice;
      w.draw(g, W, H, m, t, { ...o, mode });
    },
    step: (dt) => w.step(dt),
    pick: (x, y) => w.pick(x, y),
    settle: () => w.settle(),
    animating: () => w.animating(),
    where: (i) => w.where(i),
    cellAt: mode === 'flat' ? (x, y) => w.cellAt(x, y) : undefined,
  };
}

const sequence = new SequenceView();
addView({ id: 'world', label: 'World', title: 'The world: processes in their places, messages flying', impl: world('flat') });
addView({ id: 'voxel', label: 'Voxel', title: 'The same world in 3D', impl: world('voxel') });
addView({
  id: 'sequence',
  label: 'Sequence',
  title: 'Time as an axis: a sequence diagram and timeline',
  impl: {
    draw: (g, W, H, m, t, o) => sequence.draw(g, W, H, m, t, o),
    step: () => {},
    pick: (x, y) => sequence.pick(x, y),
    settle: () => sequence.settle(),
    animating: () => sequence.animating(),
  },
});
