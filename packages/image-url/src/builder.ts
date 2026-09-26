// imgproxy-dialect URL builder (docs/URL-DIALECTS.md §3.1):
//
//   {baseUrl}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
//
// Sources are base64url (default), `plain/` (percent-escaped, `@ext`) or
// `enc/` (AES-CBC, see encrypt.ts); base64 and enc sources take `.ext`.

import { base64url, hasControlChar, utf8 } from "./bytes.ts";
import { type EncryptionKey, encryptSource } from "./encrypt.ts";
import {
  type Arg,
  entriesOf,
  formatEntry,
  type ImgproxyInfoArgsOf,
  type ImgproxyInfoOptionName,
  type ImgproxyInfoOptions,
  type ImgproxyProcessingArgsOf,
  type ImgproxyProcessingOptionName,
  type ImgproxyProcessingOptions,
  infoCatalogue,
  type OptionEntry,
  type OptionNameStyle,
  processingCatalogue,
} from "./options.ts";
import { type SigningKey, signImgproxyPath } from "./sign.ts";

/** How the source URL is written. */
export type SourceEncoding = "base64" | "plain" | "encrypted";

/** Settings shared by every URL a builder makes. */
export interface ImgproxyUrlConfig {
  /**
   * Prepended to every path: the server origin and the dialect's mount
   * prefix, if any ("https://img.example.com", "https://cdn.example.com/img").
   * Default "", which yields root-relative paths.
   */
  readonly baseUrl?: string;
  /** Signs URLs with the imgproxy scheme. Without it the signature segment is `unsignedSignature`. */
  readonly signing?: SigningKey;
  /** Signature segment of unsigned URLs: "insecure" (default), "unsafe" or "_". */
  readonly unsignedSignature?: string;
  /** Key for `encrypted` sources. */
  readonly encryption?: EncryptionKey;
  /** Default source encoding. Default "base64". */
  readonly sourceEncoding?: SourceEncoding;
  /**
   * Splits base64 and encrypted sources into "/"-separated segments of this
   * many characters (imgproxy accepts both; the docs' example uses 16).
   * Default: no split.
   */
  readonly splitSource?: number;
  /** Argument separator (XI_IMGPROXY_ARGUMENTS_SEPARATOR). Default ":". */
  readonly separator?: string;
  /** Option names in the URL: the shortest alias (default), the canonical name, or as given. */
  readonly optionNames?: OptionNameStyle;
}

/** One URL, in the plain object form. */
export interface ImgproxyUrlRequest<O = ImgproxyProcessingOptions> {
  /** The source image URL (http(s)://, s3://, local:///, a relative key, …). */
  readonly source: string;
  /** Options; an array chains pipelines (`/-/`). */
  readonly options?: O | readonly O[];
  /** Output format extension (`@webp` / `.webp`), overriding `format`. */
  readonly extension?: string;
  /** Overrides the configured source encoding. */
  readonly sourceEncoding?: SourceEncoding;
}

/** One `/info` URL, in the plain object form. */
export type ImgproxyInfoUrlRequest = Omit<ImgproxyUrlRequest<ImgproxyInfoOptions>, "options" | "extension"> & {
  readonly options?: ImgproxyInfoOptions;
};

