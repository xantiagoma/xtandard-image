// Elixir: leaf tokens and the printer (WP-146). Modules are defmodule …
// do … end blocks; documentation and typespecs are module attributes
// (@moduledoc, @doc, @typedoc, @type, @spec), the long ones as ~S heredocs
// and multi-line unions in the layout mix format writes (members under the
// name, eight columns in, each after "| "). Collections never take a
// trailing comma.
//
// Elixir's own nodes (ext kinds "ex.*"):
//   ex.attr        @name value
//   ex.heredoc     ~S"""…""" (no interpolation, no escapes)
//   ex.spec        @spec name(types) :: type
//   ex.def         def name(patterns) [when guard], do: expr  |  … do … end
//   ex.defdelegate defdelegate name(args), to: Module
//   ex.command     a call without parentheses (defstruct [...])
//   ex.capture     &expr;  ex.arg: &1
//   ex.at          @name (a module attribute's value)
//   ex.tlist       [type];  ex.tmap: %{key => value};  ex.tstruct: %__MODULE__{field: type}

import { concat, type Doc, empty, hardline, indent, join, lines } from "../doc.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { Printer } from "../printer.ts";

/** Elixir's reserved words: a function cannot be named after them. */
export const reserved: ReadonlySet<string> = new Set("true false nil when and or not in fn do end catch rescue after else".split(" "));

/** A function name for an option name or alias ("min-width" → "min_width", "3d" → "o_3d", "do" → "do_"). */
export function ident(name: string): string {
  let id = name.replace(/[^A-Za-z0-9_]/g, "_").toLowerCase();
  if (/^[0-9]/.test(id)) id = `o_${id}`;
  return reserved.has(id) ? `${id}_` : id;
}

/** An Elixir string literal: JSON escapes, and "#{" escaped so it never interpolates. */
export function string(s: string): string {
  return JSON.stringify(s).replaceAll("#{", "\\#{");
}

/** An Elixir atom literal: :name, or :"quoted" when it is not a plain name. */
export function atom(s: string): string {
  return /^[a-z_][A-Za-z0-9_]*$/.test(s) ? `:${s}` : `:${string(s)}`;
}

// ---- Elixir's nodes ------------------------------------------------------------------

export interface Attr extends M.Ext {
  readonly name: string;
  readonly value: M.Expr | M.TypeRef;
}
export interface Spec extends M.Ext {
  readonly name: string;
  readonly params: readonly M.TypeRef[];
  readonly returns: M.TypeRef;
}
export interface Def extends M.Ext {
  readonly name: string;
  readonly params: readonly M.Expr[];
  readonly guard?: M.Expr;
  readonly body: readonly M.Stmt[] | M.Expr;
}

const nil = N.tlit(N.null());
const isNil = (t: M.TypeRef) => t.k === "tlit" && (t as M.TLit).lit.type === "null";

/** Elixir's node constructors. */
export const X = {
  attr: (name: string, value: M.Expr | M.TypeRef) => N.ext("ex.attr", { name, value }),
  /** A ~S heredoc of text (may span lines). */
  heredoc: (text: string) => N.ext("ex.heredoc", { lines: text.split("\n") }),
  spec: (name: string, params: readonly M.TypeRef[], returns: M.TypeRef) => N.ext("ex.spec", { name, params, returns }),
  def: (name: string, params: readonly M.Expr[], body: readonly M.Stmt[] | M.Expr, guard?: M.Expr) =>
    N.ext("ex.def", guard ? { name, params, body, guard } : { name, params, body }),
  defdelegate: (name: string, params: readonly string[], to: string) => N.ext("ex.defdelegate", { name, params, to }),
  command: (name: string, args: readonly M.Expr[]) => N.ext("ex.command", { name, args }),
  capture: (expr: M.Expr) => N.ext("ex.capture", { expr }),
  arg: (n: number) => N.ext("ex.arg", { n }),
  at: (name: string) => N.ext("ex.at", { name }),
  tlist: (of: M.TypeRef) => N.ext("ex.tlist", { of }),
  tmap: (key: M.TypeRef, value: M.TypeRef) => N.ext("ex.tmap", { key, value }),
  tstruct: (fields: readonly (readonly [string, M.TypeRef])[]) => N.ext("ex.tstruct", { fields }),
  /** A remote or local type: String.t(), integer(), crop_value(). */
  t: (name: string, ...args: M.TypeRef[]) => N.tref(name, args),
  atom: (s: string) => N.tlit(N.sym(s)),
  nil,
  /**
   * A union with each member once and nil last; nested unions are spliced
   * in (a union type has no grouping in typespecs).
   */
  union: (parts: readonly M.TypeRef[]): M.TypeRef => {
    const flat = (t: M.TypeRef): M.TypeRef[] => (t.k === "tunion" ? (t as M.TUnion).types.flatMap(flat) : [t]);
    const seen = new Set<string>();
    const members: M.TypeRef[] = [];
    for (const t of parts.flatMap(flat)) {
      const key = JSON.stringify(t);
      if (!seen.has(key)) {
        seen.add(key);
        members.push(t);
      }
    }
    const out = [...members.filter((m) => !isNil(m)), ...members.filter(isNil)];
    return out.length === 1 ? out[0]! : N.tunion(out);
  },
  /** A union without nil (the element of a list). */
  nonNil: (t: M.TypeRef): M.TypeRef => (t.k === "tunion" ? X.union((t as M.TUnion).types.filter((m) => !isNil(m))) : t),
};

