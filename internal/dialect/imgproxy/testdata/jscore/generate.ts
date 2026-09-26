// Generates corpus.json: imgproxy URLs built by the official URL generator
// @imgproxy/imgproxy-js-core (MIT) from option objects, for the parser's
// compatibility tests (docs/URL-DIALECTS.md §13).
//
//   bun internal/dialect/imgproxy/testdata/jscore/generate.ts \
//     > internal/dialect/imgproxy/testdata/jscore/corpus.json
//
// IMGPROXY_JS_CORE points at a checkout of imgproxy-js-core (default:
// the .refs clone next to the main checkout, scripts/clone-refs.sh).

const root =
  process.env.IMGPROXY_JS_CORE ??
  "/Users/santi/Projects/xtandard-image/.refs/imgproxy-js-core";
const { generateUrl } = await import(`${root}/src/index.ts`);

type URLType = "plain" | "base64";
type Case = { name: string; source: string; type: URLType; ext?: string; options: Record<string, unknown> };

const b64 = (s: string) => Buffer.from(s).toString("base64url");

const src = "https://example.com/images/cat.jpg";
const s3 = "s3://bucket/path/to/key.png";

const cases: Case[] = [
  { name: "readme_example", source: src, type: "plain", options: {
    width: 150, height: 150, format: "webp", quality: 80, enlarge: "t",
    extend: { extend: 1, gravity: { type: "nowe", y_offset: 5 } }, blur: 5, zoom: 1.5 } },
  { name: "resize_full", source: src, type: "plain", options: {
    resize: { resizing_type: "fill", width: 300, height: 400, enlarge: 1, extend: { extend: 1, gravity: { type: "so" } } } } },
  { name: "resize_type_only", source: src, type: "plain", options: { resize: { resizing_type: "fit" } } },
  { name: "size_wh", source: src, type: "base64", options: { size: { width: 640, height: 480 } } },
  { name: "size_enlarge_extend", source: src, type: "base64", options: {
    size: { width: 640, height: 480, enlarge: "t", extend: { extend: true } } } },
  { name: "resizing_type_force", source: src, type: "plain", options: { resizing_type: "force", width: 50 } },
  { name: "resizing_algorithm", source: src, type: "plain", options: { resizing_algorithm: "lanczos2", w: 100 } },
  { name: "min_dims", source: src, type: "plain", options: { min_width: 200, min_height: 100, mw: undefined } },
  { name: "zoom_xy", source: src, type: "plain", options: { zoom: { zoom_x: 0.5, zoom_y: 2 } } },
  { name: "dpr_enlarge", source: src, type: "plain", options: { dpr: 2, enlarge: 1, width: 300 } },
  { name: "extend_gravity", source: src, type: "plain", options: {
    extend: { extend: 1, gravity: { type: "soea", x_offset: 10, y_offset: 20 } } } },
  { name: "extend_ar_fp", source: src, type: "plain", options: {
    extend_aspect_ratio: { extend: true, gravity: { type: "fp", x: 0.25, y: 0.75 } } } },
  { name: "gravity_smart", source: src, type: "plain", options: { resize: { resizing_type: "fill", width: 300, height: 300 }, gravity: { type: "sm" } } },
  { name: "gravity_fp", source: src, type: "plain", options: { gravity: { type: "fp", x: 0.5, y: 0.3 } } },
  { name: "gravity_compass_offsets", source: src, type: "plain", options: { gravity: { type: "we", x_offset: 5, y_offset: 0.1 } } },
  { name: "gravity_obj", source: src, type: "plain", options: { gravity: { type: "obj", class_names: ["face", "cat"] } } },
  { name: "gravity_objw", source: src, type: "plain", options: {
    gravity: { type: "objw", class_weights: [{ class: "face", weight: 2 }, { class: "dog", weight: 0.5 }] } } },
  { name: "crop_abs_gravity", source: src, type: "plain", options: { crop: { width: 100, height: 200, gravity: { type: "noea", x_offset: 3 } } } },
  { name: "crop_rel", source: src, type: "base64", options: { crop: { width: 0.5, height: 0.25 } } },
  { name: "trim_full", source: src, type: "plain", options: { trim: { threshold: 10, color: "ffaa00", equal_hor: 1, equal_ver: false } } },
  { name: "trim_threshold", source: src, type: "plain", options: { trim: { threshold: 2.5 } } },
  { name: "padding_number", source: src, type: "plain", options: { padding: 10 } },
  { name: "padding_object", source: src, type: "plain", options: { padding: { top: 1, right: 2, bottom: 3, left: 4 } } },
  { name: "padding_partial", source: src, type: "plain", options: { padding: { top: 5, right: 6 } } },
  { name: "auto_rotate_rotate_flip", source: src, type: "plain", options: { auto_rotate: false, rotate: 270, flip: { horizontal: true, vertical: false } } },
  { name: "background_hex", source: src, type: "plain", options: { background: "ff0080" } },
  { name: "background_rgb", source: src, type: "plain", options: { background: { r: 10, g: 20, b: 30 } } },
  { name: "background_alpha", source: src, type: "plain", options: { background: "fff", background_alpha: 0.5 } },
  { name: "effects", source: src, type: "plain", options: { blur: 2.5, sharpen: 0.7, pixelate: 8 } },
  { name: "watermark_full", source: src, type: "plain", options: { watermark: { opacity: 0.5, position: "soea", x_offset: 10, y_offset: 12, scale: 0.2 } } },
  { name: "watermark_replicate", source: src, type: "plain", options: { watermark: { opacity: 1, position: "re" } } },
  { name: "watermark_chessboard", source: src, type: "plain", options: { watermark: { opacity: 0.3, position: "ch", x_offset: 4 } } },
  { name: "metadata_flags", source: src, type: "plain", options: { strip_metadata: 0, keep_copyright: "t", strip_color_profile: false, enforce_thumbnail: 1, preserve_hdr: true } },
  { name: "quality_format_quality", source: src, type: "plain", options: { quality: 75, format_quality: [{ format: "jpeg", quality: 80 }, { format: "webp", quality: 70 }, { format: "avif", quality: 50 }] } },
  { name: "max_bytes_format", source: s3, type: "plain", options: { max_bytes: 100000, format: "avif" } },
  { name: "format_jpg_base64", source: src, type: "base64", options: { format: "jpg" } },
  { name: "skip_processing", source: src, type: "plain", options: { skip_processing: ["svg", "gif"] } },
  { name: "raw", source: src, type: "plain", options: { raw: true } },
  { name: "delivery", source: src, type: "plain", options: {
    cachebuster: "v42", expires: 4102444800, filename: { name: "my-cat", encoded: false }, return_attachment: 1 } },
  { name: "filename_encoded", source: src, type: "plain", options: { filename: { name: b64("chat résumé.jpg"), encoded: true } } },
  { name: "presets", source: src, type: "plain", options: { preset: ["sharp", "thumbnail"] } },
  { name: "disable_animation_pages", source: src, type: "plain", options: { disable_animation: 1, page: 2, pages: 3 } },
  { name: "later_options", source: src, type: "plain", options: {
    adjust: { brightness: 10, contrast: 1.2, saturation: 0.8 }, dpi: 300, jpeg_options: { progressive: true } } },
  { name: "wild_options", source: src, type: "plain", options: { wild_options: [["rs", "fit", 10, 10], ["custom", "a", "b"]] } },
  { name: "base64_s3", source: s3, type: "base64", options: { width: 100, height: 100, resizing_type: "fill-down" } },
  { name: "plain_ext", source: src, type: "plain", ext: "webp", options: { width: 200, format: "png" } },
  { name: "base64_ext", source: src, type: "base64", ext: "png", options: { height: 120, strip_metadata: true } },
  { name: "all_aliases_short", source: s3, type: "plain", options: {
    rs: { resizing_type: "auto", width: 10 }, g: { type: "ce" }, ex: { extend: 0 }, fn: { name: "x" }, exp: 0, pr: ["a"] } },
];

const out = cases.map(c => {
  const value = c.type === "base64" ? b64(c.source) : c.source;
  const opts = Object.fromEntries(Object.entries(c.options).filter(([, v]) => v !== undefined));
  let url: string = generateUrl({ value, type: c.type }, opts);
  if (c.ext) url += (c.type === "plain" ? "@" : ".") + c.ext;
  return { name: c.name, source: c.source, type: c.type, options: opts, path: url };
});

console.log(JSON.stringify(out, null, 2));
