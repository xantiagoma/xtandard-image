// The option builder: every option of the mount's catalogue, grouped by
// category, with typed inputs, inline docs, aliases and a search box. The
// state lives in state.pipelines (one list of entries per chained
// pipeline); this module renders it and reports edits through onChange.

import { $, el, uid } from './dom.js';
import { argTypes, colorValue, isActive, isFlag } from './grammar.js';

const GROUPS = [
  ['size', 'Resize, crop and rotate'],
  ['gravity', 'Gravity and focus'],
  ['format', 'Format and quality'],
  ['color', 'Colour'],
  ['effects', 'Effects'],
  ['watermark', 'Watermarks'],
  ['detection', 'Detection (faces, objects)'],
  ['frames', 'Pages, frames and video'],
  ['metadata', 'Metadata'],
  ['delivery', 'Delivery and cache'],
  ['presets', 'Presets and raw options'],
  ['other', 'Other'],
];

// formLabel names a form: its keyword (a single-valued first argument)
// and its arguments ("obj: class…", "fp: x, y").
function formLabel(f) {
  const args = [...(f.args ?? [])];
  let head = '';
  if (args[0]?.enum?.length === 1) head = args.shift().enum[0];
  const names = [...args.map((a) => a.name), ...(f.rest ?? []).map((a) => `${a.name}…`)].join(', ');
  return [head, names].filter(Boolean).join(': ') || '(no arguments)';
}

// PIPELINE_HINTS explain chaining in each grammar that has it: text with
// `code` spans.
const PIPELINE_HINTS = {
  imgproxy: 'Chained pipelines (`/-/`): each runs on the result of the previous one.',
  imagekit: 'Chained steps (`:`): each runs on the result of the previous one.',
  cloudinary: 'Chained components (`/`): each runs on the result of the previous one; an overlay is `l` in one component and `fl_layer_apply` (with its placement) in a later one.',
};

const hintNodes = (text) => text.split('`').map((part, i) => (i % 2 ? el('code', { text: part }) : part));

// vPattern makes a catalogue pattern valid as an HTML pattern attribute,
// which browsers compile with the v flag: a literal "-" at the end of a
// character class must be escaped there.
const vPattern = (p) => (p ? p.replace(/(?<!\\)-\]/g, '\\-]') : undefined);

// Bounds at the int32 limit are "unbounded" for a person.
const BIG = 1e9;

export class Builder {
  constructor({ state, onChange }) {
    this.state = state;
    this.onChange = onChange;
    this.cat = null; // {options, index}
    this.grammar = '';
    this.pipeline = 0;
    this.rows = new Map(); // option name → row element (rendered groups only)
    this.query = '';
  }

  // entry returns the entry of option name in the current pipeline,
  // creating it when create is set.
  entry(name, create = false) {
    const p = this.state.pipelines[this.pipeline] ?? (this.state.pipelines[this.pipeline] = []);
    let e = p.find((x) => x.name === name);
    if (!e && create) {
      e = { name, form: 0, groups: 1, values: [], on: false };
      p.push(e);
    }
    return e;
  }

  // render rebuilds the catalogue for a grammar's catalogue.
  render(grammar, cat) {
    this.grammar = grammar;
    this.cat = cat;
    this.rows.clear();
    const box = $('catalogue');
    box.replaceChildren();
    if (!cat) return;
    const byGroup = new Map(GROUPS.map(([g]) => [g, []]));
    for (const o of cat.options) (byGroup.get(o.group) ?? byGroup.get('other')).push(o);
    for (const [g, label] of GROUPS) {
      const opts = byGroup.get(g);
      if (!opts.length) continue;
      const count = el('span', { class: 'count' });
      const body = el('div', { class: 'group-body' });
      const det = el('details', { class: 'group', 'data-group': g }, el('summary', {}, el('span', { text: label }), count), body);
      det.addEventListener('toggle', () => { if (det.open) this.fillGroup(det, opts); });
      det._opts = opts;
      det._count = count;
      box.append(det);
    }
    this.renderPipelines();
    this.refreshCounts();
    this.applySearch(this.query);
    // The groups holding active options start open.
    for (const det of box.querySelectorAll('details.group')) {
      if (det._opts.some((o) => { const e = this.entry(o.name); return e && isActive(o, e); })) det.open = true;
    }
  }

