// PHP emitter (clients/php, Packagist xtandard/image, namespace Xtandard\Image):
// writes clients/php/generated/ from the spec. composer.json maps the
// namespace to both src/ (hand-written) and generated/ (this emitter).
//
//   OptionTypes.php      PHPStan/Psalm type aliases: one argument-list type and
//                        one value type per option, the options array shapes
//                        (every name and alias a key), query parameter shapes
//   OptionNames.php      the name tables (name or alias to canonical,
//                        canonical to short)
//   *Methods.php         traits with the fluent builders' per-option methods
//   Constants.php        signing, encryption, URL, stream and query constants
//   <Enum>.php           backed enums: ImageFormat, VideoContainer,
//                        StreamProtocol, SegmentFormat, SourceScheme,
//                        SourceEncoding, ErrorKind
//   ResponseTypes.php    array shapes of the JSON bodies (InfoResponse, ErrorResponse)
//
// This file is the refiner (WP-146): it lowers the neutral model (build.ts)
// into PHP nodes (model.ts plus the php.* nodes of lang/php.ts), which
// lang/php.ts prints.

import {
  catalogues,
  type CatalogueModel,
  defaultSegmentFormat,
  enums,
  imaginaryParams,
  nativeVideoKeys,
  type OptionModel,
  queryKeyNames,
  refName,
  responseSchemas,
  schemaTypes,
} from "../build.ts";
import { camel, caseName, P, PhpPrinter, type PhpTag } from "../lang/php.ts";
import { pascal, type Shape } from "../lib.ts";
import { type Expr, N, type Node, type Stmt, type TypeRef } from "../model.ts";
import type { CatalogueKind, Emitter, JSONSchema, Spec, SpecArg } from "../spec.ts";

const dir = "clients/php/generated/";
const printer = new PhpPrinter();

/** Builder methods the per-option methods must not shadow (PHP method names are case-insensitive). */
export const reservedMethods = new Set(
  ["option", "options", "pipeline", "extension", "encoding", "withPdfPassword", "build", "param", "__toString"].map((m) =>
    m.toLowerCase(),
  ),
);

// ---- types ----------------------------------------------------------------------------

const t = (name: string, ...args: TypeRef[]) => N.tref(name, args);
const lit = (v: string | number) => N.tlit(N.scalar(v));
const nul = t("null");

/** The members of a type, unions spliced in. */
const atoms = (x: TypeRef): TypeRef[] => (x.k === "tunion" ? (x as { types: readonly TypeRef[] }).types.flatMap(atoms) : [x]);

/** The union of the distinct members of some types (nested unions spliced), or undefined for none. */
function union(types: readonly TypeRef[]): TypeRef | undefined {
  const seen = new Set<string>();
  const out: TypeRef[] = [];
  for (const a of types.flatMap(atoms)) {
    const key = JSON.stringify(a);
    if (!seen.has(key)) {
      seen.add(key);
      out.push(a);
    }
  }
  return out.length === 0 ? undefined : out.length === 1 ? out[0]! : N.tunion(out);
}
const oneOf = (types: readonly TypeRef[]) => union(types) ?? t("mixed");

/** The PHPDoc type of one argument. */
function argType(a: SpecArg): TypeRef {
  let x: TypeRef[];
  switch (a.type) {
    case "integer":
      x = [t("int")];
      break;
    case "number":
      x = [t("int"), t("float")];
      break;
    case "boolean":
      x = [t("bool")];
      break;
    case "enum":
      x = (a.enum ?? []).map(lit);
      break;
    case "direction":
      x = [...["down", "up", "left", "right"].map(lit), t("int"), t("float")];
      break;
    default:
      x = [t("string")];
  }
  if (a.nullable) x.push(nul);
  return x.length === 1 ? x[0]! : N.tunion(x);
}

