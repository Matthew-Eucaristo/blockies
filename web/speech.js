// Speech in and out behind two small interfaces, so another engine (for
// example Whisper running locally) can replace the browser's later without
// touching the page. Today both use the Web Speech API.

/**
 * @typedef {object} Listener
 * @property {() => void} stop   finish: the text heard so far is delivered
 * @property {() => void} abort  cancel: nothing is delivered
 *
 * @typedef {object} ListenOptions
 * @property {string} lang
 * @property {(text: string) => void} onText   everything heard so far, while listening
 * @property {(text: string) => void} onEnd    the final text ('' if nothing)
 * @property {(code: string) => void} onError  a stable reason: blocked, no-mic, network, language, no-speech, failed
 */

/** The browser's recognizer, looked up when needed (so tests can install a fake). */
const Recognition = () => /** @type {any} */ (window).SpeechRecognition || /** @type {any} */ (window).webkitSpeechRecognition;

export const canListen = () => !!Recognition();

/**
 * Start listening. Returns null when the browser has no recognizer.
 * @param {ListenOptions} o
 * @returns {Listener | null}
 */
export function listen(o) {
  const R = Recognition();
  if (!R) return null;
  const rec = new R();
  rec.lang = o.lang;
  rec.continuous = true;
  rec.interimResults = true;
  let heard = '';
  let interim = '';
  let aborted = false;
  rec.onresult = (/** @type {any} */ e) => {
    interim = '';
    for (let i = e.resultIndex; i < e.results.length; i++) {
      const r = e.results[i];
      if (r.isFinal) heard += r[0].transcript;
      else interim += r[0].transcript;
    }
    o.onText(`${heard}${interim}`.trim());
  };
  rec.onerror = (/** @type {any} */ e) => {
    const code =
      {
        'not-allowed': 'blocked',
        'service-not-allowed': 'blocked',
        'audio-capture': 'no-mic',
        network: 'network',
        'language-not-supported': 'language',
        'no-speech': 'no-speech',
      }[/** @type {string} */ (e.error)] ?? (e.error === 'aborted' ? '' : 'failed');
    if (code) o.onError(code);
  };
  rec.onend = () => o.onEnd(aborted ? '' : `${heard} ${interim}`.trim());
  try {
    rec.start();
  } catch {
    return null;
  }
  return {
    stop: () => rec.stop(),
    abort: () => {
      aborted = true;
      rec.abort();
    },
  };
}

/**
 * Speaks a reply as it streams in, one sentence at a time.
 */
export class Speaker {
  constructor() {
    this.on = true;
    this.lang = 'en-US';
    this.text = '';
    this.spoken = 0;
    // Some browsers load their voices late; asking once starts that.
    window.speechSynthesis?.getVoices();
  }

  get speaking() {
    return !!window.speechSynthesis?.speaking;
  }

  /** Forget the reply and stop talking. */
  reset() {
    this.text = '';
    this.spoken = 0;
    window.speechSynthesis?.cancel();
  }

  /** @param {string} delta  more of the reply */
  add(delta) {
    this.text += delta;
    const rest = this.text.slice(this.spoken);
    let end = 0;
    for (const m of rest.matchAll(/[.!?…。]+["')\]]?\s/g)) end = (m.index ?? 0) + m[0].length;
    if (end > 0) this.say(rest.slice(0, end));
  }

  /** The reply is complete: speak what is left. */
  finish() {
    this.say(this.text.slice(this.spoken));
  }

  /** @param {string} part */
  say(part) {
    this.spoken += part.length;
    const synth = window.speechSynthesis;
    if (!this.on || !synth || !part.trim()) return;
    const u = new SpeechSynthesisUtterance(part.trim());
    u.lang = this.lang;
    const voice = pickVoice(this.lang);
    if (voice) u.voice = voice;
    synth.speak(u);
  }
}

/** The best voice for a language: an exact match, a natural-sounding one first. @param {string} lang */
function pickVoice(lang) {
  const voices = window.speechSynthesis?.getVoices() ?? [];
  const norm = (/** @type {string} */ l) => l.replace('_', '-').toLowerCase();
  const exact = voices.filter((v) => norm(v.lang) === lang.toLowerCase());
  const near = exact.length ? exact : voices.filter((v) => norm(v.lang).startsWith(lang.slice(0, 2).toLowerCase()));
  return near.find((v) => /premium|enhanced|natural|neural|google/i.test(v.name)) ?? near.find((v) => v.localService) ?? near[0];
}
