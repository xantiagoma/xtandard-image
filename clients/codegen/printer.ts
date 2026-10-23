// The printer base (WP-146): maps the nodes of model.ts to a Doc (doc.ts)
// and lays it out. A language's printer (lang/<lang>.ts) extends it and
// supplies its syntax: the leaf tokens (string escaping, numbers,
// identifiers, keywords), comments, declarations and statements. The base
// prints the expressions and type references in the common C-like way
// (calls, members, brackets, generic arguments, unions with " | "), so a
// printer overrides only what its language writes differently.
//
// Line breaks come from the layout (groups break when they do not fit the
// width) or from the structure (one declaration or statement per line,
// blank() nodes, "expanded" collections); a printer never writes "\n".

import { block, bracket, concat, type Doc, empty, hardline, indent, join, lines, print } from "./doc.ts";
import type * as M from "./model.ts";
import { isStmts } from "./model.ts";

export class Printer {
  /** The line width the layout breaks groups at; Infinity keeps every group flat. */
  width = Number.POSITIVE_INFINITY;
  /** One level of indentation. */
  unit = "  ";
  /** The statement terminator. */
  semi = "";
  /** A trailing separator in broken argument lists and collections. */
  trailing = true;

  /** Lays out a file (or any node) as text. */
  print(n: M.Node): string {
    return print(this.node(n), { width: this.width, unit: this.unit });
  }

  // ---- leaf tokens ------------------------------------------------------------------

  /** A string literal. */
  string(s: string): Doc {
    return JSON.stringify(s);
  }
  /** A number as JavaScript writes it (the spec's number formatting). */
  number(n: number, _type: "int" | "num"): Doc {
    return String(n);
  }
  bool(b: boolean): Doc {
    return b ? "true" : "false";
  }
  nullToken(): Doc {
    return "null";
  }
  char(c: string): Doc {
    return this.string(c);
  }
  symbol(s: string): Doc {
    return concat(":", s);
  }
  /** An identifier as written (a refiner has already cased and escaped it). */
  ident(name: string): Doc {
    return name;
  }

  // ---- dispatch ----------------------------------------------------------------------

  node(n: M.Node): Doc {
    switch (n.k) {
      case "file":
        return this.file(n as M.File);
      case "blank":
        return empty;
      case "comment":
        return this.comment(n as M.Comment);
      case "import":
        return this.import(n as M.Import);
      case "namespace":
        return this.namespace(n as M.Namespace);
      case "annotation":
        return this.annotation(n as M.Annotation);
      case "type-alias":
        return this.typeAlias(n as M.TypeAlias);
      case "const":
        return this.const(n as M.Const);
      case "enum":
        return this.enum(n as M.Enum);
      case "enum-member":
        return this.enumMember(n as M.EnumMember);
      case "type":
        return this.type(n as M.TypeDecl);
      case "field":
        return this.field(n as M.Field);
      case "func":
        return this.func(n as M.Func);
      case "param":
        return this.param(n as M.Param);
      case "return":
        return this.return(n as M.Return);
      case "expr-stmt":
        return concat(this.node((n as M.ExprStmt).expr), this.semi);
      case "let":
        return this.let(n as M.Let);
      case "if":
        return this.if(n as M.If);
      case "switch":
        return this.switch(n as M.Switch);
      case "case":
        return this.case(n as M.Case);
      case "for-each":
        return this.forEach(n as M.ForEach);
      case "throw":
        return this.throw(n as M.Throw);
      case "lit":
        return this.lit(n as M.Lit);
      case "id":
        return this.ident((n as M.Id).name);
      case "member":
        return this.member(n as M.Member);
      case "call":
        return this.call(n as M.Call);
      case "new":
        return this.new(n as M.New);
      case "index":
        return concat(this.node((n as M.Index).obj), "[", this.node((n as M.Index).key), "]");
      case "named":
        return this.named(n as M.Named);
      case "list":
        return this.list(n as M.List);
      case "map":
        return this.map(n as M.MapLit);
      case "entry":
        return this.entry(n as M.Entry);
      case "binary":
        return this.binary(n as M.Binary);
      case "unary":
        return this.unary(n as M.Unary);
      case "lambda":
        return this.lambda(n as M.Lambda);
      case "spread":
        return concat("...", this.node((n as M.Spread).expr));
      case "type-expr":
        return this.node((n as M.TypeExpr).type);
      case "tref":
        return this.tref(n as M.TRef);
      case "tunion":
        return this.tunion(n as M.TUnion);
      case "tlit":
        return this.lit((n as M.TLit).lit);
      case "ttuple":
        return this.ttuple(n as M.TTuple);
      default:
        if (n.k.includes(".")) return this.ext(n as M.Ext);
        throw new Error(`${this.constructor.name}: unknown node ${n.k}`);
    }
  }

