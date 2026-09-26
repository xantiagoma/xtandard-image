// Generates corpus.json: imgix URLs built by the official URL builder
// @imgix/js-core (BSD-2-Clause) from parameter objects, for the parser's
// compatibility tests (docs/URL-DIALECTS.md §13). Every URL is signed with
// the imgix-blueprint token "FOO123bar" and carries js-core's ixlib tag.
//
//   (cd $IMGIX_JS_CORE && npm install --omit=dev --no-save)   # js-base64, md5, ufo
//   bun internal/dialect/imgix/testdata/jscore/generate.ts \
//     > internal/dialect/imgix/testdata/jscore/corpus.json
//
// IMGIX_JS_CORE points at a checkout of imgix/js-core (default: the .refs
// clone next to the main checkout, scripts/clone-refs.sh).

const root = process.env.IMGIX_JS_CORE ?? "/Users/santi/Projects/xtandard-image/.refs/imgix-js-core";
const { default: ImgixClient } = await import(`${root}/src/index.js`);

const client = new ImgixClient({ domain: "assets.imgix.net", secureURLToken: "FOO123bar" });

type Case = { name: string; path: string; params: Record<string, unknown>; source: string };

const photo = "photos/cat.jpg";
const proxied = "https://example.com/images/dog.png?v=2";

