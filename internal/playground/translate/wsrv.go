package translate

import (
	"math"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var wsrvCompass = compassNames{
	transform.GravityCenter: "center", transform.GravityNorth: "top", transform.GravitySouth: "bottom",
	transform.GravityEast: "right", transform.GravityWest: "left", transform.GravityNorthEast: "top-right",
	transform.GravityNorthWest: "top-left", transform.GravitySouthEast: "bottom-right", transform.GravitySouthWest: "bottom-left",
}

var wsrvFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpg", transform.FormatPNG: "png", transform.FormatGIF: "gif", transform.FormatTIFF: "tiff",
	transform.FormatWebP: "webp", transform.FormatAVIF: "avif", transform.FormatJXL: "jxl",
}

var wsrvMasks = map[transform.MaskShape]string{
	transform.MaskCircle: "circle", transform.MaskEllipse: "ellipse", transform.MaskTriangle: "triangle",
	transform.MaskTriangle180: "triangle-180", transform.MaskPentagon: "pentagon", transform.MaskPentagon180: "pentagon-180",
	transform.MaskHexagon: "hexagon", transform.MaskSquare: "square", transform.MaskStar: "star", transform.MaskHeart: "heart",
}

// emitWsrv writes /?url=<source>&k=v.
//
//nolint:gocyclo,funlen // one flat walk over the pipeline model
func emitWsrv(e *emitter) (string, string, error) {
	var q params
	if k, ok := e.src.keyOn(e.mountSource); ok {
		q.set("url", k)
	} else {
		q.set("url", e.src.abs)
	}
	p := e.pl()
	rs := &p.Resize
	for _, d := range []struct {
		k string
		v transform.Dim
	}{{"w", rs.Width}, {"h", rs.Height}} {
		switch {
		case d.v.Px > 0:
			q.set(d.k, itoa(d.v.Px))
		case d.v.Rel > 0:
			q.set(d.k, num(round4(d.v.Rel*100))+"%")
		}
	}
	if rs.DPR != 0 && rs.DPR != 1 {
		q.set("dpr", num(rs.DPR))
	}
	sized := !rs.Width.IsZero() || !rs.Height.IsZero()
	switch {
	case p.Extend.Enabled && rs.Type == transform.ResizeFit:
		q.set("fit", "contain")
		if bg := p.Background; bg != nil {
			q.set("cbg", hexARGB(*bg))
		}
	case rs.Type == transform.ResizeFill || rs.Type == transform.ResizeFillDown:
		q.set("fit", "cover")
	case rs.Type == transform.ResizeForce:
		q.set("fit", "fill")
	case rs.Type == transform.ResizeOutside:
		q.set("fit", "outside")
	case sized:
		q.set("fit", "inside")
	}
	if !rs.Enlarge && (sized || rs.DPR > 1) {
		q.set("we", "")
	}
	g := p.Gravity
	if p.Extend.Enabled {
		g = p.Extend.Gravity
	}
	switch g.Type {
	case transform.GravitySmart:
		q.set("a", "attention")
	case transform.GravityEntropy:
		q.set("a", "entropy")
	case transform.GravityFocusPoint:
		if g.FocusPx == nil {
			q.set("a", "focal")
			q.set("fpx", num(g.FocusX))
			q.set("fpy", num(g.FocusY))
		}
	default:
		if n, ok := wsrvCompass[g.Type]; ok && !isDefaultGravity(g) && g.XOffset == 0 && g.YOffset == 0 {
			q.set("a", n)
		}
	}
	if t := p.Trim; t != nil && t.Threshold >= 1 && t.Threshold <= 254 {
		q.set("trim", num(math.Round(t.Threshold)))
		if t.Color != nil {
			q.set("tbg", hexARGB(*t.Color))
		}
	}
	if p.Flip.V {
		q.set("flip", "")
	}
	if p.Flip.H {
		q.set("flop", "")
	}
	if deg := float64(p.Rotate) + p.RotateAny; deg != 0 {
		q.set("ro", num(deg))
	}
	if p.Pages != nil {
		q.set("n", itoa(*p.Pages))
	}
	if p.Page != nil {
		q.set("page", itoa(*p.Page))
	}
	if bg := p.Background; bg != nil && !p.Extend.Enabled {
		q.set("bg", hexARGB(*bg))
	}
	a := &p.Adjust
	if a.Exposure != nil {
		q.set("mod", num(round4(*a.Exposure)))
	}
	if a.Saturation != nil {
		q.set("sat", num(round4(*a.Saturation)))
	}
	if a.Hue != nil {
		q.set("hue", num(math.Mod(math.Trunc(*a.Hue)+360, 360)))
	}
	if a.Contrast != nil {
		q.set("con", num(math.Round((*a.Contrast-1)*100)))
	}
	if a.Gamma != nil {
		q.set("gam", num(round4(*a.Gamma)))
	}
	if p.Sharpen > 0 {
		q.set("sharp", num(p.Sharpen))
	}
	switch {
	case p.Grayscale:
		q.set("filt", "greyscale")
	case p.Sepia:
		q.set("filt", "sepia")
	case p.Negate:
		q.set("filt", "negate")
	case p.Duotone != nil:
		q.set("filt", "duotone")
		q.set("start", hexRGB(p.Duotone.Shadow))
		q.set("stop", hexRGB(p.Duotone.Highlight))
	}
	if p.Blur > 0 {
		q.set("blur", num(p.Blur))
	}
	if t := p.Tint; t != nil {
		q.set("tint", hexARGB(*t))
	}
	if m := p.Mask; m != nil {
		if s, ok := wsrvMasks[m.Shape]; ok {
			q.set("mask", s)
			if m.Trim {
				q.set("mtrim", "")
			}
			if m.Background != nil {
				q.set("mbg", hexARGB(*m.Background))
			}
		}
	}
	out := &e.r.Output
	if f, ok := wsrvFormats[out.Format]; ok {
		q.set("output", f)
	}
	if out.Quality > 0 {
		q.set("q", itoa(out.Quality))
	}
	if out.Lossless != nil && *out.Lossless {
		q.set("ll", "")
	}
	if out.Progressive != nil && *out.Progressive {
		q.set("il", "")
	}
	if png := out.PNG; png != nil && png.Compression != nil {
		q.set("l", itoa(*png.Compression))
	}
	d := &e.r.Delivery
	if d.Filename != "" {
		q.set("filename", d.Filename)
	}
	if d.TTL >= 24*time.Hour {
		q.set("maxage", itoa64(int64(d.TTL/time.Second)))
	}
	if d.OnErrorRedirect && d.Fallback == nil {
		q.set("default", "1")
	}
	return "/", q.encode(), nil
}
