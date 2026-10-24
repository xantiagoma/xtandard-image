import { type ImgproxyInfoUrlRequest, type ImgproxyStreamUrlRequest, type ImgproxyUrlConfig, type ImgproxyUrlRequest, type ImgproxyVideoUrlRequest } from "./builder.ts";
import type { components } from "./generated/openapi.ts";
export type { components, operations, paths } from "./generated/openapi.ts";
/** The `/info` response body. */
export type InfoResponse = components["schemas"]["InfoResponse"];
/** The JSON error envelope: `{"error":{"kind","message","request_id"}}`. */
export type ErrorResponse = components["schemas"]["ErrorResponse"];
/** The error kind (errs.Kind), one per row of the server's status table. */
export type ErrorKind = components["schemas"]["ErrorBody"]["kind"];
/** The playground's `api/meta` body (capabilities, catalogues, sources). */
export type PlaygroundMeta = components["schemas"]["PlaygroundMeta"];
/** `/readyz` states. */
export type ReadyState = "ready" | "starting" | "draining";
type Fetch = (input: Request | string | URL, init?: RequestInit) => Promise<Response>;
/** Client settings. */
export interface XtandardClientOptions {
    /** Server origin, plus the imgproxy mount prefix if any ("http://localhost:8080"). */
    readonly baseUrl: string;
    /** fetch implementation. Default: the global fetch. */
    readonly fetch?: Fetch;
    /** Headers sent with every request. */
    readonly headers?: HeadersInit;
    /** URL builder settings for `/info`, images, `/video` and streams (signing, encryption, …); baseUrl is the client's. */
    readonly imgproxy?: Omit<ImgproxyUrlConfig, "baseUrl">;
    /**
     * Where the playground's JSON API lives, for `playgroundMeta()`: the
     * admin listener's origin (default: baseUrl) and XI_PLAYGROUND_PATH
     * (default "/_playground/").
     */
    readonly playground?: {
        readonly baseUrl?: string;
        readonly path?: string;
    };
    /**
     * Send query dialect URLs (those under `queryMount`) passed to `media()`
     * with the HTTP QUERY method (RFC 10008): the query moves into the
     * request content. Off by default: the server needs XI_SERVER_QUERY_METHOD
     * (on by default), and many CDNs and proxies neither cache nor forward
     * QUERY. `{ method: "QUERY" }` on one call does the same for that call.
     */
    readonly queryMethod?: boolean;
    /** The query dialect's mount (XI_DIALECTS_QUERY_PREFIX), for `queryMethod`. Default "/q". */
    readonly queryMount?: string;
}
/** Per-call options. */
export interface CallOptions {
    readonly signal?: AbortSignal;
    readonly headers?: HeadersInit;
}
/**
 * A byte range: a header value ("bytes=0-1023"), a start and an optional
 * inclusive end, or the last `suffix` bytes.
 */
export type ByteRange = string | {
    readonly start: number;
    readonly end?: number;
} | {
    readonly suffix: number;
};
/** Options of a media request (image, video, audio, stream part). */
export interface MediaOptions extends CallOptions {
    /**
     * GET (default) or HEAD: HEAD answers the headers (type, length, ETag)
     * without the body. QUERY sends a query dialect URL's query as the
     * request content (RFC 10008; see XtandardClientOptions.queryMethod).
     */
    readonly method?: "GET" | "HEAD" | "QUERY";
    /** Ask for part of the body (206 Partial Content): `/video` results and stream parts support it. */
    readonly range?: ByteRange;
    /** The Accept header (format negotiation for images). Default `*\/*`. */
    readonly accept?: string;
}
/** A fetched HLS or DASH manifest. */
export interface StreamManifest {
    /** The manifest URL (signed `/video/…@m3u8|mpd`); hand it to a player. */
    readonly url: string;
    /** application/vnd.apple.mpegurl or application/dash+xml. */
    readonly contentType: string;
    /** The playlist or MPD; its child URLs (`/_stream/<ticket>/…`) carry a ticket and need no signing. */
    readonly body: string;
}
/** Formats a ByteRange as a Range header value. */
export declare function rangeHeader(r: ByteRange): string;
/**
 * A non-2xx response. `kind`, `message` and `requestId` come from the
 * server's JSON error envelope; a body that is not the envelope (a proxy's
 * error page, say) yields kind "unknown" and the status text.
 */
export declare class XtandardError extends Error {
    readonly name = "XtandardError";
    /** HTTP status. */
    readonly status: number;
    /** Error kind from the envelope, or "unknown". */
    readonly kind: ErrorKind;
    /** Request ID from the envelope (also in the server logs). */
    readonly requestId: string | undefined;
    /** The parsed envelope, when the body was one. */
    readonly body: ErrorResponse | undefined;
    /** The request URL. */
    readonly url: string;
    constructor(status: number, url: string, body: ErrorResponse | undefined, fallback: string);
}
/** Converts a non-2xx response into an XtandardError. */
export declare function toXtandardError(res: Response): Promise<XtandardError>;
/** The client returned by createClient. */
export interface XtandardClient {
    /** GET /healthz: resolves while the process can serve HTTP. */
    healthz(opts?: CallOptions): Promise<void>;
    /** GET /readyz: the readiness state; 503 (starting, draining) is not an error. */
    readyz(opts?: CallOptions): Promise<{
        ready: boolean;
        state: ReadyState;
    }>;
    /** GET /info for a source, with the URL built (and signed) from the request. */
    info(request: ImgproxyInfoUrlRequest, opts?: CallOptions): Promise<InfoResponse>;
    /** GET a prebuilt `/info/…` path or URL. */
    infoUrl(url: string, opts?: CallOptions): Promise<InfoResponse>;
    /**
     * GET (or HEAD) a processed image, with the URL built and signed from the
     * request. Resolves with the 2xx Response, body unread; throws
     * XtandardError otherwise.
     */
    image(request: ImgproxyUrlRequest, opts?: MediaOptions): Promise<Response>;
    /**
     * GET (or HEAD) a `/video` result: video, audio or a waveform picture.
     * `range` asks for part of it (206); `raw: true` in the options streams
     * the source unmodified.
     */
    video(request: ImgproxyVideoUrlRequest, opts?: MediaOptions): Promise<Response>;
    /** GET an HLS or DASH manifest (`/video/…@m3u8|mpd`). */
    stream(request: ImgproxyStreamUrlRequest, opts?: CallOptions): Promise<StreamManifest>;
    /**
     * GET (or HEAD) any URL or server path: a prebuilt image or video URL, a
     * query dialect URL, or a stream part (`/_stream/<ticket>/v0/1.m4s`).
     * A query dialect URL goes out as QUERY with `{ method: "QUERY" }`, or
     * by default with the client's `queryMethod`.
     */
    media(url: string, opts?: MediaOptions): Promise<Response>;
    /** GET the playground's `api/meta` (needs XI_PLAYGROUND_ENABLED; on the admin listener by default). */
    playgroundMeta(opts?: CallOptions): Promise<PlaygroundMeta>;
}
/**
 * Creates a client.
 *
 * ```ts
 * const xi = createClient({ baseUrl: "http://localhost:8080", imgproxy: { signing: { key, salt } } });
 * const info = await xi.info({ source: "s3://bucket/cat.jpg", options: { size: true, format: true } });
 * const head = await xi.video({ source: "s3://bucket/talk.mov", options: { vd: 5 } }, { method: "HEAD" });
 * ```
 */
export declare function createClient(options: XtandardClientOptions): XtandardClient;
