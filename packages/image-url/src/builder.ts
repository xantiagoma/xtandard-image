// imgproxy-dialect URL builder (docs/URL-DIALECTS.md §3.1):
//
//   {baseUrl}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
//   {baseUrl}/video/{signature}/{video option}/…/{source}   (§3.8: the
//       signature covers "/video" + the path after it)
//
// Sources are base64url (default), `plain/` (percent-escaped, `@ext`) or
// `enc/` (AES-CBC, see encrypt.ts); base64 and enc sources take `.ext`.

import { base64url, hasControlChar, utf8 } from "./bytes.ts";
import { type EncryptionKey, encryptPdfPassword, encryptSource } from "./encrypt.ts";
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
  type ImgproxyVideoArgsOf,
  type ImgproxyVideoOptionName,
  type ImgproxyVideoOptions,
  infoCatalogue,
  type OptionEntry,
  type OptionNameStyle,
  processingCatalogue,
  videoCatalogue,
} from "./options.ts";
import { type SigningKey, sign, signImgproxyPath } from "./sign.ts";

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
  /**
   * The password of a password-protected PDF source, as plain text: it is
   * encrypted with `encryption` (encryptPdfPassword) into a `pdf_password`
   * option. The server accepts it on signed URLs only, so `signing` is
   * required too. Build such URLs on a server.
   */
  readonly pdfPassword?: string;
}

/** One `/info` URL, in the plain object form. */
export type ImgproxyInfoUrlRequest = Omit<ImgproxyUrlRequest<ImgproxyInfoOptions>, "options" | "extension"> & {
  readonly options?: ImgproxyInfoOptions;
};

/**
 * One `/video` URL (video transformation, audio or waveform), in the plain
 * object form. `extension` is the output container: mp4, webm, m4a, mp3,
 * opus, ogg, flac, wav, or png/webp for a waveform.
 */
export type ImgproxyVideoUrlRequest = Omit<ImgproxyUrlRequest<ImgproxyVideoOptions>, "options" | "pdfPassword"> & {
  readonly options?: ImgproxyVideoOptions;
};

/** Which grammar a URL uses: processing, `/info` or `/video`. */
type Endpoint = "process" | "info" | "video";

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

/**
 * Joins the base URL, the endpoint, the signature and the path. A `/video`
 * signature covers "/video" + the path, so no image signature is valid for
 * a video transformation.
 */
async function finish(cfg: ImgproxyUrlConfig, endpoint: Endpoint, path: string): Promise<string> {
  const base = (cfg.baseUrl ?? "").replace(/\/+$/, "");
  if (endpoint === "video") {
    const sig = cfg.signing ? await sign(`/video${path}`, cfg.signing) : (cfg.unsignedSignature ?? "insecure");
    return `${base}/video/${sig}${path}`;
  }
  const signed = cfg.signing
    ? await signImgproxyPath(path, cfg.signing)
    : `/${cfg.unsignedSignature ?? "insecure"}${path}`;
  return `${base}${endpoint === "info" ? "/info" : ""}${signed}`;
}

/**
 * The `pdf_password` segment for a plain-text password. The token is
 * written verbatim: base64url may contain a separator such as "_", which the
 * server joins back.
 */
async function passwordSegment(cfg: ImgproxyUrlConfig, password: string, sep: string, style: OptionNameStyle): Promise<string> {
  if (!cfg.encryption) throw new Error("pdfPassword needs an encryption key");
  if (!cfg.signing) throw new Error("pdfPassword needs signing: the server refuses pdf_password on unsigned URLs");
  const token = await encryptPdfPassword(password, cfg.encryption);
  return `${style === "canonical" ? "pdf_password" : "pdp"}${sep}${token}`;
}

/** Builds a URL from option pipelines (entries) and a source. */
async function build(
  cfg: ImgproxyUrlConfig,
  endpoint: Endpoint,
  pipelines: readonly (readonly OptionEntry[])[],
  source: string,
  encoding: SourceEncoding | undefined,
  ext: string | undefined,
  pdfPassword?: string,
): Promise<string> {
  const sep = checkSeparator(cfg.separator ?? ":");
  const style = cfg.optionNames ?? "short";
  const cat = endpoint === "info" ? infoCatalogue : endpoint === "video" ? videoCatalogue : processingCatalogue;
  // Empty pipelines (an unused chain()) are dropped: the server rejects them.
  const nonEmpty = pipelines.filter((p) => p.length > 0);
  if (endpoint !== "process" && nonEmpty.length > 1) throw new TypeError(`/${endpoint} URLs have no chained pipelines`);
  if (endpoint === "video" && pdfPassword !== undefined) throw new TypeError("/video URLs take no pdfPassword");
  const segments: string[] = [];
  if (pdfPassword !== undefined) segments.push(await passwordSegment(cfg, pdfPassword, sep, style));
  nonEmpty.forEach((p, i) => {
    if (i > 0) segments.push("-");
    for (const e of p) segments.push(formatEntry(cat, e, sep, style));
  });
  segments.push(await sourceSegment(cfg, source, encoding ?? cfg.sourceEncoding ?? "base64", ext));
  return finish(cfg, endpoint, `/${segments.join("/")}`);
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
    "process",
    pipelinesOf(request.options),
    request.source,
    request.sourceEncoding,
    request.extension,
    request.pdfPassword,
  );
}

