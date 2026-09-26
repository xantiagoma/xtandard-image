package translate

import (
	"math"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var cloudinaryCompass = compassNames{
	transform.GravityCenter: "center", transform.GravityNorth: "north", transform.GravitySouth: "south",
	transform.GravityEast: "east", transform.GravityWest: "west", transform.GravityNorthEast: "north_east",
	transform.GravityNorthWest: "north_west", transform.GravitySouthEast: "south_east", transform.GravitySouthWest: "south_west",
}

var cloudinaryFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpg", transform.FormatPNG: "png", transform.FormatWebP: "webp", transform.FormatAVIF: "avif",
	transform.FormatJXL: "jxl", transform.FormatGIF: "gif", transform.FormatHEIC: "heic", transform.FormatTIFF: "tiff",
	transform.FormatBMP: "bmp", transform.FormatICO: "ico", transform.FormatSVG: "svg", transform.FormatMP4: "mp4",
	transform.FormatWebM: "webm",
}

// cloudinaryGravity writes g_ (and x_, y_) into c.
func cloudinaryGravity(g transform.Gravity, c *[]string) {
	switch g.Type {
	case transform.GravitySmart:
		*c = append(*c, "g_auto")
	case transform.GravityFace:
		if g.FaceIndex != nil && *g.FaceIndex == 0 {
			*c = append(*c, "g_face")
		} else if g.FaceIndex == nil {
			*c = append(*c, "g_faces")
		}
	case transform.GravityObject:
		if len(g.Classes) == 1 {
			*c = append(*c, "g_"+g.Classes[0])
		}
	case transform.GravityFocusPoint:
		if g.FocusPx == nil {
			*c = append(*c, "g_xy_center", "x_"+num(g.FocusX), "y_"+num(g.FocusY))
		}
	default:
		if n, ok := cloudinaryCompass[g.Type]; ok && !isDefaultGravity(g) {
			*c = append(*c, "g_"+n)
			if g.XOffset != 0 {
				*c = append(*c, "x_"+num(g.XOffset))
			}
			if g.YOffset != 0 {
				*c = append(*c, "y_"+num(g.YOffset))
			}
		}
	}
}

// cloudinaryColor is rgb:<hex>.
func cloudinaryColor(c transform.Color) string { return "rgb:" + hexRGB(c) }

// cloudinaryPipeline is the component of one pipeline.
//
//nolint:gocyclo,funlen // one flat walk over the pipeline model
func cloudinaryPipeline(p *transform.Pipeline) []string {
	var c []string
	rs := &p.Resize
	w, hasW := dim(rs.Width)
	h, hasH := dim(rs.Height)
	if hasW {
		c = append(c, "w_"+w)
	}
	if hasH {
		c = append(c, "h_"+h)
	}
	if rs.AspectRatio > 0 {
		c = append(c, "ar_"+num(rs.AspectRatio))
	}
	sized := hasW || hasH || rs.AspectRatio > 0
	gravity := p.Gravity
	switch {
	case !sized:
	case p.Extend.Enabled && rs.Type == transform.ResizeFit:
		if rs.Enlarge {
			c = append(c, "c_pad")
		} else {
			c = append(c, "c_lpad")
		}
		gravity = p.Extend.Gravity
	case rs.Type == transform.ResizeFill && rs.Enlarge:
		c = append(c, "c_fill")
	case rs.Type == transform.ResizeFill || rs.Type == transform.ResizeFillDown:
		c = append(c, "c_lfill")
	case rs.Type == transform.ResizeOutside:
		c = append(c, "c_mfit")
	case rs.Type == transform.ResizeForce:
		c = append(c, "c_scale")
	case rs.Enlarge:
		c = append(c, "c_fit")
	default:
		c = append(c, "c_limit")
	}
	if !isDefaultGravity(gravity) {
		cloudinaryGravity(gravity, &c)
	}
	if rs.DPR != 0 && rs.DPR != 1 {
		c = append(c, "dpr_"+num(rs.DPR))
	}
	var angle []string
	if p.Rotate != 0 {
		angle = append(angle, itoa(p.Rotate))
	}
	if p.RotateAny != 0 {
		angle = append(angle, num(p.RotateAny))
	}
	if p.Flip.H {
		angle = append(angle, "hflip")
	}
	if p.Flip.V {
		angle = append(angle, "vflip")
	}
	if p.AutoRotate != nil && !*p.AutoRotate {
		angle = append(angle, "ignore")
	}
	if len(angle) > 0 {
		c = append(c, "a_"+strings.Join(angle, "."))
	}
	if bg := p.Background; bg != nil {
		c = append(c, "b_"+cloudinaryColor(*bg))
	}
	if b := p.Border; b != nil && b.Top == b.Right && b.Top == b.Bottom && b.Top == b.Left {
		c = append(c, "bo_"+itoa(b.Top)+"px_solid_"+cloudinaryColor(b.Color))
	}
	if r := p.Radius; r != nil {
		if r.Max {
			c = append(c, "r_max")
		} else {
			c = append(c, "r_"+itoa(r.TopLeft)+":"+itoa(r.TopRight)+":"+itoa(r.BottomRight)+":"+itoa(r.BottomLeft))
		}
	}
	a := &p.Adjust
	if a.Opacity != nil {
		c = append(c, "o_"+num(math.Round(*a.Opacity*100)))
	}
	eff := func(name string, v float64) { c = append(c, "e_"+name+":"+num(math.Round(v))) }
	if p.Blur > 0 {
		eff("blur", p.Blur*20)
	}
	if p.Sharpen > 0 {
		eff("sharpen", p.Sharpen*100)
	}
	if u := p.Unsharp; u != nil && u.Mode == transform.UnsharpUnset && u.Sigma == 1 {
		eff("unsharp_mask", u.Amount*100)
	}
	if p.Pixelate > 0 {
		eff("pixelate", float64(p.Pixelate))
	}
	if p.Grayscale {
		c = append(c, "e_grayscale")
	}
	if p.Sepia {
		c = append(c, "e_sepia")
	}
	if p.Negate {
		c = append(c, "e_negate")
	}
	if a.Brightness != nil {
		eff("brightness", *a.Brightness/2.55)
	}
	for _, m := range []struct {
		name string
		v    *float64
	}{
		{"contrast", a.Contrast}, {"saturation", a.Saturation}, {"gamma", a.Gamma}, {"vibrance", a.Vibrance},
		{"red", a.Red}, {"green", a.Green}, {"blue", a.Blue},
	} {
		if m.v != nil {
			eff(m.name, (*m.v-1)*100)
		}
	}
	if a.Hue != nil {
		eff("hue", *a.Hue/1.8)
	}
	if col := p.Colorize; col != nil {
		eff("colorize", col.Opacity*100)
		c = append(c, "co_"+cloudinaryColor(col.Color))
	}
	if t := p.Trim; t != nil && t.Threshold <= 100 {
		v := "e_trim:" + num(math.Round(t.Threshold))
		if t.Color != nil {
			v += ":" + cloudinaryColor(*t.Color)
		}
		c = append(c, v)
	}
	if bd := p.BlurDetections; bd != nil && len(bd.Classes) == 1 && bd.Classes[0] == "face" {
		if bd.Pixelate > 0 {
			eff("pixelate_faces", float64(bd.Pixelate))
		} else {
			eff("blur_faces", bd.Sigma*20)
		}
	}
	return c
}

