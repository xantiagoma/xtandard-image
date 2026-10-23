// Ruby: the xtandard-image gem (module XtandardImage). Factory methods with
// keyword arguments, option hashes, symbols for enumerations, keys from
// ENV.fetch.

import { concat } from '../doc.js';
import { lower, N, snake, uniqueNames } from '../model.js';
import { Printer } from '../printer.js';

const lit = (a) => N.lit(a);
const value = (args) => (args.length === 1 ? lit(args[0]) : N.list(args.map(lit)));
const hash = (p) => N.map(p.map((o) => N.entry(N.str(o.name), value(o.args))));
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.named(k, v));
const mod = (name) => N.member(N.type('XtandardImage'), name, { static: true });
const lib = (name, args) => N.call(N.member(N.type('XtandardImage'), name), args);

const rules = {
  env: (e) => N.call(N.member(N.type('ENV'), 'fetch'), [N.str(e.name)]),
  'signing-key': (e) => N.call(N.member(mod('SigningKey'), 'new'), named([['key', e.key], ['salt', e.salt]])),
  'encryption-key': (e) => N.call(N.member(mod('EncryptionKey'), 'new'), named([['key', e.key], ['iv', e.iv]])),
  client: (e) => {
    const c = e.config;
    return lib(e.kind === 'query' ? 'query_urls' : 'imgproxy', named([
      ['base_url', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['source_encoding', c.sourceEncoding && N.sym(c.sourceEncoding)], ['separator', c.separator && N.str(c.separator)],
    ]));
  },
  request: (e) => {
    const d = e.data;
    uniqueNames(d.pipelines, 'Ruby');
    const pls = d.pipelines;
    const has = pls.some((p) => p.length);
    const opts = has ? (pls.length > 1 ? N.list(pls.map(hash)) : hash(pls[0])) : null;
    const params = d.params.length ? N.map(d.params.map(([k, a]) => N.entry(N.str(k), lit(a)))) : null;
    let args;
    switch (e.op) {
      case 'url': args = [N.str(d.source), ...(e.client === 'q' ? named([['params', params]]) : opts ? [opts] : [])]; break;
      case 'streamUrl': args = [N.str(d.source), ...named([
        ['protocol', N.sym(d.protocol)], ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int)) : null],
        ['segment_duration', d.segmentDuration && N.int(d.segmentDuration)], ['segment_format', d.segmentFormat && N.sym(d.segmentFormat)], ['options', opts],
      ])]; break;
      case 'imaginaryUrl': args = [N.sym(d.endpoint), N.str(d.source), ...(params ? [params] : [])]; break;
      default: args = [N.str(d.source), ...(opts ? [opts] : [])];
    }
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdf_password', d.pdfPassword]]));
    return N.call(N.member(N.id(e.client), snake(e.op)), args);
  },
  'sign-xi-url': (e) => lib('sign_xi_url', [N.str(e.url), e.key, N.named('mount', N.str(e.mount))]),
  print: (e) => N.stmt(N.call(N.type('puts'), [e.expr], { parens: false })),
};

export function refine(program) {
  const body = lower(program.body, rules, {});
  const spaced = body.length > 2 ? [body[0], N.blank(), ...body.slice(1)] : body;
  return N.program(spaced, { imports: [N.imp('xtandard_image')] });
}

const SYMBOL = /^[a-z_][a-z0-9_]*$/;

export class RubyPrinter extends Printer {
  // Double quotes with JSON escapes; # is escaped so that nothing
  // interpolates (#{…}, #@ivar, #$global).
  string(s) { return JSON.stringify(s).replaceAll('#', '\\#'); }
  nullToken() { return 'nil'; }
  staticSep = '::';
  importLine(i) { return concat('require ', this.string(i.module)); }
  letStmt(s) { return concat(s.names[0], ' = ', this.expr(s.value)); }
  x_map(e) {
    const key = (k) => (SYMBOL.test(k.v.v) ? `${k.v.v}:` : `${this.string(k.v.v)}:`);
    return this.bracket('{', e.entries.map((en) => concat(key(en.key), ' ', this.expr(en.value))), '}', { pad: true });
  }
}

export const ruby = { id: 'ruby', label: 'Ruby', refine, printer: new RubyPrinter() };
