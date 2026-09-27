package translate

import (
	"math"
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var cloudflareFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpeg", transform.FormatPNG: "png", transform.FormatWebP: "webp",
	transform.FormatAVIF: "avif", transform.FormatGIF: "gif",
}

// emitCloudflare writes /<k=v,...>/<source>.
//
//nolint:gocyclo,funlen // one flat walk over the pipeline model
func emitCloudflare(e *emitter) (string, string, error) {
	var srcPath string
	switch k, ok := e.src.keyOn(e.mountSource); {
	case ok:
		srcPath = k
	case e.src.isHTTP():
		srcPath = e.src.abs
		if strings.ContainsAny(srcPath, "?#%") {
			srcPath = url.PathEscape(srcPath)
		}
	default:
		return "", "", errSource("Cloudflare takes an http(s) URL or a path on the mount's source")
	}
	p := e.pl()
	var o []string
	set := func(k, v string) { o = append(o, k+"="+v) }
	rs := &p.Resize
	if rs.Width.Px > 0 {
		set("width", itoa(rs.Width.Px))
	}
	if rs.Height.Px > 0 {
		set("height", itoa(rs.Height.Px))
	}
	switch {
	case p.Extend.Enabled && rs.Type == transform.ResizeFit:
		set("fit", "pad")
	case rs.Type == transform.ResizeFill && rs.Enlarge:
		set("fit", "cover")
	case rs.Type == transform.ResizeFillDown:
		set("fit", "crop")
	case rs.Type == transform.ResizeFill:
		set("fit", "aspect-crop")
	case rs.Type == transform.ResizeForce:
		set("fit", "squeeze")
	case rs.Enlarge:
		set("fit", "contain")
	case rs.Width.Px > 0 || rs.Height.Px > 0:
		set("fit", "scale-down")
	}
	g := p.Gravity
	if p.Extend.Enabled {
		g = p.Extend.Gravity
	}
	switch g.Type {
	case transform.GravitySmart:
		set("gravity", "auto")
	case transform.GravityFace:
		if g.FaceIndex == nil {
			set("gravity", "face")
			if g.FacePad > 0 {
				set("zoom", num(round4(1/g.FacePad)))
			}
		}
	case transform.GravityFocusPoint:
		if g.FocusPx == nil {
			set("gravity", num(g.FocusX)+"x"+num(g.FocusY))
		}
	case transform.GravityWest:
		set("gravity", "left")
	case transform.GravityEast:
		set("gravity", "right")
	case transform.GravityNorth:
		set("gravity", "top")
	case transform.GravitySouth:
		set("gravity", "bottom")
	default:
	}
	if rs.DPR != 0 && rs.DPR != 1 {
		set("dpr", num(rs.DPR))
	}
	if p.DisableAnimation {
		set("anim", "false")
	}
	if bg := p.Background; bg != nil {
		set("background", "%23"+hexRGB(*bg))
	}
	if p.Blur > 0 {
		set("blur", num(math.Round(p.Blur*2)))
	}
	if p.Sharpen > 0 {
		set("sharpen", num(round4(p.Sharpen*4)))
	}
	a := &p.Adjust
	for _, m := range []struct {
		k string
		v *float64
	}{{"brightness", a.Exposure}, {"contrast", a.Contrast}, {"saturation", a.Saturation}, {"gamma", a.Gamma}} {
		if m.v != nil {
			set(m.k, num(round4(*m.v)))
		}
	}
	if p.Rotate != 0 {
		set("rotate", itoa(p.Rotate))
	}
	switch {
	case p.Flip.H && p.Flip.V:
		set("flip", "hv")
	case p.Flip.H:
		set("flip", "h")
	case p.Flip.V:
		set("flip", "v")
	}
	if b := p.Border; b != nil && sameSides(b.Top, b.Right, b.Bottom, b.Left) {
		set("border", "%23"+hexRGB(b.Color)+";"+itoa(b.Top))
	}
	if t := p.Trim; t != nil && t.Threshold <= 255 {
		set("trim", "border")
		set("trim.border.tolerance", num(math.Round(255-t.Threshold)))
		if t.Color != nil {
			set("trim.border.color", "%23"+hexRGB(*t.Color))
		}
	}
	out := &e.r.Output
	switch f, ok := cloudflareFormats[out.Format]; {
	case out.Format == transform.FormatJPEG && out.Progressive != nil && !*out.Progressive:
		set("format", "baseline-jpeg")
	case ok:
		set("format", f)
	}
	if out.Quality > 0 {
		set("quality", itoa(out.Quality))
	}
	if out.SlowConnectionQuality > 0 {
		set("slow-connection-quality", itoa(out.SlowConnectionQuality))
	}
	if out.Effort == 1 {
		set("compression", "fast")
	}
	if out.StripMetadata != nil && !*out.StripMetadata {
		set("metadata", "keep")
	}
	if e.r.Delivery.OnErrorRedirect {
		set("onerror", "redirect")
	}
	if len(o) == 0 {
		set("fit", "scale-down")
	}
	return "/" + strings.Join(o, ",") + "/" + srcPath, "", nil
}