  /** Prints a language's own node kind; a printer that has some overrides it. */
  ext(n: M.Ext): Doc {
    throw new Error(`${this.constructor.name}: no ${n.k} nodes`);
  }

  /** Throws for a node kind the language does not have. */
  protected unsupported(n: M.Node): never {
    throw new Error(`${this.constructor.name}: cannot print ${n.k} nodes`);
  }

  // ---- layout helpers ---------------------------------------------------------------

  /** Items one per line (a blank() is an empty line). */
  items(list: readonly M.Node[]): Doc {
    return lines(list.map((n) => this.node(n)));
  }

  /** A braced body: open, the items indented one per line, close. */
  body(open: Doc, list: readonly M.Node[], close: Doc): Doc {
    return block(open, list.map((n) => this.node(n)), close);
  }

  /** Items between brackets, by layout. */
  bracketed(open: Doc, items: readonly Doc[], close: Doc, layout: M.Layout, opts: { pad?: boolean; trailing?: boolean } = {}): Doc {
    return bracket(open, items, close, { force: layout === "expanded", trailing: opts.trailing ?? this.trailing, pad: opts.pad ?? false });
  }

  /** A parenthesized argument list. */
  args(args: readonly M.Expr[], layout: M.Layout = "auto"): Doc {
    return this.bracketed("(", args.map((a) => this.node(a)), ")", layout);
  }

  /** Type arguments ("<A, B>"), or nothing. */
  typeArgs(args: readonly M.TypeRef[], open = "<", close = ">"): Doc {
    return args.length ? concat(open, join(", ", args.map((a) => this.node(a))), close) : empty;
  }

  /** A comment's lines with a marker before each ("// a", "//" for an empty line). */
  marked(marker: Doc, text: readonly string[]): Doc {
    return lines(text.map((l) => (l === "" ? marker : concat(marker, " ", l))));
  }

  /** A declaration's doc comment and annotations, each on its own line before it. */
  decorated(decl: { readonly doc?: M.Comment; readonly annotations?: readonly M.Annotation[] }, d: Doc): Doc {
    const parts: Doc[] = [];
    if (decl.doc) parts.push(this.node(decl.doc));
    for (const a of decl.annotations ?? []) parts.push(this.node(a));
    parts.push(d);
    return lines(parts);
  }

  // ---- files, comments, declarations (C-like defaults) ---------------------------------

  file(f: M.File): Doc {
    return concat(this.items(f.items), hardline);
  }

  comment(c: M.Comment): Doc {
    switch (c.style) {
      case "line":
        return this.marked("//", c.lines);
      case "doc":
        return concat("/**", hardline, this.marked(" *", c.lines), hardline, " */");
      case "block":
        return concat("/*", hardline, this.marked(" *", c.lines), hardline, " */");
    }
  }

  import(i: M.Import): Doc {
    return this.unsupported(i);
  }
  namespace(n: M.Namespace): Doc {
    return this.unsupported(n);
  }
  annotation(a: M.Annotation): Doc {
    return concat("@", a.name, a.args.length ? this.args(a.args) : empty);
  }
  typeAlias(t: M.TypeAlias): Doc {
    return this.unsupported(t);
  }
  const(c: M.Const): Doc {
    return this.unsupported(c);
  }
  enum(e: M.Enum): Doc {
    return this.unsupported(e);
  }
  enumMember(m: M.EnumMember): Doc {
    return this.unsupported(m);
  }
  type(t: M.TypeDecl): Doc {
    return this.unsupported(t);
  }
  field(f: M.Field): Doc {
    return this.unsupported(f);
  }
  func(f: M.Func): Doc {
    return this.unsupported(f);
  }
  param(p: M.Param): Doc {
    return concat(p.mods.includes("variadic") ? "..." : empty, this.ident(p.name), p.type ? concat(": ", this.node(p.type)) : empty);
  }

