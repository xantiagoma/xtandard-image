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

const openapiSource =
  COMMENT_HEADER + astToString(await openapiTS(await Bun.file(specURL).text()));

// ---- options.ts ------------------------------------------------------------

const scalar: Record<string, string> = {
  string: "string",
  integer: "number",
  number: "number",
  boolean: "boolean",
  null: "null",
};

function typeOf(s: Schema): string {
  if (s.anyOf) return union(s.anyOf.map(typeOf));
  if (s.enum) return union(s.enum.map((v) => (v === null ? "null" : JSON.stringify(v))));
  const types = s.type === undefined ? [] : Array.isArray(s.type) ? s.type : [s.type];
  if (types.length === 0) return "unknown";
  return union(types.map((t) => scalar[t] ?? "unknown"));
}

function union(parts: string[]): string {
  return [...new Set(parts)].join(" | ");
}

// Words a tuple label cannot be (Bun's transpiler rejects them even where
// tsc accepts them).
const reserved = new Set(
  ("break case catch class const continue debugger default delete do else enum export extends false " +
    "finally for function if import in instanceof new null return super switch this throw true try " +
    "typeof var void while with").split(" "),
);

function label(s: Schema, used: Map<string, number>, fallback: string, rest = false): string {
  const titles = s.anyOf?.map((a) => a.title);
  let name = s.title ?? (titles && titles.length <= 2 && titles.every(Boolean) ? titles.join("_or_") : fallback);
  if (rest) name = /(s|x|ch|sh)$/.test(name) ? `${name}es` : `${name}s`;
  name = name.replace(/[^A-Za-z0-9_$]/g, "_");
  if (/^[0-9]/.test(name) || reserved.has(name)) name = `${name}_`;
  const n = used.get(name) ?? 0;
  used.set(name, n + 1);
  return n === 0 ? name : `${name}_${n + 1}`;
}

// tuple renders one argument form as a labelled TypeScript tuple.
function tuple(f: Schema): string {
  const prefix = f.prefixItems ?? [];
  const min = f.minItems ?? 0;
  const max = f.items ? Number.POSITIVE_INFINITY : (f.maxItems ?? prefix.length);
  const used = new Map<string, number>();
  const elems: string[] = [];
  for (let i = 0; i < Math.max(prefix.length, f.items ? min : 0); i++) {
    const s = prefix[i] ?? f.items;
    if (!s || i >= max) break;
    const opt = i >= min ? "?" : "";
    elems.push(`${label(s, used, "arg")}${opt}: ${typeOf(s)}`);
  }
  if (f.items) {
    elems.push(`...${label(f.items, used, "arg", true)}: Array<${typeOf(f.items)}>`);
  }
  return `[${elems.join(", ")}]`;
}

function argsType(s: Schema): string {
  const forms = s.anyOf ?? [s];
  return forms.map(tuple).join("\n    | ");
}

function doc(name: string, s: Schema, indent: string): string {
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
  const body = lines.map((l) => (l ? `${indent} * ${l.replaceAll("*/", "*\\/")}` : `${indent} *`));
  return `${indent}/**\n${body.join("\n")}\n${indent} */`;
}

function catalogue(kind: "Processing" | "Info" | "Video", schema: Schema): string {
  const props = schema.properties ?? {};
  const names = Object.keys(props).sort();
  const out: string[] = [];
  out.push(`/**`);
  out.push(` * Argument tuples of every imgproxy ${kind.toLowerCase()} option, by canonical name.`);
  out.push(` * \`/rs:fill:300:400/\` is \`{ resize: ["fill", 300, 400] }\`: \`null\` is an empty`);
  out.push(` * argument, booleans are written \`1\`/\`0\`.`);
  out.push(` */`);
  out.push(`export interface Imgproxy${kind}Args {`);
  for (const n of names) {
    const s = props[n]!;
    out.push(doc(n, s, "  "));
    out.push(`  ${JSON.stringify(n)}:\n    | ${argsType(s)};`);
  }
  out.push(`}`);
  out.push("");
  out.push(`/** Alias → canonical name of every imgproxy ${kind.toLowerCase()} option. */`);
  out.push(`export const imgproxy${kind}Aliases = {`);
  const aliasPairs: [string, string][] = [];
  for (const n of names) for (const a of props[n]!["x-xi-aliases"] ?? []) aliasPairs.push([a, n]);
  aliasPairs.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  for (const [a, n] of aliasPairs) out.push(`  ${JSON.stringify(a)}: ${JSON.stringify(n)},`);
  out.push(`} as const;`);
  out.push("");
  out.push(`/** Canonical names of every imgproxy ${kind.toLowerCase()} option. */`);
  out.push(`export const imgproxy${kind}OptionNames = [`);
  for (const n of names) out.push(`  ${JSON.stringify(n)},`);
  out.push(`] as const;`);
  out.push("");
  return out.join("\n");
}

const schemas = spec.components.schemas;
const optionsSource = [
  "// Code generated by packages/image-url/scripts/generate.ts from",
  "// internal/apidoc/openapi.json (ImgproxyProcessingOptions, ImgproxyInfoOptions, ImgproxyVideoOptions).",
  "// DO NOT EDIT. Run `bun run generate`.",
  "",
  catalogue("Processing", schemas["ImgproxyProcessingOptions"]!),
  catalogue("Info", schemas["ImgproxyInfoOptions"]!),
  catalogue("Video", schemas["ImgproxyVideoOptions"]!),
].join("\n");

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
