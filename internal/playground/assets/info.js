// The Info panel (WP-109): an option builder over the imgproxy /info
// catalogue (api/meta info_options), the live /info URL with snippets, and
// the result rendered for people: a summary card, the BlurHash and
// ThumbHash decoded next to a real thumbnail, colour swatches, hashes,
// searchable metadata tables, video metadata, the crop and detections over
// the preview, and badges for options this server ignored or refused.
//
// Requests go through api/render like the preview. When a request fails
// the panel asks for each option alone to find the ones the server
// refuses (detection without models, security options without
// XI_SECURITY_ALLOW_SECURITY_OPTIONS, a wrong hashsum...), badges them
// with the server's answer and loads the rest; ignored options come from
// the server's X-Xi-Ignored header, which api/render requests always get.

import { $, copyText, debounce, el, errorText, escapePlain, fmtBytes, svg, uid } from './dom.js';
import { activeEntries, curlInfoSnippet, tsInfoSnippet } from './grammar.js';
import { blurhashSize, decodeBlurhash, decodeThumbhash, paint } from './placeholder.js';

const GROUPS = [
  ['report', 'Size, format and pixels'],
  ['metadata', 'Metadata: EXIF, IPTC, XMP, video'],
  ['color', 'Colours'],
  ['hashes', 'Placeholders and hashes'],
  ['detection', 'Crop and detection'],
  ['frames', 'Pages and video frames'],
  ['delivery', 'Delivery and cache'],
  ['presets', 'Presets'],
  ['security', 'Security limits'],
  ['other', 'Other'],
];

// The "Recommended" preset: cheap options that show every renderer but
// detection (added when the models are loaded).
const RECOMMENDED = ['colorspace', 'bands', 'sample_format', 'pages_number', 'alpha', 'average', 'dominant_colors',
  'palette', 'blurhash', 'thumbhash', 'perceptual_hash', 'calc_hashsums'];

// "Select all" values that differ from the catalogue example: the example
// file size limit (10 bytes) would refuse every source.
const SELECT_ALL_VALUES = { max_src_file_size: ['104857600'], max_src_resolution: ['100'] };

const ORIENTATION = ['', 'normal', 'mirrored', 'rotated 180°', 'flipped', 'mirrored, rotated 270°', 'rotated 90°',
  'mirrored, rotated 90°', 'rotated 270°'];

const DOMINANT = [['vibrant', 'Vibrant'], ['muted', 'Muted'], ['dark_vibrant', 'Dark vibrant'], ['dark_muted', 'Dark muted'],
  ['light_vibrant', 'Light vibrant'], ['light_muted', 'Light muted']];

const hex2 = (n) => Number(n).toString(16).padStart(2, '0');
const hexOf = (c) => `#${hex2(c.R)}${hex2(c.G)}${hex2(c.B)}${c.A !== undefined && c.A < 255 ? hex2(c.A) : ''}`;
const rgbaOf = (c) => (c.A === undefined ? `rgb(${c.R}, ${c.G}, ${c.B})` : `rgba(${c.R}, ${c.G}, ${c.B}, ${+(c.A / 255).toFixed(3)})`);
const pct = (v) => `${(v * 100).toFixed(1)}%`;

// restGroups is how many rest groups the values of an entry fill.
function restGroups(o, e) {
  const f = o.forms[e.form ?? 0] ?? o.forms[0];
  const rest = f.rest?.length ?? 0;
  if (!rest) return 1;
  return Math.max(1, Math.ceil(Math.max(0, (e.values?.length ?? 0) - (f.args?.length ?? 0)) / rest));
}

// formFor picks the form whose keyword and enum arguments accept values.
function formFor(o, values) {
  if (o.forms.length < 2) return 0;
  const i = o.forms.findIndex((f) => (f.args ?? []).every((a, j) => {
    const v = values[j];
    if (a.enum?.length === 1) return v === a.enum[0];
    return v === undefined || v === '' || !a.enum || a.enum.includes(v);
  }));
  return i < 0 ? 0 : i;
}

// exampleEntry is o set to its catalogue example (every boolean on for
// "select all" when all is set).
function exampleEntry(o, all = false) {
  let values = (o.example ?? '').split(':').filter((v, i, a) => v !== '' || i < a.length - 1);
  const form = formFor(o, values);
  const f = o.forms[form];
  if (all) {
    const args = f.args ?? [];
    values = args.map((a, i) => (a.type === 'boolean' ? '1' : values[i] ?? ''));
    if (!args.length && f.rest?.length === 1 && f.rest[0].enum) values = [...f.rest[0].enum];
    else if (f.rest?.length) values.push(...(o.example ?? '').split(':').slice(args.length));
    if (SELECT_ALL_VALUES[o.name]) values = [...SELECT_ALL_VALUES[o.name]];
  }
  while (values.length && values.at(-1) === '') values.pop();
  return { name: o.name, form, values };
}

