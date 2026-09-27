// "Same transform in every dialect": api/translate writes the current URL
// in each dialect and says what each cannot express.

import { $, copyText, el, postJSON } from './dom.js';

export class Translate {
  constructor({ publicURL, onPreview }) {
    this.publicURL = publicURL; // path → absolute URL
    this.onPreview = onPreview; // path → render it in the preview
    this.seq = 0;
    this.visible = true;
    this.pending = null;
  }

  setVisible(v) {
    this.visible = v;
    if (v && this.pending) this.run(this.pending);
  }

  async run(req) {
    this.pending = req;
    if (!this.visible || !req) return;
    const seq = ++this.seq;
    const box = $('translate');
    try {
      const res = await postJSON('api/translate', { mount: req.mount, path: req.path, query: req.query || '' });
      if (seq !== this.seq) return;
      this.pending = null;
      // One card per dialect, whatever the server sends.
      const seen = new Set();
      const targets = res.targets.filter((t) => !seen.has(t.dialect) && seen.add(t.dialect));
      box.replaceChildren(...targets.map((t) => this.card(t)));
    } catch (err) {
      if (seq !== this.seq) return;
      box.replaceChildren(el('p', { class: 'hint', text: `Cannot translate this URL: ${err.message}` }));
    }
  }

  card(t) {
    const where = t.mount ? `mount ${t.mount}` : 'not mounted here (default prefix)';
    const head = el('div', { class: 'tcard-head' }, el('strong', { text: t.label }), el('span', { class: 'hint', text: ` · ${where}` }),
      t.signed ? el('span', { class: 'badge ok', text: 'signed' }) : null);
    const card = el('article', { class: `tcard${t.error ? ' off' : ''}`, 'aria-label': t.label }, head);
    if (t.error || !t.url) {
      // Never an empty card: say why this dialect cannot write the URL.
      const why = t.error || 'The server returned no URL for this dialect.';
      card.append(el('p', { class: 'treason' }, el('strong', { text: 'Not expressible here. ' }), why));
      return card;
    }
    // A mount with several prefixes: the same URL on each, picked here.
    const urls = [{ prefix: '', url: t.url }, ...(t.alternates ?? [])];
    let current = t.url;
    const code = el('code', { class: 'turl', text: this.publicURL(current) });
    const copy = el('button', { type: 'button', class: 'small', text: 'Copy' });
    copy.addEventListener('click', () => copyText(this.publicURL(current), `${t.label} URL copied`));
    const actions = el('div', { class: 'row wrap' }, copy);
    if (t.mount) {
      const show = el('button', { type: 'button', class: 'small', text: 'Preview' });
      show.addEventListener('click', () => this.onPreview(current));
      actions.append(show);
    }
    card.append(code);
    if (urls.length > 1) {
      const id = `talt-${t.dialect.replace(/[^a-z0-9]/gi, '-')}`;
      const pick = el('select', { id }, ...urls.map((u, i) => el('option', { value: String(i), text: i ? u.prefix : 'primary prefix' })));
      pick.addEventListener('change', () => {
        current = urls[Number(pick.value)].url;
        code.textContent = this.publicURL(current);
      });
      card.append(el('div', { class: 'talt' }, el('label', { class: 'inline', for: id, text: 'Prefix' }), pick));
    }
    card.append(actions);
    const chips = (label, list, cls) => (list?.length
      ? el('p', { class: 'tnotes' }, el('span', { class: 'hint', text: `${label}: ` }), ...list.map((f) => el('span', { class: `chip ${cls}`, text: f })))
      : null);
    const lost = chips('Unsupported here', t.unsupported, 'warn');
    const approx = chips('Approximated', t.approximated, 'info');
    if (!lost && !approx) card.append(el('p', { class: 'tnotes ok', text: 'Everything expressed.' }));
    card.append(...[lost, approx].filter(Boolean));
    return card;
  }
}
