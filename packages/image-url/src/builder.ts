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
  type ImgproxyVideoArgs,
  type ImgproxyVideoArgsOf,
  type ImgproxyVideoOptionName,
  type ImgproxyVideoOptions,
  infoCatalogue,
  type OptionEntry,
  type OptionNameStyle,
  processingCatalogue,
  videoCatalogue,
} from "./options.ts";
import {
  buildImaginary,
  buildQueryImage,
  buildQueryVideo,
  type ImaginaryEndpoint,
  type ImaginaryParams,
  type QueryImageParams,
  type QueryUrlConfig,
  type QueryValue,
} from "./query.ts";
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

/**
 * A `/video` output container, from the video catalogue's `format` enum:
 * mp4, webm, m4a, mp3, opus, ogg, flac, wav, png, webp, m3u8, mpd.
 */
export type VideoContainer = NonNullable<ImgproxyVideoArgs["format"][0]>;

/** A stream protocol, from the video catalogue's `stream` option: hls or dash. */
export type StreamProtocol = NonNullable<ImgproxyVideoArgs["stream"][0]>;

/**
 * The manifest container of each stream protocol. Typed against the
 * catalogue: a protocol added on the Go side fails to compile here until
 * it has its container.
 */
const streamManifests: { readonly [P in StreamProtocol]: VideoContainer } = { hls: "m3u8", dash: "mpd" };

/**
 * An HLS or DASH stream of a video source (URL-DIALECTS §3.10): a
 * `/video` URL with the `m3u8` or `mpd` container. The manifest's child
 * URLs (`/_stream/<ticket>/…`) carry a ticket, so a player needs no key.
 */
export type ImgproxyStreamUrlRequest = Omit<ImgproxyVideoUrlRequest, "extension"> & {
  /** hls (`@m3u8`) or dash (`@mpd`). */
  readonly protocol: StreamProtocol;
  /**
   * Renditions by the short side of the frame (`[360, 720]`, at most 8);
   * default: the server's XI_VIDEO_TRANSFORM_ABR_LADDER.
   */
  readonly renditions?: readonly number[];
  /** Segment length in whole seconds (1..30); default XI_VIDEO_TRANSFORM_SEGMENT_DURATION. */
  readonly segmentDuration?: number;
};

/** The video options and container of a stream request. */
function streamParts(req: ImgproxyStreamUrlRequest): { entries: OptionEntry[]; ext: string } {
  const entries = pipelinesOf(req.options).flat();
  for (const [name] of entries) {
    const c = videoCatalogue.canonical.get(name);
    if (c === "stream" || c === "segment_duration" || c === "format") {
      throw new TypeError(`stream requests set ${name} through protocol, renditions and segmentDuration`);
    }
  }
  const ext = Object.hasOwn(streamManifests, req.protocol) ? streamManifests[req.protocol] : undefined;
  if (ext === undefined) throw new TypeError(`unknown stream protocol ${JSON.stringify(req.protocol)}`);
  if (req.renditions?.length) entries.push(["stream", [req.protocol, ...req.renditions]]);
  if (req.segmentDuration !== undefined) entries.push(["segment_duration", [req.segmentDuration]]);
  return { entries, ext };
}

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

/** Builds an HLS or DASH stream manifest URL (`/video/…@m3u8`, `@mpd`). */
export async function buildImgproxyStreamUrl(request: ImgproxyStreamUrlRequest, config: ImgproxyUrlConfig = {}): Promise<string> {
  const { entries, ext } = streamParts(request);
  return build(config, "video", [entries], request.source, request.sourceEncoding, ext);
}

// ---- query dialect -----------------------------------------------------------

/**
 * One native query dialect URL (`/q/?url=…`, URL-DIALECTS §4): native keys
 * in `params`, and any imgproxy processing options in `options`, written
 * as one `o=` option path (an array chains pipelines).
 */
export interface QueryUrlRequest {
  /** The source URL (http(s)://, s3://, xi://<source>/<key>, …), written as is. */
  readonly source: string;
  /** Native keys (`w`, `fit`, `g`, `f`, `q`, `preset`, `vts`, …), in key order, before `o=`. */
  readonly params?: QueryImageParams;
  /** imgproxy processing options, written as `o=<option path>`. */
  readonly options?: ImgproxyProcessingOptions | readonly ImgproxyProcessingOptions[];
  /** Output format, written as a last `format=` key. */
  readonly extension?: string;
  /** Plain-text PDF password, encrypted into `pdp=` (needs `encryption` and `signing`). */
  readonly pdfPassword?: string;
}

