// Query dialect URLs (docs/URL-DIALECTS.md §4), mounted at /q/:
//
//   {baseUrl}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
//   {baseUrl}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
//   {baseUrl}{mount}/<imaginary endpoint>?url=<source>&<imaginary param>=…[&xi_sig=…]
//
// xi_sig covers the escaped path after the mount ("/", "/video",
// "/resize") plus the query, so it is appended last (sign.ts signXiQuery).

import { hasControlChar, utf8 } from "./bytes.ts";
import { type EncryptionKey, encryptPdfPassword } from "./encrypt.ts";
import type { operations } from "./generated/openapi.ts";
import {
  type Arg,
  formatArg,
  formatEntry,
  type OptionEntry,
  type OptionNameStyle,
  processingCatalogue,
  videoCatalogue,
} from "./options.ts";
import { type SigningKey, signXiQuery } from "./sign.ts";

/** Settings of query dialect URLs. */
export interface QueryUrlConfig {
  /** Server origin (and any path before the mount). Default "" (root-relative URLs). */
  readonly baseUrl?: string;
  /** The query dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX). Default "/q". */
  readonly mount?: string;
  /** Signs with xi_sig (the imgproxy key and salt). Without it the URL is unsigned. */
  readonly signing?: SigningKey;
  /** Key for `pdfPassword` (XI_ENCRYPTION_KEYS). */
  readonly encryption?: EncryptionKey;
  /** Argument separator inside `o=` (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR). Default ":". */
  readonly separator?: string;
  /** Option names in `o=` and video keys: the shortest alias (default), the canonical name, or as given. */
  readonly optionNames?: OptionNameStyle;
}

/** A native query key value; booleans are written `true`/`false`. */
export type QueryValue = string | number | boolean;

type QueryOf<Op extends keyof operations> = operations[Op] extends { parameters: { query?: infer Q } }
  ? NonNullable<Q>
  : never;

type Params<Q, Drop extends string> = { [K in Exclude<keyof Q, Drop>]?: QueryValue | null };

/**
 * Native keys of `/q/` (getQueryImage in the OpenAPI document): `w`,
 * `fit`, `g`, `f`, `q`, `preset`, `vts`, `pages`, … `url`, `o`, `sign`,
 * `xi_sig` and `pdf_password` are written by the builder.
 */
export type QueryImageParams = Params<QueryOf<"getQueryImage">, "url" | "o" | "sign" | "xi_sig" | "pdf_password" | "pdp">;

/** imaginary endpoints of `/q/<endpoint>` (getImaginaryImage). */
export type ImaginaryEndpoint = operations["getImaginaryImage"]["parameters"]["path"]["endpoint"];

/** imaginary parameters of `/q/<endpoint>` (getImaginaryImage). */
export type ImaginaryParams = Params<QueryOf<"getImaginaryImage">, "url" | "sign" | "xi_sig">;

