// URL grammars: the builder state of one mount turned into the sign
// request {mount, path, query} (the path after the mount prefix, without
// a signature), plus the curl and TypeScript snippets. Pure functions.

import { b64url, escapePlain, escapeValue } from './dom.js';

// argTypes lists the argument of each value position of an entry (the
// form's args, then its rest group repeated).
export function argTypes(option, entry) {
  const form = option.forms[entry.form] ?? option.forms[0] ?? { args: [] };
  const out = [...(form.args ?? [])];
  for (let g = 0; g < (entry.groups ?? 1); g++) out.push(...(form.rest ?? []));
  return out;
}

export const isFlag = (option) => (option.forms[0]?.args?.length ?? 0) === 0 && !(option.forms[0]?.rest?.length);

// cleanValues trims the values, drops trailing empty ones and, for the
// imgproxy grammar, encodes base64 arguments.
export function cleanValues(option, entry, grammar) {
  const types = argTypes(option, entry);
  const vals = (entry.values ?? []).slice(0, types.length || 1).map((v, i) => {
    const s = String(v ?? '').trim();
    if (s && grammar === 'imgproxy' && types[i]?.type === 'base64') return b64url(s);
    return s;
  });
  while (vals.length && vals.at(-1) === '') vals.pop();
  return vals;
}

// isActive: a flag that is on, or an option with at least one value.
export function isActive(option, entry) {
  if (isFlag(option)) return Boolean(entry.on);
  return (entry.values ?? []).some((v) => String(v ?? '').trim() !== '');
}

// activeEntries yields [option, entry, values] for the active entries of
// a pipeline.
export function* activeEntries(pipeline, index, grammar) {
  for (const entry of pipeline) {
    const option = index.get(entry.name);
    if (option && isActive(option, entry)) yield [option, entry, cleanValues(option, entry, grammar)];
  }
}

const isHTTP = (s) => /^https?:\/\//i.test(s);

// keyOn returns the escaped key of src when it is xi://<name>/<key>.
function keyOn(src, name) {
  const p = `xi://${name}/`;
  if (!name || !src.startsWith(p)) return null;
  return src.slice(p.length).split('/').map(encodeURIComponent).join('/');
}

function decodedKeyOn(src, name) {
  const p = `xi://${name}/`;
  return name && src.startsWith(p) ? src.slice(p.length) : null;
}

const enc = encodeURIComponent;

// VIDEO_GRAMMARS build video transformations (/video, /q/video).
export const VIDEO_GRAMMARS = ['imgproxy', 'query'];

