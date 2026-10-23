// The code model of a snippet (WP-145): typed nodes, a CodeDOM in the
// manner of Kiota's. neutral() turns a request (request.js) into a
// language-neutral program of domain nodes (client, request, signing and
// encryption keys, environment reads, options, enums); each language's
// refiner (lang/<id>.js) lowers those into the generic nodes below, in the
// shape that language's client wants (a builder chain, a config object,
// keyword arguments, an error check after each call); its printer maps the
// generic nodes to a Doc (doc.js) whose line breaks the layout decides.

// ---- generic nodes -----------------------------------------------------------

export const N = {
  // Statements and declarations.
  program: (body, { imports = [], header = [] } = {}) => ({ k: 'program', imports, header, body }),
  imp: (module, names = [], opts = {}) => ({ k: 'import', module, names, ...opts }),
  let: (names, value, opts = {}) => ({ k: 'let', names: [].concat(names), value, ...opts }),
  stmt: (expr, opts = {}) => ({ k: 'stmt', expr, ...opts }),
  ret: (value) => ({ k: 'return', value }),
  if: (cond, body) => ({ k: 'if', cond, body }),
  func: (name, body, opts = {}) => ({ k: 'func', name, body, ...opts }),
  cls: (name, body, opts = {}) => ({ k: 'class', name, body, ...opts }),
  // A statement made of a keyword and its operands (echo, require).
  cmd: (keyword, args) => ({ k: 'cmd', keyword, args }),
  blank: () => ({ k: 'blank' }),

  // Expressions.
  lit: (v) => ({ k: 'lit', v }),
  str: (s) => ({ k: 'lit', v: { k: 'str', v: s } }),
  int: (n) => ({ k: 'lit', v: { k: 'int', v: String(n) } }),
  bool: (b) => ({ k: 'lit', v: { k: 'bool', v: b } }),
  nil: () => ({ k: 'lit', v: { k: 'null' } }),
  id: (name) => ({ k: 'id', name }),
  type: (name) => ({ k: 'type', name }),
  sym: (name) => ({ k: 'sym', name }),
  member: (obj, name, opts = {}) => ({ k: 'member', obj, name, ...opts }),
  call: (fn, args = [], opts = {}) => ({ k: 'call', fn, args, ...opts }),
  named: (name, value) => ({ k: 'named', name, value }),
  index: (obj, key) => ({ k: 'index', obj, key }),
  unwrap: (expr) => ({ k: 'unwrap', expr }),
  try: (expr) => ({ k: 'try', expr }),
  ref: (expr) => ({ k: 'ref', expr }),
  await: (expr) => ({ k: 'await', expr }),
  new: (type, args = [], opts = {}) => ({ k: 'new', type, args, ...opts }),
  list: (items, style = 'list') => ({ k: 'list', items, style }),
  map: (entries, style = 'map') => ({ k: 'map', entries, style }),
  entry: (key, value) => ({ key, value }),
  composite: (type, items) => ({ k: 'composite', type, items }),
  binary: (op, left, right) => ({ k: 'binary', op, left, right }),
  // A lambda body passed as the last argument (Kotlin's DSL blocks).
  block: (stmts) => ({ k: 'block', stmts }),
};

// ---- domain nodes (language-neutral; refiners lower them) --------------------

export const D = {
  env: (name) => ({ k: 'env', name }),
  signingKey: () => ({ k: 'signing-key', key: D.env(ENV.key), salt: D.env(ENV.salt) }),
  encryptionKey: (fixedIv) => ({ k: 'encryption-key', key: D.env(ENV.encKey), iv: fixedIv ? D.env(ENV.encIv) : null }),
  // client: an imgproxy or query URL builder and its settings.
  client: (kind, config) => ({ k: 'client', kind, config }),
  // request: one URL from the client (op: url, infoUrl, videoUrl,
  // streamUrl, imaginaryUrl), with the request's data.
  request: (client, op, data) => ({ k: 'request', client, op, data }),
  signXiUrl: (url, mount) => ({ k: 'sign-xi-url', url, mount, key: D.signingKey() }),
  print: (expr) => ({ k: 'print', expr }),
  // queryRequest splits a query dialect URL into an HTTP QUERY request
  // (the client's queryMethodRequest, WP-147); printRequest prints it as
  // "QUERY <url>", a newline and the content.
  queryRequest: (url) => ({ k: 'query-request', url }),
  printRequest: (req) => ({ k: 'print-request', req }),
};

// QUERY_LANGUAGES are the languages whose refiners lower queryRequest and
// printRequest; the others show the GET snippet with a note.
export const QUERY_LANGUAGES = new Set(['typescript', 'python', 'go']);

export const ENV = {
  key: 'XI_SIGNING_KEY',
  salt: 'XI_SIGNING_SALT',
  encKey: 'XI_ENCRYPTION_KEY',
  encIv: 'XI_ENCRYPTION_IV',
  pdf: 'XI_PDF_PASSWORD',
};

