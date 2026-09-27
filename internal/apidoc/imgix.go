package apidoc

import (
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/xantiagoma/xtandard-image/internal/playground"
)

// imgixParam is one imgix Rendering API parameter the imgix dialect applies
// (URL-DIALECTS §5, internal/dialect/imgix). typ is a playground.Arg type.
type imgixParam struct {
	name      string
	aliases   []string
	typ       string
	enum      []string
	doc       string
	milestone string
}

// imgixParams lists the parameters in documentation order. Parameters xi
// records as unsupported (AI features, blend, video, ...) are left out; s
// and expires are signing parameters the page does not set.
//
//nolint:funlen // one flat table is the clearest form
func imgixParams() []imgixParam {
	p := func(name, typ, doc string, aliases ...string) imgixParam {
		return imgixParam{name: name, typ: typ, doc: doc, aliases: aliases}
	}
	e := func(name, doc string, enum ...string) imgixParam {
		return imgixParam{name: name, typ: "enum", enum: enum, doc: doc}
	}
	m3 := func(ip imgixParam) imgixParam { ip.milestone = "M3"; return ip }
	return []imgixParam{
		// Size and crop.
		p("w", "number", "Width: pixels, or a fraction of the source below 1.", "width"),
		p("h", "number", "Height: pixels, or a fraction of the source below 1.", "height"),
		e("fit", "Resize mode (default clip once w or h is set).",
			"clip", "crop", "fill", "fillmax", "max", "min", "scale", "clamp", "facearea", "smart-crop"),
		p("crop", "string", "With fit=crop/min: top, bottom, left, right, faces, focalpoint, entropy, edges or object classes, comma-separated."),
		p("ar", "string", "Aspect ratio w:h (with fit=crop)."),
		p("dpr", "number", "Device pixel ratio (0 < dpr <= 10)."),
		p("max-w", "integer", "Maximum width.", "max-width"),
		p("max-h", "integer", "Maximum height.", "max-height"),
		p("min-w", "integer", "Minimum width.", "min-width"),
		p("min-h", "integer", "Minimum height.", "min-height"),
		p("rect", "string", "Source crop x,y,w,h before resizing (pixels, or fractions below 1)."),
		p("fp-x", "number", "Focal point x, 0..1 (crop=focalpoint)."),
		p("fp-y", "number", "Focal point y, 0..1 (crop=focalpoint)."),
		e("fill", "With fit=fill/fillmax: how the extra space is filled.", "solid", "blur"),
		p("fill-color", "color", "Fill colour for fit=fill.", "fillcolor"),
		m3(p("facepad", "number", "Padding around the face for fit=facearea.")),
		m3(p("faceindex", "integer", "1-based face for fit=facearea.")),
		// Format and quality.
		e("fm", "Output format; json answers image info.",
			"jpg", "pjpg", "png", "png8", "png32", "webp", "avif", "jxl", "gif", "json", "blurhash"),
		p("auto", "string", "format (negotiate from Accept) and/or compress (quality 45, strip metadata), comma-separated."),
		p("q", "integer", "Quality 0..100."),
		p("lossless", "boolean", "Lossless encoding."),
		p("jpg-progressive", "boolean", "Progressive JPEG.", "jpeg-progressive"),
		e("chromasub", "Chroma subsampling.", "420", "422", "444"),
		e("cs", "Colour space.", "srgb", "tinysrgb", "strip", "origin"),
		p("dpi", "integer", "Output resolution metadata."),
		p("dl", "string", "Download as an attachment with this file name."),
		p("ch", "string", "Client hints to honour: Width, DPR, Save-Data."),
		// Rotation.
		e("orient", "EXIF orientation override.", "1", "2", "3", "4", "5", "6", "7", "8", "90", "180", "270"),
		p("rot", "number", "Counter-clockwise rotation, 0..359 degrees."),
		e("flip", "Mirror.", "h", "v", "hv"),
		// Adjustments and stylize.
		p("bri", "number", "Brightness -100..100."),
		p("con", "number", "Contrast -100..100."),
		p("exp", "number", "Exposure -100..100."),
		p("gam", "number", "Gamma -100..100."),
		p("sat", "number", "Saturation -100..100."),
		p("hue", "number", "Hue shift in degrees."),
		p("sharp", "number", "Sharpen 0..100."),
		p("usm", "number", "Unsharp mask amount -100..100."),
		p("usmrad", "number", "Unsharp mask radius (default 2.5)."),
		p("blur", "number", "Blur 0..2000."),
		p("px", "integer", "Pixelate 0..100."),
		p("sepia", "number", "Sepia 0..100."),
		p("monochrome", "color", "Monochrome tint (alpha = intensity).", "mono"),
		p("duotone", "string", "Two colours: shadow,highlight."),
		p("duotone-alpha", "number", "Duotone intensity 0..100."),
		p("invert", "boolean", "Invert colours.", "inv"),
		m3(p("face-blur", "integer", "Blur detected faces 0..100.")),
		m3(p("face-pixel", "integer", "Pixelate detected faces 0..100.", "face-pixelate")),
		// Canvas.
		p("bg", "color", "Background colour."),
		p("pad", "integer", "Padding on every side."),
		p("border", "string", "Border width,color."),
		p("border-radius", "string", "Corner radius r or tl,tr,br,bl."),
		e("trim", "Trim borders.", "auto", "color", "colorunlessalpha"),
		p("trim-color", "color", "Trim colour (trim=color)."),
		p("trim-tol", "number", "Trim tolerance (trim=color)."),
		p("trim-md", "number", "Trim mean difference (trim=auto, default 11)."),
		e("mask", "Mask shape.", "ellipse", "corners"),
		p("mask-bg", "color", "Background outside an ellipse mask."),
		p("corner-radius", "string", "Corner radius for mask=corners."),
		p("page", "integer", "1-based page of a PDF."),
		p("frame", "integer", "1-based frame of an animation."),
		// Watermarks.
		p("mark", "string", "Watermark image: http(s) URL or a path on the mount's source.", "m"),
		p("mark-align", "string", "Watermark alignment: top, middle, bottom, left, center, right.", "ma"),
		p("mark-alpha", "number", "Watermark opacity 0..100.", "malph"),
		p("mark-pad", "integer", "Watermark padding (default 5).", "mp"),
		p("mark-scale", "number", "Watermark size as a percentage of the image.", "ms"),
		p("mark-w", "integer", "Watermark width.", "mw"),
		p("mark-h", "integer", "Watermark height.", "mh"),
		p("mark-x", "number", "Watermark x from the left edge.", "mx"),
		p("mark-y", "number", "Watermark y from the top edge.", "my"),
		e("mark-tile", "Repeat the watermark.", "grid"),
		p("mark-rot", "number", "Watermark rotation, counter-clockwise degrees."),
		p("mark-base", "string", "http(s) URL prefix for mark paths.", "mb"),
		p("txt", "string", "Text watermark (plain text).", "t"),
		p("txt-font", "string", "Font family and style, comma-separated.", "tf"),
		p("txt-size", "integer", "Text size (default 12).", "tsz"),
		p("txt-color", "color", "Text colour.", "tc", "txtclr"),
		p("txt-align", "string", "Text alignment: top, middle, bottom, left, center, right.", "ta"),
		p("txt-pad", "integer", "Text padding (default 10).", "tp"),
		p("txt-shad", "number", "Text shadow 0..10.", "tsh"),
		p("txt-width", "integer", "Text wrap width.", "txtwidth"),
		p("txt-x", "number", "Text x from the left edge."),
		p("txt-y", "number", "Text y from the top edge."),
	}
}

