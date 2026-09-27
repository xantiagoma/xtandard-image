package translate

import (
	"math"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var thumborFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpeg", transform.FormatPNG: "png", transform.FormatWebP: "webp", transform.FormatGIF: "gif",
	transform.FormatAVIF: "avif", transform.FormatHEIC: "heic",
}

// emitThumbor writes /[AxB:CxD/][fit-in/][-]Wx[-]H/[h/][v/][smart/][filters:…/]<image>.
//
//nolint:gocyclo,funlen // one flat walk over the pipeline model
func emitThumbor(e *emitter) (string, string, error) {
	image := escapePlain(e.src.abs)
	if k, ok := e.src.keyOn(e.mountSource); ok {
		image = k
	}
	p := e.pl()
	var segs, filters []string
	f := func(s string) { filters = append(filters, s) }
	if c := p.Crop; c != nil && c.Width.Px > 0 && c.Height.Px > 0 && c.AspectRatio == 0 &&
		(c.Gravity.Type == transform.GravityNorthWest || isDefaultGravity(&c.Gravity)) &&
		c.Gravity.XOffset == math.Trunc(c.Gravity.XOffset) && c.Gravity.YOffset == math.Trunc(c.Gravity.YOffset) {
		x, y := int(c.Gravity.XOffset), int(c.Gravity.YOffset)
		segs = append(segs, itoa(x)+"x"+itoa(y)+":"+itoa(x+c.Width.Px)+"x"+itoa(y+c.Height.Px))
	}
	rs := &p.Resize
	switch {
	case rs.Type == transform.ResizeOutside:
		segs = append(segs, "full-fit-in")
	case rs.Type == transform.ResizeFit || rs.Type == transform.ResizeUnset:
		if !rs.Width.IsZero() || !rs.Height.IsZero() {
			segs = append(segs, "fit-in")
		}
		if rs.Enlarge {
			f("upscale()")
		}
	case rs.Type == transform.ResizeFill && !rs.Enlarge, rs.Type == transform.ResizeFillDown:
		f("no_upscale()")
	case rs.Type == transform.ResizeForce:
		f("stretch()")
	}
	if rs.Adaptive && len(segs) > 0 && strings.HasSuffix(segs[len(segs)-1], "fit-in") {
		segs[len(segs)-1] = "adaptive-" + segs[len(segs)-1]
	}
	size := func(d transform.Dim, flip bool) string {
		s := "0"
		switch {
		case d.Px > 0:
			s = itoa(d.Px)
		case d.Rel == 1:
			s = "orig"
		}
		if flip {
			s = "-" + s
		}
		return s
	}
	if !rs.Width.IsZero() || !rs.Height.IsZero() || p.Flip.H || p.Flip.V {
		segs = append(segs, size(rs.Width, p.Flip.H)+"x"+size(rs.Height, p.Flip.V))
	}
	switch g := p.Gravity; g.Type {
	case transform.GravitySmart:
		segs = append(segs, "smart")
	case transform.GravityFocusPoint:
		if g.FocusPx == nil {
			pt := num(g.FocusX) + "x" + num(g.FocusY)
			f("focal(" + pt + ":" + pt + ")")
		}
	default:
		if w, ok := sideWords[g.Type]; ok && g.XOffset == 0 && g.YOffset == 0 {
			if w[0] != "" {
				segs = append(segs, w[0])
			}
			if w[1] != "" {
				segs = append(segs, w[1])
			}
		}
	}
	if (rs.ZoomX != 0 && rs.ZoomX != 1) && rs.ZoomX == rs.ZoomY && rs.ZoomX < 1 {
		f("proportion(" + num(rs.ZoomX) + ")")
	}
	if p.Rotate != 0 {
		f("rotate(" + itoa((360-p.Rotate)%360) + ")")
	}
	a := &p.Adjust
	if a.Brightness != nil {
		f("brightness(" + num(math.Round(*a.Brightness*100/255)) + ")")
	}
	if a.Contrast != nil && *a.Contrast >= 0 {
		f("contrast(" + num(math.Round(math.Sqrt(*a.Contrast)*100-100)) + ")")
	}
	if a.Saturation != nil {
		f("saturation(" + num(round4((*a.Saturation-1)*100)) + ")")
	}
	if a.Red != nil || a.Green != nil || a.Blue != nil {
		pct := func(v *float64) string {
			if v == nil {
				return "0"
			}
			return num(math.Round((*v - 1) * 100))
		}
		f("rgb(" + pct(a.Red) + "," + pct(a.Green) + "," + pct(a.Blue) + ")")
	}
	if p.Grayscale {
		f("grayscale()")
	}
	if p.Equalize {
		f("equalize()")
	}
	if bg := p.Background; bg != nil {
		if p.Extend.Enabled {
			f("fill(" + hexRGB(*bg) + ")")
		} else {
			f("background_color(" + hexRGB(*bg) + ")")
		}
	}
	if p.Blur > 0 {
		f("blur(" + num(math.Max(1, math.Ceil(p.Blur))) + "," + num(p.Blur) + ")")
	}
	if u := p.Unsharp; u != nil && u.Mode == transform.UnsharpUnset && u.Amount > 0 && u.Sigma > 0 {
		f("sharpen(" + num(u.Amount) + "," + num(u.Sigma) + ",false)")
	}
	out := &e.r.Output
	if fm, ok := thumborFormats[out.Format]; ok {
		f("format(" + fm + ")")
	}
	if out.Quality > 0 {
		f("quality(" + itoa(out.Quality) + ")")
	}
	if out.MaxBytes > 0 {
		f("max_bytes(" + itoa(out.MaxBytes) + ")")
	}
	if out.PreferJPEGIfOpaque {
		f("autojpg()")
	}
	if e.r.Delivery.TTL > 0 {
		f("max_age(" + itoa64(int64(e.r.Delivery.TTL/time.Second)) + ")")
	}
	if len(filters) > 0 {
		segs = append(segs, "filters:"+strings.Join(filters, ":"))
	}
	return "/" + strings.Join(append(segs, image), "/"), "", nil
}
