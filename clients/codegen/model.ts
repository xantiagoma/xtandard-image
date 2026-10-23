// The code model of the generated client files (WP-146).
//
//   spec.json ──build.ts──▶ neutral model (catalogues, names, short names,
//                            aliases, argument shapes, enums, constants,
//                            response schemas: data, no syntax)
//             ──emitters/<lang>.ts (the refiner)──▶ that language's files as
//                            the typed nodes below: declarations, statements,
//                            expressions and type references, lowered to the
//                            shape the client wants (TypedDicts, sealed
//                            interfaces, macros, overloads, …)
//             ──lang/<lang>.ts (the printer)──▶ Doc (doc.ts, the playground's
//                            Wadler/Leijen algebra) ──layout──▶ text
//             ──optional official formatter (gofmt for Go)──▶ the file
//
// No source code is built by concatenating strings: a refiner builds nodes,
// a printer maps nodes to Docs, and the only strings are leaf tokens
// (identifiers in their language's casing, keywords, escaped literals,
// punctuation, numbers, and the text of one comment line). The node kinds
// here are the ones most languages share, in the manner of CodeDOM or
// Kiota's code elements. A language may add its own kinds (`<lang>.<kind>`,
// for example `py.typeddict`): its refiner builds them with ext() and its
// printer prints them in ext().
//
// Layout: blank() is an empty line between items; the collections and the
// argument lists take a layout, "auto" (the layout engine breaks them when
// they do not fit the printer's width) or "expanded" (one item per line,
// as tables are written). Everything else is the printer's.

// ---- the nodes ----------------------------------------------------------------

/** Free-form flags a printer reads (public, static, const, readonly, override, …). */
export type Mods = readonly string[];

/** How a bracketed list lays out: by width (auto) or one item per line (expanded). */
export type Layout = "auto" | "expanded";

/** A file: its items (comments, imports, declarations, statements, blank lines) in order. */
export interface File {
  readonly k: "file";
  readonly items: readonly Node[];
}

/** An empty line between items. */
export interface Blank {
  readonly k: "blank";
}

/**
 * A comment: line ("// …", "# …"), doc (the language's documentation
 * comment or docstring) or block ("/* … *\/"). Each element of lines is
 * one line of text, without the comment markers.
 */
export interface Comment {
  readonly k: "comment";
  readonly style: "line" | "doc" | "block";
  readonly lines: readonly string[];
  readonly mods: Mods;
}

/** An import: a module, optionally some of its names, an alias. */
export interface Import {
  readonly k: "import";
  readonly module: string;
  readonly names: readonly string[];
  readonly alias?: string;
  readonly mods: Mods;
}

/** A namespace, package, module or other block of declarations. */
export interface Namespace {
  readonly k: "namespace";
  readonly name: string;
  readonly items: readonly Node[];
  readonly doc?: Comment;
  readonly mods: Mods;
}

/** An annotation or attribute (@Nullable, #[derive(…)], [Obsolete]). */
export interface Annotation {
  readonly k: "annotation";
  readonly name: string;
  readonly args: readonly Expr[];
}

/** A type alias (Python's `X = …`, TypeScript's `type X = …`, Elixir's @type). */
export interface TypeAlias {
  readonly k: "type-alias";
  readonly name: string;
  readonly typeParams: readonly TypeRef[];
  readonly type: TypeRef;
  readonly doc?: Comment;
  readonly mods: Mods;
}

/** A constant or variable declaration (a module constant, a static field). */
export interface Const {
  readonly k: "const";
  readonly name: string;
  readonly type?: TypeRef;
  readonly value?: Expr;
  readonly doc?: Comment;
  readonly mods: Mods;
  readonly annotations: readonly Annotation[];
}

/** An enumeration. */
export interface Enum {
  readonly k: "enum";
  readonly name: string;
  readonly type?: TypeRef;
  readonly members: readonly EnumMember[];
  /** Declarations after the members (methods, fields). */
  readonly body: readonly Node[];
  readonly supers: readonly TypeRef[];
  readonly doc?: Comment;
  readonly mods: Mods;
  readonly annotations: readonly Annotation[];
}

/** One member of an enum: a name and an optional raw value or constructor arguments. */
export interface EnumMember {
  readonly k: "enum-member";
  readonly name: string;
  readonly value?: Expr;
  readonly args: readonly Expr[];
  readonly doc?: Comment;
  readonly annotations: readonly Annotation[];
}