// build returns {mount, path, query}, or {error} when the state cannot be
// written for the mount (for example a source form the dialect lacks).
// state.video builds a video transformation (WP-127): /video on imgproxy
// mounts and /q/video on query mounts, from the video catalogue (index).
export function build(m, state, index) {
  const src = state.source;
  if (!src) return { error: 'Choose a source.' };
  const pls = state.pipelines;
  const first = pls[0] ?? [];
  // Raw mode: the path and query typed by hand, on any mount.
  const g = state.raw ? 'path' : m.grammar;
  const own = keyOn(src, m.source);
  const video = Boolean(state.video) && !state.raw;
  if (video && !VIDEO_GRAMMARS.includes(g)) {
    return { error: 'Video transformations are built on an imgproxy or query mount (/video, /q/video); "Every dialect" writes them for Cloudinary, ImageKit and Cloudflare.' };
  }
  if (video && pls.slice(1).some((p) => p.length)) return { error: 'Video URLs have no chained pipelines: remove pipeline 2 and later.' };
  const params = (sep, flagOnly) => [...activeEntries(first, index, g)].map(([o, , v]) =>
    (isFlag(o) ? (flagOnly ? enc(o.name) : `${enc(o.name)}=true`) : `${enc(o.name)}${sep}${enc(v.join(','))}`));
  switch (g) {
    case 'imgproxy': {
      const sep = m.arguments_separator || ':';
      const pipes = pls.map((p) => [...activeEntries(p, index, g)].map(([o, , v]) => {
        const name = state.alias && o.aliases?.length ? o.aliases[0] : o.name;
        return v.length ? [name, ...v].join(sep) : name;
      }));
      while (pipes.length > 1 && pipes.at(-1).length === 0) pipes.pop();
      const opts = pipes.map((p) => p.join('/')).filter((p, i) => p !== '' || i === 0);
      const optPath = opts.join('/-/');
      const ext = state.ext;
      const source = state.encoding === 'base64'
        ? `/${b64url(src)}${ext ? `.${ext}` : ''}`
        : `/plain/${escapePlain(src)}${ext ? `@${ext}` : ''}`;
      return { mount: m.id, path: (video ? '/video' : '') + (optPath ? `/${optPath}` : '') + source, query: '' };
    }
    case 'query': {
      if (!video) return { mount: m.id, path: '/', query: [`url=${enc(src)}`, ...params('=', false)].join('&') };
      // /q/video: every /video option is a key with its arguments joined
      // by ","; base64 arguments (wmu, wmt) are encoded as in /video paths.
      const keys = [...activeEntries(first, index, 'imgproxy')].map(([o, , v]) => `${enc(o.name)}=${enc(v.join(','))}`);
      return { mount: m.id, path: '/video', query: [`url=${enc(src)}`, ...keys].join('&') };
    }
    case 'imgix': {
      const path = own ?? (isHTTP(src) ? enc(src) : null);
      if (path === null) return { error: 'imgix takes an http(s) URL or a key on the mount\'s source.' };
      return { mount: m.id, path: `/${path}`, query: params('=', false).join('&') };
    }
    case 'cloudinary': {
      let kind; let id;
      if (own !== null) [kind, id] = ['upload', own];
      else if (isHTTP(src)) [kind, id] = ['fetch', escapePlain(src)];
      else return { error: 'Cloudinary takes an http(s) URL (fetch) or a public ID on the mount\'s source (upload).' };
      // Each chained pipeline is one component ("/"): a layer group is
      // l_… in one component and fl_layer_apply in a later one.
      const comps = pls.map((p) => [...activeEntries(p, index, g)].map(([o, , v]) => {
        const val = escapeValue(v.join(','));
        if (o.name.startsWith('e_') || o.name.startsWith('fl_')) return v.length ? `${o.name}:${val}` : o.name;
        return `${o.name}_${val}`;
      }).join(',')).filter((c) => c !== '');
      return { mount: m.id, path: `/xi/image/${kind}${comps.length ? `/${comps.join('/')}` : ''}/${id}`, query: '' };
    }
    case 'imagekit': {
      if (own === null) return { error: 'ImageKit takes a key on the mount\'s source (its URL endpoint).' };
      // A layer (l-image, l-text) is written l-<kind>,<its parameters>,l-end.
      const steps = pls.map((p) => [...activeEntries(p, index, g)].map(([o, , v]) => {
        if (!v.length) return o.name;
        const val = escapeValue(v.join(','));
        return o.name.startsWith('l-') ? `${o.name},${val},l-end` : `${o.name}-${val}`;
      }).join(',')).filter((s) => s !== '');
      return { mount: m.id, path: (steps.length ? `/tr:${steps.join(':')}` : '') + `/${own}`, query: '' };
    }
    case 'cloudflare': {
      let path = own;
      if (path === null) {
        if (!isHTTP(src)) return { error: 'Cloudflare takes an http(s) URL or a path on the mount\'s source.' };
        path = /[?#%]/.test(src) ? enc(src) : src;
      }
      const opts = [...activeEntries(first, index, g)].map(([o, , v]) => (v.length ? `${o.name}=${v.join(',')}` : `${o.name}=true`));
      return { mount: m.id, path: `/${opts.length ? opts.join(',') : 'fit=scale-down'}/${path}`, query: '' };
    }
    case 'wsrv': {
      const url = decodedKeyOn(src, m.source) ?? src;
      return { mount: m.id, path: '/', query: [`url=${enc(url)}`, ...params('=', true)].join('&') };
    }
    case 'thumbor': {
      const seg = { trim: '', crop: '', fit: '', size: '', halign: '', valign: '', smart: '' };
      const filters = [];
      for (const [o, , v] of activeEntries(first, index, g)) {
        if (o.name in seg) seg[o.name] = isFlag(o) ? o.name : v.join(',');
        else filters.push(`${o.name}(${v.join(',')})`);
      }
      if (seg.fit && !seg.size) seg.size = '0x0';
      const parts = [seg.trim, seg.crop, seg.fit, seg.size, seg.halign, seg.valign, seg.smart].filter(Boolean);
      if (filters.length) parts.push(`filters:${filters.join(':')}`);
      const image = own ?? escapePlain(src);
      return { mount: m.id, path: `/${[...parts, image].join('/')}`, query: '' };
    }
    case 'bunny': {
      if (own === null) return { error: 'Bunny Optimizer takes a path on the mount\'s source (its pull zone).' };
      return { mount: m.id, path: `/${own}`, query: params('=', false).join('&') };
    }
    case 'awssih': {
      const key = decodedKeyOn(src, m.source);
      if (key === null) return { error: 'AWS SIH takes an object key on the mount\'s source (its bucket).' };
      const body = { key };
      if (state.edits?.trim()) {
        try {
          body.edits = JSON.parse(state.edits);
        } catch {
          return { error: 'The edits are not valid JSON.' };
        }
      }
      const bytes = new TextEncoder().encode(JSON.stringify(body));
      let bin = '';
      for (const b of bytes) bin += String.fromCharCode(b);
      return { mount: m.id, path: `/${btoa(bin)}`, query: params('=', false).join('&') };
    }
    default: {
      // {src} is the source, {key} its key on the mount's source.
      if (`${state.free.path}${state.free.query}`.includes('{key}') && own === null) {
        return { error: `{key} needs a source on the mount's source (xi://${m.source || '<source>'}/…).` };
      }
      const fill = (s) => s.replaceAll('{src}', escapePlain(src)).replaceAll('{key}', own ?? '');
      const path = fill(state.free.path.trim() || '/');
      return { mount: m.id, path: path.startsWith('/') ? path : `/${path}`, query: fill(state.free.query.trim()) };
    }
  }
}

// colorValue writes #rrggbb in a grammar's colour syntax.
export function colorValue(grammar, hex) {
  const h = hex.replace(/^#/, '');
  switch (grammar) {
    case 'cloudinary': return `rgb:${h}`;
    case 'imagekit': return h.toUpperCase();
    case 'cloudflare': return `%23${h}`;
    default: return h;
  }
}

// ---- snippets ---------------------------------------------------------

const shq = (s) => `'${s.replaceAll("'", "'\\''")}'`;

export function curlSnippet(url, accept) {
  return `curl -sS -D - -o result.bin -H ${shq(`Accept: ${accept}`)} ${shq(url)}`;
}

const ident = /^[A-Za-z_$][\w$]*$/;

// tsArg writes one argument as a TypeScript literal of its catalogue type.
function tsArg(v, type) {
  if (v === '') return 'null';
  if ((type === 'integer' || type === 'number') && /^-?\d+(\.\d+)?$/.test(v)) return v;
  if (type === 'boolean') return ['1', 't', 'true'].includes(v) ? 'true' : 'false';
  return JSON.stringify(v);
}

// curlInfoSnippet asks for an /info URL's JSON.
export function curlInfoSnippet(url) {
  return `curl -sS -H 'Accept: application/json' ${shq(url)}`;
}

// tsInfoSnippet writes the fluent /info call of @xtandard/image-url
// (img.info(source).<option>(...args).build()) for entries of the info
// catalogue index.
export function tsInfoSnippet({ source, entries, index, base, signed, separator, encoding }) {
  if (!source) return null;
  const cfg = [`baseUrl: ${JSON.stringify(base)}`, `sourceEncoding: ${JSON.stringify(encoding === 'base64' ? 'base64' : 'plain')}`];
  if ((separator || ':') !== ':') cfg.push(`separator: ${JSON.stringify(separator)}`);
  if (signed) cfg.push('signing: { key: process.env.XI_SIGNING_KEY!, salt: process.env.XI_SIGNING_SALT! }');
  const calls = [];
  for (const [o, e, v] of activeEntries(entries, index, 'imgproxy')) {
    const types = argTypes(o, e);
    const args = v.map((x, j) => tsArg(x, types[j]?.type)).join(', ');
    calls.push(ident.test(o.name) ? `.${o.name}(${args})` : `.option(${JSON.stringify(o.name)}${args ? `, ${args}` : ''})`);
  }
  return [
    'import { imgproxy } from "@xtandard/image-url";',
    '',
    `const img = imgproxy({ ${cfg.join(', ')} });`,
    'const url = await img',
    `  .info(${JSON.stringify(source)})`,
    ...calls.map((c) => `  ${c}`),
    '  .build();',
    'const info = await (await fetch(url, { headers: { Accept: "application/json" } })).json();',
  ].join('\n');
}

// tsSnippet writes the fluent @xtandard/image-url call for an imgproxy
// mount (null for other grammars).
export function tsSnippet(m, state, index, base, signed) {
  if (m.grammar !== 'imgproxy' || !state.source) return null;
  const video = Boolean(state.video);
  const cfg = [`baseUrl: ${JSON.stringify(base)}`, `sourceEncoding: ${JSON.stringify(state.encoding === 'base64' ? 'base64' : 'plain')}`];
  if ((m.arguments_separator || ':') !== ':') cfg.push(`separator: ${JSON.stringify(m.arguments_separator)}`);
  if (signed) cfg.push('signing: { key: process.env.XI_SIGNING_KEY!, salt: process.env.XI_SIGNING_SALT! }');
  const calls = [];
  state.pipelines.forEach((p, i) => {
    if (i > 0 && video) return;
    if (i > 0) calls.push('.chain()');
    for (const [o, e, v] of activeEntries(p, index, 'imgproxy')) {
      const types = argTypes(o, e);
      const args = v.map((x, j) => tsArg(x, types[j]?.type)).join(', ');
      calls.push(ident.test(o.name) ? `.${o.name}(${args})` : `.option(${JSON.stringify(o.name)}${args ? `, ${args}` : ''})`);
    }
  });
  if (state.ext) calls.push(`.extension(${JSON.stringify(state.ext)})`);
  return [
    'import { imgproxy } from "@xtandard/image-url";',
    '',
    `const img = imgproxy({ ${cfg.join(', ')} });`,
    `const url = await img`,
    `  .${video ? 'video' : 'from'}(${JSON.stringify(state.source)})`,
    ...calls.map((c) => `  ${c}`),
    '  .build();',
  ].join('\n');
}
