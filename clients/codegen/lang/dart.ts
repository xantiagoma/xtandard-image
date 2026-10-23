// Dart: leaf tokens and the printer (WP-146). Doc comments are `///` lines
// (dartdoc Markdown), strings single-quoted with `$` escaped, declarations
// end in `;`, members are indented two spaces. Constructors are funcs with
// the "constructor" mod; their initializer list is a dart.initializers body.
// Dart's own nodes:
//
//   dart.ignore-for-file  {lints}                `// ignore_for_file: a, b`
//   dart.initializers     {items}                a constructor's ` : this._(…)`
//   dart.const            {expr}                 `const []`
//   dart.for-element      {name, iterable, body} a collection-for element
//   dart.ternary          {cond, then, else}     `c ? a : b`
//   dart.record           {fields: [{name, type}]} a named record type `({int a})`

import { concat, type Doc, empty, hardline, indent, join, lines } from "../doc.ts";
import { pascal } from "../lib.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { Printer } from "../printer.ts";

/** Dart reserved words, which cannot name a method, parameter or record field. */
export const reserved: ReadonlySet<string> = new Set(
  (
    "assert break case catch class const continue default do else enum extends false final finally for if in is new " +
    "null rethrow return super switch this throw true try var void while with await yield"
  ).split(" "),
);

/** lowerCamelCase of a name ("min-width" → "minWidth", "c_obj" → "cObj", "R" → "r"). */
export function camel(name: string): string {
  const p = pascal(name);
  return p.length === 0 ? "value" : p[0]!.toLowerCase() + p.slice(1);
}

/** A Dart identifier for a method, parameter or field: lowerCamelCase, reserved words get a trailing `_`. */
export function ident(name: string): string {
  const id = camel(name);
  return reserved.has(id) ? `${id}_` : id;
}

/** A single-quoted Dart string literal: backslashes, quotes, `$` and newlines escaped. */
export function quote(s: string): string {
  return `'${s.replaceAll("\\", "\\\\").replaceAll("'", "\\'").replaceAll("$", "\\$").replaceAll("\n", "\\n")}'`;
}

/** Escapes Markdown that dartdoc would read as references or HTML, outside `code` spans. */
export function md(text: string): string {
  return text
    .split("`")
    .map((part, i) => (i % 2 === 1 ? part : part.replace(/([[\]<>])/g, (c) => ({ "[": "\\[", "]": "\\]", "<": "&lt;", ">": "&gt;" })[c]!)))
    .join("`");
}

/** A doc comment of dartdoc text: trimmed and Markdown-escaped. */
export const mdDoc = (text: string): M.Comment => N.doc(md(text.trim()));

// Keywords a declaration's mods put before its type, in Dart's order.
const leading = ["static", "factory", "const", "final"];

export class DartPrinter extends Printer {
  override semi = ";";

  override string(s: string): Doc {
    return quote(s);
  }

  override comment(c: M.Comment): Doc {
    return this.marked(c.style === "doc" ? "///" : "//", c.lines);
  }

