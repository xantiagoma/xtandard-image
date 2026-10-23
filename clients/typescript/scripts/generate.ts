// Generates src/generated/ from the committed OpenAPI document
// (internal/apidoc/openapi.json, ADR 0008/0009):
//
//   openapi.ts  openapi-typescript output: paths, InfoResponse, ErrorResponse, …
//   options.ts  the imgproxy option catalogue: one argument-tuple type per
//               option and form, the alias table, and the option types the
//               URL builder is typed by (ImgproxyProcessingOptions, …).
//
// openapi-typescript renders prefixItems as fixed-length tuples and drops the
// x-xi-* extensions (aliases), so options.ts is emitted here from the same
// schemas: minItems/maxItems become optional trailing elements, `items`
// becomes a rest element and `title` labels each element.
//
//   bun scripts/generate.ts           write the files
//   bun scripts/generate.ts --check   exit 1 if a committed file is stale

import openapiTS, { astToString, COMMENT_HEADER } from "openapi-typescript";
import { label, TS, TypeScriptPrinter } from "../../codegen/lang/typescript.ts";
import { type Comment, N, type Node, type TypeRef, union } from "../../codegen/model.ts";

const here = new URL("../", import.meta.url);
const specURL = new URL("../../internal/apidoc/openapi.json", here);
const outDir = new URL("src/generated/", here);
const check = process.argv.includes("--check");

type Schema = {
  type?: string | string[];
  enum?: unknown[];
  anyOf?: Schema[];
  prefixItems?: Schema[];
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  title?: string;
  description?: string;
  examples?: unknown[];
  properties?: Record<string, Schema>;
  "x-xi-aliases"?: string[];
  "x-xi-milestone"?: string;
  "x-xi-pro"?: boolean;
  "x-xi-scope"?: string;
  "x-xi-syntax"?: string;
  "x-xi-default"?: string;
  "x-xi-security"?: boolean;
};

const spec = (await Bun.file(specURL).json()) as {
  components: { schemas: Record<string, Schema> };
};

// ---- openapi.ts ------------------------------------------------------------

// openapi-typescript builds this file as a TypeScript AST and prints it with
// the compiler's printer (astToString): no template strings here either.
const openapiSource =
  COMMENT_HEADER + astToString(await openapiTS(await Bun.file(specURL).text()));

// ---- options.ts ------------------------------------------------------------
//
// Built as nodes of the clients' code model (clients/codegen/model.ts, WP-146)
// and printed by clients/codegen/lang/typescript.ts.

const scalar: Record<string, TypeRef> = {
  string: N.tref("string"),
  integer: N.tref("number"),
  number: N.tref("number"),
  boolean: N.tref("boolean"),
  null: N.tref("null"),
};
const unknown = N.tref("unknown");

function typeOf(s: Schema): TypeRef {
  if (s.anyOf) return union(s.anyOf.map(typeOf));
  if (s.enum) return union(s.enum.map((v) => (v === null ? N.tref("null") : N.tlit(N.scalar(v as string | number)))));
  const types = s.type === undefined ? [] : Array.isArray(s.type) ? s.type : [s.type];
  if (types.length === 0) return unknown;
  return union(types.map((t) => scalar[t] ?? unknown));
}

function labelOf(s: Schema, used: Map<string, number>, fallback: string, rest = false): string {
  const titles = s.anyOf?.map((a) => a.title);
  let name = s.title ?? (titles && titles.length <= 2 && titles.every(Boolean) ? titles.join("_or_") : fallback);
  if (rest) name = /(s|x|ch|sh)$/.test(name) ? `${name}es` : `${name}s`;
  name = label(name);
  const n = used.get(name) ?? 0;
  used.set(name, n + 1);
  return n === 0 ? name : `${name}_${n + 1}`;
}

// tuple is one argument form as a labelled TypeScript tuple.
function tuple(f: Schema): TypeRef {
  const prefix = f.prefixItems ?? [];
  const min = f.minItems ?? 0;
  const max = f.items ? Number.POSITIVE_INFINITY : (f.maxItems ?? prefix.length);
  const used = new Map<string, number>();
  const elems: TypeRef[] = [];
  for (let i = 0; i < Math.max(prefix.length, f.items ? min : 0); i++) {
    const s = prefix[i] ?? f.items;
    if (!s || i >= max) break;
    elems.push(TS.element(labelOf(s, used, "arg"), typeOf(s), { optional: i >= min }));
  }
  if (f.items) elems.push(TS.element(labelOf(f.items, used, "arg", true), N.tref("Array", [typeOf(f.items)]), { rest: true }));
  return N.ttuple(elems);
}

