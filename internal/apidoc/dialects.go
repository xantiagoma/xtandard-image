package apidoc

import (
	"github.com/xantiagoma/xtandard-image/internal/playground"
)

// Builder groups (playground.Option.Group).
const (
	groupSize      = "size"
	groupGravity   = "gravity"
	groupFormat    = "format"
	groupColor     = "color"
	groupEffects   = "effects"
	groupWatermark = "watermark"
	groupMetadata  = "metadata"
	groupFrames    = "frames"
	groupDetection = "detection"
	groupDelivery  = "delivery"
	groupPresets   = "presets"
	groupOther     = "other"
	// Info panel groups (InfoOptions).
	groupReport   = "report"
	groupHashes   = "hashes"
	groupSecurity = "security"
)

// dparam is one parameter of a dialect's static catalogue: the playground
// builds URLs from it and TestDialectCataloguesParse checks that the
// dialect's parser knows every name (with its example value), so the
// tables cannot go stale.
type dparam struct {
	name    string
	typ     string // playground.Arg type
	enum    []string
	min     *float64
	max     *float64
	doc     string
	group   string
	example string // a valid value ("" for a flag without value)
}

func bound(lo, hi float64) (*float64, *float64) { return new(lo), new(hi) }

// dp builds a dparam; typ "flag" is a parameter without a value.
func dp(name, typ, group, example, doc string, enum ...string) dparam {
	return dparam{name: name, typ: typ, enum: enum, doc: doc, group: group, example: example}
}

func dpr(name, typ, group, example, doc string, lo, hi float64) dparam {
	p := dp(name, typ, group, example, doc)
	p.min, p.max = bound(lo, hi)
	return p
}

// dialectCatalogue turns a table into the playground catalogue of grammar.
func dialectCatalogue(grammar string, ps []dparam) playground.Catalogue {
	opts := make([]playground.Option, 0, len(ps))
	for _, p := range ps {
		o := playground.Option{Name: p.name, Doc: p.doc, Group: p.group, Example: p.example, Forms: []playground.Form{{Args: []playground.Arg{}}}}
		if p.typ != "flag" {
			o.Forms[0].Args = []playground.Arg{{Name: "value", Type: p.typ, Enum: p.enum, Min: p.min, Max: p.max, Doc: p.doc}}
		}
		opts = append(opts, o)
	}
	return playground.Catalogue{Grammar: grammar, Options: opts}
}

