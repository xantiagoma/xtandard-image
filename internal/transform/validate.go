package transform

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Sanity bounds. Config limits (XI_LIMITS_*) are enforced later by the
// planner; these only reject values no config could make meaningful and keep
// integer arithmetic downstream far from overflow.
const (
	MaxDimension = 1 << 16 // px, any width/height/offset/padding/border
	MaxRelative  = 16.0    // relative dimension, 1600 %
	MaxFactor    = 1000.0  // zoom, DPR, aspect ratio, focus zoom, watermark scale
	// MaxAutoQualityTarget bounds the autoquality target, which is a byte
	// count for the size method (imgproxy aq:size:10240).
	MaxAutoQualityTarget = 1 << 30
	MaxSigma             = 1000.0 // blur, sharpen and shadow sigma (wsrv's maximum)
	MaxPipelines         = 16
	MaxWatermarks        = 16
	MaxBlurAreas         = 64
	MaxListLen           = 64 // classes, cache tags, skip formats, hashsums
	MaxEffort            = 9
	MaxQuality           = 100
	MaxBrightness        = 255.0
	MaxDPI               = 10000
	MaxPaletteSize       = 256
	MaxBlurhashDim       = 9
	MaxJPEGQuant         = 8
	MaxPNGLevel          = 9
	// MaxWatermarkText bounds a text watermark's markup in bytes, and
	// MaxFontDescription its font description.
	MaxWatermarkText   = 4096
	MaxFontDescription = 128
)

// ValidationError reports one invalid field. Validate joins several with
// errors.Join; find them with errors.AsType[*ValidationError].
//
// It satisfies the server's statusError interface (go-standards §6): every
// ValidationError is a client error, and its message is built only from the
// field path and a static reason, so it is safe to return to clients.
type ValidationError struct {
	Field  string // path, for example "pipelines[1].resize.width.px"
	Reason string // static text, for example "must be between 0 and 65536"
}

func (e *ValidationError) Error() string {
	return "transform: invalid " + e.Field + ": " + e.Reason
}

// HTTPStatus is always 400.
func (e *ValidationError) HTTPStatus() int { return http.StatusBadRequest }

// PublicMessage is safe to show to clients.
func (e *ValidationError) PublicMessage() string { return "invalid " + e.Field + ": " + e.Reason }

// Validate checks every field against its allowed range. Call it after
// ApplyDefaults: it expects the effective form (at least one pipeline,
// Rotate in 0/90/180/270, RotateAny in (−180, 180]). It returns nil or an
// errors.Join of *ValidationError values.
func (r *Request) Validate() error {
	v := &validator{}
	v.enum("kind", validEnum(r.Kind, numKinds))
	if r.Info != nil && r.Kind != KindInfo {
		v.fail("info", "set but kind is not info")
	}
	v.source("source", &r.Source, true)

	switch n := len(r.Pipelines); {
	case n == 0:
		v.fail("pipelines", "at least one pipeline required")
	case n > MaxPipelines:
		v.fail("pipelines", "at most "+strconv.Itoa(MaxPipelines)+" pipelines")
	}
	for i := range r.Pipelines {
		if i >= MaxPipelines {
			break
		}
		r.Pipelines[i].validate(v, "pipelines["+strconv.Itoa(i)+"]")
	}

	r.Output.validate(v, "output")
	r.Delivery.validate(v, "delivery")
	v.intRange("client_hints.width_step", r.ClientHints.WidthStep, 0, MaxDimension)
	r.Limits.validate(v, "limits")
	if r.Info != nil {
		r.Info.validate(v, "info")
	}
	return errors.Join(v.errs...)
}

// validator accumulates ValidationErrors.
type validator struct {
	errs []error
}

func (v *validator) fail(field, reason string) {
	v.errs = append(v.errs, &ValidationError{Field: field, Reason: reason})
}

func (v *validator) enum(field string, ok bool) {
	if !ok {
		v.fail(field, "unknown value")
	}
}