/** A class-like declaration: class, struct, record, interface, trait, protocol, mixin, extension. */
export interface TypeDecl {
  readonly k: "type";
  readonly kind: "class" | "struct" | "record" | "interface" | "trait" | "protocol" | "mixin" | "extension" | "object";
  readonly name: string;
  readonly typeParams: readonly TypeRef[];
  /** A record's components or a primary constructor's parameters. */
  readonly params: readonly Param[];
  readonly supers: readonly TypeRef[];
  readonly members: readonly Node[];
  readonly doc?: Comment;
  readonly mods: Mods;
  readonly annotations: readonly Annotation[];
}

/** A field or property. */
export interface Field {
  readonly k: "field";
  readonly name: string;
  readonly type?: TypeRef;
  readonly value?: Expr;
  readonly doc?: Comment;
  readonly mods: Mods;
  readonly annotations: readonly Annotation[];
}

/** A function, method or constructor. body: statements, or one expression (an expression-bodied member). */
export interface Func {
  readonly k: "func";
  readonly name: string;
  readonly typeParams: readonly TypeRef[];
  readonly params: readonly Param[];
  readonly returns?: TypeRef;
  readonly body?: readonly Stmt[] | Expr;
  /** Go's receiver, Kotlin's extension receiver. */
  readonly receiver?: Param;
  readonly doc?: Comment;
  readonly mods: Mods;
  readonly annotations: readonly Annotation[];
}

/** A parameter. mods: variadic, named, required, self, … */
export interface Param {
  readonly k: "param";
  readonly name: string;
  readonly type?: TypeRef;
  readonly value?: Expr;
  readonly mods: Mods;
  readonly annotations: readonly Annotation[];
}

// Statements.

/** return, with zero or more values. */
export interface Return {
  readonly k: "return";
  readonly values: readonly Expr[];
}

/** An expression as a statement. */
export interface ExprStmt {
  readonly k: "expr-stmt";
  readonly expr: Expr;
}

/** A local declaration or assignment (`x = …`, `let x: T = …`, `a, b := …`). */
export interface Let {
  readonly k: "let";
  readonly names: readonly string[];
  readonly type?: TypeRef;
  readonly value?: Expr;
  readonly mods: Mods;
}

/** if / else. */
export interface If {
  readonly k: "if";
  readonly cond: Expr;
  readonly then: readonly Node[];
  readonly else?: readonly Node[];
}

/** One case of a switch: the values it matches (none: the default case) and its body. */
export interface Case {
  readonly k: "case";
  readonly match: readonly Expr[];
  readonly body: readonly Node[];
}

/** switch / match / case; a switch without a subject switches on booleans. */
export interface Switch {
  readonly k: "switch";
  readonly subject?: Expr;
  readonly cases: readonly Case[];
}

/** A loop over a collection. */
export interface ForEach {
  readonly k: "for-each";
  readonly name: string;
  readonly type?: TypeRef;
  readonly iterable: Expr;
  readonly body: readonly Node[];
}

/** throw / raise / panic. */
export interface Throw {
  readonly k: "throw";
  readonly value: Expr;
}

// Expressions.

/** A literal: string, integer, number, boolean, null, char or symbol. */
export interface Lit {
  readonly k: "lit";
  readonly type: "str" | "int" | "num" | "bool" | "null" | "char" | "sym";
  readonly value: string | number | boolean | null;
}

/** A name: a variable, a type, a function, a package. */
export interface Id {
  readonly k: "id";
  readonly name: string;
}

/** obj.name (static for a type's member: Type::name, Type.name). */
export interface Member {
  readonly k: "member";
  readonly obj: Expr;
  readonly name: string;
  readonly mods: Mods;
}

/** A call. */
export interface Call {
  readonly k: "call";
  readonly fn: Expr;
  readonly args: readonly Expr[];
  readonly typeArgs: readonly TypeRef[];
  readonly layout: Layout;
}

/** new Type(args) (an object creation, a composite literal). */
export interface New {
  readonly k: "new";
  readonly type: TypeRef;
  readonly args: readonly Expr[];
  readonly layout: Layout;
}

