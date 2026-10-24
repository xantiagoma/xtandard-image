// imgproxy-dialect URL builder (docs/URL-DIALECTS.md §3.1):
//
//   {baseUrl}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
//   {baseUrl}/video/{signature}/{video option}/…/{source}   (§3.8: the
//       signature covers "/video" + the path after it)
//
// Sources are base64url (default), `plain/` (percent-escaped, `@ext`) or
// `enc/` (AES-CBC, see encrypt.ts); base64 and enc sources take `.ext`.
import { base64url, hasControlChar, utf8 } from "./bytes.js";
import { encryptPdfPassword, encryptSource } from "./encrypt.js";
import { entriesOf, formatEntry, infoCatalogue, processingCatalogue, videoCatalogue, } from "./options.js";
import { buildImaginary, buildQueryImage, buildQueryVideo, } from "./query.js";
import { sign, signImgproxyPath } from "./sign.js";
/**
 * The manifest container of each stream protocol. Typed against the
 * catalogue: a protocol added on the Go side fails to compile here until
 * it has its container.
 */
const streamManifests = { hls: "m3u8", dash: "mpd" };
/** The video options and container of a stream request. */
function streamParts(req) {
    const entries = pipelinesOf(req.options).flat();
    for (const [name] of entries) {
        const c = videoCatalogue.canonical.get(name);
        if (c === "stream" || c === "segment_duration" || c === "segment_format" || c === "format") {
            throw new TypeError(`stream requests set ${name} through protocol, renditions, segmentDuration and segmentFormat`);
        }
    }
    const ext = Object.hasOwn(streamManifests, req.protocol) ? streamManifests[req.protocol] : undefined;
    if (ext === undefined)
        throw new TypeError(`unknown stream protocol ${JSON.stringify(req.protocol)}`);
    if (req.renditions?.length)
        entries.push(["stream", [req.protocol, ...req.renditions]]);
    if (req.segmentDuration !== undefined)
        entries.push(["segment_duration", [req.segmentDuration]]);
    if (req.segmentFormat === "ts" && req.protocol !== "hls")
        throw new TypeError("MPEG-TS segments are HLS only");
    if (req.segmentFormat !== undefined && req.segmentFormat !== "fmp4")
        entries.push(["segment_format", [req.segmentFormat]]);
    return { entries, ext };
}
// Characters kept verbatim in a plain source: RFC 3986 unreserved, the
// sub-delims, ":" and "/". Everything else, notably "%", "@" (the extension
// separator), "?" and "#", is percent-encoded.
const plainKeep = /[A-Za-z0-9\-._~!$&'()*+,;=:/]/;
/** Percent-escapes a source for a `plain/` URL; the server path-unescapes it. */
export function escapePlainSource(source) {
    let out = "";
    for (const ch of source) {
        if (plainKeep.test(ch)) {
            out += ch;
            continue;
        }
        for (const b of utf8(ch))
            out += `%${b.toString(16).toUpperCase().padStart(2, "0")}`;
    }
    return out;
}
function checkExtension(ext) {
    if (!/^[A-Za-z0-9]+$/.test(ext))
        throw new TypeError(`invalid extension ${JSON.stringify(ext)}`);
    return ext;
}
function split(token, n) {
    if (!n || n <= 0 || token.length <= n)
        return token;
    const parts = [];
    for (let i = 0; i < token.length; i += n)
        parts.push(token.slice(i, i + n));
    return parts.join("/");
}
async function sourceSegment(cfg, source, encoding, ext) {
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
            if (!cfg.encryption)
                throw new Error("encrypted sources need an encryption key");
            const token = await encryptSource(source, cfg.encryption);
            return `enc/${split(token, cfg.splitSource)}${e ? `.${e}` : ""}`;
        }
        default:
            throw new TypeError(`unknown source encoding ${JSON.stringify(encoding)}`);
    }
}
function checkSeparator(sep) {
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
async function finish(cfg, endpoint, path) {
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
async function passwordSegment(cfg, password, sep, style) {
    if (!cfg.encryption)
        throw new Error("pdfPassword needs an encryption key");
    if (!cfg.signing)
        throw new Error("pdfPassword needs signing: the server refuses pdf_password on unsigned URLs");
    const token = await encryptPdfPassword(password, cfg.encryption);
    return `${style === "canonical" ? "pdf_password" : "pdp"}${sep}${token}`;
}
/** Builds a URL from option pipelines (entries) and a source. */
async function build(cfg, endpoint, pipelines, source, encoding, ext, pdfPassword) {
    const sep = checkSeparator(cfg.separator ?? ":");
    const style = cfg.optionNames ?? "short";
    const cat = endpoint === "info" ? infoCatalogue : endpoint === "video" ? videoCatalogue : processingCatalogue;
    // Empty pipelines (an unused chain()) are dropped: the server rejects them.
    const nonEmpty = pipelines.filter((p) => p.length > 0);
    if (endpoint !== "process" && nonEmpty.length > 1)
        throw new TypeError(`/${endpoint} URLs have no chained pipelines`);
    if (endpoint === "video" && pdfPassword !== undefined)
        throw new TypeError("/video URLs take no pdfPassword");
    const segments = [];
    if (pdfPassword !== undefined)
        segments.push(await passwordSegment(cfg, pdfPassword, sep, style));
    nonEmpty.forEach((p, i) => {
        if (i > 0)
            segments.push("-");
        for (const e of p)
            segments.push(formatEntry(cat, e, sep, style));
    });
    segments.push(await sourceSegment(cfg, source, encoding ?? cfg.sourceEncoding ?? "base64", ext));
    return finish(cfg, endpoint, `/${segments.join("/")}`);
}
function pipelinesOf(options) {
    if (options === undefined)
        return [[]];
    const list = Array.isArray(options) ? options : [options];
    return list.length === 0 ? [[]] : list.map(entriesOf);
}
/** Builds a processing URL from the plain object form. */
export function buildImgproxyUrl(request, config = {}) {
    return build(config, "process", pipelinesOf(request.options), request.source, request.sourceEncoding, request.extension, request.pdfPassword);
}
/** Builds an `/info` URL from the plain object form. */
export function buildImgproxyInfoUrl(request, config = {}) {
    return build(config, "info", pipelinesOf(request.options), request.source, request.sourceEncoding, undefined, request.pdfPassword);
}
/**
 * Builds a `/video` URL (video transformation, audio or waveform) from the
 * plain object form. The server serves it with XI_VIDEO_TRANSFORM_ENABLED
 * on the full or gpl image, signed by default.
 */
export function buildImgproxyVideoUrl(request, config = {}) {
    return build(config, "video", pipelinesOf(request.options), request.source, request.sourceEncoding, request.extension, 
    // Not in the type: an untyped caller's password is refused, not dropped.
    request.pdfPassword);
}
/** Builds an HLS or DASH stream manifest URL (`/video/…@m3u8`, `@mpd`). */
export async function buildImgproxyStreamUrl(request, config = {}) {
    const { entries, ext } = streamParts(request);
    return build(config, "video", [entries], request.source, request.sourceEncoding, ext);
}
/** Builds a native query dialect URL (`/q/?url=…`). */
export function buildQueryUrl(request, config = {}) {
    return buildQueryImage(config, pipelinesOf(request.options), request.source, request.params, request.extension, request.pdfPassword);
}
/** Builds a `/q/video` URL. */
export function buildQueryVideoUrl(request, config = {}) {
    return buildQueryVideo(config, pipelinesOf(request.options).flat(), request.source, request.extension);
}
/** Builds a `/q/video` HLS or DASH stream manifest URL (`format=m3u8|mpd`). */
export async function buildQueryStreamUrl(request, config = {}) {
    const { entries, ext } = streamParts(request);
    return buildQueryVideo(config, entries, request.source, ext);
}
/** Builds an imaginary endpoint URL (`/q/<endpoint>?url=…`). */
export function buildImaginaryUrl(request, config = {}) {
    return buildImaginary(config, request.endpoint, request.source, request.params);
}
class Chain {
    #run;
    #s;
    constructor(run, source) {
        this.#run = run;
        this.#s = { source, pipelines: [[]], params: [], encoding: undefined, ext: undefined, pdfPassword: undefined };
    }
    option(name, ...args) {
        this.#s.pipelines.at(-1).push([name, args]);
        return this;
    }
    options(options) {
        this.#s.pipelines.at(-1).push(...entriesOf(options));
        return this;
    }
    param(key, value) {
        this.#s.params.push([key, value]);
        return this;
    }
    chain() {
        this.#s.pipelines.push([]);
        return this;
    }
    extension(ext) {
        this.#s.ext = ext;
        return this;
    }
    encoding(encoding) {
        this.#s.encoding = encoding;
        return this;
    }
    pdfPassword(password) {
        this.#s.pdfPassword = password;
        return this;
    }
    build() {
        return this.#run(this.#s);
    }
}
// Per-option methods, installed once on a prototype per catalogue: they
// delegate to option(name, ...args), so the URL keeps the name as written.
function withOptionMethods(names) {
    class Methods extends Chain {
    }
    const reserved = new Set(Object.getOwnPropertyNames(Chain.prototype));
    for (const name of names) {
        if (reserved.has(name))
            throw new Error(`option ${name} collides with a builder method`);
        Object.defineProperty(Methods.prototype, name, {
            value(...args) {
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
function imgproxyRun(cfg, endpoint) {
    return (s) => build(cfg, endpoint, s.pipelines, s.source, s.encoding, s.ext, s.pdfPassword);
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
export function imgproxy(config = {}) {
    const cfg = Object.freeze({ ...config });
    return {
        config: cfg,
        url: (request) => buildImgproxyUrl(request, cfg),
        infoUrl: (request) => buildImgproxyInfoUrl(request, cfg),
        from: (source) => new ProcessingChain(imgproxyRun(cfg, "process"), source),
        info: (source) => new InfoChain(imgproxyRun(cfg, "info"), source),
        videoUrl: (request) => buildImgproxyVideoUrl(request, cfg),
        video: (source) => new VideoChain(imgproxyRun(cfg, "video"), source),
        streamUrl: (request) => buildImgproxyStreamUrl(request, cfg),
    };
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
export function queryUrls(config = {}) {
    const cfg = Object.freeze({ ...config });
    const image = (s) => buildQueryImage(cfg, s.pipelines, s.source, s.params, s.ext, s.pdfPassword);
    const video = (s) => {
        if (s.pipelines.filter((p) => p.length > 0).length > 1)
            throw new TypeError("/q/video URLs have no chained pipelines");
        return buildQueryVideo(cfg, s.pipelines.flat(), s.source, s.ext);
    };
    return {
        config: cfg,
        url: (request) => buildQueryUrl(request, cfg),
        from: (source) => new ProcessingChain(image, source),
        videoUrl: (request) => buildQueryVideoUrl(request, cfg),
        video: (source) => new VideoChain(video, source),
        streamUrl: (request) => buildQueryStreamUrl(request, cfg),
        imaginaryUrl: (request) => buildImaginaryUrl(request, cfg),
    };
}