func between(lo, hi string) string { return "must be between " + lo + " and " + hi }

func (v *validator) intRange(field string, x, lo, hi int) {
	if x < lo || x > hi {
		v.fail(field, between(strconv.Itoa(lo), strconv.Itoa(hi)))
	}
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// floatRange checks lo ≤ x ≤ hi and rejects NaN/Inf.
func (v *validator) floatRange(field string, x, lo, hi float64) {
	if math.IsNaN(x) || x < lo || x > hi {
		v.fail(field, between(fmtFloat(lo), fmtFloat(hi)))
	}
}

func (v *validator) optFloat(field string, p *float64, lo, hi float64) {
	if p != nil {
		v.floatRange(field, *p, lo, hi)
	}
}

func (v *validator) list(field string, n int) {
	if n > MaxListLen {
		v.fail(field, "at most "+strconv.Itoa(MaxListLen)+" entries")
	}
}

func (v *validator) strings(field string, ss []string) {
	v.list(field, len(ss))
	for i, s := range ss {
		if s == "" {
			v.fail(field+"["+strconv.Itoa(i)+"]", "must not be empty")
		}
	}
}

// classes checks a detection class list: bounded, and every name a class
// ResolveClass knows.
func (v *validator) classes(field string, ss []string) {
	v.list(field, len(ss))
	for i, s := range ss {
		if i >= MaxListLen {
			break
		}
		if _, ok := ResolveClass(s); !ok {
			v.fail(field+"["+strconv.Itoa(i)+"]", "unknown class (face, a COCO class or supercategory)")
		}
	}
}

func (v *validator) source(field string, s *SourceSpec, required bool) {
	if required && s.Raw == "" {
		v.fail(field+".raw", "must not be empty")
	}
	v.enum(field+".encoding", validEnum(s.Encoding, numSourceEncodings))
	if p := s.Password; p != nil {
		switch {
		case len(p.Secret) == 0 || len(p.Secret) > MaxPasswordLen:
			v.fail(field+".password", "must be 1 to "+strconv.Itoa(MaxPasswordLen)+" bytes")
		case !isLowerHex(p.Digest, sha256HexLen):
			v.fail(field+".password.digest", "must be 64 lowercase hex digits")
		}
	}
	if h := s.Hashsum; h != nil {
		if h.Algorithm == HashUnset || !validEnum(h.Algorithm, numHashAlgorithms) {
			v.fail(field+".hashsum.algorithm", "unknown value")
		} else if !isLowerHex(h.Digest, h.Algorithm.HexLen()) {
			v.fail(field+".hashsum.digest", "must be "+strconv.Itoa(h.Algorithm.HexLen())+" lowercase hex digits")
		}
	}
}

// sha256HexLen is the length of a hex SHA-256 or HMAC-SHA256
// (DocumentPassword.Digest).
const sha256HexLen = 64

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (v *validator) dim(field string, d Dim) {
	v.intRange(field+".px", d.Px, 0, MaxDimension)
	v.floatRange(field+".rel", d.Rel, 0, MaxRelative)
	if d.Px != 0 && d.Rel != 0 {
		v.fail(field, "px and rel are mutually exclusive")
	}
}

// gravity checks g. crop is set for the gravities that place a crop
// (Pipeline.Gravity, Crop.Gravity): only they accept content-aware types
// and a pixel focus.
func (v *validator) gravity(field string, g *Gravity, crop bool) {
	switch {
	case !validEnum(g.Type, numGravityTypes):
		v.fail(field+".type", "unknown value")
	case !crop && g.Type.IsDetection():
		v.fail(field+".type", "content-aware gravity not allowed here")
	}
	if r := g.FocusPx; r != nil {
		v.focusPx(field+".focus_px", g, r, crop)
	}
	v.floatRange(field+".x_offset", g.XOffset, -MaxDimension, MaxDimension)
	v.floatRange(field+".y_offset", g.YOffset, -MaxDimension, MaxDimension)
	v.floatRange(field+".focus_x", g.FocusX, 0, 1)
	v.floatRange(field+".focus_y", g.FocusY, 0, 1)
	v.floatRange(field+".focus_zoom", g.FocusZoom, 0, MaxFactor)
	v.classes(field+".classes", g.Classes)
	if len(g.Weights) > 0 && len(g.Weights) != len(g.Classes) {
		v.fail(field+".weights", "must have one weight per class")
	}
	for i, w := range g.Weights {
		v.floatRange(field+".weights["+strconv.Itoa(i)+"]", w, 0, MaxFactor)
	}
	if g.FaceIndex != nil {
		v.intRange(field+".face_index", *g.FaceIndex, 0, MaxListLen)
	}
	v.floatRange(field+".face_pad", g.FacePad, 0, MaxFactor)
	if op := g.ObjectsPosition; op != nil {
		if !op.Proportional && !op.Type.IsCompass() && op.Type != GravityFocusPoint {
			v.fail(field+".objects_position.type", "must be a compass point or focus-point")
		}
		v.floatRange(field+".objects_position.x", op.X, -MaxDimension, MaxDimension)
		v.floatRange(field+".objects_position.y", op.Y, -MaxDimension, MaxDimension)
	}
}

func (v *validator) focusPx(field string, g *Gravity, r *FocusRect, crop bool) {
	switch {
	case !crop:
		v.fail(field, "pixel focus not allowed here")
	case g.Type != GravityFocusPoint:
		v.fail(field, "requires focus-point gravity")
	case g.FocusX != 0 || g.FocusY != 0:
		v.fail(field, "focus_x and focus_y must be 0 with a pixel focus")
	}
	v.intRange(field+".x1", r.X1, 0, MaxSourceCoord)
	v.intRange(field+".y1", r.Y1, 0, MaxSourceCoord)
	v.intRange(field+".x2", r.X2, r.X1, MaxSourceCoord)
	v.intRange(field+".y2", r.Y2, r.Y1, MaxSourceCoord)
}

func (v *validator) crop(f string, c *Crop) {
	v.dim(f+".width", c.Width)
	v.dim(f+".height", c.Height)
	v.gravity(f+".gravity", &c.Gravity, true)
	v.floatRange(f+".aspect_ratio", c.AspectRatio, 0, MaxFactor)
}

func (v *validator) direction(field string, d Direction, angle float64) {
	v.enum(field+".direction", validEnum(d, numDirections))
	v.floatRange(field+".angle", angle, -360, 360)
}

func (p *Pipeline) validate(v *validator, f string) {
	rs := &p.Resize
	v.enum(f+".resize.type", rs.Type != ResizeUnset && validEnum(rs.Type, numResizeTypes))
	v.dim(f+".resize.width", rs.Width)
	v.dim(f+".resize.height", rs.Height)
	v.intRange(f+".resize.min_width", rs.MinWidth, 0, MaxDimension)
	v.intRange(f+".resize.min_height", rs.MinHeight, 0, MaxDimension)
	v.intRange(f+".resize.max_width", rs.MaxWidth, 0, MaxDimension)
	v.intRange(f+".resize.max_height", rs.MaxHeight, 0, MaxDimension)
	v.floatRange(f+".resize.aspect_ratio", rs.AspectRatio, 0, MaxFactor)
	positive(v, f+".resize.zoom_x", rs.ZoomX)
	positive(v, f+".resize.zoom_y", rs.ZoomY)
	positive(v, f+".resize.dpr", rs.DPR)
	v.enum(f+".resize.algorithm", rs.Algorithm != KernelUnset && validEnum(rs.Algorithm, numKernels))

	v.gravity(f+".gravity", &p.Gravity, true)
	if p.Crop != nil {
		v.crop(f+".crop", p.Crop)
	}
	if c := p.CropObjects; c != nil {
		v.floatRange(f+".crop_objects.scale", c.Scale, 0, MaxFactor)
		v.classes(f+".crop_objects.classes", c.Classes)
		if c.Index != nil {
			v.intRange(f+".crop_objects.index", *c.Index, 0, MaxListLen)
		}
		v.intRange(f+".crop_objects.padding", c.Padding, 0, MaxDimension)
	}
	if t := p.Trim; t != nil {
		v.floatRange(f+".trim.threshold", t.Threshold, 0, 255)
	}
	if p.Rotate%90 != 0 || p.Rotate < 0 || p.Rotate >= 360 {
		v.fail(f+".rotate", "must be 0, 90, 180 or 270")
	}
	if math.IsNaN(p.RotateAny) || p.RotateAny <= -180 || p.RotateAny > 180 {
		v.fail(f+".rotate_any", "must be in (-180, 180]")
	}
	if pd := p.Padding; pd != nil {
		v.intRange(f+".padding.top", pd.Top, 0, MaxDimension)
		v.intRange(f+".padding.right", pd.Right, 0, MaxDimension)
		v.intRange(f+".padding.bottom", pd.Bottom, 0, MaxDimension)
		v.intRange(f+".padding.left", pd.Left, 0, MaxDimension)
	}
	p.Extend.validate(v, f+".extend")
	p.ExtendAR.validate(v, f+".extend_ar")
	if b := p.Border; b != nil {
		v.intRange(f+".border.top", b.Top, 0, MaxDimension)
		v.intRange(f+".border.right", b.Right, 0, MaxDimension)
		v.intRange(f+".border.bottom", b.Bottom, 0, MaxDimension)
		v.intRange(f+".border.left", b.Left, 0, MaxDimension)
	}
	if r := p.Radius; r != nil {
		v.intRange(f+".radius.top_left", r.TopLeft, 0, MaxDimension)
		v.intRange(f+".radius.top_right", r.TopRight, 0, MaxDimension)
		v.intRange(f+".radius.bottom_right", r.BottomRight, 0, MaxDimension)
		v.intRange(f+".radius.bottom_left", r.BottomLeft, 0, MaxDimension)
	}
	if m := p.Mask; m != nil {
		v.enum(f+".mask.shape", m.Shape != MaskUnset && validEnum(m.Shape, numMaskShapes))
	}
	p.Adjust.validate(v, f+".adjust")
	if m := p.Monochrome; m != nil {
		v.floatRange(f+".monochrome.intensity", m.Intensity, 0, 1)
	}
	if d := p.Duotone; d != nil {
		v.floatRange(f+".duotone.intensity", d.Intensity, 0, 1)
	}
	if c := p.Colorize; c != nil {
		v.floatRange(f+".colorize.opacity", c.Opacity, 0, 1)
	}
	if g := p.Gradient; g != nil {
		v.floatRange(f+".gradient.opacity", g.Opacity, 0, 1)
		v.direction(f+".gradient", g.Direction, g.Angle)
		v.floatRange(f+".gradient.start", g.Start, 0, 1)
		v.floatRange(f+".gradient.stop", g.Stop, 0, 1)
	}
	v.floatRange(f+".blur", p.Blur, 0, MaxSigma)
	v.floatRange(f+".sharpen", p.Sharpen, 0, MaxSigma)
	if u := p.Unsharp; u != nil {
		v.enum(f+".unsharp.mode", validEnum(u.Mode, numUnsharpModes))
		v.floatRange(f+".unsharp.weight", u.Weight, 0, MaxFactor)
		v.floatRange(f+".unsharp.divider", u.Divider, 0, MaxFactor)
		v.floatRange(f+".unsharp.sigma", u.Sigma, 0, MaxSigma)
		v.floatRange(f+".unsharp.amount", u.Amount, -MaxFactor, MaxFactor)
	}
	v.intRange(f+".pixelate", p.Pixelate, 0, MaxDimension)
	if ba := p.BlurAreas; ba != nil {
		v.floatRange(f+".blur_areas.sigma", ba.Sigma, 0, MaxSigma)
		if len(ba.Areas) > MaxBlurAreas {
			v.fail(f+".blur_areas.areas", "at most "+strconv.Itoa(MaxBlurAreas)+" areas")
		}
		for i, a := range ba.Areas {
			af := f + ".blur_areas.areas[" + strconv.Itoa(i) + "]"
			v.floatRange(af+".left", a.Left, 0, 1)
			v.floatRange(af+".top", a.Top, 0, 1)
			v.floatRange(af+".width", a.Width, 0, 1)
			v.floatRange(af+".height", a.Height, 0, 1)
		}
	}
	if pb := p.ProgressiveBlur; pb != nil {
		v.floatRange(f+".progressive_blur.sigma", pb.Sigma, 0, MaxSigma)
		v.direction(f+".progressive_blur", pb.Direction, pb.Angle)
		v.floatRange(f+".progressive_blur.start", pb.Start, 0, 1)
		v.floatRange(f+".progressive_blur.stop", pb.Stop, 0, 1)
	}
	p.BlurDetections.validate(v, f+".blur_detections")
	p.DrawDetections.validate(v, f+".draw_detections")
	if len(p.Watermarks) > MaxWatermarks {
		v.fail(f+".watermarks", "at most "+strconv.Itoa(MaxWatermarks)+" watermarks")
	}
	for i := range p.Watermarks {
		p.Watermarks[i].validate(v, f+".watermarks["+strconv.Itoa(i)+"]")
	}
	if p.Page != nil {
		v.intRange(f+".page", *p.Page, 0, math.MaxInt32)
	}
	if p.Pages != nil && *p.Pages != -1 {
		v.intRange(f+".pages", *p.Pages, 1, math.MaxInt32)
	}
	if vo := p.Video; vo != nil {
		vo.validate(v, f+".video")
	}
}

func positive(v *validator, field string, x float64) {
	if math.IsNaN(x) || x <= 0 || x > MaxFactor {
		v.fail(field, "must be > 0 and <= "+fmtFloat(MaxFactor))
	}
}

func (e *Extend) validate(v *validator, f string) {
	v.gravity(f+".gravity", &e.Gravity, false)
	v.enum(f+".mode", validEnum(e.Mode, numExtendModes))
}

func (a *Adjust) validate(v *validator, f string) {
	v.optFloat(f+".brightness", a.Brightness, -MaxBrightness, MaxBrightness)
	v.optFloat(f+".contrast", a.Contrast, 0, MaxFactor)
	v.optFloat(f+".saturation", a.Saturation, 0, MaxFactor)
	if a.Gamma != nil {
		positive(v, f+".gamma", *a.Gamma)
	}
	v.optFloat(f+".hue", a.Hue, -360, 360)
	v.optFloat(f+".exposure", a.Exposure, 0, MaxFactor)
	v.optFloat(f+".vibrance", a.Vibrance, 0, MaxFactor)
	v.optFloat(f+".red", a.Red, 0, MaxFactor)
	v.optFloat(f+".green", a.Green, 0, MaxFactor)
	v.optFloat(f+".blue", a.Blue, 0, MaxFactor)
	v.optFloat(f+".opacity", a.Opacity, 0, 1)
}

func (d *DetectionFilter) validate(v *validator, f string) {
	if d == nil {
		return
	}
	v.floatRange(f+".sigma", d.Sigma, 0, MaxSigma)
	v.intRange(f+".pixelate", d.Pixelate, 0, MaxDimension)
	v.classes(f+".classes", d.Classes)
}

func (w *Watermark) validate(v *validator, f string) {
	v.floatRange(f+".opacity", w.Opacity, 0, 1)
	v.gravity(f+".position", &w.Position, false)
	v.enum(f+".tile", validEnum(w.Tile, numTileModes))
	v.floatRange(f+".scale", w.Scale, 0, MaxFactor)
	if w.Source != nil {
		v.source(f+".source", w.Source, true)
		if w.Text != "" {
			v.fail(f+".text", "source and text are mutually exclusive")
		}
	}
	if len(w.Text) > MaxWatermarkText {
		v.fail(f+".text", "at most "+strconv.Itoa(MaxWatermarkText)+" bytes")
	} else if !utf8.ValidString(w.Text) {
		v.fail(f+".text", "must be UTF-8")
	}
	if len(w.Font) > MaxFontDescription || !utf8.ValidString(w.Font) || strings.ContainsAny(w.Font, "<>&\"\x00") {
		v.fail(f+".font", "must be a font description of at most "+strconv.Itoa(MaxFontDescription)+" bytes")
	}
	if w.Text == "" && (w.Font != "" || w.Color != nil || w.TextWidth != 0) {
		v.fail(f+".text", "font, color and text width need a text")
	}
	v.intRange(f+".text_width", w.TextWidth, 0, MaxDimension)
	v.intRange(f+".width", w.Width, 0, MaxDimension)
	v.intRange(f+".height", w.Height, 0, MaxDimension)
	v.floatRange(f+".rotate", w.Rotate, -360, 360)
	v.floatRange(f+".shadow", w.Shadow, 0, MaxSigma)
}

func (vo *VideoOptions) validate(v *validator, f string) {
	if vo.Second != nil {
		v.floatRange(f+".second", *vo.Second, -math.MaxInt32, math.MaxInt32)
	}
	if t := vo.Tile; t != nil {
		v.floatRange(f+".tile.step", t.Step, 0, math.MaxInt32)
		v.intRange(f+".tile.columns", t.Columns, 1, MaxDimension)
		v.intRange(f+".tile.rows", t.Rows, 1, MaxDimension)
		v.intRange(f+".tile.tile_width", t.TileWidth, 0, MaxDimension)
		v.intRange(f+".tile.tile_height", t.TileHeight, 0, MaxDimension)
		v.floatRange(f+".tile.focus_x", t.FocusX, 0, 1)
		v.floatRange(f+".tile.focus_y", t.FocusY, 0, 1)
	}
	if a := vo.Animation; a != nil {
		v.floatRange(f+".animation.step", a.Step, 0, math.MaxInt32)
		v.floatRange(f+".animation.delay", a.Delay, 0, math.MaxInt32)
		v.intRange(f+".animation.frames", a.Frames, 1, MaxDimension)
		v.intRange(f+".animation.frame_width", a.FrameWidth, 0, MaxDimension)
		v.intRange(f+".animation.frame_height", a.FrameHeight, 0, MaxDimension)
		v.floatRange(f+".animation.focus_x", a.FocusX, 0, 1)
		v.floatRange(f+".animation.focus_y", a.FocusY, 0, 1)
	}
}

func (o *Output) validate(v *validator, f string) {
	v.enum(f+".format", validEnum(o.Format, numFormats))
	v.intRange(f+".quality", o.Quality, 0, MaxQuality)
	for format, q := range o.FormatQuality {
		ff := f + ".format_quality[" + format.String() + "]"
		if format == FormatUnset || !validEnum(format, numFormats) {
			v.fail(ff, "unknown format")
		}
		v.intRange(ff, q, 1, MaxQuality)
	}
	v.intRange(f+".slow_connection_quality", o.SlowConnectionQuality, 0, MaxQuality)
	if aq := o.AutoQuality; aq != nil {
		v.enum(f+".auto_quality.method", validEnum(aq.Method, numAutoQualityMethods))
		v.floatRange(f+".auto_quality.target", aq.Target, 0, MaxAutoQualityTarget)
		v.intRange(f+".auto_quality.min", aq.Min, 0, MaxQuality)
		v.intRange(f+".auto_quality.max", aq.Max, 0, MaxQuality)
		if aq.Min != 0 && aq.Max != 0 && aq.Min > aq.Max {
			v.fail(f+".auto_quality.min", "must not exceed max")
		}
		v.floatRange(f+".auto_quality.allowed_error", aq.AllowedError, 0, 1)
	}
	v.intRange(f+".max_bytes", o.MaxBytes, 0, math.MaxInt32)
	v.enum(f+".chroma_subsampling", validEnum(o.ChromaSubsampling, numSubsamplings))
	v.intRange(f+".effort", o.Effort, 0, MaxEffort)
	if j := o.JPEG; j != nil && j.QuantTable != nil {
		v.intRange(f+".jpeg.quant_table", *j.QuantTable, 0, MaxJPEGQuant)
	}
	if p := o.PNG; p != nil {
		if p.QuantizationColors != 0 {
			v.intRange(f+".png.quantization_colors", p.QuantizationColors, 2, MaxPaletteSize)
		}
		if p.Compression != nil {
			v.intRange(f+".png.compression", *p.Compression, 0, MaxPNGLevel)
		}
	}
	if w := o.WebP; w != nil {
		v.enum(f+".webp.compression", validEnum(w.Compression, numWebPCompressions))
		v.enum(f+".webp.preset", validEnum(w.Preset, numWebPPresets))
	}
	if a := o.AVIF; a != nil {
		v.enum(f+".avif.subsample", a.Subsample != Subsampling422 && validEnum(a.Subsample, numSubsamplings))
	}
	v.intRange(f+".dpi", o.DPI, 0, MaxDPI)
}

func (d *Delivery) validate(v *validator, f string) {
	if d.TTL < 0 {
		v.fail(f+".ttl", "must not be negative")
	}
	if d.Fallback != nil {
		v.source(f+".fallback", d.Fallback, true)
	}
	v.list(f+".skip_processing", len(d.SkipProcessing))
	for i, sf := range d.SkipProcessing {
		v.enum(f+".skip_processing["+strconv.Itoa(i)+"]", sf != FormatUnset && validEnum(sf, numFormats))
	}
	v.strings(f+".cache_tags", d.CacheTags)
	v.enum(f+".encoding", validEnum(d.Encoding, numResponseEncodings))
}

func (l *LimitOverrides) validate(v *validator, f string) {
	v.floatRange(f+".max_src_resolution", l.MaxSrcResolution, 0, math.MaxInt32)
	if l.MaxSrcFileSize < 0 {
		v.fail(f+".max_src_file_size", "must not be negative")
	}
	v.intRange(f+".max_animation_frames", l.MaxAnimationFrames, 0, math.MaxInt32)
	v.floatRange(f+".max_animation_frame_resolution", l.MaxAnimationFrameResolution, 0, math.MaxInt32)
	v.intRange(f+".max_result_dimension", l.MaxResultDimension, 0, MaxDimension)
}

func (in *InfoOptions) validate(v *validator, f string) {
	v.enum(f+".schema", validEnum(in.Schema, numInfoSchemas))
	if in.Crop != nil {
		v.crop(f+".crop", in.Crop)
	}
	if in.Palette != 0 {
		v.intRange(f+".palette", in.Palette, 2, MaxPaletteSize)
	}
	if b := in.Blurhash; b != nil {
		v.intRange(f+".blurhash.x_components", b.XComponents, 1, MaxBlurhashDim)
		v.intRange(f+".blurhash.y_components", b.YComponents, 1, MaxBlurhashDim)
	}
	v.classes(f+".detect_classes", in.DetectClasses)
	// The classifier's vocabulary is the deployment's (a mountable model):
	// class names are matched when the result is rendered.
	v.intRange(f+".classify_top_k", in.ClassifyTopK, 0, MaxListLen)
	v.strings(f+".classify_classes", in.ClassifyClasses)
	v.list(f+".calc_hashsums", len(in.CalcHashsums))
	for i, h := range in.CalcHashsums {
		v.enum(f+".calc_hashsums["+strconv.Itoa(i)+"]", h != HashUnset && validEnum(h, numHashAlgorithms))
	}
}
