// The board page: follows a board live, plays its timeline, and lets a
// person switch views, scrub, and inspect processes.

import { fitCanvas } from './draw.js';
import { Model, tone } from './model.js';
import { SequenceView } from './sequence.js';
import { WorldView } from './world.js';

const TICKS_PER_SECOND = 2;

/** @template {HTMLElement} T @param {string} id @returns {T} */
const $ = (id) => /** @type {T} */ (document.getElementById(id));

const board = decodeURIComponent(location.pathname.split('/').pop() || '');
const canvas = /** @type {HTMLCanvasElement} */ ($('view'));
const scrub = /** @type {HTMLInputElement} */ ($('scrub'));
const speedEl = /** @type {HTMLSelectElement} */ ($('speed'));
const followEl = /** @type {HTMLInputElement} */ ($('follow'));
const sliceEl = /** @type {HTMLInputElement} */ ($('slice'));
const tip = $('tip');

const views = { world: new WorldView(), voxel: new WorldView(), sequence: new SequenceView() };

const st = {
  /** @type {Model | null} */ model: null,
  /** @type {import('./model.js').Frame | null} */ frame: null,
  t: 0,
  playing: true,
  /** @type {'world' | 'voxel' | 'sequence'} */ view: 'world',
  hover: -1,
  selected: -1,
  shownTick: -1,
  scrubbing: false,
  clock: 0,
};

try {
  const v = localStorage.getItem('blockies.view');
  if (v === 'world' || v === 'voxel' || v === 'sequence') st.view = v;
} catch {}
const hashT = Number(new URLSearchParams(location.hash.slice(1)).get('t'));

document.title = `${board} · Blockies`;
$('name').textContent = board;
$('emptyHint').textContent = `blockies append ${board} 'proc api "API"'\n\nor, from an agent with the Blockies MCP server:\nboard_write { board: "${board}", script: "..." }`;

// ---- live connection ----

function connect() {
  const es = new EventSource(`/api/boards/${encodeURIComponent(board)}/stream`);
  const live = $('live');
  es.addEventListener('open', () => {
    live.textContent = 'live';
    live.className = 'live on';
  });
  es.addEventListener('frame', (e) => apply(JSON.parse(/** @type {MessageEvent} */ (e).data)));
  es.addEventListener('deleted', () => {
    live.textContent = 'deleted';
    live.className = 'live off';
  });
  es.addEventListener('error', () => {
    live.textContent = 'reconnecting';
    live.className = 'live off';
  });
}

/** @param {import('./model.js').Frame} frame */
function apply(frame) {
  const prev = st.model;
  const wasAtEnd = prev ? st.t >= maxT(prev) - 0.01 : false;
  st.frame = frame;
  st.model = new Model(frame.timeline);
  const m = st.model;
  if (!prev) {
    st.t = Number.isFinite(hashT) && hashT > 0 ? Math.min(hashT, maxT(m)) : 0;
    st.playing = !(hashT > 0);
  } else if (followEl.checked && wasAtEnd && m.end > prev.end) {
    st.t = Math.min(st.t, maxT(m));
    st.playing = true;
  }
  st.t = Math.min(st.t, maxT(m));
  if (st.selected >= m.procs.length) st.selected = -1;
  scrub.max = String(maxT(m));
  const rank = m.world.length;
  $('sliceBox').hidden = rank < 3;
  sliceEl.max = String((m.world[2] ?? 1) - 1);
  $('summary').textContent = `rev ${frame.rev} · ${m.procs.length} processes · ${m.msgs.length} messages · ${m.end + 1} ticks · ${m.world.join('×')} world`;
  $('empty').hidden = frame.script.trim() !== '';
  renderScript();
  renderErrors();
  st.shownTick = -1;
}

/** @param {Model} m */
const maxT = (m) => m.end + 0.99;

// ---- panels ----

