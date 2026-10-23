// Swift: leaf tokens and the printer (WP-146). Doc comments are `///`
// lines before the declaration; blocks are braced and indented by four
// spaces; a switch's cases sit at the switch's indentation, each a one-line
// switch-expression arm. Swift's own nodes: swift.array ([T]), swift.dict
// ([K: V]), swift.tuple-type ((a: A, b: B)), swift.optional (T?, (any T)?),
// swift.any (any T), swift.dot (.member), swift.computed (a computed
// property) and swift.guard-let (guard let x = v else { … }).

import { concat, type Doc, empty, hardline, indent, join } from "../doc.ts";
import { pascal, words } from "../lib.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { Printer } from "../printer.ts";

/** Swift keywords that need backticks as identifiers. */
export const keywords: ReadonlySet<string> = new Set(
  (
    "associatedtype class deinit enum extension fileprivate func import init inout internal let open operator private " +
    "precedencegroup protocol public rethrows static struct subscript typealias var break case catch continue default " +
    "defer do else fallthrough for guard if in repeat return throw switch where while as false is nil self Self super " +
    "throws true try Any Type"
  ).split(" "),
);

/** lowerCamelCase of a name ("min-width" → "minWidth", "c_obj" → "cObj", "3d" → "v3d"). */
export function camel(name: string): string {
  const p = pascal(name);
  if (p === "") throw new Error(`${JSON.stringify(name)} has no identifier characters`);
  const id = p[0]!.toLowerCase() + p.slice(1);
  return /^[0-9]/.test(id) ? `v${id}` : id;
}

/** An identifier, backticked when it is a keyword. */
export function escape(id: string): string {
  return keywords.has(id) ? `\`${id}\`` : id;
}

/** The lowerCamelCase identifier of a name, backticked when it is a keyword. */
export const ident = (name: string): string => escape(camel(name));

/** A property name: lowerCamelCase with `ID` for an `id` word (unescaped; see escape). */
export function property(p: string): string {
  return words(p)
    .map((w, i) => {
      const l = w.toLowerCase();
      if (i > 0 && l === "id") return "ID";
      return i === 0 ? l : l[0]!.toUpperCase() + l.slice(1);
    })
    .join("");
}

/** A Swift string literal: backslash (so `\(` cannot interpolate), quote and control characters escaped. */
export function string(s: string): string {
  let out = '"';
  for (const ch of s) {
    const c = ch.codePointAt(0)!;
    if (ch === "\\") out += "\\\\";
    else if (ch === '"') out += '\\"';
    else if (ch === "\n") out += "\\n";
    else if (ch === "\t") out += "\\t";
    else if (ch === "\r") out += "\\r";
    else if (c < 0x20 || c === 0x7f) out += `\\u{${c.toString(16)}}`;
    else out += ch;
  }
  return `${out}"`;
}

// ---- Swift's own nodes -----------------------------------------------------------

/** [T]. */
export const array = (elem: M.TypeRef) => N.ext("swift.array", { elem });
/** [K: V]. */
export const dict = (key: M.TypeRef, value: M.TypeRef) => N.ext("swift.dict", { key, value });
/** (label: T, …). */
export const tupleType = (fields: readonly (readonly [string, M.TypeRef])[]) => N.ext("swift.tuple-type", { fields });
/** T? ((any T)? for an existential). */
export const optional = (type: M.TypeRef) => N.ext("swift.optional", { type });
/** any T. */
export const any = (type: M.TypeRef) => N.ext("swift.any", { type });
/** .name (an implicit member: an enum case, a static member). */
export const dot = (name: string) => N.ext("swift.dot", { name });
/** A computed property: mods var name: type { body }. */
export const computed = (name: string, type: M.TypeRef, body: readonly M.Stmt[], o: { doc?: M.Comment; mods?: M.Mods } = {}) =>
  N.ext("swift.computed", { name, type, body, mods: o.mods ?? [], doc: o.doc });
/** guard let name = value else { body }. */
export const guardLet = (name: string, value: M.Expr, otherwise: readonly M.Stmt[]) => N.ext("swift.guard-let", { name, value, otherwise });

type Computed = { name: string; type: M.TypeRef; body: readonly M.Stmt[]; mods: M.Mods; doc?: M.Comment };

export class SwiftPrinter extends Printer {
  override unit = "    ";

  override string(s: string): Doc {
    return string(s);
  }
  override nullToken(): Doc {
    return "nil";
  }

  // `///` lines: the text trimmed, each line trimmed, blank lines kept.
  override comment(c: M.Comment): Doc {
    if (c.style !== "doc") return super.comment(c);
    const text = c.lines
      .join("\n")
      .trim()
      .split("\n")
      .map((l) => l.trim());
    return this.marked("///", text);
  }

