package exec

import (
	"context"
	"encoding/binary"
	"math"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// ToneMap is the operator that maps scene-linear HDR sources (OpenEXR,
// Radiance, PFM, DDS BC6H) to 8-bit sRGB (XI_FORMATS_HDR_TONEMAP, FORMATS.md §1.4).
// Every operator works per channel on linear light, clips the result to
// [0, 1] and encodes it with the sRGB transfer function.
type ToneMap uint8

// Tone-mapping operators. W is the white point: the 99th-percentile
// luminance of the source, at least 1.
const (
	// ToneMapReinhard is extended Reinhard with white W:
	// x·(1 + x/W²)/(1 + x). W maps to 1; a source within [0, 1] (W = 1) is
	// left unchanged.
	ToneMapReinhard ToneMap = iota
	// ToneMapACES is Narkowicz's fitted ACES filmic curve
	// x(2.51x + 0.03)/(x(2.43x + 0.59) + 0.14) on x = c·e, with the usual
	// exposure e = 0.6, lowered to 7.2417/W when needed so that W still maps
	// to 1 (the curve reaches 1 at 7.2417).
	ToneMapACES
	// ToneMapClip clips at 1: no curve and no exposure change.
	ToneMapClip
)

// String returns the configuration name.
func (t ToneMap) String() string {
	switch t {
	case ToneMapACES:
		return "aces"
	case ToneMapClip:
		return "clip"
	default:
		return "reinhard"
	}
}

// ParseToneMap parses a configuration name ("reinhard", "aces", "clip").
func ParseToneMap(s string) (ToneMap, error) {
	switch strings.ToLower(s) {
	case "", "reinhard":
		return ToneMapReinhard, nil
	case "aces":
		return ToneMapACES, nil
	case "clip":
		return ToneMapClip, nil
	}
	return 0, errs.Newf(errs.KindInternal, "exec: unknown tone-mapping operator %q", s)
}

// Tone-mapping constants.
const (
	// hdrPercentile is the luminance percentile (nearest rank) taken as the
	// white point: the brightest 1% (speculars, the sun) clips.
	hdrPercentile = 99
	// hdrProbeSide bounds the long side of the box-filtered copy the
	// percentile is measured on.
	hdrProbeSide = 256
	acesExposure = 0.6
	// acesWhite solves f(x) = 1 for the fitted ACES curve:
	// 0.08x² − 0.56x − 0.14 = 0.
	acesWhite = 7.241665
)

// hdrMode is what the colour step does with an HDR source.
type hdrMode uint8

const (
	hdrToneMap   hdrMode = iota // map to 8-bit sRGB with the configured operator
	hdrClip                     // preserve_hdr cannot be honoured: clip to 8-bit sRGB
	hdrKeepFloat                // preserve_hdr to JPEG XL: linear float, values above 1 kept
	hdrKeep16                   // preserve_hdr to AVIF: 16-bit sRGB (12-bit AVIF), clipped at 1
)

// hdrPending reports whether img is the root pipeline's scene-linear source
// that the colour step still has to bring to the working space.
func (r *runner) hdrPending() bool {
	if r.hdrDone {
		return false
	}
	switch r.j.Meta.Format {
	case "exr", "radiance", "pfm":
		return true
	}
	return r.j.Meta.HDR // DDS BC6H and float textures
}

// fromHDR converts a scene-linear source to the working space: 8-bit sRGB
// (8-bit grey for a grey PFM) through the configured tone-mapping operator,
// or, with preserve_hdr and an output that holds more than 8 bits, linear
// float for JPEG XL or 16-bit sRGB for AVIF. Alpha is kept (clamped to
// [0, 1]) unless the file has none: libvips gives every OpenEXR image an
// alpha band of ones.
func (r *runner) fromHDR(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	r.hdrDone = true
	colour, alpha, grey, err := r.hdrSplit(ctx, img)
	if err != nil {
		return nil, err
	}
	mode := r.hdrMode()
	switch mode {
	case hdrKeepFloat:
		if colour, err = r.a.Add(colour.Clamp(ctx, 0, math.MaxFloat32)); err != nil {
			return nil, err
		}
		return r.hdrJoin(ctx, colour, alpha, vips.InterpretationScRGB)
	case hdrKeep16:
		if colour, err = r.a.Add(colour.Clamp(ctx, 0, 1)); err != nil {
			return nil, err
		}
		return r.hdrJoin(ctx, colour, alpha, vips.InterpretationRGB16)
	default:
	}
	op := r.e.cfg.ToneMap
	if mode == hdrClip {
		op = ToneMapClip
	}
	if colour, err = r.a.Add(colour.Clamp(ctx, 0, math.MaxFloat32)); err != nil {
		return nil, err
	}
	white := 1.0
	if op != ToneMapClip {
		if white, err = r.hdrWhite(ctx, colour); err != nil {
			return nil, err
		}
	}
	if colour, err = r.toneCurve(ctx, colour, op, white); err != nil {
		return nil, err
	}
	if colour, err = r.a.Add(colour.Clamp(ctx, 0, 1)); err != nil {
		return nil, err
	}
	out, err := r.hdrJoin(ctx, colour, alpha, vips.InterpretationSRGB)
	if err != nil || !grey {
		return out, err
	}
	return r.a.Add(out.Colourspace(ctx, vips.InterpretationBW))
}

// hdrSplit decodes img to float linear RGB (Radiance RGBE, XYZ and grey
// sources included) and splits off its alpha band; grey reports a one-band
// source, whose three colour bands are equal.
func (r *runner) hdrSplit(ctx context.Context, img *vips.Image) (colour, alpha *vips.Image, grey bool, err error) {
	if img, err = r.a.Add(img.Decode(ctx)); err != nil {
		return nil, nil, false, err
	}
	if img.Interpretation() == vips.InterpretationXYZ {
		if img, err = r.a.Add(img.Colourspace(ctx, vips.InterpretationScRGB)); err != nil {
			return nil, nil, false, err
		}
	}
	if img.BandFormat() != vips.BandFormatFloat {
		if img, err = r.a.Add(img.Cast(ctx, vips.BandFormatFloat)); err != nil {
			return nil, nil, false, err
		}
	}
	n := img.Bands()
	grey = n < 3
	nc := 3
	if grey {
		nc = 1
	}
	colour = img
	if n > nc {
		// The root plan knows whether the file has alpha; a watermark
		// (no plan) keeps whatever the loader gives.
		if r.plan == nil || r.meta.HasAlpha {
			if alpha, err = r.a.Add(img.ExtractBand(ctx, nc, 1)); err != nil {
				return nil, nil, false, err
			}
			if alpha, err = r.a.Add(alpha.Clamp(ctx, 0, 1)); err != nil {
				return nil, nil, false, err
			}
		}
		if colour, err = r.a.Add(img.ExtractBand(ctx, 0, nc)); err != nil {
			return nil, nil, false, err
		}
	}
	if grey {
		if colour, err = r.a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, colour, colour})); err != nil {
			return nil, nil, false, err
		}
	}
	return colour, alpha, grey, nil
}

