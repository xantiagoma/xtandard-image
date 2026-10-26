// Generates one fixture file per unpic provider (<provider>.json next to this
// script) for the dialect compatibility tests (docs/URL-DIALECTS.md §12-13).
//
// Every fixture is a URL unpic itself produced or accepts, together with
// what unpic's own extract() reads out of it: width, height, format,
// quality and the source image. The URLs come from
//
//   - the string literals in unpic's <provider>.test.ts (and
//     shopify.fixtures.json), with `${CONST}` templates of string constants
//     resolved, kept when unpic detects them as that provider;
//   - unpic's generate() over a fixed operations matrix, applied to every
//     base image found above.
//
// A URL is only kept when unpic's extract() parses it, so the expectation is
// always unpic's reading, never ours.
//
//   UNPIC_DIR=/path/to/unpic bun tools/compat/unpic/generate.ts
//
// UNPIC_DIR defaults to the .refs clone in the main checkout, or $XI_REFS
// (scripts/clone-refs.sh). unpic is MIT licensed.

import { $ } from "bun";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

const refs = process.env.XI_REFS ?? `${(await $`git rev-parse --path-format=absolute --git-common-dir`.text()).trim()}/../.refs`;
const unpicDir = process.env.UNPIC_DIR ?? `${refs}/unpic`;
const outDir = dirname(new URL(import.meta.url).pathname);
const providersDir = join(unpicDir, "src/providers");
const { getProviderForUrl } = await import(join(unpicDir, "src/detect.ts"));
const version = JSON.parse(readFileSync(join(unpicDir, "package.json"), "utf8")).version;

// How a provider carries the source image:
//   embedded: the source is a URL or path inside the URL (query parameter,
//             path segment); xi's source is unpic's src, with a relative
//             src's leading "/" removed (a key under the mount's source).
//   path:     the URL is the asset on the provider's origin; xi's source is
//             unpic's src path (and kept query) without the leading "/".
type Mode = "embedded" | "path";

type Provider = {
  // unpic's provider id; the fixture file is named after xi's provider name.
  unpic: string;
  mode: Mode;
  // unpic operation keys for width/height/format/quality when unpic's
  // extract() returns them unnormalised (extractFromURL providers).
  keys?: Partial<Record<"width" | "height" | "format" | "quality", string>>;
  // Accepts a literal from the test file as this provider's URL; default:
  // unpic detects the URL as this provider.
  accept?: (u: URL) => boolean;
  // generate() options.
  options?: Record<string, unknown>;
  // Extra base images for the generate() matrix.
  bases?: string[];
  // Lifts nested unpic operations into the four compared fields.
  lift?: (ops: Record<string, any>) => void;
};

const prefixed = (...prefixes: string[]) => (u: URL) => prefixes.some((p) => u.pathname.startsWith(p));