// cloudinaryParams: URL-DIALECTS §6. Names ending in an effect or flag
// ("e_blur", "fl_progressive") are written <name>:<value> (or bare); the
// others <name>_<value>.
//
//nolint:funlen // one flat table is the clearest form
func cloudinaryParams() []dparam {
	return []dparam{
		dp("w", "number", groupSize, "300", "Width in pixels; a decimal below 1 is relative (w_0.5)."),
		dp("h", "number", groupSize, "200", "Height in pixels; a decimal below 1 is relative."),
		dp("c", "enum", groupSize, "fill", "Crop mode.", "scale", "fit", "limit", "mfit", "fill", "lfill", "fill_pad", "pad", "lpad", "mpad", "crop", "thumb", "auto", "auto_pad"),
		dp("ar", "string", groupSize, "16:9", "Aspect ratio w:h or a decimal."),
		dpr("dpr", "number", groupSize, "2", "Device pixel ratio.", 0.1, 10),
		dp("g", "enum", groupGravity, "auto", "Gravity: compass point, auto (smart), face(s) or a COCO object (g_dog).",
			"center", "north", "south", "east", "west", "north_east", "north_west", "south_east", "south_west",
			"auto", "auto:faces", "face", "faces", "xy_center", "person", "dog", "cat", "car"),
		dp("x", "number", groupGravity, "10", "Horizontal offset (or focus x with g_xy_center)."),
		dp("y", "number", groupGravity, "10", "Vertical offset (or focus y with g_xy_center)."),
		dpr("z", "number", groupGravity, "1.5", "Zoom (c_thumb, c_crop).", 0.01, 100),
		dp("f", "enum", groupFormat, "webp", "Output format; auto negotiates.", "auto", "jpg", "png", "webp", "avif", "jxl", "gif", "heic", "tiff", "bmp", "ico", "svg", "mp4", "webm"),
		dp("q", "string", groupFormat, "80", "Quality 1-100 (80:420 adds chroma subsampling) or auto[:best|good|eco|low]."),
		dp("a", "string", groupSize, "90", "Rotation in degrees, hflip, vflip, exif or ignore; several joined with a dot."),
		dp("b", "color", groupColor, "rgb:ff0000", "Background colour (rgb:<hex>, a CSS name or transparent)."),
		dp("bo", "string", groupColor, "4px_solid_rgb:000000", "Border <n>px_solid_<colour>."),
		dp("r", "string", groupSize, "20", "Corner radius (1-4 values joined with :) or max."),
		dpr("o", "integer", groupColor, "50", "Opacity 0-100.", 0, 100),
		dp("co", "color", groupColor, "rgb:ff8800", "Colour for e_colorize."),
		dpr("e_blur", "integer", groupEffects, "300", "Blur 1-2000.", 1, 2000),
		dpr("e_sharpen", "integer", groupEffects, "100", "Sharpen 1-2000.", 1, 2000),
		dpr("e_unsharp_mask", "integer", groupEffects, "100", "Unsharp mask 1-2000.", 1, 2000),
		dpr("e_pixelate", "integer", groupEffects, "10", "Pixelate block size 1-200.", 1, 200),
		dp("e_grayscale", "flag", groupEffects, "", "Greyscale."),
		dp("e_sepia", "flag", groupEffects, "", "Sepia."),
		dp("e_negate", "flag", groupEffects, "", "Negative."),
		dpr("e_brightness", "integer", groupColor, "30", "Brightness -99..100.", -99, 100),
		dpr("e_contrast", "integer", groupColor, "30", "Contrast -100..100.", -100, 100),
		dpr("e_saturation", "integer", groupColor, "30", "Saturation -100..100.", -100, 100),
		dpr("e_gamma", "integer", groupColor, "30", "Gamma -50..150.", -50, 150),
		dpr("e_hue", "integer", groupColor, "30", "Hue -100..100.", -100, 100),
		dpr("e_red", "integer", groupColor, "30", "Red channel -100..100.", -100, 100),
		dpr("e_green", "integer", groupColor, "30", "Green channel -100..100.", -100, 100),
		dpr("e_blue", "integer", groupColor, "30", "Blue channel -100..100.", -100, 100),
		dpr("e_colorize", "integer", groupColor, "60", "Colorize 0-100 with co_.", 0, 100),
		dp("e_trim", "string", groupSize, "10", "Trim borders: tolerance[:colour]."),
		dpr("e_blur_faces", "integer", groupDetection, "500", "Blur detected faces 1-2000.", 1, 2000),
		dpr("e_pixelate_faces", "integer", groupDetection, "10", "Pixelate detected faces 1-200.", 1, 200),
		dp("fl_progressive", "flag", groupFormat, "", "Progressive JPEG."),
		dp("fl_lossy", "flag", groupFormat, "", "Lossy (not lossless)."),
		dp("fl_png8", "flag", groupFormat, "", "8-bit PNG."),
		dp("fl_strip_profile", "flag", groupMetadata, "", "Strip the colour profile."),
		dp("fl_keep_iptc", "flag", groupMetadata, "", "Keep metadata."),
		dp("fl_attachment", "flag", groupDelivery, "", "Download as an attachment."),
		dp("fl_immutable_cache", "flag", groupDelivery, "", "Cache-Control immutable."),
		dp("cs", "enum", groupColor, "srgb", "Colour space.", "srgb", "tinysrgb", "no_cmyk", "cmyk"),
		dpr("pg", "integer", groupFrames, "2", "Page or frame, 1-based.", 1, 1e6),
		dp("so", "number", groupFrames, "2.5", "Video frame at this second (video sources)."),
		dp("t", "string", groupPresets, "thumb", "Named transformation (a preset)."),
	}
}

