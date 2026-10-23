// The code panel (WP-145): the current URL as a snippet for curl or one of
// the official clients, highlighted, with copy and line wrap. The language,
// wrap and the signed / enc/ settings are one preference for every panel
// on the page, kept in this browser's local storage.

import { copyText, el, store } from './dom.js';
import { tokenize } from './snippets/highlight.js';
import { LANGUAGES, renderSnippet } from './snippets/index.js';

const KEY = 'xi-playground-snippets';
const saved = store.get(KEY, {});
const prefs = {
  lang: LANGUAGES.some((l) => l.id === saved?.lang) ? saved.lang : 'typescript',
  wrap: Boolean(saved?.wrap),
  signed: saved?.signed !== false,
  encrypt: Boolean(saved?.encrypt),
  open: Boolean(saved?.open),
};
const panels = new Set();

function setPref(key, value) {
  prefs[key] = value;
  store.set(KEY, prefs);
  for (const p of panels) p.sync();
}

// rememberOpen keeps a <details> around a panel open or closed as the
// viewer left it.
export function rememberOpen(details) {
  details.open = prefs.open;
  details.addEventListener('toggle', () => {
    prefs.open = details.open;
    store.set(KEY, prefs);
  });
}

export class SnippetPanel {
  // root: the container; id: a prefix for element ids; model(settings)
  // returns the snippet model of the panel's request (snippets.js).
  constructor(root, { id, model }) {
    this.model = model;
    this.lang = el('select', { id: `${id}-lang`, onchange: (e) => setPref('lang', e.target.value) },
      ...LANGUAGES.map((l) => el('option', { value: l.id, text: l.label })));
    this.wrap = el('input', { type: 'checkbox', id: `${id}-wrap`, onchange: (e) => setPref('wrap', e.target.checked) });
    this.signed = el('input', { type: 'checkbox', id: `${id}-signed`, onchange: (e) => setPref('signed', e.target.checked) });
    this.encrypt = el('input', { type: 'checkbox', id: `${id}-enc`, onchange: (e) => setPref('encrypt', e.target.checked) });
    this.copy = el('button', { type: 'button', id: `${id}-copy`, onclick: () => this.copyCode() }, 'Copy code');
    this.note = el('p', { class: 'hint snippet-note', id: `${id}-note` });
    this.code = el('code', { class: 'snippet-code' });
    this.pre = el('pre', { class: 'snippet-pre', id: `${id}-code`, tabindex: '0', role: 'region' }, this.code);
    root.replaceChildren(
      el('div', { class: 'row wrap snippet-bar', role: 'group', 'aria-label': 'Code snippet settings' },
        el('label', { class: 'inline', for: `${id}-lang`, id: `${id}-label` }, 'Code'),
        this.lang,
        this.copy,
        el('label', { class: 'inline', title: 'Wrap long lines' }, this.wrap, ' wrap'),
        el('label', { class: 'inline', title: 'Read the signing key and salt from the environment (XI_SIGNING_KEY, XI_SIGNING_SALT)' }, this.signed, ' signed'),
        el('label', { class: 'inline', title: 'An encrypted enc/ source: the key comes from XI_ENCRYPTION_KEY (imgproxy URLs)' }, this.encrypt, ' encrypt source')),
      this.note,
      this.pre,
    );
    panels.add(this);
    this.sync();
  }

  // sync shows the preferences and redraws.
  sync() {
    this.lang.value = prefs.lang;
    this.wrap.checked = prefs.wrap;
    this.signed.checked = prefs.signed;
    this.encrypt.checked = prefs.encrypt;
    this.pre.classList.toggle('wrap', prefs.wrap);
    this.refresh();
  }

  // refresh redraws the snippet from the current request.
  refresh() {
    const model = this.model({ signed: prefs.signed, encrypt: prefs.encrypt });
    const { code, note } = model ? renderSnippet(prefs.lang, model) : { code: '', note: '' };
    this.text = code;
    const label = LANGUAGES.find((l) => l.id === prefs.lang)?.label ?? prefs.lang;
    this.pre.dataset.lang = prefs.lang;
    this.pre.setAttribute('aria-label', `${label} snippet`);
    this.code.replaceChildren(...tokenize(code, prefs.lang).map((t) => (t.t ? el('span', { class: `tk-${t.t}` }, t.s) : document.createTextNode(t.s))));
    this.pre.hidden = !code;
    this.copy.disabled = !code;
    this.note.textContent = note || (code ? '' : 'Nothing to show yet.');
    this.note.hidden = !this.note.textContent;
  }

  copyCode() {
    const label = LANGUAGES.find((l) => l.id === prefs.lang)?.label ?? 'Code';
    copyText(this.text, `${label} copied`);
  }
}