export class ElixirPrinter extends Printer {
  override trailing = false;

  override string(s: string): Doc {
    return string(s);
  }
  override symbol(s: string): Doc {
    return atom(s);
  }
  override nullToken(): Doc {
    return "nil";
  }

  override comment(c: M.Comment): Doc {
    if (c.style !== "line") throw new Error("elixir: documentation is an @doc attribute (ex.attr)");
    return this.marked("#", c.lines);
  }

  // alias Module
  override import(i: M.Import): Doc {
    return concat("alias ", i.module);
  }

  override namespace(n: M.Namespace): Doc {
    return this.body(concat("defmodule ", n.name, " do"), n.items, "end");
  }

  // @type name :: type, or (mods: expanded) each union member on its own
  // line, eight columns in and after "| ", as mix format writes long specs.
  override typeAlias(t: M.TypeAlias): Doc {
    const head = concat("@type ", t.name, " ::");
    if (!t.mods.includes("expanded")) return concat(head, " ", this.node(t.type));
    const members = t.type.k === "tunion" ? (t.type as M.TUnion).types : [t.type];
    const body = members.map((m, i) => concat(i ? "| " : empty, this.node(m)));
    return concat(head, this.deep(concat(hardline, lines(body))));
  }

  /** Four levels in (a typespec continuation). */
  private deep(d: Doc): Doc {
    return indent(indent(indent(indent(d))));
  }

  override named(n: M.Named): Doc {
    return concat(n.name, ": ", this.node(n.value));
  }

  override list(l: M.List): Doc {
    const [open, close] = l.style === "tuple" ? ["{", "}"] : ["[", "]"];
    return this.bracketed(open, l.items.map((i) => this.node(i)), close, l.layout);
  }

  override map(m: M.MapLit): Doc {
    return this.bracketed("%{", m.entries.map((e) => this.node(e)), "}", m.layout);
  }

  override entry(e: M.Entry): Doc {
    return concat(this.node(e.key), " => ", this.node(e.value));
  }

  // %Struct{field: value}
  override new(n: M.New): Doc {
    if (n.type.k !== "tref") throw new Error("elixir: a struct literal names its module");
    return concat("%", (n.type as M.TRef).name, this.bracketed("{", n.args.map((a) => this.node(a)), "}", n.layout));
  }

  // Types: name(args) always with parentheses; tuples in braces.
  override tref(t: M.TRef): Doc {
    return concat(t.name, "(", join(", ", t.args.map((a) => this.node(a))), ")");
  }
  override ttuple(t: M.TTuple): Doc {
    if (t.rest) throw new Error("elixir: a tuple type cannot repeat");
    return concat("{", join(", ", t.items.map((i) => this.node(i))), "}");
  }

  private paramList(params: readonly Doc[]): Doc {
    return params.length ? concat("(", join(", ", params), ")") : empty;
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "ex.attr": {
        const a = n as Attr;
        return concat("@", a.name, " ", this.node(a.value));
      }
      case "ex.heredoc": {
        const text = (n["lines"] as string[]).join("\n").trim().split("\n");
        if (text.some((l) => l.includes('"""'))) throw new Error('elixir: a ~S heredoc cannot hold """');
        return concat('~S"""', hardline, lines(text), hardline, '"""');
      }
      case "ex.spec": {
        const s = n as Spec;
        return concat("@spec ", s.name, "(", join(", ", s.params.map((p) => this.node(p))), ") :: ", this.node(s.returns));
      }
      case "ex.def": {
        const d = n as Def;
        const head = concat("def ", d.name, this.paramList(d.params.map((p) => this.node(p))), d.guard ? concat(" when ", this.node(d.guard)) : empty);
        if (!isStmts(d.body)) return concat(head, ", do: ", this.node(d.body));
        return this.body(concat(head, " do"), d.body, "end");
      }
      case "ex.defdelegate":
        return concat("defdelegate ", n["name"] as string, this.paramList((n["params"] as string[]).map((p) => p)), ", to: ", n["to"] as string);
      case "ex.command":
        return concat(n["name"] as string, " ", join(", ", (n["args"] as M.Expr[]).map((a) => this.node(a))));
      case "ex.capture":
        return concat("&", this.node(n["expr"] as M.Expr));
      case "ex.arg":
        return concat("&", String(n["n"]));
      case "ex.at":
        return concat("@", n["name"] as string);
      case "ex.tlist":
        return concat("[", this.node(n["of"] as M.TypeRef), "]");
      case "ex.tmap":
        return concat("%{", this.node(n["key"] as M.TypeRef), " => ", this.node(n["value"] as M.TypeRef), "}");
      case "ex.tstruct": {
        // Fields eight columns in, the brace six (mix format's layout of a struct type).
        const fields = (n["fields"] as [string, M.TypeRef][]).map(([f, t]) => concat(f, ": ", this.node(t)));
        return concat("%__MODULE__{", indent(indent(indent(concat(indent(concat(hardline, join(concat(",", hardline), fields))), hardline, "}")))));
      }
      default:
        return super.ext(n);
    }
  }
}
