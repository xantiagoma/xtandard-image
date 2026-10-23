// Ruby and RBS: leaf tokens and the two printers (WP-146). Ruby files are
// modules of frozen constants and endless one-line methods; RBS files are
// type aliases, records, constant types and method overloads, whose
// continuation lines line up under the first overload.
//
// Language nodes (built by emitters/ruby.ts):
//   rb.regexp     a Regexp literal of a JS/Go pattern, anchored on the whole string
//   rbs.optional  T? (parenthesized when T has a union inside)
//   rbs.record    { key: T, ?:"k" => T, "k" => T } (one field per line)
//   rbs.method    def name: (…) -> T | (…) -> T
//   rbs.overload  (params) -> return

import { bracket, concat, type Doc, empty, hardline, join } from "../doc.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { Printer } from "../printer.ts";

// ---- leaf tokens ----------------------------------------------------------------------

/** A Ruby double-quoted string literal; `#` is escaped so `#{` never interpolates. */
export const rbString = (s: string): string => JSON.stringify(s).replaceAll("#", "\\#");

/** A Ruby symbol literal (:name, or :"…" when it is not a plain name). */
export const rbSymbol = (s: string): string => (/^[a-z_][a-z0-9_]*$/.test(s) ? `:${s}` : `:${rbString(s)}`);

/** An RBS string literal type. */
export const rbsString = (s: string): string => JSON.stringify(s);

/** An RBS symbol literal type (always quoted, so keywords and dashes are fine). */
export const rbsSymbol = (s: string): string => `:${JSON.stringify(s)}`;

/** An RBS record key: `name:` when it is a plain symbol, otherwise `:"min-width" =>`. */
export const recordKey = (s: string): string => (/^[a-z_][a-z0-9_]*$/.test(s) ? `${s}:` : `${rbsSymbol(s)} =>`);

/** A Ruby method name for an option name or alias ("min-width" → "min_width"). */
export function ident(name: string): string {
  let id = name.replace(/[^A-Za-z0-9_]/g, "_");
  if (/^[0-9]/.test(id)) id = `_${id}`;
  return id;
}

/** A JS/Go regular expression as a Ruby Regexp literal anchored on the whole string (^ → \A, $ → \z). */
export function regexp(pattern: string): string {
  let p = pattern;
  if (p.startsWith("^")) p = `\\A${p.slice(1)}`;
  if (p.endsWith("$") && !p.endsWith("\\$")) p = `${p.slice(0, -1)}\\z`;
  return `/${p.replaceAll("/", "\\/")}/`;
}

/** An RBS type name of a schema name ("InfoRGB" → "info_rgb"). */
export const typeName = (schema: string): string =>
  schema
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1_$2")
    .toLowerCase();

// ---- language nodes ---------------------------------------------------------------------

export interface RbsOptional extends M.Ext {
  readonly k: "rbs.optional";
  readonly type: M.TypeRef;
}
export interface RbsField {
  readonly name: string;
  /** symbol: `name:` / `:"k" =>`; string: `"k" =>`. */
  readonly key: "symbol" | "string";
  readonly optional: boolean;
  readonly type: M.TypeRef;
}
export interface RbsRecord extends M.Ext {
  readonly k: "rbs.record";
  readonly fields: readonly RbsField[];
}
export interface RbsOverload extends M.Ext {
  readonly k: "rbs.overload";
  readonly params: readonly M.Param[];
  readonly returns: M.TypeRef;
}
export interface RbsMethod extends M.Ext {
  readonly k: "rbs.method";
  readonly name: string;
  readonly overloads: readonly RbsOverload[];
}

export const R = {
  regexp: (pattern: string) => N.ext("rb.regexp", { pattern }),
  optional: (type: M.TypeRef): RbsOptional => N.ext("rbs.optional", { type }) as RbsOptional,
  record: (fields: readonly RbsField[]): RbsRecord => N.ext("rbs.record", { fields }) as RbsRecord,
  overload: (params: readonly M.Param[], returns: M.TypeRef): RbsOverload => N.ext("rbs.overload", { params, returns }) as RbsOverload,
  method: (name: string, overloads: readonly RbsOverload[]): RbsMethod => N.ext("rbs.method", { name, overloads }) as RbsMethod,
};

/** Whether a type prints with a " | " in it (then `?` and `*` need parentheses around it). */
export function hasUnion(t: M.TypeRef): boolean {
  switch (t.k) {
    case "tunion":
      return true;
    case "tref":
      return (t as M.TRef).args.some(hasUnion);
    case "ttuple":
      return (t as M.TTuple).items.some(hasUnion);
    case "rbs.optional":
      return hasUnion((t as RbsOptional).type);
    default:
      return false;
  }
}

// ---- printers -----------------------------------------------------------------------------

/** What Ruby and RBS share: two-space indentation, # comments, module … end. */
class RubyFamilyPrinter extends Printer {
  override unit = "  ";

