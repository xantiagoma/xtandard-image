// Format matrix: every sample (built-in and public) probed with /info and
// a 160 px thumbnail through the first imgproxy mount, two at a time, to
// check "a file of every format works".

import { $, el, errorText, escapePlain, fmtBytes, fmtMs } from './dom.js';

const CONCURRENCY = 2;

export class Matrix {
  constructor({ meta, sign }) {
    this.meta = meta;
    this.sign = sign;
    this.mount = meta.mounts.find((m) => m.grammar === 'imgproxy' && m.prefix);
    this.rows = [];
    this.built = false;
    this.sort = null; // {key, dir}
    $('matrix-run').addEventListener('click', () => this.runAll());
    for (const th of $('matrix').tHead.querySelectorAll('th[data-sort]')) {
      th.querySelector('button').addEventListener('click', () => this.sortBy(th.dataset.sort));
    }
  }

  // sortBy orders the rows by a column; a second click reverses. Status
  // puts failures first, then rows not run yet, successes, and samples no
  // source allows.
  sortBy(key) {
    const dir = this.sort?.key === key && this.sort.dir === 1 ? -1 : 1;
    this.sort = { key, dir };
    const rank = (r) => (!r.e.allowed ? 3 : r.ok === false ? 0 : r.ok ? 2 : 1);
    const value = {
      format: (r) => r.e.format,
      name: (r) => r.e.name,
      time: (r) => r.ms ?? Infinity,
      status: rank,
    }[key];
    const rows = [...this.rows].sort((a, b) => {
      const x = value(a);
      const y = value(b);
      const c = typeof x === 'string' ? x.localeCompare(y) : x - y;
      return (c || a.i - b.i) * dir;
    });
    $('matrix').tBodies[0].append(...rows.map((r) => r.tr));
    for (const th of $('matrix').tHead.querySelectorAll('th[data-sort]')) {
      th.setAttribute('aria-sort', th.dataset.sort === key ? (dir === 1 ? 'ascending' : 'descending') : 'none');
    }
  }

  entries() {
    const out = (this.meta.samples ?? []).map((s) => ({ format: s.kind, name: `${s.label} (built-in)`, source: s.source, allowed: true }));
    for (const g of this.meta.gallery ?? []) out.push({ format: g.format, name: g.name, source: g.url, allowed: g.allowed, bytes: g.bytes });
    return out;
  }

  build() {
    if (this.built) return;
    this.built = true;
    const body = $('matrix').tBodies[0];
    if (!this.mount) {
      $('matrix-status').textContent = 'No imgproxy mount with a prefix: the matrix needs one.';
      $('matrix-run').disabled = true;
      return;
    }
    for (const e of this.entries()) {
      const cells = { probe: el('td'), thumb: el('td'), time: el('td'), bytes: el('td'), result: el('td') };
      const retry = el('button', { type: 'button', class: 'small', text: 'Run', 'aria-label': `Run ${e.name}` });
      const tr = el('tr', {}, el('td', { text: e.format }), el('td', { text: e.name, title: e.source }),
        cells.probe, cells.thumb, cells.time, cells.bytes, cells.result, el('td', {}, retry));
      const row = { e, tr, cells, i: this.rows.length };
      retry.addEventListener('click', () => this.run(row));
      if (!e.allowed) {
        cells.result.textContent = 'no http source allows this host';
        retry.disabled = true;
        tr.classList.add('off');
      }
      this.rows.push(row);
      body.append(tr);
    }
  }

  async runAll() {
    const todo = this.rows.filter((r) => r.e.allowed);
    let done = 0;
    const status = $('matrix-status');
    const worker = async () => {
      while (todo.length) {
        const r = todo.shift();
        await this.run(r);
        done++;
        status.textContent = `${done} done`;
      }
    };
    $('matrix-run').disabled = true;
    await Promise.all(Array.from({ length: CONCURRENCY }, worker));
    $('matrix-run').disabled = false;
    const ok = this.rows.filter((r) => r.ok).length;
    status.textContent = `${ok} of ${this.rows.length} samples rendered.`;
    if (this.sort) {
      const { key, dir } = this.sort;
      this.sort = { key, dir: -dir }; // sortBy flips it back
      this.sortBy(key);
    }
  }

  async run(row) {
    const { e, cells } = row;
    for (const c of Object.values(cells)) c.replaceChildren();
    cells.result.textContent = '…';
    row.ok = undefined;
    row.ms = undefined;
    const src = escapePlain(e.source);
    const prefix = (this.meta.path_prefix || '').replace(/\/+$/, '') + (this.mount.prefix || '/').replace(/\/+$/, '');
    const started = performance.now();
    try {
      const infoSigned = await this.sign({ mount: this.mount.id, path: `/pn:1/plain/${src}`, query: '' });
      const infoURL = `${prefix}/info${infoSigned.url.slice(prefix.length)}`;
      const ires = await fetch(`api/render?u=${encodeURIComponent(infoURL)}`, { headers: { Accept: 'application/json' } });
      const itext = await ires.text();
      if (ires.ok) {
        const info = JSON.parse(itext);
        cells.probe.append(el('span', { class: 'badge ok', text: `${info.format} ${info.width}×${info.height}` }));
      } else {
        cells.probe.append(el('span', { class: 'badge err', text: `${ires.status}`, title: errorText(itext, ires.status) }));
      }
      const tSigned = await this.sign({ mount: this.mount.id, path: `/rs:fit:160:160/f:webp/plain/${src}`, query: '' });
      const tres = await fetch(`api/render?u=${encodeURIComponent(tSigned.url)}`, { headers: { Accept: 'image/webp,*/*' } });
      const blob = await tres.blob();
      const elapsed = performance.now() - started;
      row.ms = elapsed;
      cells.time.textContent = fmtMs(elapsed);
      cells.result.replaceChildren();
      if (tres.ok) {
        cells.thumb.append(el('img', { src: URL.createObjectURL(blob), alt: `${e.name} thumbnail`, class: 'mthumb' }));
        cells.bytes.textContent = `${fmtBytes(Number(tres.headers.get('x-xi-origin-content-length')) || e.bytes)} → ${fmtBytes(blob.size)}`;
        cells.result.append(el('span', { class: 'badge ok', text: 'ok' }));
        row.ok = true;
      } else {
        const reason = errorText(await blob.text(), tres.status);
        cells.result.append(el('span', { class: 'badge err', text: `${tres.status}` }), ` ${reason}`);
        row.ok = false;
      }
    } catch (err) {
      cells.result.textContent = err.message;
      row.ok = false;
    }
  }
}
