// The request of a snippet (WP-145): the playground's builder state (or the
// Info panel's /info options) read as data: which client call, its source,
// the options as typed values, the stream settings, the query parameters
// and which keys it needs. The code model (model.js) is built from it;
// nothing here writes code.

import { activeEntries, argTypes, VIDEO_GRAMMARS } from '../grammar.js';

// typedValue turns one builder value into {k, v}: null (an empty
// argument), int, num, bool or str, from the catalogue type of its slot.
export function typedValue(v, type) {
  const s = String(v ?? '');
  if (s === '') return { k: 'null' };
  if (type === 'boolean') {
    const t = s.toLowerCase();
    if (['1', 't', 'true'].includes(t)) return { k: 'bool', v: true };
    if (['0', 'f', 'false'].includes(t)) return { k: 'bool', v: false };
  }
  if (type === 'integer' || type === 'number') {
    if (/^-?\d+$/.test(s) && Math.abs(Number(s)) <= Number.MAX_SAFE_INTEGER) return { k: 'int', v: String(Number(s)) };
    if (/^-?(\d+\.?\d*|\.\d+)(e[-+]?\d+)?$/i.test(s) && Number.isFinite(Number(s))) {
      const n = Number(s);
      return Number.isInteger(n) && Math.abs(n) < 1e21 ? { k: 'int', v: String(n) } : { k: 'num', v: String(n), t: type };
    }
  }
  return { k: 'str', v: s, t: type };
}

// paramValue types a native query value: whole numbers as ints, the rest
// as strings (written the same way by every client).
const paramValue = (s) => (/^(0|[1-9]\d{0,14})$/.test(s) ? { k: 'int', v: s } : { k: 'str', v: s });

// pipelineOf reads the active entries of a builder pipeline as
// [{name, args}] (base64 arguments already encoded, as in the URL).
function pipelineOf(entries, index) {
  const out = [];
  for (const [o, e, v] of activeEntries(entries, index, 'imgproxy')) {
    const types = argTypes(o, e);
    out.push({ name: o.name, args: v.map((x, i) => typedValue(x, types[i]?.type)) });
  }
  return out;
}

const STREAM_EXT = { m3u8: 'hls', mpd: 'dash' };
const SEGMENT_FORMAT = { fmp4: 'fmp4', cmaf: 'fmp4', ts: 'ts', mpegts: 'ts' };
const STREAM_OPTS = new Set(['stream', 'segment_duration', 'segment_format']);

// streamOf splits a /video pipeline into stream settings and the other
// options, or returns null when it is not a stream.
function streamOf(pipeline, ext) {
  const get = (n) => pipeline.find((o) => o.name === n);
  const st = get('stream');
  const fmt = get('format');
  const fmtExt = fmt?.args[0]?.k === 'str' ? fmt.args[0].v : '';
  let protocol = STREAM_EXT[ext] ?? STREAM_EXT[fmtExt] ?? null;
  if (st?.args[0]?.k === 'str' && ['hls', 'dash'].includes(st.args[0].v)) protocol = st.args[0].v;
  if (!protocol) return null;
  const renditions = (st?.args.slice(1) ?? []).filter((a) => a.k === 'int').map((a) => Number(a.v));
  const sgd = get('segment_duration')?.args[0];
  const sgf = get('segment_format')?.args[0];
  return {
    protocol,
    renditions,
    segmentDuration: sgd?.k === 'int' ? Number(sgd.v) : null,
    segmentFormat: sgf?.k === 'str' ? (SEGMENT_FORMAT[sgf.v] ?? null) : null,
    options: pipeline.filter((o) => !STREAM_OPTS.has(o.name) && !(o === fmt && STREAM_EXT[fmtExt])),
  };
}

const stripSlash = (s) => (s || '').replace(/\/+$/, '');

