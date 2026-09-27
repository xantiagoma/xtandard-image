package translate

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var bunnyFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpeg", transform.FormatPNG: "png", transform.FormatWebP: "webp",
	transform.FormatGIF: "gif", transform.FormatAVIF: "avif",
}

// emitBunny writes /<key>?k=v.
//
//nolint:gocyclo // one flat walk over the pipeline model
func emitBunny(e *emitter) (string, string, error) {
	key, ok := e.src.keyOn(e.mountSource)
	if !ok {
		return "", "", e.errNeedsKey("Bunny Optimizer")
	}
	p := e.pl()
	var q params
	rs := &p.Resize
	if rs.Width.Px > 0 {
		q.set("width", itoa(rs.Width.Px))
	}
	if rs.Height.Px > 0 {
		q.set("height", itoa(rs.Height.Px))
	}
	fill := rs.Type == transform.ResizeFill || rs.Type == transform.ResizeFillDown
	if fill && rs.Width.Px > 0 && rs.Height.Px > 0 {
		q.set("aspect_ratio", itoa(rs.Width.Px)+":"+itoa(rs.Height.Px))
	}
	if rs.Enlarge {
		q.set("upscaling", "resampling")
	}
	switch g := p.Gravity; g.Type {
	case transform.GravityFace:
		if g.FaceIndex == nil && g.FacePad == 0 {
			q.set("face_crop", "true")
		}
	default:
		if _, ok := ipCompass[g.Type]; ok && !isDefaultGravity(&g) && g.XOffset == 0 && g.YOffset == 0 {
			q.set("crop_gravity", map[transform.GravityType]string{
				transform.GravityNorth: "north", transform.GravitySouth: "south", transform.GravityEast: "east",
				transform.GravityWest: "west", transform.GravityNorthEast: "northeast", transform.GravityNorthWest: "northwest",
				transform.GravitySouthEast: "southeast", transform.GravitySouthWest: "southwest",
			}[g.Type])
		}
	}
	if c := p.Crop; c != nil && c.Width.Px > 0 && c.Height.Px > 0 && c.AspectRatio == 0 {
		if c.Gravity.Type == transform.GravityNorthWest {
			q.set("crop", itoa(c.Width.Px)+","+itoa(c.Height.Px)+","+num(c.Gravity.XOffset)+","+num(c.Gravity.YOffset))
		} else if isDefaultGravity(&c.Gravity) {
			q.set("crop", itoa(c.Width.Px)+","+itoa(c.Height.Px))
		}
	}
	if p.Flip.V {
		q.set("flip", "true")
	}
	if p.Flip.H {
		q.set("flop", "true")
	}
	if p.Rotate != 0 {
		q.set("rotate", itoa(p.Rotate))
	}
	if p.Blur > 0 {
		q.set("blur", num(math.Round(p.Blur*4)))
	}
	if p.Sharpen > 0 {
		q.set("sharpen", "true")
	}
	a := &p.Adjust
	if a.Brightness != nil {
		q.set("brightness", num(math.Round(*a.Brightness/2.55)))
	}
	for _, m := range []struct {
		k string
		v *float64
	}{{"contrast", a.Contrast}, {"saturation", a.Saturation}} {
		if m.v != nil {
			q.set(m.k, num(math.Round((*m.v-1)*100)))
		}
	}
	if a.Gamma != nil && *a.Gamma > 0 {
		q.set("gamma", num(math.Round(math.Log2(*a.Gamma)*100)))
	}
	if a.Hue != nil {
		q.set("hue", num(math.Round(*a.Hue/3.6)))
	}
	if t := p.Tint; t != nil {
		q.set("tint", hexRGB(transform.Color{R: t.R, G: t.G, B: t.B, A: 255}))
	}
	if p.Sepia {
		q.set("sepia", "100")
	}
	out := &e.r.Output
	if f, ok := bunnyFormats[out.Format]; ok {
		q.set("format", f)
	}
	if out.Quality > 0 {
		q.set("quality", itoa(out.Quality))
	}
	return "/" + key, q.encode(), nil
}
