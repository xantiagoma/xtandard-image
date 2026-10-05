package imagekit

import (
	"encoding/base64"
	"html"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Expectation phrases for layers.
const (
	expLayerInput = "i-<path> or ie-<base64 path>"
	expLayerText  = "i-<text> or ie-<base64 text>"
	expFontSize   = "positive integer"
	expBase64     = "base64"
	// Layer rotation, typography and text alpha.
	expLayerRotation = "degrees 0-360, N prefix for counter-clockwise"
	expTypography    = "b, i, strikethrough joined by _"
	expTextAlpha     = "integer 1-9"
)

// canvasInput is the solid-colour layer input ("l-image,i-ik_canvas,bg-…").
const canvasInput = "ik_canvas"

// layerSpec collects the parameters of one l-image or l-text layer.
type layerSpec struct {
	input    string // decoded path or text
	hasInput bool
	lx, ly   optFloat
	lfo      transform.GravityType
	w, h     int
	opacity  float64 // 0..1
	fontSize int
	font     string
	color    *transform.Color
	rotate   float64
	bold     bool
	italic   bool
	strike   bool
}

// layer applies one overlay (params are those between l-<kind> and its
// l-end). l-image and l-text become watermarks: position (lx, ly, lfo),
// size (w, h; a text layer's w is its wrap width), opacity (o; al for
// text), rotation (rt, "N" prefix counter-clockwise), and for text the
// font (ff, fs, tg) and colour (co). Other layer kinds, nested layers and
// the remaining in-layer transformations have no equivalent.
func (ps *parseState) layer(kind string, params []string) error {
	var text bool
	switch kind {
	case "l-image":
	case "l-text":
		text = true
	default:
		return ps.unsupported(kind)
	}
	spec := layerSpec{opacity: 1}
	for i := 0; i < len(params); i++ {
		key, v := splitParam(params[i])
		if strings.HasPrefix(key, "l-") {
			// A nested layer: skip it whole.
			end := layerEnd(params, i)
			if err := ps.unsupportedAs(kind+":"+key, reasonUnsupported); err != nil {
				return err
			}
			i = end
			continue
		}
		if err := ps.layerParam(&spec, kind, text, key, v); err != nil {
			return err
		}
	}
	if !spec.hasInput || spec.input == "" {
		exp := expLayerInput
		if text {
			exp = expLayerText
		}
		return dialect.InvalidArgument(kind, "", exp)
	}
	if !text && spec.input == canvasInput {
		return ps.unsupportedAs(kind+":"+canvasInput, reasonUnsupported)
	}

	pl := ps.b.Pipeline()
	if len(pl.Watermarks) >= transform.MaxWatermarks {
		return dialect.InvalidURL("too many layers")
	}
	wm := transform.Watermark{Opacity: spec.opacity, Position: spec.position(), Rotate: spec.rotate}
	if text {
		t := html.EscapeString(spec.input)
		if spec.strike {
			t = "<s>" + t + "</s>"
		}
		wm.Text = t
		wm.Font = spec.fontDescription()
		wm.Color = spec.color
		wm.TextWidth = spec.w
		if spec.h > 0 {
			ps.b.Ignore(kind+":h", reasonApproximated) // text is as tall as its lines
		}
	} else {
		wm.Width, wm.Height = spec.w, spec.h
		wm.Source = &transform.SourceSpec{Raw: relativeKey(spec.input), Encoding: transform.SourceRelative, Mount: ps.mountSource}
	}
	pl.Watermarks = append(pl.Watermarks, wm)
	return nil
}

// layerParam applies one parameter inside a layer.
func (ps *parseState) layerParam(spec *layerSpec, kind string, text bool, key, v string) error {
	switch key {
	case "i":
		if !utf8.ValidString(v) {
			return dialect.InvalidArgument(kind+".i", v, "UTF-8 text or a path")
		}
		spec.input, spec.hasInput = v, true
		if !text {
			spec.input = decodeLayerPath(v)
		}
	case "ie":
		b, err := decodeBase64(v)
		if err != nil || !utf8.Valid(b) {
			return dialect.InvalidArgument(kind+".ie", v, expBase64)
		}
		spec.input, spec.hasInput = string(b), true
		if !text {
			spec.input = strings.Trim(spec.input, "/")
		}
	case "lx", "ly":
		if isExpression(v) {
			return ps.unsupportedAs(kind+":"+key, reasonExpression)
		}
		f, ok := parseSigned(v, transform.MaxDimension)
		if !ok || f != math.Trunc(f) {
			return dialect.InvalidArgument(kind+"."+key, v, expOffset)
		}
		if key == "lx" {
			spec.lx = optFloat{set: true, v: f}
		} else {
			spec.ly = optFloat{set: true, v: f}
		}
	case "lfo":
		gt, ok := compass[v]
		if !ok {
			return dialect.InvalidArgument(kind+".lfo", v, expPosition)
		}
		spec.lfo = gt
	case "w", "h":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > transform.MaxDimension {
			if isExpression(v) || strings.HasPrefix(v, "0.") {
				return ps.unsupportedAs(kind+":"+key, reasonUnsupported)
			}
			return dialect.InvalidArgument(kind+"."+key, v, expLayerSize)
		}
		if key == "w" {
			spec.w = n
		} else {
			spec.h = n
		}
	case "o":
		f, ok := parseFloatIn(v, 0, 100)
		if !ok {
			return dialect.InvalidArgument(kind+".o", v, expOpacity)
		}
		spec.opacity = f / 100
	case "fs":
		n, err := strconv.Atoi(v)
		if !text || err != nil || n < 1 || n > transform.MaxDimension {
			if !text {
				return ps.unsupportedAs(kind+":"+key, reasonUnsupported)
			}
			return dialect.InvalidArgument(kind+".fs", v, expFontSize)
		}
		spec.fontSize = n
	case "ff":
		if !text {
			return ps.unsupportedAs(kind+":"+key, reasonUnsupported)
		}
		f := decodeLayerPath(v)
		if strings.ContainsAny(f, "/.") {
			// A custom font file on the media library: not fetched; the
			// configured font is used.
			ps.b.Ignore(kind+":ff", reasonApproximated)
			break
		}
		spec.font = f
	case "co":
		if !text {
			return ps.unsupportedAs(kind+":"+key, reasonUnsupported)
		}
		c, ok := parseColor(v)
		if !ok {
			return dialect.InvalidArgument(kind+".co", v, expColor)
		}
		spec.color = &c
	case "rt":
		body, neg := strings.CutPrefix(v, "N")
		f, ok := parseFloatIn(body, 0, 360)
		if !ok {
			if isExpression(v) {
				return ps.unsupportedAs(kind+":"+key, reasonExpression)
			}
			return dialect.InvalidArgument(kind+".rt", v, expLayerRotation)
		}
		if neg {
			f = -f
		}
		spec.rotate = f
	case "tg":
		if !text {
			return ps.unsupportedAs(kind+":"+key, reasonUnsupported)
		}
		for t := range strings.SplitSeq(v, "_") {
			switch t {
			case "b":
				spec.bold = true
			case "i":
				spec.italic = true
			case "strikethrough":
				spec.strike = true
			default:
				return dialect.InvalidArgument(kind+".tg", v, expTypography)
			}
		}
	case "al":
		if !text {
			return ps.unsupportedAs(kind+":"+key, reasonUnsupported)
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 9 {
			return dialect.InvalidArgument(kind+".al", v, expTextAlpha)
		}
		spec.opacity = float64(n) / 10
	default:
		// Positioning variants (lxc, lyc, lap), layer modes, timing,
		// typography and in-layer transformations.
		return ps.unsupportedAs(kind+":"+key, reasonUnsupported)
	}
	return nil
}

// position places the layer: lx/ly offsets from the top-left corner
// (negative "N" values from the right or bottom edge), else lfo, else the
// centre (ImageKit's default).
func (spec *layerSpec) position() transform.Gravity {
	if !spec.lx.set && !spec.ly.set {
		if spec.lfo != transform.GravityUnset {
			return transform.Gravity{Type: spec.lfo}
		}
		return transform.Gravity{Type: transform.GravityCenter}
	}
	right, bottom := spec.lx.v < 0, spec.ly.v < 0
	var gt transform.GravityType
	switch {
	case right && bottom:
		gt = transform.GravitySouthEast
	case right:
		gt = transform.GravityNorthEast
	case bottom:
		gt = transform.GravitySouthWest
	default:
		gt = transform.GravityNorthWest
	}
	return transform.Gravity{Type: gt, XOffset: math.Abs(spec.lx.v), YOffset: math.Abs(spec.ly.v)}
}

// fontDescription renders ff/tg/fs as a Pango font description.
func (spec *layerSpec) fontDescription() string {
	words := make([]string, 0, 4)
	if spec.font != "" {
		words = append(words, spec.font)
	}
	if spec.bold {
		words = append(words, "bold")
	}
	if spec.italic {
		words = append(words, "italic")
	}
	if (spec.bold || spec.italic) && spec.font == "" {
		words = append([]string{"sans"}, words...)
	}
	if spec.fontSize > 0 {
		words = append(words, strconv.Itoa(spec.fontSize))
	}
	return strings.Join(words, " ")
}

// decodeLayerPath turns an ImageKit path value ("folder@@file.png") into
// a path without surrounding slashes.
func decodeLayerPath(v string) string {
	return strings.Trim(strings.ReplaceAll(v, "@@", "/"), "/")
}

// decodeBase64 decodes standard base64 with or without padding (the SDK
// writes padded standard base64; some clients drop the padding).
func decodeBase64(v string) ([]byte, error) {
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(v, "="))
	if err != nil || len(b) == 0 {
		b, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(v, "="))
	}
	if err == nil && len(b) == 0 {
		err = base64.CorruptInputError(0)
	}
	return b, err
}
