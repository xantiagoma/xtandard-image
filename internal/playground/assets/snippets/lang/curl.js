// curl: the URL itself, fetched with the Accept header of the preview (or
// JSON for /info). It works for every dialect, so it is built from the
// request's URL, not from a client call.

import { concat, group, ifBreak, indent, join, line } from '../doc.js';
import { N } from '../model.js';
import { Printer } from '../printer.js';

// program is the curl command of request r.
export function program(r) {
  const info = r.kind === 'info';
  const accept = info ? 'application/json' : (r.accept || 'image/avif,image/webp,image/*');
  const url = r.kind === 'xisig' ? (r.shownUrl || r.url) : r.url;
  const args = [N.type('-sS'), N.type('-D'), N.type('-'), ...(info ? [] : [N.type('-o'), N.type('result.bin')]), N.type('-H'), N.str(`Accept: ${accept}`), N.str(url)];
  return N.program([N.stmt(N.call(N.type('curl'), args, { parens: false }))]);
}

export class ShellPrinter extends Printer {
  // POSIX single quotes: nothing inside is special but the quote itself.
  string(s) { return `'${s.replaceAll("'", "'\\''")}'`; }
  callArgs(call) {
    // Options stay on the command's line; the URL goes on its own line,
    // after a backslash, when it does not fit.
    const docs = call.args.map((a) => this.expr(a));
    const last = docs.pop();
    return group(concat(' ', join(' ', docs), indent(concat(ifBreak(' \\', ''), line, last))));
  }
}

export const curl = { id: 'curl', label: 'curl', printer: new ShellPrinter() };
