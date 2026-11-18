// The drawer on the right: tabs registered with addPanel(). Each panel gets
// its own element once and keeps it up to date itself (usually by listening
// on the bus); the drawer only switches tabs and shows badges.

import { $, el, saved } from './ui.js';

/**
 * @typedef {object} Panel
 * @property {string} id
 * @property {string} label
 * @property {(box: HTMLElement) => void} mount
 * @property {() => string} [badge]  short text next to the tab name, or ''
 * @property {() => boolean} [when]  shown only when this returns true
 * @property {number} [order]  tabs are sorted by this (default 10)
 */

/** @type {Panel[]} */
export const panels = [];
/** @type {Map<string, {tab: HTMLButtonElement, box: HTMLElement}>} */
const mounted = new Map();
let current = saved.get('panel', 'chat');

/** @param {Panel} p */
export function addPanel(p) {
  panels.push(p);
}

/** Build the drawer. Call once, after panels are registered. */
export function mountPanels() {
  const tabs = $('tabs');
  const body = $('panelBody');
  panels.sort((a, b) => (a.order ?? 10) - (b.order ?? 10));
  for (const p of panels) {
    const tab = /** @type {HTMLButtonElement} */ (
      el('button', { type: 'button', role: 'tab', id: `tab-${p.id}`, 'aria-controls': `panel-${p.id}`, onclick: () => show(p.id) }, el('span', { text: p.label }), el('span', { class: 'badge' }))
    );
    const box = el('section', { role: 'tabpanel', id: `panel-${p.id}`, 'aria-labelledby': `tab-${p.id}`, hidden: true });
    tabs.append(tab);
    body.append(box);
    mounted.set(p.id, { tab, box });
    p.mount(box);
  }
  tabs.addEventListener('keydown', (e) => {
    const shown = visible();
    const i = shown.findIndex((p) => p.id === current);
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      const next = shown[(i + (e.key === 'ArrowRight' ? 1 : shown.length - 1)) % shown.length];
      show(next.id);
      mounted.get(next.id)?.tab.focus();
    }
  });
  refreshPanels();
}

const visible = () => panels.filter((p) => !p.when || p.when());

/** @param {string} id */
export function show(id) {
  current = id;
  saved.set('panel', id);
  refreshPanels();
}

/** Update tab visibility, badges and the selected tab. */
export function refreshPanels() {
  const shown = visible();
  if (!shown.some((p) => p.id === current) && shown.length) current = shown[0].id;
  for (const p of panels) {
    const m = mounted.get(p.id);
    if (!m) continue;
    const on = shown.includes(p);
    m.tab.hidden = !on;
    m.tab.setAttribute('aria-selected', String(p.id === current));
    m.tab.tabIndex = p.id === current ? 0 : -1;
    m.box.hidden = p.id !== current || !on;
    const badge = /** @type {HTMLElement} */ (m.tab.querySelector('.badge'));
    badge.textContent = p.badge?.() ?? '';
  }
}

/** Open or close the drawer. @param {boolean} [open] */
export function toggleDrawer(open) {
  const d = $('drawer');
  const now = open ?? Boolean(d.hidden);
  d.hidden = !now;
  document.body.classList.toggle('drawer-open', now);
  saved.set('drawer', now ? '1' : '0');
  $('drawerBtn').setAttribute('aria-expanded', String(now));
  window.dispatchEvent(new Event('resize'));
}
