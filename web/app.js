// The board page: follows a board live and plays its timeline in any view.
// The page is a full-window canvas with small floating panels; everything a
// person can do is an action (actions.js), every way of drawing the board is
// a view (views.js), and the drawer's tabs are panels (panels.js). Talking to
// the agent lives in talk.js and is off on watch links (?watch).

import { addAction, bindKeys, renderMenu, renderShortcuts, runAction } from './actions.js';
import { fitCanvas } from './draw.js';
import { Model, formatNum, tone } from './model.js';
import { addPanel, mountPanels, refreshPanels, show, toggleDrawer } from './panels.js';
import { setupTalk, talk } from './talk.js';
import { $, el, emit, esc, motion, on, saved, toast } from './ui.js';
import { views } from './views.js';

const TICKS_PER_SECOND = 2;

const board = decodeURIComponent(location.pathname.split('/').pop() || '');
const params = new URLSearchParams(location.search);
// ?view=ID: a phone or a second screen locked onto one part of the board
// (for example the voting buttons). It never talks to the agent.
const viewId = params.get('view') ?? '';
const watch = params.has('watch') || viewId !== '';
const canvas = /** @type {HTMLCanvasElement} */ ($('view'));
const scrub = /** @type {HTMLInputElement} */ ($('scrub'));
const speedEl = /** @type {HTMLSelectElement} */ ($('speed'));
const followEl = /** @type {HTMLInputElement} */ ($('follow'));
const sliceEl = /** @type {HTMLInputElement} */ ($('slice'));
const tip = $('tip');

const st = {
  /** @type {Model | null} */ model: null,
  /** @type {import('./model.js').Frame | null} */ frame: null,
  t: 0,
  playing: true,
  view: views[0].id,
  hover: -1,
  selected: -1,
  shownTick: -1,
  scrubbing: false,
  clock: 0,
  dirty: true,
  wholeBoard: false, // ignore the board's focus and show everything
  /** @type {'connecting' | 'live' | 'reconnecting' | 'closed' | 'deleted'} */ conn: 'connecting',
  safe: { top: 0, right: 0, bottom: 0, left: 0 },
};

// Phones locked onto a part always use the flat world: it frames that part
// and is the easiest to tap.
// A link can pick the time and the view: #t=12&view=sequence.
const hash = new URLSearchParams(location.hash.slice(1));
const saved0 = hash.get('view') ?? saved.get('view', st.view);
if (!viewId && views.some((v) => v.id === saved0)) st.view = saved0;
const hashT = Number(hash.get('t'));

document.title = `${board} · Blockies`;
$('name').textContent = board;
document.body.classList.toggle('watch', watch);
document.body.classList.toggle('viewlock', viewId !== '');

// ---- theme ----

const systemDark = matchMedia('(prefers-color-scheme: dark)');
/** @param {string} t  system, light or dark */
function setTheme(t) {
  saved.set('theme', t);
  document.documentElement.dataset.theme = t === 'system' ? (systemDark.matches ? 'dark' : 'light') : t;
  st.dirty = true;
}
// Light by default: most people expect a white page.
setTheme(saved.get('theme', 'light'));
systemDark.addEventListener('change', () => setTheme(saved.get('theme', 'light')));

// ---- live connection ----

function connect() {
  const es = new EventSource(`/api/boards/${encodeURIComponent(board)}/stream`);
  es.addEventListener('open', () => setConn('live'));
  es.addEventListener('frame', (e) => apply(JSON.parse(/** @type {MessageEvent} */ (e).data)));
  es.addEventListener('deleted', () => {
    setConn('deleted');
    es.close();
  });
  es.addEventListener('error', () => {
    if (st.conn === 'deleted') return;
    // The browser retries by itself unless the server refused the stream.
    setConn(es.readyState === EventSource.CLOSED ? 'closed' : 'reconnecting');
  });
}

/** @param {typeof st.conn} c */
function setConn(c) {
  st.conn = c;
  const live = $('live');
  live.className = `conn ${c}`;
  live.title = { connecting: 'Connecting…', live: 'Live: changes show up as they happen', reconnecting: 'Lost the connection; reconnecting…', closed: 'Disconnected', deleted: 'This board was deleted' }[c];
  $('connNote').hidden = c === 'live' || c === 'connecting';
  $('connNote').textContent = { deleted: 'This board was deleted.', closed: 'Disconnected. Reload the page to try again.' }[/** @type {string} */ (c)] ?? 'Reconnecting…';
}

/** @param {import('./model.js').Frame} frame */
function apply(frame) {
  const began = performance.now();
  try {
    applyFrame(frame);
  } finally {
    applyTimes.push(performance.now() - began);
    if (applyTimes.length > 200) applyTimes.shift();
  }
}
/** How long each frame took to apply (ms), for checking the page stays light. @type {number[]} */
const applyTimes = [];