function renderScript() {
  const ol = $('script');
  const m = /** @type {Model} */ (st.model);
  const errs = new Map(m.tl.errors.map((e) => [e.line, e]));
  ol.replaceChildren(
    ...(st.frame?.script ?? '').replace(/\n$/, '').split('\n').map((text, i) => {
      const li = document.createElement('li');
      const line = i + 1;
      li.dataset.line = String(line);
      li.textContent = text || ' ';
      const err = errs.get(line);
      if (err) {
        li.classList.add('bad');
        li.title = `${err.code}: ${err.detail}`;
      }
      if (/^\s*#/.test(text)) li.classList.add('comment');
      const at = m.lineTick.get(line);
      if (at !== undefined) {
        li.classList.add('jump');
        li.addEventListener('click', () => {
          st.t = at;
          st.playing = false;
          settle();
        });
      }
      return li;
    }),
  );
}

function renderErrors() {
  const m = /** @type {Model} */ (st.model);
  $('errBox').hidden = m.tl.errors.length === 0;
  $('errors').replaceChildren(
    ...m.tl.errors.map((e) => {
      const li = document.createElement('li');
      li.innerHTML = `<b>line ${e.line}</b> <code>${esc(e.code)}</code> ${esc(e.detail)}`;
      return li;
    }),
  );
}

function renderProcs() {
  const m = /** @type {Model} */ (st.model);
  const k = Math.min(m.end, Math.floor(st.t));
  const ul = $('procs');
  ul.replaceChildren(
    ...m.order.map((i) => {
      const p = m.procs[i];
      const li = document.createElement('li');
      li.style.paddingLeft = `${m.depth[i] * 14 + 6}px`;
      if (!m.alive(i, k)) li.classList.add('gone');
      if (i === st.selected) li.classList.add('sel');
      const state = m.stateAt(i, k);
      const note = m.noteAt(i, k);
      const q = m.queued(i, k);
      li.innerHTML =
        `<span class="dot" style="background:${m.colors[i]}"></span>` +
        `<span class="pl">${esc(p.label)}</span>` +
        (p.label !== p.id ? ` <span class="muted id">${esc(p.id)}</span>` : '') +
        (state ? ` <span class="chip ${tone(state)}">${esc(state)}</span>` : '') +
        (q ? ` <span class="chip queue" title="messages waiting">${q} waiting</span>` : '') +
        (note ? `<div class="note">${esc(note)}</div>` : '');
      li.addEventListener('mouseenter', () => (st.hover = i));
      li.addEventListener('mouseleave', () => (st.hover = -1));
      li.addEventListener('click', () => (st.selected = st.selected === i ? -1 : i));
      return li;
    }),
  );
  for (const li of $('script').children) {
    li.classList.toggle('now', m.linesAt(k).has(Number(/** @type {HTMLElement} */ (li).dataset.line)));
  }
  const now = $('script').querySelector('.now');
  if (now && st.playing) now.scrollIntoView({ block: 'nearest' });
}

/** @param {string} s */
function esc(s) {
  return s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c] ?? c);
}

// ---- playback ----

function settle() {
  views.world.springs.begin();
  views.voxel.springs.begin();
  st.scrubbing = true;
}

function setView(/** @type {'world' | 'voxel' | 'sequence'} */ v) {
  st.view = v;
  try {
    localStorage.setItem('blockies.view', v);
  } catch {}
  for (const b of document.querySelectorAll('[data-view]')) {
    b.classList.toggle('on', /** @type {HTMLElement} */ (b).dataset.view === v);
  }
}

let last = performance.now();
/** @param {number} now */
function frame(now) {
  const dt = Math.min(0.1, (now - last) / 1000);
  last = now;
  st.clock += dt;
  const m = st.model;
  const { g, W, H } = fitCanvas(canvas);
  if (m) {
    if (st.playing) {
      st.t += dt * TICKS_PER_SECOND * Number(speedEl.value);
      if (st.t >= maxT(m)) {
        st.t = maxT(m);
        st.playing = false;
      }
    }
    const view = views[st.view];
    if (st.scrubbing && view instanceof WorldView) {
      view.draw(g, W, H, m, st.t, { mode: st.view === 'voxel' ? 'voxel' : 'flat', hover: st.hover, selected: st.selected, clock: st.clock });
      view.springs.settle();
      st.scrubbing = false;
    }
    view.step(dt);
    if (view instanceof WorldView) {
      view.slice = Number(sliceEl.value);
      view.draw(g, W, H, m, st.t, { mode: st.view === 'voxel' ? 'voxel' : 'flat', hover: st.hover, selected: st.selected, clock: st.clock });
    } else {
      view.draw(g, W, H, m, st.t, { hover: st.hover, selected: st.selected });
    }
    if (document.activeElement !== scrub) scrub.value = String(st.t);
    const k = Math.min(m.end, Math.floor(st.t));
    $('tick').textContent = `${k} / ${m.end}`;
    $('play').textContent = st.playing ? '⏸' : '▶';
    if (k !== st.shownTick) {
      st.shownTick = k;
      renderProcs();
    }
  }
  requestAnimationFrame(frame);
}

