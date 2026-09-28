// xtandard-image playground v2 (WP-94). Plain ES2022 modules, no
// dependencies. Everything comes from api/meta; URLs are signed by
// api/sign on the server (keys never reach the browser) and previews go
// through api/render, so every request stays on this origin.

import { Builder } from './builder.js';
import { $, b64url, copyText, debounce, el, escapePlain, postJSON, store, tabs, toast, unb64url } from './dom.js';
import { build, curlSnippet, tsSnippet } from './grammar.js';
import { Frames } from './frames.js';
import { Info } from './info.js';
import { Matrix } from './matrix.js';
import { Preview } from './preview.js';
import { Examples } from './recipes.js';
import { Sources } from './sources.js';
import { History, toolFromHash, Tools } from './tools.js';
import { Translate } from './translate.js';

const PREFS = 'xi-playground';

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
  info: null, // Info panel entries {name, form, values}; null = recommended
  raw: false, // the path and query typed by hand (free) on a mount with a builder
};

let meta;
const catalogues = new Map(); // grammar → {options, index}
let builder;
let preview;
let sources;
let info;
let translate;
let matrix;
let tools;
let frames;
let hist;
let more;
let examples;
let lastSigned = null; // {req, url, signed, scheme}
let seq = 0;

// ---- state in the URL hash -------------------------------------------

