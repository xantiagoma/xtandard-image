// TypeScript: leaf tokens and the printer (WP-146), for the generated files
// of the TypeScript client (clients/typescript/scripts/generate.ts). JSDoc
// comments come before the declaration (one line when the text is one
// line), interfaces hold `key: type;` properties, and the language's own
// constructs are ext nodes: `ts.as-const` (expr as const), `ts.element` (a
// labelled, optional or rest tuple element) and `ts.lines-union` (a union
// written one member per line, each after a leading "| ").

import { concat, type Doc, empty, hardline, indent, lines } from "../doc.ts";
import type * as M from "../model.ts";
import { N } from "../model.ts";
import { Printer } from "../printer.ts";

/**
 * Words a tuple label cannot be (Bun's transpiler rejects them even where
 * tsc accepts them).
 */
export const reserved: ReadonlySet<string> = new Set(
  (
    "break case catch class const continue debugger default delete do else enum export extends false " +
    "finally for function if import in instanceof new null return super switch this throw true try " +
    "typeof var void while with"
  ).split(" "),
);

/** A tuple label from a name: invalid characters become "_", a leading digit or a reserved word gets a trailing "_". */
export function label(name: string): string {
  const id = name.replace(/[^A-Za-z0-9_$]/g, "_");
  return /^[0-9]/.test(id) || reserved.has(id) ? `${id}_` : id;
}

/** A JSDoc line's text with "*\/" escaped so it cannot close the comment. */
export const jsdocText = (s: string): string => s.replaceAll("*/", "*\\/");

/** Nodes of TypeScript's own constructs. */
export const TS = {
  /** expr as const. */
  asConst: (expr: M.Expr) => N.ext("ts.as-const", { expr }),
  /** One tuple element: label?: type, or ...label: type. */
  element: (lbl: string, type: M.TypeRef, o: { optional?: boolean; rest?: boolean } = {}) =>
    N.ext("ts.element", { label: lbl, type, optional: o.optional ?? false, rest: o.rest ?? false }),
  /** A union written one member per line after a leading "| ". */
  linesUnion: (types: readonly M.TypeRef[]) => N.ext("ts.lines-union", { types }),
};

export class TypeScriptPrinter extends Printer {
  override semi = ";";

  override comment(c: M.Comment): Doc {
    if (c.style !== "doc") return super.comment(c);
    const text = c.lines.map(jsdocText);
    if (text.length === 1) return concat("/** ", text[0]!, " */");
    return concat("/**", hardline, this.marked(" *", text), hardline, " */");
  }

  /** export, when the declaration has the mod. */
  private exported(mods: M.Mods): Doc {
    return mods.includes("export") ? "export " : empty;
  }

  override const(c: M.Const): Doc {
    const decl = concat(this.exported(c.mods), "const ", c.name, c.type ? concat(": ", this.node(c.type)) : empty, c.value ? concat(" = ", this.node(c.value)) : empty, this.semi);
    return this.decorated(c, decl);
  }

  override type(t: M.TypeDecl): Doc {
    if (t.kind !== "interface") return this.unsupported(t);
    return this.decorated(t, this.body(concat(this.exported(t.mods), "interface ", t.name, " {"), t.members, "}"));
  }

  // A property: its key quoted when the field has the "quoted" mod.
  override field(f: M.Field): Doc {
    const key = f.mods.includes("quoted") ? this.string(f.name) : f.name;
    const type = f.type ? (f.type.k === "ts.lines-union" ? concat(":", this.node(f.type)) : concat(": ", this.node(f.type))) : empty;
    return this.decorated(f, concat(key, type, this.semi));
  }

  override ttuple(t: M.TTuple): Doc {
    const items = t.items.map((i) => this.node(i));
    if (t.rest) items.push(concat("...", this.node(t.rest)));
    return this.bracketed("[", items, "]", "auto", { trailing: false });
  }

  override ext(n: M.Ext): Doc {
    switch (n.k) {
      case "ts.as-const":
        return concat(this.node(n["expr"] as M.Expr), " as const");
      case "ts.element": {
        const e = n as M.Ext & { label: string; type: M.TypeRef; optional: boolean; rest: boolean };
        return concat(e.rest ? "..." : empty, e.label, e.optional ? "?" : empty, ": ", this.node(e.type));
      }
      case "ts.lines-union": {
        const types = n["types"] as readonly M.TypeRef[];
        return indent(concat(hardline, lines(types.map((t) => concat("| ", this.node(t))))));
      }
      default:
        return super.ext(n);
    }
  }
}
