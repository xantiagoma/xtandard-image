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
	reasonM2             = "takes effect in M2"
	reasonM3             = "takes effect in M3"
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
	expFill    = "solid, blur or gen"
	expSub     = "420, 422 or 444"
	expCS      = "srgb, tinysrgb, adobergb1998, strip or origin"
	expCH      = "Width, DPR or Save-Data"
	expOrient  = "1-8, 90, 180 or 270"
	expFlip    = "h, v or hv"
	expTrim    = "auto or color"
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
)

// paramKinds lists every parameter name the dialect understands.
var paramKinds = func() map[string]paramKind {
	m := map[string]paramKind{"ixlib": kindSilent, "ixid": kindSilent, "ixjsv": kindSilent}
	for k := range strings.FieldsSeq(`
		w h ar dpr max-w max-h min-w min-h fit crop fp-x fp-y fp-z rect fill fill-color facepad faceindex
		fm q auto lossless chromasub cs dpi dl ch expires
		orient rot flip
		bri con exp gam sat hue vib sharp usm usmrad blur px sepia monochrome duotone duotone-alpha invert
		bg pad pad-top pad-right pad-bottom pad-left
		border border-top border-right border-bottom border-left border-radius border-radius-inner
		trim trim-color trim-tol trim-md trim-sd trim-pad mask mask-bg corner-radius page frame` +
		" " + markParams + " " + txtParams) {
		m[k] = kindKnown
	}
	for k := range strings.FieldsSeq(`
		blend blend-mode blend-alpha blend-align blend-color blend-crop blend-fit blend-h blend-w blend-x blend-y
		blend-pad blend-size bg-remove bg-remove-fallback bg-replace bg-replace-fallback bg-replace-neg-prompt
		upscale upscale-fallback nr nrs palette colorquant colors prefix faces fp-debug htn high shad
		fill-gen-fallback fill-gen-neg-prompt fill-gen-pos fill-gen-prompt fill-gen-seed`) {
		m[k] = kindUnsupported
	}
	return m
}()

const (
	markParams = "mark mark-alpha mark-align mark-pad mark-scale mark-w mark-h mark-x mark-y mark-fit mark-tile mark-rot mark-base"
	txtParams  = "txt txt-font txt-size txt-color txt-align txt-pad txt-shad txt-width txt-fit txt-clip txt-lead txt-track txt-line txt-line-color"
)

// aliases maps legacy imgix parameter spellings to the current names.
var aliases = map[string]string{
	"markalign": "mark-align", "markalpha": "mark-alpha", "markpad": "mark-pad", "markscale": "mark-scale",
	"markfit": "mark-fit", "marktile": "mark-tile", "markw": "mark-w", "markh": "mark-h", "markx": "mark-x",
	"marky": "mark-y", "markbase": "mark-base", "txtfont": "txt-font", "txtclr": "txt-color", "txtcolor": "txt-color",
	"txtsize": "txt-size", "txtalign": "txt-align", "txtpad": "txt-pad", "txtshad": "txt-shad", "txtclip": "txt-clip",
	"txtfit": "txt-fit", "txtwidth": "txt-width", "txtlead": "txt-lead", "txttrack": "txt-track",
	"txtline": "txt-line", "txtlineclr": "txt-line-color", "trimcolor": "trim-color", "trimtol": "trim-tol",
	"trimmd": "trim-md", "trimsd": "trim-sd",
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
		switch paramKinds[key] {
		case kindKnown:
			st.vals[key] = kv.value
		case kindSilent:
		case kindUnsupported:
			if st.p.o.Strict {
				return dialect.UnknownOption(key)
			}
			st.b.Ignore(key, reasonUnsupported)
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
