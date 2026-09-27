package translate

import (
	"maps"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// ipOpt is one imgproxy option: name and arguments.
type ipOpt struct {
	name string
	args []string
}

// ipOpts collects imgproxy options.
type ipOpts []ipOpt

// add appends an option, dropping trailing empty arguments.
func (o *ipOpts) add(name string, args ...string) {
	for len(args) > 0 && args[len(args)-1] == "" {
		args = args[:len(args)-1]
	}
	*o = append(*o, ipOpt{name: name, args: args})
}

func (o *ipOpts) segments(sep string) []string {
	out := make([]string, 0, len(*o))
	for _, x := range *o {
		out = append(out, strings.Join(append([]string{x.name}, x.args...), sep))
	}
	return out
}

var ipCompass = compassNames{
	transform.GravityCenter: "ce", transform.GravityNorth: "no", transform.GravitySouth: "so",
	transform.GravityEast: "ea", transform.GravityWest: "we", transform.GravityNorthEast: "noea",
	transform.GravityNorthWest: "nowe", transform.GravitySouthEast: "soea", transform.GravitySouthWest: "sowe",
}

func boolArg(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// ipGravity is the gravity arguments ("sm", "fp:x:y", "obj:classes", a
// compass point with offsets); nil when imgproxy has no form for it.
func ipGravity(g *transform.Gravity, crop bool) []string {
	switch g.Type {
	case transform.GravityUnset:
		return nil
	case transform.GravitySmart:
		if crop {
			return []string{"sm"}
		}
	case transform.GravityFocusPoint:
		if g.FocusPx == nil {
			return []string{"fp", num(g.FocusX), num(g.FocusY)}
		}
	case transform.GravityFace:
		if crop {
			return []string{"obj", "face"}
		}
	case transform.GravityObject:
		if crop {
			return append([]string{"obj"}, g.Classes...)
		}
	case transform.GravityObjectWeighted:
		if crop && len(g.Weights) == len(g.Classes) {
			a := []string{"objw"}
			for i, c := range g.Classes {
				a = append(a, c, num(g.Weights[i]))
			}
			return a
		}
	default:
		if c, ok := ipCompass[g.Type]; ok {
			a := []string{c, "", ""}
			if g.XOffset != 0 {
				a[1] = num(g.XOffset)
			}
			if g.YOffset != 0 {
				a[2] = num(g.YOffset)
			}
			for len(a) > 1 && a[len(a)-1] == "" {
				a = a[:len(a)-1]
			}
			return a
		}
	}
	return nil
}

// isDefaultGravity reports centre without offsets.
func isDefaultGravity(g *transform.Gravity) bool {
	return (g.Type == transform.GravityUnset || g.Type == transform.GravityCenter) && g.XOffset == 0 && g.YOffset == 0
}

func ipDirection(d transform.Direction, angle float64) string {
	if d == transform.DirectionAngle {
		return num(angle)
	}
	return d.String()
}

// ipPipeline writes the options of one pipeline.
//
//nolint:gocyclo,funlen // one flat walk over the pipeline model is the clearest form
func ipPipeline(p *transform.Pipeline) ipOpts {
	var o ipOpts
	rs := &p.Resize
	w := dimPx(rs.Width)
	h := dimPx(rs.Height)
	typ := ""
	if rs.Type != transform.ResizeUnset && rs.Type != transform.ResizeFit {
		typ = rs.Type.String()
	}
	if typ != "" || w != "" || h != "" || rs.Enlarge {
		el := ""
		if rs.Enlarge {
			el = "1"
		}
		o.add("rs", typ, w, h, el)
	}
	if rs.MinWidth > 0 {
		o.add("mw", itoa(rs.MinWidth))
	}
	if rs.MinHeight > 0 {
		o.add("mh", itoa(rs.MinHeight))
	}
	if (rs.ZoomX != 0 && rs.ZoomX != 1) || (rs.ZoomY != 0 && rs.ZoomY != 1) {
		o.add("z", num(orOne(rs.ZoomX)), num(orOne(rs.ZoomY)))
	}
	if rs.DPR != 0 && rs.DPR != 1 {
		o.add("dpr", num(rs.DPR))
	}
	if rs.Algorithm != transform.KernelUnset && rs.Algorithm != transform.KernelLanczos3 {
		o.add("ra", rs.Algorithm.String())
	}
	for _, x := range []struct {
		name string
		e    *transform.Extend
	}{{"ex", &p.Extend}, {"exar", &p.ExtendAR}} {
		if x.e.Enabled {
			args := []string{"1"}
			if !isDefaultGravity(&x.e.Gravity) {
				args = append(args, ipGravity(&x.e.Gravity, false)...)
			}
			o.add(x.name, args...)
		}
	}
	if !isDefaultGravity(&p.Gravity) {
		if g := ipGravity(&p.Gravity, true); g != nil {
			o.add("g", g...)
		}
	}
	if op := p.Gravity.ObjectsPosition; op != nil {
		switch {
		case op.Proportional:
			o.add("op", "prop")
		case op.Type == transform.GravityFocusPoint:
			o.add("op", "fp", num(op.X), num(op.Y))
		default:
			if c, ok := ipCompass[op.Type]; ok {
				o.add("op", c, num(op.X), num(op.Y))
			}
		}
	}
	if c := p.Crop; c != nil {
		cw, okW := dim(c.Width)
		ch, okH := dim(c.Height)
		if okW || okH {
			args := []string{orZero(cw), orZero(ch)}
			if !isDefaultGravity(&c.Gravity) {
				args = append(args, ipGravity(&c.Gravity, true)...)
			}
			o.add("c", args...)
		}
		if c.AspectRatio > 0 {
			o.add("car", num(c.AspectRatio), boolIf(c.AspectEnlarge))
		}
	}
	if co := p.CropObjects; co != nil && co.Index == nil && co.Padding == 0 {
		o.add("c_obj", append([]string{num(orOne(co.Scale))}, co.Classes...)...)
	}
	if t := p.Trim; t != nil && !t.ColorFromBottomRight {
		col := ""
		if t.Color != nil {
			col = hexRGB(*t.Color)
		}
		o.add("t", num(t.Threshold), col, boolIf(t.EqualHor), boolIf(t.EqualVer))
	}
	if pd := p.Padding; pd != nil {
		o.add("pd", itoa(pd.Top), itoa(pd.Right), itoa(pd.Bottom), itoa(pd.Left))
	}
	if p.AutoRotate != nil && !*p.AutoRotate {
		o.add("ar", "0")
	}
	if p.Rotate != 0 {
		o.add("rot", itoa(p.Rotate))
	}
	if p.Flip.H || p.Flip.V {
		o.add("fl", boolArg(p.Flip.H), boolArg(p.Flip.V))
	}
	if bg := p.Background; bg != nil {
		o.add("bg", hexRGB(transform.Color{R: bg.R, G: bg.G, B: bg.B, A: 255}))
		if bg.A != 255 {
			o.add("bga", num(round4(float64(bg.A)/255)))
		}
	}
	a := &p.Adjust
	if a.Brightness != nil {
		o.add("br", num(*a.Brightness))
	}
	if a.Contrast != nil {
		o.add("co", num(*a.Contrast))
	}
	if a.Saturation != nil {
		o.add("sa", num(*a.Saturation))
	}
	if m := p.Monochrome; m != nil {
		o.add("mc", num(m.Intensity), hexRGB(m.Color))
	}
	if d := p.Duotone; d != nil {
		o.add("dt", num(d.Intensity), hexRGB(d.Shadow), hexRGB(d.Highlight))
	}
	if c := p.Colorize; c != nil {
		o.add("col", num(c.Opacity), hexRGB(c.Color), boolIf(c.KeepAlpha))
	}
	if g := p.Gradient; g != nil {
		o.add("gr", num(g.Opacity), hexRGB(g.Color), ipDirection(g.Direction, g.Angle), num(g.Start), num(g.Stop))
	}
	if p.Blur > 0 {
		o.add("bl", num(p.Blur))
	}
	if p.Sharpen > 0 {
		o.add("sh", num(p.Sharpen))
	}
	if p.Pixelate > 0 {
		o.add("pix", itoa(p.Pixelate))
	}
	if u := p.Unsharp; u != nil && u.Mode != transform.UnsharpUnset {
		o.add("ush", u.Mode.String(), num(u.Weight), num(u.Divider))
	}
	if ba := p.BlurAreas; ba != nil {
		args := make([]string, 0, 1+4*len(ba.Areas))
		args = append(args, num(ba.Sigma))
		for _, ar := range ba.Areas {
			args = append(args, num(ar.Left), num(ar.Top), num(ar.Width), num(ar.Height))
		}
		o.add("ba", args...)
	}
	if pb := p.ProgressiveBlur; pb != nil {
		o.add("pbl", num(pb.Sigma), ipDirection(pb.Direction, pb.Angle), num(pb.Start), num(pb.Stop))
	}
	if bd := p.BlurDetections; bd != nil && bd.Pixelate == 0 {
		o.add("bd", append([]string{num(bd.Sigma)}, bd.Classes...)...)
	}
	if dd := p.DrawDetections; dd != nil {
		o.add("dd", append([]string{"1"}, dd.Classes...)...)
	}
	if len(p.Watermarks) > 0 {
		ipWatermark(&o, &p.Watermarks[0])
	}
	if p.SVGStyle != "" {
		o.add("st", b64(p.SVGStyle))
	}
	if p.Page != nil && *p.Page != 0 {
		o.add("pg", itoa(*p.Page))
	}
	if p.Pages != nil {
		o.add("pgs", itoa(*p.Pages))
	}
	if p.DisableAnimation {
		o.add("da", "1")
	}
	if v := p.Video; v != nil {
		ipVideo(&o, v)
	}
	return o
}

func ipWatermark(o *ipOpts, w *transform.Watermark) {
	pos := ""
	switch w.Tile {
	case transform.TileRepeat:
		pos = "re"
	case transform.TileChessboard:
		pos = "ch"
	default:
		if c, ok := ipCompass[w.Position.Type]; ok && w.Position.Type != transform.GravityCenter {
			pos = c
		}
	}
	x, y, scale := "", "", ""
	if w.Position.XOffset != 0 {
		x = num(w.Position.XOffset)
	}
	if w.Position.YOffset != 0 {
		y = num(w.Position.YOffset)
	}
	if w.Scale > 0 {
		scale = num(w.Scale)
	}
	o.add("wm", num(w.Opacity), pos, x, y, scale)
	if w.Source != nil {
		o.add("wmu", b64(w.Source.Raw))
	}
	if w.Text != "" {
		o.add("wmt", b64(w.Text))
	}
	if w.Width > 0 || w.Height > 0 {
		o.add("wms", itoa(w.Width), itoa(w.Height))
	}
	if w.Rotate != 0 {
		o.add("wmr", num(w.Rotate))
	}
	if w.Shadow > 0 {
		o.add("wmsh", num(w.Shadow))
	}
}

func ipVideo(o *ipOpts, v *transform.VideoOptions) {
	if v.Second != nil {
		o.add("vts", num(*v.Second))
	}
	if v.Keyframes != nil {
		o.add("vtk", boolArg(*v.Keyframes))
	}
	if t := v.Tile; t != nil {
		o.add("vtt", num(t.Step), itoa(t.Columns), itoa(t.Rows), itoa(t.TileWidth), itoa(t.TileHeight),
			boolArg(t.ExtendTile), boolArg(t.Trim), boolArg(t.Fill), num(t.FocusX), num(t.FocusY))
	}
	if a := v.Animation; a != nil {
		o.add("vta", num(a.Step), num(round4(a.Delay*1000)), itoa(a.Frames), itoa(a.FrameWidth), itoa(a.FrameHeight),
			boolArg(a.ExtendFrame), boolArg(a.Trim), boolArg(a.Fill), num(a.FocusX), num(a.FocusY))
	}
}

// ipRequest writes the request-wide options.
//
//nolint:gocyclo // one flat walk over the output and delivery model
func ipRequest(r *transform.Request) ipOpts {
	var o ipOpts
	out := &r.Output
	if out.Format != transform.FormatUnset {
		o.add("f", out.Format.String())
	}
	if out.Quality > 0 {
		o.add("q", itoa(out.Quality))
	}
	if len(out.FormatQuality) > 0 {
		args := make([]string, 0, 2*len(out.FormatQuality))
		for _, f := range slices.Sorted(maps.Keys(out.FormatQuality)) {
			args = append(args, f.String(), itoa(out.FormatQuality[f]))
		}
		o.add("fq", args...)
	}
	if aq := out.AutoQuality; aq != nil {
		o.add("aq", aq.Method.String(), num(aq.Target), itoa0(aq.Min), itoa0(aq.Max), num0(aq.AllowedError))
	}
	if out.MaxBytes > 0 {
		o.add("mb", itoa(out.MaxBytes))
	}
	if j := out.JPEG; j != nil {
		qt := ""
		if j.QuantTable != nil {
			qt = itoa(*j.QuantTable)
		}
		o.add("jpgo", boolPtr(j.Progressive), boolPtr(j.NoSubsample), boolPtr(j.TrellisQuant),
			boolPtr(j.OvershootDeringing), boolPtr(j.OptimizeScans), qt)
	}
	if p := out.PNG; p != nil && p.Compression == nil && p.AdaptiveFilter == nil {
		o.add("pngo", boolPtr(p.Interlaced), boolPtr(p.Quantize), itoa0(p.QuantizationColors))
	}
	if w := out.WebP; w != nil {
		o.add("webpo", w.Compression.String(), boolPtr(w.SmartSubsample), w.Preset.String())
	}
	if a := out.AVIF; a != nil {
		sub := map[transform.Subsampling]string{
			transform.SubsamplingAuto: "auto", transform.Subsampling420: "on", transform.Subsampling444: "off",
		}[a.Subsample]
		o.add("avifo", sub)
	}
	for _, b := range []struct {
		name string
		v    *bool
		def  bool
	}{
		{"sm", out.StripMetadata, true},
		{"kcr", out.KeepCopyright, true},
		{"scp", out.StripColorProfile, true},
		{"ph", out.PreserveHDR, false},
		{"eth", out.EnforceThumbnail, false},
	} {
		if b.v != nil && *b.v != b.def {
			o.add(b.name, boolArg(*b.v))
		}
	}
	if out.ColorProfile != "" {
		o.add("cp", out.ColorProfile)
	}
	if out.DPI > 0 {
		o.add("dpi", itoa(out.DPI))
	}
	d := &r.Delivery
	if d.Filename != "" {
		if strings.ContainsAny(d.Filename, "/:%?#") {
			o.add("fn", b64(d.Filename), "1")
		} else {
			o.add("fn", d.Filename)
		}
	}
	if d.Attachment != nil && *d.Attachment {
		o.add("att", "1")
	}
	if d.CacheBuster != "" {
		o.add("cb", d.CacheBuster)
	}
	if !d.Expires.IsZero() {
		o.add("exp", itoa64(d.Expires.Unix()))
	}
	if len(d.SkipProcessing) > 0 {
		args := make([]string, 0, len(d.SkipProcessing))
		for _, f := range d.SkipProcessing {
			args = append(args, f.String())
		}
		o.add("skp", args...)
	}
	if len(d.CacheTags) > 0 {
		o.add("ct", d.CacheTags...)
	}
	if d.Fallback != nil {
		o.add("fiu", b64(d.Fallback.Raw))
	}
	if h := r.Source.Hashsum; h != nil {
		o.add("hs", h.Algorithm.String(), h.Digest)
	}
	return o
}

// ipPaths is the option path of every pipeline; request-wide options go
// with the first.
func ipPaths(r *transform.Request, sep string) [][]string {
	pls := r.Pipelines
	if len(pls) == 0 {
		pls = []transform.Pipeline{{}}
	}
	out := make([][]string, 0, len(pls))
	for i := range pls {
		po := ipPipeline(&pls[i])
		segs := po.segments(sep)
		if i == 0 {
			ro := ipRequest(r)
			segs = append(segs, ro.segments(sep)...)
		}
		out = append(out, segs)
	}
	return out
}

// emitImgproxy writes /<options>/plain/<source>; chained pipelines are
// joined with /-/.
func emitImgproxy(e *emitter) (string, string, error) {
	var b strings.Builder
	for i, segs := range ipPaths(e.r, e.sep) {
		if i > 0 {
			b.WriteString("/-")
		}
		for _, s := range segs {
			b.WriteByte('/')
			b.WriteString(s)
		}
	}
	b.WriteString("/plain/")
	b.WriteString(escapePlain(e.src.abs))
	return b.String(), "", nil
}

func dimPx(d transform.Dim) string {
	if d.Px > 0 {
		return itoa(d.Px)
	}
	return ""
}

func orOne(f float64) float64 {
	if f == 0 {
		return 1
	}
	return f
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func boolIf(b bool) string {
	if b {
		return "1"
	}
	return ""
}

func boolPtr(b *bool) string {
	if b == nil {
		return ""
	}
	return boolArg(*b)
}

func itoa0(n int) string {
	if n == 0 {
		return ""
	}
	return itoa(n)
}

func num0(f float64) string {
	if f == 0 {
		return ""
	}
	return num(f)
}

func itoa64(n int64) string { return num(float64(n)) }
