// xtandard-image playground (WP-80). Plain ES2022 module, no dependencies.
// Everything it needs comes from api/meta; URLs are signed by api/sign on
// the server (keys never reach the browser) and previews go through
// api/render, so every request stays on this origin.

const SAMPLES = [
  ['JPEG photo (WebP gallery)', 'https://www.gstatic.com/webp/gallery/1.jpg'],
  ['PNG with transparency', 'https://upload.wikimedia.org/wikipedia/commons/4/47/PNG_transparency_demonstration_1.png'],
  ['Animated GIF', 'https://upload.wikimedia.org/wikipedia/commons/2/2c/Rotating_earth_%28large%29.gif'],
  ['SVG logo', 'https://upload.wikimedia.org/wikipedia/commons/0/02/SVG_logo.svg'],
];

const STORAGE_KEY = 'xi-playground';
const $ = (id) => document.getElementById(id);

const state = {
  meta: null,
  catalogues: new Map(), // grammar → Map(name or alias → option)
  selected: [],          // [{option, form, groups, el}]
  seq: 0,                // request sequence, drops stale responses
  ids: 0,                 // element id counter
  lastBefore: '',
  objectURLs: { before: '', after: '' },
};

// ---- persistence (per-browser convenience only) ---------------------------

function loadPrefs() {
  try { return JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}'); } catch { return {}; }
}

function savePrefs() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({
      base: $('base-url').value, accept: $('accept').value, mount: $('mount').value,
    }));
  } catch { /* storage unavailable */ }
}

// ---- helpers ---------------------------------------------------------------

function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k === 'text') e.textContent = v;
    else if (v === true) e.setAttribute(k, '');
    else e.setAttribute(k, String(v));
  }
  e.append(...children);
  return e;
}

function setStatus(msg, isError = false) {
  const s = $('status');
  s.textContent = msg;
  s.classList.toggle('error', isError);
}

function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