/**
 * One `/q/video` URL: every `/video` option is a query key with its
 * arguments joined by "," (`rs=fill,640,360&vs=1`). An array of option
 * objects is written in order, so a key may repeat (later wins argument by
 * argument). `crop` and `flip` are written `c` and `fl`, since their query
 * spellings are native.
 */
export interface QueryVideoUrlRequest {
  readonly source: string;
  readonly options?: ImgproxyVideoOptions | readonly ImgproxyVideoOptions[];
  /** Output container, written as a last `format=` key. */
  readonly extension?: VideoContainer | (string & {});
}

/** One `/q/video` HLS or DASH stream manifest URL. */
export type QueryStreamUrlRequest = Omit<ImgproxyStreamUrlRequest, "sourceEncoding">;

/** One imaginary endpoint URL (`/q/resize?url=…&width=300`). */
export interface ImaginaryUrlRequest {
  readonly endpoint: ImaginaryEndpoint;
  readonly source: string;
  readonly params?: ImaginaryParams;
}

/** Builds a native query dialect URL (`/q/?url=…`). */
export function buildQueryUrl(request: QueryUrlRequest, config: QueryUrlConfig = {}): Promise<string> {
  return buildQueryImage(config, pipelinesOf(request.options), request.source, request.params, request.extension, request.pdfPassword);
}

/** Builds a `/q/video` URL. */
export function buildQueryVideoUrl(request: QueryVideoUrlRequest, config: QueryUrlConfig = {}): Promise<string> {
  return buildQueryVideo(config, pipelinesOf(request.options).flat(), request.source, request.extension);
}

/** Builds a `/q/video` HLS or DASH stream manifest URL (`format=m3u8|mpd`). */
export async function buildQueryStreamUrl(request: QueryStreamUrlRequest, config: QueryUrlConfig = {}): Promise<string> {
  const { entries, ext } = streamParts(request);
  return buildQueryVideo(config, entries, request.source, ext);
}

/** Builds an imaginary endpoint URL (`/q/<endpoint>?url=…`). */
export function buildImaginaryUrl(request: ImaginaryUrlRequest, config: QueryUrlConfig = {}): Promise<string> {
  return buildImaginary(config, request.endpoint, request.source, request.params);
}

// ---- fluent form -------------------------------------------------------------

type ProcessingMethods = {
  readonly [K in ImgproxyProcessingOptionName]: (...args: ImgproxyProcessingArgsOf<K>) => ImgproxyUrlChain;
};

type ProcessingMethodsOf<Self> = {
  readonly [K in ImgproxyProcessingOptionName]: (...args: ImgproxyProcessingArgsOf<K>) => Self;
};

// Interfaces defer their members, which lets the query chains refer to themselves.
interface QueryProcessingMethods extends ProcessingMethodsOf<QueryUrlChain> {}

type InfoMethods = {
  readonly [K in ImgproxyInfoOptionName]: (...args: ImgproxyInfoArgsOf<K>) => ImgproxyInfoUrlChain;
};

type VideoMethods = {
  readonly [K in ImgproxyVideoOptionName]: (...args: ImgproxyVideoArgsOf<K>) => ImgproxyVideoUrlChain;
};

type VideoMethodsOf<Self> = {
  readonly [K in ImgproxyVideoOptionName]: (...args: ImgproxyVideoArgsOf<K>) => Self;
};

interface QueryVideoMethods extends VideoMethodsOf<QueryVideoUrlChain> {}

interface OptionControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>> {
  /** Appends one option occurrence (same as the per-option method). */
  option<K extends Name>(name: K, ...args: ArgsOf[K]): Self;
  /** Appends every option of an options object, in key order. */
  options(options: Options): Self;
  /** Builds the URL (async: signing and encryption use WebCrypto). */
  build(): Promise<string>;
}

interface BaseControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>>
  extends OptionControl<Self, Options, Name, ArgsOf> {
  /** Overrides the source encoding. */
  encoding(encoding: SourceEncoding): Self;
}

