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

// --- watermarks (M3) --------------------------------------------------------

// applyWatermarks maps mark (image watermark, source URL or a path on the
// mount's source) and txt (text watermark) with their placement options.
// Both take effect in M3; every mark*/txt* parameter is recorded.
func (st *state) applyWatermarks() error {
	if err := st.imageWatermark(); err != nil {
		return err
	}
	if err := st.textWatermark(); err != nil {
		return err
	}
	for k := range strings.FieldsSeq(markParams + " " + txtParams) {
		st.ignoreIf(reasonM3, k)
	}
	return nil
}

func (st *state) imageWatermark() error {
	v, ok := st.get("mark")
	if !ok {
		return nil
	}
	if v == "" || !imgproxy.PlausibleSource(v) {
		return dialect.InvalidArgument("mark", v, "image url or path")
	}
	src := &transform.SourceSpec{Raw: v, Encoding: transform.SourcePlain}
	if l := strings.ToLower(v); !strings.HasPrefix(l, "http:") && !strings.HasPrefix(l, "https:") {
		src.Raw, src.Encoding = strings.TrimLeft(v, "/"), transform.SourceRelative
		if src.Raw == "" {
			return dialect.InvalidArgument("mark", v, "image url or path")
		}
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
	if v, ok := st.get("mark-x"); ok {
		x, err := offset("mark-x", v)
		if err != nil {
			return err
		}
		w.Position = transform.Gravity{Type: transform.GravityNorthWest, XOffset: x, YOffset: w.Position.YOffset}
	}
	if v, ok := st.get("mark-y"); ok {
		y, err := offset("mark-y", v)
		if err != nil {
			return err
		}
		x := w.Position.XOffset
		if !st.has("mark-x") {
			x = 0
		}
		w.Position = transform.Gravity{Type: transform.GravityNorthWest, XOffset: x, YOffset: y}
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
	font, _ := st.get("txt-font")
	if v, ok := st.get("txt-size"); ok {
		n, err := intRange("txt-size", v, 1, 1000)
		if err != nil {
			return err
		}
		if font == "" {
			font = "sans"
		}
		font += " " + strconv.Itoa(n)
	}
	w.Font = font
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
