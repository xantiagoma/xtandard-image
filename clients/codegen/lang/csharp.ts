// C#: leaf tokens and the printer (WP-146). Braces go on their own lines
// (Allman, as `dotnet format` writes them), XML doc comments are one
// Comment node per element (its mods: the tag, then attribute name/value
// pairs), properties are fields with accessor mods (get, init, set), and
// file-scoped namespaces print as `namespace X;`.
//
// C# nodes besides the shared ones (built with the helpers below):
//   cs.init       an object or collection initializer after a `new`
//   cs.ctor       a constructor with its `: base(…)` initializer
//   cs.paren      a parenthesized expression
//   cs.next-line  an expression on the line after its `=`, indented
//   cs.array      T[]
//   cs.tuple-type (T Name, U Other)
//   cs.type-param a type parameter with its `where` constraint
//   cs.directive  #nullable enable

import { concat, type Doc, empty, hardline, indent, join, lines } from "../doc.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { pascal } from "../lib.ts";
import { Printer } from "../printer.ts";

/** C#'s reserved keywords (an identifier that is one is written @name). */
export const keywords: ReadonlySet<string> = new Set(
  (
    "abstract as base bool break byte case catch char checked class const continue decimal default delegate do double " +
    "else enum event explicit extern false finally fixed float for foreach goto if implicit in int interface internal " +
    "is lock long namespace new null object operator out override params private protected public readonly ref return " +
    "sbyte sealed short sizeof stackalloc static string struct switch this throw true try typeof uint ulong unchecked " +
    "unsafe ushort using virtual void volatile while"
  ).split(" "),
);

/** A keyword-safe identifier: `class` → `@class`. */
export const escape = (id: string): string => (keywords.has(id) ? `@${id}` : id);

/** A camelCase name ("x_offset" → "xOffset"), not yet keyword-escaped. */
export function camelName(name: string): string {
  const p = pascal(name);
  return p.length === 0 ? "arg" : p[0]!.toLowerCase() + p.slice(1);
}

/** A C# type or member name for an option name or alias ("c_obj" → "CObj", "min-width" → "MinWidth"). */
export function member(name: string): string {
  const p = pascal(name);
  return /^[0-9]/.test(p) ? `_${p}` : p;
}

/** XML-escapes doc text and turns `code` spans into <c>code</c>. */
export function xml(s: string): string {
  return s
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replace(/`([^`\n]+)`/g, "<c>$1</c>");
}

/** The mod that puts an expression body's `=>` on the next line. */
export const ARROW_NEXT_LINE = "cs:arrow-next-line";

// ---- C# node constructors ------------------------------------------------------------

/** One XML doc element (`/// <tag a="v">text</tag>`); no text is a self-closing element. */
export const xdoc = (tag: string, text: string | undefined, attrs: readonly (readonly [string, string])[] = []): M.Comment =>
  N.comment("doc", text === undefined ? [] : text.split("\n"), { mods: [tag, ...attrs.flat()] });
export const init = (target: M.Expr, items: readonly (M.Expr | M.Entry)[], layout: M.Layout) => N.ext("cs.init", { target, items, layout });
export const ctor = (func: M.Func, base: readonly M.Expr[]) => N.ext("cs.ctor", { func, base });
export const paren = (expr: M.Expr) => N.ext("cs.paren", { expr });
export const nextLine = (expr: M.Expr) => N.ext("cs.next-line", { expr });
export const array = (elem: M.TypeRef) => N.ext("cs.array", { elem });
export const tupleType = (elems: readonly (readonly [M.TypeRef, string])[]) => N.ext("cs.tuple-type", { elems });
export const typeParam = (name: string, constraint?: M.TypeRef) => N.ext("cs.type-param", { name, constraint });
export const directive = (name: string, value: string) => N.ext("cs.directive", { name, value });

const accessors = new Set(["get", "init", "set"]);

export class CSharpPrinter extends Printer {
  override unit = "    ";
  override semi = ";";