// imgixCatalogue is the imgix option catalogue of the playground: one
// value argument per parameter.
func imgixCatalogue() playground.Catalogue {
	params := imgixParams()
	opts := make([]playground.Option, 0, len(params))
	for _, ip := range params {
		opts = append(opts, playground.Option{
			Name: ip.name, Aliases: ip.aliases, Doc: ip.doc, Milestone: ip.milestone, Group: imgixGroup(ip.name),
			Forms: []playground.Form{{Args: []playground.Arg{{Name: "value", Type: ip.typ, Enum: ip.enum, Doc: ip.doc}}}},
		})
	}
	return playground.Catalogue{Grammar: playground.GrammarImgix, Options: opts}
}

// imgixGroup is the builder group of an imgix parameter.
func imgixGroup(name string) string {
	switch {
	case strings.HasPrefix(name, "mark"), strings.HasPrefix(name, "txt"):
		return groupWatermark
	case strings.HasPrefix(name, "face-"):
		return groupDetection
	}
	switch name {
	case "crop", "fp-x", "fp-y":
		return groupGravity
	case "fm", "auto", "q", "lossless", "jpg-progressive", "chromasub":
		return groupFormat
	case "cs", "dpi":
		return groupMetadata
	case "dl", "ch":
		return groupDelivery
	case "page", "frame":
		return groupFrames
	case "bri", "con", "exp", "gam", "sat", "hue", "monochrome", "duotone", "duotone-alpha", "bg":
		return groupColor
	case "sharp", "usm", "usmrad", "blur", "px", "sepia", "invert", "mask", "mask-bg":
		return groupEffects
	}
	return groupSize
}

func imgixOperation() *huma.Operation {
	params := imgixParams()
	ps := make([]*huma.Param, 0, len(params)+3)
	ps = append(ps, pathParam("path", "Object key on the mount's source, or a percent-encoded http(s) URL (web proxy source).", "photos/a.jpg", nil))
	for _, ip := range params {
		ps = append(ps, queryParam(ip.name, ip.doc))
	}
	ps = append(ps,
		queryParam("expires", "Expiry, unix seconds; 404 once passed (checked after the signature)."),
		queryParam("s", "Signature: hex MD5 of the mount's token, the path and ? plus the query without s."),
	)
	return &huma.Operation{
		OperationID: "getImgixImage", Method: http.MethodGet, Path: "/imgix/{path}", Tags: []string{"image"},
		Summary: "Process an image (imgix dialect)",
		Description: "imgix Rendering API URL under its mount (XI_DIALECTS_IMGIX_ENABLED, prefix /imgix/ or host-bound). " +
			"Parameters apply by name, the last occurrence wins; any parameter may be base64url-encoded under its name plus 64 " +
			"(txt64, mark64, ...). Unknown and unsupported parameters are ignored (400 with XI_DIALECTS_STRICT).",
		Parameters:   ps,
		Responses:    imageResponses(),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#5-imgix"},
		Extensions:   map[string]any{"x-xi-listener": ListenerPublic, "x-xi-dialect": "imgix"},
	}
}
