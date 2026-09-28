package apidoc

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/playground"
)

// Catalogues returns the option catalogues the playground's option builder
// uses (GET /_playground/api/meta): the imgproxy processing options from
// the declarative catalogue behind the ImgproxyProcessingOptions schema,
// the query dialect's native keys documented on getQueryImage, and the
// imgix parameters documented on getImgixImage. Removed
// imgproxy options are left out; the query keys the page sets itself (url
// and the signatures) too.
//
// The other dialects' catalogues are static tables (dialects.go) that
// TestDialectCataloguesParse checks against each dialect's parser.
func Catalogues() []playground.Catalogue {
	return []playground.Catalogue{
		imgproxyCatalogue(), queryCatalogue(), imgixCatalogue(),
		dialectCatalogue(playground.GrammarCloudinary, cloudinaryParams()),
		dialectCatalogue(playground.GrammarImagekit, imagekitParams()),
		dialectCatalogue(playground.GrammarCloudflare, cloudflareParams()),
		dialectCatalogue(playground.GrammarWsrv, wsrvParams()),
		dialectCatalogue(playground.GrammarThumbor, thumborParams()),
		dialectCatalogue(playground.GrammarBunny, bunnyParams()),
		dialectCatalogue(playground.GrammarAWSSIH, awssihParams()),
	}
}

// imgproxyGroups is the builder group of each imgproxy option (and of the
// query keys named like them); anything else is "other".
var imgproxyGroups = map[string]string{
	"resize": groupSize, "size": groupSize, "resizing_type": groupSize, "resizing_algorithm": groupSize,
	"width": groupSize, "height": groupSize, "min-width": groupSize, "min-height": groupSize, "zoom": groupSize,
	"dpr": groupSize, "enlarge": groupSize, "extend": groupSize, "extend_aspect_ratio": groupSize,
	"crop": groupSize, "crop_aspect_ratio": groupSize, "trim": groupSize, "padding": groupSize,
	"auto_rotate": groupSize, "rotate": groupSize, "flip": groupSize, "fit": groupSize, "w": groupSize, "h": groupSize,
	"rot": groupSize, "pad": groupSize,
	"gravity": groupGravity, "objects_position": groupGravity, "g": groupGravity,
	"format": groupFormat, "quality": groupFormat, "format_quality": groupFormat, "autoquality": groupFormat,
	"max_bytes": groupFormat, "jpeg_options": groupFormat, "png_options": groupFormat, "webp_options": groupFormat,
	"avif_options": groupFormat, "f": groupFormat, "q": groupFormat, "lossless": groupFormat, "progressive": groupFormat,
	"background": groupColor, "background_alpha": groupColor, "adjust": groupColor, "brightness": groupColor,
	"contrast": groupColor, "saturation": groupColor, "monochrome": groupColor, "duotone": groupColor,
	"colorize": groupColor, "gradient": groupColor, "bg": groupColor, "color_profile": groupColor,
	"blur": groupEffects, "sharpen": groupEffects, "pixelate": groupEffects, "unsharp_masking": groupEffects,
	"blur_areas": groupEffects, "progressive_blur": groupEffects, "style": groupEffects,
	"watermark": groupWatermark, "watermark_url": groupWatermark, "watermark_text": groupWatermark,
	"watermark_size": groupWatermark, "watermark_rotate": groupWatermark, "watermark_shadow": groupWatermark,
	"strip_metadata": groupMetadata, "keep_copyright": groupMetadata, "dpi": groupMetadata,
	"strip_color_profile": groupMetadata, "preserve_hdr": groupMetadata, "gain_map": groupMetadata, "enforce_thumbnail": groupMetadata,
	"strip": groupMetadata,
	"page":  groupFrames, "pages": groupFrames, "disable_animation": groupFrames, "video_thumbnail_second": groupFrames,
	"video_thumbnail_keyframes": groupFrames, "video_thumbnail_tile": groupFrames, "video_thumbnail_animation": groupFrames,
	"blur_detections": groupDetection, "draw_detections": groupDetection, "crop_objects": groupDetection,
	"fallback_image_url": groupDelivery, "skip_processing": groupDelivery, "raw": groupDelivery,
	"cachebuster": groupDelivery, "bypass_cache": groupDelivery, "cache_tags": groupDelivery, "expires": groupDelivery,
	"filename": groupDelivery, "return_attachment": groupDelivery, "cb": groupDelivery, "attachment": groupDelivery,
	"preset": groupPresets, "o": groupPresets,
	"pdf_password": groupSecurity, "pdp": groupSecurity,
}

func groupOf(groups map[string]string, name string) string {
	if g, ok := groups[name]; ok {
		return g
	}
	return groupOther
}