// snippetModel turns the session into a snippet model.
//   m: the mount; state: the builder state; index: the mount's catalogue
//   index (the video one when state.video); base: the public origin plus
//   the server's path prefix; signed/encrypt/pdf: the snippet settings;
//   url: the URL the page shows (for curl and notes).
//   queryMethod: send a query dialect request with HTTP QUERY (WP-147).
export function snippetModel({ m, state, index, base, signed, encrypt = false, pdf = false, url = '', accept = '', queryMethod = false }) {
  const model = { kind: 'none', url, signed: Boolean(signed), base: stripSlash(base), note: '', accept };
  if (!m || !state?.source) return { ...model, note: 'Choose a source.' };
  const prefix = stripSlash(m.prefix || '/');
  const separator = m.arguments_separator && m.arguments_separator !== ':' ? m.arguments_separator : '';
  if (m.grammar === 'imgproxy' && !state.raw) {
    const video = Boolean(state.video);
    const pls = (video ? state.pipelines.slice(0, 1) : state.pipelines).map((p) => pipelineOf(p, index));
    while (pls.length > 1 && pls.at(-1).length === 0) pls.pop();
    const common = {
      ...model, baseUrl: model.base + prefix, separator, source: state.source,
      sourceEncoding: encrypt ? 'encrypted' : (state.encoding === 'base64' ? 'base64' : 'plain'),
    };
    if (!video) {
      return finish({ ...common, kind: 'image', pipelines: pls, extension: state.ext || '', pdf });
    }
    const stream = streamOf(pls[0] ?? [], state.ext);
    if (stream) return finish({ ...common, kind: 'stream', ...stream });
    return finish({ ...common, kind: 'video', options: pls[0] ?? [], extension: state.ext || '' });
  }
  if (m.grammar === 'query') {
    const common = { ...model, baseUrl: model.base, mount: prefix, separator: '', source: state.source, queryMethod: Boolean(queryMethod) };
    if (state.raw) {
      const ep = /^\/([a-z]+)\/?$/.exec((state.free?.path || '').trim());
      const q = new URLSearchParams((state.free?.query || '').trim());
      const src = q.get('url');
      if (ep && ep[1] !== 'video' && src !== null) {
        const source = src.replaceAll('{src}', state.source).replaceAll('{key}', '');
        const params = [...q].filter(([k]) => k !== 'url').map(([k, v]) => [k, paramValue(v)]);
        return { ...common, kind: 'imaginary', endpoint: ep[1], source, params };
      }
      return xiSigModel(common, m, url);
    }
    if (state.video && VIDEO_GRAMMARS.includes('query')) {
      const opts = pipelineOf(state.pipelines[0] ?? [], index);
      const stream = streamOf(opts, '');
      if (stream) return finish({ ...common, kind: 'q-stream', ...stream });
      const fmt = opts.find((o) => o.name === 'format');
      return finish({ ...common, kind: 'q-video', options: opts.filter((o) => o !== fmt), extension: fmt?.args[0]?.v ?? '' });
    }
    const params = [];
    for (const [o, , v] of activeEntries(state.pipelines[0] ?? [], index, 'query')) {
      params.push([o.name, v.length ? paramValue(v.join(',')) : { k: 'str', v: 'true' }]);
    }
    return finish({ ...common, kind: 'q-image', params, pdf });
  }
  return xiSigModel(model, m, url);
}

// xiSigModel: a dialect without a client builder. Mounts signed with
// xi_sig get the clients' signXiUrl helper on the unsigned URL.
function xiSigModel(model, m, url) {
  const dialect = m.grammar === 'path' ? m.id : m.grammar;
  const note = `Official clients build imgproxy (/, /info, /video) and query (/q) URLs; ${dialect} URLs are written by hand or by that service's own SDK.`;
  if (m.scheme === 'xi_sig' && url && model.signed) {
    let unsigned = url;
    try {
      const u = new URL(url);
      u.searchParams.delete('xi_sig');
      unsigned = u.href.replace(/\?$/, '');
    } catch { /* keep the URL as it is */ }
    return { ...model, kind: 'xisig', url: unsigned, shownUrl: url, mount: stripSlash(m.prefix || '/'), note: `${note} This mount is signed with xi_sig: the snippet signs the URL with signXiUrl.` };
  }
  return { ...model, kind: 'none', note };
}

// finish adds the notes and forces the keys a request needs.
function finish(model) {
  const notes = [];
  // pdf_password carries an encrypted token: the clients take the plain
  // password instead (pdfPassword), so the option is left out.
  const opts = [model.pipelines, model.options].flat(2).filter(Boolean);
  if (opts.some((o) => o.name === 'pdf_password')) {
    const drop = (p) => p.filter((o) => o.name !== 'pdf_password');
    if (model.pipelines) model.pipelines = model.pipelines.map(drop);
    if (model.options) model.options = drop(model.options);
    notes.push('The pdf_password option is left out: the clients encrypt the plain password themselves (pdfPassword; type it in the Source panel).');
  }
  if (model.pdf && !model.signed) {
    notes.push('A PDF password needs a signed URL: the snippet signs it.');
    model.signed = true;
  }
  if (model.kind === 'stream' || model.kind === 'q-stream') {
    if (model.segmentFormat === 'ts' && model.protocol !== 'hls') notes.push('MPEG-TS segments are HLS only: the clients refuse ts on DASH.');
  }
  return { ...model, encryption: model.sourceEncoding === 'encrypted' || Boolean(model.pdf), note: notes.join(' ') };
}

// infoModel is the /info request of the Info panel.
export function infoModel({ m, source, entries, index, base, signed, encrypt = false, pdf = false, url = '' }) {
  const model = { kind: 'none', url, signed: Boolean(signed), base: stripSlash(base), note: '' };
  if (!m || !source) return { ...model, note: 'Choose a source.' };
  const separator = m.arguments_separator && m.arguments_separator !== ':' ? m.arguments_separator : '';
  return finish({
    ...model, kind: 'info', baseUrl: model.base + stripSlash(m.prefix || '/'), separator, source,
    sourceEncoding: encrypt ? 'encrypted' : 'plain', options: pipelineOf(entries, index), pdf,
  });
}
