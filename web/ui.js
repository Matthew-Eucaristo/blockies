// Small helpers shared by the page's modules: elements, storage that never
// throws, a toast, and an event bus so modules talk without importing each
// other.

/** @template {HTMLElement} [T=HTMLElement] @param {string} id @returns {T} */
export const $ = (id) => /** @type {T} */ (document.getElementById(id));

/** @param {string} s */
export function esc(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] ?? c);
}

/**
 * Make an element. Attributes starting with "on" become listeners; others
 * become attributes (true for boolean ones). Children are strings or nodes.
 * @param {string} tag
 * @param {Record<string, any>} [attrs]
 * @param {...(string | Node | null | undefined | false)} children
 */
export function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith('on') && typeof v === 'function') e.addEventListener(k.slice(2), v);
    else if (k === 'text') e.textContent = String(v);
    else e.setAttribute(k, v === true ? '' : String(v));
  }
  for (const c of children) if (c !== null && c !== undefined && c !== false) e.append(c);
  return e;
}

/** localStorage that keeps working when storage is blocked. */
export const saved = {
  /** @param {string} key @param {string} fallback */
  get(key, fallback) {
    try {
      return localStorage.getItem(`blockies.${key}`) ?? fallback;
    } catch {
      return fallback;
    }
  },
  /** @param {string} key @param {string} value */
  set(key, value) {
    try {
      localStorage.setItem(`blockies.${key}`, value);
    } catch {}
  },
};

/** @type {number | undefined} */
let toastTimer;
/** Show a short message at the top of the page. @param {string} text */
export function toast(text) {
  const t = $('toast');
  t.textContent = text;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), 2600);
}

export const motion = matchMedia('(prefers-reduced-motion: reduce)');

/** The page's event bus: frame (a new board frame), tick (the shown tick changed), layout. */
export const bus = new EventTarget();
/** @param {string} name @param {any} [detail] */
export const emit = (name, detail) => bus.dispatchEvent(new CustomEvent(name, { detail }));
/** @param {string} name @param {(detail: any) => void} fn */
export const on = (name, fn) => bus.addEventListener(name, (e) => fn(/** @type {CustomEvent} */ (e).detail));

/** Whether a keyboard event is typing into a field. @param {EventTarget | null} t */
export const typing = (t) =>
  t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t instanceof HTMLSelectElement || (t instanceof HTMLElement && t.isContentEditable);