// hdrJoin joins linear colour in [0, ∞) and alpha in [0, 1] as scRGB and
// converts to target (sRGB, RGB16 or scRGB itself); libvips rescales the
// alpha band to the target's range.
func (r *runner) hdrJoin(ctx context.Context, colour, alpha *vips.Image, target vips.Interpretation) (*vips.Image, error) {
	img := colour
	var err error
	if alpha != nil {
		if img, err = r.a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, alpha})); err != nil {
			return nil, err
		}
	}
	if img, err = r.a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationScRGB})); err != nil {
		return nil, err
	}
	if target == vips.InterpretationScRGB {
		return img, nil
	}
	return r.a.Add(img.Colourspace(ctx, target))
}

// hdrMode decides between tone mapping and preserve_hdr. The high bit
// depth survives only into JPEG XL (float) and AVIF (12-bit), and only
// through steps that are exact on float or 16-bit pixels (crop, resize,
// rotate, flip); otherwise preserve_hdr is reported as skipped and the
// source is clipped at 1 instead of tone-mapped.
func (r *runner) hdrMode() hdrMode {
	if r.req == nil || r.req.Output.PreserveHDR == nil || !*r.req.Output.PreserveHDR {
		return hdrToneMap
	}
	var mode hdrMode
	switch r.j.Target.Format {
	case transform.FormatJXL:
		mode = hdrKeepFloat
	case transform.FormatAVIF:
		mode = hdrKeep16
	default:
		r.skip("preserve_hdr")
		return hdrClip
	}
	if !r.preservable() {
		r.skip("preserve_hdr")
		return hdrClip
	}
	return mode
}

