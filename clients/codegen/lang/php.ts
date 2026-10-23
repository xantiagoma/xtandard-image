// PHP: leaf tokens and the printer (WP-146). PSR-12 layout: a class's and
// a method's opening brace on its own line, four-space indentation,
// `$variables`, `->` and `::` member access, single-quoted strings, and
// docblocks whose PHPStan/Psalm tags carry type nodes (php.docblock,
// php.tag), with array shapes as php.shape nodes.

import { block, concat, type Doc, empty, hardline, join, lines } from "../doc.ts";
import type * as M from "../model.ts";
import { N } from "../model.ts";
import { Printer } from "../printer.ts";
import { pascal, words } from "../lib.ts";

/** camelCase of an option name ("min-width" → "minWidth", "crop_ar" → "cropAr"). */
export function camel(name: string): string {
  const w = words(name);
  return w.map((x, i) => (i === 0 ? x.toLowerCase() : x[0]!.toUpperCase() + x.slice(1).toLowerCase())).join("");
}

/** An enum case name for a value ("fill-down" → "FillDown", "h265" → "H265"). */
export function caseName(v: string): string {
  const c = pascal(v);
  return /^[0-9]/.test(c) ? `V${c}` : c;
}

/** A PHP single-quoted string literal (no interpolation: `$` is literal). */
export const quote = (s: string): string => `'${s.replaceAll("\\", "\\\\").replaceAll("'", "\\'")}'`;

/** Text safe inside a /** … *\/ block. */
export const docText = (s: string): string => s.replaceAll("*/", "*\\/");

/** A docblock tag: @phpstan-type Name T, @param T ...$args, @var T. */
export interface PhpTag {
  readonly k: "php.tag";
  readonly tag: string;
  readonly type: M.TypeRef;
  /** The alias name (@phpstan-type) or the parameter (@param) after or before the type. */
  readonly name?: string;
  /** A variadic @param (...$name). */
  readonly variadic?: boolean;
  readonly [field: string]: unknown;
}

/** A docblock: prose (strings, which may span lines) and tags. */
export interface PhpDocblock {
  readonly k: "php.docblock";
  readonly items: readonly (string | PhpTag)[];
  readonly [field: string]: unknown;
}

/** One key of an array shape ('k'?: T), or a positional element (no key). */
export interface ShapeItem {
  readonly key?: M.Lit;
  readonly optional: boolean;
  readonly type: M.TypeRef;
}

/** An array shape: array{T, U} or array{'k'?: T}. */
export interface PhpShape {
  readonly k: "php.shape";
  readonly items: readonly ShapeItem[];
  readonly [field: string]: unknown;
}

export const P = {
  tag: (tag: string, type: M.TypeRef, o: { name?: string; variadic?: boolean } = {}): PhpTag => N.ext("php.tag", { tag, type, ...o }) as PhpTag,
  docblock: (items: readonly (string | PhpTag)[]): PhpDocblock => N.ext("php.docblock", { items }) as PhpDocblock,
  shape: (items: readonly ShapeItem[]): PhpShape => N.ext("php.shape", { items }) as PhpShape,
  /** The <?php open tag. */
  open: (): M.Ext => N.ext("php.open", {}),
  /** declare(name=value); */
  declare: (name: string, value: M.Expr): M.Ext => N.ext("php.declare", { name, value }),
  /** A class reference in an expression (Constants::X). */
  cls: (name: string): M.TypeExpr => N.typeExpr(N.tref(name)),
};

export class PhpPrinter extends Printer {
  override unit = "    ";
  override semi = ";";
  override staticSep = "::";

  override string(s: string): Doc {
    return quote(s);
  }

  /** Variables are written with their sigil. */
  override node(n: M.Node): Doc {
    if (n.k === "id") return concat("$", (n as M.Id).name);
    return super.node(n);
  }

  override member(m: M.Member): Doc {
    return concat(this.node(m.obj), m.mods.includes("static") ? "::" : "->", m.name);
  }

