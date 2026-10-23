// Kotlin: leaf tokens and the printer (WP-146). KDoc comments ("/** … */",
// one line when the comment has the "inline" mod), declarations with their
// modifiers in the order the refiner gives them, expression-bodied members
// ("fun f(): T = …"), enum classes with constructor parameters, data
// classes with annotated constructor properties, and trailing lambdas.

import { concat, type Doc, empty, hardline, indent, join, lines } from "../doc.ts";
import { words } from "../lib.ts";
import type * as M from "../model.ts";
import { isStmts } from "../model.ts";
import { Printer } from "../printer.ts";

/** Kotlin's hard keywords (an identifier that is one gets a "Value" suffix). */
export const keywords: ReadonlySet<string> = new Set(
  (
    "as break class continue do else false for fun if in interface is null object package return super this throw " +
    "true try typealias typeof val var when while"
  ).split(" "),
);

/** camelCase Kotlin identifier of a name ("min-width" → "minWidth", "R" → "r", "in" → "inValue"). */
export function camel(name: string): string {
  const ws = words(name);
  let id = ws.map((w, i) => (i === 0 ? w.toLowerCase() : w[0]!.toUpperCase() + w.slice(1).toLowerCase())).join("");
  if (id === "") id = "value";
  if (/^[0-9]/.test(id)) id = `v${id}`;
  return keywords.has(id) ? `${id}Value` : id;
}

/** UPPER_SNAKE constant name of a value ("fill-down" → "FILL_DOWN", "m3u8" → "M3U8", "3d" → "V3D"). */
export function constant(value: string): string {
  let id = words(value)
    .map((w) => w.toUpperCase())
    .join("_");
  if (/^[0-9]/.test(id)) id = `V${id}`;
  return id;
}

/** KDoc text: no comment openers or terminators, no Markdown links or code spans. */
export function kdocText(text: string): string {
  return text.replaceAll("*/", "*&#47;").replaceAll("/*", "&#47;*").replaceAll("[", "\\[").replaceAll("]", "\\]").replaceAll("`", "'");
}

/** A Kotlin string literal: JSON escapes, and "$" escaped so no template starts. */
export const stringLiteral = (s: string): string => JSON.stringify(s).replaceAll("$", "\\$");

/** A constructor parameter with its own one-line KDoc before it. */
export type KtParam = M.Param & { readonly doc?: M.Comment };

/** A supertype written with its constructor call (ParamList<T>()). */
export interface SuperCall extends M.Ext {
  readonly k: "kt.super-call";
  readonly type: M.TypeRef;
}

/** The package line. */
export interface Package extends M.Ext {
  readonly k: "kt.package";
  readonly name: string;
}

export class KotlinPrinter extends Printer {
  override unit = "    ";

  override string(s: string): Doc {
    return stringLiteral(s);
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "kt.package":
        return concat("package ", (n as Package).name);
      case "kt.super-call":
        return concat(this.node((n as SuperCall).type), "()");
      default:
        return super.ext(n);
    }
  }

  override comment(c: M.Comment): Doc {
    if (c.style === "doc" && c.mods.includes("inline")) return concat("/** ", join(" ", c.lines), " */");
    return super.comment(c);
  }

  override import(i: M.Import): Doc {
    return concat("import ", i.module);
  }

  /** Modifiers, then the keyword ("public data class", "public const val"). */
  private head(mods: M.Mods, keyword: Doc): Doc {
    return join(" ", [...mods, keyword]);
  }

  override param(p: M.Param): Doc {
    const kp = p as KtParam;
    const parts: Doc[] = [];
    if (kp.doc) parts.push(this.node(kp.doc), " ");
    for (const a of p.annotations) parts.push(this.node(a), " ");
    const mods = p.mods.filter((m) => m !== "variadic");
    if (p.mods.includes("variadic")) parts.push("vararg ");
    if (mods.length) parts.push(join(" ", mods), " ");
    parts.push(p.name);
    if (p.type) parts.push(": ", this.node(p.type));
    if (p.value) parts.push(" = ", this.node(p.value));
    return concat(...parts);
  }

  override const(c: M.Const): Doc {
    const decl = concat(this.head(c.mods, "val"), " ", c.name, c.type ? concat(": ", this.node(c.type)) : empty);
    if (!c.value) return this.decorated(c, decl);
    // A table (an expanded call) starts on the line after "=".
    const expanded = c.value.k === "call" && (c.value as M.Call).layout === "expanded";
    const value = expanded ? concat(" =", indent(concat(hardline, this.node(c.value)))) : concat(" = ", this.node(c.value));
    return this.decorated(c, concat(decl, value));
  }

  override field(f: M.Field): Doc {
    return this.const({ ...f, k: "const" });
  }

  override func(f: M.Func): Doc {
    const sig = concat(
      this.head(f.mods, "fun"),
      " ",
      f.name,
      "(",
      join(", ", f.params.map((p) => this.param(p))),
      ")",
      f.returns ? concat(": ", this.node(f.returns)) : empty,
    );
    if (f.body !== undefined && isStmts(f.body)) throw new Error(`kotlin: fun ${f.name} needs an expression body`);
    return this.decorated(f, f.body === undefined ? sig : concat(sig, " = ", this.node(f.body)));
  }

  override type(t: M.TypeDecl): Doc {
    const keyword = t.kind === "object" ? "object" : t.kind === "interface" ? "interface" : "class";
    const parts: Doc[] = [this.head(t.mods, keyword)];
    if (t.name) parts.push(" ", t.name);
    if (t.typeParams.length) parts.push(this.typeArgs(t.typeParams));
    if (t.params.length) parts.push(this.bracketed("(", t.params.map((p) => this.param(p)), ")", "expanded"));
    if (t.supers.length) parts.push(" : ", join(", ", t.supers.map((s) => this.node(s))));
    if (t.members.length === 0 && t.params.length) return this.decorated(t, concat(...parts));
    parts.push(" {");
    return this.decorated(t, concat(...parts, indent(concat(hardline, this.members(t.members))), hardline, "}"));
  }

  /** A class body: enum entries separated by "," and closed by ";", then the other members. */
  private members(list: readonly M.Node[]): Doc {
    const last = list.findLastIndex((n) => n.k === "enum-member");
    return lines(list.map((n, i) => (n.k === "enum-member" ? concat(this.node(n), i === last ? ";" : ",") : this.node(n))));
  }

  override enumMember(m: M.EnumMember): Doc {
    const args = m.value ? [m.value, ...m.args] : m.args;
    return this.decorated(m, concat(m.name, this.args(args)));
  }

  override call(c: M.Call): Doc {
    // A lone lambda argument trails the call: entries.firstOrNull { … }.
    if (c.args.length === 1 && c.args[0]!.k === "lambda") return concat(this.node(c.fn), " ", this.node(c.args[0]!));
    return super.call(c);
  }

  override lambda(l: M.Lambda): Doc {
    if (isStmts(l.body)) throw new Error("kotlin: lambdas take one expression");
    const params = l.params.length ? concat(join(", ", l.params.map((p) => this.param(p))), " -> ") : empty;
    return concat("{ ", params, this.node(l.body), " }");
  }

  override node(n: M.Node): Doc {
    if (n.k === "spread") return concat("*", this.node((n as M.Spread).expr));
    return super.node(n);
  }
}
