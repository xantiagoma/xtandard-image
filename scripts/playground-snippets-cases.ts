// Builder states and the reference URLs of the playground snippet checks
// (WP-145), shared by scripts/playground-snippets-check.ts (runs every
// language's snippet with its toolchain) and scripts/playground-snippets.test.ts
// (bun test: the generator's invariants and the TypeScript snippets).
import { join, resolve } from "node:path";
// @ts-expect-error: plain ES module without types
import { build } from "../internal/playground/assets/grammar.js";
// @ts-expect-error: plain ES module without types
import { infoModel, snippetModel } from "../internal/playground/assets/snippets/index.js";
import { imgproxy, queryMethodRequest, queryUrls, signXiUrl } from "../clients/typescript/src/index.ts";

export const ROOT = resolve(import.meta.dir, "..");
export const CLIENTS = join(ROOT, "clients");

const conformance = await Bun.file(join(CLIENTS, "spec/conformance.json")).json();
const spec = await Bun.file(join(CLIENTS, "spec/spec.json")).json();
export const KEYS = conformance.keys;
export const ENV = {
  XI_SIGNING_KEY: KEYS.signing.key,
  XI_SIGNING_SALT: KEYS.signing.salt,
  XI_ENCRYPTION_KEY: KEYS.encryption.key,
  XI_ENCRYPTION_IV: KEYS.encryption.iv,
  XI_PDF_PASSWORD: "s3cret pdf",
};
export const BASE = "http://localhost:8080";

// ---- catalogues and mounts (the shapes api/meta serves) ----------------------------

type Opt = { name: string; aliases?: string[]; forms: { args: { type: string }[]; rest?: { type: string }[] }[] };
function indexOf(options: Opt[]): Map<string, Opt> {
  const m = new Map<string, Opt>();
  for (const o of options) m.set(o.name, o);
  for (const o of options) for (const a of o.aliases ?? []) if (!m.has(a)) m.set(a, o);
  return m;
}
const processing = indexOf(spec.options.processing);
const video = indexOf(spec.options.video);
const info = indexOf(spec.options.info);
// The query catalogue: native keys with one string value (as api/meta).
const queryIndex = indexOf(["width", "height", "fit", "gravity", "quality", "format", "blur"].map((name) => ({ name, aliases: [], forms: [{ args: [{ type: "string" }] }] })));

const MOUNTS = {
  imgproxy: { id: "imgproxy", grammar: "imgproxy", scheme: "imgproxy", prefix: "/", arguments_separator: ":" },
  query: { id: "query", grammar: "query", scheme: "xi_sig", prefix: "/q/" },
  cloudflare: { id: "cloudflare", grammar: "cloudflare", scheme: "xi_sig", prefix: "/cdn-cgi/image/", source: "local" },
} as const;

const entry = (name: string, ...values: string[]) => ({ name, form: 0, groups: 1, values, on: true });
const state = (s: Record<string, unknown>) => ({ source: "", pipelines: [[]], encoding: "plain", ext: "", alias: false, raw: false, video: false, free: { path: "", query: "" }, edits: "", ...s });