  // ---- statements -------------------------------------------------------------------

  return(r: M.Return): Doc {
    return concat("return", r.values.length ? concat(" ", join(", ", r.values.map((v) => this.node(v)))) : empty, this.semi);
  }
  let(l: M.Let): Doc {
    return this.unsupported(l);
  }
  if(s: M.If): Doc {
    const head = this.body(concat("if ", this.node(s.cond), " {"), s.then, "}");
    return s.else ? this.body(concat(head, " else {"), s.else, "}") : head;
  }
  switch(s: M.Switch): Doc {
    return this.body(concat("switch ", s.subject ? concat(this.node(s.subject), " ") : empty, "{"), s.cases, "}");
  }
  case(c: M.Case): Doc {
    const head = c.match.length ? concat("case ", join(", ", c.match.map((m) => this.node(m))), ":") : "default:";
    return concat(head, indent(concat(hardline, this.items(c.body))));
  }
  forEach(s: M.ForEach): Doc {
    return this.unsupported(s);
  }
  throw(t: M.Throw): Doc {
    return concat("throw ", this.node(t.value), this.semi);
  }

  /** A function body: statements in braces, or " => expr". */
  funcBody(body: readonly M.Stmt[] | M.Expr | undefined, open: Doc = " {", close: Doc = "}"): Doc {
    if (body === undefined) return this.semi;
    if (!isStmts(body)) return concat(" => ", this.node(body), this.semi);
    return this.body(open, body, close);
  }

  // ---- expressions ------------------------------------------------------------------

  lit(l: M.Lit): Doc {
    switch (l.type) {
      case "null":
        return this.nullToken();
      case "bool":
        return this.bool(l.value as boolean);
      case "int":
      case "num":
        return this.number(l.value as number, l.type);
      case "char":
        return this.char(l.value as string);
      case "sym":
        return this.symbol(l.value as string);
      case "str":
        return this.string(l.value as string);
    }
  }

  member(m: M.Member): Doc {
    return concat(this.node(m.obj), m.mods.includes("static") ? this.staticSep : ".", this.ident(m.name));
  }
  /** The separator of a type's static member (Rust's and PHP's "::"). */
  staticSep: Doc = ".";

  call(c: M.Call): Doc {
    return concat(this.node(c.fn), this.typeArgs(c.typeArgs), this.args(c.args, c.layout));
  }
  new(n: M.New): Doc {
    return concat("new ", this.node(n.type), this.args(n.args, n.layout));
  }
  named(n: M.Named): Doc {
    return concat(this.ident(n.name), ": ", this.node(n.value));
  }
  list(l: M.List): Doc {
    const [open, close] = l.style === "tuple" ? ["(", ")"] : ["[", "]"];
    return this.bracketed(open, l.items.map((i) => this.node(i)), close, l.layout);
  }
  map(m: M.MapLit): Doc {
    return this.bracketed("{", m.entries.map((e) => this.node(e)), "}", m.layout);
  }
  entry(e: M.Entry): Doc {
    return concat(this.node(e.key), ": ", this.node(e.value));
  }
  binary(b: M.Binary): Doc {
    return concat(this.node(b.left), " ", b.op, " ", this.node(b.right));
  }
  unary(u: M.Unary): Doc {
    return u.postfix ? concat(this.node(u.expr), u.op) : concat(u.op, this.node(u.expr));
  }
  lambda(l: M.Lambda): Doc {
    return concat("(", join(", ", l.params.map((p) => this.node(p))), ") => ", isStmts(l.body) ? this.body("{", l.body, "}") : this.node(l.body));
  }

  // ---- types ------------------------------------------------------------------------

  tref(t: M.TRef): Doc {
    const args = t.args.length ? this.bracketed("<", t.args.map((a) => this.node(a)), ">", t.layout, { trailing: false }) : empty;
    return concat(this.ident(t.name), args, t.nullable ? "?" : empty);
  }
  tunion(u: M.TUnion): Doc {
    return join(" | ", u.types.map((t) => this.node(t)));
  }
  ttuple(t: M.TTuple): Doc {
    const items = t.items.map((i) => this.node(i));
    if (t.rest) items.push(concat("...", this.node(t.rest)));
    return this.bracketed("[", items, "]", "auto", { trailing: false });
  }
}