/** The native PHP types of one argument (for the variadic per-option methods). */
function nativeParts(a: SpecArg): string[] {
  switch (a.type) {
    case "integer":
      return ["int"];
    case "number":
      return ["int", "float"];
    case "boolean":
      return ["bool"];
    case "direction":
      return ["string", "int", "float"];
    default:
      return ["string"];
  }
}

const nativeOrder = ["string", "int", "float", "bool"];

function shapeType(s: Shape): TypeRef {
  if (s.rest) return t("non-empty-list", oneOf([...s.fixed.map(argType), ...s.rest.map(argType)]));
  return P.shape(s.fixed.map((a) => ({ optional: false, type: argType(a) })));
}

/** An array shape of optional string keys. */
const keyed = (keys: readonly (readonly [string, TypeRef])[]) =>
  P.shape(keys.map(([k, type]) => ({ key: N.str(k), optional: true, type })));

const kindPrefix: Record<CatalogueKind, string> = { processing: "Processing", info: "Info", video: "Video" };
const argsAlias = (o: OptionModel) => `${kindPrefix[o.kind]}${pascal(o.name)}Args`;
const valueAlias = (o: OptionModel) => `${kindPrefix[o.kind]}${pascal(o.name)}Value`;
const alias = (name: string, type: TypeRef) => P.tag("phpstan-type", type, { name });

function optionDoc(o: OptionModel): string[] {
  return [o.spec.doc, "", `URL names: ${o.names.join(", ")}; arguments ${o.spec.syntax}; default ${o.spec.default}.`, `${o.tags.join("; ")}.`];
}

// ---- files -------------------------------------------------------------------------------

/** A file of the namespace: the open tag, the notice, strict types, the namespace, then the declarations. */
const file = (what: string, decls: readonly Node[]) =>
  N.file([
    P.open(),
    N.blank(),
    N.line("Code generated by clients/codegen/emitters/php.ts from clients/spec/spec.json.\nDO NOT EDIT. Run `task clients:generate`."),
    N.blank(),
    P.declare("strict_types", N.int(1)),
    N.blank(),
    N.namespace("Xtandard\\Image", []),
    N.blank(),
    N.line(what),
    N.blank(),
    ...decls,
  ]);

/** private function __construct() {} (a class of constants or types only). */
const noConstructor = () => N.func("__construct", [], [], { mods: ["private"] });

const finalClass = (name: string, members: readonly Node[]) => N.type("class", name, members, { mods: ["final"] });

const ret = (e: Expr): Stmt => N.ret(e);

function emitOptionTypes(spec: Spec): Node {
  const doc: (string | PhpTag)[] = [
    "Type aliases (PHPStan and Psalm) of the processing, /info and /video option catalogues.",
    "",
    "Import them where you build URLs (phpstan-import-type ProcessingOptions from \\Xtandard\\Image\\OptionTypes).",
    "",
    "An options array maps option names or aliases, in URL order, to the argument list",
    "or, when the rest are optional, the first argument alone:",
    "['rs' => ['fill', 300, 400], 'q' => 80]. null inside a list is an empty argument;",
    "a null value skips the option.",
    "",
  ];
  for (const c of catalogues(spec)) {
    for (const o of c.options) {
      const variants = o.shapes.map(shapeType);
      const distinct = [...new Map(variants.map((v) => [JSON.stringify(v), v])).values()];
      doc.push(alias(argsAlias(o), distinct.length === 1 ? distinct[0]! : N.tunion(distinct)));
      const bare = union(o.bare.map((a) => argType({ ...a, nullable: false })));
      doc.push(alias(valueAlias(o), bare ? N.tunion([t(argsAlias(o)), bare]) : t(argsAlias(o))));
    }
    doc.push(alias(`${kindPrefix[c.kind]}Options`, keyed(c.names.map(([n, canonical]) => [n, N.tunion([t(valueAlias(c.byName.get(canonical)!)), nul])]))));
    doc.push(alias(`${kindPrefix[c.kind]}OptionName`, N.tunion(c.names.map(([n]) => lit(n)))));
  }
  const queryValue = N.tunion([t("QueryValue"), nul]);
  doc.push(alias("QueryValue", N.tunion([t("string"), t("int"), t("float"), t("bool")])));
  doc.push(alias("QueryImageParams", keyed(queryKeyNames(spec).map((n) => [n, queryValue]))));
  doc.push(alias("ImaginaryParams", keyed(imaginaryParams(spec).map((p) => [p, queryValue]))));
  doc.push(alias("ImaginaryEndpoint", N.tunion(enums(spec).imaginary_endpoint.values.map(lit))));
  return file("Type aliases of the option catalogues and the query parameters.", [P.docblock(doc), finalClass("OptionTypes", [noConstructor()])]);
}

