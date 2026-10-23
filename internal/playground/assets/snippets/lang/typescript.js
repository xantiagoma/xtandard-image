// TypeScript: @xtandard/image, the reference client. Object arguments
// ({ source, options, … }), async builders (await), keys from process.env.

import { concat, join } from '../doc.js';
import { lower, N, uniqueNames } from '../model.js';
import { Printer } from '../printer.js';

const lit = (a) => N.lit(a);
const value = (args) => (args.length === 1 ? lit(args[0]) : N.list(args.map(lit)));
const options = (p) => N.map(p.map((o) => N.entry(N.str(o.name), value(o.args))));
const object = (fields) => N.map(fields.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.entry(N.str(k), v)));

const rules = {
  env: (e) => N.unwrap(N.member(N.member(N.type('process'), 'env'), e.name)),
  'signing-key': (e) => object([['key', e.key], ['salt', e.salt]]),
  'encryption-key': (e) => object([['key', e.key], ['iv', e.iv]]),
  client: (e, ctx) => {
    const factory = e.kind === 'query' ? 'queryUrls' : 'imgproxy';
    ctx.names.add(factory);
    const c = e.config;
    return N.call(N.type(factory), [object([
      ['baseUrl', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['sourceEncoding', c.sourceEncoding && N.str(c.sourceEncoding)], ['separator', c.separator && N.str(c.separator)],
    ])]);
  },
  request: (e) => {
    const d = e.data;
    uniqueNames(d.pipelines, 'TypeScript');
    const pls = d.pipelines;
    const opts = pls.some((p) => p.length) ? (pls.length > 1 ? N.list(pls.map(options)) : options(pls[0])) : null;
    const fields = [
      ['endpoint', d.endpoint && N.str(d.endpoint)],
      ['source', N.str(d.source)],
      ['params', d.params.length ? N.map(d.params.map(([k, a]) => N.entry(N.str(k), lit(a)))) : null],
      ['protocol', d.protocol && N.str(d.protocol)],
      ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int)) : null],
      ['segmentDuration', d.segmentDuration && N.int(d.segmentDuration)],
      ['segmentFormat', d.segmentFormat && N.str(d.segmentFormat)],
      ['options', opts],
      ['extension', d.extension && N.str(d.extension)],
      ['pdfPassword', d.pdfPassword],
    ];
    return N.await(N.call(N.member(N.id(e.client), e.op), [object(fields)]));
  },
  'sign-xi-url': (e, ctx) => {
    ctx.names.add('signXiUrl');
    return N.await(N.call(N.type('signXiUrl'), [N.str(e.url), e.key, object([['mount', N.str(e.mount)]])]));
  },
  print: (e) => N.stmt(N.call(N.member(N.type('console'), 'log'), [e.expr])),
};

export function refine(program) {
  const ctx = { names: new Set() };
  const body = lower(program.body, rules, ctx);
  const spaced = body.length > 2 ? [body[0], N.blank(), ...body.slice(1)] : body;
  return N.program(spaced, { imports: [N.imp('@xtandard/image', [...ctx.names].sort())] });
}

const IDENT = /^[A-Za-z_$][\w$]*$/;

export class TypeScriptPrinter extends Printer {
  semi = ';';
  importLine(i) { return concat('import { ', join(', ', i.names), ' } from ', this.string(i.module), ';'); }
  letStmt(s) { return concat('const ', s.names[0], ' = ', this.expr(s.value), ';'); }
  x_map(e) {
    const key = (k) => (k.k === 'lit' && IDENT.test(k.v.v) ? k.v.v : this.expr(k));
    return this.bracket('{', e.entries.map((en) => concat(key(en.key), ': ', this.expr(en.value))), '}', { pad: true });
  }
}

export const typescript = { id: 'typescript', label: 'TypeScript', refine, printer: new TypeScriptPrinter() };
