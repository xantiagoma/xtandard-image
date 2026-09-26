package plan

import (
	"fmt"
	"math"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// maxCoord bounds every width and height the planner produces (libvips
// VIPS_MAX_COORD).
const maxCoord = 10_000_000

// Build plans req (after ApplyDefaults and Validate) for a source with the
// given header. Errors are *errs.Error: KindLimitExceeded when the source or
// the plan exceeds the limits, KindCorrupt for an impossible header.
//
//nolint:gocritic // hugeParam: values by design (ARCHITECTURE §8.1 signature); Build never retains or mutates them
func Build(req *transform.Request, meta SourceMeta, limits Limits) (*Plan, error) {
	return build(req, &meta, &limits, nil)
}

// Continue replans after the executor observed what a header cannot tell
// (see the package documentation). It returns the complete plan; the steps
// up to and including the observed one are unchanged.
//
//nolint:gocritic // hugeParam: values like Build
func Continue(req *transform.Request, meta SourceMeta, limits Limits, obs Observed) (*Plan, error) {
	if obs.Width <= 0 || obs.Height <= 0 || obs.Width > maxCoord || obs.Height > maxCoord {
		return nil, errs.New(errs.KindCorrupt, "plan: observed image size is out of range")
	}
	return build(req, &meta, &limits, &obs)
}

// effLimits are Limits with the request overrides applied.
type effLimits struct {
	srcPixels, framePixels int64
	frames, resultDim      int
	memory                 int64
	noShrinkOnLoad         bool
}

func override(cfg, req int64, raise bool) int64 {
	if req <= 0 || (cfg > 0 && req > cfg && !raise) {
		return cfg
	}
	return req
}

func resolveLimits(l *Limits, o *transform.LimitOverrides) effLimits {
	mp := func(f float64) int64 {
		if !(f > 0) || f > 1e12 {
			return 0
		}
		return int64(f * 1e6)
	}
	return effLimits{
		srcPixels:      override(l.MaxSrcPixels, mp(o.MaxSrcResolution), l.AllowRaisingLimits),
		framePixels:    override(l.MaxAnimationFramePixels, mp(o.MaxAnimationFrameResolution), l.AllowRaisingLimits),
		frames:         int(override(int64(l.MaxAnimationFrames), int64(o.MaxAnimationFrames), l.AllowRaisingLimits)),
		resultDim:      int(override(int64(l.MaxResultDimension), int64(o.MaxResultDimension), l.AllowRaisingLimits)),
		memory:         l.MemoryBudget,
		noShrinkOnLoad: l.DisableShrinkOnLoad,
	}
}

// source is the input of one pipeline.
type source struct {
	w, h        int // one frame, as stored
	orientation int
	alpha       bool
	format      string
	vector      bool
	hasData     bool // the encoded source is available: shrink-on-load possible
	frames      int  // frames processed (1 unless animated)
	page        int
	stack       int // pages of a still loaded stacked as one image (≤ 1: one)
	density     float64
}

func build(req *transform.Request, meta *SourceMeta, limits *Limits, obs *Observed) (*Plan, error) {
	if req == nil {
		return nil, errs.New(errs.KindInternal, "plan: nil request")
	}
	frameH := meta.Height
	if meta.PageHeight > 0 && meta.PageHeight < meta.Height {
		frameH = meta.PageHeight
	}
	if meta.Width <= 0 || frameH <= 0 {
		return nil, errs.New(errs.KindCorrupt, "plan: source has no pixels")
	}
	if meta.Width > maxCoord || frameH > maxCoord {
		return nil, errs.New(errs.KindLimitExceeded, "plan: source dimensions exceed limits")
	}
	lim := resolveLimits(limits, &req.Limits)

	pipes := req.Pipelines
	if len(pipes) == 0 {
		pipes = []transform.Pipeline{{}}
	}
	format := strings.ToLower(meta.Format)
	outFmt := req.Output.Format
	if outFmt == transform.FormatUnset {
		outFmt, _ = transform.ParseFormat(format)
	}

	first := &pipes[0]
	src := source{
		w: meta.Width, h: frameH, orientation: meta.Orientation, alpha: meta.HasAlpha,
		format: format, vector: meta.IsVector, hasData: true, frames: 1, density: meta.Density,
	}
	nFrames := max(meta.Frames, 1)
	if first.Page != nil && *first.Page > 0 {
		src.page = min(*first.Page, nFrames-1)
	}
	if meta.IsAnimated && nFrames > 1 && lim.frames != 1 && !first.DisableAnimation && animationFormat(outFmt) {
		n := nFrames - src.page
		if first.Pages != nil && *first.Pages > 0 {
			n = min(n, *first.Pages)
		}
		if lim.frames > 0 {
			n = min(n, lim.frames)
		}
		if n > 1 {
			src.frames = n
			src.hasData = false // frames are processed one by one: no shrink-on-load
		}
	}
	if n := stackedPages(first, meta, &src, nFrames); n > 1 {
		src.stack = n
		src.hasData = src.vector // raster pages: no shrink-on-load or embedded thumbnail
		if !meta.Selection {
			// Predicted as n pages of the first one's size; the executor
			// replans on the loaded (or, for vectors, measured) strip.
			src.h = frameH * n
		}
	}
	if err := checkSource(&src, &lim); err != nil {
		return nil, err
	}

	root, err := planPipeline(first, &src, &lim, outFmt, obs, len(pipes) == 1)
	if err != nil {
		return nil, err
	}
	// Chained pipelines are predicted on the previous prediction; the
	// executor plans each again on the observed result (Chain).
	prev := root
	for i := 1; i < len(pipes); i++ {
		out := &prev.Output
		in := Input{Width: out.Width, Height: out.Height, Frames: out.Frames, Alpha: out.Alpha}
		pl, err := planChained(pipes, i, &in, &lim, outFmt, nil)
		if err != nil {
			return nil, fmt.Errorf("plan: pipeline %d: %w", i, err)
		}
		root.Chained = append(root.Chained, pl)
		prev = pl
	}
	chainCost(root)
	if lim.memory > 0 && root.Cost.EstimatedBytes > lim.memory {
		return nil, errs.New(errs.KindLimitExceeded, "plan: estimated memory exceeds the processing budget")
	}
	return root, nil
}

// stackedPages is the number of pages the `pages` option stacks into one
// image (imgproxy Pro: "the pages will be stacked vertically and
// left-aligned"): multi-page stills only, from the selected page on. PSD
// "pages" are layers of their own sizes, never stacked.
func stackedPages(p *transform.Pipeline, meta *SourceMeta, s *source, nFrames int) int {
	if s.frames > 1 || meta.IsAnimated || nFrames <= 1 || p.Pages == nil || s.format == "psd" {
		return 1
	}
	n := nFrames - s.page
	if *p.Pages > 0 {
		n = min(n, *p.Pages)
	}
	return n
}

// checkSource is imgproxy's security CheckDimensions on the loaded source.
func checkSource(s *source, lim *effLimits) error {
	if s.vector {
		return nil // rendered at the requested size
	}
	px := int64(s.w) * int64(s.h)
	if s.frames > 1 && lim.framePixels > 0 {
		if px > lim.framePixels {
			return errs.New(errs.KindLimitExceeded, "plan: source frame resolution exceeds limits")
		}
		return nil
	}
	if lim.srcPixels > 0 && px*int64(s.frames) > lim.srcPixels {
		return errs.New(errs.KindLimitExceeded, "plan: source resolution exceeds limits")
	}
	return nil
}

func animationFormat(f transform.Format) bool {
	return f == transform.FormatGIF || f == transform.FormatWebP
}

func alphaFormat(f transform.Format) bool {
	switch f {
	case transform.FormatJPEG, transform.FormatMP4, transform.FormatWebM:
		return false
	default:
		return true
	}
}

func jpegShrink(s float64) int {
	switch {
	case s >= 8:
		return 8
	case s >= 4:
		return 4
	case s >= 2:
		return 2
	}
	return 1
}

// builder carries the state of one pipeline while its steps are emitted.
type builder struct {
	pl       *Plan
	curW     int
	curH     int
	alpha    bool
	random   bool
	frames   int64
	peak     int64
	tooLarge bool
	// geoFrom is the index of the first geometry step; geoW, geoH the
	// size before it (stored orientation), exifAngle and exifFlip the
	// orientation the geometry applies. Filters read them.
	geoFrom, geoW, geoH int
	exifAngle           int
	exifFlip            bool
}

func (b *builder) add(s Step) { b.pl.Steps = append(b.pl.Steps, s) }

// size records the current image size for the cost estimate.
func (b *builder) size(w, h int) {
	b.curW, b.curH = w, h
	if w <= 0 || h <= 0 || w > maxCoord || h > maxCoord {
		b.tooLarge = true
		return
	}
	b.peak = max(b.peak, int64(w)*int64(h)*b.frames)
}

func (b *builder) oriented(g *geo, rotate, w, h int) (int, int) {
	if (g.angle+rotate)%180 != 0 {
		return h, w
	}
	return w, h
}

func planPipeline(p *transform.Pipeline, s *source, lim *effLimits, outFmt transform.Format, obs *Observed, last bool) (*Plan, error) {
	p = pixelFocus(p, s)
	o := newOpts(p, lim.resultDim)
	b := &builder{
		pl:     &Plan{Steps: make([]Step, 0, 12)},
		alpha:  s.alpha,
		frames: int64(s.frames),
	}
	g := geo{vectorBaseShrink: 1}
	g.angle, g.flip = orientation(s.orientation, o.autoRotate)
	if p.Crop != nil && p.Crop.Gravity.Type != transform.GravityUnset {
		g.cropGravity, _ = resolveGravity(&p.Crop.Gravity)
	} else {
		g.cropGravity, _ = resolveGravity(&p.Gravity)
	}
	cropSmart := InterestingNone
	if p.Crop != nil {
		cg := p.Crop.Gravity
		if cg.Type == transform.GravityUnset {
			cg = p.Gravity
		}
		_, cropSmart = resolveGravity(&cg)
	}
	animated := s.frames > 1

	load := LoadStep{Page: s.page, NPages: max(s.frames, s.stack), Width: s.w, Height: s.h}
	if s.vector {
		load.Density = s.density
	}
	b.size(s.w, s.h)
	hasData := s.hasData

	// Vector guard: render oversized vectors at the resolution limit.
	if hasData && s.vector && lim.srcPixels > 0 {
		if res := int64(s.w) * int64(s.h); res > lim.srcPixels {
			g.vectorBaseShrink = math.Sqrt(float64(res) / float64(lim.srcPixels))
			load.Scale = 1 / g.vectorBaseShrink
			load.Width = max(1, shrinkDimEven(s.w, g.vectorBaseShrink))
			load.Height = max(1, shrinkDimEven(s.h, g.vectorBaseShrink))
			b.peak = 0
			b.size(load.Width, load.Height)
		}
	}

	b.add(ColourStep{Stage: ColourToProcessing})
	if p.Trim != nil && !animated {
		t := p.Trim
		b.add(TrimStep{Threshold: t.Threshold, Color: t.Color, ColorFromBottomRight: t.ColorFromBottomRight, EqualHor: t.EqualHor, EqualVer: t.EqualVer})
		b.random = true
		hasData = false // imgproxy drops the source after trim: no shrink-on-load
		if obs != nil && obs.Trimmed {
			b.size(obs.Width, obs.Height)
		}
	}
	o.vector = hasData && s.vector
	g.srcW, g.srcH = b.oriented(&g, o.rotate, b.curW, b.curH)
	g.calcParams(&o)

	if hasData {
		if err := scaleOnLoad(&g, &o, s, lim, obs, &load, b); err != nil {
			return nil, err
		}
	}
	if !g.valid() {
		return nil, errs.New(errs.KindLimitExceeded, "plan: result dimensions exceed limits")
	}
	b.pl.Load = load

	b.geoFrom, b.geoW, b.geoH = len(b.pl.Steps), b.curW, b.curH
	b.exifAngle, b.exifFlip = g.angle, g.flip
	emitGeometry(p, &g, &o, b, cropSmart, animated)
	emitFinish(p, &g, b, outFmt, s.frames, last)

	if b.tooLarge {
		return nil, errs.New(errs.KindLimitExceeded, "plan: result dimensions exceed limits")
	}
	if b.random {
		b.pl.Load.Access = AccessRandom
	}
	if animated {
		b.pl.Animation = &AnimationStep{Frames: s.frames, PageHeight: load.Height}
	}
	b.pl.Output.Width, b.pl.Output.Height = b.curW, b.curH
	b.pl.Output.Frames = s.frames
	b.pl.Output.Format = outFmt
	b.pl.Output.Alpha = b.alpha
	b.pl.Output.DPRScale = g.dprScale
	b.pl.Cost = cost(b)
	b.pl.Unplanned = unplanned(p)
	return b.pl, nil
}

// valid reports whether the computed sizes are usable.
func (g *geo) valid() bool {
	for _, v := range [...]int{g.targetW, g.targetH, g.scaledW, g.scaledH, g.resultCropW, g.resultCropH, g.exarW, g.exarH, g.cropW, g.cropH} {
		if v < 0 || v > maxCoord {
			return false
		}
	}
	ok := func(f float64) bool { return f > 0 && !math.IsInf(f, 0) }
	return ok(g.wScale) && ok(g.hScale) && ok(g.dprScale)
}

// scaleOnLoad is imgproxy's scaleOnLoad: pick the loader shrink from the
// scales CalcParams computed, then rebase the scales on the loaded size.
func scaleOnLoad(g *geo, o *opts, s *source, lim *effLimits, obs *Observed, load *LoadStep, b *builder) error {
	// Shrink by the size the scales would give, not by the scales, to avoid
	// overshrinking when only one target dimension is set.
	wshrink := float64(g.srcW) / float64(max(1, scaleDim(g.srcW, g.wScale)))
	hshrink := float64(g.srcH) / float64(max(1, scaleDim(g.srcH, g.hScale)))
	pre := min(wshrink, hshrink)
	if s.vector {
		pre *= g.vectorBaseShrink
	}
	if pre == 1 || math.IsNaN(pre) || math.IsInf(pre, 0) {
		return nil
	}
	jpeg := s.format == "jpeg" || s.format == "jpg"
	webp := s.format == "webp"
	thumb := s.format == "heic" || s.format == "heif" || s.format == "avif"
	if !s.vector && (lim.noShrinkOnLoad || pre <= 1 || (!jpeg && !webp && !thumb)) {
		return nil
	}

	var nw, nh int
	switch {
	case thumb && !s.vector:
		if obs == nil || !obs.Thumbnail {
			load.Thumbnail = true // the executor reports the thumbnail size
			return nil
		}
		nw, nh = obs.Width, obs.Height
		ow, oh := b.oriented(g, o.rotate, nw, nh)
		wp, hp := float64(g.srcW)/float64(ow), float64(g.srcH)/float64(oh)
		if min(wp, hp) <= 1 || max(wp, hp) > pre {
			return nil // not worth it: load the full image
		}
		load.Thumbnail = true
	case jpeg && !s.vector:
		sh := jpegShrink(pre)
		if sh == 1 {
			return nil
		}
		load.ShrinkOnLoad = sh
		nw, nh = s.w/sh, s.h/sh // libjpeg DCT scaling rounds down in libvips
	case webp && !s.vector:
		load.Scale = 1 / pre
		nw, nh = roundEvenInt(float64(s.w)*load.Scale), roundEvenInt(float64(s.h)*load.Scale)
	default: // vector
		load.Scale = 1 / pre
		nw, nh = shrinkDimEven(s.w, pre), shrinkDimEven(s.h, pre)
	}
	if obs != nil && obs.Loaded {
		nw, nh = obs.Width, obs.Height
	}
	nw, nh = max(1, nw), max(1, nh)
	if nw > maxCoord || nh > maxCoord {
		return errs.New(errs.KindLimitExceeded, "plan: result dimensions exceed limits")
	}
	load.Width, load.Height = nw, nh
	b.peak = 0 // the full-size image is never decoded
	b.size(nw, nh)

	ow, oh := b.oriented(g, o.rotate, nw, nh)
	wp := float64(g.srcW) / float64(ow)
	hp := float64(g.srcH) / float64(oh)
	g.wScale *= wp
	g.hScale *= hp
	// An exact preshrink needs no further resize.
	if ow == scaleDim(ow, g.wScale) {
		g.wScale = 1
	}
	if oh == scaleDim(oh, g.hScale) {
		g.hScale = 1
	}
	if g.cropW > 0 {
		g.cropW = max(1, shrinkDim(g.cropW, wp))
	}
	if g.cropH > 0 {
		g.cropH = max(1, shrinkDim(g.cropH, hp))
	}
	// Absolute crop offsets follow the preshrink; relative ones and focus
	// points do not.
	if g.cropGravity.Type != transform.GravityFocusPoint {
		if math.Abs(g.cropGravity.X) >= 1 {
			g.cropGravity.X = math.RoundToEven(g.cropGravity.X / wp)
		}
		if math.Abs(g.cropGravity.Y) >= 1 {
			g.cropGravity.Y = math.RoundToEven(g.cropGravity.Y / hp)
		}
	}
	return nil
}

// emitGeometry emits pre-crop, resize, orientation and the result crop.
func emitGeometry(p *transform.Pipeline, g *geo, o *opts, b *builder, cropSmart Interesting, animated bool) {
	if animated {
		// libvips smart crop cannot run on a strip; the executor would have
		// to crop every frame differently. Fall back to the centre.
		cropSmart = InterestingNone
	}
	if g.cropW > 0 || g.cropH > 0 {
		w, h := g.cropW, g.cropH
		cg := g.cropGravity
		// The crop runs before rotation and flips: undo them on the gravity,
		// user operations first, then the EXIF orientation.
		cg.undoRotateFlip(o.rotate, o.flipX, o.flipY)
		cg.undoRotateFlip(g.angle, g.flip, false)
		if (g.angle+o.rotate)%180 == 90 {
			w, h = h, w
		}
		c := CropGeometry{Width: w, Height: h, Gravity: cg, OffsetScale: 1, Smart: cropSmart}
		if x, y, cw, ch, ok := c.Rect(b.curW, b.curH); ok {
			c.X, c.Y, c.W, c.H = x, y, cw, ch
			b.add(PreCropStep{c})
			b.random = b.random || c.Smart != InterestingNone
			b.size(cw, ch)
		}
	}

	if g.wScale != 1 || g.hScale != 1 {
		ws, hs := g.wScale, g.hScale
		if (g.angle+o.rotate)%180 == 90 {
			ws, hs = hs, ws
		}
		w, h := resizedDim(b.curW, ws), resizedDim(b.curH, hs)
		b.add(ResizeStep{ScaleX: ws, ScaleY: hs, Kernel: p.Resize.Algorithm, Premultiply: b.alpha, W: w, H: h})
		b.size(w, h)
	}

	if g.angle != 0 {
		b.add(RotateStep{Angle: g.angle})
		b.random = true
		if g.angle != 180 {
			b.size(b.curH, b.curW)
		}
	}
	if g.flip {
		b.add(FlipStep{Horizontal: true})
		b.random = true
	}
	if o.rotate != 0 {
		b.add(RotateStep{Angle: o.rotate})
		b.random = true
		if o.rotate != 180 {
			b.size(b.curH, b.curW)
		}
	}
	if o.flipX || o.flipY {
		b.add(FlipStep{Horizontal: o.flipX, Vertical: o.flipY})
		b.random = true
	}

	rg, smart := resolveGravity(&p.Gravity)
	if animated {
		smart = InterestingNone
	}
	c := CropGeometry{Width: g.resultCropW, Height: g.resultCropH, Gravity: rg, OffsetScale: g.dprScale, Smart: smart}
	if x, y, cw, ch, ok := c.Rect(b.curW, b.curH); ok {
		c.X, c.Y, c.W, c.H = x, y, cw, ch
		b.add(CropStep{c})
		b.random = b.random || smart != InterestingNone
		b.size(cw, ch)
	}
}

// emitFinish emits filters, colour adjustments, extend, padding, output size fixes, flatten,
// watermarks and the output colour.
func emitFinish(p *transform.Pipeline, g *geo, b *builder, outFmt transform.Format, frames int, last bool) {
	if fs, ok := filterStep(p, b); ok {
		b.add(fs)
		b.random = true
	}
	if st, ok := colourAdjust(p); ok {
		b.add(st)
		b.alpha = b.alpha || st.Adjust.Opacity != nil
	}

	extend := func(e *transform.Extend, w, h int, ar bool) {
		eg, _ := resolveGravity(&e.Gravity)
		st := ExtendStep{
			Width: w, Height: h, Gravity: eg, OffsetScale: g.dprScale,
			AspectRatio: ar, Mode: e.Mode, Background: p.Background,
		}
		if x, y, cw, ch, ok := st.Rect(b.curW, b.curH); ok {
			st.X, st.Y, st.W, st.H = x, y, cw, ch
			b.add(st)
			b.size(cw, ch)
			b.alpha = b.alpha || p.Background == nil
		}
	}
	if p.Extend.Enabled {
		extend(&p.Extend, g.targetW, g.targetH, false)
	}
	if p.ExtendAR.Enabled && g.exarW > 0 && g.exarH > 0 {
		extend(&p.ExtendAR, g.exarW, g.exarH, true)
	}

	if pd := p.Padding; pd != nil && (pd.Top != 0 || pd.Right != 0 || pd.Bottom != 0 || pd.Left != 0) {
		st := PaddingStep{
			Top:        scaleDimEven(pd.Top, g.dprScale),
			Right:      scaleDimEven(pd.Right, g.dprScale),
			Bottom:     scaleDimEven(pd.Bottom, g.dprScale),
			Left:       scaleDimEven(pd.Left, g.dprScale),
			Background: p.Background,
		}
		b.add(st)
		b.size(b.curW+st.Left+st.Right, b.curH+st.Top+st.Bottom)
		b.alpha = b.alpha || p.Background == nil
	}

	if s := fixSize(outFmt, b.curW, b.curH); s < 1 {
		w, h := resizedDim(b.curW, s), resizedDim(b.curH, s)
		b.add(ResizeStep{ScaleX: s, ScaleY: s, Kernel: p.Resize.Algorithm, Premultiply: b.alpha, W: w, H: h})
		b.size(w, h)
	}

	if p.Background != nil || !alphaFormat(outFmt) {
		bg := transform.Color{R: 255, G: 255, B: 255, A: 255}
		if p.Background != nil {
			bg = *p.Background
		}
		// A translucent background (imgproxy background_alpha) keeps the
		// alpha band, unless the format cannot hold one.
		if bg.A < 255 && alphaFormat(outFmt) {
			b.add(FlattenStep{Background: bg})
		} else {
			bg.A = 255
			b.add(FlattenStep{Background: bg})
			b.alpha = false
		}
	}

	for i := range p.Watermarks {
		w := &p.Watermarks[i]
		pos, _ := resolveGravity(&w.Position)
		b.pl.After = append(b.pl.After, WatermarkStep{
			Index: i, Opacity: w.Opacity, Position: pos, Tile: w.Tile, Scale: w.Scale, DPRScale: g.dprScale,
			Frames: frames, Default: w.Source == nil && w.Text == "",
		})
	}
	if last {
		b.pl.After = append(b.pl.After, ColourStep{Stage: ColourToResult})
	}
}

// fixSize is imgproxy's fixSize: the scale an encoder's dimension limits
// require (1 when none).
func fixSize(f transform.Format, w, h int) float64 {
	side := float64(max(w, h))
	var limit float64
	switch f {
	case transform.FormatWebP:
		limit = 16383
	case transform.FormatAVIF, transform.FormatHEIC:
		limit = 16384
	case transform.FormatICO:
		limit = 256
	case transform.FormatGIF:
		shrink := math.Max(float64(w)*float64(h)/float64(math.MaxInt32/4), side/65535)
		if shrink <= 1 {
			return 1
		}
		return math.Sqrt(1 / shrink)
	default:
		return 1
	}
	if shrink := side / limit; shrink > 1 {
		return 1 / shrink
	}
	return 1
}

func cost(b *builder) Cost {
	bands := int64(3)
	if b.alpha || b.pl.Load.Thumbnail {
		bands = 4
	}
	bytes := b.peak * bands
	if b.random {
		bytes *= 2
	}
	return Cost{Pixels: b.peak, EstimatedBytes: bytes}
}

// unplanned lists options later work packages turn into steps.
func unplanned(p *transform.Pipeline) []string {
	var out []string
	add := func(set bool, name string) {
		if set {
			out = append(out, name)
		}
	}
	add(p.RotateAny != 0, "rotate_any")
	add(p.Border != nil, "border")
	add(p.Radius != nil, "radius")
	add(p.Mask != nil, "mask")
	add(p.Adjust.Vibrance != nil, "vibrance")
	add(p.Equalize, "equalize")
	add(p.BlurDetections != nil || p.DrawDetections != nil || p.CropObjects != nil, "detections")
	add(p.Crop != nil && p.Crop.AspectRatio != 0, "crop_aspect_ratio")
	add(p.Video != nil, "video")
	return out
}

// colourAdjust is the ColourAdjustStep of p; ok is false when p has no
// colour adjustment or effect to apply.
func colourAdjust(p *transform.Pipeline) (ColourAdjustStep, bool) {
	a := p.Adjust
	a.Vibrance = nil // not applied: listed in Unplanned
	st := ColourAdjustStep{
		Adjust: a, Grayscale: p.Grayscale, Sepia: p.Sepia, Negate: p.Negate, Tint: p.Tint,
		Monochrome: p.Monochrome, Duotone: p.Duotone, Colorize: p.Colorize, Gradient: p.Gradient,
	}
	ok := a.Brightness != nil || a.Contrast != nil || a.Saturation != nil || a.Gamma != nil || a.Hue != nil ||
		a.Exposure != nil || a.Red != nil || a.Green != nil || a.Blue != nil || a.Opacity != nil ||
		st.Grayscale || st.Sepia || st.Negate || st.Tint != nil || st.Monochrome != nil || st.Duotone != nil ||
		st.Colorize != nil || st.Gradient != nil
	return st, ok
}