const stringTable = (entries: readonly (readonly [string, string])[]) =>
  N.map(entries.map(([k, v]) => N.entry(N.str(k), N.str(v))), { layout: "expanded" });

/** A docblock (prose, then a @var tag) and the public constant it documents. */
const constant = (text: string, type: TypeRef, name: string, value: Expr): Node[] => [
  P.docblock([text, "", P.tag("var", type)]),
  N.const(name, value, { mods: ["public"] }),
];

const mapOf = (k: TypeRef, v: TypeRef) => t("array", k, v);
const strMap = mapOf(t("string"), t("string"));

function emitOptionNames(spec: Spec): Node {
  const members: Node[] = [];
  for (const c of catalogues(spec)) {
    const K = c.kind.toUpperCase();
    members.push(
      ...constant(`${c.kind} option name or alias to canonical name.`, strMap, `${K}_NAMES`, stringTable(c.names)),
      N.blank(),
      ...constant(
        `Canonical ${c.kind} option name to the name builders write by default (the shortest).`,
        strMap,
        `${K}_SHORT`,
        stringTable(c.options.map((o) => [o.name, o.short])),
      ),
      N.blank(),
    );
  }
  members.push(noConstructor());
  return file("Name tables of the option catalogues.", [
    P.docblock(["Option name tables: every name and alias to its canonical name, and canonical names to the name builders write by default."]),
    finalClass("OptionNames", members),
  ]);
}

function emitMethods(c: CatalogueModel): Node {
  const { kind } = c;
  const anyArg = N.tunion([t("string"), t("int"), t("float"), t("bool"), nul]);
  const members: Node[] = [
    N.func("option", [N.param("name", t("string")), N.param("args", anyArg, { mods: ["variadic"] })], undefined, {
      returns: t("static"),
      mods: ["abstract", "public"],
      doc: N.doc("Appends one option occurrence."),
    }),
  ];
  const used = new Map<string, { name: string; canonical: string }>();
  for (const o of c.options) {
    const doc = union(o.args.map(argType)) ?? anyArg;
    const native = nativeOrder.filter((x) => o.args.some((a) => nativeParts(a).includes(x))).map((x) => t(x));
    if (o.args.some((a) => a.nullable)) native.push(nul);
    const nativeType = native.length === 1 ? native[0]! : N.tunion(native);
    for (const n of o.names) {
      const id = camel(n);
      if (!/^[a-z][A-Za-z0-9]*$/.test(id)) throw new Error(`${kind} option ${n} is not a PHP method name (${id})`);
      const key = id.toLowerCase();
      if (reservedMethods.has(key)) throw new Error(`${kind} option ${n} collides with the builder method ${id}`);
      const prev = used.get(key);
      if (prev !== undefined) {
        if (prev.canonical === o.name) continue;
        throw new Error(`${kind} option names ${prev.name} and ${n} are both the PHP method ${id}`);
      }
      used.set(key, { name: n, canonical: o.name });
      members.push(
        N.blank(),
        P.docblock([...optionDoc(o), "", P.tag("param", doc, { name: "args", variadic: true })]),
        N.func(id, [N.param("args", nativeType, { mods: ["variadic"] })], [ret(N.invoke(N.id("this"), "option", [N.str(n), N.spread(N.id("args"))]))], {
          returns: t("static"),
          mods: ["public"],
        }),
      );
    }
  }
  return file(`Per-option methods of the fluent ${kind} URL builders.`, [
    P.docblock([
      `One method per ${kind} option name and alias; each appends that option, written with the name as called.`,
      "Names that differ only in punctuation (min-width, min_width) share one method.",
      "",
      "@internal used by the fluent URL chains",
    ]),
    N.type("trait", `${kindPrefix[kind]}Methods`, members),
  ]);
}