// imagekitParams: URL-DIALECTS §7, written <name>-<value> ("e-" effects
// bare or e-<name>-<value>).
func imagekitParams() []dparam {
	return []dparam{
		dp("w", "number", groupSize, "300", "Width in pixels; a fraction below 1 is relative."),
		dp("h", "number", groupSize, "200", "Height in pixels; a fraction below 1 is relative."),
		dp("ar", "string", groupSize, "16-9", "Aspect ratio w-h."),
		dp("c", "enum", groupSize, "at_max", "Crop strategy (maintain_ratio is the default).", "maintain_ratio", "maintain_ratio_no_enlarge", "force", "at_max", "at_max_enlarge", "at_least"),
		dp("cm", "enum", groupSize, "pad_resize", "Crop mode.", "pad_resize", "pad_resize_no_enlarge", "extract", "pad_extract"),
		dp("fo", "enum", groupGravity, "auto", "Focus: compass point, auto (smart), face or a COCO object (fo-dog).",
			"center", "top", "left", "bottom", "right", "top_left", "top_right", "bottom_left", "bottom_right", "auto", "face", "person", "dog", "cat", "car"),
		dp("x", "number", groupGravity, "10", "Extract x (top-left, px)."),
		dp("y", "number", groupGravity, "10", "Extract y (top-left, px)."),
		dpr("z", "number", groupGravity, "0.8", "Zoom on the detected faces or objects.", 0.01, 100),
		dpr("dpr", "number", groupSize, "2", "Device pixel ratio 0.1-5.", 0.1, 5),
		dp("bg", "color", groupColor, "FF0000", "Background colour RRGGBB[AA] or blurred."),
		dpr("q", "integer", groupFormat, "80", "Quality 1-100.", 1, 100),
		dp("f", "enum", groupFormat, "webp", "Output format.", "auto", "orig", "jpg", "png", "webp", "avif", "gif", "jxl", "mp4", "webm"),
		dp("pr", "boolean", groupFormat, "true", "Progressive."),
		dp("lo", "boolean", groupFormat, "true", "Lossless."),
		dp("md", "boolean", groupMetadata, "true", "Keep metadata."),
		dp("cp", "boolean", groupMetadata, "true", "Keep the colour profile."),
		dpr("bl", "number", groupEffects, "10", "Blur 1-100.", 1, 100),
		dp("e-sharpen", "flag", groupEffects, "", "Sharpen."),
		dp("e-grayscale", "flag", groupEffects, "", "Greyscale."),
		dp("r", "string", groupSize, "20", "Corner radius, tl_tr_br_bl or max."),
		dp("b", "string", groupColor, "5_FF0000", "Border <width>_<colour>."),
		dp("rt", "string", groupSize, "90", "Rotation in degrees (N prefix counter-clockwise) or auto."),
		dp("fl", "enum", groupSize, "h", "Flip.", "h", "v", "h_v"),
		dp("t", "string", groupSize, "true", "Trim borders: true or a threshold 1-99."),
		dpr("o", "integer", groupColor, "50", "Opacity 0-100.", 0, 100),
		dp("pg", "string", groupFrames, "2", "Page, 1-based (or a_b for a range)."),
		dp("so", "number", groupFrames, "2.5", "Video frame at this second (video sources)."),
		dp("n", "string", groupPresets, "thumb", "Named transformation (a preset)."),
	}
}

// cloudflareParams: URL-DIALECTS §8, written <name>=<value>.
func cloudflareParams() []dparam {
	return []dparam{
		dpr("width", "integer", groupSize, "300", "Width in pixels.", 1, 12000),
		dpr("height", "integer", groupSize, "200", "Height in pixels.", 1, 12000),
		dp("fit", "enum", groupSize, "cover", "Resize mode.", "scale-down", "contain", "cover", "crop", "pad", "squeeze", "scale-up", "aspect-crop"),
		dp("gravity", "string", groupGravity, "auto", "auto, face, left, right, top, bottom or XxY (0-1)."),
		dpr("zoom", "number", groupDetection, "0.5", "How closely a face crop frames the faces (gravity=face).", 0, 1),
		dpr("dpr", "number", groupSize, "2", "Device pixel ratio.", 0.1, 8),
		dp("quality", "string", groupFormat, "80", "Quality 1-100 or high, medium-high, medium-low, low."),
		dp("slow-connection-quality", "string", groupFormat, "50", "Quality on slow connections (Save-Data or a slow effective connection type)."),
		dp("format", "enum", groupFormat, "webp", "Output format.", "auto", "avif", "webp", "jpeg", "baseline-jpeg", "png", "gif"),
		dp("anim", "boolean", groupFrames, "false", "Keep animation."),
		dp("background", "color", groupColor, "%23ff0000", "Background colour (#hex, CSS name, rgb())."),
		dpr("blur", "number", groupEffects, "10", "Blur 0-250.", 0, 250),
		dpr("sharpen", "number", groupEffects, "2", "Sharpen 0-10.", 0, 10),
		dp("brightness", "number", groupColor, "1.2", "Brightness multiplier."),
		dp("contrast", "number", groupColor, "1.2", "Contrast multiplier."),
		dp("saturation", "number", groupColor, "1.2", "Saturation multiplier."),
		dp("gamma", "number", groupColor, "1.2", "Gamma."),
		dp("rotate", "enum", groupSize, "90", "Rotation.", "0", "90", "180", "270"),
		dp("flip", "enum", groupSize, "h", "Flip.", "h", "v", "hv"),
		dp("trim", "string", groupSize, "border", "border, or top;right;bottom;left in pixels."),
		dp("border", "string", groupColor, "%23000000;4", "Border colour;width."),
		dp("metadata", "enum", groupMetadata, "none", "Metadata to keep.", "keep", "copyright", "none"),
		dp("onerror", "enum", groupDelivery, "redirect", "On a source error, redirect to the source.", "redirect"),
		dp("compression", "enum", groupFormat, "fast", "Encoder speed.", "fast"),
	}
}