/** obj[key]. */
export interface Index {
  readonly k: "index";
  readonly obj: Expr;
  readonly key: Expr;
}

/** A named (keyword) argument or record field: name: value, name=value. */
export interface Named {
  readonly k: "named";
  readonly name: string;
  readonly value: Expr;
}

/** A list, array, tuple or set literal; type: an element or collection type some languages write. */
export interface List {
  readonly k: "list";
  readonly items: readonly Expr[];
  readonly style: "list" | "tuple" | "set" | "array";
  readonly type?: TypeRef;
  readonly layout: Layout;
}

/** A map or dictionary literal. */
export interface MapLit {
  readonly k: "map";
  readonly entries: readonly Entry[];
  readonly type?: TypeRef;
  readonly layout: Layout;
}

/** One entry of a map literal. */
export interface Entry {
  readonly k: "entry";
  readonly key: Expr;
  readonly value: Expr;
}

/** left op right. */
export interface Binary {
  readonly k: "binary";
  readonly op: string;
  readonly left: Expr;
  readonly right: Expr;
}

/** op expr (prefix) or expr op (postfix: `!`, `?`). */
export interface Unary {
  readonly k: "unary";
  readonly op: string;
  readonly expr: Expr;
  readonly postfix: boolean;
}

/** A lambda / closure / anonymous function. */
export interface Lambda {
  readonly k: "lambda";
  readonly params: readonly Param[];
  readonly body: readonly Stmt[] | Expr;
}

/** ...expr, *args, ..rest. */
export interface Spread {
  readonly k: "spread";
  readonly expr: Expr;
}

/** A type used as an expression (a conversion T(x), a generic argument, a class literal). */
export interface TypeExpr {
  readonly k: "type-expr";
  readonly type: TypeRef;
}

// Type references.

/** A named type with optional type arguments: Map<String, String>, list[int]. */
export interface TRef {
  readonly k: "tref";
  readonly name: string;
  readonly args: readonly TypeRef[];
  readonly nullable: boolean;
  /** How the type arguments lay out (a long Literal[…] one per line). */
  readonly layout: Layout;
}

/** A | B. */
export interface TUnion {
  readonly k: "tunion";
  readonly types: readonly TypeRef[];
}

/** A literal type ("a", :a, 1). */
export interface TLit {
  readonly k: "tlit";
  readonly lit: Lit;
}

/** A tuple type, optionally ending in a repeated element. */
export interface TTuple {
  readonly k: "ttuple";
  readonly items: readonly TypeRef[];
  readonly rest?: TypeRef;
}

/** A language's own node kind ("<lang>.<kind>"); its printer prints it in ext(). */
export interface Ext {
  readonly k: `${string}.${string}`;
  readonly [field: string]: unknown;
}

export type TypeRef = TRef | TUnion | TLit | TTuple | Ext;
export type Expr = Lit | Id | Member | Call | New | Index | Named | List | MapLit | Binary | Unary | Lambda | Spread | TypeExpr | Ext;
export type Stmt = Return | ExprStmt | Let | If | Switch | ForEach | Throw | Comment | Blank | Ext;
export type Decl = Import | Namespace | TypeAlias | Const | Enum | TypeDecl | Field | Func | Comment | Blank | Ext;
export type Node = File | Decl | Stmt | Expr | TypeRef | EnumMember | Param | Annotation | Case | Entry;

// ---- constructors ---------------------------------------------------------------

type Opt<T> = { -readonly [K in keyof T]?: T[K] };
/** Drops the fields that are undefined (exactOptionalPropertyTypes: absent, never undefined). */
const clean = <T>(o: { [K in keyof T]: T[K] | undefined }): T =>
  Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined)) as T;

const mods = { mods: [] as Mods };
const annotated = { mods: [] as Mods, annotations: [] as readonly Annotation[] };