/** A backed string enum, one case per value, then extra members. */
function emitEnum(name: string, values: readonly string[], doc: string, extra: readonly Node[] = []): Node {
  const seen = new Set<string>();
  const cases = values.map((v) => {
    const c = caseName(v);
    if (seen.has(c.toLowerCase())) throw new Error(`enum ${name}: two cases named ${c}`);
    seen.add(c.toLowerCase());
    return N.member(c, N.str(v));
  });
  return file(doc, [N.enum(name, cases, { type: t("string"), body: extra.length ? [N.blank(), ...extra] : [], doc: N.doc(doc) })]);
}

/** A method of an enum that reads Constants::TABLE[$this->value] (and an element of it). */
const lookup = (doc: string, name: string, returns: string, table: string, at?: number) => {
  const row = N.index(N.get(P.cls("Constants"), table, { mods: ["static"] }), N.get(N.id("this"), "value"));
  return N.func(name, [], [ret(at === undefined ? row : N.index(row, N.int(at)))], {
    returns: t(returns),
    mods: ["public"],
    doc: N.doc(doc, { mods: ["inline"] }),
  });
};

const constList = (values: readonly (string | number)[]) => N.list(values.map(N.scalar));
/** list<string> or list<int>, by the first value. */
const listType = (values: readonly (string | number)[]) => t("list", t(typeof values[0] === "string" ? "string" : "int"));

