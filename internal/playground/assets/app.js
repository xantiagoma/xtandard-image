// xtandard-image playground v2 (WP-94). Plain ES2022 modules, no
// dependencies. Everything comes from api/meta; URLs are signed by
// api/sign on the server (keys never reach the browser) and previews go
// through api/render, so every request stays on this origin.

import { Builder } from './builder.js';
import { $, b64url, copyText, debounce, el, escapePlain, postJSON, store, tabs, toast, unb64url } from './dom.js';
import { build, curlSnippet, tsSnippet } from './grammar.js';
import { Info } from './info.js';
import { Matrix } from './matrix.js';
import { Preview } from './preview.js';
import { Sources } from './sources.js';
import { Translate } from './translate.js';

const PREFS = 'xi-playground';
const HISTORY = 'xi-playground-history';
const HISTORY_MAX = 30;

const state = {
  mount: '',
  prefix: '', // one of the mount's prefixes; '' is its first
  source: '',
  sourceLabel: '',
  pipelines: [[]],
  alias: false,
  encoding: 'plain',
  ext: '',
  free: { path: '', query: '' },
  edits: '',
};

let meta;
const catalogues = new Map(); // grammar → {options, index}
let builder;
let preview;
let sources;
let info;
let translate;
let matrix;
let lastSigned = null; // {req, url, signed, scheme}
let seq = 0;

// ---- state in the URL hash -------------------------------------------

function snapshot() {
  return {
    m: state.mount, s: state.source, l: state.sourceLabel, a: state.alias ? 1 : 0, e: state.encoding, x: state.ext,
    p: state.pipelines.map((p) => p.map((e) => [e.name, e.form, e.values, e.groups, e.on ? 1 : 0])),
    f: state.free, j: state.edits, ...(state.prefix ? { pf: state.prefix } : {}),
  };
}

function restore(s) {
  if (!s || typeof s !== 'object') return false;
  state.mount = s.m ?? state.mount;
  state.prefix = typeof s.pf === 'string' ? s.pf : '';
  state.source = s.s ?? '';
  state.sourceLabel = s.l ?? '';
  state.alias = Boolean(s.a);
  state.encoding = s.e === 'base64' ? 'base64' : 'plain';
  state.ext = s.x ?? '';
  state.pipelines = Array.isArray(s.p) && s.p.length
    ? s.p.map((p) => (Array.isArray(p) ? p.map(([name, form, values, groups, on]) => ({
      name, form: form ?? 0, values: Array.isArray(values) ? values.map(String) : [], groups: groups ?? 1, on: Boolean(on),
    })) : []))
    : [[]];
  state.free = { path: s.f?.path ?? '', query: s.f?.query ?? '' };
  state.edits = s.j ?? '';
  return true;
}

