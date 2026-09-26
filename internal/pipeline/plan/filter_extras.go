package plan

import "github.com/xantiagoma/xtandard-image/internal/transform"

// imgproxy's unsharp_masking defaults (IMGPROXY_UNSHARP_MASKING_WEIGHT and
// _DIVIDER).
const (
	defaultUnsharpWeight  = 1
	defaultUnsharpDivider = 24
	// defaultUnsharpSigma is the classic mask's sigma when only an amount is
	// given (imgix usmrad defaults to 2.5 in the dialect; this is a
	// fallback).
	defaultUnsharpSigma = 1
)

// filterStep is the FilterStep of p after geometry, ok false when it does
// nothing.
func filterStep(p *transform.Pipeline, b *builder) (FilterStep, bool) {
	fs := FilterStep{Blur: p.Blur, Sharpen: p.Sharpen, Pixelate: p.Pixelate}
	fs.Unsharp = unsharpMask(p, b)
	if ba := p.BlurAreas; ba != nil && ba.Sigma > 0 {
		if areas := mapAreas(ba.Areas, b); len(areas) > 0 {
			fs.BlurAreas = &BlurAreas{Sigma: ba.Sigma, Areas: areas}
		}
	}
	if pb := p.ProgressiveBlur; pb != nil && pb.Sigma > 0 {
		c := *pb
		fs.ProgressiveBlur = &c
	}
	fs.Detections = detectionBlur(p, b)
	ok := fs.Blur > 0 || fs.Sharpen > 0 || fs.Pixelate > 1 || fs.Unsharp != nil || fs.BlurAreas != nil ||
		fs.ProgressiveBlur != nil || fs.Detections != nil
	return fs, ok
}

// unsharpMask resolves p.Unsharp. An unset mode is imgproxy's default,
// auto, for unsharp_masking, and always for the classic parameters (imgix
// usm, ImageKit e-usm ask for sharpening explicitly). Auto applies only
// when the pipeline downscales and sharpen is not set (imgproxy docs).
func unsharpMask(p *transform.Pipeline, b *builder) *UnsharpMask {
	u := p.Unsharp
	if u == nil {
		return nil
	}
	classic := u.Sigma > 0 || u.Amount != 0
	mode := u.Mode
	if mode == transform.UnsharpUnset {
		mode = transform.UnsharpAuto
		if classic {
			mode = transform.UnsharpAlways
		}
	}
	switch mode {
	case transform.UnsharpNone:
		return nil
	case transform.UnsharpAuto:
		if p.Sharpen > 0 || !b.downscaled() {
			return nil
		}
	default:
	}
	if classic {
		if u.Amount == 0 {
			return nil
		}
		sigma := u.Sigma
		if sigma <= 0 {
			sigma = defaultUnsharpSigma
		}
		return &UnsharpMask{Sigma: sigma, Amount: u.Amount}
	}
	m := &UnsharpMask{Weight: u.Weight, Divider: u.Divider}
	if m.Weight <= 0 {
		m.Weight = defaultUnsharpWeight
	}
	if m.Divider <= 0 {
		m.Divider = defaultUnsharpDivider
	}
	return m
}

// downscaled reports whether the load or a geometry resize made the image
// smaller than its source on either axis.
func (b *builder) downscaled() bool {
	l := &b.pl.Load
	if l.ShrinkOnLoad > 1 || (l.Scale > 0 && l.Scale < 1) || l.Thumbnail {
		return true
	}
	for _, s := range b.pl.Steps[b.geoFrom:] {
		if r, ok := s.(ResizeStep); ok && (r.ScaleX < 1 || r.ScaleY < 1) {
			return true
		}
	}
	return false
}

// relRect is a rectangle in fractions of the image: [x0, x1) × [y0, y1).
type relRect struct{ x0, y0, x1, y1 float64 }

// rot rotates r clockwise by angle (a multiple of 90) with the image.
func (r relRect) rot(angle int) relRect {
	switch (angle%360 + 360) % 360 {
	case 90: // (x, y) → (1 − y, x)
		return relRect{1 - r.y1, r.x0, 1 - r.y0, r.x1}
	case 180:
		return relRect{1 - r.x1, 1 - r.y1, 1 - r.x0, 1 - r.y0}
	case 270: // (x, y) → (y, 1 − x)
		return relRect{r.y0, 1 - r.x1, r.y1, 1 - r.x0}
	}
	return r
}

// flip mirrors r with the image.
func (r relRect) flip(horizontal, vertical bool) relRect {
	if horizontal {
		r.x0, r.x1 = 1-r.x1, 1-r.x0
	}
	if vertical {
		r.y0, r.y1 = 1-r.y1, 1-r.y0
	}
	return r
}

// crop maps r on a w×h image into the cw×ch crop at (x, y).
func (r relRect) crop(w, h, x, y, cw, ch int) relRect {
	fx := func(v float64) float64 { return (v*float64(w) - float64(x)) / float64(cw) }
	fy := func(v float64) float64 { return (v*float64(h) - float64(y)) / float64(ch) }
	return relRect{fx(r.x0), fy(r.y0), fx(r.x1), fy(r.y1)}
}

// mapAreas maps areas given relative to the source (imgproxy blur_areas:
// after the EXIF orientation, before the rotate and flip options, which
// move them with the image) onto the image after the geometry steps. Areas
// that end up outside the image are dropped; the others are clipped to it.
// A smart crop's position is only known when it runs; areas are mapped
// through its predicted rectangle (at the origin).
func mapAreas(areas []transform.Area, b *builder) []transform.Area {
	out := make([]transform.Area, 0, len(areas))
	for _, a := range areas {
		if m, ok := mapArea(a, b); ok {
			out = append(out, m)
		}
	}
	return out
}

// mapArea maps one source-relative area like mapAreas; ok is false when it
// ends up outside the image.
func mapArea(a transform.Area, b *builder) (transform.Area, bool) {
	r := relRect{a.Left, a.Top, a.Left + a.Width, a.Top + a.Height}
	// Oriented → stored: the executor rotates by angle, then flips.
	r = r.flip(b.exifFlip, false).rot(-b.exifAngle)
	w, h := b.geoW, b.geoH
	for _, s := range b.pl.Steps[b.geoFrom:] {
		switch s := s.(type) {
		case PreCropStep:
			r = r.crop(w, h, s.X, s.Y, s.W, s.H)
			w, h = s.W, s.H
		case CropStep:
			r = r.crop(w, h, s.X, s.Y, s.W, s.H)
			w, h = s.W, s.H
		case ResizeStep:
			w, h = s.W, s.H
		case RotateStep:
			r = r.rot(s.Angle)
			if s.Angle%180 != 0 {
				w, h = h, w
			}
		case FlipStep:
			r = r.flip(s.Horizontal, s.Vertical)
		default:
		}
	}
	r.x0, r.y0 = max(r.x0, 0), max(r.y0, 0)
	r.x1, r.y1 = min(r.x1, 1), min(r.y1, 1)
	if r.x1 <= r.x0 || r.y1 <= r.y0 {
		return transform.Area{}, false
	}
	return transform.Area{Left: r.x0, Top: r.y0, Width: r.x1 - r.x0, Height: r.y1 - r.y0}, true
}