/** After a change made here (a move), show the end of the run. */
let jumpToEnd = false;

/** @param {import('./model.js').Frame} frame */
function applyFrame(frame) {
  const prev = st.model;
  const wasAtEnd = prev ? st.t >= maxT(prev) - 0.01 : false;
  st.frame = frame;
  st.model = new Model(frame.timeline);
  const m = st.model;
  if (!prev && viewId) {
    // A phone joining a live board starts at now, not at the beginning.
    st.t = maxT(m);
    st.playing = false;
  } else if (!prev) {
    st.t = Number.isFinite(hashT) && hashT > 0 ? Math.min(hashT, maxT(m)) : 0;
    st.playing = !(hashT > 0);
  } else if (followEl.checked && wasAtEnd && m.end > prev.end) {
    st.playing = true;
  }
  if (jumpToEnd) {
    jumpToEnd = false;
    st.t = maxT(m);
    st.playing = false;
    settle();
  }
  st.t = Math.min(st.t, maxT(m));
  if (st.selected >= m.procs.length) st.selected = -1;
  scrub.max = String(maxT(m));
  $('sliceBox').hidden = m.world.length < 3;
  sliceEl.max = String((m.world[2] ?? 1) - 1);
  $('summary').textContent = `rev ${frame.rev} · ${m.procs.length} processes · ${m.msgs.length} messages · ${m.end + 1} ticks`;
  if (viewId) {
    const i = m.find(viewId);
    const taps = i >= 0 && m.subtree(i).some((j) => m.procs[j].tap);
    $('viewHint').hidden = !taps;
  }
  // A board with no time (a tool, a form) needs no playback bar.
  document.body.classList.toggle('static', m.end === 0 && m.msgs.length === 0);
  const empty = frame.script.trim() === '';
  $('welcome').hidden = !empty;
  document.body.classList.toggle('empty', empty);
  st.shownTick = -1;
  st.dirty = true;
  emit('frame', { frame, model: m });
  refreshPanels();
}

/** @param {Model} m */
const maxT = (m) => m.end + 0.99;

// ---- panels: processes, script, errors ----

const procsList = el('ul', { class: 'procs' });
const scriptList = el('ol', { class: 'script' });
const errorList = el('ul', { class: 'errors' });

addPanel({ id: 'procs', label: 'Processes', order: 1, mount: (box) => box.append(procsList) });
// The script is the board: read it here, jump to a line, or edit it.
const scriptEdit = /** @type {HTMLTextAreaElement} */ (el('textarea', { class: 'script-edit', spellcheck: 'false', 'aria-label': 'Board script', hidden: true }));
const editBtn = el('button', { type: 'button', class: 'quiet', onclick: () => editScript(!editing) }, 'Edit');
const applyBtn = el('button', { type: 'button', class: 'primary', hidden: true, onclick: () => applyScript() }, 'Apply');
let editing = false;
let editRev = 0; // the board's rev when editing started
addPanel({
  id: 'script',
  label: 'Script',
  order: 2,
  mount: (box) =>
    box.append(
      el('div', { class: 'panel-head' }, el('p', { class: 'panel-note', text: 'The board is this script. Click a line to jump to it.' }), watch ? '' : editBtn, applyBtn),
      scriptList,
      scriptEdit,
    ),
});

/** @param {boolean} on */
function editScript(on) {
  editing = on;
  scriptEdit.hidden = !on;
  scriptList.hidden = on;
  applyBtn.hidden = !on;
  editBtn.textContent = on ? 'Cancel' : 'Edit';
  if (on) {
    scriptEdit.value = st.frame?.script ?? '';
    editRev = st.frame?.rev ?? 0;
    scriptEdit.focus();
  }
}

async function applyScript() {
  if ((st.frame?.rev ?? 0) !== editRev && !confirm('The board changed while you were editing. Replace it with your version?')) return;
  const res = await fetch(`/api/boards/${encodeURIComponent(board)}`, { method: 'PUT', body: scriptEdit.value });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) return toast(body.detail ?? 'Could not save the script.');
  editScript(false);
  toast(body.errors?.length ? `Saved, with ${body.errors.length} line${body.errors.length > 1 ? 's' : ''} to fix (see Errors).` : 'Saved.');
}
addPanel({
  id: 'errors',
  label: 'Errors',
  order: 3,
  when: () => (st.model?.tl.errors.length ?? 0) > 0,
  badge: () => String(st.model?.tl.errors.length || ''),
  mount: (box) => box.append(errorList),
});