// emitCloudinary writes /<cloud>/image/{fetch,upload}/<components>/<source>.
func emitCloudinary(e *emitter) (string, string, error) {
	var kind, id string
	if k, ok := e.src.keyOn(e.mountSource); ok {
		kind, id = "upload", k
	} else if e.src.isHTTP() {
		kind, id = "fetch", escapePlain(e.src.abs)
	} else {
		return "", "", errSource("Cloudinary takes an http(s) URL (fetch) or a public ID on the mount's source (upload)")
	}
	var comps []string
	for i := range e.r.Pipelines {
		if c := cloudinaryPipeline(&e.r.Pipelines[i]); len(c) > 0 {
			comps = append(comps, strings.Join(c, ","))
		}
	}
	var req []string
	o := &e.r.Output
	if f, ok := cloudinaryFormats[o.Format]; ok {
		req = append(req, "f_"+f)
	} else if o.Format == transform.FormatUnset && kind == "upload" && fileExt(id) != "" {
		// The public ID's extension would pick the format.
		req = append(req, "f_auto")
	}
	if o.Quality > 0 {
		q := "q_" + itoa(o.Quality)
		switch o.ChromaSubsampling {
		case transform.Subsampling420:
			q += ":420"
		case transform.Subsampling422:
			q += ":422"
		case transform.Subsampling444:
			q += ":444"
		}
		req = append(req, q)
	}
	var flags []string
	if o.Progressive != nil && *o.Progressive {
		flags = append(flags, "progressive")
	}
	if o.StripMetadata != nil && !*o.StripMetadata {
		flags = append(flags, "keep_iptc")
	}
	d := &e.r.Delivery
	if d.Filename != "" && d.Attachment != nil && *d.Attachment {
		flags = append(flags, "attachment:"+strings.TrimSuffix(d.Filename, fileExt(d.Filename)))
	}
	if d.Immutable {
		flags = append(flags, "immutable_cache")
	}
	if len(flags) > 0 {
		req = append(req, "fl_"+strings.Join(flags, "."))
	}
	p := e.pl()
	if p.Page != nil {
		req = append(req, "pg_"+itoa(*p.Page+1))
	}
	if v := p.Video; v != nil && v.Second != nil {
		req = append(req, "so_"+num(*v.Second))
	}
	if len(req) > 0 {
		comps = append(comps, strings.Join(req, ","))
	}
	path := "/xi/image/" + kind
	for _, c := range comps {
		path += "/" + c
	}
	return path + "/" + id, "", nil
}

// fileExt is the extension of name including the dot, or "".
func fileExt(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 || strings.ContainsRune(name[i:], '/') {
		return ""
	}
	return name[i:]
}