/** Builds an `/info` URL from the plain object form. */
export function buildImgproxyInfoUrl(request: ImgproxyInfoUrlRequest, config: ImgproxyUrlConfig = {}): Promise<string> {
  return build(
    config,
    "info",
    pipelinesOf(request.options),
    request.source,
    request.sourceEncoding,
    undefined,
    request.pdfPassword,
  );
}

/**
 * Builds a `/video` URL (video transformation, audio or waveform) from the
 * plain object form. The server serves it with XI_VIDEO_TRANSFORM_ENABLED
 * on the full or gpl image, signed by default.
 */
export function buildImgproxyVideoUrl(request: ImgproxyVideoUrlRequest, config: ImgproxyUrlConfig = {}): Promise<string> {
  return build(
    config,
    "video",
    pipelinesOf(request.options),
    request.source,
    request.sourceEncoding,
    request.extension,
  );
}

// ---- fluent form -------------------------------------------------------------

type ProcessingMethods = {
  readonly [K in ImgproxyProcessingOptionName]: (...args: ImgproxyProcessingArgsOf<K>) => ImgproxyUrlChain;
};

type InfoMethods = {
  readonly [K in ImgproxyInfoOptionName]: (...args: ImgproxyInfoArgsOf<K>) => ImgproxyInfoUrlChain;
};

type VideoMethods = {
  readonly [K in ImgproxyVideoOptionName]: (...args: ImgproxyVideoArgsOf<K>) => ImgproxyVideoUrlChain;
};

interface BaseControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>> {
  /** Appends one option occurrence (same as the per-option method). */
  option<K extends Name>(name: K, ...args: ArgsOf[K]): Self;
  /** Appends every option of an options object, in key order. */
  options(options: Options): Self;
  /** Overrides the source encoding. */
  encoding(encoding: SourceEncoding): Self;
  /** Builds the URL (async: signing and encryption use WebCrypto). */
  build(): Promise<string>;
}

interface ChainControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>>
  extends BaseControl<Self, Options, Name, ArgsOf> {
  /** Opens a password-protected PDF: see ImgproxyUrlRequest.pdfPassword. */
  pdfPassword(password: string): Self;
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

/**
 * A fluent `/video` URL: one method per video option name and alias
 * (`start`, `vs`, `resize`, `loudnorm`, `waveform`, …).
 */
export type ImgproxyVideoUrlChain = VideoMethods &
  BaseControl<
    ImgproxyVideoUrlChain,
    ImgproxyVideoOptions,
    ImgproxyVideoOptionName,
    { [K in ImgproxyVideoOptionName]: ImgproxyVideoArgsOf<K> }
  > & {
    /** Sets the output container (`@webm`, `.mp3`, `@png` for a waveform). */
    extension(ext: string): ImgproxyVideoUrlChain;
  };

class Chain {
  readonly #cfg: ImgproxyUrlConfig;
  readonly #endpoint: Endpoint;
  readonly #source: string;
  readonly #pipelines: OptionEntry[][] = [[]];
  #encoding: SourceEncoding | undefined;
  #ext: string | undefined;
  #pdfPassword: string | undefined;

  constructor(cfg: ImgproxyUrlConfig, endpoint: Endpoint, source: string) {
    this.#cfg = cfg;
    this.#endpoint = endpoint;
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

  pdfPassword(password: string): this {
    this.#pdfPassword = password;
    return this;
  }

  build(): Promise<string> {
    return build(this.#cfg, this.#endpoint, this.#pipelines, this.#source, this.#encoding, this.#ext, this.#pdfPassword);
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
const VideoChain = withOptionMethods(videoCatalogue.canonical.keys());

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
  /** Builds a `/video` URL from the plain object form. */
  videoUrl(request: ImgproxyVideoUrlRequest): Promise<string>;
  /** Starts a fluent `/video` URL (video transformation, audio, waveform) for source. */
  video(source: string): ImgproxyVideoUrlChain;
}

/**
 * Returns a URL builder for an xtandard-image (or imgproxy) server.
 *
 * ```ts
 * const img = imgproxy({ baseUrl: "https://img.example.com", signing: { key, salt } });
 * await img.url({ source: "s3://bucket/cat.jpg", options: { rs: ["fill", 300, 400], q: 80 }, extension: "webp" });
 * await img.from("s3://bucket/cat.jpg").resize("fill", 300, 400).quality(80).extension("webp").build();
 * await img.video("s3://bucket/talk.mov").resize("fill", 640, 360).start(12).duration(8).extension("webm").build();
 * ```
 */
export function imgproxy(config: ImgproxyUrlConfig = {}): ImgproxyUrlBuilder {
  const cfg = Object.freeze({ ...config });
  return {
    config: cfg,
    url: (request) => buildImgproxyUrl(request, cfg),
    infoUrl: (request) => buildImgproxyInfoUrl(request, cfg),
    from: (source) => new ProcessingChain(cfg, "process", source) as unknown as ImgproxyUrlChain,
    info: (source) => new InfoChain(cfg, "info", source) as unknown as ImgproxyInfoUrlChain,
    videoUrl: (request) => buildImgproxyVideoUrl(request, cfg),
    video: (source) => new VideoChain(cfg, "video", source) as unknown as ImgproxyVideoUrlChain,
  };
}