on('frame', ({ model }) => {
  const m = /** @type {Model} */ (model);
  const errs = new Map(m.tl.errors.map((e) => [e.line, e]));
  scriptList.replaceChildren(
    ...(st.frame?.script ?? '')
      .replace(/\n$/, '')
      .split('\n')
      .map((text, i) => {
        const line = i + 1;
        const at = m.lineTick.get(line);
        const err = errs.get(line);
        const li = el('li', { 'data-line': line, class: [err && 'bad', /^\s*#/.test(text) && 'comment', at !== undefined && 'jump'].filter(Boolean).join(' '), title: err ? `${err.code}: ${err.detail}` : undefined });
        li.textContent = text || ' ';
        if (at !== undefined) {
          li.tabIndex = 0;
          const jump = () => {
            st.t = at;
            st.playing = false;
            settle();
          };
          li.addEventListener('click', jump);
          li.addEventListener('keydown', (e) => e.key === 'Enter' && jump());
        }
        return li;
      }),
  );
  errorList.replaceChildren(...m.tl.errors.map((e) => el('li', {}, el('b', { text: `line ${e.line} ` }), el('code', { text: e.code }), ` ${e.detail}`)));
});

on('tick', (k) => {
  const m = /** @type {Model} */ (st.model);
  procsList.replaceChildren(
    ...m.order.map((i) => {
      const p = m.procs[i];
      const li = el('li', { style: `padding-left:${m.depth[i] * 14 + 6}px`, class: [!m.alive(i, k) && 'gone', i === st.selected && 'sel'].filter(Boolean).join(' '), tabindex: 0 });
      li.innerHTML =
        `<span class="dot" style="background:${m.colors[i]}"></span><span class="pl">${esc(p.label)}</span>` +
        (p.label !== p.id ? ` <span class="muted id">${esc(p.id)}</span>` : '') +
        procInfo(m, i, k);
      // Keyboard access to buttons and controls.
      if (!viewId && (tappable(i) || controllable(i))) {
        const act = tappable(i) ? 'Tap' : 'Set';
        li.append(
          ' ',
          el('button', {
            type: 'button',
            class: 'quiet small',
            'aria-label': `${act} ${p.label}`,
            onclick: (/** @type {Event} */ e) => {
              e.stopPropagation();
              if (tappable(i)) tap(i);
              else openControl(i, views.find((x) => x.id === st.view) ?? views[0]);
            },
          }, act),
        );
      }
      li.addEventListener('mouseenter', () => hover(i));
      li.addEventListener('mouseleave', () => hover(-1));
      const pick = () => select(st.selected === i ? -1 : i);
      li.addEventListener('click', pick);
      li.addEventListener('keydown', (e) => e.key === 'Enter' && pick());
      return li;
    }),
  );
  for (const li of scriptList.children) {
    li.classList.toggle('now', m.linesAt(k).has(Number(/** @type {HTMLElement} */ (li).dataset.line)));
  }
  const now = scriptList.querySelector('.now');
  if (now && st.playing && !$('drawer').hidden) now.scrollIntoView({ block: 'nearest' });
});

/** A process's state, waiting messages and note at tick k, as HTML. @param {Model} m @param {number} i @param {number} k */
function procInfo(m, i, k) {
  const state = m.stateAt(i, k);
  const note = m.noteAt(i, k);
  const q = m.queued(i, k);
  return (
    (state ? ` <span class="chip ${tone(state)}">${esc(state)}</span>` : '') +
    (q ? ` <span class="chip queue" title="messages waiting">${q} waiting</span>` : '') +
    (note ? `<div class="note">${esc(note)}</div>` : '')
  );
}

/** @param {number} i */
function hover(i) {
  if (st.hover !== i) {
    st.hover = i;
    st.dirty = true;
  }
}
/** @param {number} i */
function select(i) {
  st.selected = i;
  st.shownTick = -1;
  st.dirty = true;
}

// ---- playback and drawing ----

function settle() {
  st.scrubbing = true;
  st.dirty = true;
}

/** @param {string} id */
function setView(id) {
  st.view = id;
  if (!viewId) saved.set('view', id);
  for (const b of $('views').querySelectorAll('button')) {
    b.setAttribute('aria-pressed', String(/** @type {HTMLElement} */ (b).dataset.view === id));
  }
  st.dirty = true;
}

/** The part of the canvas no floating panel covers. */
function measureSafe() {
  const r = canvas.getBoundingClientRect();
  /** A panel's box, or null when it is not shown. @param {string} id */
  const box = (id) => {
    const b = $(id).getBoundingClientRect();
    return b.width && b.height ? b : null;
  };
  const top = Math.max(r.top + 8, ...['top-left', 'views', 'top-right'].map((id) => box(id)?.bottom ?? 0)) - r.top + 8;
  const stack = box('bottom');
  const drawer = box('drawer');
  const right = !drawer || innerWidth < 760 ? 0 : r.right - drawer.left + 8;
  st.safe = { top, right, bottom: stack ? r.bottom - stack.top + 8 : 16, left: 0 };
  st.dirty = true;
}
new ResizeObserver(measureSafe).observe(document.body);
new ResizeObserver(measureSafe).observe($('bottom'));
addEventListener('resize', measureSafe);

let last = performance.now();
let failedOnce = false;
/** @param {number} now */
function frame(now) {
  try {
    tick(now);
  } catch (err) {
    // One bad frame must not stop the page: report it once and keep going.
    if (!failedOnce) console.error('blockies: drawing failed', err);
    failedOnce = true;
    st.dirty = true;
  }
  requestAnimationFrame(frame);
}

/** @param {number} now */
function tick(now) {
  // Frame times can arrive out of order (or be synthetic in tests): never
  // step backwards, and never by more than a tenth of a second.
  const dt = Math.max(0, Math.min(0.1, (now - last) / 1000));
  last = Math.max(last, now);
  const m = st.model;
  const v = views.find((x) => x.id === st.view) ?? views[0];
  if (m) {
    if (st.playing) {
      st.t += dt * TICKS_PER_SECOND * Number(speedEl.value);
      if (st.t >= maxT(m)) {
        st.t = maxT(m);
        st.playing = false;
        st.dirty = true;
      }
    }
    const moving = st.playing || st.dirty || st.scrubbing || v.impl.animating();
    if (moving) {
      st.clock += dt;
      draw(m, v, dt);
    }
    if (document.activeElement !== scrub) scrub.value = String(st.t);
    const k = Math.min(m.end, Math.floor(st.t));
    $('tick').textContent = `${k} / ${m.end}`;
    const play = $('play');
    play.textContent = st.playing ? '⏸' : '▶';
    play.setAttribute('aria-label', st.playing ? 'Pause' : 'Play');
    if (k !== st.shownTick) {
      st.shownTick = k;
      emit('tick', k);
    }
  } else if (st.dirty) {
    fitCanvas(canvas);
    st.dirty = false;
  }
}

/** @param {Model} m @param {import('./views.js').ViewEntry} v @param {number} dt */
function draw(m, v, dt) {
  const { g, W, H } = fitCanvas(canvas);
  if (document.body.classList.contains('empty')) {
    st.dirty = false; // the welcome screen stands in for an empty world
    return;
  }
  const s = st.safe;
  const w = Math.max(40, W - s.left - s.right);
  const h = Math.max(40, H - s.top - s.bottom);
  const o = { hover: st.hover, selected: st.selected, clock: st.clock, motion: !motion.matches, playing: st.playing, slice: Number(sliceEl.value), frame: frameAt(m) };
  g.save();
  g.translate(s.left, s.top);
  if (st.scrubbing) {
    v.impl.draw(g, w, h, m, st.t, o);
    v.impl.settle();
    g.clearRect(-s.left, -s.top, W, H);
    st.scrubbing = false;
  }
  v.impl.step(motion.matches ? 1 : dt);
  v.impl.draw(g, w, h, m, st.t, o);
  g.restore();
  st.dirty = false;
}

/** @type {Map<number, {lo: number[], hi: number[]}>} */
let frames = new Map();
on('frame', () => (frames = new Map()));

/**
 * The part of the world to show: the ?view part, else the part the board
 * focuses on at this tick (focus), else everything. @param {Model} m
 */
function frameAt(m) {
  const k = Math.min(m.end, Math.floor(st.t));
  const i = viewId ? m.find(viewId) : st.wholeBoard ? -1 : m.focusAt(k);
  if (i < 0) return null;
  let f = frames.get(i);
  if (!f) frames.set(i, (f = m.boundsOf(m.subtree(i))));
  return f;
}

/** Tap a block people can tap: one `tap` line on the board. @param {number} i */
async function tap(i) {
  const m = /** @type {Model} */ (st.model);
  try {
    const res = await fetch(`/api/boards/${encodeURIComponent(board)}/tap`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: m.procs[i].id }),
    });
    if (!res.ok) {
      const e = await res.json().catch(() => ({}));
      if (e.code !== 'slow_down') toast(e.detail ?? 'That tap did not go through.');
    } else if (navigator.vibrate) navigator.vibrate(15);
  } catch {
    toast('Could not reach the board.');
  }
}

