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
const { generateUrl, generateImageInfoUrl } = await import(`${root}/src/index.ts`);

type URLType = "plain" | "base64";
type Case = {
  name: string; source: string; type: URLType; ext?: string; options: Record<string, unknown>;
  kind?: "info"; // generateImageInfoUrl; the path excludes the "/info" prefix
};

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
  // Pro options (M2/M3, WP-21b). js-core does not export watermark_rotate.
  { name: "pro_crop_ar_objects_position", source: src, type: "plain", options: {
    crop: { width: 100, height: 100 }, crop_aspect_ratio: { aspect_ratio: 1.5, enlarge: true }, objects_position: { type: "soea", xOffset: 5, yOffset: 6 } } },
  { name: "pro_objects_position_fp", source: src, type: "plain", options: { objects_position: { type: "fp", x: 0.2, y: 0.4 } } },
  { name: "pro_objects_position_prop", source: src, type: "plain", options: { op: { type: "prop" } } },
  { name: "pro_color_adjustments", source: src, type: "plain", options: {
    brightness: -20, contrast: 1.3, saturation: 0.5, monochrome: { intensity: 0.4, color: "aabbcc" },
    duotone: { intensity: 0.6, color1: "001122", color2: "ffeedd" } } },
  { name: "pro_adjust_partial", source: src, type: "plain", options: { adjust: { contrast: 1.1 } } },
  { name: "pro_colorize_gradient", source: src, type: "plain", options: {
    colorize: { opacity: 0.5, color: "ff0000", keepAlpha: true },
    gradient: { opacity: 0.8, color: "00ff00", direction: "up", start: 0.1, stop: 0.9 } } },
  { name: "pro_gradient_angle", source: src, type: "plain", options: { gr: { opacity: 1, direction: 45 } } },
  { name: "pro_blurs", source: src, type: "plain", options: {
    unsharp_masking: { mode: "always", weight: 1.5, divider: 20 },
    blur_areas: { sigma: 10, areas: [{ left: 0, top: 0, width: 0.5, height: 0.5 }, { left: 0.5, top: 0.5, width: 0.25, height: 0.25 }] },
    progressive_blur: { sigma: 5, direction: "left", start: 0.2, stop: 0.8 } } },
  { name: "pro_detections", source: src, type: "plain", options: {
    blur_detections: { sigma: 8, class_names: ["face", "license_plate"] },
    draw_detections: { draw: true, class_names: ["cat"] }, crop_objects: { scale_factor: 1.2, class_names: ["dog"] } } },
  { name: "pro_watermark", source: src, type: "plain", options: {
    watermark: { opacity: 0.7, position: "soea" }, watermark_url: b64("https://example.com/logo.png"),
    watermark_size: { width: 120, height: 40 }, watermark_shadow: 2 } },
  { name: "pro_watermark_text", source: src, type: "plain", options: {
    watermark: { opacity: 1 }, watermark_text: b64("<span foreground='red'>© ACME</span>") } },
  { name: "pro_style", source: src, type: "base64", options: { style: b64("svg { background: #fff }") } },
  { name: "pro_output", source: src, type: "plain", options: {
    dpi: 300, color_profile: "srgb", autoquality: { method: "dssim", target: 0.02, min_quality: 60, max_quality: 90, allowed_error: 0.001 },
    jpeg_options: { progressive: true, no_subsample: false, trellis_quant: true, overshoot_deringing: true, optimize_scans: true, quant_table: 3 },
    png_options: { interlaced: true, quantize: true, quantization_colors: 128 },
    webp_options: { compression: "near_lossless", smart_subsample: true, preset: "photo" }, avif_options: "off" } },
  { name: "pro_format_best", source: src, type: "plain", options: { format: "best" } },
  { name: "pro_video", source: src, type: "plain", options: {
    video_thumbnail_second: 5, video_thumbnail_keyframes: true,
    video_thumbnail_tile: { step: 2, columns: 3, rows: 3, tile_width: 160, tile_height: 90, extend_tile: true, trim: false, fill: true, focus_x: 0.5, focus_y: 0.25 } } },
  { name: "pro_video_animation", source: src, type: "plain", options: {
    video_thumbnail_animation: { step: -1, delay: 100, frames: 10, frame_width: 320, frame_height: 180 } } },
  { name: "pro_delivery", source: src, type: "plain", options: {
    fallback_image_url: b64("https://example.com/fallback.png"), bypass_cache: true, cache_tags: ["product-1", "catalog"],
    max_result_dimension: undefined } },
  // /info URLs (generateImageInfoUrl).
  { name: "info_defaults", source: src, type: "plain", kind: "info", options: {} },
  { name: "info_basic", source: src, type: "plain", kind: "info", options: {
    size: true, format: true, dimensions: true, exif: { enabled: true, canonical_names: false }, iptc: false, xmp: false, video_meta: false } },
  { name: "info_colors", source: src, type: "base64", kind: "info", options: {
    palette: 16, average: { average: true, ignore_transparent: false }, dominant_colors: { dominant_colors: true, build_missed: true } } },
  { name: "info_hashes", source: src, type: "plain", kind: "info", options: {
    blurhash: { x_components: 4, y_components: 3 }, thumb_hash: true, perceptual_hash: true, calcHashsums: ["md5", "sha256"] } },
  { name: "info_detection", source: src, type: "plain", kind: "info", options: {
    detect_objects: true, classify: { top_k: 3, class_names: ["cat", "dog"] }, crop: { width: 100, height: 100, gravity: { type: "sm" } } } },
  { name: "info_pages", source: s3, type: "plain", kind: "info", options: {
    pages_number: true, page: 2, video_thumbnail_second: 3, video_thumbnail_keyframes: false } },
  { name: "info_delivery", source: src, type: "base64", kind: "info", options: {
    cachebuster: "v1", expires: 4102444800, preset: ["info"], bypass_cache: true, cache_tags: ["i"] } },
];

const out = cases.map(c => {
  const value = c.type === "base64" ? b64(c.source) : c.source;
  const opts = Object.fromEntries(Object.entries(c.options).filter(([, v]) => v !== undefined));
  const gen = c.kind === "info" ? generateImageInfoUrl : generateUrl;
  let url: string = gen({ value, type: c.type }, opts);
  if (c.ext) url += (c.type === "plain" ? "@" : ".") + c.ext;
  return { name: c.name, source: c.source, type: c.type, ...(c.kind ? { kind: c.kind } : {}), options: opts, path: url };
});

console.log(JSON.stringify(out, null, 2));
