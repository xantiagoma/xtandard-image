// Java: io.github.xantiagoma:xtandard-image. Builders (ImgproxyUrlBuilder
// .builder()…build()), typed option lists (ProcessingOptions.create()
// .resize(…)), parameter records (UrlParams, StreamParams), keys from
// System.getenv, in a Main class.

import { concat } from '../doc.js';
import { camel, lower, N } from '../model.js';
import { Printer } from '../printer.js';

const lit = (a) => N.lit(a);
const cls = (name) => N.type(name);
const statik = (type, name) => N.member(cls(type), name, { static: true });
const chain = (head, links) => links.reduce((obj, [name, args]) => N.call(N.member(obj, name), args), head);

// method is an option's generated method, or option(name, …) when a value
// does not fit the method's catalogue types (text in a number slot).
export function jvmOption(o, litFn = lit) {
  const misfit = o.args.some((a) => (a.k === 'str' && ['integer', 'number', 'boolean'].includes(a.t)) || (a.k === 'num' && a.t === 'integer'));
  return misfit ? ['option', [N.str(o.name), ...o.args.map(litFn)]] : [camel(o.name), o.args.map(litFn)];
}
const options = (type, p) => chain(N.call(statik(type, 'create')), p.map((o) => jvmOption(o)));

const rules = {
  env: (e) => N.call(statik('System', 'getenv'), [N.str(e.name)]),
  'signing-key': (e) => N.call(statik('SigningKey', 'of'), [e.key, e.salt]),
  'encryption-key': (e) => chain(N.call(statik('EncryptionKey', 'of'), [e.key]), e.iv ? [['withIv', [e.iv]]] : []),
  client: (e) => {
    const c = e.config;
    const links = [['baseUrl', [N.str(c.baseUrl)]]];
    if (c.mount) links.push(['mount', [N.str(c.mount)]]);
    if (c.signing) links.push(['signing', [c.signing]]);
    if (c.encryption) links.push(['encryption', [c.encryption]]);
    if (c.sourceEncoding) links.push(['sourceEncoding', [statik('SourceEncoding', c.sourceEncoding.toUpperCase())]]);
    if (c.separator) links.push(['separator', [N.str(c.separator)]]);
    links.push(['build', []]);
    return chain(N.call(statik(e.kind === 'query' ? 'QueryUrlBuilder' : 'ImgproxyUrlBuilder', 'builder')), links);
  },
  request: (e, ctx) => {
    const d = e.data;
    const v = N.id(e.client);
    const pls = d.pipelines;
    const call = (method, args) => N.call(N.member(v, method), args);
    const urlParams = () => chain(d.extension ? N.call(statik('UrlParams', 'ofExtension'), [N.str(d.extension)]) : N.call(statik('UrlParams', 'of')), d.pdfPassword ? [['withPdfPassword', [d.pdfPassword]]] : []);
    switch (e.op) {
      case 'url':
        if (e.client === 'q') {
          ctx.list = true;
          return call('url', [N.str(d.source), chain(N.call(statik('QueryParams', 'create')), d.params.map(([k, a]) => ['param', [N.str(k), lit(a)]])), N.call(statik('List', 'of')), urlParams()]);
        }
        if (pls.length > 1) {
          ctx.list = true;
          return call('url', [N.str(d.source), N.call(statik('List', 'of'), pls.map((p) => options('ProcessingOptions', p))), urlParams()]);
        }
        return call('url', [N.str(d.source), options('ProcessingOptions', pls[0]), urlParams()]);
      case 'infoUrl': return call('infoUrl', [N.str(d.source), options('InfoOptions', pls[0]), urlParams()]);
      case 'videoUrl':
        if (e.client === 'q') return call('videoUrl', [N.str(d.source), options('VideoOptions', pls[0]), d.extension ? N.str(d.extension) : N.nil()]);
        return call('videoUrl', [N.str(d.source), options('VideoOptions', pls[0]), urlParams()]);
      case 'streamUrl': {
        const links = [];
        if (d.renditions.length) links.push(['withRenditions', d.renditions.map(N.int)]);
        if (d.segmentDuration) links.push(['withSegmentDuration', [N.int(d.segmentDuration)]]);
        if (d.segmentFormat) links.push(['withSegmentFormat', [statik('SegmentFormat', d.segmentFormat.toUpperCase())]]);
        if (pls[0].length) links.push(['withOptions', [options('VideoOptions', pls[0])]]);
        ctx.pre.push(N.let('stream', chain(N.call(statik('StreamParams', d.protocol)), links), { type: 'StreamParams' }));
        return call('streamUrl', [N.str(d.source), N.id('stream')]);
      }
      case 'imaginaryUrl':
        return call('imaginaryUrl', [N.str(d.endpoint), N.str(d.source), chain(N.call(statik('ImaginaryParams', 'create')), d.params.map(([k, a]) => ['param', [N.str(k), lit(a)]]))]);
      default: throw new Error(`java: no ${e.op}`);
    }
  },
  'sign-xi-url': (e) => N.call(statik('Signing', 'signXiUrl'), [N.str(e.url), e.key, N.str(e.mount)]),
  print: (e) => N.stmt(N.call(N.member(statik('System', 'out'), 'println'), [e.expr])),
};

export function refine(program) {
  const ctx = { pre: [], list: false };
  const body = [];
  for (const s of program.body) {
    const out = lower(s, rules, ctx);
    body.push(...ctx.pre, out.k === 'let' ? { ...out, type: out.names[0] === 'url' ? 'String' : (s.value.kind === 'query' ? 'QueryUrlBuilder' : 'ImgproxyUrlBuilder') } : out);
    ctx.pre = [];
  }
  const imports = [N.imp('io.github.xantiagoma.xtandard.image.*'), ...(ctx.list ? [N.imp('java.util.List')] : [])];
  return N.program([N.cls('Main', [N.func('main', body)])], { imports });
}

export class JavaPrinter extends Printer {
  width = 100;
  unit = '    ';
  semi = ';';
  trailingArgs = false;
  trailingItems = false;
  number(v, k) { return k === 'int' && Math.abs(Number(v)) > 2147483647 ? `${v}L` : v; }
  importLine(i) { return concat('import ', i.module, ';'); }
  letStmt(s) { return concat(s.type, ' ', s.names[0], ' = ', this.expr(s.value), ';'); }
  classHead(c) { return concat('public class ', c.name); }
  funcHead(f) { return concat('public static void ', f.name, '(String[] args)'); }
}

export const java = { id: 'java', label: 'Java', refine, printer: new JavaPrinter() };
