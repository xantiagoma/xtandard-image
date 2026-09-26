// Generates corpus.json: ImageKit URLs built by the official SDKs from
// transformation objects, for the parser's compatibility tests
// (docs/URL-DIALECTS.md §13).
//
//   - unsigned URLs: @imagekit/javascript buildSrc (imagekit-javascript,
//     src/url.ts, MIT);
//   - signed URLs (ik-s / ik-t): @imagekit/nodejs client.helper.buildSrc
//     (imagekit-nodejs, src/resources/helper.ts, Apache-2.0), the SDK that
//     implements URL signing, with a fixed clock and a dummy private key.
//
//   bun internal/dialect/imagekit/testdata/sdk/generate.ts \
//     > internal/dialect/imagekit/testdata/sdk/corpus.json
//
// IMAGEKIT_JS and IMAGEKIT_NODE point at checkouts of the two SDKs
// (default: the .refs clones next to the main checkout,
// scripts/clone-refs.sh).

const refs = "/Users/santi/Projects/xtandard-image/.refs";
const jsRoot = process.env.IMAGEKIT_JS ?? `${refs}/imagekit-javascript`;
const nodeRoot = process.env.IMAGEKIT_NODE ?? `${refs}/imagekit-nodejs`;
const { buildSrc } = await import(`${jsRoot}/src/index.ts`);
const { Helper } = await import(`${nodeRoot}/src/resources/helper.ts`);

// Signing material shared with the Go tests (signing_test.go).
const privateKey = "private_xtandard_test_key";
const nowSeconds = 1790000000; // 2026-09-21T14:13:20Z, before the Go clock
Date.prototype.getTime = function () {
  return nowSeconds * 1000;
};
const signer = new Helper({ privateKey } as any);

const endpoint = "https://ik.imagekit.io/demo";
const src = "/folder/photo.jpg";

type Position = "path" | "query";
type Case = {
  name: string;
  src?: string;
  position?: Position;
  transformation?: Record<string, unknown>[];
  queryParameters?: Record<string, string | number>;
  signed?: boolean;
  expiresIn?: number;
};

const both = (name: string, transformation: Record<string, unknown>[], extra: Partial<Case> = {}): Case[] => [
  { name: name + "_path", position: "path", transformation, ...extra },
  { name: name + "_query", position: "query", transformation, ...extra },
];

