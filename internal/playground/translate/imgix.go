package translate

import (
	"math"
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// params is an ordered query string.
type params []string

func (p *params) set(k, v string) { *p = append(*p, url.QueryEscape(k)+"="+url.QueryEscape(v)) }

func (p params) encode() string { return strings.Join(p, "&") }

// sideWords maps compass gravities to (horizontal, vertical) words.
var sideWords = map[transform.GravityType][2]string{
	transform.GravityNorth: {"", "top"}, transform.GravitySouth: {"", "bottom"},
	transform.GravityEast: {"right", ""}, transform.GravityWest: {"left", ""},
	transform.GravityNorthEast: {"right", "top"}, transform.GravityNorthWest: {"left", "top"},
	transform.GravitySouthEast: {"right", "bottom"}, transform.GravitySouthWest: {"left", "bottom"},
}

// imgixCrop is the crop= value of a gravity.
func imgixCrop(g transform.Gravity, q *params) {
	switch g.Type {
	case transform.GravitySmart:
		q.set("crop", "edges")
	case transform.GravityEntropy:
		q.set("crop", "entropy")
	case transform.GravityFace:
		q.set("crop", "faces")
	case transform.GravityObject:
		cs := make([]string, 0, len(g.Classes))
		for _, c := range g.Classes {
			cs = append(cs, strings.ReplaceAll(c, "_", " "))
		}
		q.set("crop", strings.Join(cs, ","))
	case transform.GravityFocusPoint:
		if g.FocusPx != nil {
			return
		}
		q.set("crop", "focalpoint")
		q.set("fp-x", num(g.FocusX))
		q.set("fp-y", num(g.FocusY))
		if g.FocusZoom > 0 {
			q.set("fp-z", num(g.FocusZoom))
		}
	default:
		if w, ok := sideWords[g.Type]; ok {
			var parts []string
			for _, s := range []string{w[1], w[0]} {
				if s != "" {
					parts = append(parts, s)
				}
			}
			q.set("crop", strings.Join(parts, ","))
		}
	}
}

// imgixFit writes fit and its companions for the first pipeline.
func imgixFit(p *transform.Pipeline, q *params) {
	rs := &p.Resize
	crop := false
	switch {
	case p.Gravity.Type == transform.GravityFace && p.Gravity.FacePad > 0:
		q.set("fit", "facearea")
		q.set("facepad", num(p.Gravity.FacePad))
		if p.Gravity.FaceIndex != nil {
			q.set("faceindex", itoa(*p.Gravity.FaceIndex+1))
		}
		return
	case p.Extend.Enabled && p.Extend.Mode == transform.ExtendCopy:
		q.set("fit", "clamp")
	case p.Extend.Enabled:
		if rs.Enlarge {
			q.set("fit", "fill")
		} else {
			q.set("fit", "fillmax")
		}
		if p.Extend.Mode == transform.ExtendBlur {
			q.set("fill", "blur")
		} else if p.Background != nil {
			q.set("fill-color", hexARGB(*p.Background))
		}
	case rs.Type == transform.ResizeFill || rs.Type == transform.ResizeFillDown:
		crop = true
		if rs.Type == transform.ResizeFill && rs.Enlarge {
			q.set("fit", "crop")
			if rs.AspectRatio > 0 {
				q.set("ar", num(rs.AspectRatio)+":1")
			}
		} else {
			q.set("fit", "min")
		}
	case rs.Type == transform.ResizeForce:
		q.set("fit", "scale")
	case rs.Type == transform.ResizeFit || rs.Type == transform.ResizeUnset:
		sized := !rs.Width.IsZero() || !rs.Height.IsZero()
		if !rs.Enlarge && sized {
			q.set("fit", "max")
		} else if rs.Width.IsZero() && rs.Height.IsZero() {
			q.set("fit", "clip")
		}
	}
	if crop && !isDefaultGravity(p.Gravity) {
		imgixCrop(p.Gravity, q)
	}
}

// emitImgix writes /<key or encoded url>?params.
//
//nolint:gocyclo,funlen // one flat walk over the pipeline model
func emitImgix(e *emitter) (string, string, error) {
	var path string
	if k, ok := e.src.keyOn(e.mountSource); ok {
		path = "/" + k
	} else if e.src.isHTTP() {
		path = "/" + url.PathEscape(e.src.abs)
	} else {
		return "", "", errSource("imgix takes an http(s) URL (web proxy source) or a key on the mount's source")
	}
	p := e.pl()
	var q params
	rs := &p.Resize
	if w, ok := dim(rs.Width); ok {
		q.set("w", w)
	}
	if h, ok := dim(rs.Height); ok {
		q.set("h", h)
	}
	imgixFit(p, &q)
	if rs.DPR != 0 && rs.DPR != 1 {
		q.set("dpr", num(rs.DPR))
	}
	for _, c := range []struct {
		k string
		v int
	}{{"max-w", rs.MaxWidth}, {"max-h", rs.MaxHeight}, {"min-w", rs.MinWidth}, {"min-h", rs.MinHeight}} {
		if c.v > 0 {
			q.set(c.k, itoa(c.v))
		}
	}
	if c := p.Crop; c != nil && (c.Gravity.Type == transform.GravityNorthWest || isDefaultGravity(c.Gravity)) && c.AspectRatio == 0 {
		cw, okW := dim(c.Width)
		ch, okH := dim(c.Height)
		if okW && okH {
			q.set("rect", num(c.Gravity.XOffset)+","+num(c.Gravity.YOffset)+","+cw+","+ch)
		}
	}
	// Rotation: rot is counter-clockwise.
	if deg := math.Mod(360-float64(p.Rotate)-p.RotateAny, 360); deg != 0 {
		q.set("rot", num(round4(math.Mod(deg+360, 360))))
	}
	switch {
	case p.Flip.H && p.Flip.V:
		q.set("flip", "hv")
	case p.Flip.H:
		q.set("flip", "h")
	case p.Flip.V:
		q.set("flip", "v")
	}
	a := &p.Adjust
	if a.Brightness != nil {
		q.set("bri", num(round4(*a.Brightness*100/255)))
	}
	for _, c := range []struct {
		k string
		v *float64
	}{{"con", a.Contrast}, {"exp", a.Exposure}, {"sat", a.Saturation}, {"vib", a.Vibrance}} {
		if c.v != nil {
			q.set(c.k, num(round4((*c.v-1)*100)))
		}
	}
	if a.Gamma != nil && *a.Gamma > 0 {
		q.set("gam", num(round4(math.Log2(*a.Gamma)*100)))
	}
	if a.Hue != nil {
		q.set("hue", num(*a.Hue))
	}
	if p.Blur > 0 {
		q.set("blur", num(round4(p.Blur*20)))
	}
	if p.Sharpen > 0.5 {
		q.set("sharp", num(round4((p.Sharpen-0.5)*50)))
	}
	if u := p.Unsharp; u != nil && u.Mode == transform.UnsharpUnset && u.Amount != 0 {
		q.set("usm", num(round4(u.Amount*100)))
		q.set("usmrad", num(u.Sigma))
	}
	if p.Pixelate > 0 {
		q.set("px", itoa(p.Pixelate))
	}
	if bd := p.BlurDetections; bd != nil && len(bd.Classes) == 1 && bd.Classes[0] == "face" {
		if bd.Pixelate > 0 {
			q.set("face-pixel", itoa(bd.Pixelate))
		} else {
			q.set("face-blur", num(round4(bd.Sigma*5)))
		}
	}
	if p.Sepia {
		q.set("sepia", "100")
	}
	if m := p.Monochrome; m != nil {
		c := m.Color
		c.A = uint8(math.Round(m.Intensity * 255))
		q.set("monochrome", hexARGB(c))
	}
	if d := p.Duotone; d != nil {
		q.set("duotone", hexRGB(d.Shadow)+","+hexRGB(d.Highlight))
		if d.Intensity != 1 {
			q.set("duotone-alpha", num(round4(d.Intensity*100)))
		}
	}
	if p.Negate {
		q.set("invert", "true")
	}
	if bg := p.Background; bg != nil && !(p.Extend.Enabled && p.Extend.Mode != transform.ExtendBlur) {
		q.set("bg", hexARGB(*bg))
	}
	if pd := p.Padding; pd != nil {
		if pd.Top == pd.Right && pd.Top == pd.Bottom && pd.Top == pd.Left {
			q.set("pad", itoa(pd.Top))
		} else {
			q.set("pad-top", itoa(pd.Top))
			q.set("pad-right", itoa(pd.Right))
			q.set("pad-bottom", itoa(pd.Bottom))
			q.set("pad-left", itoa(pd.Left))
		}
	}
	if b := p.Border; b != nil && b.Top == b.Right && b.Top == b.Bottom && b.Top == b.Left {
		q.set("border", itoa(b.Top)+","+hexARGB(b.Color))
	}
	if r := p.Radius; r != nil && !r.Max {
		if r.TopLeft == r.TopRight && r.TopLeft == r.BottomRight && r.TopLeft == r.BottomLeft {
			q.set("border-radius", itoa(r.TopLeft))
		} else {
			q.set("border-radius", itoa(r.TopLeft)+","+itoa(r.TopRight)+","+itoa(r.BottomRight)+","+itoa(r.BottomLeft))
		}
	}
	if t := p.Trim; t != nil {
		if t.Color == nil {
			q.set("trim", "auto")
			q.set("trim-md", num(t.Threshold))
		} else {
			q.set("trim", "color")
			q.set("trim-color", hexARGB(*t.Color))
			q.set("trim-tol", num(t.Threshold))
		}
	}
	if m := p.Mask; m != nil && m.Shape == transform.MaskEllipse {
		q.set("mask", "ellipse")
		if m.Background != nil {
			q.set("mask-bg", hexARGB(*m.Background))
		}
	}
	if p.Page != nil {
		if p.Pages != nil && *p.Pages == 1 {
			q.set("frame", itoa(*p.Page+1))
		} else {
			q.set("page", itoa(*p.Page+1))
		}
	}
	imgixWatermarks(p, &q)
	imgixOutput(e.r, &q)
	return path, q.encode(), nil
}

// imgixAlign writes <prefix>-align and <prefix>-pad, or -x/-y for a
// north-west position with offsets.
func imgixAlign(w *transform.Watermark, prefix string, q *params) {
	g := w.Position
	if g.Type == transform.GravityNorthWest {
		q.set(prefix+"-x", num(g.XOffset))
		q.set(prefix+"-y", num(g.YOffset))
		return
	}
	words, ok := sideWords[g.Type]
	h, v := "center", "middle"
	if ok {
		if words[0] != "" {
			h = words[0]
		}
		if words[1] != "" {
			v = words[1]
		}
	}
	q.set(prefix+"-align", v+","+h)
	pad := math.Max(g.XOffset, g.YOffset)
	q.set(prefix+"-pad", num(pad))
}

func imgixWatermarks(p *transform.Pipeline, q *params) {
	var img, txt bool
	for i := range p.Watermarks {
		w := &p.Watermarks[i]
		switch {
		case w.Source != nil && !img:
			img = true
			q.set("mark", w.Source.Raw)
			if w.Opacity != 1 {
				q.set("mark-alpha", num(round4(w.Opacity*100)))
			}
			imgixAlign(w, "mark", q)
			if w.Scale > 0 {
				q.set("mark-scale", num(round4(w.Scale*100)))
			}
			if w.Width > 0 {
				q.set("mark-w", itoa(w.Width))
			}
			if w.Height > 0 {
				q.set("mark-h", itoa(w.Height))
			}
			if w.Tile == transform.TileRepeat {
				q.set("mark-tile", "grid")
			}
			if w.Rotate != 0 {
				q.set("mark-rot", num(round4(math.Mod(360-w.Rotate, 360))))
			}
		case w.Text != "" && !txt:
			s, ok := plainText(w.Text)
			if !ok {
				continue
			}
			txt = true
			q.set("txt", s)
			imgixAlign(w, "txt", q)
			if family, size, ok := splitFont(w.Font); ok {
				q.set("txt-font", family)
				q.set("txt-size", size)
			}
			if w.Color != nil {
				q.set("txt-color", hexARGB(*w.Color))
			}
			if w.Shadow > 0 {
				q.set("txt-shad", num(w.Shadow))
			}
			if w.TextWidth > 0 {
				q.set("txt-width", itoa(w.TextWidth))
			}
		}
	}
}

// splitFont splits "family [style] size" into family and size.
func splitFont(f string) (family, size string, ok bool) {
	i := strings.LastIndexByte(f, ' ')
	if i <= 0 {
		return "", "", false
	}
	return f[:i], f[i+1:], true
}

var imgixFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpg", transform.FormatPNG: "png", transform.FormatWebP: "webp", transform.FormatAVIF: "avif",
	transform.FormatJXL: "jxl", transform.FormatGIF: "gif", transform.FormatMP4: "mp4", transform.FormatWebM: "webm",
}

func imgixOutput(r *transform.Request, q *params) {
	o := &r.Output
	if f, ok := imgixFormats[o.Format]; ok {
		q.set("fm", f)
	}
	if o.Quality > 0 {
		q.set("q", itoa(o.Quality))
	}
	if o.Progressive != nil {
		q.set("jpg-progressive", boolArg(*o.Progressive))
	}
	if o.Lossless != nil {
		q.set("lossless", boolArg(*o.Lossless))
	}
	switch o.ChromaSubsampling {
	case transform.Subsampling420:
		q.set("chromasub", "420")
	case transform.Subsampling422:
		q.set("chromasub", "422")
	case transform.Subsampling444:
		q.set("chromasub", "444")
	}
	if o.DPI > 0 {
		q.set("dpi", itoa(o.DPI))
	}
	if d := &r.Delivery; d.Filename != "" {
		q.set("dl", d.Filename)
	}
	if !r.Delivery.Expires.IsZero() {
		q.set("expires", itoa64(r.Delivery.Expires.Unix()))
	}
}