const cases: Case[] = [
  { name: "plain", path: photo, params: {}, source: photo },
  { name: "size", path: photo, params: { w: 400, h: 300 }, source: photo },
  { name: "relative_width", path: photo, params: { w: 0.5 }, source: photo },
  { name: "fit_crop_faces", path: photo, params: { w: 400, h: 300, fit: "crop", crop: "faces,entropy" }, source: photo },
  { name: "fit_crop_compass", path: photo, params: { w: 400, h: 300, fit: "crop", crop: "top,left" }, source: photo },
  { name: "fit_crop_edges", path: photo, params: { w: 300, h: 300, fit: "crop", crop: "edges" }, source: photo },
  { name: "fit_crop_focalpoint", path: photo, params: { w: 600, ar: "16:9", fit: "crop", crop: "focalpoint", "fp-x": 0.3, "fp-y": 0.6, "fp-z": 2 }, source: photo },
  { name: "fit_min", path: photo, params: { w: 200, h: 100, fit: "min", auto: "format" }, source: photo },
  { name: "fit_max", path: photo, params: { w: 2000, fit: "max" }, source: photo },
  { name: "fit_clip", path: photo, params: { w: 640, h: 480, fit: "clip" }, source: photo },
  { name: "fit_scale", path: photo, params: { w: 100, h: 400, fit: "scale" }, source: photo },
  { name: "fit_fill", path: photo, params: { w: 500, h: 500, fit: "fill", "fill-color": "ff0000" }, source: photo },
  { name: "fit_fill_blur", path: photo, params: { w: 500, h: 300, fit: "fill", fill: "blur" }, source: photo },
  { name: "fit_fillmax", path: photo, params: { w: 500, h: 500, fit: "fillmax", bg: "80ffffff" }, source: photo },
  { name: "fit_clamp", path: photo, params: { w: 500, h: 500, fit: "clamp" }, source: photo },
  { name: "fit_facearea", path: photo, params: { w: 200, h: 200, fit: "facearea", facepad: 1.5, faceindex: 2 }, source: photo },
  { name: "dpr_quality", path: photo, params: { w: 320, dpr: 2, q: 60 }, source: photo },
  { name: "auto_format_compress", path: photo, params: { w: 800, auto: "format,compress" }, source: photo },
  { name: "auto_compress", path: photo, params: { auto: "compress" }, source: photo },
  { name: "auto_enhance", path: photo, params: { auto: "enhance,redeye" }, source: photo },
  { name: "fm_webp", path: photo, params: { w: 200, fm: "webp", q: 75 }, source: photo },
  { name: "fm_avif_lossless", path: photo, params: { fm: "avif", lossless: 1 }, source: photo },
  { name: "fm_pjpg", path: photo, params: { fm: "pjpg" }, source: photo },
  { name: "fm_png8", path: photo, params: { fm: "png8" }, source: photo },
  { name: "fm_json", path: photo, params: { fm: "json" }, source: photo },
  { name: "fm_blurhash", path: photo, params: { fm: "blurhash", w: 32 }, source: photo },
  { name: "fm_mp4", path: "videos/clip.gif", params: { fm: "mp4" }, source: "videos/clip.gif" },
  { name: "chromasub_cs_dpi", path: photo, params: { chromasub: 444, cs: "tinysrgb", dpi: 300 }, source: photo },
  { name: "rect_max_min", path: photo, params: { rect: "10,20,300,200", "max-w": 800, "max-h": 600, "min-w": 50, "min-h": 50 }, source: photo },
  { name: "orient_rot_flip", path: photo, params: { orient: 90, rot: 45, flip: "hv" }, source: photo },
  { name: "orient_exif", path: photo, params: { orient: 5 }, source: photo },
  { name: "adjust", path: photo, params: { bri: 20, con: -10, exp: 5, gam: 50, sat: -100, hue: 180, vib: 30 }, source: photo },
  { name: "stylize", path: photo, params: { blur: 200, px: 10, sharp: 25, usm: 40, usmrad: 3, sepia: 80 }, source: photo },
  { name: "monochrome_duotone", path: photo, params: { monochrome: "c0ff0000", duotone: "000080,ffff00", "duotone-alpha": 50 }, source: photo },
  { name: "invert", path: photo, params: { invert: true }, source: photo },
  { name: "bg_pad", path: photo, params: { bg: "red", pad: 10, "pad-left": 20 }, source: photo },
  { name: "border", path: photo, params: { border: "5,000000", "border-top": 10, "border-radius": "8,8,0,0" }, source: photo },
  { name: "trim_auto", path: photo, params: { trim: "auto", "trim-md": 20 }, source: photo },
  { name: "trim_color", path: photo, params: { trim: "color", "trim-color": "fff", "trim-tol": 12 }, source: photo },
  { name: "mask_ellipse", path: photo, params: { mask: "ellipse", "mask-bg": "00ff00" }, source: photo },
  { name: "mask_corners", path: photo, params: { mask: "corners", "corner-radius": 16 }, source: photo },
  { name: "page_frame", path: "docs/manual.pdf", params: { page: 3 }, source: "docs/manual.pdf" },
  { name: "frame", path: "anim/loop.gif", params: { frame: 2 }, source: "anim/loop.gif" },
  { name: "download", path: photo, params: { dl: "cat photo.jpg" }, source: photo },
  { name: "client_hints", path: photo, params: { ch: "Width,DPR,Save-Data", w: 300 }, source: photo },
  { name: "expires", path: photo, params: { w: 100, expires: 1893456000 }, source: photo },
  { name: "mark64", path: photo, params: { mark64: "https://assets.imgix.net/logo.png", "mark-align": "top,left", "mark-alpha": 60, "mark-scale": 20, "mark-pad": 8 }, source: photo },
  { name: "txt64", path: photo, params: { txt64: "Hello, World! <b>", "txt-font": "Avenir Next Demi,Bold", "txt-size": 32, "txt-color": "ffffff", "txt-align": "bottom,center", "txt-shad": 5 }, source: photo },
  { name: "bg64", path: photo, params: { bg64: "ff00ff", w: 50 }, source: photo },
  { name: "unknown_and_unsupported", path: photo, params: { w: 10, "bg-remove": true, blend: "ff0000", foo: "bar" }, source: photo },
  { name: "unicode_path", path: "photos/café au lait.jpg", params: { w: 100 }, source: "photos/caf%C3%A9%20au%20lait.jpg" },
  { name: "path_special_chars", path: "photos/a+b#c?d:e.jpg", params: {}, source: "photos/a%2Bb%23c%3Fd%3Ae.jpg" },
  { name: "web_proxy", path: proxied, params: { w: 400, fit: "crop" }, source: proxied },
  { name: "web_proxy_plain", path: "http://avatars.com/john-smith.png", params: {}, source: "http://avatars.com/john-smith.png" },
];

const out = cases.map((c) => {
  const u = new URL(client.buildURL(c.path, c.params));
  return { name: c.name, params: c.params, path: u.pathname, query: u.search.slice(1), source: c.source };
});
console.log(JSON.stringify(out, null, 2));