/** Node constructors; options are the node's optional fields. */
export const N = {
  file: (items: readonly Node[]): File => ({ k: "file", items }),
  blank: (): Blank => ({ k: "blank" }),
  comment: (style: Comment["style"], lines: readonly string[], o: Opt<Pick<Comment, "mods">> = {}): Comment =>
    clean({ k: "comment", style, lines, ...mods, ...o }),
  /** A doc comment whose text may span lines ("\n"). */
  doc: (text: string, o: Opt<Pick<Comment, "mods">> = {}): Comment => N.comment("doc", text.split("\n"), o),
  /** A line comment whose text may span lines ("\n"). */
  line: (text: string): Comment => N.comment("line", text.split("\n")),
  imp: (module: string, names: readonly string[] = [], o: Opt<Pick<Import, "alias" | "mods">> = {}): Import =>
    clean({ k: "import", module, names, ...mods, ...o }),
  namespace: (name: string, items: readonly Node[], o: Opt<Pick<Namespace, "doc" | "mods">> = {}): Namespace =>
    clean({ k: "namespace", name, items, ...mods, ...o }),
  annotation: (name: string, args: readonly Expr[] = []): Annotation => ({ k: "annotation", name, args }),
  typeAlias: (name: string, type: TypeRef, o: Opt<Pick<TypeAlias, "doc" | "mods" | "typeParams">> = {}): TypeAlias =>
    clean({ k: "type-alias", name, type, typeParams: [], ...mods, ...o }),
  const: (name: string, value: Expr | undefined, o: Opt<Pick<Const, "type" | "doc" | "mods" | "annotations">> = {}): Const =>
    clean({ k: "const", name, value, ...annotated, ...o }),
  enum: (
    name: string,
    members: readonly EnumMember[],
    o: Opt<Pick<Enum, "type" | "body" | "supers" | "doc" | "mods" | "annotations">> = {},
  ): Enum => clean({ k: "enum", name, members, body: [], supers: [], ...annotated, ...o }),
  member: (name: string, value?: Expr, o: Opt<Pick<EnumMember, "args" | "doc" | "annotations">> = {}): EnumMember =>
    clean({ k: "enum-member", name, value, args: [], annotations: [], ...o }),
  type: (
    kind: TypeDecl["kind"],
    name: string,
    members: readonly Node[],
    o: Opt<Pick<TypeDecl, "typeParams" | "params" | "supers" | "doc" | "mods" | "annotations">> = {},
  ): TypeDecl => clean({ k: "type", kind, name, members, typeParams: [], params: [], supers: [], ...annotated, ...o }),
  field: (name: string, type: TypeRef | undefined, o: Opt<Pick<Field, "value" | "doc" | "mods" | "annotations">> = {}): Field =>
    clean({ k: "field", name, type, ...annotated, ...o }),
  func: (
    name: string,
    params: readonly Param[],
    body: readonly Stmt[] | Expr | undefined,
    o: Opt<Pick<Func, "returns" | "receiver" | "typeParams" | "doc" | "mods" | "annotations">> = {},
  ): Func => clean({ k: "func", name, params, body, typeParams: [], ...annotated, ...o }),
  param: (name: string, type?: TypeRef, o: Opt<Pick<Param, "value" | "mods" | "annotations">> = {}): Param =>
    clean({ k: "param", name, type, ...annotated, ...o }),

  ret: (...values: Expr[]): Return => ({ k: "return", values }),
  stmt: (expr: Expr): ExprStmt => ({ k: "expr-stmt", expr }),
  let: (names: string | readonly string[], value?: Expr, o: Opt<Pick<Let, "type" | "mods">> = {}): Let =>
    clean({ k: "let", names: typeof names === "string" ? [names] : names, value, ...mods, ...o }),
  if: (cond: Expr, then: readonly Node[], otherwise?: readonly Node[]): If => clean({ k: "if", cond, then, else: otherwise }),
  switch: (subject: Expr | undefined, cases: readonly Case[]): Switch => clean({ k: "switch", subject, cases }),
  case: (match: readonly Expr[], body: readonly Node[]): Case => ({ k: "case", match, body }),
  forEach: (name: string, iterable: Expr, body: readonly Node[], o: Opt<Pick<ForEach, "type">> = {}): ForEach =>
    clean({ k: "for-each", name, iterable, body, ...o }),
  throw: (value: Expr): Throw => ({ k: "throw", value }),

  str: (value: string): Lit => ({ k: "lit", type: "str", value }),
  int: (value: number): Lit => ({ k: "lit", type: "int", value }),
  num: (value: number): Lit => ({ k: "lit", type: "num", value }),
  bool: (value: boolean): Lit => ({ k: "lit", type: "bool", value }),
  null: (): Lit => ({ k: "lit", type: "null", value: null }),
  char: (value: string): Lit => ({ k: "lit", type: "char", value }),
  sym: (value: string): Lit => ({ k: "lit", type: "sym", value }),
  /** A string or number literal of a JSON value. */
  scalar: (v: string | number): Lit => (typeof v === "string" ? N.str(v) : Number.isInteger(v) ? N.int(v) : N.num(v)),
  id: (name: string): Id => ({ k: "id", name }),
  get: (obj: Expr, name: string, o: Opt<Pick<Member, "mods">> = {}): Member => ({ k: "member", obj, name, ...mods, ...o }),
  call: (fn: Expr, args: readonly Expr[] = [], o: Opt<Pick<Call, "typeArgs" | "layout">> = {}): Call => ({
    k: "call",
    fn,
    args,
    typeArgs: [],
    layout: "auto",
    ...o,
  }),
  /** obj.name(args). */
  invoke: (obj: Expr, name: string, args: readonly Expr[] = [], o: Opt<Pick<Call, "typeArgs" | "layout">> = {}): Call =>
    N.call(N.get(obj, name), args, o),
  new: (type: TypeRef, args: readonly Expr[] = [], o: Opt<Pick<New, "layout">> = {}): New => ({ k: "new", type, args, layout: "auto", ...o }),
  index: (obj: Expr, key: Expr): Index => ({ k: "index", obj, key }),
  named: (name: string, value: Expr): Named => ({ k: "named", name, value }),
  list: (items: readonly Expr[], o: Opt<Pick<List, "style" | "type" | "layout">> = {}): List =>
    clean({ k: "list", items, style: "list", layout: "auto", ...o }),
  tuple: (items: readonly Expr[], o: Opt<Pick<List, "type" | "layout">> = {}): List => N.list(items, { style: "tuple", ...o }),
  map: (entries: readonly Entry[], o: Opt<Pick<MapLit, "type" | "layout">> = {}): MapLit =>
    clean({ k: "map", entries, layout: "auto", ...o }),
  entry: (key: Expr, value: Expr): Entry => ({ k: "entry", key, value }),
  binary: (op: string, left: Expr, right: Expr): Binary => ({ k: "binary", op, left, right }),
  unary: (op: string, expr: Expr, postfix = false): Unary => ({ k: "unary", op, expr, postfix }),
  lambda: (params: readonly Param[], body: readonly Stmt[] | Expr): Lambda => ({ k: "lambda", params, body }),
  spread: (expr: Expr): Spread => ({ k: "spread", expr }),
  typeExpr: (type: TypeRef): TypeExpr => ({ k: "type-expr", type }),

  tref: (name: string, args: readonly TypeRef[] = [], o: Opt<Pick<TRef, "nullable" | "layout">> = {}): TRef => ({
    k: "tref",
    name,
    args,
    nullable: false,
    layout: "auto",
    ...o,
  }),
  tunion: (types: readonly TypeRef[]): TUnion => ({ k: "tunion", types }),
  tlit: (lit: Lit): TLit => ({ k: "tlit", lit }),
  ttuple: (items: readonly TypeRef[], rest?: TypeRef): TTuple => clean({ k: "ttuple", items, rest }),

  /** A language's own node: kind is "<lang>.<kind>". */
  ext: <T extends Record<string, unknown>>(kind: `${string}.${string}`, fields: T): Ext & T => ({ ...fields, k: kind }) as Ext & T,
};

/** Whether a body is a statement list (else one expression). */
export const isStmts = (b: readonly Stmt[] | Expr): b is readonly Stmt[] => Array.isArray(b);

/**
 * A union of the distinct types (compared by key, structurally by default),
 * or the one type, in the order given. A member that is itself a union
 * stays one member (it prints the same); flatten splices its members in.
 */
export function union(
  types: readonly TypeRef[],
  { key = (t: TypeRef) => JSON.stringify(t), flatten = false }: { key?: (t: TypeRef) => string; flatten?: boolean } = {},
): TypeRef {
  const seen = new Set<string>();
  const out: TypeRef[] = [];
  for (const t of flatten ? types.flatMap((t) => (t.k === "tunion" ? (t as TUnion).types : [t])) : types) {
    const id = key(t);
    if (!seen.has(id)) {
      seen.add(id);
      out.push(t);
    }
  }
  return out.length === 1 ? out[0]! : N.tunion(out);
}
