// Swift: the XtandardImage package. The imgproxy(…) / queryURLs(…)
// factories with argument labels, options as dictionary literals, implicit
// member enumerations (.plain, .hls), `try` on every throwing call, keys
// from ProcessInfo's environment.

import { concat } from '../doc.js';
import { lower, N } from '../model.js';
import { Printer } from '../printer.js';

const lit = (a) => N.lit(a);
const value = (args) => (args.length === 1 ? lit(args[0]) : N.list(args.map(lit)));
const dict = (p) => N.map(p.map((o) => N.entry(N.str(o.name), value(o.args))));
const named = (pairs) => pairs.filter(([, v]) => v !== null && v !== undefined).map(([k, v]) => N.named(k, v));
const dot = (name) => N.type(`.${name}`);
const OPS = { url: 'url', infoUrl: 'infoURL', videoUrl: 'videoURL', streamUrl: 'streamURL', imaginaryUrl: 'imaginaryURL' };

const rules = {
  env: (e, ctx) => {
    ctx.env = true;
    return N.unwrap(N.index(N.id('env'), N.str(e.name)));
  },
  'signing-key': (e) => N.try(N.call(N.type('SigningKey'), named([['key', e.key], ['salt', e.salt]]))),
  'encryption-key': (e) => N.try(N.call(N.type('EncryptionKey'), named([['key', e.key], ['iv', e.iv]]))),
  client: (e) => {
    const c = e.config;
    return N.call(N.type(e.kind === 'query' ? 'queryURLs' : 'imgproxy'), named([
      ['baseURL', N.str(c.baseUrl)], ['mount', c.mount && N.str(c.mount)], ['signing', c.signing], ['encryption', c.encryption],
      ['sourceEncoding', c.sourceEncoding && dot(c.sourceEncoding)], ['separator', c.separator && N.str(c.separator)],
    ]));
  },
  request: (e) => {
    const d = e.data;
    const pls = d.pipelines;
    const has = pls.some((p) => p.length);
    const params = d.params.length ? dict(d.params.map(([k, a]) => ({ name: k, args: [a] }))) : null;
    let args;
    switch (e.op) {
      case 'url':
        if (e.client === 'q') args = [N.str(d.source), ...named([['params', params]])];
        else if (pls.length > 1) args = [N.str(d.source), N.named('pipelines', N.list(pls.map(dict)))];
        else args = [N.str(d.source), ...named([['options', has ? dict(pls[0]) : null]])];
        break;
      case 'streamUrl': args = [N.str(d.source), ...named([
        ['protocol', dot(d.protocol)], ['renditions', d.renditions.length ? N.list(d.renditions.map(N.int)) : null],
        ['segmentDuration', d.segmentDuration && N.int(d.segmentDuration)], ['segmentFormat', d.segmentFormat && dot(d.segmentFormat)],
        ['options', has ? dict(pls[0]) : null],
      ])]; break;
      case 'imaginaryUrl': args = [N.str(d.endpoint), N.str(d.source), ...named([['params', params]])]; break;
      default: args = [N.str(d.source), ...named([['options', has ? dict(pls[0]) : null]])];
    }
    args.push(...named([['extension', d.extension && N.str(d.extension)], ['pdfPassword', d.pdfPassword]]));
    return N.try(N.call(N.member(N.id(e.client), OPS[e.op]), args));
  },
  'sign-xi-url': (e) => N.try(N.call(N.type('signXiURL'), [N.str(e.url), N.named('key', e.key), N.named('mount', N.str(e.mount))])),
  print: (e) => N.stmt(N.call(N.type('print'), [e.expr])),
};

export function refine(program) {
  const ctx = { env: false };
  const body = lower(program.body, rules, ctx);
  const env = ctx.env ? [N.let('env', N.member(N.member(N.type('ProcessInfo'), 'processInfo', { static: true }), 'environment'))] : [];
  const imports = [...(ctx.env ? [N.imp('Foundation')] : []), N.imp('XtandardImage')];
  return N.program([...env, ...body], { imports });
}

export class SwiftPrinter extends Printer {
  unit = '    ';
  trailingArgs = false;
  // JSON escapes (a backslash is always escaped, so "\(" never
  // interpolates) with \u{XXXX} for \uXXXX.
  string(s) { return JSON.stringify(s).replace(/(?<!\\)((?:\\\\)*)\\u([0-9a-fA-F]{4})/g, '$1\\u{$2}'); }
  nullToken() { return 'nil'; }
  importLine(i) { return concat('import ', i.module); }
  letStmt(s) { return concat('let ', s.names[0], ' = ', this.expr(s.value)); }
  x_try(e) { return concat('try ', this.expr(e.expr)); }
  x_map(e) {
    if (!e.entries.length) return '[:]';
    return this.bracket('[', e.entries.map((en) => concat(this.expr(en.key), ': ', this.expr(en.value))), ']');
  }
}

export const swift = { id: 'swift', label: 'Swift', refine, printer: new SwiftPrinter() };
