// Global tools (WP-111): views that are not about the current asset, so
// they live outside the session workspace, behind the header's Views
// navigation and their own hash routes (#tools/<name>). Opening one hides
// the workspace without touching it; closing it shows the workspace again
// and puts the session back in the hash, so the share link keeps working.
//
// History lists the sessions rendered in this browser (localStorage);
// Examples (recipes.js) lists ready-made sessions.

import { $, copyText, el, store } from './dom.js';

export const TOOLS = {
  gallery: { title: 'Gallery', caption: 'Global tool: every sample this server knows. Picking one loads it into the session; nothing else in the session changes.' },
  history: { title: 'History', caption: 'Global tool: the sessions you rendered in this browser, for every asset, not only the current one.' },
  matrix: { title: 'Format matrix', caption: 'Global tool: the whole sample gallery run against this server, independent of the current session.' },
  examples: { title: 'Examples', caption: 'Global tool: ready-made sessions (watermarks in every dialect, and more). Loading one replaces the session with it; everything stays editable, and Back returns here.' },
};

// toolFromHash is the tool a hash routes to, or null.
export function toolFromHash(hash = location.hash) {
  const m = /^#tools\/([a-z]+)$/.exec(hash);
  return m && Object.hasOwn(TOOLS, m[1]) ? m[1] : null;
}

export class Tools {
  // sessionHash() is the #v1: hash of the current session; onOpen(name)
  // runs when a tool is shown (lazy builds).
  constructor({ sessionHash, onOpen }) {
    this.sessionHash = sessionHash;
    this.onOpen = onOpen;
    this.current = null;
    this.opener = null;
    $('tool-close').addEventListener('click', () => this.close());
    $('nav-session').addEventListener('click', (e) => {
      e.preventDefault();
      if (this.current) this.close();
    });
    // The session is written to the current history entry before the
    // link pushes #tools/<name>, so Back returns to it.
    for (const a of document.querySelectorAll('#tools-nav a[data-tool]')) {
      a.addEventListener('click', () => {
        if (!this.current) {
          this.opener = a;
          history.replaceState(null, '', this.sessionHash());
        }
      });
    }
    // Skip links focus their target without touching the hash.
    for (const a of document.querySelectorAll('a.skip')) {
      a.addEventListener('click', (e) => {
        const t = $(a.getAttribute('href').slice(1));
        if (!t) return;
        e.preventDefault();
        if (!t.hasAttribute('tabindex')) t.tabIndex = -1;
        t.focus();
      });
    }
    this.refreshNav();
  }

  open(name) {
    const t = TOOLS[name];
    if (!t) return;
    if (!this.current && !this.opener) this.opener = document.activeElement;
    this.current = name;
    $('workspace').hidden = true;
    $('tools').hidden = false;
    for (const id of Object.keys(TOOLS)) $(`tool-${id}`).hidden = id !== name;
    $('tool-title').textContent = t.title;
    $('tool-caption').textContent = t.caption;
    document.title = `${t.title} · xtandard-image playground`;
    this.refreshNav();
    this.onOpen(name);
    $('tool-title').focus();
  }

  // close shows the workspace again as it was; the hash becomes the
  // session's (replaced, not pushed: Back still leads out of the tool
  // history the browser already has).
  close({ writeHash = true } = {}) {
    if (!this.current) return;
    this.current = null;
    $('tools').hidden = true;
    $('workspace').hidden = false;
    document.title = 'xtandard-image playground';
    if (writeHash) history.replaceState(null, '', this.sessionHash());
    this.refreshNav();
    const back = this.opener?.isConnected && !this.opener.closest('#tools') ? this.opener : $('preview-title');
    this.opener = null;
    back.focus();
  }

  // refreshNav marks the current view and keeps the Session link pointing
  // at the session (open in a new tab works).
  refreshNav() {
    const session = $('nav-session');
    session.href = this.sessionHash();
    for (const a of document.querySelectorAll('#tools-nav a')) {
      const on = a.dataset.tool ? a.dataset.tool === this.current : !this.current;
      if (on) a.setAttribute('aria-current', 'page');
      else a.removeAttribute('aria-current');
    }
    for (const a of document.querySelectorAll('a.skip[data-view]')) a.hidden = a.dataset.view !== (this.current ? 'tools' : 'session');
  }
}

const HISTORY = 'xi-playground-history';
const HISTORY_MAX = 30;

// History: the sessions rendered in this browser, newest first; each
// entry {url, hash, when, mount, label, thumb?} opens its session or
// copies its share link.
export class History {
  constructor({ open, shareURL }) {
    this.open = open; // (entry) => void
    this.shareURL = shareURL; // (hash) => absolute URL
    $('history-clear').addEventListener('click', () => {
      store.set(HISTORY, []);
      this.render();
      $('history-status').textContent = 'History cleared.';
    });
    $('history-search').addEventListener('input', () => this.render());
  }

  list() { return store.get(HISTORY, []).filter((h) => h && typeof h.url === 'string' && typeof h.hash === 'string'); }

  push(entry) {
    const list = this.list().filter((h) => h.url !== entry.url);
    list.unshift(entry);
    store.set(HISTORY, list.slice(0, HISTORY_MAX));
    if (!$('tool-history').hidden) this.render();
  }

  render() {
    const box = $('history');
    const q = $('history-search').value.trim().toLowerCase();
    const all = this.list();
    const shown = all.filter((h) => !q || `${h.url} ${h.label ?? ''} ${h.mount ?? ''}`.toLowerCase().includes(q));
    box.replaceChildren();
    $('history-status').textContent = all.length ? `${shown.length} of ${all.length} sessions shown.` : '';
    if (!all.length) box.append(el('li', { class: 'hint empty', text: 'Rendered sessions appear here (kept in this browser only).' }));
    else if (!shown.length) box.append(el('li', { class: 'hint empty', text: 'No session matches the search.' }));
    for (const h of shown) {
      const when = new Date(h.when);
      const title = h.label || h.url;
      const thumb = h.thumb?.startsWith('data:image/')
        ? el('img', { class: 'hthumb', src: h.thumb, alt: '' })
        : el('span', { class: 'hthumb none', 'aria-hidden': 'true', text: '—' });
      const open = el('button', { type: 'button', class: 'small primary', text: 'Open', 'aria-label': `Open the session ${title}` });
      open.addEventListener('click', () => this.open(h));
      const copy = el('button', { type: 'button', class: 'small', text: 'Copy link', 'aria-label': `Copy the share link of ${title}` });
      copy.addEventListener('click', () => copyText(this.shareURL(h.hash), 'Share link copied'));
      box.append(el('li', { class: 'hentry' }, thumb,
        el('div', { class: 'hbody' },
          el('div', { class: 'hmeta' },
            el('strong', { text: title }), ' ',
            el('time', { datetime: when.toISOString(), title: when.toISOString(), text: when.toLocaleString() }),
            el('span', { class: 'hint', text: ` · ${h.mount}` })),
          el('code', { class: 'hurl', text: h.url })),
        el('div', { class: 'hactions' }, open, copy)));
    }
  }
}
