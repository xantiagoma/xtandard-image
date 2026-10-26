// Generates corpus.json: wsrv.nl URLs built by unpic's wsrv provider
// (MIT, src/providers/wsrv.ts) from operation objects, including every
// case of its wsrv.test.ts, for the parser's compatibility tests
// (docs/URL-DIALECTS.md §14).
//
//   bun internal/dialect/wsrv/testdata/unpic/generate.ts \
//     > internal/dialect/wsrv/testdata/unpic/corpus.json
//
// UNPIC points at a checkout of unpic (default: the .refs clone in the
// main checkout or $XI_REFS, scripts/clone-refs.sh).

import { $ } from "bun";

const refs = process.env.XI_REFS ?? `${(await $`git rev-parse --path-format=absolute --git-common-dir`.text()).trim()}/../.refs`;
const root = process.env.UNPIC ?? `${refs}/unpic`;
const { generate, transform, extract } = await import(`${root}/src/providers/wsrv.ts`);

const img = "https://example.com/image.jpg";

type Case = { name: string; url: string };

const gen = (name: string, src: string, ops: Record<string, unknown>): Case => ({
  name,
  url: generate(src, ops),
});
const tr = (name: string, url: string, ops: Record<string, unknown>): Case => ({
  name,
  url: transform(url, ops),
});

const cases: Case[] = [
  // wsrv.test.ts "wsrv extract"
  { name: "extract_basic", url: "https://wsrv.nl/?url=example.com/image.jpg" },
  {
    name: "extract_operations",
    url: "https://wsrv.nl/?url=example.com/image.jpg&w=300&h=200&q=85&output=webp&fit=cover",
  },
  // wsrv.test.ts "wsrv generate"
  gen("generate_width_height", img, { width: 300, height: 200 }),
  gen("generate_width_only", img, { width: 300 }),
  gen("generate_quality", img, { width: 600, quality: 80 }),
  gen("generate_format", img, { width: 400, format: "webp" }),
  gen("generate_fit_contain", img, { width: 300, fit: "contain" }),
  gen("generate_provider_ops", img, { width: 300, dpr: 2, blur: 5 }),
  // wsrv.test.ts "wsrv transform"
  tr("transform_width_height", img, { width: 200, height: 100 }),
  tr("transform_width_only", img, { width: 200 }),
  tr("transform_defaults", "https://wsrv.nl/?url=example.com/image.jpg&w=300", {}),
  tr("transform_override", "https://wsrv.nl/?url=example.com/image.jpg&w=300&fit=inside", { fit: "contain" }),
  // Every documented WsrvOperations field.
  gen("ops_aliases", img, { w: 320, h: 240, output: "png", q: 70 }),
  gen("ops_fit_inside_we", img, { width: 300, height: 300, fit: "inside", we: true }),
  gen("ops_fit_outside", img, { width: 300, height: 300, fit: "outside" }),
  gen("ops_fit_fill", img, { width: 300, height: 300, fit: "fill" }),
  gen("ops_contain_cbg", img, { width: 300, height: 300, fit: "contain", cbg: "black" }),
  gen("ops_align_attention", img, { width: 300, height: 300, a: "attention" }),
  gen("ops_align_entropy", img, { width: 300, height: 300, a: "entropy" }),
  gen("ops_align_bottom_right", img, { width: 300, height: 300, a: "bottom-right" }),
  gen("ops_crop", img, { crop: "100,100,720,530" }),
  gen("ops_trim", img, { width: 300, trim: 10 }),
  gen("ops_mask", img, { width: 300, height: 300, mask: "circle", mbg: "red" }),
  gen("ops_flip_flop", img, { width: 300, flip: true, flop: true }),
  gen("ops_rotate", img, { width: 300, ro: 90 }),
  gen("ops_bg", img, { width: 300, bg: "white" }),
  gen("ops_adjust", img, { width: 300, con: 25, sat: 2, hue: 90, sharp: 3, tint: "red" }),
  gen("ops_filters", img, { width: 300, filt: "greyscale" }),
  gen("ops_duotone", img, { width: 300, filt: "duotone" }),
  gen("ops_png", img, { width: 300, format: "png", l: 9, il: true }),
  gen("ops_webp_lossless", img, { width: 300, format: "webp", ll: true }),
  gen("ops_gif_pages", img, { height: 300, format: "gif", n: -1 }),
  gen("ops_formats_avif", img, { width: 640, format: "avif" }),
  gen("ops_formats_jpeg", img, { width: 640, format: "jpeg" }),
  gen("ops_formats_tiff", img, { width: 640, format: "tiff" }),
  gen("ops_formats_json", img, { format: "json" }),
  gen("ops_http_source", "http://example.com/a/b.png?v=2", { width: 128 }),
];

const corpus = cases.map((c) => {
  const e = extract(c.url);
  if (!e) throw new Error(`unpic cannot extract ${c.url}`);
  return { name: c.name, url: c.url, source: String(e.src) };
});
console.log(JSON.stringify(corpus, null, 2));