  fillGroup(det, opts) {
    const body = det.querySelector('.group-body');
    if (body.childElementCount) return;
    for (const o of opts) body.append(this.row(o));
    this.applySearch(this.query);
  }

  // row renders one option.
  row(o) {
    const id = uid('opt');
    const docId = `${id}-doc`;
    const clear = el('button', { type: 'button', class: 'icon clear', 'aria-label': `Clear ${o.name}`, title: 'Clear', text: '×' });
    const docBtn = el('button', { type: 'button', class: 'icon', 'aria-expanded': 'false', 'aria-controls': docId, 'aria-label': `About ${o.name}`, text: '?' });
    const doc = el('div', { class: 'opt-doc', id: docId, hidden: true },
      el('p', { text: o.doc || 'No description.' }),
      o.example ? el('p', {}, 'Example: ', el('code', { text: o.example })) : null,
      o.milestone ? el('p', { class: 'hint', text: `Since ${o.milestone}.` }) : null,
      o.security ? el('p', { class: 'hint', text: 'Needs XI_SECURITY_ALLOW_SECURITY_OPTIONS.' }) : null);
    docBtn.addEventListener('click', () => {
      doc.hidden = !doc.hidden;
      docBtn.setAttribute('aria-expanded', String(!doc.hidden));
    });
    const head = el('div', { class: 'opt-head' },
      el('span', { class: 'opt-name', id: `${id}-name`, text: o.name }),
      o.aliases?.length ? el('span', { class: 'aliases', title: 'Aliases', text: o.aliases.join(' · ') }) : null,
      el('span', { class: 'spacer' }), docBtn, clear);
    const row = el('div', { class: 'opt', 'data-name': o.name, role: 'group', 'aria-labelledby': `${id}-name` }, head, doc);
    row._doc = `${o.name} ${(o.aliases ?? []).join(' ')} ${o.doc ?? ''}`.toLowerCase();
    const args = el('div', { class: 'args' });
    row.append(args);
    if (o.forms.length > 1) {
      const sel = el('select', { 'aria-label': `${o.name} form` });
      o.forms.forEach((f, i) => sel.append(el('option', { value: i, text: formLabel(f) })));
      sel.value = String(this.entry(o.name)?.form ?? 0);
      sel.addEventListener('change', () => {
        const e = this.entry(o.name, true);
        e.form = Number(sel.value);
        // Arguments with a single allowed value (the form's keyword) are set.
        e.values = (o.forms[e.form].args ?? []).map((x) => (x.enum?.length === 1 ? x.enum[0] : ''));
        e.groups = 1;
        this.renderArgs(o, args);
        this.changed(o);
      });
      head.after(el('div', { class: 'form-choice' }, sel));
    }
    clear.addEventListener('click', () => {
      const p = this.state.pipelines[this.pipeline];
      const i = p.findIndex((x) => x.name === o.name);
      if (i >= 0) p.splice(i, 1);
      this.renderArgs(o, args);
      this.changed(o);
    });
    this.renderArgs(o, args);
    this.rows.set(o.name, row);
    this.mark(o);
    return row;
  }

  // renderArgs renders the inputs of an option's current form.
  renderArgs(o, box) {
    box.replaceChildren();
    const e = this.entry(o.name);
    if (isFlag(o)) {
      const id = uid('flag');
      const cb = el('input', { type: 'checkbox', id });
      cb.checked = Boolean(e?.on);
      cb.addEventListener('change', () => { this.entry(o.name, true).on = cb.checked; this.changed(o); });
      box.append(el('label', { class: 'inline', for: id }, cb, ' on'));
      return;
    }
    const form = o.forms[e?.form ?? 0] ?? o.forms[0];
    const types = argTypes(o, e ?? { form: 0, groups: 1 });
    types.forEach((a, i) => {
      if (form.rest?.length && i === form.args.length) box.append(el('div', { class: 'group-sep', 'aria-hidden': 'true' }));
      box.append(this.input(o, a, i));
    });
    if (form.rest?.length) {
      const names = form.rest.map((a) => a.name).join(' / ');
      const add = el('button', { type: 'button', class: 'small', text: `+ ${names}` });
      add.addEventListener('click', () => {
        const x = this.entry(o.name, true);
        x.groups = (x.groups ?? 1) + 1;
        this.renderArgs(o, box);
      });
      box.append(el('div', { class: 'row' }, add));
    }
  }