function base64url(s) {
  const bytes = new TextEncoder().encode(s);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

// plainSource escapes a source for imgproxy's plain/ form: everything that
// would end the path segment or start the @extension, keeping : and /.
function plainSource(src) {
  return encodeURIComponent(src).replaceAll('%3A', ':').replaceAll('%2F', '/');
}

function currentMount() {
  return state.meta.mounts.find((m) => m.id === $('mount').value);
}

function currentSource() {
  const kind = $('source-kind').value;
  const input = $('source-input').value.trim();
  if (kind === 'sample') return $('sample').value;
  if (kind.startsWith('xi:')) return input ? `xi://${kind.slice(3)}/${input.replace(/^\/+/, '')}` : '';
  return input;
}

// ---- meta ------------------------------------------------------------------

async function loadMeta() {
  const res = await fetch('api/meta', { headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`api/meta answered ${res.status}`);
  const meta = await res.json();
  state.meta = meta;
  for (const cat of meta.catalogues ?? []) {
    const index = new Map();
    for (const o of cat.options) {
      index.set(o.name, o);
      for (const a of o.aliases ?? []) if (!index.has(a)) index.set(a, o);
    }
    state.catalogues.set(cat.grammar, { options: cat.options, index });
  }
  return meta;
}

function renderMeta(meta, prefs) {
  $('version').textContent = `version ${meta.version || 'dev'}`;
  const mode = $('signing-mode');
  mode.textContent = `${meta.signing.mode}, unsigned policy ${meta.signing.unsigned_policy}`;
  mode.classList.add(meta.signing.mode === 'off' ? 'warn' : 'ok');

  const port = (meta.public_addr || ':8080').split(':').pop() || '8080';
  $('base-url').value = prefs.base || (meta.public ? location.origin : `${location.protocol}//${location.hostname}:${port}`);
  if (prefs.accept) $('accept').value = prefs.accept;

  const kind = $('source-kind');
  kind.append(el('option', { value: 'url', text: 'URL' }), el('option', { value: 'sample', text: 'Sample image' }));
  for (const s of meta.sources ?? []) {
    kind.append(el('option', { value: `xi:${s.name}`, text: `Named source ${s.name} (${s.type}): xi://${s.name}/…` }));
  }
  for (const [label, url] of SAMPLES) $('sample').append(el('option', { value: url, text: label }));
  kind.value = 'sample';

  const mount = $('mount');
  for (const m of meta.mounts) {
    const where = m.prefix || (m.hosts ?? []).join(', ');
    mount.append(el('option', { value: m.id, text: `${m.id} (${m.dialect}) ${where}` }));
  }
  if (prefs.mount && meta.mounts.some((m) => m.id === prefs.mount)) mount.value = prefs.mount;

  const ext = $('extension');
  for (const f of meta.formats ?? []) ext.append(el('option', { value: f, text: f }));
  updateSourceKind();
  updateMount();
}

// ---- source ----------------------------------------------------------------

function updateSourceKind() {
  const kind = $('source-kind').value;
  $('sample-row').hidden = kind !== 'sample';
  const input = $('source-input');
  input.hidden = kind === 'sample';
  $('source-label').hidden = kind === 'sample';
  if (kind === 'url') {
    $('source-label').textContent = 'Source URL';
    input.placeholder = 'https://example.com/photo.jpg';
  } else if (kind.startsWith('xi:')) {
    $('source-label').textContent = `Key in source ${kind.slice(3)}`;
    input.placeholder = 'photos/a.jpg';
  }
}

// ---- mounts and the option builder -----------------------------------------

function updateMount() {
  const m = currentMount();
  if (!m) return;
  const signing = m.scheme === 'none'
    ? 'the playground cannot sign this dialect; URLs are returned unsigned'
    : m.keyed ? `signed with the ${m.scheme} scheme` : `${m.scheme} scheme, no key configured: unsigned`;
  $('mount-info').textContent = `Unsigned policy ${m.unsigned_policy}; ${signing}.`;
  const cat = state.catalogues.get(m.grammar);
  $('catalogue-builder').hidden = !cat;
  $('free-builder').hidden = Boolean(cat);
  $('imgproxy-extra').hidden = m.grammar !== 'imgproxy';
  if (!cat) {
    $('free-path').value = m.example ? m.example.split('?')[0] : '/{src}';
    $('free-query').value = m.example?.includes('?') ? m.example.split('?').slice(1).join('?') : '';
  }
  // The selected options belong to the previous grammar.
  for (const s of state.selected) s.el.remove();
  state.selected = [];
  const list = $('option-list');
  list.replaceChildren();
  for (const o of cat?.options ?? []) {
    list.append(el('option', { value: o.name, label: [o.name, ...(o.aliases ?? [])].join(', ') + (o.doc ? ` · ${o.doc}` : '') }));
  }
  savePrefs();
}

function addOption(name) {
  const m = currentMount();
  const cat = state.catalogues.get(m?.grammar);
  const option = cat?.index.get(name.trim());
  if (!option) {
    $('option-hint').textContent = name.trim() ? `Unknown option "${name.trim()}".` : 'Type an option name.';
    return;
  }
  $('option-hint').textContent = '';
  const node = $('option-template').content.firstElementChild.cloneNode(true);
  node.querySelector('.option-name').textContent = option.name;
  node.querySelector('.option-aliases').textContent = option.aliases?.length ? `aliases: ${option.aliases.join(', ')}` : '';
  node.querySelector('.option-doc').textContent = [option.doc, option.milestone && `(${option.milestone})`].filter(Boolean).join(' ');
  const entry = { option, form: 0, groups: 1, el: node };
  node.querySelector('.remove').setAttribute('aria-label', `Remove ${option.name}`);
  node.querySelector('.remove').addEventListener('click', () => {
    node.remove();
    state.selected = state.selected.filter((s) => s !== entry);
    $('option-search').focus();
    update();
  });
  if (option.forms.length > 1) {
    const id = `form-${++state.ids}`;
    const sel = el('select', { id });
    option.forms.forEach((f, i) => sel.append(el('option', { value: i, text: f.args.map((a) => a.name).join(', ') || '(no arguments)' })));
    sel.addEventListener('change', () => { entry.form = Number(sel.value); entry.groups = 1; renderArgs(entry); update(); });
    node.querySelector('.form-choice').append(el('label', { for: id, text: 'Form' }), sel);
  }
  state.selected.push(entry);
  $('options').append(node);
  renderArgs(entry);
  node.querySelector('.args input, .args select')?.focus();
  $('option-search').value = '';
  update();
}

function argInput(entry, arg, id) {
  let input;
  switch (arg.type) {
    case 'boolean':
      input = el('select', { id });
      for (const [v, t] of [['', '(unset)'], ['1', 'true'], ['0', 'false']]) input.append(el('option', { value: v, text: t }));
      break;
    case 'enum':
      input = el('select', { id });
      input.append(el('option', { value: '', text: '(unset)' }));
      for (const v of arg.enum ?? []) input.append(el('option', { value: v, text: v }));
      break;
    case 'integer':
    case 'number':
      input = el('input', { id, type: 'number', step: arg.type === 'integer' ? 1 : 'any', min: arg.min, max: arg.max, inputmode: 'decimal' });
      break;
    default:
      input = el('input', { id, type: 'text', autocomplete: 'off', spellcheck: 'false', pattern: arg.pattern,
        placeholder: arg.type === 'color' ? 'ff0000' : '' });
      if (entry.option.name === 'preset' && state.meta.presets?.length) input.setAttribute('list', 'preset-list');
  }
  if (arg.doc) input.title = arg.doc;
  input.addEventListener('input', update);
  input.addEventListener('change', update);
  return input;
}

function renderArgs(entry) {
  const box = entry.el.querySelector('.args');
  box.replaceChildren();
  const form = entry.option.forms[entry.form] ?? { args: [] };
  const add = (arg) => {
    const id = `arg-${++state.ids}`;
    const bounds = [arg.min !== undefined && `≥ ${arg.min}`, arg.max !== undefined && `≤ ${arg.max}`].filter(Boolean).join(' ');
    const label = el('label', { for: id, text: `${arg.name}${bounds ? ` (${bounds})` : ''}${arg.optional ? ' (optional)' : ''}` });
    const wrap = el('div', {}, label, argInput(entry, arg, id));
    box.append(wrap);
  };
  form.args.forEach(add);
  if (form.rest?.length) {
    for (let g = 0; g < entry.groups; g++) {
      box.append(el('div', { class: 'group-sep' }));
      form.rest.forEach(add);
    }
    const more = el('button', { type: 'button', text: `Add ${form.rest.map((a) => a.name).join('/')}` });
    more.addEventListener('click', () => { entry.groups++; renderArgs(entry); update(); });
    box.append(el('div', {}, more));
  }
}

function optionValues(entry) {
  const vals = [...entry.el.querySelectorAll('.args input, .args select')].map((i) => i.value.trim());
  while (vals.length > 1 && vals.at(-1) === '') vals.pop();
  return vals;
}

// ---- URL building ----------------------------------------------------------

// buildRequest returns the sign request body for the current state, or null
// when there is nothing to sign yet.
function buildRequest() {
  const m = currentMount();
  const src = currentSource();
  if (!m) return null;
  if (m.grammar === 'imgproxy') {
    if (!src) return null;
    const sep = m.arguments_separator || ':';
    const short = $('use-alias').checked;
    const segs = state.selected.map((s) => {
      const name = short && s.option.aliases?.length ? s.option.aliases[0] : s.option.name;
      const vals = optionValues(s);
      return vals.length === 0 ? name : [name, ...vals].join(sep);
    });
    const ext = $('extension').value;
    const source = $('src-encoding').value === 'base64'
      ? `/${base64url(src)}${ext ? `.${ext}` : ''}`
      : `/plain/${plainSource(src)}${ext ? `@${ext}` : ''}`;
    return { mount: m.id, path: (segs.length ? `/${segs.join('/')}` : '') + source, query: '' };
  }
  if (m.grammar === 'query') {
    if (!src) return null;
    const params = [`url=${encodeURIComponent(src)}`];
    for (const s of state.selected) {
      const v = optionValues(s).join(',');
      params.push(`${encodeURIComponent(s.option.name)}=${encodeURIComponent(v)}`);
    }
    return { mount: m.id, path: '/', query: params.join('&') };
  }
  const fill = (s) => s.replaceAll('{src}', encodeURI(src));
  const path = fill($('free-path').value.trim() || '/');
  return { mount: m.id, path: path.startsWith('/') ? path : `/${path}`, query: fill($('free-query').value.trim()) };
}

// beforeRequest is the source without options, through the first mount
// that has a structured grammar.
function beforeRequest() {
  const src = currentSource();
  if (!src) return null;
  const ip = state.meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
  if (ip) return { mount: ip.id, path: `/plain/${plainSource(src)}`, query: '' };
  const q = state.meta.mounts.find((m) => m.grammar === 'query' && m.prefix);
  if (q) return { mount: q.id, path: '/', query: `url=${encodeURIComponent(src)}` };
  return null;
}

async function signRequest(body) {
  const res = await fetch('api/sign', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `api/sign answered ${res.status}`);
  return data;
}

function publicURL(path) {
  return $('base-url').value.replace(/\/+$/, '') + path;
}

async function update() {
  const seq = ++state.seq;
  const body = buildRequest();
  if (!body) {
    $('url-out').value = '';
    $('signed').textContent = '';
    setStatus('Choose a source to build a URL.');
    return;
  }
  try {
    const signed = await signRequest(body);
    if (seq !== state.seq) return;
    $('url-out').value = publicURL(signed.url);
    $('url-out').dataset.path = signed.url;
    const badge = $('signed');
    badge.textContent = signed.signed ? `signed (${signed.scheme})` : 'unsigned';
    badge.className = `badge ${signed.signed ? 'ok' : 'warn'}`;
    setStatus('');
    if ($('auto-render').checked) renderPreview();
  } catch (err) {
    if (seq !== state.seq) return;
    setStatus(err.message, true);
  }
}

// ---- preview ---------------------------------------------------------------

const SHOWN_HEADERS = ['content-type', 'content-length', 'cache-control', 'etag', 'vary', 'content-disposition', 'server-timing'];

async function renderInto(which, path) {
  const img = $(`${which}-img`);
  const error = $(`${which}-error`);
  const table = $(`${which}-headers`);
  const started = performance.now();
  let res;
  try {
    res = await fetch(`api/render?u=${encodeURIComponent(path)}`, { headers: { Accept: $('accept').value } });
  } catch (err) {
    error.hidden = false;
    error.textContent = `Request failed: ${err.message}`;
    return;
  }
  const blob = await res.blob();
  const elapsed = performance.now() - started;
  const rows = [['status', `${res.status} ${res.statusText}`], ['size', `${blob.size.toLocaleString()} bytes`],
    ['time (browser)', `${elapsed.toFixed(1)} ms`]];
  for (const [k, v] of res.headers) {
    if (SHOWN_HEADERS.includes(k) || k.startsWith('x-xi-') || k === 'x-request-id') rows.push([k, v]);
  }
  table.replaceChildren(...rows.map(([k, v]) => el('tr', {}, el('th', { scope: 'row', text: k }), el('td', { text: v }))));
  if (state.objectURLs[which]) URL.revokeObjectURL(state.objectURLs[which]);
  state.objectURLs[which] = '';
  const type = res.headers.get('content-type') || '';
  if (!res.ok || !type.startsWith('image/')) {
    img.removeAttribute('src');
    error.hidden = false;
    const text = await blob.text().catch(() => '');
    let msg = text;
    try { msg = JSON.parse(text).error?.message ?? JSON.parse(text).error ?? text; } catch { /* plain text */ }
    error.textContent = res.ok ? `Not an image (${type || 'no content type'}).` : `${res.status}: ${String(msg).slice(0, 300)}`;
    return;
  }
  error.hidden = true;
  state.objectURLs[which] = URL.createObjectURL(blob);
  img.onload = () => {
    table.append(el('tr', {}, el('th', { scope: 'row', text: 'dimensions' }), el('td', { text: `${img.naturalWidth} × ${img.naturalHeight}` })));
  };
  img.src = state.objectURLs[which];
}

async function renderPreview() {
  const path = $('url-out').dataset.path;
  if (!path) return;
  const before = beforeRequest();
  const key = before ? JSON.stringify(before) + $('accept').value : '';
  const jobs = [renderInto('after', path)];
  if (before && key !== state.lastBefore) {
    state.lastBefore = key;
    jobs.push(signRequest(before).then((s) => renderInto('before', s.url)).catch((err) => {
      $('before-error').hidden = false;
      $('before-error').textContent = err.message;
    }));
  } else if (!before) {
    $('before-error').hidden = false;
    $('before-error').textContent = 'No imgproxy or query mount to show the source through.';
  }
  await Promise.all(jobs);
}

// ---- copy ------------------------------------------------------------------

async function copyURL() {
  const out = $('url-out');
  if (!out.value) return;
  try {
    await navigator.clipboard.writeText(out.value);
  } catch {
    out.select();
    document.execCommand('copy');
  }
  setStatus('URL copied.');
}

// ---- wiring ----------------------------------------------------------------

async function main() {
  const prefs = loadPrefs();
  let meta;
  try {
    meta = await loadMeta();
  } catch (err) {
    setStatus(`Cannot load api/meta: ${err.message}`, true);
    return;
  }
  if (meta.presets?.length) {
    const dl = el('datalist', { id: 'preset-list' });
    for (const p of meta.presets) dl.append(el('option', { value: p }));
    document.body.append(dl);
  }
  renderMeta(meta, prefs);
  const later = debounce(update, 250);
  $('source-kind').addEventListener('change', () => { updateSourceKind(); update(); });
  for (const id of ['sample', 'src-encoding', 'extension', 'use-alias']) $(id).addEventListener('change', update);
  for (const id of ['source-input', 'free-path', 'free-query']) $(id).addEventListener('input', later);
  $('base-url').addEventListener('input', () => { savePrefs(); later(); });
  $('accept').addEventListener('change', () => { savePrefs(); renderPreview(); });
  $('mount').addEventListener('change', () => { updateMount(); update(); });
  $('add-option').addEventListener('click', () => addOption($('option-search').value));
  $('option-search').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); addOption(e.target.value); }
  });
  $('copy').addEventListener('click', copyURL);
  $('render').addEventListener('click', () => { state.lastBefore = ''; renderPreview(); });
  update();
}

main();