const QUERY_KINDS = new Set(['q-image', 'q-video', 'q-stream', 'imaginary']);
const OPS = { image: 'url', 'q-image': 'url', info: 'infoUrl', video: 'videoUrl', 'q-video': 'videoUrl', stream: 'streamUrl', 'q-stream': 'streamUrl', imaginary: 'imaginaryUrl' };

// neutral is the language-neutral program of request r: a client, the URL
// it builds, and the URL printed. opts.fixedIv reads a fixed IV from
// XI_ENCRYPTION_IV (the check script's reproducible enc/ tokens).
export function neutral(r, { fixedIv = false } = {}) {
  if (r.kind === 'xisig') {
    return N.program([N.let('url', D.signXiUrl(r.url, r.mount)), D.print(N.id('url'))]);
  }
  const query = QUERY_KINDS.has(r.kind);
  const config = {
    baseUrl: r.baseUrl ?? r.base,
    mount: query && r.mount !== '/q' ? r.mount : null,
    signing: r.signed ? D.signingKey() : null,
    encryption: r.encryption ? D.encryptionKey(fixedIv) : null,
    sourceEncoding: !query && r.sourceEncoding && r.sourceEncoding !== 'base64' ? r.sourceEncoding : null,
    separator: r.separator || null,
  };
  const client = query ? 'q' : 'img';
  const data = {
    source: r.source,
    // Pipelines: [[{name, args: [typed value]}]] (chained ones for image URLs).
    pipelines: r.pipelines ? r.pipelines.filter((p, i) => p.length || i === 0) : [r.options ?? []],
    extension: r.extension || null,
    pdfPassword: r.pdf ? D.env(ENV.pdf) : null,
    protocol: r.protocol ?? null,
    renditions: r.renditions ?? [],
    segmentDuration: r.segmentDuration ?? null,
    segmentFormat: r.segmentFormat ?? null,
    params: r.params ?? [],
    endpoint: r.endpoint ?? null,
    catalogue: r.kind === 'info' ? 'info' : ['video', 'stream', 'q-video', 'q-stream'].includes(r.kind) ? 'video' : 'processing',
  };
  const build = [N.let(client, D.client(query ? 'query' : 'imgproxy', config)), N.let('url', D.request(client, OPS[r.kind], data))];
  if (query && r.queryMethod) {
    return N.program([...build, N.let('req', D.queryRequest(N.id('url'))), D.printRequest(N.id('req'))]);
  }
  return N.program([...build, D.print(N.id('url'))]);
}

// ---- helpers for refiners ------------------------------------------------------

// isNode tells nodes from plain data.
const isNode = (v) => v !== null && typeof v === 'object' && typeof v.k === 'string';

// lower rewrites a tree bottom-up: children first (nodes in fields, in
// arrays, and in a client's config or a request's data), then rules[node.k]
// (when there is one) replaces the node. Plain data is left as it is for
// the rule to read.
export function lower(node, rules, ctx) {
  if (Array.isArray(node)) return node.map((n) => lower(n, rules, ctx));
  if (!isNode(node)) return node;
  const out = { ...node };
  for (const [key, value] of Object.entries(node)) {
    if (isNode(value) || (Array.isArray(value) && value.some(isNode))) out[key] = lower(value, rules, ctx);
    else if ((key === 'config' || key === 'data') && value && typeof value === 'object' && !Array.isArray(value)) {
      out[key] = Object.fromEntries(Object.entries(value).map(([ck, cv]) => [ck, isNode(cv) ? lower(cv, rules, ctx) : cv]));
    }
  }
  const rule = rules[out.k];
  return rule ? rule(out, ctx) : out;
}

// Unexpressible marks a request a language's client cannot express; its
// message is shown instead of code.
export class Unexpressible extends Error {}

// uniqueNames refuses a pipeline that repeats an option for the mapping
// forms (a mapping holds each name once).
export function uniqueNames(pipelines, language) {
  for (const p of pipelines) {
    const seen = new Set();
    for (const o of p) {
      if (seen.has(o.name)) throw new Unexpressible(`${o.name} is repeated in one pipeline: ${language}'s options mapping holds each name once (use the fluent form's option() twice).`);
      seen.add(o.name);
    }
  }
}

// words splits a name on "-", "_" and case changes; the casings below build
// identifiers from catalogue names.
export const words = (name) => name.replace(/([a-z0-9])([A-Z])/g, '$1 $2').split(/[^A-Za-z0-9]+/).filter(Boolean);
export const camel = (name) => words(name).map((w, i) => (i ? w[0].toUpperCase() + w.slice(1) : w)).join('');
export const pascal = (name) => words(name).map((w) => w[0].toUpperCase() + w.slice(1)).join('');
export const snake = (name) => words(name).map((w) => w.toLowerCase()).join('_');
