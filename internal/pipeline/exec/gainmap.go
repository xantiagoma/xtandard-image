package exec

import (
	"context"
	"errors"

	"github.com/xantiagoma/xtandard-image/internal/codec/gainmap"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// HDR gain maps (WP-104, FORMATS.md §1.4). A source with a gain map
// (UltraHDR JPEG, an ISO 21496-1 or Adobe gain-map JPEG, an AVIF or HEIC
// with a tmap item) decodes to its SDR base. When the request wants HDR
// (preserve_hdr with gain_map:auto, or gain_map:hdr) and the output holds
// it, the colour step applies the gain map at full weight and hands the
// linear result to fromHDR as a scene-linear source: JPEG XL keeps float,
// AVIF is written as BT.2100 PQ and JPEG as UltraHDR with a new gain map
// (vips.Image.Save). Otherwise the SDR base goes through as before.

// gainMapWanted reports whether the colour step applies the source's gain
// map: the request asks for HDR, the output is AVIF, JPEG XL or JPEG
// (without alpha, which JPEG would flatten), the loaded image is the
// source's first page at its own size, and only steps that keep float
// pixels exact follow (preservable).
func (r *runner) gainMapWanted() bool {
	if r.gainDone || !r.gainLoadable || !r.j.Meta.GainMap || r.req == nil || r.plan == nil {
		return false
	}
	if !r.wantsHDR() {
		return false
	}
	switch r.j.Target.Format {
	case transform.FormatJXL, transform.FormatAVIF:
	case transform.FormatJPEG:
		if r.meta.HasAlpha {
			return false
		}
	default:
		return false
	}
	return r.preservable()
}

// wantsHDR reports whether the request asks for HDR output: preserve_hdr,
// or gain_map:hdr for a gain-map source. gain_map:sdr keeps a gain-map
// source's SDR base even with preserve_hdr.
func (r *runner) wantsHDR() bool {
	if r.req == nil {
		return false
	}
	preserve := r.req.Output.PreserveHDR != nil && *r.req.Output.PreserveHDR
	if !r.j.Meta.GainMap || r.j.Meta.HDR {
		return preserve
	}
	switch r.req.Output.GainMap {
	case transform.GainMapHDR:
		return true
	case transform.GainMapSDR:
		return false
	default:
		return preserve
	}
}

// applyGainMap turns img, the source's SDR base, into linear scRGB float
// (1.0 = SDR white) with the gain map applied at full weight; alpha, if
// any, follows as a fourth band in [0, 1]. ok is false when the gain map
// turns out to be unusable (unsupported or malformed metadata, a base that
// is already the HDR rendition): the SDR base is then processed as usual.
func (r *runner) applyGainMap(ctx context.Context, img *vips.Image) (out *vips.Image, ok bool, err error) {
	r.gainDone = true
	gm, meta, err := r.loadGainMap(ctx, img)
	switch {
	case errors.Is(err, gainmap.ErrUnsupported), errors.Is(err, gainmap.ErrMalformed):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	case gm == nil || meta.BaseIsHDR():
		return nil, false, nil
	}

	colour, alpha := img, (*vips.Image)(nil)
	nc := 3
	if img.Bands() < 3 {
		nc = 1
	}
	if img.Bands() > nc {
		if alpha, err = r.a.Add(img.ExtractBand(ctx, nc, 1)); err != nil {
			return nil, false, err
		}
		if colour, err = r.a.Add(img.ExtractBand(ctx, 0, nc)); err != nil {
			return nil, false, err
		}
	}
	lin, err := r.linearBase(ctx, colour)
	if err != nil {
		return nil, false, err
	}
	gain, err := r.gainImage(ctx, gm, &meta, lin.Width(), lin.Height())
	if err != nil {
		return nil, false, err
	}
	// (base + k_base) · gain − k_alt, clamped at 0.
	if lin, err = r.a.Add(lin.Linear(ctx, []float64{1, 1, 1}, meta.BaseOffset[:])); err != nil {
		return nil, false, err
	}
	hdr, err := r.a.Add(lin.Multiply(ctx, gain))
	if err != nil {
		return nil, false, err
	}
	neg := meta.AlternateOffset
	for c := range neg {
		neg[c] = -neg[c]
	}
	if hdr, err = r.a.Add(hdr.Linear(ctx, []float64{1, 1, 1}, neg[:])); err != nil {
		return nil, false, err
	}
	if hdr, err = r.a.Add(hdr.Cast(ctx, vips.BandFormatFloat)); err != nil {
		return nil, false, err
	}
	if alpha != nil {
		// Alpha on the float scale fromHDR expects.
		top := 255.0
		if alpha.BandFormat() == vips.BandFormatUshort {
			top = 65535
		}
		if alpha, err = r.a.Add(alpha.Linear(ctx, []float64{1 / top}, []float64{0})); err != nil {
			return nil, false, err
		}
		if alpha, err = r.a.Add(alpha.Cast(ctx, vips.BandFormatFloat)); err != nil {
			return nil, false, err
		}
		if hdr, err = r.a.Add(vips.Bandjoin(ctx, []*vips.Image{hdr, alpha})); err != nil {
			return nil, false, err
		}
	}
	out, err = r.a.Add(hdr.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationScRGB}))
	if err != nil {
		return nil, false, err
	}
	// The profile described the SDR base; the pixels are linear sRGB now.
	if _, has := out.Blob(vips.FieldICC); has {
		if out, err = r.a.Add(out.RemoveMetadata(ctx, vips.FieldICC)); err != nil {
			return nil, false, err
		}
	}
	return out, true, nil
}

