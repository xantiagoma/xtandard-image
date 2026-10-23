// PHP: xtandard/image (namespace Xtandard\Image). Constructors with named
// arguments, option arrays (Pipelines for chained ones), keys from getenv().

import { concat, hardline, join } from '../doc.js';
import { lower, N, uniqueNames } from '../model.js';
import { Printer } from '../printer.js';

const lit = (a) => N.lit(a);
const value = (args) => (args.length === 1 ? lit(args[0]) : N.list(args.map(lit)));
const array = (p) => N.map(p.map((o) => N.entry(N.str(o.name), value(o.args))));
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.named(k, v));

const rules = {
  env: (e) => N.call(N.type('getenv'), [N.str(e.name)]),
  'signing-key': (e, ctx) => (ctx.uses.add('SigningKey'), N.new('SigningKey', [e.key, e.salt])),
  'encryption-key': (e, ctx) => (ctx.uses.add('EncryptionKey'), N.new('EncryptionKey', [e.key, ...(e.iv ? [e.iv] : [])])),
  client: (e, ctx) => {
    const cls = e.kind === 'query' ? 'QueryUrlBuilder' : 'ImgproxyUrlBuilder';
    ctx.uses.add(cls);
    const c = e.config;
    return N.new(cls, named([
      ['baseUrl', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['sourceEncoding', c.sourceEncoding && N.str(c.sourceEncoding)], ['separator', c.separator && N.str(c.separator)],
    ]));
  },
  request: (e, ctx) => {
    const d = e.data;
    uniqueNames(d.pipelines, 'PHP');
    const pls = d.pipelines;
    const has = pls.some((p) => p.length);
    const params = d.params.length ? N.map(d.params.map(([k, a]) => N.entry(N.str(k), lit(a)))) : null;
    let args;
    switch (e.op) {
      case 'url':
        if (e.client === 'q') args = [N.str(d.source), ...named([['params', params]])];
        else if (pls.length > 1) {
          ctx.uses.add('Pipelines');
          args = [N.str(d.source), N.new('Pipelines', pls.map(array))];
        } else args = [N.str(d.source), ...(has ? [array(pls[0])] : [])];
        break;
      case 'streamUrl': args = [N.str(d.source), N.str(d.protocol), ...named([
        ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int)) : null], ['segmentDuration', d.segmentDuration && N.int(d.segmentDuration)],
        ['segmentFormat', d.segmentFormat && N.str(d.segmentFormat)], ['options', has ? array(pls[0]) : null],
      ])]; break;
      case 'imaginaryUrl': args = [N.str(d.endpoint), N.str(d.source), ...(params ? [params] : [])]; break;
      default: args = [N.str(d.source), ...(has ? [array(pls[0])] : [])];
    }
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdfPassword', d.pdfPassword]]));
    return N.call(N.member(N.id(e.client), e.op), args);
  },
  'sign-xi-url': (e, ctx) => (ctx.functions.add('signXiUrl'), N.call(N.type('signXiUrl'), [N.str(e.url), e.key, N.str(e.mount)])),
  print: (e) => N.cmd('echo', [e.expr, N.type('PHP_EOL')]),
};

export function refine(program) {
  const ctx = { uses: new Set(), functions: new Set() };
  const body = lower(program.body, rules, ctx);
  const spaced = body.length > 2 ? [body[0], N.blank(), ...body.slice(1)] : body;
  const imports = [
    ...[...ctx.uses].sort().map((u) => N.imp(`Xtandard\\Image\\${u}`)),
    ...[...ctx.functions].sort().map((f) => N.imp(`Xtandard\\Image\\${f}`, [], { function: true })),
  ];
  return N.program(spaced, {
    header: [N.cmd('<?php', []), N.blank(), N.cmd('require', [N.binary('.', N.type('__DIR__'), N.str('/vendor/autoload.php'))])],
    imports,
  });
}

export class PhpPrinter extends Printer {
  unit = '    ';
  semi = ';';
  memberSep = '->';
  staticSep = '::';
  // Single-quoted strings: only \ and ' are escaped, nothing interpolates.
  string(s) { return `'${s.replaceAll('\\', '\\\\').replaceAll("'", "\\'")}'`; }
  ident(name) { return `$${name}`; }
  statement(s) {
    if (s.k === 'cmd' && s.keyword === '<?php') return '<?php';
    return super.statement(s);
  }
  importLine(i) { return concat('use ', i.function ? 'function ' : '', i.module, ';'); }
  imports(list) {
    const classes = list.filter((i) => !i.function).map((i) => this.importLine(i));
    const fns = list.filter((i) => i.function).map((i) => this.importLine(i));
    return join(concat(hardline, hardline), [classes, fns].filter((l) => l.length).map((l) => join(hardline, l)));
  }
  letStmt(s) { return concat(this.ident(s.names[0]), ' = ', this.expr(s.value), ';'); }
  x_map(e) { return this.bracket('[', e.entries.map((en) => concat(this.expr(en.key), ' => ', this.expr(en.value))), ']'); }
}

export const php = { id: 'php', label: 'PHP', refine, printer: new PhpPrinter() };