export type Case = { name: string; what: string; model: () => any };
export const CASES: Case[] = [
  {
    name: "image-chained", what: "processing, chained pipelines, plain source, nulls, signed",
    model: () => session(MOUNTS.imgproxy, state({
      source: "https://example.com/images/cat photo.jpg",
      pipelines: [[entry("resize", "fill", "300", "", "1"), entry("gravity", "sm"), entry("quality", "80")], [entry("blur", "2.5")]],
      ext: "webp",
    }), processing, { signed: true }),
  },
  {
    name: "image-enc", what: "processing, enc/ source (fixed IV), signed",
    model: () => session(MOUNTS.imgproxy, state({ source: "s3://photos/private/cat.jpg", pipelines: [[entry("width", "300"), entry("sharpen", "0.5")]] }), processing, { signed: true, encrypt: true }),
  },
  {
    name: "image-base64", what: "processing, base64 source, watermark, unsigned",
    model: () => session(MOUNTS.imgproxy, state({
      source: "xi://local/jpeg/baseline.jpg", encoding: "base64",
      pipelines: [[entry("resize", "fit", "640", "480"), entry("watermark", "0.5", "soea", "10", "10")]], ext: "png",
    }), processing, { signed: false }),
  },
  {
    name: "image-escape", what: "string escaping: quotes, $, #{, \\(, backslash, non-ASCII in the source and a text watermark",
    model: () => session(MOUNTS.imgproxy, state({
      source: "https://example.com/it's \"q\" $HOME #{x} \\(y) \\n ünï.jpg",
      pipelines: [[entry("watermark_text", "© \"$x\" #{y} \\(z)"), entry("format", "png")]],
    }), processing, { signed: true }),
  },
  {
    name: "image-pdf", what: "processing, PDF password (encrypted), plain",
    model: () => session(MOUNTS.imgproxy, state({ source: "xi://local/pdf/protected.pdf", pipelines: [[entry("page", "2"), entry("width", "800")]], ext: "png" }), processing, { signed: true, pdf: true }),
  },
  {
    name: "info", what: "/info with options, signed",
    model: () => infoModel({ m: MOUNTS.imgproxy, source: "xi://local/jpeg/baseline.jpg", entries: [entry("size", "1"), entry("format", "1"), entry("blurhash", "4", "3")], index: info, base: BASE, signed: true, url: `${BASE}/info/insecure/s:1/f:1/blurhash:4:3/plain/xi://local/jpeg/baseline.jpg` }),
  },
  {
    name: "video", what: "/video cut, resize, mute, webm, signed",
    model: () => session(MOUNTS.imgproxy, state({
      source: "xi://local/video/bbb-h264-aac.mp4", video: true, ext: "webm",
      pipelines: [[entry("start", "1"), entry("duration", "3.5"), entry("resize", "fill", "640", "360"), entry("mute", "1")]],
    }), video, { signed: true }),
  },
  {
    name: "stream-hls", what: "HLS renditions, segment duration, ts segments, signed",
    model: () => session(MOUNTS.imgproxy, state({
      source: "xi://local/video/bbb-h264-aac.mp4", video: true, ext: "m3u8",
      pipelines: [[entry("start", "0"), { ...entry("stream", "hls", "360", "720"), groups: 2 },entry("segment_duration", "4"), entry("segment_format", "ts")]],
    }), video, { signed: true }),
  },
  {
    name: "query-image", what: "/q native keys, unsigned",
    model: () => session(MOUNTS.query, state({ source: "https://example.com/a b.jpg", pipelines: [[entry("width", "300"), entry("fit", "cover"), entry("gravity", "fp:0.5:0.25")]] }), queryIndex, { signed: false }),
  },
  {
    name: "query-video", what: "/q/video with crop/flip aliases and format, signed",
    model: () => session(MOUNTS.query, state({
      source: "xi://local/video/bbb-h264-aac.mp4", video: true,
      pipelines: [[entry("start", "1"), entry("duration", "2"), entry("crop", "320", "240", "ce"), entry("flip", "1", "0"), entry("format", "webm")]],
    }), video, { signed: true }),
  },
  {
    name: "query-stream", what: "/q/video DASH stream, signed",
    model: () => session(MOUNTS.query, state({
      source: "xi://local/video/bbb-h264-aac.mp4", video: true,
      pipelines: [[entry("format", "mpd"), entry("segment_duration", "6")]],
    }), video, { signed: true }),
  },
  {
    name: "imaginary", what: "imaginary endpoint (raw mode on /q), signed",
    model: () => session(MOUNTS.query, state({ source: "https://example.com/cat.jpg", raw: true, free: { path: "/resize", query: "url={src}&width=300&height=200&type=webp" } }), queryIndex, { signed: true }),
  },
  {
    name: "query-method-image", what: "/q native keys sent as HTTP QUERY (WP-147), signed",
    model: () => session(MOUNTS.query, state({ source: "https://example.com/a b.jpg", pipelines: [[entry("width", "300"), entry("fit", "cover"), entry("format", "webp")]] }), queryIndex, { signed: true, queryMethod: true }),
  },
  {
    name: "query-method-video", what: "/q/video sent as HTTP QUERY, signed",
    model: () => session(MOUNTS.query, state({
      source: "xi://local/video/bbb-h264-aac.mp4", video: true,
      pipelines: [[entry("start", "1"), entry("duration", "2"), entry("format", "webm")]],
    }), video, { signed: true, queryMethod: true }),
  },
  {
    name: "xi-sig", what: "Cloudflare URL signed with signXiUrl",
    model: () => session(MOUNTS.cloudflare, state({ source: "xi://local/jpeg/baseline.jpg", pipelines: [[]] }), processing, { signed: true, url: `${BASE}/cdn-cgi/image/width=300,format=auto/jpeg/baseline.jpg` }),
  },
];

function session(m: any, st: any, index: Map<string, Opt>, o: { signed: boolean; encrypt?: boolean; pdf?: boolean; url?: string; queryMethod?: boolean }) {
  const req = build(m, st, index);
  const url = o.url ?? (req.error ? "" : `${BASE}${m.prefix.replace(/\/+$/, "")}${req.path}${req.query ? `?${req.query}` : ""}`);
  return { ...snippetModel({ m, state: st, index, base: BASE, signed: o.signed, encrypt: o.encrypt, pdf: o.pdf, url, queryMethod: o.queryMethod }), _playground: req.error ? null : { m, req } };
}

