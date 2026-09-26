package imgix

import (
	"html"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// --- watermarks (WP-77/78) --------------------------------------------------

// applyWatermarks maps mark (image watermark, source URL or a path on the
// mount's source, optionally under mark-base) and txt (text watermark) with
// their placement options. Parameters without an equivalent are recorded:
// mark-fit other than clip/max, txt-fit (xi wraps at txt-width instead of
// shrinking), and the typography parameters txt-clip, txt-lead, txt-track,
// txt-line and txt-line-color. Parameters of an absent mark or txt are
// recorded as needing it.
func (st *state) applyWatermarks() error {
	if err := st.imageWatermark(); err != nil {
		return err
	}
	if err := st.textWatermark(); err != nil {
		return err
	}
	if !st.has("mark") {
		for k := range strings.FieldsSeq(markParams) {
			st.ignoreIf(reasonNeedsParent, k)
		}
	}
	if !st.has("txt") {
		for k := range strings.FieldsSeq(txtParams) {
			st.ignoreIf(reasonNeedsParent, k)
		}
	}
	return nil
}

// markSource is mark as a source: an absolute http(s) URL, a path on the
// mount's source, or a path joined to mark-base (an http(s) URL prefix).
func (st *state) markSource(v string) (*transform.SourceSpec, error) {
	if v == "" || !imgproxy.PlausibleSource(v) {
		return nil, dialect.InvalidArgument("mark", v, "image url or path")
	}
	if isHTTP(v) {
		return &transform.SourceSpec{Raw: v, Encoding: transform.SourcePlain}, nil
	}
	path := strings.TrimLeft(v, "/")
	if path == "" {
		return nil, dialect.InvalidArgument("mark", v, "image url or path")
	}
	if base, ok := st.get("mark-base"); ok {
		if !isHTTP(base) || !imgproxy.PlausibleSource(base) {
			return nil, dialect.InvalidArgument("mark-base", base, "http(s) url")
		}
		return &transform.SourceSpec{Raw: strings.TrimRight(base, "/") + "/" + path, Encoding: transform.SourcePlain}, nil
	}
	return &transform.SourceSpec{Raw: path, Encoding: transform.SourceRelative}, nil
}

func isHTTP(v string) bool {
	l := strings.ToLower(v)
	return strings.HasPrefix(l, "http:") || strings.HasPrefix(l, "https:")
}

func (st *state) imageWatermark() error {
	v, ok := st.get("mark")
	if !ok {
		return nil
	}
	src, err := st.markSource(v)
	if err != nil {
		return err
	}
	w := transform.Watermark{Source: src}
	if err := st.watermarkCommon(&w, "mark", 5); err != nil {
		return err
	}
	scale, err := st.optNum("mark-scale", 0, 100, 0)
	if err != nil {
		return err
	}
	w.Scale = scale / 100
	for _, d := range []struct {
		key string
		dst *int
	}{{"mark-w", &w.Width}, {"mark-h", &w.Height}} {
		if v, ok := st.get(d.key); ok {
			x, err := dim(d.key, v)
			if err != nil {
				return err
			}
			if x.Px == 0 {
				st.b.Ignore(d.key, reasonApproximated)
			}
			*d.dst = x.Px
		}
	}
	if err := st.watermarkXY(&w, "mark"); err != nil {
		return err
	}
	if v, ok := st.get("mark-tile"); ok {
		if v != "grid" {
			return dialect.InvalidArgument("mark-tile", v, "grid")
		}
		w.Tile = transform.TileRepeat
	}
	if v, ok := st.get("mark-rot"); ok {
		deg, err := numRange("mark-rot", v, -359, 359)
		if err != nil {
			return err
		}
		w.Rotate = -deg
	}
	for _, k := range []string{"mark-if-min-width", "mark-if-min-height"} {
		if v, ok := st.get(k); ok {
			if _, err := intRange(k, v, 0, transform.MaxDimension); err != nil {
				return err
			}
			// A watermark shown only on large enough results: xi always
			// draws it.
			st.b.Ignore(k, reasonNotImplemented)
		}
	}
	if v, ok := st.get("mark-fit"); ok && v != "clip" && v != "max" {
		// xi always fits inside mark-w×mark-h (clip); crop, fill and scale
		// have no watermark equivalent.
		st.b.Ignore("mark-fit", reasonApproximated)
	}
	st.pl.Watermarks = append(st.pl.Watermarks, w)
	return nil
}

func (st *state) textWatermark() error {
	v, ok := st.get("txt")
	if !ok {
		return nil
	}
	if v == "" || !imgproxy.PlausibleSource(v) {
		return dialect.InvalidArgument("txt", v, expText)
	}
	// imgix txt is plain text; Watermark.Text is Pango markup.
	w := transform.Watermark{Text: html.EscapeString(v)}
	if err := st.watermarkCommon(&w, "txt", 10); err != nil {
		return err
	}
	if err := st.watermarkXY(&w, "txt"); err != nil {
		return err
	}
	font, _ := st.get("txt-font")
	font = strings.TrimSpace(strings.ReplaceAll(font, ",", " "))
	if font == "" {
		font = "sans"
	}
	size := 12 // imgix's default txt-size
	if v, ok := st.get("txt-size"); ok {
		n, err := intRange("txt-size", v, 1, 1000)
		if err != nil {
			return err
		}
		size = n
	}
	w.Font = font + " " + strconv.Itoa(size)
	if v, ok := st.get("txt-width"); ok {
		n, err := intRange("txt-width", v, 1, transform.MaxDimension)
		if err != nil {
			return err
		}
		w.TextWidth = n
	}
	st.ignoreIf(reasonApproximated, "txt-fit")
	st.ignoreIf(reasonUnsupported, "txt-clip", "txt-lead", "txt-track", "txt-line", "txt-line-color")
	if v, ok := st.get("txt-color"); ok {
		c, err := parseColor("txt-color", v)
		if err != nil {
			return err
		}
		w.Color = &c
	}
	shadow, err := st.optNum("txt-shad", 0, 10, 0)
	if err != nil {
		return err
	}
	w.Shadow = shadow
	st.pl.Watermarks = append(st.pl.Watermarks, w)
	return nil
}

// watermarkXY maps <prefix>-x and <prefix>-y (offsets from the top-left
// corner, >= 1 pixels, < 1 fractions): either one replaces the -align/-pad
// placement with a north-west position.
func (st *state) watermarkXY(w *transform.Watermark, prefix string) error {
	vx, hasX := st.get(prefix + "-x")
	vy, hasY := st.get(prefix + "-y")
	if !hasX && !hasY {
		return nil
	}
	var x, y float64
	var err error
	if hasX {
		if x, err = offset(prefix+"-x", vx); err != nil {
			return err
		}
	}
	if hasY {
		if y, err = offset(prefix+"-y", vy); err != nil {
			return err
		}
	}
	w.Position = transform.Gravity{Type: transform.GravityNorthWest, XOffset: x, YOffset: y}
	return nil
}

// watermarkCommon maps <prefix>-alpha (0–100, default 100), <prefix>-align
// (default bottom,right) and <prefix>-pad (default padDef px).
func (st *state) watermarkCommon(w *transform.Watermark, prefix string, padDef float64) error {
	alpha, err := st.optNum(prefix+"-alpha", 0, 100, 100)
	if err != nil {
		return err
	}
	w.Opacity = alpha / 100
	hor, ver := 1, 1
	if v, ok := st.get(prefix + "-align"); ok {
		hor, ver = 0, 0
		for _, tok := range list(v) {
			switch strings.TrimSpace(tok) {
			case "top":
				ver = -1
			case "middle":
				ver = 0
			case "bottom":
				ver = 1
			case "left":
				hor = -1
			case "center":
				hor = 0
			case "right":
				hor = 1
			default:
				return dialect.InvalidArgument(prefix+"-align", v, expAlign)
			}
		}
	}
	pad, err := st.optNum(prefix+"-pad", 0, transform.MaxDimension, padDef)
	if err != nil {
		return err
	}
	pad = math.Round(pad)
	w.Position = transform.Gravity{Type: compass(hor, ver)}
	if hor != 0 {
		w.Position.XOffset = pad
	}
	if ver != 0 {
		w.Position.YOffset = pad
	}
	return nil
}