function togglePlay() {
  const m = st.model;
  if (!m) return;
  if (!st.playing && st.t >= maxT(m)) st.t = 0;
  st.playing = !st.playing;
  st.dirty = true;
}

/** @param {number} d */
function stepBy(d) {
  const m = st.model;
  if (!m) return;
  st.playing = false;
  st.t = Math.max(0, Math.min(m.end, Math.floor(st.t + (d > 0 ? 1 : st.t % 1 > 0.01 ? 0 : -1))));
  settle();
}

// ---- menu, help, share ----

const menu = $('menu');
const menuBtn = $('menuBtn');
function closeMenu() {
  if (menu.hidden) return;
  menu.hidden = true;
  menuBtn.setAttribute('aria-expanded', 'false');
}
function openMenu() {
  renderMenu(menu, closeMenu);
  menu.hidden = false;
  menuBtn.setAttribute('aria-expanded', 'true');
  /** @type {HTMLElement | null} */ (menu.querySelector('[role=menuitem]'))?.focus();
}
menuBtn.addEventListener('click', () => (menu.hidden ? openMenu() : closeMenu()));
menu.addEventListener('keydown', (e) => {
  const items = /** @type {HTMLElement[]} */ ([...menu.querySelectorAll('[role=menuitem]')]);
  const i = items.indexOf(/** @type {HTMLElement} */ (document.activeElement));
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault();
    items[(i + (e.key === 'ArrowDown' ? 1 : items.length - 1)) % items.length]?.focus();
  } else if (e.key === 'Escape') {
    e.stopPropagation();
    closeMenu();
    menuBtn.focus();
  } else if (e.key === 'Tab') closeMenu();
});
addEventListener('pointerdown', (e) => {
  if (!menu.hidden && !menu.contains(/** @type {Node} */ (e.target)) && e.target !== menuBtn) closeMenu();
});

