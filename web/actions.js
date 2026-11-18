// The action registry: everything a person can do by key or menu, in one
// table. The keyboard, the menu and the shortcuts dialog all read it, so a
// new action shows up everywhere at once. Add one with addAction().

import { el, typing } from './ui.js';

/**
 * @typedef {object} Action
 * @property {string} id
 * @property {string} label
 * @property {'view' | 'play' | 'talk' | 'board'} group  where it is listed in the shortcuts dialog
 * @property {string[]} [keys]  KeyboardEvent.key values ("Space" for the space bar)
 * @property {boolean} [menu]  also listed in the main menu
 * @property {() => boolean} [when]  enabled only when this returns true
 * @property {(e?: KeyboardEvent) => void} run
 * @property {() => void} [release]  called when a held key goes up
 */

/** @type {Action[]} */
export const actions = [];

/** @param {Action} a */
export function addAction(a) {
  actions.push(a);
}

/** @param {string} id */
export function runAction(id) {
  const a = actions.find((x) => x.id === id);
  if (a && (!a.when || a.when())) a.run();
}

const GROUPS = { view: 'View', play: 'Playback', talk: 'Talking', board: 'Board' };

/** @param {string} k */
const keyName = (k) => ({ Space: 'Space', ArrowLeft: '←', ArrowRight: '→', Escape: 'Esc', Enter: 'Enter' })[k] ?? k.toUpperCase();

/** Route key presses to actions. Typing in a field is left alone. */
export function bindKeys() {
  /** @type {Map<string, Action>} */
  const held = new Map();
  // How focus last moved: Tab, or arrows/Home/End inside a menu or a tab
  // list (where they move focus), mean the keyboard; a pointer press means
  // not. Elsewhere arrows step playback and leave focus where it is.
  let byKeyboard = false;
  const moves = new Set(['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End']);
  addEventListener('pointerdown', () => (byKeyboard = false), true);
  addEventListener(
    'keydown',
    (e) => {
      if (e.key === 'Tab') byKeyboard = true;
      else if (moves.has(e.key) && e.target instanceof Element && e.target.closest('[role="menu"], [role="tablist"]')) byKeyboard = true;
    },
    true,
  );
  addEventListener('keydown', (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (typing(e.target) && e.key !== 'Escape') return;
    // A modal dialog owns the keyboard (Esc closes it).
    if (document.querySelector('dialog[open]')) return;
    const key = e.key === ' ' ? 'Space' : e.key;
    // Space and Enter press a button or link that was reached with the
    // keyboard, as everywhere else. After a mouse click the button keeps
    // focus, but Space still means talk or play. (:focus-visible cannot tell:
    // Chrome turns it on as soon as a key goes down.)
    if ((key === 'Space' || key === 'Enter') && byKeyboard && e.target instanceof Element && e.target.closest('button, a[href], [role="tab"], [role="menuitem"], summary')) return;
    const a = actions.find((x) => x.keys?.includes(key) && (!x.when || x.when()));
    if (!a) return;
    e.preventDefault();
    if (e.repeat) return;
    if (a.release) held.set(key, a);
    a.run(e);
  });
  addEventListener('keyup', (e) => {
    const key = e.key === ' ' ? 'Space' : e.key;
    const a = held.get(key);
    if (a) {
      held.delete(key);
      a.release?.();
    }
  });
  // A key released outside the window would otherwise stay held.
  addEventListener('blur', () => {
    for (const a of held.values()) a.release?.();
    held.clear();
  });
}

/** Fill the shortcuts dialog from the registry. @param {HTMLElement} box */
export function renderShortcuts(box) {
  box.replaceChildren(
    ...Object.entries(GROUPS).map(([g, title]) => {
      const list = actions.filter((a) => a.group === g && a.keys?.length && (!a.when || a.when()));
      if (!list.length) return '';
      return el(
        'section',
        {},
        el('h3', { text: title }),
        el('dl', {}, ...list.flatMap((a) => [el('dt', {}, ...(a.keys ?? []).map((k) => el('kbd', { text: keyName(k) }))), el('dd', { text: a.label })])),
      );
    }),
  );
}

/** Fill the main menu from the registry, grouped. @param {HTMLElement} box @param {() => void} close */
export function renderMenu(box, close) {
  const order = ['talk', 'view', 'play', 'board'];
  const items = actions.filter((a) => a.menu && (!a.when || a.when())).sort((a, b) => order.indexOf(a.group) - order.indexOf(b.group));
  box.replaceChildren(
    ...items.flatMap((a, i) => [i > 0 && items[i - 1].group !== a.group ? el('hr', { role: 'separator' }) : ''].concat(
      [a].map((a) =>
        el(
          'button',
          {
            role: 'menuitem',
            type: 'button',
            onclick: () => {
              close();
              a.run();
            },
          },
          el('span', { text: a.label }),
          a.keys?.length ? el('kbd', { text: keyName(a.keys[0]) }) : '',
        ),
      ),
    )),
  );
}
