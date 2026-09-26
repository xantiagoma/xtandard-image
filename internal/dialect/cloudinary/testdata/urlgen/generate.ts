// Generates corpus.json: Cloudinary delivery URLs built by the official
// SDKs, for the dialect's compatibility tests (docs/URL-DIALECTS.md §14):
//
//   - "urlgen": @cloudinary/url-gen 1.22.0 (MIT), the action builder API
//     (same version as the .refs/cloudinary-url-gen clone; its src/ needs
//     the transformation-builder-sdk package, so the published build is
//     used);
//   - "npm": cloudinary 2.11.0 (MIT, .refs/cloudinary-npm), cloudinary.url
//     with the legacy option objects, including private CDN, custom
//     domain (cname) and fetch URLs;
//   - "signed": cloudinary.url with sign_url, i.e. signature vectors computed
//     by the Node SDK itself (SHA-1 short, SHA-256 short via
//     signature_algorithm, SHA-256 long via long_url_signature).
//
// Run (network needed once for the packages):
//
//   DEPS=$(mktemp -d)
//   bun add --cwd "$DEPS" @cloudinary/url-gen@1.22.0 cloudinary@2.11.0
//   NODE_PATH="$DEPS/node_modules" bun \
//     internal/dialect/cloudinary/testdata/urlgen/generate.ts \
//     > internal/dialect/cloudinary/testdata/urlgen/corpus.json

import { Cloudinary } from "@cloudinary/url-gen";
import * as Resize from "@cloudinary/url-gen/actions/resize";
import * as Effect from "@cloudinary/url-gen/actions/effect";
import * as Adjust from "@cloudinary/url-gen/actions/adjust";
import * as Delivery from "@cloudinary/url-gen/actions/delivery";
import * as Rotate from "@cloudinary/url-gen/actions/rotate";
import * as RoundCorners from "@cloudinary/url-gen/actions/roundCorners";
import * as Border from "@cloudinary/url-gen/actions/border";
import * as Overlay from "@cloudinary/url-gen/actions/overlay";
import * as Underlay from "@cloudinary/url-gen/actions/underlay";
import * as PSDTools from "@cloudinary/url-gen/actions/psdTools";
import * as Named from "@cloudinary/url-gen/actions/namedTransformation";
import * as Gravity from "@cloudinary/url-gen/qualifiers/gravity";
import * as Compass from "@cloudinary/url-gen/qualifiers/compass";
import * as FocusOn from "@cloudinary/url-gen/qualifiers/focusOn";
import * as AutoFocus from "@cloudinary/url-gen/qualifiers/autoFocus";
import * as Flag from "@cloudinary/url-gen/qualifiers/flag";
import * as Quality from "@cloudinary/url-gen/qualifiers/quality";
import * as Format from "@cloudinary/url-gen/qualifiers/format";
import * as Source from "@cloudinary/url-gen/qualifiers/source";
import * as Mode from "@cloudinary/url-gen/qualifiers/rotationMode";
import * as Dpr from "@cloudinary/url-gen/qualifiers/dpr";
import * as ColorSpace from "@cloudinary/url-gen/qualifiers/colorSpace";
import * as AR from "@cloudinary/url-gen/qualifiers/aspectRatio";
import { Position } from "@cloudinary/url-gen/qualifiers/position";
import cloudinary from "cloudinary";

type Entry = { name: string; group: string; url: string; path: string; secret?: string; algorithm?: string };
const out: Entry[] = [];
const seen = new Set<string>();

function add(group: string, name: string, url: string, extra: Partial<Entry> = {}) {
	if (seen.has(name)) throw new Error("duplicate " + name);
	seen.add(name);
	const u = new URL(url);
	out.push({ name, group, url, path: u.pathname, ...extra });
}

// ---------------------------------------------------------------------------
// @cloudinary/url-gen

const cld = new Cloudinary({ cloud: { cloudName: "demo" }, url: { analytics: false } });
const img = (id = "folder/sample.jpg") => cld.image(id);
const g = (name: string, i: any) => add("urlgen", name, i.toURL());