const help = /** @type {HTMLDialogElement} */ ($('help'));
function openHelp() {
  closeMenu();
  renderShortcuts($('shortcuts'));
  help.showModal();
}
$('helpBtn').addEventListener('click', openHelp);
$('closeHelp').addEventListener('click', () => help.close());
help.addEventListener('click', (e) => e.target === help && help.close());

async function share() {
  const url = `${location.origin}/b/${encodeURIComponent(board)}?watch`;
  try {
    await navigator.clipboard.writeText(url);
    toast('Watch link copied. Anyone who can reach this server can open it.');
  } catch {
    toast(url);
  }
}
$('shareBtn').addEventListener('click', share);
$('drawerBtn').addEventListener('click', () => toggleDrawer());

async function download() {
  const src = st.frame?.script ?? '';
  const a = el('a', { href: URL.createObjectURL(new Blob([src], { type: 'text/plain' })), download: `${board}.blk` });
  a.click();
  setTimeout(() => URL.revokeObjectURL(/** @type {HTMLAnchorElement} */ (a).href), 1000);
}

// ---- actions ----

views.forEach((v, i) => {
  $('views').append(el('button', { type: 'button', 'data-view': v.id, title: `${v.title} (${i + 1})`, 'aria-pressed': 'false', onclick: () => setView(v.id) }, v.label));
  addAction({ id: `view-${v.id}`, label: `${v.label} view`, group: 'view', keys: [String(i + 1)], run: () => setView(v.id) });
});
addAction({ id: 'drawer', label: 'Show or hide the side panel', group: 'view', keys: ['d'], run: () => toggleDrawer() });
addAction({
  id: 'theme',
  get label() {
    return `Theme: ${saved.get('theme', 'light')}`;
  },
  group: 'view',
  keys: ['t'],
  menu: true,
  run: () => {
    const order = ['light', 'dark', 'system'];
    const next = order[(order.indexOf(saved.get('theme', 'light')) + 1) % 3];
    setTheme(next);
    toast(`Theme: ${next}`);
  },
});
addAction({
  id: 'play',
  label: 'Play or pause',
  group: 'play',
  // Space plays only where it does not mean "talk".
  get keys() {
    return talk.on && talk.ready ? ['p'] : ['p', 'Space'];
  },
  run: togglePlay,
});
addAction({ id: 'back', label: 'Previous tick', group: 'play', keys: ['ArrowLeft'], run: () => stepBy(-1) });
addAction({ id: 'fwd', label: 'Next tick', group: 'play', keys: ['ArrowRight'], run: () => stepBy(1) });
addAction({
  id: 'follow',
  label: 'Follow new lines as they arrive',
  group: 'play',
  keys: ['f'],
  run: () => {
    followEl.checked = !followEl.checked;
    toast(followEl.checked ? 'Following new lines' : 'Not following new lines');
  },
});
addAction({ id: 'share', label: 'Copy a watch-only link', group: 'board', menu: true, run: share });
addAction({
  id: 'whole',
  get label() {
    return st.wholeBoard ? 'Follow the board’s focus' : 'Show the whole board';
  },
  group: 'view',
  keys: ['w'],
  menu: true,
  when: () => !viewId && (st.model?.focus.length ?? 0) > 0,
  run: () => {
    st.wholeBoard = !st.wholeBoard;
    st.dirty = true;
  },
});
addAction({
  id: 'focusHere',
  label: 'Show the selected part on every screen',
  group: 'board',
  menu: true,
  when: () => !watch && st.selected >= 0,
  run: async () => {
    const id = /** @type {Model} */ (st.model).procs[st.selected].id;
    const res = await fetch(`/api/boards/${encodeURIComponent(board)}/lines`, { method: 'POST', body: `focus ${id}` });
    const body = res.ok ? await res.json() : null;
    toast(body && !body.rejected?.length ? `Every screen now looks at ${id}` : 'Could not move the focus.');
  },
});
addAction({
  id: 'linkHere',
  label: 'Copy a phone link to the selected part',
  group: 'board',
  menu: true,
  when: () => st.selected >= 0,
  run: async () => {
    const id = /** @type {Model} */ (st.model).procs[st.selected].id;
    const url = `${location.origin}/b/${encodeURIComponent(board)}?view=${encodeURIComponent(id)}`;
    try {
      await navigator.clipboard.writeText(url);
      toast('Link copied: people who open it see only that part, and can tap its buttons.');
    } catch {
      toast(url);
    }
  },
});
addAction({ id: 'download', label: 'Download the script (.blk)', group: 'board', menu: true, run: download });
addAction({ id: 'guide', label: 'The board language', group: 'board', menu: true, run: () => open('/api/guide', '_blank', 'noopener') });
addAction({ id: 'help', label: 'Keyboard shortcuts', group: 'board', keys: ['?'], menu: true, run: openHelp });
addAction({ id: 'home', label: 'All boards', group: 'board', menu: true, run: () => (location.href = '/') });
addAction({ id: 'deselect', label: 'Clear the selection', group: 'view', keys: ['Escape'], when: () => st.selected >= 0, run: () => select(-1) });

