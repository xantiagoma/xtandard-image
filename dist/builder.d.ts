import { type EncryptionKey } from "./encrypt.ts";
import { type Arg, type ImgproxyInfoArgsOf, type ImgproxyInfoOptionName, type ImgproxyInfoOptions, type ImgproxyProcessingArgsOf, type ImgproxyProcessingOptionName, type ImgproxyProcessingOptions, type ImgproxyVideoArgs, type ImgproxyVideoArgsOf, type ImgproxyVideoOptionName, type ImgproxyVideoOptions, type OptionNameStyle } from "./options.ts";
import { type ImaginaryEndpoint, type ImaginaryParams, type QueryImageParams, type QueryUrlConfig, type QueryValue } from "./query.ts";
import { type SigningKey } from "./sign.ts";
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
    /**
     * Segment container: "fmp4" (CMAF, default) or "ts" (MPEG-TS for older
     * HLS players; HLS only, H.264 + AAC, so images with libx264 only).
     */
    readonly segmentFormat?: "fmp4" | "ts";
};
/** Percent-escapes a source for a `plain/` URL; the server path-unescapes it. */
export declare function escapePlainSource(source: string): string;
/** Builds a processing URL from the plain object form. */
export declare function buildImgproxyUrl(request: ImgproxyUrlRequest, config?: ImgproxyUrlConfig): Promise<string>;
/** Builds an `/info` URL from the plain object form. */
export declare function buildImgproxyInfoUrl(request: ImgproxyInfoUrlRequest, config?: ImgproxyUrlConfig): Promise<string>;
/**
 * Builds a `/video` URL (video transformation, audio or waveform) from the
 * plain object form. The server serves it with XI_VIDEO_TRANSFORM_ENABLED
 * on the full or gpl image, signed by default.
 */
export declare function buildImgproxyVideoUrl(request: ImgproxyVideoUrlRequest, config?: ImgproxyUrlConfig): Promise<string>;
/** Builds an HLS or DASH stream manifest URL (`/video/…@m3u8`, `@mpd`). */
export declare function buildImgproxyStreamUrl(request: ImgproxyStreamUrlRequest, config?: ImgproxyUrlConfig): Promise<string>;
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
export declare function buildQueryUrl(request: QueryUrlRequest, config?: QueryUrlConfig): Promise<string>;
/** Builds a `/q/video` URL. */
export declare function buildQueryVideoUrl(request: QueryVideoUrlRequest, config?: QueryUrlConfig): Promise<string>;
/** Builds a `/q/video` HLS or DASH stream manifest URL (`format=m3u8|mpd`). */
export declare function buildQueryStreamUrl(request: QueryStreamUrlRequest, config?: QueryUrlConfig): Promise<string>;
/** Builds an imaginary endpoint URL (`/q/<endpoint>?url=…`). */
export declare function buildImaginaryUrl(request: ImaginaryUrlRequest, config?: QueryUrlConfig): Promise<string>;
type ProcessingMethods = {
    readonly [K in ImgproxyProcessingOptionName]: (...args: ImgproxyProcessingArgsOf<K>) => ImgproxyUrlChain;
};
type ProcessingMethodsOf<Self> = {
    readonly [K in ImgproxyProcessingOptionName]: (...args: ImgproxyProcessingArgsOf<K>) => Self;
};
interface QueryProcessingMethods extends ProcessingMethodsOf<QueryUrlChain> {
}
type InfoMethods = {
    readonly [K in ImgproxyInfoOptionName]: (...args: ImgproxyInfoArgsOf<K>) => ImgproxyInfoUrlChain;
};
type VideoMethods = {
    readonly [K in ImgproxyVideoOptionName]: (...args: ImgproxyVideoArgsOf<K>) => ImgproxyVideoUrlChain;
};
type VideoMethodsOf<Self> = {
    readonly [K in ImgproxyVideoOptionName]: (...args: ImgproxyVideoArgsOf<K>) => Self;
};
interface QueryVideoMethods extends VideoMethodsOf<QueryVideoUrlChain> {
}
interface OptionControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>> {
    /** Appends one option occurrence (same as the per-option method). */
    option<K extends Name>(name: K, ...args: ArgsOf[K]): Self;
    /** Appends every option of an options object, in key order. */
    options(options: Options): Self;
    /** Builds the URL (async: signing and encryption use WebCrypto). */
    build(): Promise<string>;
}
interface BaseControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>> extends OptionControl<Self, Options, Name, ArgsOf> {
    /** Overrides the source encoding. */
    encoding(encoding: SourceEncoding): Self;
}
interface ChainControl<Self, Options, Name extends string, ArgsOf extends Record<Name, readonly Arg[]>> extends BaseControl<Self, Options, Name, ArgsOf> {
    /** Opens a password-protected PDF: see ImgproxyUrlRequest.pdfPassword. */
    pdfPassword(password: string): Self;
}
type ProcessingArgsTable = {
    [K in ImgproxyProcessingOptionName]: ImgproxyProcessingArgsOf<K>;
};
type VideoArgsTable = {
    [K in ImgproxyVideoOptionName]: ImgproxyVideoArgsOf<K>;
};
/**
 * A fluent processing URL: one method per option name and alias, each typed
 * by the option's argument tuples, plus `chain()` for `/-/`.
 */
export type ImgproxyUrlChain = ProcessingMethods & ChainControl<ImgproxyUrlChain, ImgproxyProcessingOptions, ImgproxyProcessingOptionName, ProcessingArgsTable> & {
    /** Starts the next chained pipeline (`/-/`). */
    chain(): ImgproxyUrlChain;
    /** Sets the output extension (`@webp` / `.webp`). */
    extension(ext: string): ImgproxyUrlChain;
};
/** A fluent `/info` URL. */
export type ImgproxyInfoUrlChain = InfoMethods & ChainControl<ImgproxyInfoUrlChain, ImgproxyInfoOptions, ImgproxyInfoOptionName, {
    [K in ImgproxyInfoOptionName]: ImgproxyInfoArgsOf<K>;
}>;
/**
 * A fluent `/video` URL: one method per video option name and alias
 * (`start`, `vs`, `resize`, `loudnorm`, `waveform`, …).
 */
export type ImgproxyVideoUrlChain = VideoMethods & BaseControl<ImgproxyVideoUrlChain, ImgproxyVideoOptions, ImgproxyVideoOptionName, VideoArgsTable> & {
    /** Sets the output container (`@webm`, `.mp3`, `@png` for a waveform, `@m3u8`/`@mpd` for a stream). */
    extension(ext: VideoContainer | (string & {})): ImgproxyVideoUrlChain;
};
/**
 * A fluent native query dialect URL (`/q/?url=…`): the processing option
 * methods write `o=`, `param()` writes a native key.
 */
export type QueryUrlChain = QueryProcessingMethods & OptionControl<QueryUrlChain, ImgproxyProcessingOptions, ImgproxyProcessingOptionName, ProcessingArgsTable> & {
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
export type QueryVideoUrlChain = QueryVideoMethods & OptionControl<QueryVideoUrlChain, ImgproxyVideoOptions, ImgproxyVideoOptionName, VideoArgsTable> & {
    /** Sets the output container (a last `format=` key). */
    extension(ext: VideoContainer | (string & {})): QueryVideoUrlChain;
};
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
export declare function imgproxy(config?: ImgproxyUrlConfig): ImgproxyUrlBuilder;
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
export declare function queryUrls(config?: QueryUrlConfig): QueryUrlBuilder;
export {};
