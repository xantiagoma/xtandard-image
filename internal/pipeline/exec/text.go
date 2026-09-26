package exec

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/security/pango"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Text watermark defaults.
const (
	// DefaultWatermarkFont is imgproxy's text watermark font.
	DefaultWatermarkFont = "sans 16"
	// DefaultMaxTextPixels bounds the area text is rendered on (4096²).
	DefaultMaxTextPixels = 1 << 24
	// maxFontSize bounds every font size in points.
	maxFontSize = 1000
)

// WatermarkConfig configures watermarks beyond the configured image.
type WatermarkConfig struct {
	// Opacity multiplies every watermark's opacity
	// (XI_PROCESSING_WATERMARK_OPACITY); nil falls back to the configured
	// Watermark's, else 1.
	Opacity *float64
	// Font is the Pango font description of text watermarks that name no
	// font, and supplies the family or size a request's font lacks
	// (XI_PROCESSING_WATERMARK_FONT); "" is DefaultWatermarkFont.
	Font string
	// FontFile is a font file registered with fontconfig before the first
	// text is rendered (XI_PROCESSING_WATERMARK_FONT_FILE); "" registers
	// nothing. The fonts under the image's fontconfig directory are always
	// available.
	FontFile string
	// MaxTextPixels bounds the area text is rendered on
	// (XI_LIMITS_MAX_WATERMARK_TEXT_PIXELS); 0 is DefaultMaxTextPixels.
	MaxTextPixels int64
}

// renderText renders a text watermark for a w×fh frame. The markup and
// font come from the URL: they are sanitised first (security/pango), so
// only whitelisted markup with bounded font sizes reaches Pango.
//
// Text wraps at t.Width (the frame width when 0) and must fit the frame:
// larger text is shrunk until it fits (vips_text autofit). The box is
// also capped at MaxTextPixels, so the rendered image is bounded whatever
// the markup. When the markup's size bound proves the text fits, it is
// rendered once at t.DPI; otherwise it is laid out to fit the box first
// and rendered again at t.DPI only when that is smaller.
func (r *runner) renderText(ctx context.Context, t *plan.TextWatermark, w, fh int) (*vips.Image, error) {
	cfg := &r.e.cfg.Watermarks
	font, err := r.e.textFont(t.Font)
	if err != nil {
		return nil, err
	}
	m, err := pango.Sanitize(t.Markup, font.Size, pango.Options{MaxSize: maxFontSize})
	if err != nil {
		return nil, textError(err)
	}
	if m.Visible == 0 {
		r.skip("watermark_text_empty")
		return nil, nil
	}
	markup := m.Text
	if c := t.Color; c != nil {
		markup = `<span foreground="#` + hex2(c.R) + hex2(c.G) + hex2(c.B) + `" fgalpha="` +
			strconv.Itoa(max(int(c.A)*257, 1)) + `">` + markup + `</span>`
	}
	dpi := max(t.DPI, 1)
	bw, bh := t.Width, fh
	if bw <= 0 {
		bw = w
	}
	limit := cfg.MaxTextPixels
	if limit <= 0 {
		limit = DefaultMaxTextPixels
	}
	if area := int64(bw) * int64(bh); area > limit {
		f := math.Sqrt(float64(limit) / float64(area))
		bw, bh = max(int(float64(bw)*f), 1), max(int(float64(bh)*f), 1)
	}
	o := vips.TextOptions{Markup: markup, Font: font.String(), FontFile: cfg.FontFile, Width: bw, DPI: dpi}
	// A character is at most 4 em wide and adds at most 2 em of height
	// (a line, or stacked marks): proven to fit, render directly.
	em := m.MaxSize * float64(dpi) / 72
	if 2*float64(max(m.Runes, 1))*em <= float64(bh) && 4*em <= float64(bw) {
		img, _, err := vips.Text(ctx, &o)
		return r.a.Add(img, textError(err))
	}
	o.Height = bh
	img, fit, err := vips.Text(ctx, &o)
	if err != nil {
		if errs.KindOf(err) == errs.KindCanceled || errs.KindOf(err) == errs.KindTimeout {
			return nil, err
		}
		// Even the smallest resolution overflows the renderer: far too
		// much text for the frame.
		return nil, errTextTooLarge(err)
	}
	if img, err = r.a.Add(img, nil); err != nil {
		return nil, err
	}
	if int64(img.Width())*int64(img.Height()) > 4*int64(bw)*int64(bh) {
		// Autofit stops at 1 DPI; text that still does not fit is refused
		// rather than drawn far outside the frame.
		return nil, errTextTooLarge(nil)
	}
	if fit < dpi {
		return img, nil // shrunk to fit the box
	}
	o.Height = 0
	img, _, err = vips.Text(ctx, &o)
	return r.a.Add(img, textError(err))
}

// textFont resolves a request's font description against the configured
// one: a missing family or size comes from XI_PROCESSING_WATERMARK_FONT.
func (e *Executor) textFont(desc string) (pango.Font, error) {
	def := e.cfg.Watermarks.Font
	if def == "" {
		def = DefaultWatermarkFont
	}
	base, err := pango.ParseFont(def, maxFontSize)
	if err != nil {
		return pango.Font{}, errs.Wrapf(errs.KindInternal, err, "exec: watermark font")
	}
	if base.Size == 0 {
		base.Size = 16
	}
	if len(base.Words) == 0 {
		base.Words = []string{"sans"}
	}
	if desc == "" {
		return base, nil
	}
	f, err := pango.ParseFont(desc, maxFontSize)
	if err != nil {
		return pango.Font{}, textError(err)
	}
	if f.Size == 0 {
		f.Size = base.Size
	}
	if len(f.Words) == 0 {
		f.Words = base.Words
	}
	return f, nil
}

// textError classifies a text failure: markup outside the whitelist is a
// client error, markup over the limits 422.
func textError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pango.ErrTooLarge):
		return errs.WithPublic(errs.Wrap(errs.KindLimitExceeded, err), "watermark text exceeds limits")
	case errors.Is(err, pango.ErrInvalid):
		return errs.WithPublic(errs.Wrap(errs.KindInvalidURL, err), "invalid watermark text")
	default:
		return err
	}
}

// errTextTooLarge is text that does not fit the frame even when shrunk.
func errTextTooLarge(err error) error {
	e := errs.Newf(errs.KindLimitExceeded, "exec: watermark text does not fit the image")
	if err != nil {
		e = errs.Wrapf(errs.KindLimitExceeded, err, "exec: watermark text does not fit the image")
	}
	return errs.WithPublic(e, "watermark text does not fit the image")
}

func hex2(v uint8) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>4], digits[v&15]})
}
