// The page around every concept: renderer switch, clock, process bar (one
// green box per running computer, as in Edward's sketch), and an editable
// spec, which is exactly what an agent would have to write.

import { Stage } from './stage.js';
import * as L from './lattice.js';

export function mount(concept) {
  document.title = concept.title;
  document.body.innerHTML = `
  <div class="app">
    <header><h1>${concept.title}</h1><span class="tag">${concept.tagline}</span>
      <nav><a href="./">all concepts</a></nav></header>
    <section class="stagecol">
      <div class="toolbar">
        <button data-mode="flat" title="1">Flat</button>
        <button data-mode="voxel" title="2">Voxel</button>
        <button data-mode="spacetime" title="3">Space-time</button>
        <span class="sep"></span>
        <button id="play" title="space">Pause</button>
        <button id="step" title=".">Step</button>
        <button id="reset">Reset</button>
        <span class="sep"></span>
        <label style="font-size:12px;color:var(--muted)">speed <input id="speed" type="range" min="1" max="30" style="width:80px;vertical-align:middle"></label>
        <span id="sliceBox" style="display:none;font-size:12px;color:var(--muted)">slice <input id="slice" type="range" min="0" value="0" style="width:70px;vertical-align:middle"></span>
        <span class="tick" id="tick"></span>
      </div>
      <canvas id="cv"></canvas>
      <div class="status" id="status"></div>
      <div class="procbar" id="procs"></div>
    </section>
    <aside>
      <div class="tabs"><button data-tab="idea" class="on">Idea</button><button data-tab="spec">Spec</button><button data-tab="notes">Notes</button></div>
      <div class="pane on" id="ideaPane">${concept.idea}</div>
      <div class="pane" id="specPane">
        <div class="specbar"><select id="variant" style="display:none"></select><span id="cost"></span><span id="purity" class="badge">pure</span><button id="apply">Apply ⌘↵</button></div>
        <textarea id="spec" spellcheck="false"></textarea>
        <div class="err" id="err"></div>
      </div>
      <div class="pane" id="notesPane">${concept.notes}</div>
    </aside>
  </div>`;

  const $ = (s) => document.querySelector(s);
  const cv = $('#cv');
  const stage = new Stage(cv);
  const specEl = $('#spec');
  const variants = concept.variants ?? { default: concept.spec };
  const vnames = Object.keys(variants);
  const want = new URLSearchParams(location.search).get('v');
  specEl.value = variants[vnames.includes(want) ? want : vnames[0]].trim();
  if (vnames.length > 1) {
    const sel = document.querySelector('#variant');
    sel.style.display = '';
    sel.innerHTML = vnames.map((n) => `<option ${n === want ? 'selected' : ''}>${n}</option>`).join('');
    sel.onchange = () => {
      specEl.value = variants[sel.value].trim();
      const u = new URL(location.href);
      u.searchParams.set('v', sel.value);
      history.replaceState(null, '', u);
      build();
    };
  }
  let rt = null;
  let playing = true;
  let rate = concept.rate ?? 6;
  let acc = 0;

  function build() {
    $('#err').textContent = '';
    try {
      const helpers = { ...L, ...(concept.helpers || {}) };
      const fn = new Function(...Object.keys(helpers), `"use strict"; return (${specEl.value}\n);`);
      const spec = fn(...Object.values(helpers));
      rt = concept.create(spec);
      if (spec.view) setMode(spec.view);
      stage.setShape(rt.shape);
      $('#sliceBox').style.display = (rt.shape[2] ?? 1) > 1 ? '' : 'none';
      $('#slice').max = (rt.shape[2] ?? 1) - 1;
      stage.setItems(rt.items());
      stage.record();
      renderProcs();
    } catch (e) {
      $('#err').textContent = String(e && e.stack ? e.message : e);
      console.error(e);
    }
    const src = specEl.value;
    $('#cost').textContent = `agent writes ${src.length} chars · ${src.split('\n').length} lines`;
  }

  function tick() {
    if (!rt) return;
    try {
      rt.tick();
    } catch (e) {
      $('#err').textContent = 'tick: ' + e.message;
      playing = false;
      syncPlay();
      return;
    }
    stage.setItems(rt.items());
    stage.record();
    renderProcs();
  }

  let lastProcs = '';
  function renderProcs() {
    const ps = rt.processes ? rt.processes() : [];
    const html =
      `<span class="label">processes (${ps.filter((p) => p.status !== 'done').length})</span>` +
      ps.map((p) => `<span class="proc ${p.status || ''}" data-id="${p.id}">${esc(p.label)}${p.note ? `<small>${esc(p.note)}</small>` : ''}</span>`).join('');
    if (html !== lastProcs) {
      $('#procs').innerHTML = html;
      lastProcs = html;
    }
    $('#tick').textContent = `tick ${rt.time ?? ''}`;
    $('#status').textContent = rt.status ? rt.status() : '';
    const bad = rt.impure && rt.impure.size;
    $('#purity').textContent = bad ? `impure: ${[...rt.impure].join(', ')}` : 'pure ✓';
    $('#purity').classList.toggle('bad', !!bad);
  }

  $('#procs').addEventListener('mouseover', (e) => {
    const el = e.target.closest('.proc');
    stage.highlight = el ? el.dataset.id : null;
  });
  $('#procs').addEventListener('mouseleave', () => (stage.highlight = null));

  function setMode(m) {
    stage.mode = m;
    document.querySelectorAll('[data-mode]').forEach((b) => b.classList.toggle('on', b.dataset.mode === m));
  }
  document.querySelectorAll('[data-mode]').forEach((b) => (b.onclick = () => setMode(b.dataset.mode)));
  setMode(concept.mode ?? 'flat');

  function syncPlay() {
    $('#play').textContent = playing ? 'Pause' : 'Play';
  }
  $('#play').onclick = () => {
    playing = !playing;
    syncPlay();
  };
  $('#step').onclick = () => {
    playing = false;
    syncPlay();
    tick();
  };
  $('#reset').onclick = build;
  $('#apply').onclick = build;
  $('#speed').value = rate;
  $('#speed').oninput = (e) => (rate = +e.target.value);
  $('#slice').oninput = (e) => (stage.slice = +e.target.value);
  specEl.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      build();
    }
    e.stopPropagation();
  });
  document.addEventListener('keydown', (e) => {
    if (e.target.tagName === 'INPUT') return;
    if (e.key === ' ') {
      e.preventDefault();
      $('#play').click();
    } else if (e.key === '.') tick();
    else if (e.key === '1') setMode('flat');
    else if (e.key === '2') setMode('voxel');
    else if (e.key === '3') setMode('spacetime');
    else if (e.key === 'Escape' && rt && rt.up) {
      rt.up();
      stage.setShape(rt.shape);
      stage.setItems(rt.items());
      renderProcs();
    }
  });
  document.querySelectorAll('[data-tab]').forEach((b) => {
    b.onclick = () => {
      document.querySelectorAll('[data-tab]').forEach((x) => x.classList.toggle('on', x === b));
      document.querySelectorAll('.pane').forEach((p) => p.classList.toggle('on', p.id === b.dataset.tab + 'Pane'));
    };
  });

  let dragging = false;
  const poke = (e, first) => {
    if (!rt || !rt.click) return;
    const r = cv.getBoundingClientRect();
    const c = stage.pick(e.clientX - r.left, e.clientY - r.top);
    if (!c) return;
    const before = rt.shape.join();
    rt.click(c, { first, shift: e.shiftKey, alt: e.altKey });
    if (rt.shape.join() !== before) stage.setShape(rt.shape);
    stage.setItems(rt.items());
    renderProcs();
  };
  cv.addEventListener('pointerdown', (e) => {
    dragging = true;
    poke(e, true);
  });
  cv.addEventListener('pointermove', (e) => dragging && concept.drag && poke(e, false));
  addEventListener('pointerup', () => (dragging = false));

  build();
  let last = performance.now();
  function frame(now) {
    const dt = Math.min(0.1, (now - last) / 1000);
    last = now;
    if (playing && rt) {
      acc += dt * rate;
      let n = 0;
      while (acc >= 1 && n < 8) {
        tick();
        acc -= 1;
        n++;
      }
      if (n === 8) acc = 0;
    }
    stage.update(dt);
    stage.render();
    requestAnimationFrame(frame);
  }
  requestAnimationFrame(frame);
  window.__stage = stage;
  window.__rt = () => rt;
}

const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);
