// Python: leaf tokens and the printer (WP-146). Docstrings follow the
// declaration they document (a module constant, a type alias, a TypedDict
// field) or open the body (a class, a function); blocks are indented by
// four spaces after a colon.

import { concat, type Doc, empty, hardline, indent, join, lines } from "../doc.ts";
import type * as M from "../model.ts";
import { isStmts } from "../model.ts";
import { Printer } from "../printer.ts";

/** Python's keywords (an identifier that is one gets a trailing underscore). */
export const keywords: ReadonlySet<string> = new Set(
  (
    "False None True and as assert async await break class continue def del elif else except finally for from " +
    "global if import in is lambda nonlocal not or pass raise return try while with yield"
  ).split(" "),
);

/** A Python identifier for an option name or alias ("min-width" → "min_width", "if" → "if_"). */
export function ident(name: string): string {
  let id = name.replace(/[^A-Za-z0-9_]/g, "_");
  if (/^[0-9]/.test(id)) id = `_${id}`;
  return keywords.has(id) ? `${id}_` : id;
}

/** Whether a name is a plain Python identifier and no keyword. */
export const isIdent = (name: string): boolean => /^[A-Za-z_][A-Za-z0-9_]*$/.test(name) && !keywords.has(name);

/** The text of a docstring: backslashes and triple quotes escaped, trimmed, by line. */
export function docstringLines(text: readonly string[]): string[] {
  return text.join("\n").replaceAll("\\", "\\\\").replaceAll('"""', '\\"\\"\\"').trim().split("\n");
}

export class PythonPrinter extends Printer {
  override unit = "    ";

  override nullToken(): Doc {
    return "None";
  }
  override bool(b: boolean): Doc {
    return b ? "True" : "False";
  }

  // A docstring: """text""" on one line, or the closing quotes on their own line.
  override comment(c: M.Comment): Doc {
    if (c.style === "line") return this.marked("#", c.lines);
    const text = docstringLines(c.lines);
    return concat('"""', lines(text), text.length > 1 ? hardline : empty, '"""');
  }

  override import(i: M.Import): Doc {
    if (i.names.length === 0) return concat("import ", i.module);
    return concat("from ", i.module, " import ", join(", ", i.names));
  }

  /** A declaration line with its docstring after it. */
  private documented(d: Doc, doc: M.Comment | undefined): Doc {
    return doc ? lines([d, this.node(doc)]) : d;
  }

  override typeAlias(t: M.TypeAlias): Doc {
    return this.documented(concat(t.name, " = ", this.node(t.type)), t.doc);
  }

  override const(c: M.Const): Doc {
    const decl = concat(c.name, c.type ? concat(": ", this.node(c.type)) : empty, c.value ? concat(" = ", this.node(c.value)) : empty);
    return this.documented(decl, c.doc);
  }

  override field(f: M.Field): Doc {
    return this.const({ ...f, k: "const" });
  }

  /** A suite: the head, then the docstring and the items indented. */
  private suite(head: Doc, doc: M.Comment | undefined, items: readonly M.Node[]): Doc {
    const body = [...(doc ? [this.node(doc)] : []), ...items.map((n) => this.node(n))];
    return concat(head, indent(concat(hardline, lines(body))));
  }

  override type(t: M.TypeDecl): Doc {
    const bases = [...t.supers.map((s) => this.node(s)), ...t.params.map((p) => this.param(p))];
    const head = concat("class ", t.name, bases.length ? concat("(", join(", ", bases), ")") : empty, ":");
    return this.suite(head, t.doc, t.members);
  }

  override func(f: M.Func): Doc {
    if (!f.body || !isStmts(f.body)) throw new Error(`python: def ${f.name} needs a statement body`);
    const head = concat("def ", f.name, "(", join(", ", f.params.map((p) => this.param(p))), ")", f.returns ? concat(" -> ", this.node(f.returns)) : empty, ":");
    return this.suite(head, f.doc, f.body);
  }

  override param(p: M.Param): Doc {
    const star = p.mods.includes("variadic") ? "*" : empty;
    const type = p.type ? concat(": ", this.node(p.type)) : empty;
    // A keyword in a class's bases (total=False) or a default value.
    const value = p.value ? concat(p.type ? " = " : "=", this.node(p.value)) : empty;
    return concat(star, p.name, type, value);
  }

  override if(s: M.If): Doc {
    const head = this.suite(concat("if ", this.node(s.cond), ":"), undefined, s.then);
    return s.else ? lines([head, this.suite("else:", undefined, s.else)]) : head;
  }

  override throw(t: M.Throw): Doc {
    return concat("raise ", this.node(t.value));
  }

  override named(n: M.Named): Doc {
    return concat(n.name, "=", this.node(n.value));
  }

  override list(l: M.List): Doc {
    // A one-element tuple keeps its comma: (x,).
    if (l.style === "tuple" && l.items.length === 1) return concat("(", this.node(l.items[0]!), ",)");
    return super.list(l);
  }

  override node(n: M.Node): Doc {
    if (n.k === "spread") return concat("*", this.node((n as M.Spread).expr));
    return super.node(n);
  }

  override tref(t: M.TRef): Doc {
    if (!t.args.length) return t.name;
    return concat(t.name, this.bracketed("[", t.args.map((a) => this.node(a)), "]", t.layout));
  }
}