  override import(i: M.Import): Doc {
    return concat("import ", this.string(i.module), ";");
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "dart.ignore-for-file":
        return concat("// ignore_for_file: ", join(", ", n["lints"] as string[]));
      case "dart.initializers":
        return concat(" : ", join(", ", (n["items"] as M.Expr[]).map((e) => this.node(e))), ";");
      case "dart.const":
        return concat("const ", this.node(n["expr"] as M.Expr));
      case "dart.for-element":
        return concat("for (final ", n["name"] as string, " in ", this.node(n["iterable"] as M.Expr), ") ", this.node(n["body"] as M.Expr));
      case "dart.ternary":
        return concat(this.node(n["cond"] as M.Expr), " ? ", this.node(n["then"] as M.Expr), " : ", this.node(n["else"] as M.Expr));
      case "dart.record": {
        const fields = (n["fields"] as { name: string; type: M.TypeRef }[]).map((f) => concat(this.node(f.type), " ", f.name));
        return concat("({", join(", ", fields), "})");
      }
      default:
        return super.ext(n);
    }
  }

  /** The keywords of mods, each followed by a space. */
  private keywords(mods: M.Mods, order: readonly string[] = leading): Doc {
    return concat(...order.filter((m) => mods.includes(m)).map((m) => concat(m, " ")));
  }

  /** `[mods] Type name[ = value];` */
  private variable(d: M.Const | M.Field): Doc {
    return this.decorated(
      d,
      concat(this.keywords(d.mods), d.type ? concat(this.node(d.type), " ") : empty, d.name, d.value ? concat(" = ", this.node(d.value)) : empty, ";"),
    );
  }

  override const(c: M.Const): Doc {
    return this.variable(c);
  }
  override field(f: M.Field): Doc {
    return this.variable(f);
  }

  override type(t: M.TypeDecl): Doc {
    const head = concat(
      this.keywords(t.mods, ["sealed", "abstract", "final", "base"]),
      t.kind === "mixin" ? "mixin " : "class ",
      t.name,
      this.typeArgs(t.typeParams),
      t.supers.length ? concat(" extends ", this.node(t.supers[0]!)) : empty,
      " {",
    );
    return this.decorated(t, this.body(head, t.members, "}"));
  }

  override enum(e: M.Enum): Doc {
    const members = e.members.map((m, i) => this.decorated(m, concat(this.enumMember(m), i === e.members.length - 1 ? ";" : ",")));
    const body = [...members, ...e.body.map((n) => this.node(n))];
    return this.decorated(e, concat("enum ", e.name, " {", indent(concat(hardline, lines(body))), hardline, "}"));
  }

  override enumMember(m: M.EnumMember): Doc {
    return concat(m.name, m.args.length ? this.args(m.args) : empty);
  }

  /** Parameters: required positional, then [optional positional] or {named}. */
  private params(ps: readonly M.Param[]): Doc {
    const req = ps.filter((p) => !p.mods.includes("optional") && !p.mods.includes("named")).map((p) => this.param(p));
    const opt = ps.filter((p) => p.mods.includes("optional")).map((p) => this.param(p));
    const named = ps.filter((p) => p.mods.includes("named")).map((p) => this.param(p));
    const groups = [...req];
    if (opt.length) groups.push(concat("[", join(", ", opt), "]"));
    if (named.length) groups.push(concat("{", join(", ", named), "}"));
    return concat("(", join(", ", groups), ")");
  }

  override param(p: M.Param): Doc {
    const value = p.value ? concat(" = ", this.node(p.value)) : empty;
    const required = p.mods.includes("required") ? "required " : empty;
    if (p.mods.includes("this")) return concat(required, "this.", p.name, value);
    if (p.mods.includes("super")) return concat("super.", p.name, value);
    return concat(required, p.type ? concat(this.node(p.type), " ") : empty, p.name, value);
  }

  override func(f: M.Func): Doc {
    const head = concat(
      this.keywords(f.mods),
      f.returns ? concat(this.node(f.returns), " ") : empty,
      f.mods.includes("get") ? "get " : f.mods.includes("operator") ? "operator " : empty,
      f.name,
      f.mods.includes("get") ? empty : this.params(f.params),
    );
    let body: Doc;
    if (f.body === undefined) body = ";";
    else if (isStmts(f.body)) body = this.body(" {", f.body, "}");
    else if (f.body.k === "dart.initializers") body = this.node(f.body);
    // An expression body on its own continuation line (four more spaces).
    else if (f.mods.includes("wrap")) body = concat(" =>", indent(indent(concat(hardline, this.node(f.body)))), ";");
    else body = concat(" => ", this.node(f.body), ";");
    return this.decorated(f, concat(head, body));
  }

  override if(s: M.If): Doc {
    // A lone statement stays on the condition's line: if (c) return x;
    if (!s.else && s.then.length === 1) return concat("if (", this.node(s.cond), ") ", this.node(s.then[0]!));
    const head = this.body(concat("if (", this.node(s.cond), ") {"), s.then, "}");
    return s.else ? this.body(concat(head, " else {"), s.else, "}") : head;
  }

  override forEach(s: M.ForEach): Doc {
    return this.body(concat("for (final ", s.name, " in ", this.node(s.iterable), ") {"), s.body, "}");
  }
}
