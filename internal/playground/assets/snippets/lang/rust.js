// Rust: the xtandard-image crate. Builder methods on ImgproxyUrlBuilder /
// QueryUrlBuilder, Options::new().set(name, args) for options, params
// structs, `?` for every fallible call, keys from std::env::var, inside a
// main that returns a Result.

import { concat, join } from '../doc.js';
import { lower, N, uniqueNames } from '../model.js';
import { Printer } from '../printer.js';

const ty = (ctx, name) => (ctx.uses.add(name), N.type(name));
const assoc = (ctx, type, name) => N.member(ty(ctx, type), name, { static: true });
const chain = (head, links) => links.reduce((obj, [name, args]) => N.call(N.member(obj, name), args), head);
const lit = (ctx, a) => (a.k === 'null' ? assoc(ctx, 'Arg', 'Null') : N.lit(a));
const value = (ctx, args) => (args.length === 1 ? lit(ctx, args[0]) : N.list(args.map((a) => lit(ctx, a)), 'tuple'));
const options = (ctx, p) => chain(N.call(assoc(ctx, 'Options', 'new')), p.map((o) => ['set', [N.str(o.name), value(ctx, o.args)]]));

const rules = {
  env: (e) => N.try(N.call(N.member(N.member(N.type('std'), 'env', { static: true }), 'var', { static: true }), [N.str(e.name)])),
  'signing-key': (e, ctx) => N.try(N.call(assoc(ctx, 'SigningKey', 'new'), [e.key, e.salt])),
  'encryption-key': (e, ctx) => {
    const key = N.try(N.call(assoc(ctx, 'EncryptionKey', 'new'), [e.key]));
    return e.iv ? N.try(N.call(N.member(key, 'with_iv'), [e.iv])) : key;
  },
  client: (e, ctx) => {
    const c = e.config;
    const links = [['base_url', [N.str(c.baseUrl)]]];
    if (c.mount) links.push(['mount', [N.str(c.mount)]]);
    if (c.signing) links.push(['signing', [c.signing]]);
    if (c.encryption) links.push(['encryption', [c.encryption]]);
    if (c.sourceEncoding) links.push(['source_encoding', [assoc(ctx, 'SourceEncoding', c.sourceEncoding === 'plain' ? 'Plain' : 'Encrypted')]]);
    if (c.separator) links.push(['separator', [N.lit({ k: 'char', v: c.separator })]]);
    return chain(N.call(assoc(ctx, e.kind === 'query' ? 'QueryUrlBuilder' : 'ImgproxyUrlBuilder', 'new')), links);
  },
  request: (e, ctx) => {
    const d = e.data;
    uniqueNames(d.pipelines, 'Rust');
    const pls = d.pipelines;
    const v = N.id(e.client);
    const call = (method, args) => N.try(N.call(N.member(v, method), args));
    const opts = pls.length > 1 ? N.list(pls.map((p) => options(ctx, p)), 'array') : options(ctx, pls[0]);
    switch (e.op) {
      case 'url': {
        if (e.client === 'q') {
          const links = d.params.map(([k, a]) => ['param', [N.str(k), lit(ctx, a)]]);
          if (d.pdfPassword) links.push(['pdf_password', [d.pdfPassword]]);
          return call('url', [N.str(d.source), N.call(assoc(ctx, 'Options', 'new')), chain(N.call(assoc(ctx, 'QueryUrlParams', 'new')), links)]);
        }
        const links = [];
        if (d.extension) links.push(['extension', [N.str(d.extension)]]);
        if (d.pdfPassword) links.push(['pdf_password', [d.pdfPassword]]);
        return call('url', [N.str(d.source), opts, chain(N.call(assoc(ctx, 'UrlParams', 'new')), links)]);
      }
      case 'infoUrl':
        return call('info_url', [N.str(d.source), opts, chain(N.call(assoc(ctx, 'InfoUrlParams', 'new')), d.pdfPassword ? [['pdf_password', [d.pdfPassword]]] : [])]);
      case 'videoUrl':
        if (e.client === 'q') return call('video_url', [N.str(d.source), opts, d.extension ? N.call(N.type('Some'), [N.str(d.extension)]) : N.type('None')]);
        return call('video_url', [N.str(d.source), opts, chain(N.call(assoc(ctx, 'VideoUrlParams', 'new')), d.extension ? [['extension', [N.str(d.extension)]]] : [])]);
      case 'streamUrl': {
        const links = [];
        if (d.renditions.length) links.push(['renditions', [N.list(d.renditions.map(N.int), 'array')]]);
        if (d.segmentDuration) links.push(['segment_duration', [N.int(d.segmentDuration)]]);
        if (d.segmentFormat) links.push(['segment_format', [assoc(ctx, 'SegmentFormat', d.segmentFormat === 'ts' ? 'Ts' : 'Fmp4')]]);
        if (pls[0].length) links.push(['options', [opts]]);
        ctx.pre.push(N.let('stream', chain(N.call(assoc(ctx, 'StreamParams', 'new'), [assoc(ctx, 'StreamProtocol', d.protocol === 'hls' ? 'Hls' : 'Dash')]), links)));
        return call('stream_url', [N.str(d.source), N.ref(N.id('stream'))]);
      }
      case 'imaginaryUrl': {
        // An array holds one type: mixed values become Args.
        const mixed = new Set(d.params.map(([, a]) => a.k)).size > 1;
        const val = (a) => (mixed ? N.call(assoc(ctx, 'Arg', 'from'), [lit(ctx, a)]) : lit(ctx, a));
        return call('imaginary_url', [N.str(d.endpoint), N.str(d.source), N.list(d.params.map(([k, a]) => N.list([N.str(k), val(a)], 'tuple')), 'array')]);
      }
      default: throw new Error(`rust: no ${e.op}`);
    }
  },
  'sign-xi-url': (e, ctx) => N.try(N.call(ty(ctx, 'sign_xi_url'), [N.str(e.url), N.ref(e.key), N.str(e.mount)])),
  print: () => N.stmt(N.call(N.type('println!'), [N.str('{url}')])),
};

export function refine(program) {
  const ctx = { uses: new Set(), pre: [] };
  const body = [];
  for (const s of program.body) {
    const out = lower(s, rules, ctx);
    body.push(...ctx.pre, out);
    ctx.pre = [];
  }
  body.push(N.stmt(N.call(N.type('Ok'), [N.type('()')]), { noSemi: true }));
  const main = N.func('main', body, { ret: 'Result<(), Box<dyn std::error::Error>>' });
  return N.program([main], { imports: [N.imp('xtandard_image', [...ctx.uses].sort((a, b) => a.localeCompare(b)))] });
}

export class RustPrinter extends Printer {
  width = 100;
  unit = '    ';
  semi = ';';
  staticSep = '::';
  // JSON escapes are Rust escapes, but \uXXXX is \u{XXXX} (a backslash is
  // always escaped itself, so only real escapes are rewritten).
  string(s) { return JSON.stringify(s).replace(/(?<!\\)((?:\\\\)*)\\u([0-9a-fA-F]{4})/g, '$1\\u{$2}'); }
  char(c) { return `'${c.replaceAll('\\', '\\\\').replaceAll("'", "\\'")}'`; }
  importLine(i) { return concat('use ', i.module, '::{', join(', ', i.names), '};'); }
  funcHead(f) { return concat('fn ', f.name, '() -> ', f.ret); }
  x_list(e) { return e.style === 'tuple' ? this.bracket('(', e.items.map((i) => this.expr(i)), ')') : super.x_list(e); }
}

export const rust = { id: 'rust', label: 'Rust', refine, printer: new RustPrinter() };