interface ChainControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>>
  extends BaseControl<Self, Options, Name, ArgsOf> {
  /** Opens a password-protected PDF: see ImgproxyUrlRequest.pdfPassword. */
  pdfPassword(password: string): Self;
}

type ProcessingArgsTable = { [K in ImgproxyProcessingOptionName]: ImgproxyProcessingArgsOf<K> };
type VideoArgsTable = { [K in ImgproxyVideoOptionName]: ImgproxyVideoArgsOf<K> };

/**
 * A fluent processing URL: one method per option name and alias, each typed
 * by the option's argument tuples, plus `chain()` for `/-/`.
 */
export type ImgproxyUrlChain = ProcessingMethods &
  ChainControl<ImgproxyUrlChain, ImgproxyProcessingOptions, ImgproxyProcessingOptionName, ProcessingArgsTable> & {
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
  BaseControl<ImgproxyVideoUrlChain, ImgproxyVideoOptions, ImgproxyVideoOptionName, VideoArgsTable> & {
    /** Sets the output container (`@webm`, `.mp3`, `@png` for a waveform, `@m3u8`/`@mpd` for a stream). */
    extension(ext: VideoContainer | (string & {})): ImgproxyVideoUrlChain;
  };

/**
 * A fluent native query dialect URL (`/q/?url=…`): the processing option
 * methods write `o=`, `param()` writes a native key.
 */
export type QueryUrlChain = QueryProcessingMethods &
  OptionControl<QueryUrlChain, ImgproxyProcessingOptions, ImgproxyProcessingOptionName, ProcessingArgsTable> & {
    /** Appends a native key (`w`, `fit`, `g`, …), written before `o=`. */
    param<K extends keyof QueryImageParams>(key: K, value: QueryValue): QueryUrlChain;
    /** Starts the next chained pipeline inside `o=` (`/-/`). */
    chain(): QueryUrlChain;
    /** Sets the output format (a last `format=` key). */
    extension(ext: string): QueryUrlChain;
    /** Opens a password-protected PDF (`pdp=`). */
    pdfPassword(password: string): QueryUrlChain;
  };

/** A fluent `/q/video` URL: one method per video option name and alias. */
export type QueryVideoUrlChain = QueryVideoMethods &
  OptionControl<QueryVideoUrlChain, ImgproxyVideoOptions, ImgproxyVideoOptionName, VideoArgsTable> & {
    /** Sets the output container (a last `format=` key). */
    extension(ext: VideoContainer | (string & {})): QueryVideoUrlChain;
  };

/** What a fluent chain collected; `run` turns it into a URL. */
interface ChainState {
  readonly source: string;
  readonly pipelines: OptionEntry[][];
  readonly params: [string, QueryValue][];
  encoding: SourceEncoding | undefined;
  ext: string | undefined;
  pdfPassword: string | undefined;
}

type Run = (s: ChainState) => Promise<string>;

class Chain {
  readonly #run: Run;
  readonly #s: ChainState;

  constructor(run: Run, source: string) {
    this.#run = run;
    this.#s = { source, pipelines: [[]], params: [], encoding: undefined, ext: undefined, pdfPassword: undefined };
  }

  option(name: string, ...args: Arg[]): this {
    this.#s.pipelines.at(-1)!.push([name, args]);
    return this;
  }

  options(options: object): this {
    this.#s.pipelines.at(-1)!.push(...entriesOf(options));
    return this;
  }

  param(key: string, value: QueryValue): this {
    this.#s.params.push([key, value]);
    return this;
  }

  chain(): this {
    this.#s.pipelines.push([]);
    return this;
  }

  extension(ext: string): this {
    this.#s.ext = ext;
    return this;
  }

  encoding(encoding: SourceEncoding): this {
    this.#s.encoding = encoding;
    return this;
  }

  pdfPassword(password: string): this {
    this.#s.pdfPassword = password;
    return this;
  }

  build(): Promise<string> {
    return this.#run(this.#s);
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

function imgproxyRun(cfg: ImgproxyUrlConfig, endpoint: Endpoint): Run {
  return (s) => build(cfg, endpoint, s.pipelines, s.source, s.encoding, s.ext, s.pdfPassword);
}

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
  /** Builds an HLS or DASH stream manifest URL (`/video/…@m3u8`, `@mpd`). */
  streamUrl(request: ImgproxyStreamUrlRequest): Promise<string>;
}

/**
 * Returns a URL builder for an xtandard-image (or imgproxy) server.
 *
 * ```ts
 * const img = imgproxy({ baseUrl: "https://img.example.com", signing: { key, salt } });
 * await img.url({ source: "s3://bucket/cat.jpg", options: { rs: ["fill", 300, 400], q: 80 }, extension: "webp" });
 * await img.from("s3://bucket/cat.jpg").resize("fill", 300, 400).quality(80).extension("webp").build();
 * await img.video("s3://bucket/talk.mov").resize("fill", 640, 360).start(12).duration(8).extension("webm").build();
 * await img.streamUrl({ source: "s3://bucket/talk.mov", protocol: "hls", renditions: [360, 720] });
 * ```
 */
export function imgproxy(config: ImgproxyUrlConfig = {}): ImgproxyUrlBuilder {
  const cfg = Object.freeze({ ...config });
  return {
    config: cfg,
    url: (request) => buildImgproxyUrl(request, cfg),
    infoUrl: (request) => buildImgproxyInfoUrl(request, cfg),
    from: (source) => new ProcessingChain(imgproxyRun(cfg, "process"), source) as unknown as ImgproxyUrlChain,
    info: (source) => new InfoChain(imgproxyRun(cfg, "info"), source) as unknown as ImgproxyInfoUrlChain,
    videoUrl: (request) => buildImgproxyVideoUrl(request, cfg),
    video: (source) => new VideoChain(imgproxyRun(cfg, "video"), source) as unknown as ImgproxyVideoUrlChain,
    streamUrl: (request) => buildImgproxyStreamUrl(request, cfg),
  };
}

/** A query dialect URL builder bound to a config (base URL, mount, keys). */
export interface QueryUrlBuilder {
  readonly config: QueryUrlConfig;
  /** Builds a native `/q/?url=…` URL from the plain object form. */
  url(request: QueryUrlRequest): Promise<string>;
  /** Starts a fluent native `/q/?url=…` URL for source. */
  from(source: string): QueryUrlChain;
  /** Builds a `/q/video` URL from the plain object form. */
  videoUrl(request: QueryVideoUrlRequest): Promise<string>;
  /** Starts a fluent `/q/video` URL for source. */
  video(source: string): QueryVideoUrlChain;
  /** Builds a `/q/video` HLS or DASH stream manifest URL. */
  streamUrl(request: QueryStreamUrlRequest): Promise<string>;
  /** Builds an imaginary endpoint URL (`/q/resize?url=…`). */
  imaginaryUrl(request: ImaginaryUrlRequest): Promise<string>;
}

/**
 * Returns a query dialect URL builder (URL-DIALECTS §4; the server needs
 * XI_DIALECTS_QUERY_ENABLED=true). With `signing` every URL ends in an
 * `xi_sig` over the path after the mount and the query.
 *
 * ```ts
 * const q = queryUrls({ baseUrl: "https://img.example.com", signing: { key, salt } });
 * await q.url({ source: "s3://bucket/cat.jpg", params: { w: 300, f: "webp" }, options: { bl: 2 } });
 * // https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
 * await q.video("s3://bucket/talk.mov").vs(1).vd(5).rs("fill", 640, 360).extension("webm").build();
 * ```
 */
export function queryUrls(config: QueryUrlConfig = {}): QueryUrlBuilder {
  const cfg = Object.freeze({ ...config });
  const image: Run = (s) => buildQueryImage(cfg, s.pipelines, s.source, s.params, s.ext, s.pdfPassword);
  const video: Run = (s) => {
    if (s.pipelines.filter((p) => p.length > 0).length > 1) throw new TypeError("/q/video URLs have no chained pipelines");
    return buildQueryVideo(cfg, s.pipelines.flat(), s.source, s.ext);
  };
  return {
    config: cfg,
    url: (request) => buildQueryUrl(request, cfg),
    from: (source) => new ProcessingChain(image, source) as unknown as QueryUrlChain,
    videoUrl: (request) => buildQueryVideoUrl(request, cfg),
    video: (source) => new VideoChain(video, source) as unknown as QueryVideoUrlChain,
    streamUrl: (request) => buildQueryStreamUrl(request, cfg),
    imaginaryUrl: (request) => buildImaginaryUrl(request, cfg),
  };
}
