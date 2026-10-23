// C#: Xtandard.Image. A builder over a config object initializer, option
// collections (new() { { "resize", "fill", 300 } }), named arguments, keys
// from Environment.GetEnvironmentVariable, top-level statements.

import { concat, group, ifBreak, indent, join, line } from '../doc.js';
import { lower, N } from '../model.js';
import { Printer } from '../printer.js';

const statik = (type, name) => N.member(N.type(type), name, { static: true });
const lit = (a) => (a.k === 'null' ? statik('OptionArg', 'Null') : N.lit(a));
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.named(k, v));
// A collection initializer: new() { { name, args… }, … }.
const collection = (rows) => N.new(null, [], { items: rows.map((r) => N.list(r, 'init')) });
const options = (p) => collection(p.map((o) => [N.str(o.name), ...o.args.map(lit)]));
const pascal = (s) => s[0].toUpperCase() + s.slice(1);

const rules = {
  env: (e) => N.unwrap(N.call(statik('Environment', 'GetEnvironmentVariable'), [N.str(e.name)])),
  'signing-key': (e) => N.new('SigningKey', [e.key, e.salt]),
  'encryption-key': (e) => N.new('EncryptionKey', [e.key, ...(e.iv ? [e.iv] : [])]),
  client: (e) => {
    const c = e.config;
    const query = e.kind === 'query';
    const init = [['BaseUrl', N.str(c.baseUrl)], ['Mount', c.mount && N.str(c.mount)], ['Signing', c.signing], ['Encryption', c.encryption],
      ['SourceEncoding', c.sourceEncoding && statik('SourceEncoding', pascal(c.sourceEncoding))], ['Separator', c.separator && N.str(c.separator)]]
      .filter(([, v]) => v).map(([k, v]) => N.named(k, v));
    return N.new(query ? 'QueryUrlBuilder' : 'ImgproxyUrlBuilder', [N.new(query ? 'QueryConfig' : 'ImgproxyConfig', [], { init })]);
  },
  request: (e) => {
    const d = e.data;
    const pls = d.pipelines;
    const has = pls.some((p) => p.length);
    const params = () => collection(d.params.map(([k, a]) => [N.str(k), lit(a)]));
    let method = { url: 'Url', infoUrl: 'InfoUrl', videoUrl: 'VideoUrl', streamUrl: 'StreamUrl', imaginaryUrl: 'ImaginaryUrl' }[e.op];
    let args;
    switch (e.op) {
      case 'url':
        if (e.client === 'q') args = [N.str(d.source), params()];
        else if (pls.length > 1) {
          method = 'ChainedUrl';
          args = [N.str(d.source), N.list(pls.map(options), 'array')];
        } else args = [N.str(d.source), ...(has ? [options(pls[0])] : [])];
        break;
      case 'streamUrl':
        args = [N.str(d.source), statik('StreamProtocol', pascal(d.protocol)), ...named([
          ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int), 'array') : null],
          ['segmentDuration', d.segmentDuration && N.int(d.segmentDuration)],
          ['segmentFormat', d.segmentFormat && statik('SegmentFormat', pascal(d.segmentFormat))],
          ['options', has ? options(pls[0]) : null],
        ])];
        break;
      case 'imaginaryUrl': args = [N.str(d.endpoint), N.str(d.source), ...(d.params.length ? [params()] : [])]; break;
      default: args = [N.str(d.source), ...(has ? [options(pls[0])] : [])];
    }
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdfPassword', d.pdfPassword]]));
    return N.call(N.member(N.id(e.client), method), args);
  },
  'sign-xi-url': (e) => N.call(statik('Signing', 'SignXiUrl'), [N.str(e.url), e.key, N.named('mount', N.str(e.mount))]),
  print: (e) => N.stmt(N.call(statik('Console', 'WriteLine'), [e.expr])),
};

export function refine(program) {
  return N.program(lower(program.body, rules, {}), { imports: [N.imp('Xtandard.Image')] });
}

export class CSharpPrinter extends Printer {
  width = 100;
  unit = '    ';
  semi = ';';
  trailingArgs = false;
  number(v, k) { return k === 'int' && Math.abs(Number(v)) > 2147483647 ? `${v}L` : v; }
  importLine(i) { return concat('using ', i.module, ';'); }
  letStmt(s) { return concat('var ', s.names[0], ' = ', this.expr(s.value), ';'); }
  x_named(e) { return concat(e.name, ': ', this.expr(e.value)); }
  hugs(arg) { return super.hugs(arg) || arg.k === 'new'; }
  // Object and collection initializers: on one line, or braces on their
  // own lines (Allman).
  initializer(head, items) {
    return group(concat(head, line, '{', indent(concat(line, join(concat(',', line), items))), ifBreak(','), line, '}'));
  }
  x_new(e) {
    const head = e.type ? concat('new ', e.type, e.init ? '' : this.args(e.args)) : 'new()';
    if (e.init) return this.initializer(head, e.init.map((n) => concat(n.name, ' = ', this.expr(n.value))));
    if (e.items) return this.initializer(head, e.items.map((i) => this.expr(i)));
    return head;
  }
  x_list(e) {
    if (e.style === 'init') return concat('{ ', join(', ', e.items.map((i) => this.expr(i))), ' }');
    return super.x_list(e);
  }
}

export const csharp = { id: 'csharp', label: 'C#', refine, printer: new CSharpPrinter() };
