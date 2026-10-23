// Java: leaf tokens and the printer (WP-146). Two-space blocks; a broken
// argument list, record header or initializer continues four spaces deeper
// with the closing parenthesis on the last line (google-java-format's
// style); a one-line Javadoc is written /** … */.

import { concat, type Doc, empty, group, hardline, indent, join, line, lines, softline } from "../doc.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { Printer } from "../printer.ts";
import { words } from "../lib.ts";

/** Java's keywords and literal words (an identifier that is one gets "Value" appended). */
export const keywords: ReadonlySet<string> = new Set(
  (
    "abstract assert boolean break byte case catch char class const continue default do double else enum extends " +
    "final finally float for goto if implements import instanceof int interface long native new package private " +
    "protected public return short static strictfp super switch synchronized this throw throws transient try void " +
    "volatile while true false null var yield record sealed permits"
  ).split(" "),
);

/** camelCase Java identifier of a name ("min-width" → "minWidth", "R" → "r", "if" → "ifValue"). */
export function camel(name: string): string {
  const ws = words(name);
  let id = ws.map((w, i) => (i === 0 ? w.toLowerCase() : w[0]!.toUpperCase() + w.slice(1).toLowerCase())).join("");
  if (id === "") id = "value";
  if (/^[0-9]/.test(id)) id = `v${id}`;
  return keywords.has(id) ? `${id}Value` : id;
}

/** UPPER_SNAKE constant name of a value ("fill-down" → "FILL_DOWN", "m3u8" → "M3U8"). */
export function constant(value: string): string {
  let id = words(value)
    .map((w) => w.toUpperCase())
    .join("_");
  if (/^[0-9]/.test(id)) id = `V${id}`;
  return id;
}

/** Text for a Javadoc comment: HTML-escaped, no comment terminators, no unicode escapes. */
export function jd(text: string): string {
  return text
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\\", "&#92;")
    .replaceAll("*/", "*&#47;")
    .replaceAll("@", "&#64;")
    .replaceAll("{", "&#123;")
    .replaceAll("}", "&#125;");
}

// ---- Java's own nodes -------------------------------------------------------------

/** `package a.b;`. */
export interface Package extends M.Ext {
  readonly k: "java.package";
  readonly name: string;
}
/** A type-use annotation on a type: `@Nullable String`. */
export interface Annotated extends M.Ext {
  readonly k: "java.annotated";
  readonly annotation: string;
  readonly type: M.TypeRef;
}
/** A type parameter with a bound: `S extends Methods<S>`. */
export interface TypeParam extends M.Ext {
  readonly k: "java.type-param";
  readonly name: string;
  readonly bound: M.TypeRef;
}
/** An array creation with an initializer: `new T[] {a, b}`. */
export interface NewArray extends M.Ext {
  readonly k: "java.new-array";
  readonly type: M.TypeRef;
  readonly items: readonly M.Expr[];
}
/** A switch expression with arrow arms: `switch (x) { case A -> v; }`. */
export interface SwitchExpr extends M.Ext {
  readonly k: "java.switch-expr";
  readonly subject: M.Expr;
  readonly arms: readonly { readonly match: M.Expr; readonly value: M.Expr }[];
}

// N.ext types k as any "<lang>.<kind>"; the casts narrow it to the literal kind.
export const J = {
  package: (name: string) => N.ext("java.package", { name }) as Package,
  annotated: (annotation: string, type: M.TypeRef) => N.ext("java.annotated", { annotation, type }) as Annotated,
  nullable: (type: M.TypeRef): Annotated => J.annotated("Nullable", type),
  typeParam: (name: string, bound: M.TypeRef) => N.ext("java.type-param", { name, bound }) as TypeParam,
  newArray: (type: M.TypeRef, items: readonly M.Expr[]) => N.ext("java.new-array", { type, items }) as NewArray,
  switchExpr: (subject: M.Expr, arms: SwitchExpr["arms"]) => N.ext("java.switch-expr", { subject, arms }) as SwitchExpr,
};

export class JavaPrinter extends Printer {
  override unit = "  ";
  override semi = ";";

  /** Arguments continued four spaces deeper, the closing parenthesis on the last line. */
  private continued(open: Doc, items: readonly Doc[], close: Doc): Doc {
    return concat(open, indent(indent(concat(hardline, join(concat(",", hardline), items)))), close);
  }

  override args(args: readonly M.Expr[], layout: M.Layout = "auto"): Doc {
    const items = args.map((a) => this.node(a));
    if (layout === "expanded") return this.continued("(", items, ")");
    // By width: on one line, or continued like an expanded list.
    return group(concat("(", indent(indent(concat(softline, join(concat(",", line), items)))), ")"));
  }

  override comment(c: M.Comment): Doc {
    if (c.style === "doc" && c.lines.length === 1) return concat("/** ", c.lines[0]!, " */");
    return super.comment(c);
  }

