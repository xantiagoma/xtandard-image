package query

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Expectation phrases for native keys (client-safe, static).
const (
	expFit  = "fit, contain, inside, fill, cover, fill-down, force, stretch, outside or auto"
	expCrop = "w,h or w,h,x,y"
	expFlip = "h, v or hv"
)

// native applies native keys (docs/URL-DIALECTS.md §4) in query order.
func (p *Parser) native(b *imgproxy.Builder, params []param) error {
	for _, kv := range params {
		if err := p.nativeKey(b, kv.key, kv.value); err != nil {
			return err
		}
	}
	return nil
}

// nativeKey applies one native key through its imgproxy equivalent.
//
//nolint:gocyclo // one flat switch over the key table is the clearest form
func (p *Parser) nativeKey(b *imgproxy.Builder, key, v string) error {
	switch key {
	case paramURL:
		return nil
	case paramO:
		return b.ApplyPath(paramO, v)
	case "width", "w":
		return b.Apply("width", v)
	case "height", "h":
		return b.Apply("height", v)
	case "fit":
		return fit(b, v)
	case "gravity", "g":
		return gravity(b, v)
	case "crop":
		return crop(b, v)
	case "dpr":
		return b.Apply("dpr", v)
	case "enlarge":
		return b.Apply("enlarge", v)
	case "format", "f":
		return b.Apply("format", v)
	case "quality", "q":
		return b.Apply("quality", v)
	case "lossless":
		return boolPtr("lossless", v, &b.Request().Output.Lossless)
	case "progressive":
		return boolPtr("progressive", v, &b.Request().Output.Progressive)
	case "blur":
		return b.Apply("blur", v)
	case "sharpen":
		return b.Apply("sharpen", v)
	case "pixelate":
		return b.Apply("pixelate", v)
	case "rotate", "rot":
		return b.Apply("rotate", v)
	case "flip":
		return flip(b, v)
	case "bg":
		return b.Apply("background", split(v)...)
	case "strip":
		return b.Apply("strip_metadata", v)
	case "trim":
		return b.Apply("trim", split(v)...)
	case "pad":
		return b.Apply("padding", split(v)...)
	case "preset":
		return b.Apply("preset", split(v)...)
	case "page":
		return b.Apply("page", v)
	case "video_thumbnail_second", "vts":
		return b.Apply("video_thumbnail_second", v)
	case "video_thumbnail_keyframes", "vtk":
		return b.Apply("video_thumbnail_keyframes", v)
	case "pages":
		return b.Apply("pages", v)
	case "cb":
		return b.Apply("cachebuster", v)
	case "filename":
		return b.Apply("filename", v)
	case "attachment":
		return b.Apply("return_attachment", v)
	case "expires":
		return b.Apply("expires", v)
	}
	if isSignatureParam(key) {
		return nil
	}
	return p.unknown(b, key)
}

// split splits a multi-argument value on "," or ":", keeping empty
// arguments so positions match the imgproxy option.
func split(v string) []string {
	args := make([]string, 0, 4)
	for {
		i := strings.IndexAny(v, ",:")
		if i < 0 {
			return append(args, v)
		}
		args = append(args, v[:i])
		v = v[i+1:]
	}
}

func boolPtr(field, v string, dst **bool) error {
	on, err := imgproxy.ParseBool(field, v)
	if err != nil {
		return err
	}
	*dst = &on
	return nil
}

// fit maps CSS/sharp-style fit names to imgproxy resizing types.
func fit(b *imgproxy.Builder, v string) error {
	switch v {
	case "fit", "contain", "inside":
		return b.Apply("resizing_type", "fit")
	case "fill", "cover":
		return b.Apply("resizing_type", "fill")
	case "fill-down":
		return b.Apply("resizing_type", "fill-down")
	case "force", "stretch":
		return b.Apply("resizing_type", "force")
	case "auto":
		return b.Apply("resizing_type", "auto")
	case "outside":
		b.Pipeline().Resize.Type = transform.ResizeOutside
		return nil
	}
	return dialect.InvalidArgument("fit", v, expFit)
}

// gravityCode maps long gravity names to imgproxy codes; anything else
// (ce, no, sm, fp, obj, ...) passes through for imgproxy to validate.
func gravityCode(name string) string {
	switch name {
	case "center", "centre":
		return "ce"
	case "north":
		return "no"
	case "south":
		return "so"
	case "east":
		return "ea"
	case "west":
		return "we"
	case "northeast":
		return "noea"
	case "northwest":
		return "nowe"
	case "southeast":
		return "soea"
	case "southwest":
		return "sowe"
	case "smart", "attention":
		return "sm"
	case "focal":
		return "fp"
	}
	return name
}

// gravity is g=<name>[:x_offset:y_offset], g=fp:x:y or g=entropy.
func gravity(b *imgproxy.Builder, v string) error {
	args := split(v)
	if args[0] == "entropy" {
		if len(args) > 1 {
			return dialect.InvalidArgument("gravity", v, "entropy without arguments")
		}
		b.Pipeline().Gravity = transform.Gravity{Type: transform.GravityEntropy}
		return nil
	}
	args[0] = gravityCode(args[0])
	return b.Apply("gravity", args...)
}

// crop is crop=w,h (gravity from g=) or crop=w,h,x,y (top-left corner).
func crop(b *imgproxy.Builder, v string) error {
	args := split(v)
	switch len(args) {
	case 1, 2:
		return b.Apply("crop", args...)
	case 4:
		return b.Apply("crop", args[0], args[1], "nowe", args[2], args[3])
	}
	return dialect.InvalidArgument("crop", v, expCrop)
}

// flip is flip=h, v or hv.
func flip(b *imgproxy.Builder, v string) error {
	switch v {
	case "h":
		return b.Apply("flip", "1", "0")
	case "v":
		return b.Apply("flip", "0", "1")
	case "hv", "vh", "both":
		return b.Apply("flip", "1", "1")
	}
	return dialect.InvalidArgument("flip", v, expFlip)
}
