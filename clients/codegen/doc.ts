// The Doc algebra of the code generator. It is the playground's
// (internal/playground/assets/snippets/doc.js, WP-145), imported by relative
// path so the repository has one layout engine: go:embed only reads files
// below internal/playground, so the module cannot move out of it, and the
// generator is development tooling that may reach into the tree. Its JSDoc
// types are what TypeScript sees (tsconfig allowJs); doc.test.ts checks that
// these exports are that module's.
//
// Besides the algebra (text, concat, group, indent, line, softline,
// hardline, ifBreak, join, print), the helpers below are what the
// declaration printers share.

import {
  concat,
  type Doc,
  group,
  hardline,
  ifBreak,
  indent,
  join,
  line,
  type Part,
  print,
  softline,
} from "../../internal/playground/assets/snippets/doc.js";

export { concat, type Doc, group, hardline, ifBreak, indent, join, line, type Part, print, softline };

/** An empty Doc. */
export const empty: Doc = concat();

/** Docs one per line. */
export function lines(docs: readonly Doc[]): Doc {
  return join(hardline, docs);
}

/** Docs one per line with an empty line between them. */
export function paragraphs(docs: readonly Doc[]): Doc {
  return join(concat(hardline, hardline), docs);
}

/** A body indented one level under its opening line: open, the docs one per line, close. */
export function block(open: Doc, body: readonly Doc[], close: Doc): Doc {
  if (body.length === 0) return concat(open, hardline, close);
  return concat(open, indent(concat(hardline, lines(body))), hardline, close);
}

/** Options of {@link bracket}. */
export interface BracketOptions {
  /** The separator after each item ("," by default). */
  readonly sep?: Doc;
  /** A separator after the last item when the brackets break. */
  readonly trailing?: boolean;
  /** Spaces inside the brackets on one line ("{ a }"). */
  readonly pad?: boolean;
  /** Always one item per line. */
  readonly force?: boolean;
}

/**
 * Items between open and close: on one line when they fit, else one per
 * line, indented (the layout decides; force always breaks).
 */
export function bracket(open: Doc, items: readonly Doc[], close: Doc, opts: BracketOptions = {}): Doc {
  const { sep = ",", trailing = true, pad = false, force = false } = opts;
  if (items.length === 0) return concat(open, close);
  const inner = pad ? line : softline;
  return group(concat(open, indent(concat(inner, join(concat(sep, line), items))), ifBreak(trailing ? sep : ""), inner, close), {
    shouldBreak: force,
  });
}
