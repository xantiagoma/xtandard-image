// Rust: leaf tokens and the printer (WP-146). Doc comments are `///` lines
// (`//!` for a module's inner doc), attributes `#[…]` lines before the item;
// a function body that is one expression is the block's tail expression,
// a match arm `pattern => expression,`.
//
// The runtime's declarative macros (src/*.rs) get their own nodes:
//   rs.str-enum      crate::str_enum! { pub enum Name (ErrorVariant) { V = "a" | "b", } }
//   rs.option-names  crate::option_names! { pub enum OptionName { V = "name", short "s", aliases [..], } }
//   rs.macro-rules   macro_rules! name { () => { items }; }
// and the types and items with no generic node:
//   rs.ref           &T, &'a T          rs.slice  [T]
//   rs.impl          impl Trait         rs.mod    mod name; (its body in another file)

import { concat, type Doc, empty, hardline, indent, join, lines } from "../doc.ts";
import { pascal } from "../lib.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { Printer } from "../printer.ts";

/** Strict and reserved keywords of Rust 2024. */
export const keywords: ReadonlySet<string> = new Set(
  (
    "as async await break const continue crate dyn else enum extern false fn for gen if impl in let loop match mod " +
    "move mut pub ref return self Self static struct super trait true type unsafe use where while abstract become box " +
    "do final macro override priv try typeof unsized virtual yield"
  ).split(" "),
);

/** A Rust method identifier for an option name or alias ("min-width" → "min_width", "type" → "type_"). */
export function ident(name: string): string {
  let id = name.replace(/[^A-Za-z0-9_]/g, "_").toLowerCase();
  if (/^[0-9]/.test(id)) id = `_${id}`;
  return keywords.has(id) ? `${id}_` : id;
}

/** A field identifier: lower case, a keyword as a raw identifier (r#type). */
export function field(name: string): string {
  const lower = name.toLowerCase();
  return keywords.has(lower) ? `r#${lower}` : lower;
}

/** A Rust enum variant for a value ("fill-down" → FillDown, "h265" → H265, "3d" → V3d). */
export function variant(value: string): string {
  const v = pascal(value);
  if (v === "") throw new Error(`enum value ${JSON.stringify(value)} has no identifier characters`);
  return /^[0-9]/.test(v) ? `V${v}` : v;
}

const escape = (c: string): string => {
  switch (c) {
    case "\\":
      return "\\\\";
    case "\n":
      return "\\n";
    case "\r":
      return "\\r";
    case "\t":
      return "\\t";
    case "\0":
      return "\\0";
  }
  const code = c.codePointAt(0)!;
  return code < 0x20 || code === 0x7f ? `\\u{${code.toString(16)}}` : c;
};

/** A Rust string literal. */
export function rustString(s: string): string {
  return `"${[...s].map((c) => (c === '"' ? '\\"' : escape(c))).join("")}"`;
}

/** A Rust char literal of one character. */
export function rustChar(c: string): string {
  if ([...c].length !== 1) throw new Error(`${JSON.stringify(c)} is not one character`);
  return `'${c === "'" ? "\\'" : escape(c)}'`;
}

/** Escapes Markdown that rustdoc would read as links or HTML, outside code spans. */
export function md(text: string): string {
  return text
    .split("`")
    .map((part, i) => (i % 2 === 1 ? part : part.replace(/([[\]<>])/g, "\\$1")))
    .join("`");
}

/** Inline code of spec text, or the escaped text when it has backticks itself. */
export function code(text: string): string {
  return text.includes("`") ? md(text) : `\`${text}\``;
}

// ---- Rust's own nodes --------------------------------------------------------------

export interface StrEnum extends M.Ext {
  readonly k: "rs.str-enum";
  readonly decl: M.Enum;
  readonly error: string;
}
export interface OptionNames extends M.Ext {
  readonly k: "rs.option-names";
  readonly decl: M.Enum;
}
export interface MacroRules extends M.Ext {
  readonly k: "rs.macro-rules";
  readonly name: string;
  readonly items: readonly M.Node[];
  readonly doc?: M.Comment;
  readonly annotations: readonly M.Annotation[];
}
export interface Ref extends M.Ext {
  readonly k: "rs.ref";
  readonly type: M.TypeRef;
  readonly lifetime?: string;
}
export interface Slice extends M.Ext {
  readonly k: "rs.slice";
  readonly type: M.TypeRef;
}
export interface ImplTrait extends M.Ext {
  readonly k: "rs.impl";
  readonly type: M.TypeRef;
}
export interface ModDecl extends M.Ext {
  readonly k: "rs.mod";
  readonly name: string;
  readonly mods: M.Mods;
  readonly annotations: readonly M.Annotation[];
}