// linearBase converts the SDR base (grey or RGB, 8 or 16 bits, possibly
// with an ICC profile) to linear sRGB float, three bands. An embedded
// profile other than sRGB is applied first (to 16-bit sRGB), so a Display
// P3 base keeps its colours inside sRGB.
func (r *runner) linearBase(ctx context.Context, img *vips.Image) (*vips.Image, error) {
	var err error
	if icc, ok := img.Blob(vips.FieldICC); ok && importable(img, img.Interpretation(), icc) {
		if img, err = r.a.Add(img.ICCTransform(ctx, "srgb", vips.IntentRelative, true, 16)); err != nil {
			return nil, err
		}
	}
	if img.Bands() == 1 {
		if img, err = r.a.Add(vips.Bandjoin(ctx, []*vips.Image{img, img, img})); err != nil {
			return nil, err
		}
		interp := vips.InterpretationSRGB
		if img.BandFormat() == vips.BandFormatUshort {
			interp = vips.InterpretationRGB16
		}
		if img, err = r.a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: interp})); err != nil {
			return nil, err
		}
	}
	return r.a.Add(img.Colourspace(ctx, vips.InterpretationScRGB))
}

// gainImage turns the gain map's code values into the linear gain per
// channel, 2^(g^(1/γ)·(max − min) + min), at w × h (bilinear, as libultrahdr
// and libvips upsample gain maps).
func (r *runner) gainImage(ctx context.Context, gm *vips.Image, m *gainmap.Metadata, w, h int) (*vips.Image, error) {
	var err error
	if m.Channels == 1 && gm.Bands() > 1 {
		if gm, err = r.a.Add(gm.ExtractBand(ctx, 0, 1)); err != nil {
			return nil, err
		}
	}
	if gm.Bands() == 2 || gm.Bands() == 4 { // a grey or RGB gain map with alpha
		if gm, err = r.a.Add(gm.ExtractBand(ctx, 0, gm.Bands()-1)); err != nil {
			return nil, err
		}
	}
	if gm.Width() != w || gm.Height() != h {
		if gm, err = r.a.Add(gm.Resize(ctx, float64(w)/float64(gm.Width()), float64(h)/float64(gm.Height()),
			vips.KernelLinear)); err != nil {
			return nil, err
		}
		// Resize rounds; the gain must cover the base exactly.
		if gm.Width() != w || gm.Height() != h {
			if gm, err = r.a.Add(gm.Embed(ctx, 0, 0, w, h, vips.ExtendCopy, nil)); err != nil {
				return nil, err
			}
		}
	}
	top := 255.0
	if gm.BandFormat() == vips.BandFormatUshort {
		top = 65535
	}
	g, err := r.a.Add(gm.Cast(ctx, vips.BandFormatFloat))
	if err != nil {
		return nil, err
	}
	if g, err = r.a.Add(g.Linear(ctx, []float64{1 / top}, []float64{0})); err != nil {
		return nil, err
	}
	if g, err = r.a.Add(g.Clamp(ctx, 0, 1)); err != nil {
		return nil, err
	}
	invGamma, scale, offset := m.Coefficients(1)
	if invGamma != [3]float64{1, 1, 1} {
		if g, err = r.a.Add(g.Pow(ctx, invGamma[:])); err != nil {
			return nil, err
		}
	}
	if g, err = r.a.Add(g.Linear(ctx, scale[:], offset[:])); err != nil {
		return nil, err
	}
	return r.a.Add(g.Exp2(ctx))
}

// loadGainMap decodes the gain map of the source and reads its metadata:
// the one uhdrload attached to img (UltraHDR), else the MPF secondary image
// of a JPEG, else the tmap item of an AVIF/HEIC. gm is nil when the
// source has none after all. Metadata errors wrap gainmap.ErrUnsupported
// or gainmap.ErrMalformed.
func (r *runner) loadGainMap(ctx context.Context, img *vips.Image) (*vips.Image, gainmap.Metadata, error) {
	switch r.j.Meta.Format {
	case "jpeg":
		gm, u, ok, err := img.UHDRGainMap(ctx)
		if err != nil {
			return nil, gainmap.Metadata{}, err
		}
		if ok {
			if gm, err = r.a.Add(gm, nil); err != nil {
				return nil, gainmap.Metadata{}, err
			}
			m, err := gainmap.FromUHDR(u.MinContentBoost, u.MaxContentBoost, u.Gamma, u.OffsetSDR, u.OffsetHDR,
				u.CapacityMin, u.CapacityMax)
			return gm, m, err
		}
		j, ok, err := gainmap.FindJPEG(r.j.Source.Bytes())
		if err != nil || !ok {
			return nil, gainmap.Metadata{}, err
		}
		buf, err := vips.NewBuffer(j.Length)
		if err != nil {
			return nil, gainmap.Metadata{}, err
		}
		defer buf.Release() // the image keeps its own reference
		copy(buf.Bytes(), r.j.Source.Bytes()[j.Offset:j.Offset+j.Length])
		gm, err = r.a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential, FailOn: r.e.cfg.FailOn}))
		if err != nil {
			return nil, gainmap.Metadata{}, errs.Wrap(errs.KindCorrupt, err)
		}
		return gm, j.Meta, nil
	case "avif", "heic", "heif":
		gm, payload, err := vips.LoadHEIFGainMap(ctx, r.j.Source)
		if err != nil || gm == nil {
			return nil, gainmap.Metadata{}, err
		}
		if gm, err = r.a.Add(gm, nil); err != nil {
			return nil, gainmap.Metadata{}, err
		}
		m, err := gainmap.ParseTmap(payload)
		return gm, m, err
	default:
		return nil, gainmap.Metadata{}, nil
	}
}