  private head(mods: M.Mods, keyword: Doc, name: string, supers: readonly M.TypeRef[]): Doc {
    return concat(
      ...mods.map((m) => concat(m, " ")),
      keyword,
      " ",
      name,
      supers.length ? concat(": ", join(", ", supers.map((s) => this.node(s)))) : empty,
    );
  }

  /** head { items } ("{}" when there are none). */
  private braced(head: Doc, items: readonly M.Node[]): Doc {
    if (items.length === 0) return concat(head, " {}");
    return this.body(concat(head, " {"), items, "}");
  }

  override enum(e: M.Enum): Doc {
    return this.decorated(e, this.braced(this.head(e.mods, "enum", e.name, e.supers), [...e.members, ...e.body]));
  }

  override enumMember(m: M.EnumMember): Doc {
    return this.decorated(m, concat("case ", m.name, m.value ? concat(" = ", this.node(m.value)) : empty));
  }

  override type(t: M.TypeDecl): Doc {
    return this.decorated(t, this.braced(this.head(t.mods, t.kind, t.name, t.supers), t.members));
  }

  override const(c: M.Const): Doc {
    const decl = concat(
      ...c.mods.map((m) => concat(m, " ")),
      "let ",
      c.name,
      c.type ? concat(": ", this.node(c.type)) : empty,
      c.value ? concat(" = ", this.node(c.value)) : empty,
    );
    return this.decorated(c, decl);
  }

  override field(f: M.Field): Doc {
    return this.decorated(f, concat(...f.mods.map((m) => concat(m, " ")), "var ", f.name, f.type ? concat(": ", this.node(f.type)) : empty));
  }

  override func(f: M.Func): Doc {
    const init = f.name === "init";
    const head = concat(
      ...f.mods.filter((m) => m !== "failable").map((m) => concat(m, " ")),
      init ? "init" : concat("func ", f.name),
      f.mods.includes("failable") ? "?" : empty,
      "(",
      join(", ", f.params.map((p) => this.param(p))),
      ")",
      f.returns ? concat(" -> ", this.node(f.returns)) : empty,
    );
    if (!f.body || !isStmts(f.body)) throw new Error(`swift: func ${f.name} needs a statement body`);
    return this.decorated(f, this.body(concat(head, " {"), f.body, "}"));
  }

  override param(p: M.Param): Doc {
    return concat(
      p.mods.includes("unlabeled") ? "_ " : empty,
      p.name,
      p.type ? concat(": ", this.node(p.type)) : empty,
      p.mods.includes("variadic") ? "..." : empty,
      p.value ? concat(" = ", this.node(p.value)) : empty,
    );
  }

  // Cases at the switch's indentation; an arm that is one expression stays on the case's line.
  override switch(s: M.Switch): Doc {
    return concat("switch ", s.subject ? concat(this.node(s.subject), " ") : empty, "{", hardline, this.items(s.cases), hardline, "}");
  }

  override case(c: M.Case): Doc {
    const head = c.match.length ? concat("case ", join(", ", c.match.map((m) => this.node(m))), ":") : "default:";
    const only = c.body.length === 1 ? c.body[0]! : undefined;
    if (only?.k === "expr-stmt") return concat(head, " ", this.node((only as M.ExprStmt).expr));
    return concat(head, indent(concat(hardline, this.items(c.body))));
  }

  override map(m: M.MapLit): Doc {
    if (m.entries.length === 0) return "[:]";
    return this.bracketed("[", m.entries.map((e) => this.node(e)), "]", m.layout);
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "swift.array":
        return concat("[", this.node(n["elem"] as M.TypeRef), "]");
      case "swift.dict":
        return concat("[", this.node(n["key"] as M.TypeRef), ": ", this.node(n["value"] as M.TypeRef), "]");
      case "swift.tuple-type": {
        const fields = n["fields"] as readonly (readonly [string, M.TypeRef])[];
        return concat("(", join(", ", fields.map(([l, t]) => concat(l, ": ", this.node(t)))), ")");
      }
      case "swift.optional": {
        const t = n["type"] as M.TypeRef;
        return t.k === "swift.any" ? concat("(", this.node(t), ")?") : concat(this.node(t), "?");
      }
      case "swift.any":
        return concat("any ", this.node(n["type"] as M.TypeRef));
      case "swift.dot":
        return concat(".", n["name"] as string);
      case "swift.computed": {
        const c = n as unknown as Computed;
        const head = concat(...c.mods.map((m) => concat(m, " ")), "var ", c.name, ": ", this.node(c.type), " {");
        return this.decorated(c, this.body(head, c.body, "}"));
      }
      case "swift.guard-let": {
        const body = n["otherwise"] as readonly M.Stmt[];
        return concat("guard let ", n["name"] as string, " = ", this.node(n["value"] as M.Expr), " else { ", join("; ", body.map((s) => this.node(s))), " }");
      }
      default:
        return super.ext(n);
    }
  }
}
