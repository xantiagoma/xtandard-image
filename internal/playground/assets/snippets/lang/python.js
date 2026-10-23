// Python: xtandard_image's synchronous builder (imgproxy(), query_urls()),
// option mappings (dicts of argument tuples), keyword arguments, keys from
// os.environ.

import { concat, hardline, join } from '../doc.js';
import { lower, N, snake, uniqueNames } from '../model.js';
import { Printer } from '../printer.js';
import { requestText } from './typescript.js';

const lit = (a) => N.lit(a);
const value = (args) => (args.length === 1 ? lit(args[0]) : N.list(args.map(lit), 'tuple'));
const dict = (p) => N.map(p.map((o) => N.entry(N.str(o.name), value(o.args))));
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined && v !== false).map(([k, v]) => N.named(k, v));

const rules = {
  env: (e, ctx) => {
    ctx.os = true;
    return N.index(N.member(N.type('os'), 'environ'), N.str(e.name));
  },
  'signing-key': (e, ctx) => (ctx.names.add('SigningKey'), N.call(N.type('SigningKey'), named([['key', e.key], ['salt', e.salt]]))),
  'encryption-key': (e, ctx) => (ctx.names.add('EncryptionKey'), N.call(N.type('EncryptionKey'), named([['key', e.key], ['iv', e.iv]]))),
  client: (e, ctx) => {
    const factory = e.kind === 'query' ? 'query_urls' : 'imgproxy';
    ctx.names.add(factory);
    const c = e.config;
    return N.call(N.type(factory), named([
      ['base_url', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['source_encoding', c.sourceEncoding && N.str(c.sourceEncoding)], ['separator', c.separator && N.str(c.separator)],
    ]));
  },
  request: (e) => {
    const d = e.data;
    uniqueNames(d.pipelines, 'Python');
    const has = d.pipelines.some((p) => p.length);
    const opts = has ? (d.pipelines.length > 1 ? N.list(d.pipelines.map(dict)) : dict(d.pipelines[0])) : null;
    const params = d.params.length ? N.map(d.params.map(([k, a]) => N.entry(N.str(k), lit(a)))) : null;
    let args;
    switch (e.op) {
      case 'url': args = [N.str(d.source), ...(e.client === 'q' ? named([['params', params]]) : opts ? [opts] : [])]; break;
      case 'streamUrl': args = [N.str(d.source), ...named([
        ['protocol', N.str(d.protocol)], ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int)) : null],
        ['segment_duration', d.segmentDuration && N.int(d.segmentDuration)], ['segment_format', d.segmentFormat && N.str(d.segmentFormat)], ['options', opts],
      ])]; break;
      case 'imaginaryUrl': args = [N.str(d.endpoint), N.str(d.source), ...(params ? [params] : [])]; break;
      default: args = [N.str(d.source), ...(opts ? [opts] : [])];
    }
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdf_password', d.pdfPassword]]));
    return N.call(N.member(N.id(e.client), snake(e.op)), args);
  },
  'sign-xi-url': (e, ctx) => (ctx.names.add('sign_xi_url'), N.call(N.type('sign_xi_url'), [N.str(e.url), e.key, N.named('mount', N.str(e.mount))])),
  print: (e) => N.stmt(N.call(N.type('print'), [e.expr])),
  'query-request': (e, ctx) => (ctx.names.add('query_method_request'), N.call(N.type('query_method_request'), [e.url])),
  'print-request': (e) => N.stmt(N.call(N.type('print'), [requestText(e.req, (f) => N.member(e.req, f))])),
};

export function refine(program) {
  const ctx = { names: new Set(), os: false };
  const body = lower(program.body, rules, ctx);
  const imports = [...(ctx.os ? [N.imp('os')] : []), N.imp('xtandard_image', [...ctx.names].sort(), { from: true })];
  return N.program(body, { imports });
}

export class PythonPrinter extends Printer {
  width = 79;
  unit = '    ';
  nullToken() { return 'None'; }
  bool(b) { return b ? 'True' : 'False'; }
  imports(list) {
    // The standard library, a blank line, then the client.
    const std = list.filter((i) => !i.from).map((i) => concat('import ', i.module));
    const from = list.filter((i) => i.from).map((i) => concat('from ', i.module, ' import ', join(', ', i.names)));
    return join(concat(hardline, hardline), [std.length ? join(hardline, std) : null, ...from].filter(Boolean));
  }
  letStmt(s) { return concat(s.names[0], ' = ', this.expr(s.value)); }
  x_named(e) { return concat(e.name, '=', this.expr(e.value)); }
  x_list(e) { return e.style === 'tuple' ? this.bracket('(', e.items.map((i) => this.expr(i)), ')') : super.x_list(e); }
}

export const python = { id: 'python', label: 'Python', refine, printer: new PythonPrinter() };