  override comment(c: M.Comment): Doc {
    return this.marked("#", c.lines);
  }

  override namespace(n: M.Namespace): Doc {
    return this.decorated(n, this.body(concat("module ", n.name), n.items, "end"));
  }
}

export class RubyPrinter extends RubyFamilyPrinter {
  override string(s: string): Doc {
    return rbString(s);
  }
  override symbol(s: string): Doc {
    return rbSymbol(s);
  }
  override nullToken(): Doc {
    return "nil";
  }

  override const(c: M.Const): Doc {
    if (!c.value) throw new Error(`ruby: constant ${c.name} needs a value`);
    return this.decorated(c, concat(c.name, " = ", this.node(c.value)));
  }

  // An endless method: def name(params) = expr.
  override func(f: M.Func): Doc {
    if (!f.body || isStmts(f.body)) throw new Error(`ruby: def ${f.name} needs an expression body`);
    return this.decorated(f, concat("def ", f.name, "(", join(", ", f.params.map((p) => this.param(p))), ") = ", this.node(f.body)));
  }

  override param(p: M.Param): Doc {
    return concat(p.mods.includes("variadic") ? "*" : empty, p.name);
  }

  // A call without arguments has no parentheses (x.freeze).
  override call(c: M.Call): Doc {
    if (c.args.length === 0) return this.node(c.fn);
    return super.call(c);
  }

  override map(m: M.MapLit): Doc {
    return this.bracketed("{", m.entries.map((e) => this.node(e)), "}", m.layout, { pad: true });
  }
  override entry(e: M.Entry): Doc {
    return concat(this.node(e.key), " => ", this.node(e.value));
  }

  override node(n: M.Node): Doc {
    if (n.k === "spread") return concat("*", this.node((n as M.Spread).expr));
    return super.node(n);
  }

  override ext(n: M.Ext): Doc {
    if (n.k === "rb.regexp") return regexp(n["pattern"] as string);
    return super.ext(n);
  }
}

export class RbsPrinter extends RubyFamilyPrinter {
  override string(s: string): Doc {
    return rbsString(s);
  }
  override symbol(s: string): Doc {
    return rbsSymbol(s);
  }

  /** A type, parenthesized when it has a union inside (before `?` or after `*`). */
  private grouped(t: M.TypeRef): Doc {
    return hasUnion(t) ? concat("(", this.node(t), ")") : this.node(t);
  }

  // module Name : SelfType … end, interface _Name … end.
  override type(t: M.TypeDecl): Doc {
    const keyword = t.kind === "interface" ? "interface " : "module ";
    const self = t.supers.length ? concat(" : ", join(", ", t.supers.map((s) => this.node(s)))) : empty;
    return this.decorated(t, this.body(concat(keyword, t.name, self), t.members, "end"));
  }

  override typeAlias(t: M.TypeAlias): Doc {
    return this.decorated(t, concat("type ", t.name, " = ", this.node(t.type)));
  }

  override const(c: M.Const): Doc {
    if (!c.type) throw new Error(`rbs: constant ${c.name} needs a type`);
    return this.decorated(c, concat(c.name, ": ", this.node(c.type)));
  }

  // A method parameter: Type name, *Type name.
  override param(p: M.Param): Doc {
    if (!p.type) throw new Error(`rbs: parameter ${p.name} needs a type`);
    const type = p.mods.includes("variadic") ? concat("*", this.grouped(p.type)) : this.node(p.type);
    return p.name ? concat(type, " ", p.name) : type;
  }

  override tref(t: M.TRef): Doc {
    if (!t.args.length) return t.name;
    return concat(t.name, "[", join(", ", t.args.map((a) => this.node(a))), "]");
  }

  override ttuple(t: M.TTuple): Doc {
    return concat("[", join(", ", t.items.map((i) => this.node(i))), "]");
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "rbs.optional":
        return concat(this.grouped((n as RbsOptional).type), "?");
      case "rbs.record": {
        const fields = (n as RbsRecord).fields.map((f) =>
          concat(f.optional ? "?" : empty, f.key === "symbol" ? recordKey(f.name) : concat(rbsString(f.name), " =>"), " ", this.node(f.type)),
        );
        return bracket("{", fields, "}", { force: true, trailing: false });
      }
      case "rbs.overload": {
        const o = n as RbsOverload;
        return concat("(", join(", ", o.params.map((p) => this.param(p))), ") -> ", this.node(o.returns));
      }
      case "rbs.method": {
        // Later overloads start with "|" under the space after "def name:".
        const m = n as RbsMethod;
        const under = " ".repeat("def ".length + m.name.length + 1);
        return concat("def ", m.name, ": ", join(concat(hardline, under, "| "), m.overloads.map((o) => this.node(o))));
      }
      default:
        return super.ext(n);
    }
  }
}