  /** The declaration modifiers of a node (keywords before it), without the printer's own flags. */
  private modifiers(mods: M.Mods): Doc[] {
    return mods.filter((m) => !accessors.has(m) && !m.startsWith("cs:")).map((m) => concat(m, " "));
  }

  /** An Allman block: the head, then `{`, the items indented, `}`. */
  private braces(head: Doc, items: readonly M.Node[]): Doc {
    const body = items.length ? concat(indent(concat(hardline, this.items(items))), hardline) : hardline;
    return concat(head, hardline, "{", body, "}");
  }

  // An XML doc element: one line when its text is, else the tags on their own lines.
  override comment(c: M.Comment): Doc {
    if (c.style !== "doc") return this.marked("//", c.lines);
    const [tag, ...attrs] = c.mods;
    if (tag === undefined) throw new Error("csharp: a doc comment needs its tag");
    const attrDocs: Doc[] = [];
    for (let i = 0; i + 1 < attrs.length; i += 2) attrDocs.push(concat(" ", attrs[i]!, '="', attrs[i + 1]!, '"'));
    const open = concat("<", tag, ...attrDocs, ">");
    const close = concat("</", tag, ">");
    if (c.lines.length === 0) return concat("/// <", tag, ...attrDocs, "/>");
    const text = xml(c.lines.join("\n").trim()).split("\n");
    if (text.length === 1) return concat("/// ", open, text[0]!, close);
    return lines([concat("/// ", open), this.marked("///", text), concat("/// ", close)]);
  }

  override import(i: M.Import): Doc {
    return concat("using ", i.module, ";");
  }

  // A file-scoped namespace: the declaration, an empty line, the items.
  override namespace(n: M.Namespace): Doc {
    return lines([concat("namespace ", n.name, ";"), empty, this.items(n.items)]);
  }

  override annotation(a: M.Annotation): Doc {
    const args = a.args.map((x) => (x.k === "named" ? concat(x.name, " = ", this.node(x.value)) : this.node(x)));
    return concat("[", a.name, args.length ? concat("(", join(", ", args), ")") : empty, "]");
  }

  /** Annotations on their own lines, then the declaration. */
  private attributed(annotations: readonly M.Annotation[], d: Doc): Doc {
    return lines([...annotations.map((a) => this.annotation(a)), d]);
  }

  override type(t: M.TypeDecl): Doc {
    const params = t.typeParams.map((p) => (p.k === "cs.type-param" ? ((p as M.Ext)["name"] as string) : this.node(p)));
    const where = t.typeParams.filter((p): p is M.Ext => p.k === "cs.type-param" && (p as M.Ext)["constraint"] !== undefined);
    const head = concat(
      ...this.modifiers(t.mods),
      t.kind,
      " ",
      t.name,
      params.length ? concat("<", join(", ", params), ">") : empty,
      t.params.length ? concat("(", join(", ", t.params.map((p) => this.param(p))), ")") : empty,
      t.supers.length ? concat(" : ", join(", ", t.supers.map((s) => this.node(s)))) : empty,
      ...where.map((p) => indent(concat(hardline, "where ", p["name"] as string, " : ", this.node(p["constraint"] as M.TypeRef)))),
    );
    return this.attributed(t.annotations, this.braces(head, t.members));
  }

  override enum(e: M.Enum): Doc {
    const head = concat(...this.modifiers(e.mods), "enum ", e.name);
    const members = e.members.map((m) => this.enumMember(m));
    return this.attributed(e.annotations, concat(head, hardline, "{", indent(concat(hardline, lines(members))), hardline, "}"));
  }

  override enumMember(m: M.EnumMember): Doc {
    const d = concat(m.name, m.value ? concat(" = ", this.node(m.value)) : empty, ",");
    return m.doc ? lines([this.node(m.doc), d]) : d;
  }