// Resize: every crop mode.
g("resize_scale_w", img().resize(Resize.scale().width(300)));
g("resize_scale_wh", img().resize(Resize.scale().width(300).height(200)));
g("resize_scale_rel", img().resize(Resize.scale().width(0.5)));
g("resize_scale_ar", img().resize(Resize.scale().width(400).aspectRatio(AR.ar16X9())));
g("resize_scale_ar_float", img().resize(Resize.scale().width(400).aspectRatio(1.5)));
g("resize_fit", img().resize(Resize.fit().width(300).height(200)));
g("resize_limit", img().resize(Resize.limitFit().width(300).height(200)));
g("resize_mfit", img().resize(Resize.minimumFit().width(300).height(200)));
g("resize_fill", img().resize(Resize.fill().width(300).height(200)));
g("resize_fill_gravity_compass", img().resize(Resize.fill().width(300).height(200).gravity(Gravity.compass(Compass.northEast()))));
g("resize_fill_gravity_auto", img().resize(Resize.fill().width(300).height(200).gravity(Gravity.autoGravity())));
g("resize_fill_gravity_auto_subject", img().resize(Resize.fill().width(300).height(200).gravity(Gravity.autoGravity().autoFocus(AutoFocus.focusOn(FocusOn.cat())))));
g("resize_fill_gravity_face", img().resize(Resize.fill().width(300).height(200).gravity(Gravity.focusOn(FocusOn.face()))));
g("resize_fill_gravity_faces", img().resize(Resize.fill().width(300).height(200).gravity(Gravity.focusOn(FocusOn.faces()))));
g("resize_fill_gravity_object", img().resize(Resize.fill().width(300).height(200).gravity(Gravity.focusOn(FocusOn.dog()))));
g("resize_lfill", img().resize(Resize.limitFill().width(300).height(200)));
g("resize_fill_pad", img().resize(Resize.fillPad().width(300).height(200).gravity(Gravity.autoGravity())));
g("resize_pad", img().resize(Resize.pad().width(300).height(200)));
g("resize_pad_gravity", img().resize(Resize.pad().width(300).height(200).gravity(Gravity.compass(Compass.south()))));
g("resize_lpad", img().resize(Resize.limitPad().width(300).height(200)));
g("resize_mpad", img().resize(Resize.minimumPad().width(300).height(200)));
g("resize_crop_xy", img().resize(Resize.crop().width(150).height(100).x(20).y(30)));
g("resize_crop_gravity", img().resize(Resize.crop().width(150).height(100).gravity(Gravity.compass(Compass.southWest()))));
g("resize_crop_xy_center", img().resize(Resize.crop().width(0.5).height(0.5).gravity(Gravity.xyCenter()).x(0.25).y(0.75)));
g("resize_crop_rel", img().resize(Resize.crop().width(0.5).height(0.25)));
g("resize_crop_ar", img().resize(Resize.crop().aspectRatio(AR.ar4X3()).gravity(Gravity.autoGravity())));
g("resize_thumb_face_zoom", img().resize(Resize.thumbnail().width(150).height(150).gravity(Gravity.focusOn(FocusOn.face())).zoom(0.7)));
g("resize_auto", img().resize(Resize.auto().width(300).height(300)));
g("resize_auto_pad", img().resize(Resize.autoPad().width(300).height(300).gravity(Gravity.autoGravity())));
g("resize_imagga_scale", img().resize(Resize.imaggaScale().width(300)));
g("resize_imagga_crop", img().resize(Resize.imaggaCrop().width(300).height(200)));

// Effects and adjustments.
g("effect_blur", img().effect(Effect.blur()));
g("effect_blur_300", img().effect(Effect.blur().strength(300)));
g("effect_pixelate", img().effect(Effect.pixelate().squareSize(20)));
g("effect_grayscale", img().effect(Effect.grayscale()));
g("effect_blackwhite", img().effect(Effect.blackwhite()));
g("effect_sepia", img().effect(Effect.sepia()));
g("effect_negate", img().effect(Effect.negate()));
g("effect_colorize", img().effect(Effect.colorize(40).color("red")));
g("effect_vignette", img().effect(Effect.vignette()));
g("effect_cartoonify", img().effect(Effect.cartoonify()));
g("effect_background_removal", img().effect(Effect.backgroundRemoval()));
g("adjust_sharpen", img().adjust(Adjust.sharpen().strength(150)));
g("adjust_unsharp", img().adjust(Adjust.unsharpMask().strength(200)));
g("adjust_brightness", img().adjust(Adjust.brightness(30)));
g("adjust_contrast", img().adjust(Adjust.contrast(-20)));
g("adjust_saturation", img().adjust(Adjust.saturation(50)));
g("adjust_gamma", img().adjust(Adjust.gamma(40)));
g("adjust_hue", img().adjust(Adjust.hue(50)));
g("adjust_vibrance", img().adjust(Adjust.vibrance(70)));
g("adjust_red", img().adjust(Adjust.red(50)));
g("adjust_opacity", img().adjust(Adjust.opacity(60)));
g("adjust_auto_color", img().adjust(Adjust.autoColor()));