const cases: Case[] = [
  { name: "plain" },
  { name: "unicode_src", src: "/sdk-testing-files/हिन्दी.png", transformation: [{ width: 300 }] },
  ...both("wh", [{ width: 300, height: 200 }]),
  ...both("chain", [{ width: 400, height: 300 }, { rotation: 90 }]),
  ...both("chain3", [{ width: 400 }, { blur: 10 }, { format: "webp", quality: 60 }]),
  ...both("relative_dims", [{ width: 0.5, height: 0.25 }]),
  ...both("aspect_ratio", [{ width: 400, aspectRatio: "4-3" }]),
  ...both("crop_force", [{ width: 300, height: 300, crop: "force" }]),
  ...both("crop_at_max", [{ width: 300, height: 300, crop: "at_max" }]),
  ...both("crop_at_max_enlarge", [{ width: 300, height: 300, crop: "at_max_enlarge" }]),
  ...both("crop_at_least", [{ width: 300, height: 300, crop: "at_least" }]),
  ...both("crop_maintain_ratio_focus", [{ width: 300, height: 300, crop: "maintain_ratio", focus: "top_left" }]),
  ...both("focus_auto", [{ width: 300, height: 300, focus: "auto" }]),
  ...both("focus_face_zoom", [{ width: 300, height: 300, focus: "face", zoom: 0.8 }]),
  ...both("focus_object", [{ width: 300, height: 300, focus: "car" }]),
  ...both("pad_resize", [{ width: 300, height: 200, cropMode: "pad_resize", background: "F3F3F3", focus: "left" }]),
  ...both("extract_focus", [{ width: 300, height: 200, cropMode: "extract", focus: "bottom_right" }]),
  ...both("extract_xy", [{ width: 300, height: 200, cropMode: "extract", x: 10, y: 20 }]),
  ...both("extract_center", [{ width: 300, height: 200, cropMode: "extract", xCenter: 400, yCenter: 300 }]),
  ...both("pad_extract", [{ width: 800, height: 800, cropMode: "pad_extract", background: "red" }]),
  ...both("dpr_quality_format", [{ width: 300, dpr: 2, quality: 80, format: "avif" }]),
  ...both("format_auto", [{ width: 300, format: "auto" }]),
  ...both("width_auto_dpr_auto", [{ width: "auto", dpr: "auto" }]),
  ...both("blur_sharpen", [{ blur: 10, sharpen: 10 }]),
  ...both("sharpen_default", [{ sharpen: true }]),
  ...both("usm", [{ unsharpMask: "2-2-0.8-0.024" }]),
  ...both("grayscale_contrast", [{ grayscale: true, contrastStretch: true }]),
  ...both("radius", [{ radius: 20 }]),
  ...both("radius_max", [{ radius: "max" }]),
  ...both("radius_corners", [{ radius: "20_40_80_160" }]),
  ...both("border", [{ border: "5_FFF000" }]),
  ...both("background_rgba", [{ background: "AAFF0040", width: 300, height: 300, cropMode: "pad_resize" }]),
  ...both("background_blurred", [{ background: "blurred", width: 300, height: 300, cropMode: "pad_resize" }]),
  ...both("rotate", [{ rotation: 180 }]),
  ...both("rotate_negative", [{ rotation: "N90" }]),
  ...both("rotate_any", [{ rotation: 45 }]),
  ...both("rotate_auto", [{ rotation: "auto" }]),
  ...both("flip", [{ flip: "h_v" }]),
  ...both("trim_true", [{ trim: true }]),
  ...both("trim_threshold", [{ trim: 15 }]),
  ...both("original", [{ original: true }]),
  ...both("flags", [{ progressive: true, lossless: true, metadata: true, colorProfile: false }]),
  ...both("opacity", [{ opacity: 50 }]),
  ...both("default_image", [{ defaultImage: "/folder/default.jpg" }]),
  ...both("named", [{ named: "thumb" }]),
  ...both("page", [{ page: 2 }]),
  ...both("overlay_image", [{
    width: 800,
    overlay: { type: "image", input: "logo.png", position: { x: 10, y: 20 }, transformation: [{ width: 100 }] },
  }]),
  ...both("overlay_image_path_focus", [{
    overlay: { type: "image", input: "/brand/logos/logo.png", position: { focus: "bottom_right" } },
  }]),
  ...both("overlay_image_base64", [{
    overlay: { type: "image", input: "/brand/logo (1).png", encoding: "base64" },
  }]),
  ...both("overlay_text", [{
    overlay: { type: "text", text: "Minimal Text", transformation: [{ fontSize: 20, fontColor: "FF0000" }] },
  }]),
  ...both("overlay_text_base64", [{ overlay: { type: "text", text: "Hello, world: ünïcode" } }]),
  ...both("overlay_solid_color", [{ overlay: { type: "solidColor", color: "FF0000" } }]),
  ...both("unsupported_ai", [{ width: 300, aiRemoveBackground: true, aiUpscale: true }]),
  { name: "query_parameters", position: "query", transformation: [{ width: 300 }], queryParameters: { v: "123" } },

  // Signed URLs (nodejs SDK).
  { name: "signed_plain", signed: true },
  { name: "signed_query", signed: true, position: "query", transformation: [{ width: 300, height: 200 }] },
  { name: "signed_path", signed: true, position: "path", transformation: [{ width: 300, height: 200 }, { rotation: 90 }] },
  { name: "signed_query_parameters", signed: true, position: "query", transformation: [{ width: 300 }], queryParameters: { version: "2.0" } },
  { name: "signed_unicode", signed: true, src: "/sdk-testing-files/हिन्दी.png" },
  { name: "signed_text_overlay", signed: true, position: "path", transformation: [{ overlay: { type: "text", text: "हिन्दी", transformation: [{ fontColor: "red", fontSize: 32 }] } }] },
  { name: "expiring_query", expiresIn: 300 * 24 * 3600, position: "query", transformation: [{ width: 300 }] },
  { name: "expiring_path", expiresIn: 300 * 24 * 3600, position: "path", transformation: [{ width: 300 }] },
  { name: "expired", expiresIn: 60, position: "query", transformation: [{ width: 300 }] },
];

const corpus = cases.map((c) => {
  const opts = {
    urlEndpoint: endpoint,
    src: c.src ?? src,
    transformation: c.transformation,
    transformationPosition: c.position,
    queryParameters: c.queryParameters,
  };
  const url = c.signed || c.expiresIn
    ? signer.buildSrc({ ...opts, signed: c.signed, expiresIn: c.expiresIn })
    : buildSrc(opts);
  if (!url) throw new Error(`${c.name}: SDK returned no URL`);
  return {
    name: c.name,
    endpoint,
    src: opts.src,
    position: c.position ?? "query",
    transformation: c.transformation,
    signed: Boolean(c.signed || c.expiresIn),
    url,
  };
});

console.log(JSON.stringify(corpus, null, 2));