function optionDoc(name: string, s: Schema): Comment {
  const lines: string[] = [];
  if (s.description) lines.push(s.description);
  const aliases = s["x-xi-aliases"] ?? [];
  lines.push("");
  lines.push(`URL names: ${[name, ...aliases].map((n) => `\`${n}\``).join(", ")}.`);
  const tags: string[] = [];
  if (s["x-xi-milestone"]) tags.push(`milestone ${s["x-xi-milestone"]}`);
  if (s["x-xi-scope"]) tags.push(`scope ${s["x-xi-scope"]}`);
  if (s["x-xi-pro"]) tags.push("imgproxy Pro option (free here)");
  if (s["x-xi-security"]) tags.push("security option (needs XI_SECURITY_ALLOW_SECURITY_OPTIONS)");
  if (tags.length) lines.push(`${tags.join("; ")}.`);
  for (const ex of s.examples ?? []) lines.push(`@example ${JSON.stringify(ex)}`);
  return N.comment("doc", lines);
}

const byCode = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

function catalogue(kind: "Processing" | "Info" | "Video", schema: Schema): Node[] {
  const props = schema.properties ?? {};
  const names = Object.keys(props).sort();
  const what = kind.toLowerCase();
  const aliasPairs: [string, string][] = [];
  for (const n of names) for (const a of props[n]!["x-xi-aliases"] ?? []) aliasPairs.push([a, n]);
  aliasPairs.sort(([a], [b]) => byCode(a, b));
  const exported = { mods: ["export"] };
  return [
    N.type(
      "interface",
      `Imgproxy${kind}Args`,
      names.map((n) => {
        const s = props[n]!;
        return N.field(n, TS.linesUnion((s.anyOf ?? [s]).map(tuple)), { mods: ["quoted"], doc: optionDoc(n, s) });
      }),
      {
        ...exported,
        doc: N.comment("doc", [
          `Argument tuples of every imgproxy ${what} option, by canonical name.`,
          "`/rs:fill:300:400/` is `{ resize: [\"fill\", 300, 400] }`: `null` is an empty",
          "argument, booleans are written `1`/`0`.",
        ]),
      },
    ),
    N.blank(),
    N.const(
      `imgproxy${kind}Aliases`,
      TS.asConst(N.map(aliasPairs.map(([a, n]) => N.entry(N.str(a), N.str(n))), { layout: "expanded" })),
      { ...exported, doc: N.doc(`Alias → canonical name of every imgproxy ${what} option.`) },
    ),
    N.blank(),
    N.const(`imgproxy${kind}OptionNames`, TS.asConst(N.list(names.map(N.str), { layout: "expanded" })), {
      ...exported,
      doc: N.doc(`Canonical names of every imgproxy ${what} option.`),
    }),
  ];
}

const schemas = spec.components.schemas;
const optionsSource = new TypeScriptPrinter().print(
  N.file([
    N.comment("line", [
      "Code generated by clients/typescript/scripts/generate.ts from",
      "internal/apidoc/openapi.json (ImgproxyProcessingOptions, ImgproxyInfoOptions, ImgproxyVideoOptions).",
      "DO NOT EDIT. Run `bun run generate`.",
    ]),
    N.blank(),
    ...catalogue("Processing", schemas["ImgproxyProcessingOptions"]!),
    N.blank(),
    ...catalogue("Info", schemas["ImgproxyInfoOptions"]!),
    N.blank(),
    ...catalogue("Video", schemas["ImgproxyVideoOptions"]!),
  ]),
);

// ---- write or check ----------------------------------------------------------

const files: Record<string, string> = {
  "openapi.ts": openapiSource,
  "options.ts": optionsSource,
};

let stale = 0;
for (const [name, content] of Object.entries(files)) {
  const target = Bun.file(new URL(name, outDir));
  if (check) {
    const current = (await target.exists()) ? await target.text() : "";
    if (current !== content) {
      console.error(`src/generated/${name} is stale: run \`bun run generate\` and commit the result`);
      stale++;
    }
    continue;
  }
  await Bun.write(target, content);
  console.log(`wrote src/generated/${name}`);
}
if (stale > 0) process.exit(1);