// ---- the reference: the TypeScript client, written from the model ------------------

const lit = (a: any) => (a.k === "null" ? null : a.k === "bool" ? a.v : a.k === "int" || a.k === "num" ? Number(a.v) : a.v);
const mapOf = (p: any[]) => Object.fromEntries(p.map((o) => [o.name, o.args.map(lit)]));
const paramsOf = (ps: any[]) => ps.map(([k, a]: [string, any]) => [k, lit(a)]);

export async function reference(model: any, optionNames: "short" | "canonical" = "short", placeholderSig = false): Promise<string> {
  if (model.queryMethod) {
    // An HTTP QUERY snippet prints the request on one line: "QUERY <url> <content>".
    const q = queryMethodRequest(await reference({ ...model, queryMethod: false }, optionNames, placeholderSig));
    return `${q.method} ${q.url} ${q.body}`;
  }
  const signing = model.signed && !placeholderSig ? { key: KEYS.signing.key, salt: KEYS.signing.salt } : undefined;
  const encryption = model.encryption ? { key: KEYS.encryption.key, iv: KEYS.encryption.iv } : undefined;
  const cfg: any = { baseUrl: model.baseUrl ?? model.base, signing, encryption, optionNames };
  if (model.separator) cfg.separator = model.separator;
  const pdfPassword = model.pdf ? ENV.XI_PDF_PASSWORD : undefined;
  switch (model.kind) {
    case "image": {
      const pls = model.pipelines.filter((p: any[], i: number) => p.length || i === 0).map(mapOf);
      return imgproxy({ ...cfg, sourceEncoding: model.sourceEncoding }).url({ source: model.source, options: pls.length > 1 ? pls : pls[0], extension: model.extension || undefined, pdfPassword } as any);
    }
    case "info": return imgproxy({ ...cfg, sourceEncoding: model.sourceEncoding }).infoUrl({ source: model.source, options: mapOf(model.options), pdfPassword } as any);
    case "video": return imgproxy({ ...cfg, sourceEncoding: model.sourceEncoding }).videoUrl({ source: model.source, options: mapOf(model.options), extension: model.extension || undefined } as any);
    case "stream": return imgproxy({ ...cfg, sourceEncoding: model.sourceEncoding }).streamUrl({ source: model.source, ...streamReq(model) } as any);
    case "q-image": return queryUrls({ ...cfg, mount: model.mount }).url({ source: model.source, params: paramsOf(model.params), pdfPassword } as any);
    case "q-video": return queryUrls({ ...cfg, mount: model.mount }).videoUrl({ source: model.source, options: mapOf(model.options), extension: model.extension || undefined } as any);
    case "q-stream": return queryUrls({ ...cfg, mount: model.mount }).streamUrl({ source: model.source, ...streamReq(model) } as any);
    case "imaginary": return queryUrls({ ...cfg, mount: model.mount }).imaginaryUrl({ endpoint: model.endpoint, source: model.source, params: paramsOf(model.params) } as any);
    case "xisig": return signXiUrl(model.url, { key: KEYS.signing.key, salt: KEYS.signing.salt }, { mount: model.mount });
    default: throw new Error(`no reference for ${model.kind}`);
  }
}
const streamReq = (m: any) => ({
  protocol: m.protocol,
  renditions: m.renditions.length ? m.renditions : undefined,
  segmentDuration: m.segmentDuration ?? undefined,
  segmentFormat: m.segmentFormat ?? undefined,
  options: m.options.length ? mapOf(m.options) : undefined,
});

// normalise drops what may differ between the page's URL and a client's for
// the same request: the signature segment, query-key order, percent-encoding
// and the order of path option segments.
export function normalise(u: string): string {
  const url = new URL(u);
  const segs = url.pathname.split("/").map((s) => decodeURIComponent(s));
  const sigAt = segs.findIndex((s, i) => i > 0 && (s === "insecure" || /^[A-Za-z0-9_-]{43}$/.test(s)));
  if (sigAt > 0) segs.splice(sigAt, 1);
  const q = [...url.searchParams].filter(([k]) => k !== "xi_sig").map(([k, v]) => `${k}=${v}`).sort();
  const last = segs.pop() ?? "";
  return `${url.origin}${[...segs.slice(0, 2), ...segs.slice(2).sort()].join("/")}/${last}?${q.join("&")}`;
}