func imgproxyCatalogue() playground.Catalogue {
	src := imgproxy.ProcessingOptions()
	opts := make([]playground.Option, 0, len(src))
	for i := range src {
		o := &src[i]
		if o.Removed {
			continue
		}
		opts = append(opts, playground.Option{
			Name: o.Name, Aliases: o.Aliases, Doc: o.Doc, Forms: forms(o.Forms),
			Example: o.Example, Milestone: o.Milestone, Security: o.Security, Group: groupOf(imgproxyGroups, o.Name),
		})
	}
	return playground.Catalogue{Grammar: playground.GrammarImgproxy, Options: opts}
}

// infoGroups is the Info panel group of each /info option.
var infoGroups = map[string]string{
	"size": groupReport, "format": groupReport, "dimensions": groupReport, "colorspace": groupReport,
	"bands": groupReport, "sample_format": groupReport, "pages_number": groupReport, "alpha": groupReport,
	"exif": groupMetadata, "iptc": groupMetadata, "xmp": groupMetadata, "video_meta": groupMetadata,
	"palette": groupColor, "average": groupColor, "dominant_colors": groupColor,
	"blurhash": groupHashes, "thumbhash": groupHashes, "perceptual_hash": groupHashes, "calc_hashsums": groupHashes,
	"hashsum":        groupHashes,
	"detect_objects": groupDetection, "classify": groupDetection, "crop": groupDetection, "objects_position": groupDetection,
	"page": groupFrames, "video_thumbnail_second": groupFrames, "video_thumbnail_keyframes": groupFrames,
	"cachebuster": groupDelivery, "expires": groupDelivery, "bypass_cache": groupDelivery, "cache_tags": groupDelivery,
	"preset":             groupPresets,
	"max_src_resolution": groupSecurity, "max_src_file_size": groupSecurity,
	"pdf_password": groupSecurity,
}

// InfoOptions returns the imgproxy /info option catalogue (the
// ImgproxyInfoOptions schema) for the playground's Info panel builder
// (GET /_playground/api/meta, info_options), with each option's default.
func InfoOptions() []playground.Option {
	src := imgproxy.InfoOptions()
	opts := make([]playground.Option, 0, len(src))
	for i := range src {
		o := &src[i]
		if o.Removed {
			continue
		}
		opts = append(opts, playground.Option{
			Name: o.Name, Aliases: o.Aliases, Doc: o.Doc, Forms: forms(o.Forms), Example: o.Example,
			Milestone: o.Milestone, Security: o.Security, Group: groupOf(infoGroups, o.Name), Default: o.Default,
		})
	}
	return opts
}

func forms(in []imgproxy.Form) []playground.Form {
	out := make([]playground.Form, 0, len(in))
	for j := range in {
		f := &in[j]
		out = append(out, playground.Form{Args: append([]playground.Arg{}, args(f.Args)...), Rest: args(f.Rest), MinItems: f.MinItems})
	}
	return out
}

func args(in []imgproxy.Arg) []playground.Arg {
	if len(in) == 0 {
		return nil
	}
	out := make([]playground.Arg, 0, len(in))
	for i := range in {
		a := &in[i]
		pa := playground.Arg{
			Name: a.Name, Type: argType(a.Type), Enum: a.Enum,
			Optional: a.Optional || a.Type == imgproxy.TypeBoolean, Pattern: a.Pattern, Doc: a.Doc,
		}
		if a.HasMin {
			pa.Min = new(a.Min)
		}
		if a.HasMax {
			pa.Max = new(a.Max)
		}
		out = append(out, pa)
	}
	return out
}

func argType(t imgproxy.ArgType) string {
	switch t {
	case imgproxy.TypeInteger:
		return "integer"
	case imgproxy.TypeNumber:
		return "number"
	case imgproxy.TypeBoolean:
		return "boolean"
	case imgproxy.TypeEnum:
		return "enum"
	case imgproxy.TypeColor:
		return "color"
	case imgproxy.TypeBase64:
		return "base64"
	case imgproxy.TypeDirection:
		return "direction"
	default:
		return "string"
	}
}

// queryCatalogue turns queryKeys into options with one string argument;
// keys documented as "Alias of x." become aliases of x.
func queryCatalogue() playground.Catalogue {
	var opts []playground.Option
	index := map[string]int{}
	for _, kv := range queryKeys() {
		name, doc := kv[0], kv[1]
		switch name {
		case "url", "sign", "xi_sig":
			continue
		}
		if target, ok := strings.CutPrefix(doc, "Alias of "); ok {
			if i, ok := index[strings.TrimSuffix(target, ".")]; ok {
				opts[i].Aliases = append(opts[i].Aliases, name)
				continue
			}
		}
		index[name] = len(opts)
		opts = append(opts, playground.Option{
			Name: name, Doc: doc, Group: groupOf(imgproxyGroups, name),
			Forms: []playground.Form{{Args: []playground.Arg{{Name: "value", Type: "string", Doc: doc}}}},
		})
	}
	return playground.Catalogue{Grammar: playground.GrammarQuery, Options: opts}
}