$('play').addEventListener('click', togglePlay);
$('back').addEventListener('click', () => stepBy(-1));
$('fwd').addEventListener('click', () => stepBy(1));
scrub.addEventListener('input', () => {
  st.t = Number(scrub.value);
  st.playing = false;
  settle();
});
sliceEl.addEventListener('input', () => (st.dirty = true));
speedEl.addEventListener('change', () => (st.dirty = true));

// ---- canvas pointer ----

canvas.addEventListener('pointermove', (e) => {
  const r = canvas.getBoundingClientRect();
  const v = views.find((x) => x.id === st.view) ?? views[0];
  const i = v.impl.pick(e.clientX - r.left - st.safe.left, e.clientY - r.top - st.safe.top);
  hover(i);
  const m = st.model;
  if (i < 0 || !m || e.pointerType === 'touch') {
    tip.hidden = true;
    canvas.style.cursor = '';
    return;
  }
  canvas.style.cursor = 'pointer';
  const k = Math.min(m.end, Math.floor(st.t));
  const p = m.procs[i];
  tip.innerHTML =
    `<b>${esc(p.label)}</b> <span class="muted">${esc(p.id)}${p.kind ? ' · ' + esc(p.kind) : ''}</span>` +
    procInfo(m, i, k) +
    `<div class="muted">tick ${p.start}${p.end !== -1 ? ' to ' + p.end : ''} · line ${p.line}</div>`;
  tip.hidden = false;
  tip.style.left = `${Math.min(innerWidth - 260, e.clientX + 14)}px`;
  tip.style.top = `${e.clientY + 14}px`;
});
canvas.addEventListener('pointerleave', () => {
  hover(-1);
  tip.hidden = true;
});
canvas.addEventListener('click', (e) => {
  if (dragged) {
    dragged = false;
    return;
  }
  const r = canvas.getBoundingClientRect();
  const v = views.find((x) => x.id === st.view) ?? views[0];
  const i = v.impl.pick(e.clientX - r.left - st.safe.left, e.clientY - r.top - st.safe.top);
  // Sliders, inputs and toggles open their control, everywhere.
  if (canControl(i)) {
    openControl(i, v);
    return;
  }
  // On watch links and phones, blocks with a click rule are buttons: a tap
  // goes to the board for everyone. While editing, a click selects and a
  // double click taps.
  if (watch && canTap(i)) {
    tap(i);
    return;
  }
  if (viewId) return;
  select(i >= 0 && st.selected !== i ? i : -1);
  if (st.selected >= 0 && innerWidth >= 760) {
    show('procs');
    toggleDrawer(true);
  }
});

