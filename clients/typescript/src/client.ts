// A small typed fetch client for xtandard-image: the JSON and health
// endpoints (`/info`, `/healthz`, `/readyz`, the playground's `api/meta`)
// and media fetches (images, `/video` results with Range and HEAD, HLS and
// DASH manifests). Types come from the OpenAPI document
// (src/generated/openapi.ts); image and video URLs are built with the URL
// builder, since the imgproxy grammar is not expressible as OpenAPI path
// parameters (docs/OPENAPI.md).

import {
  buildImgproxyInfoUrl,
  buildImgproxyStreamUrl,
  buildImgproxyUrl,
  buildImgproxyVideoUrl,
  type ImgproxyInfoUrlRequest,
  type ImgproxyStreamUrlRequest,
  type ImgproxyUrlConfig,
  type ImgproxyUrlRequest,
  type ImgproxyVideoUrlRequest,
} from "./builder.ts";
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
  readonly playground?: { readonly baseUrl?: string; readonly path?: string };
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
export type ByteRange = string | { readonly start: number; readonly end?: number } | { readonly suffix: number };

/** Options of a media request (image, video, audio, stream part). */
export interface MediaOptions extends CallOptions {
  /** GET (default) or HEAD: HEAD answers the headers (type, length, ETag) without the body. */
  readonly method?: "GET" | "HEAD";
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
export function rangeHeader(r: ByteRange): string {
  if (typeof r === "string") return r;
  const int = (n: number, what: string): number => {
    if (!Number.isSafeInteger(n) || n < 0) throw new RangeError(`range ${what} ${n} is not a non-negative integer`);
    return n;
  };
  if ("suffix" in r) return `bytes=-${int(r.suffix, "suffix")}`;
  const start = int(r.start, "start");
  if (r.end === undefined) return `bytes=${start}-`;
  const end = int(r.end, "end");
  if (end < start) throw new RangeError(`range end ${end} is before start ${start}`);
  return `bytes=${start}-${end}`;
}

/**
 * A non-2xx response. `kind`, `message` and `requestId` come from the
 * server's JSON error envelope; a body that is not the envelope (a proxy's
 * error page, say) yields kind "unknown" and the status text.
 */
export class XtandardError extends Error {
  override readonly name = "XtandardError";
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

  constructor(status: number, url: string, body: ErrorResponse | undefined, fallback: string) {
    super(body?.error.message ?? (fallback || `HTTP ${status}`));
    this.status = status;
    this.url = url;
    this.body = body;
    this.kind = body?.error.kind ?? "unknown";
    this.requestId = body?.error.request_id;
  }
}

function isErrorResponse(v: unknown): v is ErrorResponse {
  if (typeof v !== "object" || v === null) return false;
  const e = (v as { error?: unknown }).error;
  return (
    typeof e === "object" &&
    e !== null &&
    typeof (e as { kind?: unknown }).kind === "string" &&
    typeof (e as { message?: unknown }).message === "string"
  );
}

/** Converts a non-2xx response into an XtandardError. */
export async function toXtandardError(res: Response): Promise<XtandardError> {
  const text = await res.text().catch(() => "");
  let body: ErrorResponse | undefined;
  try {
    const parsed: unknown = JSON.parse(text);
    if (isErrorResponse(parsed)) body = parsed;
  } catch {
    // not JSON
  }
  return new XtandardError(res.status, res.url, body, text.trim() || res.statusText);
}

/** The client returned by createClient. */
export interface XtandardClient {
  /** GET /healthz: resolves while the process can serve HTTP. */
  healthz(opts?: CallOptions): Promise<void>;
  /** GET /readyz: the readiness state; 503 (starting, draining) is not an error. */
  readyz(opts?: CallOptions): Promise<{ ready: boolean; state: ReadyState }>;
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
export function createClient(options: XtandardClientOptions): XtandardClient {
  const base = options.baseUrl.replace(/\/+$/, "");
  const doFetch: Fetch = options.fetch ?? ((input, init) => globalThis.fetch(input, init));
  const builderConfig: ImgproxyUrlConfig = { ...options.imgproxy, baseUrl: base };
  const abs = (url: string): string =>
    /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(url) ? url : `${base}${url.startsWith("/") ? "" : "/"}${url}`;

  async function get(url: string, accept: string, opts: MediaOptions | undefined): Promise<Response> {
    const headers = new Headers(options.headers);
    new Headers(opts?.headers).forEach((v, k) => headers.set(k, v));
    headers.set("Accept", accept);
    if (opts?.range !== undefined) headers.set("Range", rangeHeader(opts.range));
    const init: RequestInit = { method: opts?.method ?? "GET", headers };
    if (opts?.signal) init.signal = opts.signal;
    return doFetch(url, init);
  }

  async function json<T>(url: string, opts: CallOptions | undefined): Promise<T> {
    const res = await get(url, "application/json", opts);
    if (!res.ok) throw await toXtandardError(res);
    return (await res.json()) as T;
  }

  async function media(url: string, opts: MediaOptions | undefined): Promise<Response> {
    const res = await get(url, opts?.accept ?? "*/*", opts);
    if (!res.ok) throw await toXtandardError(res);
    return res;
  }

  return {
    async healthz(opts) {
      const res = await get(`${base}/healthz`, "text/plain, application/json", opts);
      if (!res.ok) throw await toXtandardError(res);
      await res.text(); // "ok\n"; drain the body so the connection is reused
    },
    async readyz(opts) {
      const res = await get(`${base}/readyz`, "text/plain, application/json", opts);
      if (res.status !== 200 && res.status !== 503) throw await toXtandardError(res);
      const state = (await res.text()).trim();
      if (state !== "ready" && state !== "starting" && state !== "draining") {
        throw new XtandardError(res.status, res.url, undefined, `unexpected readiness state ${JSON.stringify(state)}`);
      }
      return { ready: res.status === 200 && state === "ready", state };
    },
    async info(request, opts) {
      return json<InfoResponse>(await buildImgproxyInfoUrl(request, builderConfig), opts);
    },
    infoUrl(url, opts) {
      return json<InfoResponse>(abs(url), opts);
    },
    image: async (request, opts) => media(await buildImgproxyUrl(request, builderConfig), opts),
    video: async (request, opts) => media(await buildImgproxyVideoUrl(request, builderConfig), opts),
    async stream(request, opts) {
      const url = await buildImgproxyStreamUrl(request, builderConfig);
      const type = request.protocol === "hls" ? "application/vnd.apple.mpegurl" : "application/dash+xml";
      const res = await get(url, `${type}, */*;q=0.5`, opts);
      if (!res.ok) throw await toXtandardError(res);
      return { url, contentType: res.headers.get("content-type") ?? type, body: await res.text() };
    },
    media: (url, opts) => media(abs(url), opts),
    playgroundMeta(opts) {
      const origin = (options.playground?.baseUrl ?? base).replace(/\/+$/, "");
      const path = (options.playground?.path ?? "/_playground/").replace(/^\/+|\/+$/g, "");
      return json<PlaygroundMeta>(`${origin}/${path}/api/meta`, opts);
    },
  };
}