// ---- input ----

for (const b of document.querySelectorAll('[data-view]')) {
  b.addEventListener('click', () => setView(/** @type {any} */ (/** @type {HTMLElement} */ (b).dataset.view)));
}
$('play').addEventListener('click', togglePlay);
$('back').addEventListener('click', () => stepBy(-1));
$('fwd').addEventListener('click', () => stepBy(1));
scrub.addEventListener('input', () => {
  st.t = Number(scrub.value);
  st.playing = false;
  settle();
});

function togglePlay() {
  const m = st.model;
  if (!m) return;
  if (!st.playing && st.t >= maxT(m)) st.t = 0;
  st.playing = !st.playing;
}

/** @param {number} d */
function stepBy(d) {
  const m = st.model;
  if (!m) return;
  st.playing = false;
  st.t = Math.max(0, Math.min(m.end, Math.floor(st.t + (d > 0 ? 1 : st.t % 1 > 0.01 ? 0 : -1))));
  settle();
}

addEventListener('keydown', (e) => {
  if (e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement || e.metaKey || e.ctrlKey) return;
  if (e.key === ' ') {
    e.preventDefault();
    togglePlay();
  } else if (e.key === 'ArrowLeft') stepBy(-1);
  else if (e.key === 'ArrowRight') stepBy(1);
  else if (e.key === '1') setView('world');
  else if (e.key === '2') setView('voxel');
  else if (e.key === '3') setView('sequence');
  else if (e.key === 'f') followEl.checked = !followEl.checked;
  else if (e.key === 'Escape') st.selected = -1;
});

canvas.addEventListener('mousemove', (e) => {
  const r = canvas.getBoundingClientRect();
  const i = views[st.view].pick(e.clientX - r.left, e.clientY - r.top);
  st.hover = i;
  const m = st.model;
  if (i < 0 || !m) {
    tip.hidden = true;
    canvas.style.cursor = '';
    return;
  }
  canvas.style.cursor = 'pointer';
  const k = Math.min(m.end, Math.floor(st.t));
  const p = m.procs[i];
  const state = m.stateAt(i, k);
  const note = m.noteAt(i, k);
  const q = m.queued(i, k);
  tip.innerHTML =
    `<b>${esc(p.label)}</b> <span class="muted">${esc(p.id)}${p.kind ? ' · ' + esc(p.kind) : ''}</span>` +
    (state ? `<div>state <span class="chip ${tone(state)}">${esc(state)}</span></div>` : '') +
    (note ? `<div class="note">${esc(note)}</div>` : '') +
    (q ? `<div>${q} message${q > 1 ? 's' : ''} waiting</div>` : '') +
    `<div class="muted">tick ${p.start}${p.end !== -1 ? ' to ' + p.end : ''} · line ${p.line}</div>`;
  tip.hidden = false;
  tip.style.left = `${Math.min(innerWidth - 260, e.clientX + 14)}px`;
  tip.style.top = `${e.clientY + 14}px`;
});
canvas.addEventListener('mouseleave', () => {
  st.hover = -1;
  tip.hidden = true;
});
canvas.addEventListener('click', () => {
  st.selected = st.hover >= 0 && st.selected !== st.hover ? st.hover : -1;
  st.shownTick = -1;
});

// For tests and debugging: drive the page without a visible window.
Object.assign(window, { __blockies: { st, frame } });

setView(st.view);
connect();
requestAnimationFrame(frame);