canvas.addEventListener('dblclick', (e) => {
  if (watch) return;
  const r = canvas.getBoundingClientRect();
  const v = views.find((x) => x.id === st.view) ?? views[0];
  const i = v.impl.pick(e.clientX - r.left - st.safe.left, e.clientY - r.top - st.safe.top);
  if (canTap(i)) tap(i);
});

/** Whether process i is a button people can tap now. @param {number} i */
function tappable(i) {
  const m = st.model;
  return !!m && i >= 0 && !!m.procs[i].tap && m.alive(i, m.end);
}
/** Whether a click on the canvas taps process i (not from the sequence view). @param {number} i */
const canTap = (i) => st.view !== 'sequence' && tappable(i);

// ---- controls: slider, input, toggle ----

const control = $('control');
/** Whether process i is a control people can set now. @param {number} i */
function controllable(i) {
  const m = st.model;
  const skin = i >= 0 && m ? m.procs[i].skin : '';
  return !!m && (skin === 'slider' || skin === 'input' || skin === 'toggle') && m.alive(i, m.end);
}
/** Whether a click on the canvas opens process i's control. @param {number} i */
const canControl = (i) => st.view !== 'sequence' && controllable(i);

/** @type {Map<string, {last: number, timer?: number, value: number | string | boolean}>} */
const sends = new Map();
/** Send a control's value, at most every 150 ms per control; the last value always goes. @param {string} id @param {number | string | boolean} value @param {boolean} [now] */
function sendValue(id, value, now) {
  const s = sends.get(id) ?? { last: 0, value };
  sends.set(id, s);
  s.value = value;
  const go = async () => {
    clearTimeout(s.timer);
    s.timer = undefined;
    s.last = performance.now();
    try {
      const res = await fetch(`/api/boards/${encodeURIComponent(board)}/input`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, value: s.value }),
      });
      if (!res.ok) toast((await res.json().catch(() => ({}))).detail ?? 'That did not go through.');
    } catch {
      toast('Could not reach the board.');
    }
  };
  if (now) return void go();
  if (s.timer !== undefined) return;
  const wait = 150 - (performance.now() - s.last);
  if (wait <= 0) go();
  else s.timer = setTimeout(go, wait);
}

/** Open the control for process i next to its block. @param {number} i @param {import('./views.js').ViewEntry} v */
function openControl(i, v) {
  const m = /** @type {Model} */ (st.model);
  const p = m.procs[i];
  const value = m.valueAt(i, m.end);
  const title = el('div', { class: 'control-title', text: p.label });
  /** @type {HTMLElement} */
  let body;
  if (p.skin === 'slider') {
    const lo = p.min ?? 0;
    const hi = p.max ?? 100;
    const step = Number.isInteger(lo) && Number.isInteger(hi) && hi - lo >= 10 ? 1 : (hi - lo) / 100;
    const out = el('output', { text: formatNum(Number(value ?? lo)) });
    const range = /** @type {HTMLInputElement} */ (el('input', { type: 'range', min: lo, max: hi, step, value: Number(value ?? lo), 'aria-label': p.label }));
    range.addEventListener('input', () => {
      out.textContent = formatNum(Number(range.value));
      sendValue(p.id, Number(range.value));
    });
    range.addEventListener('change', () => sendValue(p.id, Number(range.value), true));
    body = el('div', { class: 'control-row' }, range, out);
  } else if (p.skin === 'toggle') {
    const on = !!value && value !== '0';
    body = el(
      'button',
      {
        type: 'button',
        class: 'primary',
        onclick: () => {
          sendValue(p.id, !on, true);
          closeControl();
        },
      },
      on ? 'Turn off' : 'Turn on',
    );
  } else {
    const input = /** @type {HTMLInputElement} */ (el('input', { type: 'text', value: value ?? '', maxlength: 80, 'aria-label': p.label, enterkeyhint: 'done' }));
    const save = () => {
      sendValue(p.id, input.value, true);
      closeControl();
    };
    input.addEventListener('keydown', (e) => e.key === 'Enter' && save());
    body = el('div', { class: 'control-row' }, input, el('button', { type: 'button', class: 'primary', onclick: save }, 'Set'));
  }
  control.replaceChildren(title, body);
  control.hidden = false;
  const at = v.impl.where?.(i);
  const r = canvas.getBoundingClientRect();
  const box = control.getBoundingClientRect();
  const x = at ? r.left + st.safe.left + at.x : innerWidth / 2;
  const y = at ? r.top + st.safe.top + at.y + at.r + 12 : innerHeight / 2;
  control.style.left = `${Math.max(8, Math.min(innerWidth - box.width - 8, x - box.width / 2))}px`;
  control.style.top = `${Math.max(8, Math.min(innerHeight - box.height - 8, y))}px`;
  /** @type {HTMLElement | null} */ (control.querySelector('input, button'))?.focus();
}