// Characters kept verbatim in a query value: RFC 3986 unreserved plus
// ":", "/", ",", "@", "!", "$", "'", "(", ")" and "*". "&", "=", "+",
// ";", "#", "?", "%", spaces and non-ASCII are percent-encoded.
const keep = /^[A-Za-z0-9\-._~:/,@!$'()*]$/;

/** Percent-escapes a query value (keeps `:`, `/` and `,` readable). */
export function escapeQueryValue(v: string): string {
  let out = "";
  for (const ch of v) {
    if (keep.test(ch)) {
      out += ch;
      continue;
    }
    for (const b of utf8(ch)) out += `%${b.toString(16).toUpperCase().padStart(2, "0")}`;
  }
  return out;
}

function checkSource(source: string): string {
  if (source === "" || hasControlChar(source)) {
    throw new TypeError("source must be a non-empty string without control characters");
  }
  return source;
}

function nativeValue(key: string, v: QueryValue): string {
  switch (typeof v) {
    case "boolean":
      return v ? "true" : "false";
    case "number":
      if (!Number.isFinite(v)) throw new RangeError(`query key ${key}: ${v} is not a finite number`);
      return String(v);
    case "string":
      return v;
    default:
      throw new TypeError(`query key ${key}: unsupported value type ${typeof v}`);
  }
}

/** Native keys: an object (key order) or [key, value] pairs (keys may repeat). */
export type ParamList = object | readonly (readonly [string, QueryValue])[];

function paramPairs(params: ParamList | undefined): [string, string][] {
  const out: [string, string][] = [];
  const list = Array.isArray(params) ? params : Object.entries(params ?? {});
  for (const [k, v] of list as [string, QueryValue | null | undefined][]) {
    if (v === undefined || v === null) continue;
    out.push([k, nativeValue(k, v)]);
  }
  return out;
}

async function finish(cfg: QueryUrlConfig, path: string, pairs: readonly [string, string][]): Promise<string> {
  const base = (cfg.baseUrl ?? "").replace(/\/+$/, "");
  const mount = (cfg.mount ?? "/q").replace(/\/+$/, "");
  let query = pairs.map(([k, v]) => `${escapeQueryValue(k)}=${escapeQueryValue(v)}`).join("&");
  if (cfg.signing) query = await signXiQuery(path, query, cfg.signing);
  return `${base}${mount}${path}?${query}`;
}

async function passwordPair(cfg: QueryUrlConfig, password: string): Promise<[string, string]> {
  if (!cfg.encryption) throw new Error("pdfPassword needs an encryption key");
  if (!cfg.signing) throw new Error("pdfPassword needs signing: the server refuses pdf_password on unsigned URLs");
  return [cfg.optionNames === "canonical" ? "pdf_password" : "pdp", await encryptPdfPassword(password, cfg.encryption)];
}

function checkSeparator(sep: string): string {
  if (sep.length !== 1 || sep === "/" || sep === "-" || sep === "%" || sep === "&" || sep === "=") {
    throw new TypeError(`invalid argument separator ${JSON.stringify(sep)}`);
  }
  return sep;
}

/** Internal: a native `/q/` image URL; `o=` carries the option pipelines. */
export async function buildQueryImage(
  cfg: QueryUrlConfig,
  pipelines: readonly (readonly OptionEntry[])[],
  source: string,
  params: ParamList | undefined,
  ext: string | undefined,
  pdfPassword: string | undefined,
): Promise<string> {
  const sep = checkSeparator(cfg.separator ?? ":");
  const style = cfg.optionNames ?? "short";
  const pairs: [string, string][] = [["url", checkSource(source)], ...paramPairs(params)];
  const nonEmpty = pipelines.filter((p) => p.length > 0);
  if (nonEmpty.length > 0) {
    pairs.push(["o", nonEmpty.map((p) => p.map((e) => formatEntry(processingCatalogue, e, sep, style)).join("/")).join("/-/")]);
  }
  if (ext !== undefined && ext !== "") pairs.push(["format", checkExtension(ext)]);
  if (pdfPassword !== undefined) pairs.push(await passwordPair(cfg, pdfPassword));
  return finish(cfg, "/", pairs);
}

// Video keys whose query spelling is native (URL-DIALECTS §4: crop is
// w,h[,x,y] and flip h|v|hv), written by their imgproxy alias instead.
const nativeVideoKeys: Readonly<Record<string, string>> = { crop: "c", flip: "fl" };

function videoArg(a: Arg, where: string): string {
  // The server splits a video key's value on "," and ":".
  const s = formatArg(a, ",", where);
  if (s.includes(":")) throw new TypeError(`${where}: argument ${JSON.stringify(s)} contains ":"`);
  return s;
}

/** Internal: a `/q/video` URL; every video option is a key, its arguments joined by ",". */
export async function buildQueryVideo(
  cfg: QueryUrlConfig,
  entries: readonly OptionEntry[],
  source: string,
  ext: string | undefined,
): Promise<string> {
  const style = cfg.optionNames ?? "short";
  const pairs: [string, string][] = [["url", checkSource(source)]];
  for (const [name, args] of entries) {
    const canonical = videoCatalogue.canonical.get(name);
    if (canonical === undefined) throw new TypeError(`unknown video option ${JSON.stringify(name)}`);
    if (args.length === 0) throw new TypeError(`option ${name}: at least one argument is required`);
    let key = style === "given" ? name : style === "canonical" ? canonical : (videoCatalogue.short.get(canonical) ?? canonical);
    key = nativeVideoKeys[key] ?? key;
    pairs.push([key, args.map((a, i) => videoArg(a, `option ${name} argument ${i + 1}`)).join(",")]);
  }
  if (ext !== undefined && ext !== "") pairs.push(["format", checkExtension(ext)]);
  return finish(cfg, "/video", pairs);
}

/** Internal: an imaginary endpoint URL (`/q/resize?url=…&width=300`). */
export function buildImaginary(cfg: QueryUrlConfig, endpoint: string, source: string, params: ParamList | undefined): Promise<string> {
  if (!/^[a-z]+$/.test(endpoint) || endpoint === "video") throw new TypeError(`invalid imaginary endpoint ${JSON.stringify(endpoint)}`);
  return finish(cfg, `/${endpoint}`, [["url", checkSource(source)], ...paramPairs(params)]);
}

function checkExtension(ext: string): string {
  if (!/^[A-Za-z0-9]+$/.test(ext)) throw new TypeError(`invalid extension ${JSON.stringify(ext)}`);
  return ext;
}