// parseIgnored reads X-Xi-Ignored: "option (reason), option:arg (reason)".
export function parseIgnored(header) {
  const out = [];
  if (!header) return out;
  const re = /\s*([^,(]+?)\s*\(((?:[^()]|\([^()]*\))*)\)\s*(?:,|$)/g;
  for (const m of header.matchAll(re)) out.push({ option: m[1], reason: m[2] });
  return out;
}

// table renders rows [key, value] with a filter box (metadata sections).
function searchableTable(title, rows, open = false) {
  const id = uid('meta');
  const tbody = el('tbody');
  const fill = (q) => {
    tbody.replaceChildren();
    const needle = q.trim().toLowerCase();
    let shown = 0;
    for (const [k, v] of rows) {
      if (needle && !`${k} ${v}`.toLowerCase().includes(needle)) continue;
      shown++;
      tbody.append(el('tr', {}, el('th', { scope: 'row', text: k }), el('td', { text: v })));
    }
    if (!shown) tbody.append(el('tr', {}, el('td', { colspan: '2', class: 'hint', text: rows.length ? 'No match.' : 'None in this source.' })));
  };
  const search = el('input', { type: 'search', id, placeholder: `Filter ${rows.length} entries`, autocomplete: 'off', spellcheck: 'false' });
  search.addEventListener('input', () => fill(search.value));
  fill('');
  const det = el('details', { class: 'meta-table', open },
    el('summary', {}, el('span', { text: title }), el('span', { class: 'count', text: String(rows.length) })),
    rows.length > 6 ? el('div', {}, el('label', { for: id, class: 'sr', text: `Filter ${title}` }), search) : null,
    el('div', { class: 'table-wrap' }, el('table', { class: 'headers kv', 'aria-label': title }, tbody)));
  return det;
}

const str = (v) => (typeof v === 'string' ? v : JSON.stringify(v));

// tree renders a JSON value as nested <details> (the raw response).
export function tree(value, key = null, depth = 0) {
  const label = key === null ? null : el('span', { class: 'k', text: `${key}: ` });
  if (value !== null && typeof value === 'object') {
    const entries = Array.isArray(value) ? value.map((v, i) => [i, v]) : Object.entries(value);
    const summary = el('summary', { role: 'treeitem' }, label,
      el('span', { class: 'hint', text: Array.isArray(value) ? `[${entries.length}]` : `{${entries.length}}` }));
    const d = el('details', { open: depth < 1 || entries.length <= 6 }, summary);
    const body = el('div', { class: 'tree-body', role: 'group' });
    for (const [k, v] of entries) body.append(tree(v, k, depth + 1));
    d.append(body);
    return d;
  }
  const cls = typeof value === 'string' ? 's' : typeof value === 'number' ? 'n' : 'b';
  return el('div', { class: 'leaf', role: 'treeitem' }, label, el('span', { class: cls, text: JSON.stringify(value) }));
}

const copyBtn = (text, what, label = 'Copy') => {
  const b = el('button', { type: 'button', class: 'small', text: label, 'aria-label': `${label} ${what}` });
  b.addEventListener('click', () => copyText(text, `${what} copied`));
  return b;
};

export class Info {
  // state.info holds the entries {name, form, values} of the options in
  // the URL; onChange saves the share link.
  constructor({ meta, sign, preview, state, baseURL, onChange }) {
    this.meta = meta;
    this.sign = sign;
    this.preview = preview;
    this.state = state;
    this.baseURL = baseURL;
    this.onChange = onChange;
    this.options = meta.info_options ?? [];
    this.index = new Map();
    for (const o of this.options) {
      this.index.set(o.name, o);
      for (const a of o.aliases ?? []) if (!this.index.has(a)) this.index.set(a, o);
    }
    this.mount = meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
    this.rows = new Map();
    this.badges = new Map(); // option name → [{kind, text}]
    this.seq = 0;
    this.url = '';
    this.last = null; // last successful response
    if (!Array.isArray(state.info)) state.info = this.recommended();
    this.renderBuilder();
    const later = debounce(() => this.refresh(), 250);
    this.later = later;
    $('info-run').addEventListener('click', () => this.run());
    $('info-alias').addEventListener('change', () => this.changed());
    $('info-preset-recommended').addEventListener('click', () => this.setEntries(this.recommended()));
    $('info-preset-all').addEventListener('click', () => this.setEntries(this.selectAll()));
    $('info-preset-defaults').addEventListener('click', () => this.setEntries([]));
    $('info-copy').addEventListener('click', () => copyText(this.publicURL(), '/info URL copied'));
    $('info-copy-curl').addEventListener('click', () => copyText(curlInfoSnippet(this.publicURL()), 'curl command copied'));
    $('info-copy-ts').addEventListener('click', () => {
      const ts = tsInfoSnippet({
        source: this.state.source, entries: this.entries(), index: this.index, base: this.mountBase(),
        signed: $('snippet-signed').checked, separator: this.mount?.arguments_separator, encoding: 'plain',
      });
      if (ts) copyText(ts, 'TypeScript copied');
    });
    $('show-detections').addEventListener('change', () => {
      if ($('show-detections').checked && !this.objects && meta.detection) {
        if (!this.entry('detect_objects')) this.setEntries([...(this.state.info ?? []), { name: 'detect_objects', form: 0, values: ['1'] }]);
        this.run();
      }
      this.preview.drawDetections();
    });
  }

  recommended() {
    const names = [...RECOMMENDED, ...(this.meta.detection ? ['detect_objects'] : [])];
    return names.map((n) => this.index.get(n)).filter(Boolean).map((o) => exampleEntry(o));
  }

  // selectAll includes every option with values that work on any source;
  // hashsum only with the digest of the last response, preset only when
  // one is configured.
  selectAll() {
    const out = [];
    for (const o of this.options) {
      if (o.name === 'hashsum') {
        const d = this.last?.hashsums?.sha256;
        if (d) out.push({ name: o.name, form: 0, values: ['sha256', d] });
        continue;
      }
      if (o.name === 'preset') {
        if (this.meta.presets?.length) out.push({ name: o.name, form: 0, values: [this.meta.presets[0]] });
        continue;
      }
      out.push(exampleEntry(o, true));
    }
    return out;
  }

  entry(name) { return (this.state.info ?? []).find((e) => e.name === name); }

  // entries are the state's entries with their rest groups, for grammar.js.
  entries() {
    return (this.state.info ?? []).filter((e) => this.index.has(e.name)).map((e) => ({ ...e, groups: restGroups(this.index.get(e.name), e) }));
  }

  setEntries(list) {
    this.state.info = list;
    this.renderBuilder();
    this.changed();
  }

  // restore replaces the entries from a share link (app.js).
  restore(list) {
    this.state.info = Array.isArray(list) ? list : this.recommended();
    this.renderBuilder();
    this.refresh(false);
  }

  changed() {
    this.renderApplied();
    for (const o of this.options) this.mark(o);
    this.onChange();
    this.later();
  }

  // ---- builder -------------------------------------------------------------

  renderBuilder() {
    const box = $('info-options');
    box.replaceChildren();
    this.rows.clear();
    const byGroup = new Map(GROUPS.map(([g]) => [g, []]));
    for (const o of this.options) (byGroup.get(o.group) ?? byGroup.get('other')).push(o);
    for (const [g, label] of GROUPS) {
      const opts = byGroup.get(g);
      if (!opts.length) continue;
      const count = el('span', { class: 'count' });
      const body = el('div', { class: 'group-body' });
      const det = el('details', { class: 'group', 'data-group': g }, el('summary', {}, el('span', { text: label }), count), body);
      for (const o of opts) body.append(this.row(o));
      det._opts = opts;
      det._count = count;
      box.append(det);
    }
    this.renderApplied();
  }

  row(o) {
    const id = uid('iopt');
    const on = el('input', { type: 'checkbox', id, 'data-option': o.name });
    on.checked = Boolean(this.entry(o.name));
    const docId = `${id}-doc`;
    const docBtn = el('button', { type: 'button', class: 'icon', 'aria-expanded': 'false', 'aria-controls': docId, 'aria-label': `About ${o.name}`, text: '?' });
    const doc = el('div', { class: 'opt-doc', id: docId, hidden: true },
      el('p', { text: o.doc || 'No description.' }),
      o.example ? el('p', {}, 'Example: ', el('code', { text: `${o.name}:${o.example}` })) : null,
      o.milestone ? el('p', { class: 'hint', text: `Since ${o.milestone}.` }) : null,
      o.security ? el('p', { class: 'hint', text: 'A security option: the server refuses it (403) unless XI_SECURITY_ALLOW_SECURITY_OPTIONS is set.' }) : null);
    docBtn.addEventListener('click', () => {
      doc.hidden = !doc.hidden;
      docBtn.setAttribute('aria-expanded', String(!doc.hidden));
    });
    const badges = el('span', { class: 'opt-badges' });
    const head = el('div', { class: 'opt-head' },
      el('label', { class: 'inline opt-toggle', for: id }, on, el('span', { class: 'opt-name', id: `${id}-name`, text: o.name })),
      o.aliases?.length ? el('span', { class: 'aliases', title: 'Aliases', text: o.aliases.join(' · ') }) : null,
      o.default && o.default !== '—' ? el('span', { class: 'hint', title: 'Server default when the option is absent', text: `default ${o.default}` }) : null,
      el('span', { class: 'spacer' }), badges, docBtn);
    const args = el('div', { class: 'args' });
    const row = el('div', { class: 'opt', 'data-name': o.name, role: 'group', 'aria-labelledby': `${id}-name` }, head, doc, args);
    row._badges = badges;
    on.addEventListener('change', () => {
      if (on.checked) {
        if (!this.entry(o.name)) this.state.info = [...(this.state.info ?? []), exampleEntry(o)];
      } else {
        this.state.info = (this.state.info ?? []).filter((e) => e.name !== o.name);
      }
      this.renderArgs(o, args);
      this.changed();
    });
    this.rows.set(o.name, row);
    this.renderArgs(o, args);
    this.mark(o);
    return row;
  }

  // renderArgs renders the inputs of an included option.
  renderArgs(o, box) {
    box.replaceChildren();
    const e = this.entry(o.name);
    box.hidden = !e;
    if (!e) return;
    if (o.forms.length > 1) {
      const sel = el('select', { 'aria-label': `${o.name} form` });
      o.forms.forEach((f, i) => {
        const kw = (f.args ?? []).find((a) => a.enum?.length === 1)?.enum[0];
        const names = (f.args ?? []).filter((a) => !(a.enum?.length === 1)).map((a) => a.name);
        sel.append(el('option', { value: i, text: [kw, [...names, ...(f.rest ?? []).map((a) => `${a.name}…`)].join(', ')].filter(Boolean).join(': ') }));
      });
      sel.value = String(e.form ?? 0);
      sel.addEventListener('change', () => {
        e.form = Number(sel.value);
        e.values = (o.forms[e.form].args ?? []).map((a) => (a.enum?.length === 1 ? a.enum[0] : ''));
        this.renderArgs(o, box);
        this.changed();
      });
      box.append(el('div', { class: 'form-choice full' }, sel));
    }
    const f = o.forms[e.form ?? 0] ?? o.forms[0];
    (f.args ?? []).forEach((a, i) => { if (!(a.enum?.length === 1 && o.forms.length > 1)) box.append(this.input(o, e, a, i)); });
    if (f.rest?.length) box.append(this.restInput(o, e, f));
  }

  input(o, e, a, i) {
    const id = uid('iarg');
    const set = (v) => { e.values[i] = v; this.changed(); };
    const value = e.values[i] ?? '';
    if (a.type === 'boolean') {
      const cb = el('input', { type: 'checkbox', id });
      cb.checked = ['1', 't', 'true'].includes(value);
      cb.addEventListener('change', () => set(cb.checked ? '1' : '0'));
      if (a.doc) cb.title = a.doc;
      return el('div', { class: 'arg' }, el('label', { class: 'inline', for: id }, cb, ` ${a.name.replaceAll('_', ' ')}`));
    }
    const bounds = [a.min !== undefined && `≥ ${a.min}`, a.max !== undefined && a.max < 1e9 && `≤ ${a.max}`].filter(Boolean).join(', ');
    const label = el('label', { for: id }, a.name.replaceAll('_', ' '), bounds ? el('span', { class: 'hint', text: ` ${bounds}` }) : null);
    let control;
    if (a.type === 'enum') {
      control = el('select', { id });
      if (a.optional) control.append(el('option', { value: '', text: '—' }));
      for (const v of a.enum ?? []) control.append(el('option', { value: v, text: v }));
      control.value = value || (a.optional ? '' : a.enum?.[0] ?? '');
      if (!value && !a.optional) e.values[i] = control.value;
      control.addEventListener('change', () => set(control.value));
    } else {
      const num = a.type === 'integer' || a.type === 'number';
      control = el('input', {
        id, type: num ? 'number' : 'text', autocomplete: 'off', spellcheck: 'false', inputmode: num ? 'decimal' : undefined,
        step: num ? (a.type === 'integer' ? 1 : 'any') : undefined, min: a.min, max: a.max !== undefined && a.max < 1e9 ? a.max : undefined,
      });
      control.value = value;
      control.addEventListener('input', () => set(control.value));
    }
    if (a.doc) control.title = a.doc;
    return el('div', { class: 'arg' }, label, control);
  }

  // restInput: an enum rest (hashsum algorithms) is a set of checkboxes;
  // anything else is typed as a ':'-separated list.
  restInput(o, e, f) {
    const n = f.args?.length ?? 0;
    if (f.rest.length === 1 && f.rest[0].enum) {
      const chosen = new Set(e.values.slice(n));
      const box = el('fieldset', { class: 'arg full multi' }, el('legend', { text: f.rest[0].name.replaceAll('_', ' ') }));
      for (const v of f.rest[0].enum) {
        const id = uid('irest');
        const cb = el('input', { type: 'checkbox', id, value: v });
        cb.checked = chosen.has(v);
        cb.addEventListener('change', () => {
          if (cb.checked) chosen.add(v); else chosen.delete(v);
          e.values = [...e.values.slice(0, n), ...f.rest[0].enum.filter((x) => chosen.has(x))];
          this.changed();
        });
        box.append(el('label', { class: 'inline', for: id }, cb, ` ${v}`));
      }
      return box;
    }
    const id = uid('irest');
    const names = f.rest.map((a) => a.name).join(':');
    const input = el('input', { id, type: 'text', autocomplete: 'off', spellcheck: 'false', placeholder: `${names}:${names}…` });
    if (/class/.test(names)) input.setAttribute('list', 'xi-classes');
    if (o.name === 'preset') input.setAttribute('list', 'xi-presets');
    input.value = e.values.slice(n).join(':');
    input.addEventListener('input', () => {
      e.values = [...e.values.slice(0, n), ...(input.value ? input.value.split(':') : [])];
      this.changed();
    });
    return el('div', { class: 'arg full' }, el('label', { for: id, text: `${names}… (separated by :)` }), input);
  }

  mark(o) {
    const row = this.rows.get(o.name);
    if (!row) return;
    const e = this.entry(o.name);
    row.classList.toggle('active', Boolean(e));
    const cb = row.querySelector('input[data-option]');
    if (cb) cb.checked = Boolean(e);
    row._badges.replaceChildren(...(this.badges.get(o.name) ?? []).map((b) => el('span', { class: `badge ${b.kind}`, title: b.text, text: b.short })));
  }

  renderApplied() {
    const box = $('info-applied');
    box.replaceChildren();
    for (const det of $('info-options').querySelectorAll('details.group')) {
      const n = det._opts.filter((o) => this.entry(o.name)).length;
      det._count.textContent = n ? String(n) : '';
    }
    for (const [o, , v] of activeEntries(this.entries(), this.index, 'imgproxy')) {
      const chip = el('button', { type: 'button', class: 'chip', title: 'Show this option' }, el('strong', { text: o.name }), v.length ? `: ${v.join(':')}` : '');
      chip.addEventListener('click', () => this.reveal(o.name));
      box.append(chip);
    }
    if (!box.childElementCount) box.append(el('span', { class: 'hint', text: 'No options: the server reports its defaults (size, format, dimensions, EXIF, IPTC, XMP, video).' }));
  }

  reveal(name) {
    const row = this.rows.get(name);
    if (!row) return;
    row.closest('details').open = true;
    row.scrollIntoView({ block: 'nearest' });
    row.querySelector('input, select')?.focus();
  }

  // ---- URL -----------------------------------------------------------------

  mountBase() {
    return this.baseURL() + (this.meta.path_prefix || '').replace(/\/+$/, '') + (this.mount?.prefix || '/').replace(/\/+$/, '');
  }

  publicURL() { return this.url ? this.baseURL() + this.url : ''; }

  // optionPath is the info options of entries joined by /.
  optionPath(entries) {
    const sep = this.mount?.arguments_separator || ':';
    const alias = $('info-alias').checked;
    return [...activeEntries(entries, this.index, 'imgproxy')].map(([o, , v]) => {
      const name = alias && o.aliases?.length ? o.aliases.at(-1) : o.name;
      return v.length ? [name, ...v].join(sep) : name;
    }).join('/');
  }

  // infoURL signs the /info path of the source with entries: the
  // signature covers the path after it, like a processing URL, and "info"
  // goes after the mount prefix.
  async infoURL(entries) {
    const opts = this.optionPath(entries);
    const path = `${opts ? `/${opts}` : ''}/plain/${escapePlain(this.state.source)}`;
    const signed = await this.sign({ mount: this.mount.id, path, query: '' });
    const prefix = (this.meta.path_prefix || '').replace(/\/+$/, '') + (this.mount.prefix || '/').replace(/\/+$/, '');
    return `${prefix}/info${signed.url.slice(prefix.length)}`;
  }

  visible() { return !$('mpane-info').hidden; }

  // refresh re-signs the URL and, when live and shown, loads it.
  async refresh(load = true) {
    if (!this.mount || !this.state.source) {
      $('info-url').value = '';
      return;
    }
    try {
      this.url = await this.infoURL(this.entries());
      $('info-url').value = this.publicURL();
      if (load && $('info-live').checked && this.visible()) await this.run();
    } catch (err) {
      this.status(err.message, true);
    }
  }

  status(msg, err = false) {
    const s = $('info-status');
    s.textContent = msg;
    s.classList.toggle('error', err);
  }

  async fetchInfo(entries) {
    const url = await this.infoURL(entries);
    const t0 = performance.now();
    const res = await fetch(`api/render?u=${encodeURIComponent(url)}`, { headers: { Accept: 'application/json' } });
    const text = await res.text();
    const out = { url, ok: res.ok, status: res.status, ms: performance.now() - t0, ignored: parseIgnored(res.headers.get('X-Xi-Ignored')) };
    if (res.ok) {
      try { out.json = JSON.parse(text); } catch { out.ok = false; out.error = 'The answer is not JSON.'; }
    } else {
      out.error = errorText(text, res.status);
    }
    return out;
  }

  // probe asks for each entry alone and returns the refused ones.
  async probe(entries) {
    const failed = new Map();
    const queue = [...entries];
    const worker = async () => {
      for (let e = queue.shift(); e; e = queue.shift()) {
        const r = await this.fetchInfo([e]);
        if (!r.ok) failed.set(e.name, r);
      }
    };
    await Promise.all(Array.from({ length: Math.min(6, entries.length) }, worker));
    return failed;
  }

  async run() {
    const my = ++this.seq;
    const box = $('info-result');
    if (!this.mount) {
      box.replaceChildren(el('p', { class: 'hint', text: 'No imgproxy mount with a prefix: /info is served under it.' }));
      return;
    }
    if (!this.state.source) return;
    this.status('Loading…');
    try {
      const entries = [...activeEntries(this.entries(), this.index, 'imgproxy')].map(([, e]) => e);
      this.url = await this.infoURL(entries);
      $('info-url').value = this.publicURL();
      let res = await this.fetchInfo(entries);
      let failed = new Map();
      if (!res.ok && entries.length > 1) {
        this.status(`${res.status}: finding which options the server refuses…`);
        failed = await this.probe(entries);
        if (failed.size && failed.size < entries.length) res = await this.fetchInfo(entries.filter((e) => !failed.has(e.name)));
        else failed = new Map();
      }
      if (my !== this.seq) return;
      this.setBadges(res, failed);
      if (!res.ok) {
        this.status(`${res.status} ${res.error}`, true);
        box.replaceChildren(el('p', { class: 'error', text: `${res.status}: ${res.error}` }));
        box.dataset.source = this.state.source;
        this.objects = null;
        this.preview.setDetections(null, null);
        return;
      }
      this.last = res.json;
      this.status(`${res.status} · ${Math.round(res.ms)} ms${failed.size ? ` · ${failed.size} option${failed.size > 1 ? 's' : ''} refused` : ''}`);
      await this.render(res.json, failed, res.ignored, entries);
    } catch (err) {
      if (my === this.seq) this.status(err.message, true);
    }
  }

  // setBadges marks refused and ignored options in the builder.
  setBadges(res, failed) {
    this.badges.clear();
    // One badge per kind and option; the title lists every reason.
    const add = (name, b) => {
      const list = this.badges.get(name) ?? [];
      const same = list.find((x) => x.kind === b.kind);
      if (same) same.text += `; ${b.text}`;
      else list.push(b);
      this.badges.set(name, list);
    };
    for (const [name, r] of failed) {
      const o = this.index.get(name);
      const why = o?.security && r.status === 403 ? 'needs XI_SECURITY_ALLOW_SECURITY_OPTIONS' : r.error;
      add(name, { kind: 'err', short: `refused ${r.status}`, text: `${r.status}: ${why}` });
    }
    for (const ig of res.ignored ?? []) {
      const [opt] = ig.option.split(':');
      const o = this.index.get(opt);
      add(o?.name ?? opt, { kind: 'warn', short: 'ignored', text: `${ig.option}: ${ig.reason}` });
    }
    if (!this.meta.detection && this.entry('detect_objects') && !failed.has('detect_objects')) {
      add('detect_objects', { kind: 'info', short: 'no models', text: 'api/meta reports no detection models (full image, XI_DETECT_ENABLED)' });
    }
    for (const o of this.options) this.mark(o);
  }

  // ---- result -----------------------------------------------------------

  async render(info, failed, ignored, entries) {
    const box = $('info-result');
    const sections = [];
    const notes = [];
    for (const [name, r] of failed) {
      const o = this.index.get(name);
      notes.push(el('li', {}, el('span', { class: 'badge err', text: 'refused' }), ' ', el('code', { text: name }), ` ${r.status}: ${o?.security && r.status === 403 ? `${r.error} (security option: set XI_SECURITY_ALLOW_SECURITY_OPTIONS)` : r.error}`));
    }
    for (const ig of ignored) notes.push(el('li', {}, el('span', { class: 'badge warn', text: 'ignored' }), ' ', el('code', { text: ig.option }), ` ${ig.reason}`));
    if (notes.length) sections.push(el('section', { class: 'info-sec', 'aria-label': 'Unavailable options' }, el('h3', { text: 'Not applied by this server' }), el('ul', { class: 'notes' }, ...notes)));

    sections.push(this.summary(info));
    const ph = await this.placeholders(info);
    if (ph) sections.push(ph);
    const colours = this.colours(info);
    if (colours) sections.push(colours);
    const hashes = this.hashes(info);
    if (hashes) sections.push(hashes);
    const det = this.detections(info);
    if (det) sections.push(det);
    const meta = this.metadata(info);
    if (meta) sections.push(meta);
    const video = this.video(info);
    if (video) sections.push(video);
    const json = JSON.stringify(info, null, 2);
    sections.push(el('details', { class: 'info-sec raw' },
      el('summary', {}, 'Raw JSON ', copyBtn(json, 'JSON', 'Copy JSON')),
      el('div', { class: 'tree', role: 'tree', 'aria-label': 'Raw /info response' }, tree(info))));
    box.replaceChildren(...sections);
    box.dataset.options = entries.map((e) => e.name).join(',');
    box.dataset.source = this.state.source;

    this.objects = info.objects ?? null;
    this.crop = info.crop ?? null;
    if (this.objects?.length || this.crop) $('show-detections').checked = true;
    this.preview.setDetections(this.objects, this.crop);
  }

  summary(info) {
    const stats = [];
    const add = (k, v, title) => { if (v !== undefined && v !== '' && v !== null) stats.push(el('div', { class: 'stat', title }, el('span', { text: k }), el('strong', { text: String(v) }))); };
    add('Format', info.format ? `${info.format}${info.mime_type ? ` · ${info.mime_type}` : ''}` : undefined);
    add('Dimensions', info.width !== undefined ? `${info.width} × ${info.height}` : undefined, 'One page or frame, EXIF orientation applied');
    if (info.width && info.height) add('Megapixels', (info.width * info.height / 1e6).toFixed(2));
    add('Orientation', info.orientation !== undefined ? `${info.orientation} · ${ORIENTATION[info.orientation] ?? '?'}` : undefined);
    add('Size', info.size !== undefined ? fmtBytes(info.size) : undefined, info.size !== undefined ? `${info.size} bytes` : undefined);
    add('Colour space', info.colorspace);
    add('Bands', info.bands);
    add('Sample format', info.sample_format);
    add('Pages', info.pages_number);
    add('Alpha', info.alpha === undefined ? undefined : info.alpha ? 'yes' : 'no');
    if (info.hdr) add('HDR', 'scene-linear');
    return el('section', { class: 'info-sec', 'aria-labelledby': 'info-sum-h' }, el('h3', { id: 'info-sum-h', text: 'Summary' }),
      stats.length ? el('div', { class: 'stats' }, ...stats) : el('p', { class: 'hint', text: 'No summary fields requested.' }));
  }

  // aspect is width / height of the source, from /info or the ThumbHash.
  aspect(info) {
    if (info.width && info.height) return info.width / info.height;
    return 4 / 3;
  }

  // thumbnail renders a small image of the source through api/render.
  async thumbnail() {
    try {
      const s = await this.sign({ mount: this.mount.id, path: `/rs:fit:320:320/f:png/plain/${escapePlain(this.state.source)}`, query: '' });
      const res = await fetch(`api/render?u=${encodeURIComponent(s.url)}`, { headers: { Accept: 'image/png' } });
      if (!res.ok) return null;
      if (this.thumbURL) URL.revokeObjectURL(this.thumbURL);
      this.thumbURL = URL.createObjectURL(await res.blob());
      return this.thumbURL;
    } catch {
      return null;
    }
  }

  // overlay draws the crop and the detections as fractions of the box.
  overlay(info) {
    const o = svg('svg', { class: 'ph-overlay', viewBox: '0 0 100 100', preserveAspectRatio: 'none', 'aria-hidden': 'true' });
    for (const d of info.objects ?? []) {
      o.append(svg('rect', { class: d.class_name === 'face' ? 'det face' : 'det', x: d.left * 100, y: d.top * 100, width: d.width * 100, height: d.height * 100 }));
    }
    if (info.crop) o.append(svg('rect', { class: 'crop', x: info.crop.left * 100, y: info.crop.top * 100, width: info.crop.width * 100, height: info.crop.height * 100 }));
    return o;
  }

  async placeholders(info) {
    const ar = this.aspect(info);
    const figs = [];
    const frame = (label, media, caption) => el('figure', { class: 'ph' }, el('div', { class: 'ph-box' }, media), el('figcaption', {}, el('strong', { text: label }), caption));
    const boxStyle = (node) => { node.style.aspectRatio = String(ar); return node; };
    const url = await this.thumbnail();
    if (url) {
      const box = el('div', { class: 'ph-media' }, el('img', { src: url, alt: 'Source thumbnail' }));
      if (info.crop || info.objects?.length) box.append(this.overlay(info));
      figs.push(frame('Image', boxStyle(box), el('span', { class: 'hint', text: info.crop || info.objects?.length ? ' crop (dashed) and detections' : ' 320 px thumbnail' })));
    }
    if (info.blurhash) {
      let media;
      let size = '';
      try {
        const { x, y } = blurhashSize(info.blurhash);
        const w = ar >= 1 ? 32 : Math.max(1, Math.round(32 * ar));
        const h = ar >= 1 ? Math.max(1, Math.round(32 / ar)) : 32;
        media = el('canvas', { class: 'ph-canvas', id: 'ph-blurhash', role: 'img', 'aria-label': `BlurHash placeholder, ${x}×${y} components` });
        paint(media, decodeBlurhash(info.blurhash, w, h));
        size = ` ${x}×${y} components`;
      } catch (err) {
        media = el('p', { class: 'error', text: err.message });
      }
      figs.push(frame('BlurHash', boxStyle(el('div', { class: 'ph-media' }, media)),
        el('span', {}, el('span', { class: 'hint', text: size }), ' ', el('code', { class: 'hash', text: info.blurhash }), ' ', copyBtn(info.blurhash, 'BlurHash'))));
    }
    if (info.thumb_hash) {
      let media;
      try {
        const img = decodeThumbhash(info.thumb_hash);
        media = el('canvas', { class: 'ph-canvas', id: 'ph-thumbhash', role: 'img', 'aria-label': `ThumbHash placeholder, ${img.w}×${img.h}` });
        paint(media, img);
      } catch (err) {
        media = el('p', { class: 'error', text: err.message });
      }
      const b64 = btoa(String.fromCharCode(...(info.thumb_hash.match(/../g) ?? []).map((x) => parseInt(x, 16))));
      figs.push(frame('ThumbHash', boxStyle(el('div', { class: 'ph-media' }, media)),
        el('span', {}, ' ', el('code', { class: 'hash', text: info.thumb_hash }), ' ', copyBtn(info.thumb_hash, 'ThumbHash hex', 'Copy hex'), ' ', copyBtn(b64, 'ThumbHash base64', 'Copy base64'))));
    }
    if (!figs.length) return null;
    return el('section', { class: 'info-sec', 'aria-labelledby': 'info-ph-h' }, el('h3', { id: 'info-ph-h', text: 'Placeholders (decoded in the browser)' }),
      el('div', { class: 'ph-grid' }, ...figs));
  }

  swatch(label, c) {
    const fill = el('span', { class: 'sw-fill' });
    fill.style.backgroundColor = rgbaOf(c);
    const chip = el('span', { class: 'sw-color', role: 'img', 'aria-label': `${label} ${hexOf(c)}` }, fill);
    return el('div', { class: 'swatch' }, chip,
      el('div', { class: 'sw-text' }, el('span', { class: 'sw-label', text: label }), el('code', { text: hexOf(c) }),
        el('span', { class: 'sw-copy' }, copyBtn(hexOf(c), `${label} hex`, 'hex'), copyBtn(rgbaOf(c), `${label} ${c.A === undefined ? 'RGB' : 'RGBA'}`, c.A === undefined ? 'rgb' : 'rgba'))));
  }

  colours(info) {
    const parts = [];
    if (info.average) parts.push(el('h4', { text: 'Average' }), el('div', { class: 'swatches' }, this.swatch('average', info.average)));
    if (info.dominant_colors) {
      const present = DOMINANT.filter(([k]) => info.dominant_colors[k]);
      parts.push(el('h4', { text: 'Dominant colours' }), present.length
        ? el('div', { class: 'swatches' }, ...present.map(([k, label]) => this.swatch(label, info.dominant_colors[k])))
        : el('p', { class: 'hint', text: 'None found (build_missed derives the missing classes).' }));
    }
    if (info.palette?.length) {
      parts.push(el('h4', {}, `Palette (${info.palette.length}) `, copyBtn(info.palette.map(hexOf).join(' '), 'palette', 'Copy all')),
        el('div', { class: 'swatches palette' }, ...info.palette.map((c, i) => this.swatch(`#${i + 1}`, c))));
    }
    if (!parts.length) return null;
    return el('section', { class: 'info-sec', 'aria-labelledby': 'info-col-h' }, el('h3', { id: 'info-col-h', text: 'Colours' }), ...parts);
  }

  hashes(info) {
    const rows = [];
    const row = (k, v) => rows.push(el('tr', {}, el('th', { scope: 'row', text: k }), el('td', {}, el('code', { text: v })), el('td', {}, copyBtn(v, k))));
    if (info.perceptual_hash) row('perceptual hash', info.perceptual_hash);
    if (info.blurhash) row('BlurHash', info.blurhash);
    if (info.thumb_hash) row('ThumbHash', info.thumb_hash);
    for (const [alg, sum] of Object.entries(info.hashsums ?? {})) row(alg, sum);
    if (!rows.length) return null;
    return el('section', { class: 'info-sec', 'aria-labelledby': 'info-hash-h' }, el('h3', { id: 'info-hash-h', text: 'Hashes' }),
      el('div', { class: 'table-wrap' }, el('table', { class: 'headers hashes', 'aria-label': 'Hashes' }, el('tbody', {}, ...rows))));
  }

  detections(info) {
    if (!info.objects && !info.crop) return null;
    const parts = [];
    if (info.crop) {
      const c = info.crop;
      parts.push(el('p', {}, el('strong', { text: 'Crop: ' }), `left ${pct(c.left)}, top ${pct(c.top)}, ${pct(c.width)} × ${pct(c.height)}`,
        info.width ? el('span', { class: 'hint', text: ` (${Math.round(c.left * info.width)}, ${Math.round(c.top * info.height)}) ${Math.round(c.width * info.width)}×${Math.round(c.height * info.height)} px` }) : null));
    }
    if (info.objects) {
      if (!info.objects.length) parts.push(el('p', { class: 'hint', text: 'No faces or objects found.' }));
      else {
        parts.push(el('div', { class: 'table-wrap' }, el('table', { class: 'matrix', 'aria-label': 'Detected objects' },
          el('thead', {}, el('tr', {}, ...['Class', 'Confidence', 'Left', 'Top', 'Width', 'Height', 'Landmarks'].map((h) => el('th', { scope: 'col', text: h })))),
          el('tbody', {}, ...info.objects.map((d) => el('tr', {},
            el('td', { text: `${d.class_name} (${d.class_id})` }), el('td', { text: `${Math.round(d.confidence * 100)}%` }),
            el('td', { text: pct(d.left) }), el('td', { text: pct(d.top) }), el('td', { text: pct(d.width) }), el('td', { text: pct(d.height) }),
            el('td', { text: d.landmarks ? String(d.landmarks.length) : '' })))))));
      }
    }
    parts.push(el('p', { class: 'hint', text: 'Drawn over the source in the preview (the "detections" toggle) and over the thumbnail above.' }));
    return el('section', { class: 'info-sec', 'aria-labelledby': 'info-det-h' }, el('h3', { id: 'info-det-h', text: 'Crop and detections' }), ...parts);
  }

  metadata(info) {
    const tables = [];
    if (info.exif) tables.push(searchableTable('EXIF', Object.entries(info.exif).map(([k, v]) => [k, str(v)]), true));
    if (info.iptc) tables.push(searchableTable('IPTC', Object.entries(info.iptc).map(([k, v]) => [k, Array.isArray(v) ? v.map(str).join('; ') : str(v)])));
    if (info.photoshop) tables.push(searchableTable('Photoshop', Object.entries(info.photoshop).map(([k, v]) => [k, str(v)])));
    if (info.xmp) {
      const rows = [];
      for (const [ns, props] of Object.entries(info.xmp)) for (const [k, v] of Object.entries(props ?? {})) rows.push([`${ns}:${k}`, str(v)]);
      tables.push(searchableTable('XMP', rows));
    }
    if (!tables.length) return null;
    return el('section', { class: 'info-sec', 'aria-labelledby': 'info-meta-h' }, el('h3', { id: 'info-meta-h', text: 'Metadata' }), ...tables);
  }

  video(info) {
    if (!info.video_meta && !info.video_streams) return null;
    const parts = [];
    if (info.video_meta) {
      const vm = info.video_meta;
      const fmt = (k, v) => (k === 'duration' ? `${Number(v).toFixed(3)} s` : k === 'bit_rate' ? `${(v / 1000).toFixed(0)} kbit/s` : k === 'rotation' ? `${v}°` : str(v));
      parts.push(searchableTable('Container', Object.entries(vm).map(([k, v]) => [k, fmt(k, v)]), true));
    }
    if (info.video_streams?.length) {
      const cols = ['type', 'codec', 'width', 'height', 'fps', 'duration', 'bps', 'rotation', 'language', 'frequency', 'layout'];
      const used = cols.filter((c) => info.video_streams.some((s) => s[c] !== undefined));
      parts.push(el('h4', { text: 'Streams' }), el('div', { class: 'table-wrap' }, el('table', { class: 'matrix', 'aria-label': 'Video streams' },
        el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: '#' }), ...used.map((c) => el('th', { scope: 'col', text: c })))),
        el('tbody', {}, ...info.video_streams.map((s, i) => el('tr', {}, el('td', { text: String(i) }), ...used.map((c) => el('td', { text: s[c] === undefined ? '' : String(s[c]) }))))))));
    }
    return el('section', { class: 'info-sec', 'aria-labelledby': 'info-vid-h' }, el('h3', { id: 'info-vid-h', text: 'Video' }), ...parts);
  }

  // reset forgets the previous source's result.
  reset() {
    this.objects = null;
    this.crop = null;
    this.last = null;
    this.preview.setDetections(null, null);
    $('info-result').replaceChildren();
    delete $('info-result').dataset.source;
    delete $('info-result').dataset.options;
    this.status('');
    this.badges.clear();
    for (const o of this.options) this.mark(o);
  }
}

