package cloudinary

import (
	"encoding/base64"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// expTextLayer is the l_text grammar.
const expTextLayer = "text:<font family>_<size>[_<style>…]:<text>"

// layer turns an image or text overlay into a watermark on the current
// pipeline. comps is the layer group: the l_ component alone (legacy
// syntax, where placement and layer parameters share the component), or
// the l_ component, the components transforming the layer and the one
// carrying fl_layer_apply.
//
// Mapped: g_ → position, x_/y_ → offsets, o_ → opacity, a_ → rotation
// (clockwise), w_/h_ in pixels → size (a text layer's w_ is its wrap
// width), a relative w_ with fl_relative → scale of the base image,
// fl_tiled → repeat, co_ → text colour. l_<public id> is an image on the
// mount's source (the same storage as the base image), l_fetch:<base64
// url> a remote image; l_text:<style>:<text> renders plain text (escaped
// for Pango) in the style's font. Other parameters of the group are
// recorded as approximated. Video, subtitle, LUT and audio layers are
// unsupported.
func (a *applier) layer(key, v string, comps []*component) error {
	kind, rest, _ := strings.Cut(v, ":")
	wm := transform.Watermark{Opacity: 1}
	text := false
	switch kind {
	case "video", "subtitles", "lut", "audio":
		return a.skip(key+"_"+kind, reasonUnsupported)
	case "text":
		if err := a.textLayer(&wm, rest); err != nil {
			return err
		}
		text = true
	case "fetch":
		s, err := layerFetch(rest)
		if err != nil {
			return err
		}
		wm.Source = &transform.SourceSpec{Raw: s, Encoding: transform.SourcePlain}
	default:
		id := v
		if strings.HasPrefix(v, "image:") {
			id = rest
		}
		key := strings.ReplaceAll(id, ":", "/")
		if checkKey(key) != nil || !imgproxy.PlausibleSource(key) {
			return dialect.InvalidArgument("l", v, expLayer)
		}
		wm.Source = &transform.SourceSpec{Raw: key, Encoding: transform.SourceRelative, Mount: a.mount}
	}

	var (
		w, h     dimArg
		relative bool
		gx, gy   float64
		hasXY    bool
		gravity  string
	)
	for _, c := range comps {
		relative = relative || c.hasFlag("relative")
		for _, p := range c.params {
			var err error
			switch p.key {
			case "l":
			case "g":
				gravity = p.value
			case "x":
				gx, err = parseFloat("x", p.value)
				hasXY = true
			case "y":
				gy, err = parseFloat("y", p.value)
				hasXY = true
			case "o":
				var o *float64
				if err = opacity(&o, p.value); err == nil {
					wm.Opacity = *o
				}
			case "a":
				var deg float64
				if deg, err = strconv.ParseFloat(p.value, 64); err != nil || deg != deg || deg < -360 || deg > 360 {
					err = a.skip("l_a", reasonApproximated)
				} else {
					wm.Rotate = deg
				}
			case "co":
				if !text {
					err = a.skip("l_co", reasonApproximated)
					break
				}
				var col transform.Color
				if col, err = parseColor("co", p.value); err == nil {
					wm.Color = &col
				}
			case "w":
				w, err = a.parseDim("w", p.value)
			case "h":
				h, err = a.parseDim("h", p.value)
			case "fl":
				for f := range strings.SplitSeq(p.value, ".") {
					switch flagName(f) {
					case "tiled":
						wm.Tile = transform.TileRepeat
					case "layer_apply", "relative", "no_overflow":
					default:
						err = a.skip("l_fl_"+truncate(flagName(f)), reasonApproximated)
					}
					if err != nil {
						break
					}
				}
			case "c":
				if p.value != "scale" && p.value != "fit" && p.value != "limit" {
					err = a.skip("l_c_"+truncate(p.value), reasonApproximated)
				}
			default:
				err = a.skip("l_"+optionName(p.key, p.value), reasonApproximated)
			}
			if err != nil {
				return err
			}
		}
	}
	if gravity != "" || hasXY {
		gr, err := a.gravity(&geo{gravity: gravity, x: gx, y: gy, hasX: hasXY, hasY: hasXY})
		if err != nil {
			return err
		}
		if gr.Type.IsDetection() || gr.Type == transform.GravitySmart || gr.Type == transform.GravityFocusPoint {
			gr = transform.Gravity{Type: transform.GravityCenter}
			if err := a.skip("l_g", reasonApproximated); err != nil {
				return err
			}
		}
		wm.Position = gr
	}
	switch {
	case w.set && w.dim.Rel > 0 && relative:
		wm.Scale = w.dim.Rel
	case w.set && w.dim.Px > 0 && text:
		wm.TextWidth = w.dim.Px // text wraps at w_ (c_fit)
	case w.set && w.dim.Px > 0:
		wm.Width = w.dim.Px
	case w.set:
		if err := a.skip("l_w", reasonApproximated); err != nil {
			return err
		}
	}
	if h.set && h.dim.Px > 0 && !text {
		wm.Height = h.dim.Px
	} else if h.set {
		if err := a.skip("l_h", reasonApproximated); err != nil {
			return err
		}
	}
	a.used = true
	pl := a.b.Pipeline()
	if len(pl.Watermarks) >= transform.MaxWatermarks {
		return dialect.InvalidURL("too many layers")
	}
	pl.Watermarks = append(pl.Watermarks, wm)
	return nil
}

// textLayer parses l_text:<style>:<text>. The style is
// <family>_<size>[_<token>…]: bold, italic and the other weights and
// styles join the font description; underline and strikethrough wrap the
// text in markup; alignment, stroke, letter and line spacing, antialias and
// hinting are recorded as approximated. The text was percent-decoded with
// the component; Cloudinary double-escapes commas and slashes (%252C), so
// it is decoded once more. It is plain text: markup characters are escaped.
func (a *applier) textLayer(wm *transform.Watermark, spec string) error {
	style, text, ok := strings.Cut(spec, ":")
	if !ok || text == "" {
		return dialect.InvalidArgument("l_text", spec, expTextLayer)
	}
	if t, err := url.PathUnescape(text); err == nil {
		text = t
	}
	tokens := strings.Split(style, "_")
	if len(tokens) < 2 {
		return dialect.InvalidArgument("l_text", style, expTextLayer)
	}
	family := strings.TrimSpace(tokens[0])
	size, err := strconv.Atoi(tokens[1])
	if family == "" || err != nil || size < 1 || size > 1000 {
		return dialect.InvalidArgument("l_text", style, expTextLayer)
	}
	font := []string{family}
	var open, closeTags string
	for i := 2; i < len(tokens); i++ {
		switch t := strings.ToLower(tokens[i]); t {
		case "bold", "semibold", "light", "thin", "heavy", "black", "medium", "italic", "oblique":
			font = append(font, t)
		case "normal":
		case "underline":
			open, closeTags = open+"<u>", "</u>"+closeTags
		case "strikethrough":
			open, closeTags = open+"<s>", "</s>"+closeTags
		case "letter", "line":
			// letter_spacing_<n>, line_spacing_<n>
			if err := a.skip("l_text_"+t+"_spacing", reasonApproximated); err != nil {
				return err
			}
			i += 2
		case "antialias", "hinting":
			if err := a.skip("l_text_"+t, reasonApproximated); err != nil {
				return err
			}
			i++
		default:
			// left, center, right, justify, start, end, stroke, font_* …
			if err := a.skip("l_text_"+truncate(t), reasonApproximated); err != nil {
				return err
			}
		}
	}
	wm.Text = open + html.EscapeString(text) + closeTags
	wm.Font = strings.Join(font, " ") + " " + strconv.Itoa(size)
	return nil
}

// layerFetch decodes l_fetch:<base64 url> (standard or URL-safe alphabet,
// padding optional, as the SDKs emit it).
func layerFetch(s string) (string, error) {
	s = strings.TrimRight(s, "=")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return "", dialect.InvalidArgument("l", s, expLayer)
	}
	u, ok := fetchURL(string(b))
	if !ok || !imgproxy.PlausibleSource(u) {
		return "", dialect.InvalidArgument("l", s, expLayer)
	}
	return u, nil
}