/** Constructors of Rust's own nodes. */
export const R = {
  /** crate::str_enum! around a pub enum whose members' values are their spellings ("a" | "b"). */
  strEnum: (decl: M.Enum, error: string): StrEnum => N.ext("rs.str-enum", { decl, error }) as StrEnum,
  /** crate::option_names! around the OptionName enum (members: value the name, args [short, aliases]). */
  optionNames: (decl: M.Enum): OptionNames => N.ext("rs.option-names", { decl }) as OptionNames,
  macroRules: (name: string, items: readonly M.Node[], o: { doc?: M.Comment; annotations?: readonly M.Annotation[] } = {}): MacroRules =>
    N.ext("rs.macro-rules", { name, items, annotations: [], ...o }) as MacroRules,
  ref: (type: M.TypeRef, lifetime?: string): Ref => N.ext("rs.ref", lifetime === undefined ? { type } : { type, lifetime }) as Ref,
  slice: (type: M.TypeRef): Slice => N.ext("rs.slice", { type }) as Slice,
  impl: (type: M.TypeRef): ImplTrait => N.ext("rs.impl", { type }) as ImplTrait,
  mod: (name: string, mods: M.Mods, annotations: readonly M.Annotation[] = []): ModDecl => N.ext("rs.mod", { name, mods, annotations }) as ModDecl,
  /** A pattern or value of alternatives: a | b | c. */
  alternatives: (xs: readonly M.Expr[]): M.Expr => xs.slice(1).reduce((acc: M.Expr, x) => N.binary("|", acc, x), xs[0]!),
};

// ---- the printer ---------------------------------------------------------------------

export class RustPrinter extends Printer {
  override unit = "    ";
  override semi = ";";
  override staticSep: Doc = "::";

  override string(s: string): Doc {
    return rustString(s);
  }
  override char(c: string): Doc {
    return rustChar(c);
  }

  /** Visibility and qualifiers (pub, pub(crate), const), each followed by a space. */
  private mods(mods: M.Mods): Doc {
    return concat(...mods.map((m) => concat(m, " ")));
  }

  // `///` lines (`//!` with the inner mod); every line trimmed, so spec text never becomes a doc test.
  override comment(c: M.Comment): Doc {
    if (c.style !== "doc") return this.marked("//", c.lines);
    const text = c.lines.join("\n").trim().split("\n").map((l) => l.trim());
    return this.marked(c.mods.includes("inner") ? "//!" : "///", text);
  }

  override annotation(a: M.Annotation): Doc {
    return concat("#[", a.name, a.args.length ? concat("(", join(", ", a.args.map((x) => this.node(x))), ")") : empty, "]");
  }

  override import(i: M.Import): Doc {
    const names = i.names.length ? concat("::{", join(", ", i.names), "}") : empty;
    return concat(this.mods(i.mods), "use ", i.module, names, ";");
  }

  // An inner doc (//!) opens the module's body; an outer one precedes it.
  override namespace(n: M.Namespace): Doc {
    const inner = n.doc?.mods.includes("inner") ? [n.doc] : [];
    const body = this.body(concat(this.mods(n.mods), "mod ", n.name, " {"), [...inner, ...n.items], "}");
    return inner.length ? body : this.decorated(n, body);
  }

  override const(c: M.Const): Doc {
    const decl = concat(this.mods(c.mods), "const ", c.name, c.type ? concat(": ", this.node(c.type)) : empty, " = ", this.node(c.value!), ";");
    return this.decorated(c, decl);
  }

  override enum(e: M.Enum): Doc {
    return this.decorated(e, this.body(concat(this.mods(e.mods), "enum ", e.name, " {"), e.members, "}"));
  }

  override enumMember(m: M.EnumMember): Doc {
    return this.decorated(m, concat(m.name, m.value ? concat(" = ", this.node(m.value)) : empty, ","));
  }