// wsrvParams: URL-DIALECTS §11, written <name>=<value>.
func wsrvParams() []dparam {
	return []dparam{
		dp("w", "string", groupSize, "300", "Width in pixels or percent (50%)."),
		dp("h", "string", groupSize, "200", "Height in pixels or percent."),
		dpr("dpr", "number", groupSize, "2", "Device pixel ratio.", 0, 8),
		dp("fit", "enum", groupSize, "cover", "Resize mode.", "inside", "outside", "cover", "fill", "contain"),
		dp("we", "flag", groupSize, "", "Do not enlarge."),
		dp("a", "enum", groupGravity, "attention", "Alignment.", "center", "top", "right", "bottom", "left", "top-left", "top-right", "bottom-left", "bottom-right", "entropy", "attention", "focal"),
		dpr("fpx", "number", groupGravity, "0.3", "Focal point x (a=focal).", 0, 1),
		dpr("fpy", "number", groupGravity, "0.7", "Focal point y (a=focal).", 0, 1),
		dp("cbg", "color", groupColor, "ff0000", "Background of fit=contain."),
		dp("cx", "string", groupSize, "10", "Crop x (px or %)."),
		dp("cy", "string", groupSize, "10", "Crop y (px or %)."),
		dp("cw", "string", groupSize, "100", "Crop width (px or %)."),
		dp("ch", "string", groupSize, "100", "Crop height (px or %)."),
		dp("precrop", "flag", groupSize, "", "Crop before resizing."),
		dpr("trim", "integer", groupSize, "10", "Trim threshold 1-254.", 1, 254),
		dp("mask", "enum", groupEffects, "circle", "Mask shape.", "circle", "ellipse", "triangle", "triangle-180", "pentagon", "pentagon-180", "hexagon", "square", "star", "heart"),
		dp("mtrim", "flag", groupEffects, "", "Trim the masked area."),
		dp("mbg", "color", groupColor, "ffffff", "Background outside the mask."),
		dp("flip", "flag", groupSize, "", "Flip vertically."),
		dp("flop", "flag", groupSize, "", "Flip horizontally."),
		dp("ro", "number", groupSize, "90", "Rotation in degrees."),
		dp("rbg", "color", groupColor, "ffffff", "Background of an arbitrary rotation."),
		dp("bg", "color", groupColor, "ff0000", "Background colour."),
		dpr("blur", "number", groupEffects, "5", "Blur sigma 0.3-1000.", 0.3, 1000),
		dpr("sharp", "number", groupEffects, "1", "Sharpen sigma.", 0, 10),
		dpr("con", "integer", groupColor, "20", "Contrast -100..100.", -100, 100),
		dp("mod", "string", groupColor, "1.2", "Brightness[,saturation[,hue]] multipliers."),
		dpr("sat", "number", groupColor, "1.2", "Saturation multiplier.", 0, 1000),
		dpr("hue", "integer", groupColor, "90", "Hue rotation in degrees.", 0, 359),
		dpr("gam", "number", groupColor, "2.2", "Gamma 1-3.", 1, 3),
		dp("tint", "color", groupColor, "ff8800", "Tint colour."),
		dp("filt", "enum", groupEffects, "greyscale", "Filter.", "greyscale", "sepia", "duotone", "negate"),
		dp("start", "color", groupColor, "c83658", "Duotone shadow colour."),
		dp("stop", "color", groupColor, "d8e74f", "Duotone highlight colour."),
		dp("output", "enum", groupFormat, "webp", "Output format.", "jpg", "png", "gif", "tiff", "webp", "avif", "jxl", "json"),
		dpr("q", "integer", groupFormat, "80", "Quality 1-100.", 1, 100),
		dpr("l", "integer", groupFormat, "9", "PNG compression 0-9.", 0, 9),
		dp("ll", "flag", groupFormat, "", "Lossless."),
		dp("af", "flag", groupFormat, "", "PNG adaptive filter."),
		dp("il", "flag", groupFormat, "", "Interlace (progressive)."),
		dp("n", "integer", groupFrames, "-1", "Pages or frames (-1 all)."),
		dpr("page", "integer", groupFrames, "1", "Page, 0-based.", 0, 1e6),
		dp("filename", "string", groupDelivery, "photo", "Download file name."),
		dp("maxage", "string", groupDelivery, "31d", "Cache max-age (1d-1y)."),
		dp("default", "string", groupDelivery, "1", "Fallback: 1 (redirect to the source) or an image URL."),
		dp("encoding", "enum", groupDelivery, "base64", "Answer a data: URL.", "base64"),
	}
}

