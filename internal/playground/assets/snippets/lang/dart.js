// Dart: package:xtandard_image. Builders with named parameters, options as
// map literals (pipelines: for chained ones), enums (StreamProtocol.hls),
// keys from Platform.environment, in void main.

import { concat, hardline, join } from '../doc.js';
import { lower, N } from '../model.js';
import { Printer } from '../printer.js';

const lit = (a) => N.lit(a);
const value = (args) => (args.length === 1 ? lit(args[0]) : N.list(args.map(lit)));
const map = (p) => N.map(p.map((o) => N.entry(N.str(o.name), value(o.args))));
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.named(k, v));
const statik = (type, name) => N.member(N.type(type), name, { static: true });

const rules = {
  env: (e, ctx) => {
    ctx.env = true;
    return N.unwrap(N.index(N.id('env'), N.str(e.name)));
  },
  'signing-key': (e) => N.call(N.type('SigningKey'), named([['key', e.key], ['salt', e.salt]])),
  'encryption-key': (e) => N.call(N.type('EncryptionKey'), named([['key', e.key], ['iv', e.iv]])),
  client: (e) => {
    const c = e.config;
    return N.call(N.type(e.kind === 'query' ? 'QueryUrlBuilder' : 'ImgproxyUrlBuilder'), named([
      ['baseUrl', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['sourceEncoding', c.sourceEncoding && statik('SourceEncoding', c.sourceEncoding)], ['separator', c.separator && N.str(c.separator)],
    ]));
  },
  request: (e) => {
    const d = e.data;
    const pls = d.pipelines;
    const has = pls.some((p) => p.length);
    const params = d.params.length ? map(d.params.map(([k, a]) => ({ name: k, args: [a] }))) : null;
    let args;
    switch (e.op) {
      case 'url':
        args = e.client === 'q'
          ? [N.str(d.source), ...named([['params', params]])]
          : [N.str(d.source), ...named([['options', has ? map(pls[0]) : null], ['pipelines', pls.length > 1 ? N.list(pls.slice(1).map(map)) : null]])];
        break;
      case 'streamUrl': args = [N.str(d.source), ...named([
        ['protocol', statik('StreamProtocol', d.protocol)], ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int)) : null],
        ['segmentDuration', d.segmentDuration && N.int(d.segmentDuration)], ['segmentFormat', d.segmentFormat && statik('SegmentFormat', d.segmentFormat)],
        ['options', has ? map(pls[0]) : null],
      ])]; break;
      case 'imaginaryUrl': args = [N.str(d.endpoint), N.str(d.source), ...named([['params', params]])]; break;
      default: args = [N.str(d.source), ...named([['options', has ? map(pls[0]) : null]])];
    }
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdfPassword', d.pdfPassword]]));
    return N.call(N.member(N.id(e.client), e.op), args);
  },
  'sign-xi-url': (e) => N.call(N.type('signXiUrl'), [N.str(e.url), e.key, N.named('mount', N.str(e.mount))]),
  print: (e) => N.stmt(N.call(N.type('print'), [e.expr])),
};

export function refine(program) {
  const ctx = { env: false };
  const body = lower(program.body, rules, ctx);
  const env = ctx.env ? [N.let('env', N.member(N.type('Platform'), 'environment', { static: true }))] : [];
  const imports = [...(ctx.env ? [N.imp('dart:io')] : []), N.imp('package:xtandard_image/xtandard_image.dart')];
  return N.program([N.func('main', [...env, ...body])], { imports });
}

export class DartPrinter extends Printer {
  semi = ';';
  // Single quotes: backslash, quote and $ (interpolation) escaped, and the
  // control characters as escapes.
  string(s) {
    const esc = s.replace(/[\\'$\n\r\t\b\f\v]|[\x00-\x1f]/g, (c) => ({ '\\': '\\\\', "'": "\\'", $: '\\$', '\n': '\\n', '\r': '\\r', '\t': '\\t', '\b': '\\b', '\f': '\\f', '\v': '\\v' }[c] ?? `\\u{${c.charCodeAt(0).toString(16)}}`));
    return `'${esc}'`;
  }
  imports(list) {
    const lines = list.map((i) => concat('import ', this.string(i.module), ';'));
    return join(concat(hardline, hardline), lines);
  }
  letStmt(s) { return concat('final ', s.names[0], ' = ', this.expr(s.value), ';'); }
  funcHead(f) { return concat('void ', f.name, '()'); }
}

export const dart = { id: 'dart', label: 'Dart', refine, printer: new DartPrinter() };