// Provider table: xi name -> how to read unpic. The seven providers xi
// implements as full dialects (imgix, imagekit, cloudinary, cloudflare,
// cloudflare_images, bunny, wsrv) are generated too, for their packages.
const providers: Record<string, Provider> = {
  appwrite: { unpic: "appwrite", mode: "path" },
  astro: { unpic: "astro", mode: "embedded", accept: prefixed("/_image"), bases: ["/static/moose.png"] },
  builderio: { unpic: "builder.io", mode: "path", keys: {} },
  cloudimage: { unpic: "cloudimage", mode: "embedded", options: { token: "demo" } },
  contentful: { unpic: "contentful", mode: "path", keys: { width: "w", height: "h", format: "fm", quality: "q" } },
  contentstack: { unpic: "contentstack", mode: "path", keys: {} },
  directus: { unpic: "directus", mode: "path", keys: {}, accept: prefixed("/assets/") },
  hygraph: { unpic: "hygraph", mode: "path" },
  imageengine: { unpic: "imageengine", mode: "path" },
  ipx: { unpic: "ipx", mode: "embedded", accept: prefixed("/_ipx/"), bases: ["/images/test.jpg", "https://example.com/images/test.jpg"] },
  keycdn: { unpic: "keycdn", mode: "path" },
  kontentai: { unpic: "kontent.ai", mode: "path" },
  netlify: { unpic: "netlify", mode: "embedded", accept: prefixed("/.netlify/images"), bases: ["/cappadocia.jpg"] },
  nextjs: { unpic: "nextjs", mode: "embedded", accept: prefixed("/_next/image"), bases: ["/_next/static/media/bunny.0e498116.jpg"] },
  scene7: { unpic: "scene7", mode: "path" },
  shopify: { unpic: "shopify", mode: "path" },
  storyblok: {
    unpic: "storyblok",
    mode: "path",
    lift: (ops) => {
      if (ops.filters?.quality !== undefined) ops.quality = ops.filters.quality;
    },
  },
  supabase: {
    unpic: "supabase",
    mode: "path",
    bases: ["https://example.supabase.co/storage/v1/object/public/images/photo.jpg"],
  },
  uploadcare: {
    unpic: "uploadcare",
    mode: "path",
    // unpic keeps -/preview/WxH/ as an opaque operation; it sizes the image.
    lift: (ops) => {
      if (ops.preview && ops.width === undefined && ops.height === undefined) {
        const [w, h] = String(ops.preview).split("x");
        if (w) ops.width = Number(w);
        if (h) ops.height = Number(h);
      }
    },
  },
  vercel: { unpic: "vercel", mode: "embedded", accept: prefixed("/_vercel/image"), bases: ["/image.jpg", "https://example.net/image.jpg"] },
  wordpress: { unpic: "wordpress", mode: "path", bases: ["https://i0.wp.com/example.com/wp-content/uploads/photo.jpg"] },

  // Full dialects (not the simple engine).
  bunny: { unpic: "bunny", mode: "path" },
  cloudflare: { unpic: "cloudflare", mode: "embedded", accept: prefixed("/cdn-cgi/image/") },
  cloudflare_images: { unpic: "cloudflare_images", mode: "path" },
  cloudinary: { unpic: "cloudinary", mode: "path" },
  imagekit: { unpic: "imagekit", mode: "path" },
  imgix: { unpic: "imgix", mode: "path" },
  wsrv: { unpic: "wsrv", mode: "embedded" },
};

// The operations matrix applied to every base image.
const matrix: Record<string, unknown>[] = [
  {},
  { width: 800 },
  { width: 800, height: 600 },
  { width: 400, height: 300, format: "webp", quality: 80 },
  { width: 1200, format: "avif", quality: 60 },
  { height: 500, format: "png" },
  { width: 640, format: "jpeg", quality: 75 },
];

type Want = { width?: number; height?: number; format?: string; quality?: number; source: string };
type Fixture = { url: string; origin: "test" | "generate"; want: Want };

const num = (v: unknown): number | undefined => {
  if (v === undefined || v === null || v === "") return undefined;
  const n = Number(v);
  return Number.isFinite(n) && n !== 0 ? n : undefined;
};

function want(p: Provider, extracted: any): Want {
  const ops: Record<string, any> = { ...extracted.operations };
  p.lift?.(ops);
  const k = p.keys ?? {};
  const pick = (name: "width" | "height" | "format" | "quality") => ops[name] ?? (k[name] ? ops[k[name]!] : undefined);
  const w: Want = { source: xiSource(p, String(extracted.src), extracted.operations ?? {}) };
  const width = num(pick("width"));
  const height = num(pick("height"));
  const quality = num(pick("quality"));
  const format = pick("format");
  if (width !== undefined) w.width = width;
  if (height !== undefined) w.height = height;
  if (quality !== undefined) w.quality = quality;
  if (format !== undefined && format !== "") w.format = String(format);
  return w;
}