  override import(i: M.Import): Doc {
    return concat("import ", i.module, ";");
  }

  private mods(mods: M.Mods): Doc {
    return mods.length ? concat(join(" ", [...mods]), " ") : empty;
  }

  private typeParams(ps: readonly M.TypeRef[]): Doc {
    return ps.length ? concat("<", join(", ", ps.map((p) => this.node(p))), ">") : empty;
  }

  /** A braced body; an empty one is {}. */
  private braces(items: readonly M.Node[]): Doc {
    if (items.length === 0) return " {}";
    return concat(" {", indent(concat(hardline, this.items(items))), hardline, "}");
  }

  override type(t: M.TypeDecl): Doc {
    const keyword = t.kind === "struct" || t.kind === "object" ? "class" : t.kind;
    const head: Doc[] = [this.mods(t.mods), keyword, " ", t.name, this.typeParams(t.typeParams)];
    if (t.kind === "record") head.push(this.continued("(", t.params.map((p) => this.param(p)), ")"));
    if (t.supers.length) head.push(t.kind === "interface" ? " extends " : " extends ", join(", ", t.supers.map((s) => this.node(s))));
    return this.decorated(t, concat(...head, this.braces(t.members)));
  }

  override enum(e: M.Enum): Doc {
    const members = e.members.map((m, i) => concat(this.enumMember(m), i === e.members.length - 1 ? ";" : ","));
    const body = [...members, ...e.body.map((n) => this.node(n))];
    const head = concat(this.mods(e.mods), "enum ", e.name, " {");
    return this.decorated(e, concat(head, indent(concat(hardline, lines(body))), hardline, "}"));
  }

  override enumMember(m: M.EnumMember): Doc {
    return this.decorated(m, concat(m.name, m.args.length ? this.args(m.args) : empty));
  }

  /** A declaration with an initializer: a broken call continues on the next line. */
  private declaration(mods: M.Mods, type: M.TypeRef | undefined, name: string, value: M.Expr | undefined): Doc {
    const decl = concat(this.mods(mods), type ? concat(this.node(type), " ") : empty, name);
    if (!value) return concat(decl, ";");
    const v = this.node(value);
    const broken = (value.k === "call" || value.k === "new") && (value as M.Call | M.New).layout === "expanded";
    return concat(decl, broken ? concat(" =", indent(indent(concat(hardline, v)))) : concat(" = ", v), ";");
  }

  override field(f: M.Field): Doc {
    return this.decorated(f, this.declaration(f.mods, f.type, f.name, f.value));
  }

  override const(c: M.Const): Doc {
    return this.decorated(c, this.declaration(c.mods, c.type, c.name, c.value));
  }

  override let(l: M.Let): Doc {
    return this.declaration(l.mods, l.type, l.names.join(", "), l.value);
  }

  override func(f: M.Func): Doc {
    // No return type: a constructor.
    const head = concat(
      this.mods(f.mods),
      this.typeParams(f.typeParams),
      f.typeParams.length ? " " : empty,
      f.returns ? concat(this.node(f.returns), " ") : empty,
      f.name,
      "(",
      join(", ", f.params.map((p) => this.param(p))),
      ")",
    );
    let body: Doc;
    if (f.body === undefined) body = ";";
    else if (!isStmts(f.body)) throw new Error(`java: ${f.name} needs a statement body`);
    else body = this.braces(f.body);
    return this.decorated(f, concat(head, body));
  }

  override param(p: M.Param): Doc {
    return concat(p.type ? this.node(p.type) : empty, p.mods.includes("variadic") ? "..." : empty, " ", p.name);
  }

  override lambda(l: M.Lambda): Doc {
    if (isStmts(l.body)) throw new Error("java: block lambdas are not supported");
    return concat("(", join(", ", l.params.map((p) => p.name)), ") -> ", this.node(l.body));
  }

  override tref(t: M.TRef): Doc {
    return concat(t.name, t.args.length ? concat("<", join(", ", t.args.map((a) => this.node(a))), ">") : empty);
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "java.package":
        return concat("package ", (n as Package).name, ";");
      case "java.annotated": {
        const a = n as Annotated;
        return concat("@", a.annotation, " ", this.node(a.type));
      }
      case "java.type-param": {
        const p = n as TypeParam;
        return concat(p.name, " extends ", this.node(p.bound));
      }
      case "java.new-array": {
        const a = n as NewArray;
        return concat("new ", this.node(a.type), "[] {", join(", ", a.items.map((i) => this.node(i))), "}");
      }
      case "java.switch-expr": {
        const s = n as SwitchExpr;
        const arms = s.arms.map((a) => concat("case ", this.node(a.match), " -> ", this.node(a.value), ";"));
        return concat("switch (", this.node(s.subject), ") {", indent(concat(hardline, lines(arms))), hardline, "}");
      }
      default:
        return super.ext(n);
    }
  }
}