// Rotation, corners, border, background.
g("rotate_45", img().rotate(Rotate.byAngle(45)));
g("rotate_90", img().rotate(Rotate.byAngle(90)));
g("rotate_hflip", img().rotate(Rotate.mode(Mode.horizontalFlip())));
g("rotate_vflip", img().rotate(Rotate.mode(Mode.verticalFlip())));
g("rotate_ignore", img().rotate(Rotate.mode(Mode.ignore())));
g("rotate_auto_right", img().rotate(Rotate.mode(Mode.autoRight())));
g("round_20", img().roundCorners(RoundCorners.byRadius(20)));
g("round_4", img().roundCorners(RoundCorners.byRadius(10, 20, 30, 40)));
g("round_max", img().roundCorners(RoundCorners.max()));
g("border_solid", img().border(Border.solid(4, "black")));
g("border_rgb", img().border(Border.solid(10, "#ff0080")));
g("background_named", img().resize(Resize.pad().width(300).height(300)).backgroundColor("lightblue"));
g("background_hex", img().resize(Resize.pad().width(300).height(300)).backgroundColor("#00ff0080"));

// Delivery.
g("format_auto", img().delivery(Delivery.format(Format.auto())));
g("format_webp", img().delivery(Delivery.format(Format.webp())));
g("format_avif", img().delivery(Delivery.format(Format.avif())));
g("format_png", img().delivery(Delivery.format(Format.png())));
g("format_jpeg", img().delivery(Delivery.format(Format.jpeg())));
g("format_heif", img().delivery(Delivery.format(Format.heif())));
g("format_tiff", img().delivery(Delivery.format(Format.tiff())));
g("format_gif", img().delivery(Delivery.format(Format.gif())));
g("format_pdf", img().delivery(Delivery.format(Format.pdf())));
g("quality_80", img().delivery(Delivery.quality(80)));
g("quality_auto", img().delivery(Delivery.quality(Quality.auto())));
g("quality_auto_best", img().delivery(Delivery.quality(Quality.autoBest())));
g("quality_auto_good", img().delivery(Delivery.quality(Quality.autoGood())));
g("quality_auto_eco", img().delivery(Delivery.quality(Quality.autoEco())));
g("quality_auto_low", img().delivery(Delivery.quality(Quality.autoLow())));
g("quality_chroma", img().delivery(Delivery.quality(70).chromaSubSampling("420")));
g("quality_jpegmini", img().delivery(Delivery.quality(Quality.jpegmini())));
g("dpr_2", img().resize(Resize.scale().width(300)).delivery(Delivery.dpr(2)));
g("dpr_auto", img().resize(Resize.scale().width(300)).delivery(Delivery.dpr(Dpr.auto())));
g("colorspace_srgb", img().delivery(Delivery.colorSpace(ColorSpace.srgb())));
g("colorspace_cmyk", img().delivery(Delivery.colorSpace(ColorSpace.cmyk())));
g("default_image", img().delivery(Delivery.defaultImage("avatar.png")));
g("density", img().delivery(Delivery.density(150)));
g("page", img("doc.pdf").extract(PSDTools.getLayer().byIndex(2)));

// Flags.
g("flag_lossy", img().addFlag(Flag.lossy()));
g("flag_progressive", img().addFlag(Flag.progressive()));
g("flag_preserve_transparency", img().addFlag(Flag.preserveTransparency()));
g("flag_strip_profile", img().addFlag(Flag.stripProfile()));
g("flag_keep_iptc", img().addFlag(Flag.keepIptc()));
g("flag_keep_attribution", img().addFlag(Flag.keepAttribution()));
g("flag_force_strip", img().addFlag(Flag.forceStrip()));
g("flag_attachment", img().addFlag(Flag.attachment("my_photo")));
g("flag_attachment_plain", img().addFlag(Flag.attachment()));
g("flag_animated", img().addFlag(Flag.animated()));
g("flag_awebp", img().addFlag(Flag.animatedWebP()));
g("flag_apng", img().addFlag(Flag.animatedPng()));
g("flag_png8", img().addFlag(Flag.png8()));
g("flag_immutable_cache", img().addFlag(Flag.immutableCache()));
g("flag_getinfo", img().addFlag(Flag.getInfo()));
g("flag_any_format", img().addFlag(Flag.anyFormat()));
g("flag_rasterize", img().addFlag(Flag.rasterize()));