// xiSource is the source xi resolves for unpic's src. In path mode, query
// parameters unpic parsed as operations are not part of the source even
// where unpic leaves them in src (shopify keeps the ones it does not know).
function xiSource(p: Provider, src: string, ops: Record<string, unknown>): string {
  const abs = /^[a-z][a-z0-9+.-]*:\/\//i.test(src);
  if (p.mode === "embedded") {
    return abs ? src : src.replace(/^\//, "");
  }
  const u = new URL(src, "http://n/");
  for (const k of Object.keys(ops)) u.searchParams.delete(k);
  return (u.pathname + u.search).replace(/^\//, "");
}

// literals returns the URL-looking string literals of a test file, with
// ${CONST} placeholders of string constants resolved.
function literals(file: string): string[] {
  const text = readFileSync(file, "utf8");
  const consts = new Map<string, string>();
  for (const m of text.matchAll(/const\s+([A-Za-z_]\w*)\s*=\s*["'`]([^"'`$]*)["'`]\s*;/g)) {
    consts.set(m[1], m[2]);
  }
  const out = new Set<string>();
  for (const m of text.matchAll(/"((?:https?:\/\/|\/)[^"\s]*)"|`((?:https?:\/\/|\/|\$\{)[^`]*)`/g)) {
    let s = m[1] ?? m[2];
    s = s.replace(/\$\{\s*([A-Za-z_]\w*)\s*\}/g, (all, name) => consts.get(name) ?? all);
    if (!s.includes("${") && (s.startsWith("/") || s.startsWith("http"))) out.add(s);
  }
  for (const c of consts.values()) {
    if (c.startsWith("/") || c.startsWith("http")) out.add(c);
  }
  return [...out];
}

const providerFiles = new Set(readdirSync(providersDir));
for (const [name, p] of Object.entries(providers)) {
  const mod = await import(join(providersDir, `${p.unpic}.ts`));
  const accept = p.accept ?? ((u: URL) => getProviderForUrl(u.href.startsWith("http://n/") ? u.pathname + u.search : u.href) === p.unpic);
  const extract = (url: string) => {
    try {
      return mod.extract(url, {});
    } catch {
      return null;
    }
  };
  const fixtures = new Map<string, Fixture>();
  const bases = new Set<string>(p.bases ?? []);
  const add = (url: string, origin: Fixture["origin"]) => {
    if (fixtures.has(url)) return;
    let u: URL;
    try {
      u = new URL(url, "http://n/");
    } catch {
      return;
    }
    if (!accept(u)) return;
    const ex = extract(url);
    if (!ex || !ex.src) return;
    const w = want(p, ex);
    // A bare CDN origin ("https://images.contentstack.io/?w=1") names no image.
    if (w.source === "") return;
    fixtures.set(url, { url, origin, want: w });
  };

  const testFile = `${p.unpic}.test.ts`;
  const candidates = providerFiles.has(testFile) ? literals(join(providersDir, testFile)) : [];
  if (providerFiles.has(`${p.unpic}.fixtures.json`)) {
    for (const f of JSON.parse(readFileSync(join(providersDir, `${p.unpic}.fixtures.json`), "utf8"))) {
      candidates.push(f.original, f.base);
    }
  }
  for (const c of candidates) add(c, "test");
  // The matrix runs over the sources unpic extracted from the test URLs and,
  // for providers without explicit bases, the test literals that are not
  // provider URLs (plain source images).
  for (const f of [...fixtures.values()]) bases.add(String(extract(f.url).src));
  if (!p.bases) {
    for (const c of candidates) if (!fixtures.has(c)) bases.add(c);
  }
  for (const base of [...bases]) {
    for (const ops of matrix) {
      let url: string;
      try {
        url = String(mod.generate(base, { ...ops }, { ...(p.options ?? {}) }));
      } catch {
        continue;
      }
      add(url, "generate");
    }
  }
  const list = [...fixtures.values()].sort((a, b) => (a.origin + a.url).localeCompare(b.origin + b.url));
  const doc = {
    provider: name,
    unpic: { provider: p.unpic, version },
    mode: p.mode,
    fixtures: list,
  };
  writeFileSync(join(outDir, `${name}.json`), JSON.stringify(doc, null, 2) + "\n");
  console.error(`${name}: ${list.length} fixtures`);
}

// _index.json lists every provider unpic supports and its fixture file, so
// the Go tests notice a provider unpic adds (xi: null until it has one).
const { SupportedProviders } = await import(join(unpicDir, "src/types.ts"));
const xiName = new Map(Object.entries(providers).map(([n, p]) => [p.unpic, n]));
const index = {
  unpic: version,
  providers: Object.keys(SupportedProviders).sort().map((id) => ({ unpic: id, xi: xiName.get(id) ?? null })),
};
writeFileSync(join(outDir, "_index.json"), JSON.stringify(index, null, 2) + "\n");
const missing = index.providers.filter((p) => p.xi === null).map((p) => p.unpic);
if (missing.length > 0) console.error(`unpic providers without a fixture file: ${missing.join(", ")}`);
