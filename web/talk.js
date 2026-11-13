// Talking to the board's agent: the dock (microphone, text box, undo, voice),
// captions over the board, the conversation panel, and their actions.
// The board itself changes through the server like any other write, so this
// module never touches the canvas: app.js redraws when the new frame arrives.

import { addAction } from './actions.js';
import { addPanel, refreshPanels, show, toggleDrawer } from './panels.js';
import { Speaker, canListen, listen } from './speech.js';
import { $, el, saved, toast } from './ui.js';

/**
 * @typedef {{type: 'say' | 'draw' | 'redraw' | 'undo' | 'error' | 'done', text?: string,
 *   lines?: string[], rejected?: {line: number, code: string, detail: string}[],
 *   code?: string, detail?: string}} TalkEvent
 * @typedef {{who: 'person' | 'agent' | 'undo', text?: string, lines?: number}} Turn
 * @typedef {'off' | 'setup' | 'idle' | 'listening' | 'thinking' | 'drawing' | 'speaking'} Phase
 */

const LANGS = /** @type {const} */ ([
  ['id-ID', 'Bahasa Indonesia'],
  ['en-US', 'English'],
]);

const SPEECH_ERRORS = /** @type {Record<string, string>} */ ({
  blocked: 'The microphone is blocked. Allow it in the address bar, or type.',
  'no-mic': 'No microphone found. Type instead.',
  network: 'Speech recognition could not reach its service. Type instead.',
  language: 'This browser cannot recognise that language. Pick another in the menu, or type.',
  'no-speech': 'Nothing heard. Hold Space (or the mic) while you speak.',
  failed: 'Speech recognition stopped. Try again, or type.',
});

const SETUP = 'Talking needs Anthropic credentials where `blockies serve` runs: set ANTHROPIC_API_KEY, or run `ant auth login`.';

/** The server's error details are written for people; start them with a capital. @param {string} s */
const sentence = (s) => s.charAt(0).toUpperCase() + s.slice(1) + (/[.!?]$/.test(s) ? '' : '.');

export const talk = {
  /** Whether the dock is in use on this page. */
  on: false,
  /** Whether the agent can answer (credentials present). */
  ready: false,
  /** @type {Phase} */
  phase: /** @type {Phase} */ ('off'),
  /** @param {string} _text */
  send: async (_text) => {},
};

/**
 * Set up talking for a board. watch: the page is a watch-only link.
 * @param {string} board
 * @param {boolean} watch
 */
