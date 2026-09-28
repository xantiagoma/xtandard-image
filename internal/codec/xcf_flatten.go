package codec

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// GimpLayerMode values (GIMP app/operations/operations-enums.h): the
// legacy modes of GIMP 2.8 and older, and the modes GIMP 2.10 writes.
const (
	xcfModeNormalLegacy    = 0
	xcfModeMultiplyLegacy  = 3
	xcfModeScreenLegacy    = 4
	xcfModeOverlayLegacy   = 5 // behaves like soft light
	xcfModeDiffLegacy      = 6
	xcfModeAddLegacy       = 7
	xcfModeDarkenLegacy    = 9
	xcfModeLightenLegacy   = 10
	xcfModeDodgeLegacy     = 16
	xcfModeBurnLegacy      = 17
	xcfModeHardLegacy      = 18
	xcfModeSoftLegacy      = 19
	xcfModeOverlay         = 23
	xcfModeNormal          = 28
	xcfModeMultiply        = 30
	xcfModeScreen          = 31
	xcfModeDifference      = 32
	xcfModeAddition        = 33
	xcfModeDarken          = 35
	xcfModeLighten         = 36
	xcfModeDodge           = 42
	xcfModeBurn            = 43
	xcfModeHardLight       = 44
	xcfModeSoftLight       = 45
	xcfModeExclusion       = 52
	xcfModePassThroughMode = 61
)

// xcfBlend maps a GIMP layer mode to the closest libvips blend mode. The
// modes libvips lacks (dissolve, hue/saturation/colour/value, divide,
// grain extract/merge, subtract, vivid/pin/linear light, ...) composite as
// normal. libvips blends in sRGB; GIMP 2.10's modes blend in linear light,
// so non-normal modes are an approximation.
func xcfBlend(mode uint32) vips.BlendMode {
	switch mode {
	case xcfModeMultiplyLegacy, xcfModeMultiply:
		return vips.BlendMultiply
	case xcfModeScreenLegacy, xcfModeScreen:
		return vips.BlendScreen
	case xcfModeOverlay:
		return vips.BlendOverlay
	case xcfModeOverlayLegacy, xcfModeSoftLegacy, xcfModeSoftLight:
		return vips.BlendSoftLight
	case xcfModeDiffLegacy, xcfModeDifference:
		return vips.BlendDifference
	case xcfModeAddLegacy, xcfModeAddition:
		return vips.BlendAdd
	case xcfModeDarkenLegacy, xcfModeDarken:
		return vips.BlendDarken
	case xcfModeLightenLegacy, xcfModeLighten:
		return vips.BlendLighten
	case xcfModeDodgeLegacy, xcfModeDodge:
		return vips.BlendColourDodge
	case xcfModeBurnLegacy, xcfModeBurn:
		return vips.BlendColourBurn
	case xcfModeHardLegacy, xcfModeHardLight:
		return vips.BlendHardLight
	case xcfModeExclusion:
		return vips.BlendExclusion
	default: // normal, pass-through groups and the rest
		return vips.BlendOver
	}
}