// Characters kept verbatim in a plain source: RFC 3986 unreserved, the
// sub-delims, ":" and "/". Everything else, notably "%", "@" (the extension
// separator), "?" and "#", is percent-encoded.
const plainKeep = /[A-Za-z0-9\-._~!$&'()*+,;=:/]/;

/** Percent-escapes a source for a `plain/` URL; the server path-unescapes it. */
export function escapePlainSource(source: string): string {
  let out = "";
  for (const ch of source) {
    if (plainKeep.test(ch)) {
      out += ch;
      continue;
    }
    for (const b of utf8(ch)) out += `%${b.toString(16).toUpperCase().padStart(2, "0")}`;
  }
  return out;
}

function checkExtension(ext: string): string {
  if (!/^[A-Za-z0-9]+$/.test(ext)) throw new TypeError(`invalid extension ${JSON.stringify(ext)}`);
  return ext;
}

function split(token: string, n: number | undefined): string {
  if (!n || n <= 0 || token.length <= n) return token;
  const parts: string[] = [];
  for (let i = 0; i < token.length; i += n) parts.push(token.slice(i, i + n));
  return parts.join("/");
}

async function sourceSegment(
  cfg: ImgproxyUrlConfig,
  source: string,
  encoding: SourceEncoding,
  ext: string | undefined,
): Promise<string> {
  if (source === "" || hasControlChar(source)) {
    throw new TypeError("source must be a non-empty string without control characters");
  }
  const e = ext === undefined || ext === "" ? undefined : checkExtension(ext);
  switch (encoding) {
    case "plain":
      return `plain/${escapePlainSource(source)}${e ? `@${e}` : ""}`;
    case "base64":
      return `${split(base64url(utf8(source)), cfg.splitSource)}${e ? `.${e}` : ""}`;
    case "encrypted": {
      if (!cfg.encryption) throw new Error("encrypted sources need an encryption key");
      const token = await encryptSource(source, cfg.encryption);
      return `enc/${split(token, cfg.splitSource)}${e ? `.${e}` : ""}`;
    }
    default:
      throw new TypeError(`unknown source encoding ${JSON.stringify(encoding satisfies never)}`);
  }
}

function checkSeparator(sep: string): string {
  if (sep.length !== 1 || sep === "/" || sep === "-" || sep === "%") {
    throw new TypeError(`invalid argument separator ${JSON.stringify(sep)}`);
  }
  return sep;
}

/** Joins the base URL, the endpoint, the signature and the path. */
async function finish(cfg: ImgproxyUrlConfig, info: boolean, path: string): Promise<string> {
  const base = (cfg.baseUrl ?? "").replace(/\/+$/, "");
  const signed = cfg.signing
    ? await signImgproxyPath(path, cfg.signing)
    : `/${cfg.unsignedSignature ?? "insecure"}${path}`;
  return `${base}${info ? "/info" : ""}${signed}`;
}

/** Builds a URL from option pipelines (entries) and a source. */
async function build(
  cfg: ImgproxyUrlConfig,
  info: boolean,
  pipelines: readonly (readonly OptionEntry[])[],
  source: string,
  encoding: SourceEncoding | undefined,
  ext: string | undefined,
): Promise<string> {
  const sep = checkSeparator(cfg.separator ?? ":");
  const style = cfg.optionNames ?? "short";
  const cat = info ? infoCatalogue : processingCatalogue;
  // Empty pipelines (an unused chain()) are dropped: the server rejects them.
  const nonEmpty = pipelines.filter((p) => p.length > 0);
  if (info && nonEmpty.length > 1) throw new TypeError("/info URLs have no chained pipelines");
  const segments: string[] = [];
  nonEmpty.forEach((p, i) => {
    if (i > 0) segments.push("-");
    for (const e of p) segments.push(formatEntry(cat, e, sep, style));
  });
  segments.push(await sourceSegment(cfg, source, encoding ?? cfg.sourceEncoding ?? "base64", ext));
  return finish(cfg, info, `/${segments.join("/")}`);
}

function pipelinesOf(options: object | readonly object[] | undefined): OptionEntry[][] {
  if (options === undefined) return [[]];
  const list = Array.isArray(options) ? (options as readonly object[]) : [options as object];
  return list.length === 0 ? [[]] : list.map(entriesOf);
}

/** Builds a processing URL from the plain object form. */
export function buildImgproxyUrl(request: ImgproxyUrlRequest, config: ImgproxyUrlConfig = {}): Promise<string> {
  return build(
    config,
    false,
    pipelinesOf(request.options),
    request.source,
    request.sourceEncoding,
    request.extension,
  );
}

/** Builds an `/info` URL from the plain object form. */
export function buildImgproxyInfoUrl(request: ImgproxyInfoUrlRequest, config: ImgproxyUrlConfig = {}): Promise<string> {
  return build(config, true, pipelinesOf(request.options), request.source, request.sourceEncoding, undefined);
}

// ---- fluent form -------------------------------------------------------------

type ProcessingMethods = {
  readonly [K in ImgproxyProcessingOptionName]: (...args: ImgproxyProcessingArgsOf<K>) => ImgproxyUrlChain;
};

type InfoMethods = {
  readonly [K in ImgproxyInfoOptionName]: (...args: ImgproxyInfoArgsOf<K>) => ImgproxyInfoUrlChain;
};

interface ChainControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>> {
  /** Appends one option occurrence (same as the per-option method). */
  option<K extends Name>(name: K, ...args: ArgsOf[K]): Self;
  /** Appends every option of an options object, in key order. */
  options(options: Options): Self;
  /** Overrides the source encoding. */
  encoding(encoding: SourceEncoding): Self;
  /** Builds the URL (async: signing and encryption use WebCrypto). */
  build(): Promise<string>;
}

/**
 * A fluent processing URL: one method per option name and alias, each typed
 * by the option's argument tuples, plus `chain()` for `/-/`.
 */
export type ImgproxyUrlChain = ProcessingMethods &
  ChainControl<
    ImgproxyUrlChain,
    ImgproxyProcessingOptions,
    ImgproxyProcessingOptionName,
    { [K in ImgproxyProcessingOptionName]: ImgproxyProcessingArgsOf<K> }
  > & {
    /** Starts the next chained pipeline (`/-/`). */
    chain(): ImgproxyUrlChain;
    /** Sets the output extension (`@webp` / `.webp`). */
    extension(ext: string): ImgproxyUrlChain;
  };

/** A fluent `/info` URL. */
export type ImgproxyInfoUrlChain = InfoMethods &
  ChainControl<
    ImgproxyInfoUrlChain,
    ImgproxyInfoOptions,
    ImgproxyInfoOptionName,
    { [K in ImgproxyInfoOptionName]: ImgproxyInfoArgsOf<K> }
  >;

class Chain {
  readonly #cfg: ImgproxyUrlConfig;
  readonly #info: boolean;
  readonly #source: string;
  readonly #pipelines: OptionEntry[][] = [[]];
  #encoding: SourceEncoding | undefined;
  #ext: string | undefined;

  constructor(cfg: ImgproxyUrlConfig, info: boolean, source: string) {
    this.#cfg = cfg;
    this.#info = info;
    this.#source = source;
  }

  option(name: string, ...args: Arg[]): this {
    this.#pipelines.at(-1)!.push([name, args]);
    return this;
  }

  options(options: object): this {
    this.#pipelines.at(-1)!.push(...entriesOf(options));
    return this;
  }

  chain(): this {
    this.#pipelines.push([]);
    return this;
  }

  extension(ext: string): this {
    this.#ext = ext;
    return this;
  }

  encoding(encoding: SourceEncoding): this {
    this.#encoding = encoding;
    return this;
  }

  build(): Promise<string> {
    return build(this.#cfg, this.#info, this.#pipelines, this.#source, this.#encoding, this.#ext);
  }
}

// Per-option methods, installed once on a prototype per catalogue: they
// delegate to option(name, ...args), so the URL keeps the name as written.
function withOptionMethods(names: Iterable<string>): typeof Chain {
  class Methods extends Chain {}
  const reserved = new Set(Object.getOwnPropertyNames(Chain.prototype));
  for (const name of names) {
    if (reserved.has(name)) throw new Error(`option ${name} collides with a builder method`);
    Object.defineProperty(Methods.prototype, name, {
      value(this: Chain, ...args: Arg[]) {
        return this.option(name, ...args);
      },
      writable: false,
      enumerable: false,
    });
  }
  return Methods;
}

const ProcessingChain = withOptionMethods(processingCatalogue.canonical.keys());
const InfoChain = withOptionMethods(infoCatalogue.canonical.keys());

/** A URL builder bound to a config (base URL, keys, encoding). */
export interface ImgproxyUrlBuilder {
  readonly config: ImgproxyUrlConfig;
  /** Builds a processing URL from the plain object form. */
  url(request: ImgproxyUrlRequest): Promise<string>;
  /** Builds an `/info` URL from the plain object form. */
  infoUrl(request: ImgproxyInfoUrlRequest): Promise<string>;
  /** Starts a fluent processing URL for source. */
  from(source: string): ImgproxyUrlChain;
  /** Starts a fluent `/info` URL for source. */
  info(source: string): ImgproxyInfoUrlChain;
}

/**
 * Returns a URL builder for an xtandard-image (or imgproxy) server.
 *
 * ```ts
 * const img = imgproxy({ baseUrl: "https://img.example.com", signing: { key, salt } });
 * await img.url({ source: "s3://bucket/cat.jpg", options: { rs: ["fill", 300, 400], q: 80 }, extension: "webp" });
 * await img.from("s3://bucket/cat.jpg").resize("fill", 300, 400).quality(80).extension("webp").build();
 * ```
 */
export function imgproxy(config: ImgproxyUrlConfig = {}): ImgproxyUrlBuilder {
  const cfg = Object.freeze({ ...config });
  return {
    config: cfg,
    url: (request) => buildImgproxyUrl(request, cfg),
    infoUrl: (request) => buildImgproxyInfoUrl(request, cfg),
    from: (source) => new ProcessingChain(cfg, false, source) as unknown as ImgproxyUrlChain,
    info: (source) => new InfoChain(cfg, true, source) as unknown as ImgproxyInfoUrlChain,
  };
}