function snapshot() {
  return {
    m: state.mount, s: state.source, l: state.sourceLabel, a: state.alias ? 1 : 0, e: state.encoding, x: state.ext,
    p: state.pipelines.map((p) => p.map((e) => [e.name, e.form, e.values, e.groups, e.on ? 1 : 0])),
    f: state.free, j: state.edits, ...(state.prefix ? { pf: state.prefix } : {}), ...(state.raw ? { r: 1 } : {}),
    ...(Array.isArray(state.info) ? { i: state.info.map((e) => [e.name, e.form ?? 0, e.values ?? []]) } : {}),
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
  state.raw = Boolean(s.r);
  state.info = Array.isArray(s.i)
    ? s.i.filter(Array.isArray).map(([name, form, values]) => ({ name: String(name), form: Number(form) || 0, values: Array.isArray(values) ? values.map(String) : [] }))
    : null;
  return true;
}

function readHash() {
  const h = location.hash.replace(/^#/, '');
  if (!h.startsWith('v1:')) return null;
  try { return JSON.parse(unb64url(h.slice(3))); } catch { return null; }
}

// sessionHash is the share-link hash of the session; while a tool is
// open the hash is the tool's route and the session waits in memory.
const sessionHash = () => `#v1:${b64url(JSON.stringify(snapshot()))}`;
const shareURL = (hash) => `${location.origin}${location.pathname}${location.search}${hash}`;

const writeHash = debounce(() => {
  if (toolFromHash()) return;
  history.replaceState(null, '', sessionHash());
  tools?.refreshNav();
}, 300);

// ---- helpers -------------------------------------------------------------

const currentMount = () => meta.mounts.find((m) => m.id === state.mount);
const baseURL = () => $('base-url').value.replace(/\/+$/, '');
const publicURL = (path) => baseURL() + path;
// sign asks the server to sign a URL. The PDF password field (WP-115)
// travels as pdf_password in the body, for the mounts that take it (signed
// imgproxy and query mounts); the server encrypts it into the URL. It is
// never written to the hash, the preferences or the history.
const sign = (body) => {
  const pw = meta?.pdf_passwords ? $('pdf-password').value : '';
  const m = meta?.mounts.find((x) => x.id === body.mount);
  const capable = m?.keyed && (m.scheme === 'imgproxy' || (m.scheme === 'xi_sig' && m.grammar === 'query'));
  return postJSON('api/sign', pw && capable ? { ...body, pdf_password: pw } : body);
};

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
  $('pdf-password-row').hidden = !meta.pdf_passwords;
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
  // Raw mode: the builder is set aside (its entries are kept) and the
  // path and query are typed by hand, on any mount.
  if (!cat) state.raw = false;
  const raw = state.raw;
  $('raw-row').hidden = !cat;
  $('raw-mode').checked = raw;
  document.querySelector('section.builder').classList.toggle('raw', raw);
  $('free-hint').firstChild.textContent = raw ? 'The path and query are written by hand (the builder keeps its options for when you switch back). ' : 'This dialect is built by hand. ';
  $('free-builder').hidden = Boolean(cat) && !raw;
  $('imgproxy-extra').hidden = m.grammar !== 'imgproxy' || raw;
  $('awssih-extra').hidden = m.grammar !== 'awssih' || raw;
  $('option-search').disabled = !cat || raw;
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
  frames?.render();
  $('copy-ts').disabled = m.grammar !== 'imgproxy' || raw;
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
  frames?.sync();
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
  preview.setSource(state.source);
  await preview.render(lastSigned.url, beforePath);
  pushHistory();
}

// probeSource asks the source's own /info (format, dimensions, size,
// colour space, bands, pages, alpha; no metadata) for the Source column
// of the preview's comparison and the page, frame and time selectors.
async function probeSource(src) {
  const ip = meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
  if (!ip) return null;
  const prefix = (meta.path_prefix || '').replace(/\/+$/, '') + (ip.prefix || '/').replace(/\/+$/, '');
  const s = await sign({ mount: ip.id, path: `/exif:0/iptc:0/xmp:0/vm:1/cs:1/b:1/pn:1/a:1/plain/${escapePlain(src)}`, query: '' });
  const res = await fetch(`api/render?u=${encodeURIComponent(`${prefix}/info${s.url.slice(prefix.length)}`)}`, { headers: { Accept: 'application/json' } });
  return res.ok ? res.json() : null;
}

// thumbnail draws the result into a small WebP data URL for the history
// (images only; nothing when the browser cannot).
async function thumbnail() {
  const img = $('after-media').firstElementChild;
  if (!(img instanceof HTMLImageElement)) return undefined;
  try {
    await img.decode();
    const k = Math.min(1, 96 / Math.max(img.naturalWidth, img.naturalHeight));
    const c = el('canvas', { width: Math.max(1, Math.round(img.naturalWidth * k)), height: Math.max(1, Math.round(img.naturalHeight * k)) });
    c.getContext('2d').drawImage(img, 0, 0, c.width, c.height);
    const url = c.toDataURL('image/webp', 0.7);
    return url.length < 16000 ? url : undefined;
  } catch {
    return undefined;
  }
}

async function pushHistory() {
  const url = $('url-out').value;
  if (!url || (preview.res?.status ?? 500) >= 400) return;
  const entry = { url, hash: sessionHash(), when: Date.now(), mount: state.mount, label: state.sourceLabel || state.source };
  hist.push({ ...entry, thumb: await thumbnail() });
}

// route follows the hash: a tool route opens that tool over the session;
// a session hash closes the tools and, when it is another session than
// the one in memory, loads it.
function route() {
  const tool = toolFromHash();
  if (tool) {
    if (tools.current !== tool) tools.open(tool);
    return;
  }
  const same = location.hash === sessionHash();
  if (tools.current) tools.close({ writeHash: false });
  if (!same) loadFromHash();
}

function loadFromHash() {
  const s = readHash();
  if (!restore(s)) return false;
  if (!currentMount()) state.mount = meta.mounts[0]?.id ?? '';
  applyMount();
  preview.clearBefore();
  info.reset();
  info.restore(state.info);
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
  state.raw = false;
  const first = meta.samples?.[0] ?? null;
  const gallery = meta.gallery?.find((g) => g.allowed && g.format === 'jpeg');
  state.source = first?.source ?? gallery?.url ?? '';
  state.sourceLabel = first?.label ?? gallery?.name ?? '';
  state.info = null;
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
  const imgproxyMount = meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
  frames = new Frames({
    state,
    grammar: () => (state.raw ? 'path' : currentMount()?.grammar ?? ''),
    index: () => catalogues.get(currentMount()?.grammar)?.index ?? null,
    onChange: () => update({ render: true }),
    afterEdit: () => builder.render(builder.grammar, builder.cat),
    thumb: async (page) => {
      if (!imgproxyMount) return null;
      const s = await sign({ mount: imgproxyMount.id, path: `/page:${page}/rs:fit:96:96/f:webp/plain/${escapePlain(state.source)}`, query: '' });
      const res = await fetch(`api/render?u=${encodeURIComponent(s.url)}`, { headers: { Accept: 'image/webp,*/*' } });
      return res.ok ? URL.createObjectURL(await res.blob()) : null;
    },
  });
  preview = new Preview({
    accept: () => $('accept').value,
    probe: (src) => {
      frames.setInfo(src, null);
      return probeSource(src).then((i) => { frames.setInfo(src, i); return i; });
    },
  });
  preview.onResponse = (h) => frames.setResponse(h);
  translate = new Translate({ publicURL, onPreview: (path) => preview.render(path, null) });
  info = new Info({ meta, sign, preview, state, baseURL, onChange: writeHash });
  matrix = new Matrix({ meta, sign });
  tools = new Tools({
    sessionHash,
    onOpen: (name) => {
      if (name === 'matrix') matrix.build();
      if (name === 'history') hist.render();
      if (name === 'examples' && !$('examples').childElementCount) examples.renderTool();
    },
  });
  hist = new History({
    shareURL,
    open: (h) => {
      if (h.hash === sessionHash()) tools.close();
      else location.hash = h.hash; // route() loads it and closes the tool
    },
  });
  sources = new Sources({
    meta,
    onPick: (src, label) => {
      state.source = src;
      state.sourceLabel = label;
      preview.clearBefore();
      info.reset();
      update({ render: true });
      info.refresh();
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

  // The Gallery tool: the same tiles as the Source panel; a pick loads the
  // sample and returns to the session.
  sources.renderGallery($('gallery-big'), $('gallery-filter'), (e) => {
    tools.close();
    sources.onPick(e.source, e.name);
  });
  sources.show(state.source);

  // Examples (WP-116): a recipe loads as a session hash, so it renders,
  // stays editable, shares and goes back like any session.
  examples = new Examples({
    meta,
    index: (g) => catalogues.get(g)?.index ?? null,
    load: (r) => {
      const h = `#v1:${b64url(JSON.stringify(examples.snapshot(r)))}`;
      if (location.hash === h) {
        tools.close();
        return;
      }
      // The Info tab opens once route() has restored the session.
      if (r.info?.length) window.addEventListener('hashchange', () => more.select('mtab-info'), { once: true });
      location.hash = h;
      toast(`Example loaded: ${r.title}.`);
    },
    thumb: async (r, st) => {
      const m = meta.mounts.find((x) => x.id === st.mount);
      const req = m ? build(m, st, catalogues.get(m.grammar)?.index ?? new Map()) : { error: 'no mount' };
      if (req.error) return null;
      const s = await sign(req);
      const res = await fetch(`api/render?u=${encodeURIComponent(s.url)}`, { headers: { Accept: 'image/avif,image/webp,image/*;q=0.8' } });
      const type = res.headers.get('Content-Type') ?? '';
      if (!res.ok || !type.startsWith('image/') || type.includes('svg')) return null;
      return URL.createObjectURL(await res.blob());
    },
  });
  examples.renderQuick($('examples-quick'));

  if (!loadFromHash()) {
    defaults();
    state.mount = meta.mounts.some((m) => m.id === prefs.mount) ? prefs.mount : meta.mounts[0]?.id ?? '';
    applyMount();
    info.restore(null);
    update({ render: true });
  }
  if (toolFromHash()) tools.open(toolFromHash());
  tools.refreshNav();

  more = tabs('more-tabs', (id) => {
    translate.setVisible(id === 'mtab-translate');
    if (id === 'mtab-info' && !$('info-result').childElementCount) info.run();
  });
  // Cross-links between the source's Info and the result's details.
  $('goto-result').addEventListener('click', () => {
    $('result-details').scrollIntoView({ block: 'start' });
    $('result-details-title').focus({ preventScroll: true });
  });
  $('goto-source-info').addEventListener('click', () => {
    more.select('mtab-info');
    $('mpane-info').scrollIntoView({ block: 'start' });
    $('info-caption').focus({ preventScroll: true });
  });

  $('mount').addEventListener('change', (e) => {
    state.mount = e.target.value;
    state.prefix = '';
    state.pipelines = [[]];
    state.free = { path: '', query: '' };
    state.raw = false;
    applyMount();
    update();
  });
  $('mount-prefix').addEventListener('change', (e) => {
    const m = currentMount();
    state.prefix = e.target.value === m?.prefix ? '' : e.target.value;
    update();
  });
  $('option-search').addEventListener('input', (e) => builder.applySearch(e.target.value));
  // Raw mode starts from the URL the builder writes, with the source as
  // {src}.
  $('raw-mode').addEventListener('change', (e) => {
    const m = currentMount();
    if (e.target.checked && m) {
      const req = build(m, { ...state, raw: false }, catalogues.get(m.grammar)?.index ?? new Map());
      if (!req.error) {
        state.free = {
          path: req.path.split(escapePlain(state.source)).join('{src}'),
          query: req.query.split(encodeURIComponent(state.source)).join('{src}'),
        };
      }
    }
    state.raw = e.target.checked;
    applyMount();
    update();
  });
  $('use-alias').addEventListener('change', (e) => { state.alias = e.target.checked; update(); });
  $('src-encoding').addEventListener('change', (e) => { state.encoding = e.target.value; update(); });
  $('extension').addEventListener('change', (e) => { state.ext = e.target.value; update(); });
  $('pdf-password').addEventListener('change', () => update());
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
  $('share').addEventListener('click', () => copyText(shareURL(sessionHash()), 'Share link copied'));
  $('reset').addEventListener('click', () => {
    tools.close({ writeHash: false });
    history.replaceState(null, '', location.pathname);
    defaults();
    applyMount();
    info.reset();
    info.restore(null);
    update({ render: true });
  });
  $('theme').addEventListener('change', (e) => setTheme(e.target.value));
  $('help-button').addEventListener('click', () => {
    const h = $('help');
    h.hidden = !h.hidden;
    $('help-button').setAttribute('aria-expanded', String(!h.hidden));
  });
  window.addEventListener('hashchange', route);

  // Keyboard shortcuts.
  document.addEventListener('keydown', (e) => {
    const t = e.target;
    const typing = t instanceof HTMLElement && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName));
    // In a tool the session's shortcuts are off: Esc goes back to it.
    if (tools.current) {
      if (e.key === 'Escape' && !e.defaultPrevented) { e.preventDefault(); tools.close(); }
      else if (e.key === '?' && !typing) $('help-button').click();
      return;
    }
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
