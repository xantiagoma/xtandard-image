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
      box.replaceChildren(...res.targets.map((t) => this.card(t)));
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
    if (t.error) {
      card.append(el('p', { class: 'hint', text: t.error }));
      return card;
    }
    const url = this.publicURL(t.url);
    const copy = el('button', { type: 'button', class: 'small', text: 'Copy' });
    copy.addEventListener('click', () => copyText(url, `${t.label} URL copied`));
    const actions = el('div', { class: 'row wrap' }, copy);
    if (t.mount) {
      const show = el('button', { type: 'button', class: 'small', text: 'Preview' });
      show.addEventListener('click', () => this.onPreview(t.url));
      actions.append(show);
    }
    card.append(el('code', { class: 'turl', text: url }), actions);
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