// thumborParams: URL-DIALECTS §10. The path segments are the first
// entries (group size/gravity, written in grammar order); the rest are
// filters, written name(value).
func thumborParams() []dparam {
	return []dparam{
		dp("size", "string", groupSize, "300x200", "Target WxH; 0 keeps the ratio, orig the source size, a - prefix flips."),
		dp("fit", "enum", groupSize, "fit-in", "Fit mode segment.", "fit-in", "full-fit-in", "adaptive-fit-in", "adaptive-full-fit-in"),
		dp("crop", "string", groupSize, "10x10:300x200", "Manual crop AxB:CxD (pixels, before resizing)."),
		dp("trim", "enum", groupSize, "trim", "Trim borders.", "trim", "trim:top-left", "trim:bottom-right"),
		dp("halign", "enum", groupGravity, "left", "Horizontal alignment.", "left", "center", "right"),
		dp("valign", "enum", groupGravity, "top", "Vertical alignment.", "top", "middle", "bottom"),
		dp("smart", "flag", groupGravity, "", "Smart crop (faces first in the full image)."),
		dpr("quality", "integer", groupFormat, "80", "Quality 0-100.", 0, 100),
		dp("format", "enum", groupFormat, "webp", "Output format.", "jpeg", "png", "gif", "webp", "avif", "heic"),
		dp("max_bytes", "integer", groupFormat, "50000", "Target size in bytes."),
		dp("autojpg", "flag", groupFormat, "", "JPEG when the image is opaque."),
		dp("strip_exif", "flag", groupMetadata, "", "Strip metadata."),
		dp("strip_icc", "flag", groupMetadata, "", "Strip the colour profile."),
		dp("max_age", "integer", groupDelivery, "3600", "Cache max-age in seconds."),
		dp("no_upscale", "flag", groupSize, "", "Never enlarge."),
		dp("upscale", "flag", groupSize, "", "Enlarge fit-in results."),
		dp("stretch", "flag", groupSize, "", "Stretch to exactly WxH."),
		dpr("proportion", "number", groupSize, "0.5", "Scale the result.", 0.01, 1),
		dp("rotate", "enum", groupSize, "90", "Rotation (counter-clockwise).", "90", "180", "270"),
		dp("focal", "string", groupGravity, "100x80:200x160", "Focal rectangle AxB:CxD (pixels or fractions)."),
		dpr("brightness", "integer", groupColor, "20", "Brightness -100..100.", -100, 100),
		dpr("contrast", "integer", groupColor, "20", "Contrast -100..100.", -100, 100),
		dp("saturation", "number", groupColor, "20", "Saturation percent change."),
		dp("rgb", "string", groupColor, "10,0,-10", "Red, green, blue percent changes."),
		dp("grayscale", "flag", groupEffects, "", "Greyscale."),
		dp("equalize", "flag", groupColor, "", "Equalize the histogram."),
		dp("background_color", "color", groupColor, "ff0000", "Background colour."),
		dp("fill", "color", groupColor, "ff0000", "Fill fit-in padding with a colour (or blur, transparent)."),
		dp("round_corner", "string", groupSize, "20,255,255,255", "Rounded corners: radius,r,g,b."),
		dp("blur", "string", groupEffects, "5", "Blur radius[,sigma]."),
		dp("sharpen", "string", groupEffects, "2,1,true", "Sharpen amount,radius,luminance_only."),
		dp("watermark", "string", groupWatermark, "https://example.com/logo.png,10,10,50", "Watermark url,x,y,alpha[,w_ratio,h_ratio]."),
	}
}

