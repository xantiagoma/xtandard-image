// Elixir: xtandard_image. Module functions that take the builder first
// (Imgproxy.url!(img, …)), options as keyword lists of argument tuples,
// atoms for enumerations, keys from System.fetch_env!.

import { concat, join } from '../doc.js';
import { lower, N, snake } from '../model.js';
import { Printer } from '../printer.js';

const lit = (a) => N.lit(a);
const value = (args) => (args.length === 1 ? lit(args[0]) : N.list(args.map(lit), 'tuple'));
const kw = (p) => N.map(p.map((o) => N.entry(N.str(o.name), value(o.args))), 'keyword');
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.named(k, v));
const fn = (mod, name, args) => N.call(N.member(N.type(mod), name, { static: true }), args);

const rules = {
  env: (e) => fn('System', 'fetch_env!', [N.str(e.name)]),
  'signing-key': (e, ctx) => (ctx.aliases.add('SigningKey'), fn('SigningKey', 'new!', [e.key, e.salt])),
  'encryption-key': (e, ctx) => (ctx.aliases.add('EncryptionKey'), fn('EncryptionKey', 'new!', [e.key, ...named([['iv', e.iv]])])),
  client: (e) => {
    const c = e.config;
    return fn('XtandardImage', e.kind === 'query' ? 'query_urls' : 'imgproxy', named([
      ['base_url', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['source_encoding', c.sourceEncoding && N.sym(c.sourceEncoding)], ['separator', c.separator && N.str(c.separator)],
    ]));
  },
  request: (e, ctx) => {
    const d = e.data;
    const mod = e.client === 'q' ? 'Query' : 'Imgproxy';
    ctx.aliases.add(mod);
    const pls = d.pipelines;
    const v = N.id(e.client);
    const params = kw(d.params.map(([k, a]) => ({ name: k, args: [a] })));
    let args;
    switch (e.op) {
      case 'url':
        args = e.client === 'q'
          ? [v, N.str(d.source), ...(d.params.length ? [N.named('params', params)] : [])]
          : [v, N.str(d.source), pls.length > 1 ? N.list(pls.map(kw)) : kw(pls[0])];
        break;
      case 'streamUrl': args = [v, N.str(d.source), ...named([
        ['protocol', N.sym(d.protocol)], ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int)) : null],
        ['segment_duration', d.segmentDuration && N.int(d.segmentDuration)], ['segment_format', d.segmentFormat && N.sym(d.segmentFormat)],
        ['options', pls[0].length ? kw(pls[0]) : null],
      ])]; break;
      case 'imaginaryUrl': args = [v, N.sym(d.endpoint), N.str(d.source), params]; break;
      default: args = [v, N.str(d.source), kw(pls[0])];
    }
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdf_password', d.pdfPassword]]));
    return fn(mod, `${snake(e.op)}!`, args);
  },
  'sign-xi-url': (e, ctx) => (ctx.aliases.add('Signing'), fn('Signing', 'sign_xi_url!', [N.str(e.url), e.key, N.named('mount', N.str(e.mount))])),
  print: (e) => N.stmt(fn('IO', 'puts', [e.expr])),
};

export function refine(program) {
  const ctx = { aliases: new Set() };
  const body = lower(program.body, rules, ctx);
  const spaced = body.length > 2 ? [body[0], N.blank(), ...body.slice(1)] : body;
  return N.program(spaced, { imports: [N.imp('XtandardImage', [...ctx.aliases].sort())] });
}

const ATOM = /^[a-z_][a-zA-Z0-9_]*[?!]?$/;

export class ElixirPrinter extends Printer {
  trailingArgs = false;
  trailingItems = false;
  // JSON escapes, and #{ escaped so that nothing interpolates.
  string(s) { return JSON.stringify(s).replaceAll('#{', '\\#{'); }
  nullToken() { return 'nil'; }
  importLine(i) { return i.names.length > 1 ? concat('alias ', i.module, '.{', join(', ', i.names), '}') : concat('alias ', i.module, '.', i.names[0]); }
  letStmt(s) { return concat(s.names[0], ' = ', this.expr(s.value)); }
  x_named(e) { return concat(e.name, ': ', this.expr(e.value)); }
  x_list(e) { return e.style === 'tuple' ? this.bracket('{', e.items.map((i) => this.expr(i)), '}') : super.x_list(e); }
  x_map(e) {
    const key = (k) => (ATOM.test(k.v.v) ? `${k.v.v}:` : `${this.string(k.v.v)}:`);
    return this.bracket('[', e.entries.map((en) => concat(key(en.key), ' ', this.expr(en.value))), ']');
  }
}

export const elixir = { id: 'elixir', label: 'Elixir', refine, printer: new ElixirPrinter() };
