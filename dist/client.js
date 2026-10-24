// A small typed fetch client for xtandard-image: the JSON and health
// endpoints (`/info`, `/healthz`, `/readyz`, the playground's `api/meta`)
// and media fetches (images, `/video` results with Range and HEAD, HLS and
// DASH manifests). Types come from the OpenAPI document
// (src/generated/openapi.ts); image and video URLs are built with the URL
// builder, since the imgproxy grammar is not expressible as OpenAPI path
// parameters (docs/OPENAPI.md).
import { buildImgproxyInfoUrl, buildImgproxyStreamUrl, buildImgproxyUrl, buildImgproxyVideoUrl, } from "./builder.js";
import { queryMethodRequest } from "./query.js";
/** Formats a ByteRange as a Range header value. */
export function rangeHeader(r) {
    if (typeof r === "string")
        return r;
    const int = (n, what) => {
        if (!Number.isSafeInteger(n) || n < 0)
            throw new RangeError(`range ${what} ${n} is not a non-negative integer`);
        return n;
    };
    if ("suffix" in r)
        return `bytes=-${int(r.suffix, "suffix")}`;
    const start = int(r.start, "start");
    if (r.end === undefined)
        return `bytes=${start}-`;
    const end = int(r.end, "end");
    if (end < start)
        throw new RangeError(`range end ${end} is before start ${start}`);
    return `bytes=${start}-${end}`;
}
/**
 * A non-2xx response. `kind`, `message` and `requestId` come from the
 * server's JSON error envelope; a body that is not the envelope (a proxy's
 * error page, say) yields kind "unknown" and the status text.
 */
export class XtandardError extends Error {
    name = "XtandardError";
    /** HTTP status. */
    status;
    /** Error kind from the envelope, or "unknown". */
    kind;
    /** Request ID from the envelope (also in the server logs). */
    requestId;
    /** The parsed envelope, when the body was one. */
    body;
    /** The request URL. */
    url;
    constructor(status, url, body, fallback) {
        super(body?.error.message ?? (fallback || `HTTP ${status}`));
        this.status = status;
        this.url = url;
        this.body = body;
        this.kind = body?.error.kind ?? "unknown";
        this.requestId = body?.error.request_id;
    }
}
function isErrorResponse(v) {
    if (typeof v !== "object" || v === null)
        return false;
    const e = v.error;
    return (typeof e === "object" &&
        e !== null &&
        typeof e.kind === "string" &&
        typeof e.message === "string");
}
/** Converts a non-2xx response into an XtandardError. */
export async function toXtandardError(res) {
    const text = await res.text().catch(() => "");
    let body;
    try {
        const parsed = JSON.parse(text);
        if (isErrorResponse(parsed))
            body = parsed;
    }
    catch {
        // not JSON
    }
    return new XtandardError(res.status, res.url, body, text.trim() || res.statusText);
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
export function createClient(options) {
    const base = options.baseUrl.replace(/\/+$/, "");
    const doFetch = options.fetch ?? ((input, init) => globalThis.fetch(input, init));
    const builderConfig = { ...options.imgproxy, baseUrl: base };
    const abs = (url) => /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(url) ? url : `${base}${url.startsWith("/") ? "" : "/"}${url}`;
    const queryMount = (options.queryMount ?? "/q").replace(/\/+$/, "");
    /** Whether url is under the query dialect mount (for `queryMethod`). */
    const underQueryMount = (url) => {
        const path = new URL(url, "http://x").pathname;
        return path === queryMount || path.startsWith(`${queryMount}/`);
    };
    async function get(url, accept, opts) {
        const headers = new Headers(options.headers);
        new Headers(opts?.headers).forEach((v, k) => headers.set(k, v));
        headers.set("Accept", accept);
        if (opts?.range !== undefined)
            headers.set("Range", rangeHeader(opts.range));
        const init = { method: opts?.method ?? "GET", headers };
        if (opts?.signal)
            init.signal = opts.signal;
        if (opts?.method === "QUERY" || (opts?.method === undefined && options.queryMethod === true && underQueryMount(url))) {
            const q = queryMethodRequest(url);
            init.method = q.method;
            headers.set("Content-Type", q.contentType);
            init.body = q.body;
            return doFetch(q.url, init);
        }
        return doFetch(url, init);
    }
    async function json(url, opts) {
        const res = await get(url, "application/json", opts);
        if (!res.ok)
            throw await toXtandardError(res);
        return (await res.json());
    }
    async function media(url, opts) {
        const res = await get(url, opts?.accept ?? "*/*", opts);
        if (!res.ok)
            throw await toXtandardError(res);
        return res;
    }
    return {
        async healthz(opts) {
            const res = await get(`${base}/healthz`, "text/plain, application/json", opts);
            if (!res.ok)
                throw await toXtandardError(res);
            await res.text(); // "ok\n"; drain the body so the connection is reused
        },
        async readyz(opts) {
            const res = await get(`${base}/readyz`, "text/plain, application/json", opts);
            if (res.status !== 200 && res.status !== 503)
                throw await toXtandardError(res);
            const state = (await res.text()).trim();
            if (state !== "ready" && state !== "starting" && state !== "draining") {
                throw new XtandardError(res.status, res.url, undefined, `unexpected readiness state ${JSON.stringify(state)}`);
            }
            return { ready: res.status === 200 && state === "ready", state };
        },
        async info(request, opts) {
            return json(await buildImgproxyInfoUrl(request, builderConfig), opts);
        },
        infoUrl(url, opts) {
            return json(abs(url), opts);
        },
        image: async (request, opts) => media(await buildImgproxyUrl(request, builderConfig), opts),
        video: async (request, opts) => media(await buildImgproxyVideoUrl(request, builderConfig), opts),
        async stream(request, opts) {
            const url = await buildImgproxyStreamUrl(request, builderConfig);
            const type = request.protocol === "hls" ? "application/vnd.apple.mpegurl" : "application/dash+xml";
            const res = await get(url, `${type}, */*;q=0.5`, opts);
            if (!res.ok)
                throw await toXtandardError(res);
            return { url, contentType: res.headers.get("content-type") ?? type, body: await res.text() };
        },
        media: (url, opts) => media(abs(url), opts),
        playgroundMeta(opts) {
            const origin = (options.playground?.baseUrl ?? base).replace(/\/+$/, "");
            const path = (options.playground?.path ?? "/_playground/").replace(/^\/+|\/+$/g, "");
            return json(`${origin}/${path}/api/meta`, opts);
        },
    };
}
