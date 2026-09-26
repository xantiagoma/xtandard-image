package imgix

import (
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Reasons recorded in Request.Ignored (static, client-safe).
const (
	reasonUnknown        = "unknown option"
	reasonUnsupported    = "unsupported"
	reasonNotImplemented = "not implemented"
	reasonApproximated   = "approximated"
	reasonM3             = "takes effect in M3"
	reasonFacesJSON      = "only with fm=json"
	reasonCropFit        = "only with fit=crop, fit=min, fit=smart-crop or fit=facearea"
	reasonFocalPoint     = "only with crop=focalpoint"
	reasonFillFit        = "only with fit=fill or fit=fillmax"
	reasonFaceFit        = "only with fit=facearea"
	reasonARFit          = "only with fit=crop or fit=smart-crop"
	reasonAutoFormat     = "auto=format takes precedence"
	reasonFillColor      = "fill-color takes precedence"
	reasonNeedsParent    = "only with its base parameter"
)

// Expectation phrases (client-safe, static).
const (
	expFit     = "clamp, clip, crop, facearea, fill, fillmax, max, min, scale or smart-crop"
	expCrop    = "top, bottom, left, right, faces, focalpoint, entropy, edges, auto or an object class"
	expFormat  = "jpg, pjpg, png, png8, png32, webp, avif, jxl, gif, json, blurhash, mp4 or webm"
	expAuto    = "format, compress, enhance, redeye or true"
	expDim     = "number > 0 (below 1 = fraction of the source)"
	expAR      = "w:h with positive numbers"
	expRect    = "x,y,w,h"
	expFill    = "solid, blur, gen, generative or gradient"
	expSub     = "420, 422 or 444"
	expCS      = "srgb, tinysrgb, adobergb1998, strip or origin"
	expCH      = "Width, DPR or Save-Data"
	expOrient  = "1-8, 90, 180 or 270"
	expFlip    = "h, v or hv"
	expRotType = "pivot or straighten"
	expIPTC    = "allow or block"
	expTrim    = "auto, color, alpha or colorunlessalpha"
	expBorder  = "width,color"
	expRadius  = "r or tl,tr,br,bl"
	expDuotone = "color,color"
	expAlign   = "top, middle, bottom, left, center or right"
	expText    = "non-empty text"
)

// paramKind classifies a parameter name.
type paramKind uint8

const (
	kindUnknown paramKind = iota
	kindKnown
	kindSilent      // accepted and dropped without a trace (SDK tags)
	kindUnsupported // imgix feature xi deliberately does not offer
	kindLater       // imgix feature xi does not implement yet (no model field)
)

// paramKinds lists every parameter name the dialect understands.
var paramKinds = func() map[string]paramKind {
	m := map[string]paramKind{"ixlib": kindSilent, "ixid": kindSilent, "ixjsv": kindSilent}
	for k := range strings.FieldsSeq(`
		w h ar dpr max-w max-h min-w min-h fit crop fp-x fp-y fp-z rect fill fill-color facepad faceindex faces
		fm q auto lossless chromasub cs dpi dl ch expires jpg-progressive iptc
		orient rot rot-type flip
		bri con exp gam sat hue vib sharp usm usmrad blur px sepia monochrome duotone duotone-alpha invert
		face-blur face-pixel
		bg pad pad-top pad-right pad-bottom pad-left
		border border-top border-right border-bottom border-left border-radius border-radius-inner
		trim trim-color trim-tol trim-md trim-sd trim-pad trim-alpha mask mask-bg corner-radius page frame` +
		" " + markParams + " " + txtParams) {
		m[k] = kindKnown
	}
	for k := range strings.FieldsSeq(`
		blend blend-mode blend-alpha blend-align blend-color blend-crop blend-fit blend-h blend-w blend-x blend-y
		blend-pad blend-size bg-remove bg-remove-fallback bg-replace bg-replace-fallback bg-replace-neg-prompt
		upscale upscale-fallback nr nrs palette colorquant colors prefix fp-debug htn high shad
		fill-gen-fallback fill-gen-neg-prompt fill-gen-pos fill-gen-prompt fill-gen-seed
		bg-remove-fg-type bg-remove-semi-transparency bg-remove-transparency-threshold
		object-removal-negative-prompt object-removal-prompt object-removal-rect object-removal-seed
		fill-gradient-cs fill-gradient-linear fill-gradient-linear-direction fill-gradient-radial
		fill-gradient-radial-radius fill-gradient-radial-x fill-gradient-radial-y fill-gradient-type
		border-stroke-color border-stroke-size lp-blur pdf-annotation rasterize-bypass svg-sanitize`) {
		m[k] = kindUnsupported
	}
	// Animation frame controls and the transparency grid: no model field
	// yet.
	for k := range strings.FieldsSeq(`loop reverse fps skip interval transparency grid-colors grid-size`) {
		m[k] = kindLater
	}
	return m
}()

const (
	markParams = "mark mark-alpha mark-align mark-pad mark-scale mark-w mark-h mark-x mark-y mark-fit mark-tile mark-rot mark-base" +
		" mark-if-min-width mark-if-min-height"
	txtParams = "txt txt-font txt-size txt-color txt-align txt-pad txt-shad txt-width txt-fit txt-clip txt-lead txt-track txt-line" +
		" txt-line-color txt-x txt-y"
)

// aliases maps legacy and short imgix parameter spellings to the current
// names (imgix-url-params 11.36 parameters.json "aliases").
var aliases = map[string]string{
	"f": "fit", "width": "w", "height": "h", "max-width": "max-w", "max-height": "max-h",
	"min-width": "min-w", "min-height": "min-h", "fillcolor": "fill-color", "inv": "invert", "mono": "monochrome",
	"or": "orient", "rottype": "rot-type", "rott": "rot-type", "jpeg-progressive": "jpg-progressive",
	"maskbg": "mask-bg", "face-pixelate": "face-pixel",
	"m": "mark", "markalign": "mark-align", "ma": "mark-align", "markalpha": "mark-alpha", "malph": "mark-alpha",
	"markpad": "mark-pad", "mp": "mark-pad", "markscale": "mark-scale", "ms": "mark-scale", "markfit": "mark-fit",
	"mf": "mark-fit", "marktile": "mark-tile", "mtile": "mark-tile", "markw": "mark-w", "mw": "mark-w",
	"markh": "mark-h", "mh": "mark-h", "markx": "mark-x", "mx": "mark-x", "marky": "mark-y", "my": "mark-y",
	"markbase": "mark-base", "mb": "mark-base",
	"t": "txt", "txtfont": "txt-font", "tf": "txt-font", "txtclr": "txt-color", "txtcolor": "txt-color",
	"txt-clr": "txt-color", "tc": "txt-color", "txtsize": "txt-size", "tsz": "txt-size", "txtalign": "txt-align",
	"ta": "txt-align", "txtpad": "txt-pad", "tp": "txt-pad", "txtshad": "txt-shad", "tsh": "txt-shad",
	"txtclip": "txt-clip", "tcl": "txt-clip", "txtfit": "txt-fit", "txtwidth": "txt-width", "txtlead": "txt-lead",
	"txttrack": "txt-track", "tt": "txt-track", "txtline": "txt-line", "tl": "txt-line",
	"txtlineclr": "txt-line-color", "txtlinecolor": "txt-line-color", "txt-line-clr": "txt-line-color",
	"txtx": "txt-x", "txty": "txt-y",
	"trimcolor": "trim-color", "trimtol": "trim-tol", "trimmd": "trim-md", "trimsd": "trim-sd", "trimpad": "trim-pad",
	"b": "blend", "blendalign": "blend-align", "ba": "blend-align", "blendalpha": "blend-alpha", "balph": "blend-alpha",
	"blendcolor": "blend-color", "blend-clr": "blend-color", "blendclr": "blend-color", "blendcrop": "blend-crop",
	"bc": "blend-crop", "blendfit": "blend-fit", "bf": "blend-fit", "blendh": "blend-h", "bh": "blend-h",
	"blendmode": "blend-mode", "bm": "blend-mode", "blendpad": "blend-pad", "bp": "blend-pad",
	"blendsize": "blend-size", "bs": "blend-size", "blendw": "blend-w", "bw": "blend-w", "blendx": "blend-x",
	"bx": "blend-x", "blendy": "blend-y", "by": "blend-y",
	"background-remove": "bg-remove", "background-replace": "bg-replace",
	"background-replace-negative-prompt":   "bg-replace-neg-prompt",
	"background-removal-semi-transparency": "bg-remove-semi-transparency",
	"fill-generative-fallback":             "fill-gen-fallback", "fill-generative-negative-prompt": "fill-gen-neg-prompt",
	"fill-generative-position": "fill-gen-pos", "fill-generative-prompt": "fill-gen-prompt",
	"fill-generative-seed": "fill-gen-seed", "license-plate-blur": "lp-blur", "annotation": "pdf-annotation",
}

// state is the per-request parse state.
type state struct {
	p    *Parser
	b    *imgproxy.Builder
	r    *transform.Request
	pl   *transform.Pipeline
	vals map[string]string // last value per known parameter
}

// collect sorts the parameters into known values (last occurrence wins),
// silent tags, and ignored or rejected names.
func (st *state) collect(ps []param) error {
	for _, kv := range ps {
		key := kv.key
		if a, ok := aliases[key]; ok {
			key = a
		}
		kind := paramKinds[key]
		if kind == kindUnknown && strings.HasPrefix(key, "video-") {
			kind = kindUnsupported // video encoding and spritesheets
		}
		switch kind {
		case kindKnown:
			st.vals[key] = kv.value
		case kindSilent:
		case kindUnsupported, kindLater:
			if st.p.o.Strict {
				return dialect.UnknownOption(key)
			}
			if kind == kindLater {
				st.b.Ignore(key, reasonNotImplemented)
			} else {
				st.b.Ignore(key, reasonUnsupported)
			}
		default:
			if st.p.o.Strict {
				return dialect.UnknownOption(key)
			}
			st.b.Ignore(key, reasonUnknown)
		}
	}
	return nil
}

func (st *state) get(key string) (string, bool) {
	v, ok := st.vals[key]
	return v, ok
}

func (st *state) has(key string) bool {
	_, ok := st.vals[key]
	return ok
}

// ignoreIf records key with reason when it is present.
func (st *state) ignoreIf(reason string, keys ...string) {
	for _, k := range keys {
		if st.has(k) {
			st.b.Ignore(k, reason)
		}
	}
}

// apply maps the collected values onto the request in a fixed order.
func (st *state) apply() error {
	for _, step := range []func() error{
		st.applyCanvas, st.applySize, st.applyOutput, st.applyOrientation,
		st.applyEffects, st.applyPages, st.applyWatermarks,
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// --- numbers ---------------------------------------------------------------

func finite(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// numRange parses a number in [lo, hi].
func numRange(field, s string, lo, hi float64) (float64, error) {
	f, ok := finite(s)
	if !ok || f < lo || f > hi {
		return 0, dialect.InvalidArgument(field, s, "number in range "+fmtNum(lo)+" to "+fmtNum(hi))
	}
	return f, nil
}

// intRange parses a number in [lo, hi] rounded to the nearest integer
// (imgix rounds fractional pixel values).
func intRange(field, s string, lo, hi int) (int, error) {
	f, err := numRange(field, s, float64(lo), float64(hi))
	return int(math.Round(f)), err
}

// optNum parses key with numRange when present; def otherwise.
func (st *state) optNum(key string, lo, hi, def float64) (float64, error) {
	v, ok := st.get(key)
	if !ok {
		return def, nil
	}
	return numRange(key, v, lo, hi)
}

// dim parses w/h: >= 1 is pixels, (0, 1) a fraction of the source.
func dim(field, s string) (transform.Dim, error) {
	f, ok := finite(s)
	if !ok || f <= 0 || f > transform.MaxDimension {
		return transform.Dim{}, dialect.InvalidArgument(field, s, expDim)
	}
	if f < 1 {
		return transform.Dim{Rel: f}, nil
	}
	return transform.Dim{Px: int(math.Round(f))}, nil
}

// offset parses a crop or watermark offset: >= 1 pixels, < 1 relative.
func offset(field, s string) (float64, error) {
	f, err := numRange(field, s, 0, transform.MaxDimension)
	if err == nil && f >= 1 {
		f = math.Round(f)
	}
	return f, err
}

func list(s string) []string { return strings.Split(s, ",") }