  override comment(c: M.Comment): Doc {
    if (c.style === "line") return this.marked("//", c.lines);
    if (c.mods.includes("inline")) return concat("/** ", docText(c.lines.join("\n")), " */");
    return this.docblock(c.lines);
  }

  private docblock(items: readonly (string | PhpTag)[]): Doc {
    const body: Doc[] = [];
    for (const item of items) {
      if (typeof item === "string") {
        for (const l of docText(item).split("\n")) body.push(l === "" ? " *" : concat(" * ", l));
      } else body.push(concat(" * ", this.tag(item)));
    }
    return lines(["/**", ...body, " */"]);
  }

  private tag(t: PhpTag): Doc {
    if (t.tag === "param") return concat("@param ", this.node(t.type), " ", t.variadic ? "..." : empty, "$", t.name ?? "");
    return concat("@", t.tag, " ", t.name !== undefined ? concat(t.name, " ") : empty, this.node(t.type));
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "php.open":
        return "<?php";
      case "php.declare":
        return concat("declare(", n["name"] as string, "=", this.node(n["value"] as M.Expr), ");");
      case "php.docblock":
        return this.docblock((n as PhpDocblock).items);
      case "php.shape":
        return concat(
          "array{",
          join(
            ", ",
            (n as PhpShape).items.map((i) =>
              i.key ? concat(this.lit(i.key), i.optional ? "?" : empty, ": ", this.node(i.type)) : this.node(i.type),
            ),
          ),
          "}",
        );
      default:
        return super.ext(n);
    }
  }

  override namespace(n: M.Namespace): Doc {
    if (n.items.length) return this.unsupported(n);
    return concat("namespace ", n.name, ";");
  }

  private head(mods: M.Mods, d: Doc): Doc {
    return concat(...mods.map((m) => concat(m, " ")), d);
  }

  override type(t: M.TypeDecl): Doc {
    const head = this.head(t.mods, concat(t.kind === "trait" ? "trait" : "class", " ", t.name));
    return this.decorated(t, concat(head, hardline, this.body("{", t.members, "}")));
  }

  override enum(e: M.Enum): Doc {
    const head = concat("enum ", e.name, e.type ? concat(": ", this.node(e.type)) : empty);
    return this.decorated(e, concat(head, hardline, this.body("{", [...e.members, ...e.body], "}")));
  }

  override enumMember(m: M.EnumMember): Doc {
    return concat("case ", m.name, m.value ? concat(" = ", this.node(m.value)) : empty, ";");
  }

  override const(c: M.Const): Doc {
    return this.decorated(c, this.head(c.mods, concat("const ", c.name, " = ", c.value ? this.node(c.value) : empty, ";")));
  }

  override func(f: M.Func): Doc {
    const head = this.head(
      f.mods,
      concat("function ", f.name, "(", join(", ", f.params.map((p) => this.param(p))), ")", f.returns ? concat(": ", this.node(f.returns)) : empty),
    );
    if (f.body === undefined) return this.decorated(f, concat(head, ";"));
    if (!Array.isArray(f.body)) return this.unsupported(f);
    return this.decorated(f, concat(head, hardline, block("{", (f.body as readonly M.Stmt[]).map((s) => this.node(s)), "}")));
  }

  override param(p: M.Param): Doc {
    return concat(p.type ? concat(this.node(p.type), " ") : empty, p.mods.includes("variadic") ? "..." : empty, "$", p.name);
  }

  override list(l: M.List): Doc {
    return this.bracketed("[", l.items.map((i) => this.node(i)), "]", l.layout);
  }

  override map(m: M.MapLit): Doc {
    return this.bracketed("[", m.entries.map((e) => this.node(e)), "]", m.layout);
  }

  override entry(e: M.Entry): Doc {
    return concat(this.node(e.key), " => ", this.node(e.value));
  }

  override tunion(u: M.TUnion): Doc {
    return join("|", u.types.map((t) => this.node(t)));
  }
}
