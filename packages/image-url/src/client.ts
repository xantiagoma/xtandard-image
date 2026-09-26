// A small typed fetch client for the JSON and health endpoints of
// xtandard-image: `/info`, `/healthz` and `/readyz`. Types come from the
// OpenAPI document (src/generated/openapi.ts); image URLs are built with
// the URL builder, since the imgproxy grammar is not expressible as OpenAPI
// path parameters (docs/OPENAPI.md).

import {
  buildImgproxyInfoUrl,
  type ImgproxyInfoUrlRequest,
  type ImgproxyUrlConfig,
} from "./builder.ts";
import type { components } from "./generated/openapi.ts";

export type { components, operations, paths } from "./generated/openapi.ts";

/** The `/info` response body. */
export type InfoResponse = components["schemas"]["InfoResponse"];
/** The JSON error envelope: `{"error":{"kind","message","request_id"}}`. */
export type ErrorResponse = components["schemas"]["ErrorResponse"];
/** The error kind (errs.Kind), one per row of the server's status table. */
export type ErrorKind = components["schemas"]["ErrorBody"]["kind"];
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
  /** URL builder settings for `/info` (signing, encryption, …); baseUrl is the client's. */
  readonly imgproxy?: Omit<ImgproxyUrlConfig, "baseUrl">;
}

/** Per-call options. */
export interface CallOptions {
  readonly signal?: AbortSignal;
  readonly headers?: HeadersInit;
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
}

/**
 * Creates a client.
 *
 * ```ts
 * const xi = createClient({ baseUrl: "http://localhost:8080", imgproxy: { signing: { key, salt } } });
 * const info = await xi.info({ source: "s3://bucket/cat.jpg", options: { size: true, format: true } });
 * ```
 */
export function createClient(options: XtandardClientOptions): XtandardClient {
  const base = options.baseUrl.replace(/\/+$/, "");
  const doFetch: Fetch = options.fetch ?? ((input, init) => globalThis.fetch(input, init));
  const builderConfig: ImgproxyUrlConfig = { ...options.imgproxy, baseUrl: base };

  async function get(url: string, accept: string, opts: CallOptions | undefined): Promise<Response> {
    const headers = new Headers(options.headers);
    new Headers(opts?.headers).forEach((v, k) => headers.set(k, v));
    headers.set("Accept", accept);
    const init: RequestInit = { method: "GET", headers };
    if (opts?.signal) init.signal = opts.signal;
    return doFetch(url, init);
  }

  async function json<T>(url: string, opts: CallOptions | undefined): Promise<T> {
    const res = await get(url, "application/json", opts);
    if (!res.ok) throw await toXtandardError(res);
    return (await res.json()) as T;
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
      return json<InfoResponse>(/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(url) ? url : `${base}${url.startsWith("/") ? "" : "/"}${url}`, opts);
    },
  };
}