// Layers and named transformations.
g("overlay_image", img().overlay(Overlay.source(Source.image("logos:brand").transformation(
	new (await import("@cloudinary/url-gen/transformation/Transformation")).Transformation().resize(Resize.scale().width(100)))).position(
	new Position().gravity(Gravity.compass(Compass.northEast())).offsetX(10).offsetY(20))));
g("overlay_fetch", img().overlay(Overlay.source(Source.fetch("https://example.com/logo.png")).position(new Position().gravity(Gravity.compass(Compass.south())))));
g("overlay_tiled", img().overlay(Overlay.source(Source.image("logo")).position(new Position().tiled())));
g("overlay_text", img().overlay(Overlay.source(Source.text("Hello", "Arial_40"))));
g("underlay_image", img().underlay(Underlay.source(Source.image("paper"))));
g("named_transformation", img().namedTransformation(Named.name("thumb")));
g("chain_resize_effect_format", img()
	.resize(Resize.fill().width(400).height(300).gravity(Gravity.autoGravity()))
	.effect(Effect.blur().strength(200))
	.rotate(Rotate.byAngle(90))
	.delivery(Delivery.format(Format.auto()))
	.delivery(Delivery.quality(Quality.auto())));

// Delivery types and ids.
g("fetch_plain", cld.image("https://example.com/images/cat.jpg").setDeliveryType("fetch").resize(Resize.scale().width(300)));
g("fetch_query", cld.image("https://example.com/a b.jpg?x=1&y=2").setDeliveryType("fetch").delivery(Delivery.format(Format.auto())));
g("id_unicode", img("folder/café ü.jpg").resize(Resize.scale().width(100)));
g("id_version", img("sample.png").setVersion(1712345678).resize(Resize.scale().width(100)));
g("id_no_ext", img("folder/sample").resize(Resize.scale().width(100)));
g("private_type", img().setDeliveryType("private").resize(Resize.scale().width(100)));
g("authenticated_type", img().setDeliveryType("authenticated").resize(Resize.scale().width(100)));
{
	const priv = new Cloudinary({ cloud: { cloudName: "demo" }, url: { privateCdn: true, analytics: false } });
	add("urlgen", "private_cdn", priv.image("folder/sample.jpg").resize(Resize.fill().width(200).height(200)).toURL());
	const cname = new Cloudinary({ cloud: { cloudName: "demo" }, url: { secureDistribution: "images.example.com", analytics: false } });
	add("urlgen", "custom_domain", cname.image("folder/sample.jpg").resize(Resize.fill().width(200).height(200)).toURL());
}

// ---------------------------------------------------------------------------
// cloudinary (Node SDK), legacy option objects

cloudinary.v2.config({ cloud_name: "demo", api_key: "123", api_secret: "unused", secure: true });
const n = (name: string, id: string, opts: Record<string, unknown>) =>
	add("npm", name, cloudinary.v2.url(id, { urlAnalytics: false, ...opts }));