function emitConstants(spec: Spec): Node {
  const s = spec.media.streams;
  const sg = spec.signing;
  const en = spec.encryption;
  const u = spec.url;
  const int = t("int");
  const str = t("string");
  // PHP 8.2 has no typed class constants: the type goes in @var.
  const i = (doc: string, name: string, v: number) => constant(doc, int, name, N.int(v));
  const st = (doc: string, name: string, v: string) => constant(doc, str, name, N.str(v));
  const l = (doc: string, name: string, v: readonly (string | number)[]) => constant(doc, listType(v), name, constList(v));
  const table = (doc: string, type: TypeRef, name: string, entries: readonly (readonly [string, Expr])[]) =>
    constant(doc, type, name, N.map(entries.map(([k, v]) => N.entry(N.str(k), v)), { layout: "expanded" }));
  const members: Node[] = [
    ...i("Version of clients/spec/spec.json this client was generated from.", "SPEC_VERSION", spec.spec_version),
    ...l("Output formats of the processing `format` option.", "IMAGE_FORMATS", spec.media.image_formats),
    ...l("Output containers of /video URLs.", "VIDEO_CONTAINERS", spec.media.video_containers),
    ...table(
      "Stream protocol to its manifest container and media type.",
      mapOf(str, P.shape([str, str].map((type) => ({ optional: false, type })))),
      "STREAM_MANIFESTS",
      s.protocols.map((p) => [p.name, constList([p.container, p.content_type])]),
    ),
    ...table(
      "Segment format (and alias) to the protocols that accept it.",
      mapOf(str, t("list", str)),
      "SEGMENT_FORMAT_PROTOCOLS",
      s.segment_formats.map((f) => [f.name, constList(f.protocols)]),
    ),
    ...st("The segment format the server uses when none is given.", "DEFAULT_SEGMENT_FORMAT", defaultSegmentFormat(spec)),
    ...i("Most renditions of one stream.", "MAX_RENDITIONS", s.max_renditions),
    ...i("Smallest rendition (short side, pixels).", "RENDITION_MIN", s.rendition_min),
    ...i("Largest rendition (short side, pixels).", "RENDITION_MAX", s.rendition_max),
    ...i("Shortest segment duration (seconds).", "SEGMENT_DURATION_MIN", s.segment_duration_min),
    ...i("Longest segment duration (seconds).", "SEGMENT_DURATION_MAX", s.segment_duration_max),
    ...st("Pattern of an xi:// source name.", "XI_NAME_PATTERN", spec.sources.xi_name_pattern),
    ...st("Mount prefix of the query dialect.", "QUERY_MOUNT", spec.query.mount),
    ...table("/q/video keys written under a native name.", strMap, "NATIVE_VIDEO_KEYS", nativeVideoKeys(spec).map(([k, v]) => [k, N.str(v)])),
    ...i("Smallest signature size in bytes.", "MIN_SIGNATURE_SIZE", sg.min_size),
    ...i("Largest signature size in bytes.", "MAX_SIGNATURE_SIZE", sg.max_size),
    ...i("Default signature size in bytes.", "DEFAULT_SIGNATURE_SIZE", sg.default_size),
    ...l("Signature segments of unsigned URLs.", "UNSIGNED_PLACEHOLDERS", sg.unsigned_placeholders),
    ...st("Default signature segment of unsigned URLs.", "DEFAULT_UNSIGNED", sg.default_unsigned),
    ...st("Query parameter of the xi_sig signature.", "XI_SIG_PARAM", sg.query_param),
    ...l("AES key sizes in bytes.", "ENCRYPTION_KEY_SIZES", en.key_sizes),
    ...i("AES-CBC IV size in bytes.", "IV_SIZE", en.iv_size),
    ...st("Source segment prefix of encrypted sources.", "ENCRYPTED_SOURCE_PREFIX", en.source_prefix),
    ...i("Longest PDF password in UTF-8 bytes.", "MAX_PDF_PASSWORD_BYTES", en.max_pdf_password_bytes),
    ...st("Canonical name of the PDF password option.", "PDF_PASSWORD_NAME", en.pdf_password_names[0]!),
    ...st("Short name of the PDF password option.", "PDF_PASSWORD_SHORT", en.pdf_password_short_name),
    ...st("Default argument separator.", "DEFAULT_SEPARATOR", u.default_separator),
    ...l("Separators the imgproxy grammar refuses.", "FORBIDDEN_SEPARATORS", u.forbidden_separators),
    ...l("Separators the query dialect refuses.", "FORBIDDEN_QUERY_SEPARATORS", u.forbidden_query_separators),
    ...st("Separator segment between chained pipelines.", "PIPELINE_SEPARATOR", u.pipeline_separator),
    ...st("Characters a plain source keeps verbatim besides ASCII letters and digits.", "PLAIN_SAFE", u.plain_safe),
    ...st("Characters a query key or value keeps verbatim besides ASCII letters and digits.", "QUERY_SAFE", u.query_safe),
    ...st("Pattern of an output extension.", "EXTENSION_PATTERN", u.extension_pattern),
    ...table("HTTP status of each error kind.", mapOf(str, int), "ERROR_STATUS", spec.error_kinds.map((e) => [e.kind, N.int(e.status)])),
    N.blank(),
    noConstructor(),
  ];
  return file("Constants of the client specification.", [
    P.docblock(["Constants of the client specification: signing, encryption, URL rules, streams, query keys."]),
    finalClass("Constants", members),
  ]);
}

const scalars: Record<string, readonly string[]> = { string: ["string"], integer: ["int"], number: ["int", "float"], boolean: ["bool"], null: ["null"] };

