package dicom

import (
	"encoding/binary"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Samples is a decoded frame: stored sample values, Bands (1 or 3) per
// pixel, interleaved. Decoded reports that the codec already converted
// YCbCr to RGB (JPEG, JPEG 2000 with a colour transform), so colour
// samples are RGB whatever the photometric interpretation says.
type Samples struct {
	Width, Height, Bands int
	Data                 []int32
	Decoded              bool
}

// Render maps a decoded frame (from an image codec, FirstFrame) to 8-bit
// RGB like Decode does for native frames: modality rescale, VOI window (or
// the frame's minimum and maximum), MONOCHROME1 inversion, palette lookup,
// YBR to RGB.
func (h *Header) Render(s Samples) (*raster.Raw, error) {
	if s.Width <= 0 || s.Height <= 0 || (s.Bands != 1 && s.Bands != 3) ||
		int64(len(s.Data)) != int64(s.Width)*int64(s.Height)*int64(s.Bands) {
		return nil, errs.Newf(errs.KindInternal, "dicom: render %dx%dx%d from %d samples",
			s.Width, s.Height, s.Bands, len(s.Data))
	}
	if s.Bands != h.Samples {
		return nil, corruptf("frame has %d samples per pixel, header %d", s.Bands, h.Samples)
	}
	hh := *h
	if s.Decoded && s.Bands == 3 {
		hh.Photometric = "RGB"
	}
	return hh.render(sampler{w: s.Width, h: s.Height, bands: s.Bands, at: func(i, b int) int32 {
		return s.Data[i*s.Bands+b]
	}})
}

// render converts a frame to 8-bit RGB.
func (h *Header) render(s sampler) (*raster.Raw, error) {
	pixels := s.w * s.h
	out := &raster.Raw{Width: s.w, Height: s.h, Bands: 3, Data: make([]byte, pixels*3)}
	switch {
	case h.Photometric == "PALETTE COLOR":
		return out, h.palette.apply(s, out.Data)
	case s.bands == 1:
		h.renderGrey(s, out.Data)
	default:
		if err := h.renderColour(s, out.Data); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// renderGrey applies the modality rescale and the VOI transform.
func (h *Header) renderGrey(s sampler, dst []byte) {
	pixels := s.w * s.h
	lo, hi := int32(math.MaxInt32), int32(math.MinInt32)
	for i := range pixels {
		v := s.at(i, 0)
		lo, hi = min(lo, v), max(hi, v)
	}
	identity := !h.HasWindow && h.BitsStored <= 8 && !h.Signed && h.RescaleSlope == 1 && h.RescaleIntercept == 0
	mlo := float64(lo)*h.RescaleSlope + h.RescaleIntercept
	mhi := float64(hi)*h.RescaleSlope + h.RescaleIntercept
	if mlo > mhi {
		mlo, mhi = mhi, mlo
	}
	voi := func(x float64) float64 {
		switch {
		case identity:
			return x
		case !h.HasWindow:
			if mhi == mlo {
				return 0
			}
			return (x - mlo) / (mhi - mlo) * 255
		}
		c, w := h.WindowCenter, h.WindowWidth
		switch h.VOILUTFunction {
		case "LINEAR_EXACT":
			return ((x-c)/w + 0.5) * 255
		case "SIGMOID":
			return 255 / (1 + math.Exp(-4*(x-c)/w))
		default: // LINEAR, PS3.3 C.11.2.1.2.1
			if w <= 1 {
				if x > c-0.5 {
					return 255
				}
				return 0
			}
			return ((x-(c-0.5))/(w-1) + 0.5) * 255
		}
	}
	invert := h.Photometric == "MONOCHROME1"
	level := func(v int32) byte {
		y := voi(float64(v)*h.RescaleSlope + h.RescaleIntercept)
		b := byte(math.Round(min(max(y, 0), 255)))
		if invert {
			b = 255 - b
		}
		return b
	}
	// A lookup table over the frame's range when it is small.
	var lut []byte
	if span := int64(hi) - int64(lo); span < 1<<17 {
		lut = make([]byte, span+1)
		for i := range lut {
			lut[i] = level(lo + int32(i))
		}
	}
	for i := range pixels {
		v := s.at(i, 0)
		var b byte
		if lut != nil {
			b = lut[v-lo]
		} else {
			b = level(v)
		}
		dst[3*i], dst[3*i+1], dst[3*i+2] = b, b, b
	}
}

// renderColour scales RGB samples to 8 bits and converts YBR_FULL(_422).
func (h *Header) renderColour(s sampler, dst []byte) error {
	maxv := float64(uint32(1)<<uint(h.BitsStored) - 1)
	scale := func(v int32) float64 { return float64(v) * 255 / maxv }
	ybr := false
	switch h.Photometric {
	case "RGB":
	case "YBR_FULL", "YBR_FULL_422":
		ybr = true
	default:
		return unsupported("photometric interpretation "+h.Photometric+" needs a codec",
			"this DICOM photometric interpretation is not supported")
	}
	for i := range s.w * s.h {
		r, g, b := scale(s.at(i, 0)), scale(s.at(i, 1)), scale(s.at(i, 2))
		if ybr {
			y, cb, cr := r, g-128, b-128
			r, g, b = y+1.402*cr, y-0.344136*cb-0.714136*cr, y+1.772*cb
		}
		dst[3*i] = clamp8(r)
		dst[3*i+1] = clamp8(g)
		dst[3*i+2] = clamp8(b)
	}
	return nil
}

func clamp8(v float64) byte { return byte(math.Round(min(max(v, 0), 255))) }

// palette is the PALETTE COLOR lookup tables: descriptors (entries,
// first mapped value, bits per entry) and data per channel.
type palette struct {
	desc    [3][3]uint16
	hasDesc [3]bool
	data    [3][]byte
	order   binary.ByteOrder
}

// entries is the table length of a descriptor (0 means 65536).
func entries(d [3]uint16) int {
	if d[0] == 0 {
		return 1 << 16
	}
	return int(d[0])
}

// check reports whether every table is described and long enough.
func (p *palette) check() error {
	for c := range 3 {
		d := p.desc[c]
		if !p.hasDesc[c] || (d[2] != 8 && d[2] != 16) {
			return corrupt("bad palette descriptor")
		}
		n := entries(d)
		need := n
		if d[2] == 16 {
			need = 2 * n
		}
		if len(p.data[c]) < need {
			return corrupt("short palette data")
		}
	}
	return nil
}

// entry is table c's 8-bit value for index i (clamped to the table).
func (p *palette) entry(c, v int) byte {
	d := p.desc[c]
	n := entries(d)
	i := min(max(v-int(d[1]), 0), n-1)
	data := p.data[c]
	if d[2] == 16 {
		return byte(p.order.Uint16(data[2*i:]) >> 8)
	}
	if len(data) >= 2*n {
		// 8-bit entries stored one per 16-bit word.
		return byte(p.order.Uint16(data[2*i:])) //nolint:gosec // G115: an 8-bit entry in a 16-bit word
	}
	return data[i]
}

func (p *palette) apply(s sampler, dst []byte) error {
	if err := p.check(); err != nil {
		return err
	}
	for i := range s.w * s.h {
		v := int(s.at(i, 0))
		for c := range 3 {
			dst[3*i+c] = p.entry(c, v)
		}
	}
	return nil
}