// preservable reports whether every step of the request keeps float and
// 16-bit pixels exact: geometry only, one pipeline, no colour profile.
func (r *runner) preservable() bool {
	p := r.plan
	if p == nil || len(p.Chained) > 0 || r.req.Output.ColorProfile != "" {
		return false
	}
	for _, s := range p.Steps {
		switch s.(type) {
		case plan.ColourStep, plan.PreCropStep, plan.CropStep, plan.ResizeStep, plan.RotateStep, plan.FlipStep:
		default:
			return false
		}
	}
	for _, s := range p.After {
		if _, ok := s.(plan.ColourStep); !ok {
			return false
		}
	}
	return true
}

// hdrWhite returns the white point: the nearest-rank 99th percentile of the
// luminance of colour (linear RGB ≥ 0), measured on a box-filtered copy at
// most hdrProbeSide pixels on its long side, and at least 1.
func (r *runner) hdrWhite(ctx context.Context, colour *vips.Image) (float64, error) {
	img := colour
	var err error
	if f := (max(img.Width(), img.Height()) + hdrProbeSide - 1) / hdrProbeSide; f > 1 {
		if img, err = r.a.Add(img.Shrink(ctx, f, f)); err != nil {
			return 0, err
		}
	}
	// Rec. 709 luminance of linear sRGB.
	if img, err = r.a.Add(img.Recomb(ctx, []float64{0.2126, 0.7152, 0.0722}, 1)); err != nil {
		return 0, err
	}
	raw, err := img.WriteToMemory(ctx)
	if err != nil {
		return 0, err
	}
	return whitePoint(raw), nil
}

// whitePoint is the nearest-rank hdrPercentile of the native-endian float32
// samples in raw, at least 1 (NaN counts as 0).
func whitePoint(raw []byte) float64 {
	n := len(raw) / 4
	if n == 0 {
		return 1
	}
	ys := make([]float32, n)
	for i := range ys {
		v := math.Float32frombits(binary.NativeEndian.Uint32(raw[4*i:]))
		if math.IsNaN(float64(v)) {
			v = 0
		}
		ys[i] = v
	}
	slices.Sort(ys)
	rank := (hdrPercentile*n + 99) / 100 // ⌈p·n/100⌉
	return max(float64(ys[max(rank, 1)-1]), 1)
}

// toneCurve applies op to linear colour x (≥ 0) with white point white;
// the result is not clipped. Both curves are rational,
// x·(a·x + b) / (x·(c·x + d) + e): Reinhard with a = 1/W², b = d = e = 1,
// c = 0; ACES with 2.51, 0.03, 2.43, 0.59, 0.14 after the exposure.
func (r *runner) toneCurve(ctx context.Context, x *vips.Image, op ToneMap, white float64) (*vips.Image, error) {
	var err error
	switch op {
	case ToneMapReinhard:
		if white <= 1 {
			return x, nil // x·(1 + x)/(1 + x)
		}
		return r.rational(ctx, x, 1/(white*white), 1, 0, 1, 1)
	case ToneMapACES:
		e := min(acesExposure, acesWhite/white)
		if x, err = r.a.Add(x.Linear(ctx, []float64{e}, []float64{0})); err != nil {
			return nil, err
		}
		return r.rational(ctx, x, 2.51, 0.03, 2.43, 0.59, 0.14)
	default:
		return x, nil
	}
}

// rational computes x·(a·x + b) / (x·(c·x + d) + e) per band.
func (r *runner) rational(ctx context.Context, x *vips.Image, a, b, c, d, e float64) (*vips.Image, error) {
	lin := func(img *vips.Image, k, off float64) (*vips.Image, error) {
		return r.a.Add(img.Linear(ctx, []float64{k}, []float64{off}))
	}
	t, err := lin(x, a, b)
	if err != nil {
		return nil, err
	}
	num, err := r.a.Add(x.Multiply(ctx, t))
	if err != nil {
		return nil, err
	}
	if t, err = lin(x, c, d); err != nil {
		return nil, err
	}
	den, err := r.a.Add(x.Multiply(ctx, t))
	if err != nil {
		return nil, err
	}
	if den, err = lin(den, 1, e); err != nil {
		return nil, err
	}
	return r.a.Add(num.Divide(ctx, den))
}