// FlattenXCF composites the visible top-level layers of an XCF file onto
// a transparent canvas, bottom first, the way GIMP's "Flatten Image" does
// before it fills the background: each layer at its offset, with its
// opacity and (approximately, see xcfBlend) its blend mode. Layer masks
// are not applied. ImageMagick decodes one layer per call from a copy of
// the file whose layer table lists only that layer (XCFLayerFile), so the
// work is linear in the layers. Every composited layer and the canvas must
// fit maxPixels (errs.KindLimitExceeded; zero or less is no limit). The result is 8-bit sRGB with
// alpha, in memory, tracked by a. It must run inside vips.Run.
func FlattenXCF(ctx context.Context, a *vips.Arena, buf *vips.Buffer, maxPixels int64, failOn vips.FailOn) (*vips.Image, error) {
	data := buf.Bytes()
	h, err := ParseXCFHeader(data)
	if err != nil {
		return nil, err
	}
	if maxPixels <= 0 {
		maxPixels = 1 << 50 // unlimited
	}
	if int64(h.Width)*int64(h.Height) > maxPixels {
		return nil, errs.Newf(errs.KindLimitExceeded, "codec: xcf canvas of %dx%d exceeds %d pixels", h.Width, h.Height, maxPixels)
	}
	layers, index := h.Composited()
	for i := range layers {
		if int64(layers[i].Width)*int64(layers[i].Height) > maxPixels {
			return nil, errs.Newf(errs.KindLimitExceeded, "codec: xcf layer of %dx%d exceeds %d pixels",
				layers[i].Width, layers[i].Height, maxPixels)
		}
	}
	canvas, err := xcfCanvas(ctx, h.Width, h.Height)
	if err != nil {
		return nil, err
	}
	// canvas is the only composite kept: each step replaces (and closes)
	// the previous one; the last is handed to a.
	defer func() { canvas.Close() }()
	if len(layers) > 0 {
		scratch, err := vips.NewBuffer(len(data))
		if err != nil {
			return nil, err
		}
		defer scratch.Release()
		for k := range layers {
			if err := ctxErr(ctx); err != nil {
				return nil, err
			}
			next, err := flattenLayer(ctx, canvas, scratch, data, &h, index[k], failOn)
			if err != nil {
				return nil, err
			}
			canvas.Close()
			canvas = next
		}
	}
	out := a.Track(canvas)
	canvas = nil
	return out, nil
}

// xcfCanvas is a transparent sRGB canvas, in memory.
func xcfCanvas(ctx context.Context, w, h int) (*vips.Image, error) {
	var la vips.Arena
	defer la.Close()
	img, err := la.Add(vips.Black(ctx, w, h, 4))
	if err != nil {
		return nil, err
	}
	if img, err = la.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationSRGB})); err != nil {
		return nil, err
	}
	return img.CopyMemory(ctx)
}

// flattenLayer decodes layer i from its own copy of the file and composites
// it onto canvas. The layer and the result are materialised, so the scratch
// buffer can be rewritten for the next layer and at most two canvases and
// one layer are in memory at a time. The caller owns the result.
func flattenLayer(ctx context.Context, canvas *vips.Image, scratch *vips.Buffer, data []byte, h *XCFHeader, i int, failOn vips.FailOn) (*vips.Image, error) {
	l := &h.Layers[i]
	if err := XCFLayerFile(scratch.Bytes(), data, h, i); err != nil {
		return nil, err
	}
	var la vips.Arena // this layer's intermediates
	defer la.Close()
	img, err := la.Add(vips.Load(ctx, scratch, &vips.LoadOptions{Access: vips.AccessRandom, FailOn: failOn, NPages: 1}))
	if err != nil {
		return nil, errs.Wrapf(errs.KindCorrupt, err, "codec: xcf layer %d", i)
	}
	if img.Width() != l.Width || img.Height() != l.Height {
		return nil, xcfCorrupt("layer size disagrees with its pixels")
	}
	if img, err = la.Add(img.CopyMemory(ctx)); err != nil {
		return nil, err
	}
	if !img.HasAlpha() {
		if img, err = la.Add(img.AddAlpha(ctx)); err != nil {
			return nil, err
		}
	} else if l.Alpha && l.Opacity < 255 {
		// The coder bakes the opacity into layers without an alpha channel
		// only.
		scale := make([]float64, img.Bands())
		zero := make([]float64, img.Bands())
		for b := range scale {
			scale[b] = 1
		}
		scale[len(scale)-1] = float64(l.Opacity) / 255
		if img, err = la.Add(img.Linear(ctx, scale, zero)); err != nil {
			return nil, err
		}
		if img, err = la.Add(img.CastRound(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	out, err := la.Add(canvas.Composite2(ctx, img, xcfBlend(l.Mode), l.X, l.Y))
	if err != nil {
		return nil, err
	}
	if out.BandFormat() != vips.BandFormatUchar {
		if out, err = la.Add(out.CastRound(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	return out.CopyMemory(ctx)
}
