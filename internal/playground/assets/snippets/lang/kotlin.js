// Kotlin Multiplatform: io.github.xantiagoma:xtandard-image-kmp. Builders
// with named arguments, options as DSL blocks (processingOptions { … }),
// keys from System.getenv (JVM), in fun main.

import { concat } from '../doc.js';
import { lower, N } from '../model.js';
import { Printer } from '../printer.js';
import { jvmOption } from './java.js';

const statik = (type, name) => N.member(N.type(type), name, { static: true });
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.named(k, v));
// A DSL block: builder { call(…); call(…) }.
const dsl = (fn, calls) => N.call(N.type(fn), [], { block: N.block(calls.map(([name, args]) => N.stmt(N.call(N.type(name), args)))) });
const options = (fn, p) => (p.length ? dsl(fn, p.map((o) => jvmOption(o))) : null);

const rules = {
  env: (e) => N.call(statik('System', 'getenv'), [N.str(e.name)]),
  'signing-key': (e) => N.call(statik('SigningKey', 'of'), [e.key, e.salt]),
  'encryption-key': (e) => {
    const key = N.call(statik('EncryptionKey', 'of'), [e.key]);
    return e.iv ? N.call(N.member(key, 'withIv'), [e.iv]) : key;
  },
  client: (e) => {
    const c = e.config;
    return N.call(N.type(e.kind === 'query' ? 'QueryUrlBuilder' : 'ImgproxyUrlBuilder'), named([
      ['baseUrl', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['sourceEncoding', c.sourceEncoding && statik('SourceEncoding', c.sourceEncoding.toUpperCase())], ['separator', c.separator && N.str(c.separator)],
    ]));
  },
  request: (e) => {
    const d = e.data;
    const pls = d.pipelines;
    const params = (fn) => (d.params.length ? dsl(fn, d.params.map(([k, a]) => ['param', [N.str(k), N.lit(a)]])) : null);
    let args;
    switch (e.op) {
      case 'url':
        if (e.client === 'q') args = [N.str(d.source), params('queryParams')];
        else if (pls.length > 1) args = [N.str(d.source), N.call(N.type('listOf'), pls.map((p) => options('processingOptions', p) ?? dsl('processingOptions', [])))];
        else args = [N.str(d.source), options('processingOptions', pls[0])];
        break;
      case 'infoUrl': args = [N.str(d.source), options('infoOptions', pls[0])]; break;
      case 'videoUrl': args = [N.str(d.source), options('videoOptions', pls[0])]; break;
      case 'streamUrl':
        args = [N.str(d.source), N.call(statik('StreamParams', d.protocol), named([
          ['renditions', d.renditions.length ? N.call(N.type('listOf'), d.renditions.map(N.int)) : null],
          ['segmentDuration', d.segmentDuration && N.int(d.segmentDuration)],
          ['segmentFormat', d.segmentFormat && statik('SegmentFormat', d.segmentFormat.toUpperCase())],
          ['options', options('videoOptions', pls[0])],
        ]))];
        break;
      case 'imaginaryUrl': args = [N.str(d.endpoint), N.str(d.source), params('imaginaryParams')]; break;
      default: throw new Error(`kotlin: no ${e.op}`);
    }
    args = args.filter(Boolean);
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdfPassword', d.pdfPassword]]));
    return N.call(N.member(N.id(e.client), e.op), args);
  },
  'sign-xi-url': (e) => N.call(statik('Signing', 'signXiUrl'), [N.str(e.url), e.key, N.named('mount', N.str(e.mount))]),
  print: (e) => N.stmt(N.call(N.type('println'), [e.expr])),
};

export function refine(program) {
  const body = lower(program.body, rules, {});
  return N.program([N.func('main', body)], { imports: [N.imp('io.github.xantiagoma.xtandard.image.kmp.*')] });
}

export class KotlinPrinter extends Printer {
  width = 100;
  unit = '    ';
  // JSON escapes, and $ escaped so that nothing is a string template.
  string(s) { return JSON.stringify(s).replaceAll('$', '\\$'); }
  importLine(i) { return concat('import ', i.module); }
  letStmt(s) { return concat('val ', s.names[0], ' = ', this.expr(s.value)); }
  funcHead(f) { return concat('fun ', f.name, '()'); }
  x_named(e) { return concat(e.name, ' = ', this.expr(e.value)); }
}

export const kotlin = { id: 'kotlin', label: 'Kotlin', refine, printer: new KotlinPrinter() };