// bunnyParams: URL-DIALECTS §9, written <name>=<value>.
func bunnyParams() []dparam {
	return []dparam{
		dpr("width", "integer", groupSize, "300", "Width in pixels.", 1, 12000),
		dpr("height", "integer", groupSize, "200", "Height in pixels.", 1, 12000),
		dp("aspect_ratio", "string", groupSize, "16:9", "Aspect ratio w:h."),
		dp("upscaling", "enum", groupSize, "resampling", "Enlarge small images.", "off", "resampling"),
		dp("crop", "string", groupSize, "300,200", "Crop w,h or w,h,x,y (pixels)."),
		dp("crop_gravity", "enum", groupGravity, "north", "Crop gravity.", "center", "north", "south", "east", "west", "northeast", "northwest", "southeast", "southwest"),
		dp("focus_crop", "string", groupGravity, "300,200,0.5,0.3", "Crop w,h around x,y (fractions or pixels)."),
		dp("face_crop", "string", groupDetection, "true", "Crop to the faces: true or w,h."),
		dpr("quality", "integer", groupFormat, "80", "Quality 0-100.", 0, 100),
		dp("format", "enum", groupFormat, "webp", "Output format.", "webp", "jpeg", "png", "gif", "avif"),
		dp("auto_optimize", "enum", groupFormat, "medium", "Quality preset.", "low", "medium", "high"),
		dp("sharpen", "boolean", groupEffects, "true", "Sharpen."),
		dpr("blur", "integer", groupEffects, "20", "Blur 0-100.", 0, 100),
		dp("flip", "boolean", groupSize, "true", "Flip vertically."),
		dp("flop", "boolean", groupSize, "true", "Flip horizontally."),
		dp("rotate", "enum", groupSize, "90", "Rotation.", "90", "180", "270", "-90", "-180", "-270"),
		dpr("brightness", "integer", groupColor, "20", "Brightness -100..100.", -100, 100),
		dpr("contrast", "integer", groupColor, "20", "Contrast -100..100.", -100, 100),
		dpr("saturation", "integer", groupColor, "20", "Saturation -100..100.", -100, 100),
		dpr("gamma", "integer", groupColor, "20", "Gamma -100..100.", -100, 100),
		dpr("hue", "integer", groupColor, "33", "Hue 0-100.", 0, 100),
		dp("tint", "color", groupColor, "ff8800", "Tint colour."),
		dpr("sepia", "integer", groupEffects, "50", "Sepia 0-100.", 0, 100),
		dp("class", "string", groupPresets, "thumb", "Image class (a preset)."),
	}
}

// awssihParams: the query parameters SIH maps onto edits (§13); the edits
// JSON itself is typed on the page.
func awssihParams() []dparam {
	return []dparam{
		dp("format", "enum", groupFormat, "webp", "Output format (toFormat).", "jpeg", "png", "webp", "avif", "gif", "tiff", "heif"),
		dpr("width", "integer", groupSize, "300", "Resize width.", 1, 12000),
		dpr("height", "integer", groupSize, "200", "Resize height.", 1, 12000),
		dp("fit", "enum", groupSize, "cover", "Resize fit.", "cover", "contain", "fill", "inside", "outside"),
		dp("rotate", "number", groupSize, "90", "Rotation in degrees."),
		dp("flip", "boolean", groupSize, "true", "Flip vertically."),
		dp("flop", "boolean", groupSize, "true", "Flip horizontally."),
		dp("grayscale", "boolean", groupEffects, "true", "Greyscale."),
	}
}