function closeControl() {
  control.hidden = true;
}
addEventListener('pointerdown', (e) => {
  if (!control.hidden && !control.contains(/** @type {Node} */ (e.target))) closeControl();
});
control.addEventListener('keydown', (e) => e.key === 'Escape' && closeControl());

// ---- moving blocks (people may say where) ----

/** @type {{i: number, x: number, y: number, moving: boolean} | null} */
let drag = null;
let dragged = false;
const ghost = $('dragGhost');
canvas.addEventListener('pointerdown', (e) => {
  dragged = false; // a drag that ended outside the canvas never got its click
  if (watch || e.button !== 0) return;
  const v = views.find((x) => x.id === st.view) ?? views[0];
  if (!v.impl.cellAt) return;
  const r = canvas.getBoundingClientRect();
  const i = v.impl.pick(e.clientX - r.left - st.safe.left, e.clientY - r.top - st.safe.top);
  if (i >= 0) drag = { i, x: e.clientX, y: e.clientY, moving: false };
});
addEventListener('pointermove', (e) => {
  if (!drag) return;
  if (!drag.moving && Math.hypot(e.clientX - drag.x, e.clientY - drag.y) > 6) {
    drag.moving = true;
    ghost.hidden = false;
  }
  if (drag.moving) {
    ghost.style.left = `${e.clientX - 18}px`;
    ghost.style.top = `${e.clientY - 18}px`;
  }
});
addEventListener('pointercancel', () => {
  drag = null;
  ghost.hidden = true;
});
addEventListener('pointerup', async (e) => {
  const d = drag;
  drag = null;
  ghost.hidden = true;
  if (!d?.moving) return;
  dragged = true;
  const v = views.find((x) => x.id === st.view) ?? views[0];
  const r = canvas.getBoundingClientRect();
  const cell = v.impl.cellAt?.(e.clientX - r.left - st.safe.left, e.clientY - r.top - st.safe.top);
  const m = /** @type {Model} */ (st.model);
  if (!cell || m.world.length !== 2) return;
  const res = await fetch(`/api/boards/${encodeURIComponent(board)}/lines`, { method: 'POST', body: `move ${m.procs[d.i].id} ${cell[0]} ${cell[1]}` });
  const body = await res.json().catch(() => ({}));
  if (!res.ok || body.rejected?.length) toast(body.rejected?.[0]?.code === 'no_room' ? 'Too close to another block.' : 'It cannot go there.');
  else jumpToEnd = true;
});

// ---- welcome ----

/** @param {string} text */
const example = (text) => el('button', { type: 'button', class: 'example', onclick: () => talk.send(text) }, text);
function renderWelcome() {
  const w = $('welcomeBody');
  if (talk.on && talk.ready) {
    w.replaceChildren(
      el('h2', { text: 'Talk, and the board draws itself' }),
      el('p', {}, 'Hold ', el('kbd', { text: 'Space' }), ' or the microphone and say what you want to see. Or try one:'),
      el('div', { class: 'examples' }, example('Show me how a login request goes through our services'), example('Tunjukkan kenapa antrean checkout bisa macet')),
    );
  } else if (talk.on) {
    w.replaceChildren(el('h2', { text: 'This board is empty' }), el('p', { text: 'The agent needs Anthropic credentials on the server before it can draw. Agents connected over MCP can still write here.' }));
  } else {
    w.replaceChildren(
      el('h2', { text: 'This board is empty' }),
      el('p', { text: 'It fills in live as soon as an agent writes to it.' }),
      watch ? '' : el('pre', { text: `blockies append ${board} 'proc api "API"'` }),
    );
  }
}

// For tests and debugging: drive the page without a visible window.
Object.assign(window, { __blockies: { st, frame, runAction, applyTimes, views } });

bindKeys();
setView(st.view);
// On phones the drawer is a sheet over the board: start with it closed.
if (innerWidth >= 760 && saved.get('drawer', innerWidth >= 1200 ? '1' : '0') === '1') toggleDrawer(true);
await setupTalk(board, watch);
mountPanels();
if (!talk.on) show(saved.get('panel', 'procs') === 'chat' ? 'procs' : saved.get('panel', 'procs'));
renderWelcome();
measureSafe();
connect();
requestAnimationFrame(frame);