n("npm_basic", "sample.jpg", { width: 300, height: 200, crop: "fill", gravity: "auto" });
n("npm_chain", "folder/sample.jpg", { transformation: [{ width: 300, crop: "scale" }, { effect: "sharpen:200" }, { angle: -30 }], fetch_format: "auto", quality: "auto:eco" });
n("npm_flags_multi", "sample.jpg", { flags: ["progressive", "strip_profile", "immutable_cache"] });
n("npm_effects", "sample.jpg", { effect: "brightness:-40" });
n("npm_effect_trim", "sample.png", { effect: "trim:15" });
n("npm_effect_blur_faces", "sample.jpg", { effect: "blur_faces:600" });
n("npm_effect_pixelate_faces", "sample.jpg", { effect: "pixelate_faces:12" });
n("npm_effect_blur_region", "sample.jpg", { effect: "blur_region:800", x: 0.1, y: 0.2, width: 0.3, height: 0.4 });
n("npm_effect_unknown", "sample.jpg", { effect: "oil_paint:40" });
n("npm_border_obj", "sample.jpg", { border: { width: 3, color: "#00f" } });
n("npm_radius_list", "sample.jpg", { radius: [10, 20] });
n("npm_radius_three", "sample.jpg", { radius: [10, 20, 30] });
n("npm_opacity", "sample.jpg", { opacity: 50 });
n("npm_angle_list", "sample.jpg", { angle: ["auto_right", "hflip"] });
n("npm_background_rgb", "sample.jpg", { width: 300, height: 300, crop: "pad", background: "rgb:ff000080" });
n("npm_size", "sample.jpg", { size: "300x200", crop: "fit" });
n("npm_named_multi", "sample.jpg", { transformation: ["thumb", "watermarked"] });
n("npm_overlay_legacy", "sample.jpg", { overlay: "logo", gravity: "south_east", x: 5, y: 5, width: 0.2, flags: "relative", opacity: 60 });
n("npm_overlay_layer_apply", "sample.jpg", { transformation: [{ overlay: "logos:brand" }, { width: 80, crop: "scale" }, { flags: "layer_apply", gravity: "north_west", x: 10, y: 10 }] });
n("npm_overlay_fetch", "sample.jpg", { overlay: { url: "https://example.com/logo.png" }, gravity: "center" });
n("npm_underlay", "sample.jpg", { underlay: "paper" });
n("npm_default_image_folder", "sample.jpg", { default_image: "fallbacks:avatar.png" });
n("npm_page", "doc.pdf", { page: 3, fetch_format: "png" });
n("npm_zoom", "sample.jpg", { width: 200, height: 200, crop: "thumb", gravity: "faces", zoom: 1.5 });
n("npm_w_auto", "sample.jpg", { width: "auto:100:400", crop: "scale", responsive_width: false });
n("npm_dpr_float", "sample.jpg", { width: 100, crop: "scale", dpr: "1.5" });
n("npm_video_params", "sample.jpg", { start_offset: 2, video_codec: "auto" });
n("npm_variables", "sample.jpg", { variables: [["$size", 200]], width: "$size", crop: "scale" });
n("npm_if", "sample.jpg", { transformation: [{ if: "w_gt_1000", width: 1000, crop: "scale" }, { effect: "grayscale" }] });
n("npm_raw_transformation", "sample.jpg", { raw_transformation: "w_100,zz_1" });
n("npm_fetch", "https://example.com/img/cat.jpg", { type: "fetch", width: 150, crop: "fill", fetch_format: "auto" });
n("npm_private_cdn", "sample.jpg", { private_cdn: true, width: 100 });
n("npm_cname", "folder/sample.jpg", { cname: "images.example.com", secure: false, width: 100 });
n("npm_version", "folder/sample.jpg", { version: 1234, width: 100 });

// ---------------------------------------------------------------------------
// Signature vectors (Node SDK sign_url)

const secret = "a676b67565c6767a6767d6767f676fe1"; // test secret from the SDK's own specs
const s = (name: string, id: string, opts: Record<string, unknown>, algorithm: string) =>
	add("signed", name, cloudinary.v2.url(id, { urlAnalytics: false, sign_url: true, api_secret: secret, ...opts }), { secret, algorithm });

s("sig_sha1_basic", "sample.jpg", { width: 300, height: 200, crop: "fill" }, "sha1");
s("sig_sha1_version", "folder/sample.jpg", { version: 1234, width: 100, crop: "scale" }, "sha1");
s("sig_sha1_no_tx", "folder/sample.jpg", {}, "sha1");
s("sig_sha1_chain", "sample.png", { transformation: [{ width: 300, crop: "fill" }, { effect: "grayscale" }], fetch_format: "webp" }, "sha1");
s("sig_sha1_fetch", "https://example.com/a b.jpg?x=1", { type: "fetch", width: 300 }, "sha1");
s("sig_sha1_unicode", "folder/café.jpg", { width: 100 }, "sha1");
s("sig_sha1_private", "secret/photo.jpg", { type: "private", width: 100 }, "sha1");
s("sig_sha1_private_cdn", "sample.jpg", { private_cdn: true, width: 100 }, "sha1");
s("sig_sha256_short", "sample.jpg", { width: 300, crop: "scale", signature_algorithm: "sha256" }, "sha256");
s("sig_sha256_long", "sample.jpg", { width: 300, crop: "scale", long_url_signature: true }, "sha256_long");
s("sig_sha256_long_chain", "folder/sample.jpg", { transformation: [{ width: 300, crop: "fill" }, { effect: "blur:300" }], long_url_signature: true }, "sha256_long");

console.log(JSON.stringify(out, null, "\t"));