  // input renders one typed argument.
  input(o, a, i) {
    const id = uid('arg');
    const e = this.entry(o.name);
    const value = e?.values?.[i] ?? '';
    const set = (v) => {
      const x = this.entry(o.name, true);
      x.values[i] = v;
      this.changed(o);
    };
    const bounds = [a.min !== undefined && `≥ ${a.min}`, a.max !== undefined && a.max < BIG && `≤ ${a.max}`].filter(Boolean).join(', ');
    const label = el('label', { for: id }, a.name, bounds ? el('span', { class: 'hint', text: ` ${bounds}` }) : null);
    let control;
    switch (a.type) {
      case 'boolean': {
        control = el('select', { id });
        const [t, f] = this.grammar === 'imgproxy' ? ['1', '0'] : ['true', 'false'];
        for (const [v, txt] of [['', '—'], [t, 'true'], [f, 'false']]) control.append(el('option', { value: v, text: txt }));
        control.value = value;
        break;
      }
      case 'enum': {
        control = el('select', { id });
        control.append(el('option', { value: '', text: '—' }));
        for (const v of a.enum ?? []) control.append(el('option', { value: v, text: v }));
        control.value = value;
        break;
      }
      case 'integer':
      case 'number': {
        control = el('input', {
          id, type: 'number', step: a.type === 'integer' ? 1 : 'any', inputmode: 'decimal',
          min: a.min, max: a.max !== undefined && a.max < BIG ? a.max : undefined,
        });
        control.value = value;
        if (a.min !== undefined && a.max !== undefined && a.max - a.min <= 5000 && a.max < BIG) {
          const range = el('input', {
            type: 'range', min: a.min, max: a.max, step: a.type === 'integer' ? 1 : (a.max - a.min) / 200,
            'aria-label': `${a.name} slider`, tabindex: '-1',
          });
          range.value = value === '' ? String(a.min) : value;
          range.addEventListener('input', () => { control.value = range.value; set(range.value); });
          control.addEventListener('input', () => { if (control.value !== '') range.value = control.value; });
          const wrap = el('div', { class: 'arg' }, label, el('div', { class: 'num' }, control, range));
          control.addEventListener('input', () => set(control.value));
          if (a.doc) control.title = a.doc;
          return wrap;
        }
        break;
      }
      case 'color': {
        control = el('input', { id, type: 'text', autocomplete: 'off', spellcheck: 'false', placeholder: colorValue(this.grammar, 'ff0000') });
        control.value = value;
        const picker = el('input', { type: 'color', 'aria-label': `${a.name} colour picker` });
        const hex = value.replace(/^(rgb:|%23|#)/i, '').slice(0, 6);
        if (/^[0-9a-f]{6}$/i.test(hex)) picker.value = `#${hex.toLowerCase()}`;
        picker.addEventListener('input', () => { control.value = colorValue(this.grammar, picker.value); set(control.value); });
        control.addEventListener('input', () => set(control.value));
        if (a.doc) control.title = a.doc;
        return el('div', { class: 'arg' }, label, el('div', { class: 'num' }, control, picker));
      }
      default: {
        // A base64 argument is typed as plain text (encoded for you), so the
        // pattern of its encoded form does not apply.
        control = el('input', { id, type: 'text', autocomplete: 'off', spellcheck: 'false', pattern: a.type === 'base64' ? undefined : vPattern(a.pattern) });
        control.value = value;
        if (a.type === 'base64') control.placeholder = 'plain text (encoded for you)';
        if (a.type === 'direction') control.setAttribute('list', 'xi-directions');
        if (/class/.test(a.name) || (o.name === 'crop' && this.grammar === 'imgix') || /^(g|fo)$/.test(o.name)) control.setAttribute('list', 'xi-classes');
        if (o.name === 'preset' || o.name === 't' || o.name === 'n' || o.name === 'class') control.setAttribute('list', 'xi-presets');
      }
    }
    if (a.doc) control.title = a.doc;
    control.addEventListener(control.tagName === 'SELECT' ? 'change' : 'input', () => set(control.value));
    return el('div', { class: 'arg' }, label, control);
  }

  changed(o) {
    this.mark(o);
    this.refreshCounts();
    this.renderApplied();
    this.onChange();
  }

  mark(o) {
    const row = this.rows.get(o.name);
    const e = this.entry(o.name);
    if (row) row.classList.toggle('active', Boolean(e && isActive(o, e)));
  }

  refreshCounts() {
    for (const det of $('catalogue').querySelectorAll('details.group')) {
      const n = det._opts.filter((o) => { const e = this.entry(o.name); return e && isActive(o, e); }).length;
      det._count.textContent = n ? String(n) : '';
    }
    this.renderApplied();
  }

  // renderApplied lists the active options of the pipeline as chips.
  renderApplied() {
    const box = $('applied');
    box.replaceChildren();
    if (!this.cat) return;
    const p = this.state.pipelines[this.pipeline] ?? [];
    for (const e of p) {
      const o = this.cat.index.get(e.name);
      if (!o || !isActive(o, e)) continue;
      const vals = (e.values ?? []).filter((v) => v !== '' && v !== undefined).join(':');
      const chip = el('button', { type: 'button', class: 'chip', title: 'Show this option' }, el('strong', { text: o.name }), vals ? `: ${vals}` : '');
      chip.addEventListener('click', () => this.reveal(o.name));
      box.append(chip);
    }
  }

  // reveal opens the option's group and focuses its first input.
  reveal(name) {
    const o = this.cat?.index.get(name);
    if (!o) return;
    const det = [...$('catalogue').querySelectorAll('details.group')].find((d) => d._opts.includes(o));
    if (!det) return;
    det.open = true;
    this.fillGroup(det, det._opts);
    const row = this.rows.get(o.name);
    row?.scrollIntoView({ block: 'nearest' });
    row?.querySelector('input, select')?.focus();
  }

  applySearch(q) {
    this.query = q.trim().toLowerCase();
    const box = $('catalogue');
    for (const det of box.querySelectorAll('details.group')) {
      if (!this.query) {
        det.hidden = false;
        for (const r of det.querySelectorAll('.opt')) r.hidden = false;
        continue;
      }
      const matches = det._opts.filter((o) => `${o.name} ${(o.aliases ?? []).join(' ')} ${o.doc ?? ''}`.toLowerCase().includes(this.query));
      det.hidden = matches.length === 0;
      if (matches.length) {
        det.open = true;
        this.fillGroup(det, det._opts);
        for (const r of det.querySelectorAll('.opt')) r.hidden = !r._doc.includes(this.query);
      }
    }
  }

  // renderPipelines draws the pipeline tabs of the grammars that chain:
  // imgproxy (/-/), ImageKit steps (:) and Cloudinary components (/).
  renderPipelines() {
    const box = $('pipelines');
    const hint = PIPELINE_HINTS[this.grammar];
    box.hidden = !hint;
    if (!hint) {
      this.pipeline = 0;
      return;
    }
    $('pipelines-hint').replaceChildren(...hintNodes(hint));
    const tabs = $('pipeline-tabs');
    tabs.replaceChildren();
    const n = Math.max(1, this.state.pipelines.length);
    for (let i = 0; i < n; i++) {
      const t = el('button', { type: 'button', role: 'tab', 'aria-selected': String(i === this.pipeline), text: `Pipeline ${i + 1}` });
      t.addEventListener('click', () => { this.pipeline = i; this.render(this.grammar, this.cat); });
      tabs.append(t);
    }
    const add = el('button', { type: 'button', class: 'small', title: 'Chain a pipeline', text: '+ chain' });
    add.addEventListener('click', () => {
      this.state.pipelines.push([]);
      this.pipeline = this.state.pipelines.length - 1;
      this.render(this.grammar, this.cat);
      this.onChange();
    });
    tabs.append(add);
    if (n > 1) {
      const del = el('button', { type: 'button', class: 'small', text: `− remove pipeline ${this.pipeline + 1}` });
      del.addEventListener('click', () => {
        this.state.pipelines.splice(this.pipeline, 1);
        if (!this.state.pipelines.length) this.state.pipelines.push([]);
        this.pipeline = Math.max(0, this.pipeline - 1);
        this.render(this.grammar, this.cat);
        this.onChange();
      });
      tabs.append(del);
    }
  }
}