  override type(t: M.TypeDecl): Doc {
    switch (t.kind) {
      case "struct":
        return this.decorated(t, this.body(concat(this.mods(t.mods), "struct ", t.name, " {"), t.members, "}"));
      case "extension":
        return this.decorated(t, this.body(concat("impl ", t.name, " {"), t.members, "}"));
      default:
        return this.unsupported(t);
    }
  }

  override field(f: M.Field): Doc {
    return this.decorated(f, concat(this.mods(f.mods), f.name, ": ", this.node(f.type!), ","));
  }

  override func(f: M.Func): Doc {
    const head = concat(
      this.mods(f.mods),
      "fn ",
      f.name,
      "(",
      join(", ", f.params.map((p) => this.node(p))),
      ")",
      f.returns ? concat(" -> ", this.node(f.returns)) : empty,
    );
    if (f.body === undefined) return this.decorated(f, concat(head, ";"));
    // One expression: the block's tail expression.
    const body = isStmts(f.body) ? this.body(" {", f.body, "}") : concat(" {", indent(concat(hardline, this.node(f.body))), hardline, "}");
    return this.decorated(f, concat(head, body));
  }

  // match subject { pattern | pattern => expression, … }
  override switch(s: M.Switch): Doc {
    return this.body(concat("match ", this.node(s.subject!), " {"), s.cases, "}");
  }
  override case(c: M.Case): Doc {
    const [arm] = c.body;
    if (c.body.length !== 1 || arm?.k !== "expr-stmt") throw new Error("rust: a match arm is one expression");
    const pattern = c.match.length ? join(" | ", c.match.map((m) => this.node(m))) : "_";
    return concat(pattern, " => ", this.node((arm as M.ExprStmt).expr), ",");
  }

  override lambda(l: M.Lambda): Doc {
    if (isStmts(l.body)) return this.unsupported(l);
    return concat("|", join(", ", l.params.map((p) => this.node(p))), "| ", this.node(l.body));
  }

  // Type { field: value, … }
  override new(n: M.New): Doc {
    return concat(this.node(n.type), " ", this.bracketed("{", n.args.map((a) => this.node(a)), "}", n.layout, { pad: true }));
  }

  override list(l: M.List): Doc {
    return this.bracketed(l.style === "tuple" ? "(" : "[", l.items.map((i) => this.node(i)), l.style === "tuple" ? ")" : "]", l.layout);
  }

  override ttuple(t: M.TTuple): Doc {
    if (t.rest) return this.unsupported(t);
    return concat("(", join(", ", t.items.map((i) => this.node(i))), ")");
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "rs.ref": {
        const r = n as Ref;
        return concat("&", r.lifetime ? concat("'", r.lifetime, " ") : empty, this.node(r.type));
      }
      case "rs.slice":
        return concat("[", this.node((n as Slice).type), "]");
      case "rs.impl":
        return concat("impl ", this.node((n as ImplTrait).type));
      case "rs.mod": {
        const m = n as ModDecl;
        return this.decorated(m, concat(this.mods(m.mods), "mod ", m.name, ";"));
      }
      case "rs.str-enum": {
        const { decl, error } = n as StrEnum;
        const head = concat(this.mods(decl.mods), "enum ", decl.name, " (", error, ") {");
        return this.macroCall("crate::str_enum", this.decorated(decl, this.body(head, decl.members, "}")));
      }
      case "rs.option-names": {
        const { decl } = n as OptionNames;
        const members = decl.members.map((m) => {
          const [short, aliases] = m.args;
          return this.decorated(m, concat(m.name, " = ", this.node(m.value!), ", short ", this.node(short!), ", aliases ", this.node(aliases!), ","));
        });
        const e = concat(this.mods(decl.mods), "enum ", decl.name, " {", indent(concat(hardline, lines(members))), hardline, "}");
        return this.macroCall("crate::option_names", this.decorated(decl, e));
      }
      case "rs.macro-rules": {
        const m = n as MacroRules;
        const arm = this.body("() => {", m.items, "};");
        return this.decorated(m, concat("macro_rules! ", m.name, " {", indent(concat(hardline, arm)), hardline, "}"));
      }
      default:
        return super.ext(n);
    }
  }

  /** path! { body } with the body indented. */
  private macroCall(path: string, body: Doc): Doc {
    return concat(path, "! {", indent(concat(hardline, body)), hardline, "}");
  }
}
