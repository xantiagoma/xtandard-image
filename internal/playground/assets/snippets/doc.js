// A pretty-printing document algebra (Wadler's "prettier printer", as
// Prettier's doc builders): printers describe code as a Doc and print()
// decides the line breaks from the width. A group is printed flat (its
// lines as spaces, its soft lines as nothing) when it fits on the rest of
// the line, else broken (every line of the group a newline at the current
// indentation). A hard line, or a group made with shouldBreak, breaks every
// group around it.
//
// Docs: a string (text without newlines), concat(...), group(doc),
// indent(doc), line, softline, hardline, ifBreak(broken, flat), and
// join(sep, docs).
//
// This module is the one Doc algebra of the repository: the playground's
// snippet printers import it here, and the official clients' code generator
// (clients/codegen/doc.ts, WP-146) imports this same file by relative path.
// It stays under internal/playground because go:embed only reads files
// below the package directory. The JSDoc types are what TypeScript callers
// see (clients/codegen compiles with allowJs).

/**
 * @typedef {{ t: 'line', soft?: boolean, hard?: boolean }} Line
 * @typedef {string | Line | { t: 'concat', parts: Doc[] } | { t: 'group', doc: Doc, break: boolean }
 *   | { t: 'indent', doc: Doc } | { t: 'if-break', broken: Doc, flat: Doc }} Doc
 * @typedef {Doc | Doc[] | '' | null | undefined | false} Part
 */

/** @type {Line} */
export const line = { t: 'line' };
/** @type {Line} */
export const softline = { t: 'line', soft: true };
/** @type {Line} */
export const hardline = { t: 'line', hard: true };

/** @type {(...parts: Part[]) => Doc} */
export const concat = (...parts) => ({ t: 'concat', parts: parts.flat().filter((p) => p !== '' && p !== null && p !== undefined && p !== false) });
/** @type {(doc: Doc, opts?: { shouldBreak?: boolean }) => Doc} */
export const group = (doc, { shouldBreak = false } = {}) => ({ t: 'group', doc, break: shouldBreak });
/** @type {(doc: Doc) => Doc} */
export const indent = (doc) => ({ t: 'indent', doc });
/** @type {(broken: Doc, flat?: Doc) => Doc} */
export const ifBreak = (broken, flat = '') => ({ t: 'if-break', broken, flat });
/**
 * @param {Doc} sep
 * @param {readonly Doc[]} docs
 * @returns {Doc}
 */
export function join(sep, docs) {
  const out = [];
  docs.forEach((d, i) => {
    if (i) out.push(sep);
    out.push(d);
  });
  return concat(...out);
}

// propagate marks every group that contains a hard line as broken.
function propagate(doc) {
  if (typeof doc === 'string') return false;
  switch (doc.t) {
    case 'line': return Boolean(doc.hard);
    case 'concat': return doc.parts.map(propagate).some(Boolean);
    case 'indent': return propagate(doc.doc);
    case 'if-break': return [propagate(doc.broken), propagate(doc.flat)].some(Boolean);
    case 'group': {
      // A broken group (a hard line, or shouldBreak) breaks its parents.
      const hard = propagate(doc.doc) || doc.break;
      if (hard) doc.break = true;
      return hard;
    }
    default: throw new Error(`doc: unknown node ${doc.t}`);
  }
}

const BREAK = 1;
const FLAT = 2;

// fits reports whether the next command, then the rest of the stack up to
// its first line break, fit in width columns.
function fits(next, rest, width, tabWidth) {
  const cmds = [next];
  let r = rest.length;
  while (width >= 0) {
    if (!cmds.length) {
      if (!r) return true;
      cmds.push(rest[--r]);
      continue;
    }
    const [ind, mode, doc] = cmds.pop();
    if (typeof doc === 'string') {
      width -= doc.replaceAll('\t', ' '.repeat(tabWidth)).length;
      continue;
    }
    switch (doc.t) {
      case 'concat': for (let i = doc.parts.length - 1; i >= 0; i--) cmds.push([ind, mode, doc.parts[i]]); break;
      case 'indent': cmds.push([ind + 1, mode, doc.doc]); break;
      case 'group': cmds.push([ind, doc.break ? BREAK : mode, doc.doc]); break;
      case 'if-break': cmds.push([ind, mode, mode === BREAK ? doc.broken : doc.flat]); break;
      case 'line':
        if (mode === BREAK || doc.hard) return true;
        if (!doc.soft) width -= 1;
        break;
      default: throw new Error(`doc: unknown node ${doc.t}`);
    }
  }
  return false;
}

// print lays doc out in width columns, indenting with unit (a tab counts
// tabWidth columns).
/**
 * @param {Doc} doc
 * @param {{ width?: number, unit?: string, tabWidth?: number }} [opts]
 * @returns {string}
 */
export function print(doc, { width = 80, unit = '  ', tabWidth = 4 } = {}) {
  propagate(doc);
  const unitWidth = unit === '\t' ? tabWidth : unit.length;
  const out = [];
  let pos = 0;
  const stack = [[0, BREAK, doc]];
  while (stack.length) {
    const [ind, mode, d] = stack.pop();
    if (typeof d === 'string') {
      out.push(d);
      pos += d.length;
      continue;
    }
    switch (d.t) {
      case 'concat': for (let i = d.parts.length - 1; i >= 0; i--) stack.push([ind, mode, d.parts[i]]); break;
      case 'indent': stack.push([ind + 1, mode, d.doc]); break;
      case 'group': {
        if (mode === FLAT && !d.break) {
          stack.push([ind, FLAT, d.doc]);
          break;
        }
        const flat = [ind, FLAT, d.doc];
        stack.push(!d.break && fits(flat, stack, width - pos, tabWidth) ? flat : [ind, BREAK, d.doc]);
        break;
      }
      case 'if-break': stack.push([ind, mode, mode === BREAK ? d.broken : d.flat]); break;
      case 'line':
        if (mode === FLAT && !d.hard) {
          if (!d.soft) {
            out.push(' ');
            pos += 1;
          }
          break;
        }
        while (out.length && / +$/.test(out.at(-1))) out[out.length - 1] = out.at(-1).replace(/ +$/, '');
        out.push(`\n${unit.repeat(ind)}`);
        pos = ind * unitWidth;
        break;
      default: throw new Error(`doc: unknown node ${d.t}`);
    }
  }
  return out.join('').replace(/[ \t]+\n/g, '\n');
}