function schemaType(s: JSONSchema): TypeRef {
  if (s.$ref) return t(refName(s.$ref));
  const types = schemaTypes(s);
  if (s.enum) {
    const vals: TypeRef[] = s.enum.filter((v) => v !== null).map((v) => lit(v as string | number));
    if (s.enum.includes(null) || types.includes("null")) vals.push(nul);
    return vals.length === 1 ? vals[0]! : N.tunion(vals);
  }
  const parts: TypeRef[] = types.flatMap((ty): TypeRef[] => {
    if (scalars[ty]) return scalars[ty].map((x) => t(x));
    if (ty === "array") return [t("list", s.items ? schemaType(s.items) : t("mixed"))];
    if (ty === "object") {
      if (s.properties) throw new Error("inline object schemas are not supported");
      return [mapOf(t("string"), typeof s.additionalProperties === "object" ? schemaType(s.additionalProperties) : t("mixed"))];
    }
    return [t("mixed")];
  });
  const isNull = (p: TypeRef) => p.k === "tref" && p.name === "null";
  return oneOf([...parts.filter((p) => !isNull(p)), ...parts.filter(isNull)]);
}

function emitResponseTypes(spec: Spec): Node {
  const doc: (string | PhpTag)[] = [
    "Array shapes (PHPStan and Psalm) of the JSON response bodies, from the OpenAPI components.",
    "Client::info() returns an InfoResponse; HttpException::$body is an ErrorResponse",
    "(phpstan-import-type InfoResponse from \\Xtandard\\Image\\ResponseTypes).",
    "",
  ];
  for (const sm of responseSchemas(spec)) {
    doc.push(alias(sm.name, P.shape(sm.properties.map(([p, ps]) => ({ key: N.str(p), optional: !sm.required.has(p), type: schemaType(ps) })))));
  }
  return file("Array shapes of the JSON response bodies.", [P.docblock(doc), finalClass("ResponseTypes", [noConstructor()])]);
}

export const emitter: Emitter = {
  language: "php",
  emit(spec) {
    const e = enums(spec);
    const segAliases = spec.media.streams.segment_formats.flatMap((f) => f.aliases);
    const [processing, info, video] = catalogues(spec) as [CatalogueModel, CatalogueModel, CatalogueModel];
    const files: Record<string, Node> = {
      "OptionTypes.php": emitOptionTypes(spec),
      "OptionNames.php": emitOptionNames(spec),
      "ProcessingMethods.php": emitMethods(processing),
      "InfoMethods.php": emitMethods(info),
      "VideoMethods.php": emitMethods(video),
      "Constants.php": emitConstants(spec),
      "ResponseTypes.php": emitResponseTypes(spec),
      "ImageFormat.php": emitEnum("ImageFormat", e.image_format.values, "Output formats of the processing format option (the URL extension)."),
      "VideoContainer.php": emitEnum("VideoContainer", e.video_container.values, "Output containers of /video URLs (the URL extension)."),
      "StreamProtocol.php": emitEnum("StreamProtocol", e.stream_protocol.values, "Adaptive streaming protocols: HLS or DASH.", [
        lookup("The manifest container, also the URL extension (m3u8, mpd).", "container", "string", "STREAM_MANIFESTS", 0),
        N.blank(),
        lookup("The manifest's media type.", "contentType", "string", "STREAM_MANIFESTS", 1),
      ]),
      "SegmentFormat.php": emitEnum(
        "SegmentFormat",
        e.segment_format.values,
        `Stream segment containers: fmp4 (CMAF) or ts (MPEG-TS, HLS only). The server also accepts ${segAliases.join(", ")}.`,
      ),
      "SourceScheme.php": emitEnum("SourceScheme", e.source_scheme.values, "Source schemes the server resolves."),
      "SourceEncoding.php": emitEnum("SourceEncoding", e.source_encoding.values, "How a source is written in an imgproxy URL."),
      "ErrorKind.php": emitEnum("ErrorKind", e.error_kind.values, "Error kinds of the server's JSON error envelope.", [
        lookup("The HTTP status the server answers with.", "status", "int", "ERROR_STATUS"),
      ]),
    };
    return Object.fromEntries(Object.entries(files).map(([f, n]) => [`${dir}${f}`, printer.print(n)]));
  },
};