  // A field, or a property when it has accessors ({ get; init; }).
  override field(f: M.Field): Doc {
    const acc = f.mods.filter((m) => accessors.has(m));
    const decl = concat(
      ...this.modifiers(f.mods),
      f.type ? concat(this.node(f.type), " ") : empty,
      f.name,
      acc.length ? concat(" { ", join(" ", acc.map((a) => concat(a, ";"))), " }") : empty,
      f.value ? concat(" = ", this.node(f.value), ";") : acc.length ? empty : ";",
    );
    return this.attributed(f.annotations, decl);
  }

  override const(c: M.Const): Doc {
    return this.field({ ...c, k: "field" });
  }

  /** The head of a method: modifiers, return type, name, parameters. */
  private funcHead(f: M.Func): Doc {
    return concat(
      ...this.modifiers(f.mods),
      f.returns ? concat(this.node(f.returns), " ") : empty,
      f.name,
      "(",
      join(", ", f.params.map((p) => this.param(p))),
      ")",
    );
  }

  override func(f: M.Func): Doc {
    const head = this.funcHead(f);
    if (f.body === undefined) return this.attributed(f.annotations, concat(head, ";"));
    if (isStmts(f.body)) return this.attributed(f.annotations, this.braces(head, f.body));
    const arrow = concat("=> ", this.node(f.body), ";");
    return this.attributed(f.annotations, f.mods.includes(ARROW_NEXT_LINE) ? concat(head, indent(concat(hardline, arrow))) : concat(head, " ", arrow));
  }

  override param(p: M.Param): Doc {
    return concat(
      ...this.modifiers(p.mods),
      p.type ? concat(this.node(p.type), " ") : empty,
      p.name,
      p.value ? concat(" = ", this.node(p.value)) : empty,
    );
  }

  override let(l: M.Let): Doc {
    return concat(l.type ? this.node(l.type) : "var", " ", join(", ", l.names), l.value ? concat(" = ", this.node(l.value)) : empty, ";");
  }

  override forEach(s: M.ForEach): Doc {
    return this.braces(concat("foreach (", s.type ? this.node(s.type) : "var", " ", s.name, " in ", this.node(s.iterable), ")"), s.body);
  }

  override named(n: M.Named): Doc {
    return concat(n.name, ": ", this.node(n.value));
  }

  // A dictionary initializer entry.
  override entry(e: M.Entry): Doc {
    return concat("[", this.node(e.key), "] = ", this.node(e.value));
  }

  override node(n: M.Node): Doc {
    if (n.k === "spread") return concat(".. ", this.node((n as M.Spread).expr));
    return super.node(n);
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "cs.init": {
        const target = this.node(n["target"] as M.Expr);
        const items = (n["items"] as (M.Expr | M.Entry)[]).map((i) => this.node(i));
        if (items.length === 0) return target;
        if (n["layout"] === "expanded") {
          return concat(target, hardline, "{", indent(concat(hardline, lines(items.map((i) => concat(i, ","))))), hardline, "}");
        }
        return concat(target, " { ", join(", ", items), " }");
      }
      case "cs.ctor": {
        const f = n["func"] as M.Func;
        const base = concat(": base(", join(", ", (n["base"] as M.Expr[]).map((a) => this.node(a))), ")");
        const body = f.body !== undefined && isStmts(f.body) ? f.body : [];
        return this.braces(concat(this.funcHead(f), indent(concat(hardline, base))), body);
      }
      case "cs.paren":
        return concat("(", this.node(n["expr"] as M.Expr), ")");
      case "cs.next-line":
        return indent(concat(hardline, this.node(n["expr"] as M.Expr)));
      case "cs.array":
        return concat(this.node(n["elem"] as M.TypeRef), "[]");
      case "cs.tuple-type":
        return concat("(", join(", ", (n["elems"] as [M.TypeRef, string][]).map(([t, name]) => concat(this.node(t), " ", name))), ")");
      case "cs.type-param":
        return n["name"] as string;
      case "cs.directive":
        return concat("#", n["name"] as string, " ", n["value"] as string);
      default:
        return super.ext(n);
    }
  }
}