export async function setupTalk(board, watch) {
  const api = `/api/boards/${encodeURIComponent(board)}/talk`;
  const speaker = new Speaker();
  const st = {
    lang: saved.get('lang', navigator.language.toLowerCase().startsWith('id') ? 'id-ID' : 'en-US'),
    /** @type {import('./speech.js').Listener | null} */ listener: null,
    latched: false,
    pressedAt: 0,
    /** @type {AbortController | null} */ ctl: null,
    /** @type {Promise<void> | null} */ running: null,
    /** @type {number | undefined} */ hideTimer: undefined,
    turns: 0,
  };
  speaker.on = saved.get('voice', '1') === '1';
  speaker.lang = st.lang;

  const dock = $('dock');
  const mic = /** @type {HTMLButtonElement} */ ($('mic'));
  const input = /** @type {HTMLInputElement} */ ($('say'));
  const status = $('status');
  const caption = $('caption');
  const voiceBtn = $('voice');
  const chat = el('ol', { class: 'chat' });

  // ---- phases, status and captions ----

  /** @param {Phase} p @param {string} [text] @param {boolean} [bad] */
  function phase(p, text = '', bad = false) {
    talk.phase = p;
    document.body.dataset.talk = p;
    status.textContent = text;
    status.classList.toggle('bad', bad);
    mic.setAttribute('aria-pressed', String(p === 'listening'));
  }

  /** @param {'you' | 'agent'} who @param {string} text */
  function showCaption(who, text) {
    clearTimeout(st.hideTimer);
    caption.hidden = !text;
    caption.className = `caption ${who}`;
    caption.textContent = text;
  }

  function hideCaptionSoon() {
    clearTimeout(st.hideTimer);
    st.hideTimer = setTimeout(function wait() {
      if (speaker.speaking) st.hideTimer = setTimeout(wait, 400);
      else {
        caption.hidden = true;
        if (talk.phase === 'speaking') phase('idle', idleText());
      }
    }, 4000);
  }

  // The text box's placeholder already says how to talk; the status line only
  // speaks up when something is happening or needs saying.
  const idleText = () => (canListen() ? '' : 'This browser cannot listen: type, or use dictation (on a Mac, press fn twice).');

  // ---- conversation panel ----

  /** @param {Turn} t */
  function addTurn(t) {
    chat.querySelector('.hint')?.remove();
    const li = el('li', { class: t.who });
    if (t.who === 'undo') li.textContent = 'Went back one step';
    else li.append(el('div', { class: 'text', text: t.text ?? '' }));
    if (t.lines) setLines(li, t.lines);
    chat.append(li);
    li.scrollIntoView({ block: 'nearest' });
    if (t.who === 'person') st.turns++;
    refreshPanels();
    return li;
  }

  /** @param {HTMLElement} li @param {number} n */
  function setLines(li, n) {
    let meta = li.querySelector('.meta');
    if (!meta) li.append((meta = el('div', { class: 'meta' })));
    meta.textContent = `drew ${n} line${n === 1 ? '' : 's'}`;
  }

  function emptyChat() {
    chat.replaceChildren(el('li', { class: 'hint', text: 'Nothing said yet. What you and the agent say shows up here.' }));
    st.turns = 0;
    refreshPanels();
  }

  addPanel({
    id: 'chat',
    label: 'Chat',
    order: 0,
    when: () => talk.on,
    badge: () => (st.turns ? String(st.turns) : ''),
    mount: (box) => {
      box.append(
        el('div', { class: 'panel-head' }, el('button', { type: 'button', class: 'quiet', onclick: () => newChat(), title: 'Start a new conversation; the board stays' }, 'New chat')),
        chat,
      );
    },
  });
  emptyChat();

  // ---- is talking available here? ----

  if (watch) return;
  let info;
  try {
    const res = await fetch(api);
    info = await res.json();
    if (!res.ok) return; // no_agent: the page stays a viewer
  } catch {
    return;
  }
  talk.on = true;
  talk.ready = !!info.ready;
  document.body.classList.add('talking');
  dock.hidden = false;
  for (const t of /** @type {Turn[]} */ (info.turns ?? [])) addTurn(t);
  refreshPanels();
  if (!talk.ready) {
    setNotReady();
  } else {
    phase('idle', idleText());
  }
  if (matchMedia('(pointer: coarse)').matches) input.placeholder = 'Type, or tap the mic';

  function setNotReady() {
    phase('setup', SETUP, true);
    mic.disabled = true;
    input.disabled = true;
    $('send').toggleAttribute('disabled', true);
  }

  async function recheck() {
    try {
      const res = await fetch(api);
      const body = await res.json();
      talk.ready = !!body.ready;
    } catch {}
    if (talk.ready) {
      mic.disabled = false;
      input.disabled = false;
      $('send').toggleAttribute('disabled', false);
      phase('idle', idleText());
    }
    return talk.ready;
  }

  // ---- listening ----

  function startListening() {
    speaker.reset();
    if (st.running) st.ctl?.abort(); // talking over the agent interrupts it
    if (!canListen()) {
      input.focus();
      phase('idle', 'This browser has no speech recognition. Type, or use dictation (on a Mac, press fn twice). Chrome and Safari can listen.');
      return;
    }
    st.listener = listen({
      lang: st.lang,
      onText: (text) => showCaption('you', text || '…'),
      onError: (code) => phase('idle', SPEECH_ERRORS[code] ?? SPEECH_ERRORS.failed, code !== 'no-speech'),
      onEnd: (text) => {
        st.listener = null;
        st.latched = false;
        mic.classList.remove('on');
        if (text) send(text);
        else {
          caption.hidden = true;
          if (talk.phase === 'listening') phase('idle', idleText());
        }
      },
    });
    if (!st.listener) return;
    mic.classList.add('on');
    showCaption('you', '…');
    phase('listening', 'Listening… release to send');
  }

  function press() {
    if (!talk.ready) return;
    if (st.listener && st.latched) return st.listener.stop();
    if (st.listener) return;
    st.pressedAt = performance.now();
    startListening();
  }

  function release() {
    if (!st.listener) return;
    // A short press keeps listening until the next press.
    if (performance.now() - st.pressedAt < 300) {
      st.latched = true;
      phase('listening', 'Listening… press again to send');
    } else st.listener.stop();
  }

  mic.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    mic.setPointerCapture(e.pointerId);
    press();
  });
  mic.addEventListener('pointerup', release);
  mic.addEventListener('pointercancel', release);
  mic.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.repeat) {
      e.preventDefault();
      press();
    }
  });
  mic.addEventListener('keyup', (e) => {
    if (e.key === 'Enter') release();
  });

  // ---- a turn ----

  /** @param {string} text */
  async function send(text) {
    text = text.trim();
    if (!text || !talk.ready) return;
    if (st.running) {
      st.ctl?.abort();
      await st.running;
    }
    st.running = turn(text).finally(() => (st.running = null));
    return st.running;
  }
  talk.send = send;

  /** @param {string} text */
  async function turn(text) {
    addTurn({ who: 'person', text });
    const li = addTurn({ who: 'agent', text: '' });
    li.classList.add('pending');
    const out = /** @type {HTMLElement} */ (li.querySelector('.text'));
    speaker.reset();
    phase('thinking', 'Thinking…');
    let drawn = 0;
    let failed = false;
    const ctl = (st.ctl = new AbortController());

    /** @param {TalkEvent} ev */
    const handle = (ev) => {
      switch (ev.type) {
        case 'say':
          speaker.add(ev.text ?? '');
          out.textContent = speaker.text;
          showCaption('agent', speaker.text);
          if (talk.phase === 'thinking') phase('speaking', speaker.on ? 'Speaking… (Esc stops)' : '');
          break;
        case 'draw':
          drawn += (ev.lines?.length ?? 0) - (ev.rejected?.length ?? 0);
          setLines(li, drawn);
          phase('drawing', `Drawing… ${drawn} line${drawn === 1 ? '' : 's'}`);
          break;
        case 'redraw':
          drawn += ev.lines?.length ?? 0;
          setLines(li, drawn);
          phase('drawing', 'Redrawing the board…');
          break;
        case 'undo':
          phase('drawing', 'Going back…');
          break;
        case 'error':
          failed = true;
          fail(ev.code ?? 'internal', ev.detail ?? '');
          break;
      }
    };

    try {
      const res = await fetch(api, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text }), signal: ctl.signal });
      if (!res.ok || !res.body) {
        const e = await res.json().catch(() => ({}));
        failed = true;
        fail(e.code ?? 'internal', e.detail ?? res.statusText);
        return;
      }
      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
      let buf = '';
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += value;
        let i;
        while ((i = buf.indexOf('\n\n')) >= 0) {
          for (const line of buf.slice(0, i).split('\n')) {
            if (line.startsWith('data: ')) handle(JSON.parse(line.slice(6)));
          }
          buf = buf.slice(i + 2);
        }
      }
      speaker.finish();
    } catch {
      if (ctl.signal.aborted) {
        li.classList.add('stopped');
        failed = true;
        phase('idle', 'Stopped.');
      } else {
        failed = true;
        fail('offline', 'Could not reach the server.');
      }
    } finally {
      li.classList.remove('pending');
      if (!out.textContent) out.textContent = li.classList.contains('stopped') ? '(stopped)' : drawn ? '' : '…';
      if (!failed) phase(speaker.speaking ? 'speaking' : 'idle', speaker.speaking ? 'Speaking… (Esc stops)' : idleText());
      hideCaptionSoon();
    }
  }

  /** @param {string} code @param {string} detail */
  function fail(code, detail) {
    if (code === 'agent_auth') {
      talk.ready = false;
      setNotReady();
      return;
    }
    phase('idle', detail ? sentence(detail) : code, true);
  }

  $('ask').addEventListener('submit', (e) => {
    e.preventDefault();
    const text = input.value;
    input.value = '';
    send(text);
  });

  async function undo() {
    if (st.running) return phase(talk.phase, 'Wait for the answer to finish, then undo.');
    const res = await fetch(`${api}/undo`, { method: 'POST' });
    if (res.ok) {
      addTurn({ who: 'undo' });
      phase('idle', 'Went back one step.');
      return;
    }
    const e = await res.json().catch(() => ({}));
    const words = /** @type {Record<string, string>} */ ({
      no_history: 'Nothing to undo.',
      conflict: 'Someone else changed this board since, so undo would erase their work.',
      busy: 'Wait for the answer to finish, then undo.',
    });
    phase('idle', words[e.code] ?? e.detail ?? 'Undo failed.', e.code === 'conflict');
  }
  $('undo').addEventListener('click', undo);

  async function newChat() {
    if (st.running) st.ctl?.abort();
    await st.running;
    const res = await fetch(api, { method: 'DELETE' });
    if (!res.ok) return phase('idle', 'Could not start a new chat.', true);
    emptyChat();
    speaker.reset();
    caption.hidden = true;
    phase('idle', 'New conversation. The board stays as it is.');
  }

  function setVoice(/** @type {boolean} */ on) {
    speaker.on = on;
    saved.set('voice', on ? '1' : '0');
    voiceBtn.setAttribute('aria-pressed', String(on));
    voiceBtn.title = on ? 'Replies are read aloud (click to mute)' : 'Replies are muted (click to read aloud)';
    if (!on) window.speechSynthesis?.cancel();
  }
  setVoice(speaker.on);
  voiceBtn.addEventListener('click', () => setVoice(!speaker.on));

  // ---- actions ----

  const canTalk = () => talk.on && talk.ready;
  addAction({ id: 'talk', label: 'Hold to talk (a short press keeps listening)', group: 'talk', keys: ['Space'], when: canTalk, run: press, release });
  addAction({ id: 'type', label: 'Type instead', group: 'talk', keys: ['/'], when: canTalk, run: () => input.focus() });
  addAction({ id: 'undo', label: 'Undo the last change', group: 'talk', keys: ['u'], menu: true, when: () => talk.on, run: undo });
  addAction({
    id: 'stop',
    label: 'Stop listening or speaking',
    group: 'talk',
    keys: ['Escape'],
    when: () => !!st.listener || speaker.speaking || !!st.running,
    run: () => {
      if (st.listener) st.listener.abort();
      else if (st.running) st.ctl?.abort();
      speaker.reset();
      caption.hidden = true;
      phase('idle', idleText());
    },
  });
  addAction({ id: 'newChat', label: 'New chat (the board stays)', group: 'talk', menu: true, when: () => talk.on, run: newChat });
  addAction({ id: 'voice', label: 'Read replies aloud on or off', group: 'talk', keys: ['v'], when: () => talk.on, run: () => setVoice(!speaker.on) });
  addAction({
    id: 'lang',
    label: `Speech language: ${LANGS.find(([k]) => k === st.lang)?.[1] ?? st.lang}`,
    group: 'talk',
    menu: true,
    when: () => talk.on,
    run() {
      const i = LANGS.findIndex(([k]) => k === st.lang);
      const [k, name] = LANGS[(i + 1) % LANGS.length];
      st.lang = k;
      speaker.lang = k;
      saved.set('lang', k);
      this.label = `Speech language: ${name}`;
      toast(`Speech language: ${name}`);
    },
  });
  addAction({
    id: 'chat',
    label: 'Show the conversation',
    group: 'talk',
    keys: ['c'],
    when: () => talk.on,
    run: () => {
      show('chat');
      toggleDrawer(true);
    },
  });
  addAction({ id: 'recheck', label: 'Check the agent again', group: 'talk', menu: true, when: () => talk.on && !talk.ready, run: async () => toast((await recheck()) ? 'The agent is ready.' : 'Still no credentials on the server.') });
}