function readHash() {
  const h = location.hash.replace(/^#/, '');
  if (!h.startsWith('v1:')) return null;
  try { return JSON.parse(unb64url(h.slice(3))); } catch { return null; }
}

const writeHash = debounce(() => {
  history.replaceState(null, '', `#v1:${b64url(JSON.stringify(snapshot()))}`);
}, 300);

// ---- helpers -------------------------------------------------------------

const currentMount = () => meta.mounts.find((m) => m.id === state.mount);
const baseURL = () => $('base-url').value.replace(/\/+$/, '');
const publicURL = (path) => baseURL() + path;
const sign = (body) => postJSON('api/sign', body);

function setStatus(msg, isError = false) {
  const s = $('status');
  s.textContent = msg;
  s.classList.toggle('error', isError);
}

function savePrefs() {
  store.set(PREFS, { base: $('base-url').value, accept: $('accept').value, mount: state.mount });
}

// ---- meta ------------------------------------------------------------------

async function loadMeta() {
  const res = await fetch('api/meta', { headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`api/meta answered ${res.status}`);
  meta = await res.json();
  for (const cat of meta.catalogues ?? []) {
    const index = new Map();
    for (const o of cat.options) {
      index.set(o.name, o);
      for (const a of o.aliases ?? []) if (!index.has(a)) index.set(a, o);
    }
    catalogues.set(cat.grammar, { options: cat.options, index });
  }
}

function renderMeta(prefs) {
  $('version').textContent = `version ${meta.version || 'dev'}`;
  const mode = $('signing-mode');
  mode.textContent = `${meta.signing.mode}, unsigned policy ${meta.signing.unsigned_policy}`;
  mode.classList.add(meta.signing.mode === 'off' ? 'warn' : 'ok');
  const port = (meta.public_addr || ':8080').split(':').pop() || '8080';
  $('base-url').value = prefs.base || (meta.public ? location.origin : `${location.protocol}//${location.hostname}:${port}`);
  if (prefs.accept) $('accept').value = prefs.accept;
  renderMounts();
  for (const f of meta.formats ?? []) $('extension').append(el('option', { value: f, text: f }));
  for (const c of meta.classes ?? []) $('xi-classes').append(el('option', { value: c }));
  for (const p of meta.presets ?? []) $('xi-presets').append(el('option', { value: p }));
}

// ---- mount and builder -----------------------------------------------------

// renderMounts fills the mount picker: one entry per mount (a mount with
// several prefixes, like a simple provider's native prefixes and
// /simple/<name>/, is listed once and its prefix picked below), grouped
// into dialects with a builder, simple providers and the other free-form
// dialects.
function renderMounts() {
  const seen = new Set();
  meta.mounts = meta.mounts.filter((m) => !seen.has(m.id) && seen.add(m.id));
  const groups = [
    ['Dialects', (m) => m.grammar !== 'path'],
    ['Simple providers', (m) => m.dialect.startsWith('simple:')],
    ['Other dialects', () => true],
  ].map(([label, test]) => ({ label, test, node: el('optgroup', { label }) }));
  for (const m of meta.mounts) {
    const where = m.prefix || (m.hosts ?? []).join(', ');
    const more = (m.prefixes?.length ?? 0) > 1 ? ` (+${m.prefixes.length - 1} prefixes)` : '';
    const g = groups.find((x) => x.test(m));
    g.node.append(el('option', { value: m.id, text: `${m.dialect} · ${where}${more}${m.id !== m.dialect ? ` (${m.id})` : ''}` }));
  }
  $('mount').replaceChildren(...groups.filter((g) => g.node.childElementCount).map((g) => g.node));
}

// mountPrefix is the prefix URLs of m are built on: the picked one when
// m has several.
function mountPrefix(m) {
  return m.prefixes?.includes(state.prefix) ? state.prefix : m.prefix || '';
}

function applyPrefixes(m) {
  const list = m.prefixes ?? [];
  $('prefix-row').hidden = list.length < 2;
  $('mount-prefix').replaceChildren(...list.map((p, i) => el('option', { value: p, text: i ? p : `${p} (primary)` })));
  if (!list.includes(state.prefix)) state.prefix = '';
  $('mount-prefix').value = mountPrefix(m);
}

function applyMount() {
  const m = currentMount();
  if (!m) return;
  $('mount').value = m.id;
  applyPrefixes(m);
  const signing = m.scheme === 'none'
    ? 'the playground cannot sign this dialect; URLs are unsigned'
    : m.keyed ? `signed with the ${m.scheme} scheme` : `${m.scheme} scheme, no key configured: unsigned`;
  const bound = m.source ? ` Keys resolve against source "${m.source}".` : '';
  $('mount-info').textContent = `Unsigned policy ${m.unsigned_policy}; ${signing}.${bound}`;
  const cat = catalogues.get(m.grammar);
  $('free-builder').hidden = Boolean(cat);
  $('imgproxy-extra').hidden = m.grammar !== 'imgproxy';
  $('awssih-extra').hidden = m.grammar !== 'awssih';
  $('option-search').disabled = !cat;
  if (!cat && !state.free.path) {
    state.free.path = m.example ? m.example.split('?')[0] : '/{src}';
    state.free.query = m.example?.includes('?') ? m.example.split('?').slice(1).join('?') : '';
  }
  $('free-path').value = state.free.path;
  $('free-query').value = state.free.query;
  $('use-alias').checked = state.alias;
  $('src-encoding').value = state.encoding;
  $('extension').value = state.ext;
  $('sih-edits').value = state.edits;
  builder.pipeline = 0;
  builder.render(m.grammar, cat);
  $('copy-ts').disabled = m.grammar !== 'imgproxy';
  savePrefs();
}

// ---- URL -------------------------------------------------------------------

// urlView colour-codes the URL: base, signature, options, source, query.
function urlView(full, m) {
  const box = $('url-view');
  box.replaceChildren();
  const base = baseURL();
  if (!full.startsWith(base)) { box.textContent = full; return; }
  let rest = full.slice(base.length);
  const [path, query] = [rest.split('?')[0], rest.includes('?') ? rest.slice(rest.indexOf('?') + 1) : ''];
  box.append(el('span', { class: 'u-base', text: base }));
  const prefix = (meta.path_prefix || '').replace(/\/+$/, '') + (mountPrefix(m) || '/').replace(/\/+$/, '');
  rest = path.slice(prefix.length);
  box.append(el('span', { class: 'u-prefix', text: prefix }));
  const segs = rest.split('/').slice(1);
  let i = 0;
  if (m.scheme === 'imgproxy' || m.scheme === 'thumbor') box.append('/', el('span', { class: 'u-sig', title: 'signature', text: segs[i++] ?? '' }));
  const srcAt = segs.findIndex((s, j) => j >= i && (s === 'plain' || /^https?:/i.test(decodeURIComponent(s))));
  for (; i < segs.length; i++) {
    const cls = srcAt >= 0 && i >= srcAt ? 'u-src' : segs[i].startsWith('tr:') || /[:=_]/.test(segs[i]) ? 'u-opt' : 'u-path';
    box.append('/', el('span', { class: cls, text: segs[i] }));
  }
  if (query) {
    box.append('?');
    query.split('&').forEach((kv, j) => {
      if (j) box.append('&');
      const [k] = kv.split('=');
      const cls = k === 'url' ? 'u-src' : ['s', 'xi_sig', 'sign', 'signature', 'token'].includes(k) ? 'u-sig' : 'u-opt';
      box.append(el('span', { class: cls, text: kv }));
    });
  }
}

// beforeRequest: the source without options, through the first structured
// mount.
function beforeRequest() {
  const src = state.source;
  if (!src) return null;
  const ip = meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
  if (ip) return { mount: ip.id, path: `/plain/${escapePlain(src)}`, query: '' };
  const q = meta.mounts.find((m) => m.grammar === 'query' && m.prefix);
  if (q) return { mount: q.id, path: '/', query: `url=${encodeURIComponent(src)}` };
  return null;
}

async function update({ render = $('auto-render').checked } = {}) {
  const my = ++seq;
  writeHash();
  sources?.show(state.source);
  const m = currentMount();
  const req = m ? build(m, state, catalogues.get(m.grammar)?.index ?? new Map()) : { error: 'No mount.' };
  if (m && !req.error && mountPrefix(m) !== (m.prefix || '')) req.prefix = mountPrefix(m);
  if (req.error) {
    $('url-out').value = '';
    $('url-view').replaceChildren();
    $('signed').textContent = '';
    lastSigned = null;
    setStatus(req.error);
    return;
  }
  try {
    const signed = await sign(req);
    if (my !== seq) return;
    lastSigned = { req, ...signed };
    const full = publicURL(signed.url);
    $('url-out').value = full;
    urlView(full, m);
    const badge = $('signed');
    badge.textContent = signed.signed ? `signed (${signed.scheme})` : 'unsigned';
    badge.className = `badge ${signed.signed ? 'ok' : 'warn'}`;
    setStatus('');
    translate.run(req);
    if (render) await renderPreview();
  } catch (err) {
    if (my !== seq) return;
    setStatus(err.message, true);
  }
}

async function renderPreview() {
  if (!lastSigned) return;
  const before = beforeRequest();
  let beforePath = null;
  if (before) {
    try { beforePath = (await sign(before)).url; } catch { beforePath = null; }
  }
  await preview.render(lastSigned.url, beforePath);
  pushHistory();
}

function pushHistory() {
  const url = $('url-out').value;
  if (!url || (preview.res?.status ?? 500) >= 400) return;
  const list = store.get(HISTORY, []).filter((h) => h.url !== url);
  list.unshift({ url, hash: location.hash, when: Date.now(), mount: state.mount, label: state.sourceLabel || state.source });
  store.set(HISTORY, list.slice(0, HISTORY_MAX));
  renderHistory();
}

function renderHistory() {
  const box = $('history');
  box.replaceChildren();
  const list = store.get(HISTORY, []);
  if (!list.length) box.append(el('li', { class: 'hint', text: 'Rendered URLs appear here (kept in this browser only).' }));
  for (const h of list) {
    const b = el('button', { type: 'button', class: 'link', text: h.url });
    b.addEventListener('click', () => { location.hash = h.hash; loadFromHash(); });
    box.append(el('li', {}, el('span', { class: 'hint', text: `${new Date(h.when).toLocaleString()} · ${h.mount} · ${h.label} ` }), b));
  }
}

function loadFromHash() {
  const s = readHash();
  if (!restore(s)) return false;
  if (!currentMount()) state.mount = meta.mounts[0]?.id ?? '';
  applyMount();
  preview.clearBefore();
  info.reset();
  update({ render: true });
  return true;
}

// ---- wiring ----------------------------------------------------------------

function setTheme(t) {
  if (t === 'auto') delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
  $('theme').value = t;
  store.set('xi-playground-theme', t);
}

function defaults() {
  state.pipelines = [[]];
  state.alias = false;
  state.encoding = 'plain';
  state.ext = '';
  state.free = { path: '', query: '' };
  state.edits = '';
  const first = meta.samples?.[0] ?? null;
  const gallery = meta.gallery?.find((g) => g.allowed && g.format === 'jpeg');
  state.source = first?.source ?? gallery?.url ?? '';
  state.sourceLabel = first?.label ?? gallery?.name ?? '';
}

async function main() {
  setTheme(store.get('xi-playground-theme', 'auto'));
  const prefs = store.get(PREFS, {});
  try {
    await loadMeta();
  } catch (err) {
    setStatus(`Cannot load api/meta: ${err.message}`, true);
    return;
  }
  renderMeta(prefs);
  const later = debounce(() => update(), 250);
  builder = new Builder({ state, onChange: later });
  preview = new Preview({ accept: () => $('accept').value });
  translate = new Translate({ publicURL, onPreview: (path) => preview.render(path, null) });
  info = new Info({ meta, sign, preview, getSource: () => state.source });
  matrix = new Matrix({ meta, sign });
  sources = new Sources({
    meta,
    onPick: (src, label) => {
      state.source = src;
      state.sourceLabel = label;
      preview.clearBefore();
      info.reset();
      update({ render: true });
    },
    thumb: async (src) => {
      const ip = meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
      if (!ip) return null;
      const s = await sign({ mount: ip.id, path: `/rs:fit:160:160/f:webp/plain/${escapePlain(src)}`, query: '' });
      const res = await fetch(`api/render?u=${encodeURIComponent(s.url)}`, { headers: { Accept: 'image/webp,*/*' } });
      if (!res.ok) return null;
      return URL.createObjectURL(await res.blob());
    },
  });

  if (!loadFromHash()) {
    defaults();
    state.mount = meta.mounts.some((m) => m.id === prefs.mount) ? prefs.mount : meta.mounts[0]?.id ?? '';
    applyMount();
    update({ render: true });
  }
  renderHistory();

  tabs('more-tabs', (id) => {
    translate.setVisible(id === 'mtab-translate');
    if (id === 'mtab-matrix') matrix.build();
    if (id === 'mtab-info' && !$('info-tree').childElementCount) info.run();
  });

  $('mount').addEventListener('change', (e) => {
    state.mount = e.target.value;
    state.prefix = '';
    state.pipelines = [[]];
    state.free = { path: '', query: '' };
    applyMount();
    update();
  });
  $('mount-prefix').addEventListener('change', (e) => {
    const m = currentMount();
    state.prefix = e.target.value === m?.prefix ? '' : e.target.value;
    update();
  });
  $('option-search').addEventListener('input', (e) => builder.applySearch(e.target.value));
  $('use-alias').addEventListener('change', (e) => { state.alias = e.target.checked; update(); });
  $('src-encoding').addEventListener('change', (e) => { state.encoding = e.target.value; update(); });
  $('extension').addEventListener('change', (e) => { state.ext = e.target.value; update(); });
  $('sih-edits').addEventListener('input', (e) => { state.edits = e.target.value; later(); });
  $('free-path').addEventListener('input', (e) => { state.free.path = e.target.value; later(); });
  $('free-query').addEventListener('input', (e) => { state.free.query = e.target.value; later(); });
  $('base-url').addEventListener('input', () => { savePrefs(); later(); });
  $('accept').addEventListener('change', () => { savePrefs(); preview.clearBefore(); renderPreview(); });
  $('render').addEventListener('click', () => { preview.clearBefore(); renderPreview(); });
  $('copy').addEventListener('click', () => copyText($('url-out').value, 'URL copied'));
  $('copy-curl').addEventListener('click', () => copyText(curlSnippet($('url-out').value, $('accept').value), 'curl command copied'));
  $('copy-ts').addEventListener('click', () => {
    const m = currentMount();
    const base = baseURL() + (meta.path_prefix || '').replace(/\/+$/, '') + (mountPrefix(m) || '/').replace(/\/+$/, '');
    const ts = tsSnippet(m, state, catalogues.get('imgproxy').index, base, $('snippet-signed').checked);
    if (ts) copyText(ts, 'TypeScript copied');
  });
  $('share').addEventListener('click', () => { writeHash(); setTimeout(() => copyText(location.href, 'Share link copied'), 350); });
  $('reset').addEventListener('click', () => {
    history.replaceState(null, '', location.pathname);
    defaults();
    applyMount();
    update({ render: true });
  });
  $('history-clear').addEventListener('click', () => { store.set(HISTORY, []); renderHistory(); });
  $('theme').addEventListener('change', (e) => setTheme(e.target.value));
  $('help-button').addEventListener('click', () => {
    const h = $('help');
    h.hidden = !h.hidden;
    $('help-button').setAttribute('aria-expanded', String(!h.hidden));
  });
  window.addEventListener('hashchange', () => loadFromHash());

  // Keyboard shortcuts.
  document.addEventListener('keydown', (e) => {
    const t = e.target;
    const typing = t instanceof HTMLElement && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName));
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      preview.clearBefore();
      renderPreview();
      return;
    }
    if (e.key === 'Enter' && typing && t.closest('.builder') && t.tagName !== 'TEXTAREA') {
      e.preventDefault();
      update({ render: true });
      return;
    }
    if (typing || e.ctrlKey || e.metaKey || e.altKey) return;
    switch (e.key) {
      case '/': e.preventDefault(); $('option-search').focus(); break;
      case 'c': copyText($('url-out').value, 'URL copied'); break;
      case 's': preview.toggleMode(); break;
      case '1': preview.setZoom('1'); break;
      case '2': preview.setZoom('2'); break;
      case '0': preview.setZoom('fit'); break;
      case '?': $('help-button').click(); break;
      default:
    }
  });
  toast('Ready.');
}

main();
