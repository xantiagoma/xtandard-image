// Small DOM and formatting helpers shared by the playground modules.

export const $ = (id) => document.getElementById(id);

// el creates an element: attrs "text" sets textContent, true sets a
// boolean attribute, false/null/undefined are skipped, "on<event>"
// functions become listeners.
export function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k === 'text') e.textContent = v;
    else if (k.startsWith('on') && typeof v === 'function') e.addEventListener(k.slice(2), v);
    else if (v === true) e.setAttribute(k, '');
    else e.setAttribute(k, String(v));
  }
  e.append(...children.filter((c) => c !== null && c !== undefined && c !== false));
  return e;
}

export function svg(tag, attrs = {}) {
  const e = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, String(v));
  return e;
}

export function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

let ids = 0;
export const uid = (p = 'x') => `${p}-${++ids}`;

export function toast(msg) {
  const t = $('toast');
  t.textContent = msg;
  t.classList.add('show');
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => t.classList.remove('show'), 2200);
}

export async function copyText(text, what = 'Copied') {
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // Clipboard API unavailable (http origin): fall back to a hidden textarea.
    const ta = el('textarea', { class: 'sr', readonly: true });
    ta.value = text;
    document.body.append(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
  }
  toast(`${what}.`);
}

export function fmtBytes(n) {
  if (n === undefined || n === null || Number.isNaN(n)) return '–';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / 1024 / 1024).toFixed(2)} MiB`;
}

export const fmtMs = (ms) => (ms < 10 ? `${ms.toFixed(2)} ms` : `${ms.toFixed(0)} ms`);

export function b64url(s) {
  const bytes = new TextEncoder().encode(s);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

export function unb64url(s) {
  const bin = atob(s.replaceAll('-', '+').replaceAll('_', '/'));
  return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)));
}

// escapePlain percent-encodes everything but unreserved characters and
// ":/", like the server's translate emitter: "?", "#", "@" and "%" never
// end an imgproxy plain/ source early.
export function escapePlain(s) {
  let out = '';
  for (const b of new TextEncoder().encode(s)) {
    const c = String.fromCharCode(b);
    out += /[A-Za-z0-9\-._~:/]/.test(c) ? c : `%${b.toString(16).toUpperCase().padStart(2, '0')}`;
  }
  return out;
}

// escapeValue percent-encodes an option value written into a path
// segment (Cloudinary components, ImageKit steps): spaces, non-ASCII text
// and "/?#" are encoded; sub-delimiters, ":" and "@" stay, and so does
// "%", so a value typed already encoded ("%252C") is left alone.
export function escapeValue(s) {
  let out = '';
  for (const b of new TextEncoder().encode(s)) {
    const c = String.fromCharCode(b);
    out += /[A-Za-z0-9\-._~:@!$&'()*+,;=%]/.test(c) ? c : `%${b.toString(16).toUpperCase().padStart(2, '0')}`;
  }
  return out;
}

// errorText reads a JSON error body ({"error": "..."} or the server's
// {"error": {"kind", "message"}}) or plain text.
export function errorText(text, status) {
  try {
    const j = JSON.parse(text);
    if (typeof j.error === 'string') return j.error;
    if (j.error?.message) return `${j.error.kind ?? 'error'}: ${j.error.message}`;
  } catch { /* not JSON */ }
  return text ? text.slice(0, 300) : `HTTP ${status}`;
}

export async function postJSON(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(errorText(text, res.status));
  return JSON.parse(text);
}

// store wraps localStorage: per-browser conveniences only, never required.
export const store = {
  get(key, fallback) {
    try { return JSON.parse(localStorage.getItem(key) ?? 'null') ?? fallback; } catch { return fallback; }
  },
  set(key, value) {
    try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* storage unavailable */ }
  },
};

// tabs wires a WAI-ARIA tab list: arrow keys move, the selected tab shows
// its panel. onChange receives the tab id.
export function tabs(listId, onChange = () => {}) {
  const list = $(listId);
  const all = () => [...list.querySelectorAll('[role=tab]')];
  const select = (tab, focus = false) => {
    for (const t of all()) {
      const on = t === tab;
      t.setAttribute('aria-selected', String(on));
      t.tabIndex = on ? 0 : -1;
      const pane = $(t.getAttribute('aria-controls'));
      if (pane) pane.hidden = !on;
    }
    if (focus) tab.focus();
    onChange(tab.id);
  };
  list.addEventListener('click', (e) => {
    const t = e.target.closest('[role=tab]');
    if (t) select(t);
  });
  list.addEventListener('keydown', (e) => {
    const ts = all();
    const i = ts.indexOf(document.activeElement);
    if (i < 0) return;
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      select(ts[(i + (e.key === 'ArrowRight' ? 1 : ts.length - 1)) % ts.length], true);
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      select(ts[e.key === 'Home' ? 0 : ts.length - 1], true);
    }
  });
  for (const t of all()) t.tabIndex = t.getAttribute('aria-selected') === 'true' ? 0 : -1;
  return { select: (id) => select($(id)) };
}
