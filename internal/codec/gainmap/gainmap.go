// Package gainmap reads HDR gain-map metadata (WP-104, FORMATS.md §1.4):
// the ISO 21496-1 binary form (AVIF/HEIF tmap items, JPEG APP2), Adobe's
// hdrgm XMP (JPEG, also written by Google UltraHDR) and libultrahdr's
// linear form (vips uhdrload). It also finds the gain map of a JPEG that
// libultrahdr does not recognise (an MPF secondary image with hdrgm XMP or
// ISO metadata) and reports whether an ISO BMFF file has a tmap item. Pure
// Go, no decoding: every parser is bounded and fuzzed.
//
// A gain map G (per pixel, one or three channels, code values in [0, 1])
// maps the base rendition B to the alternate rendition A in linear light:
//
//	log2 gain = lerp(Min, Max, G^(1/Gamma)) · w
//	A = (B + BaseOffset) · 2^(log2 gain) − AlternateOffset
//
// with w in [0, 1] the weight for a display headroom; w = 1 gives the full
// alternate rendition.
package gainmap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Errors. ErrUnsupported marks metadata of a version this package does not
// read; the others are malformed input. Callers fall back to the base
// image either way.
var (
	ErrUnsupported = errors.New("gainmap: unsupported metadata version")
	ErrMalformed   = errors.New("gainmap: malformed metadata")
)

// Metadata is gain-map metadata in ISO 21496-1 terms, per channel (one
// channel is repeated in all three).
type Metadata struct {
	// Channels is 1 (one gain for R, G and B) or 3.
	Channels int
	// Min and Max bound the log2 gain the map encodes.
	Min, Max [3]float64
	// Gamma is the gain map's encoding gamma (> 0).
	Gamma [3]float64
	// BaseOffset and AlternateOffset are the ε offsets added to the base
	// and subtracted from the result (linear light).
	BaseOffset, AlternateOffset [3]float64
	// BaseHeadroom and AlternateHeadroom are the log2 HDR headrooms of the
	// two renditions; the alternate is the HDR one when it is larger.
	BaseHeadroom, AlternateHeadroom float64
	// UseBaseColourSpace says the gain applies in the base image's colour
	// space (otherwise in the alternate's, which the tmap item describes).
	UseBaseColourSpace bool
}

// BaseIsHDR reports a file whose base image is the HDR rendition (the gain
// map leads to SDR): its HDR is the base itself.
func (m *Metadata) BaseIsHDR() bool { return m.BaseHeadroom > m.AlternateHeadroom }

// Validate checks the ranges the renderer relies on.
func (m *Metadata) Validate() error {
	if m.Channels != 1 && m.Channels != 3 {
		return fmt.Errorf("%w: %d channels", ErrMalformed, m.Channels)
	}
	finite := func(vs ...float64) bool {
		for _, v := range vs {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return false
			}
		}
		return true
	}
	if !finite(m.BaseHeadroom, m.AlternateHeadroom) || m.BaseHeadroom < 0 || m.AlternateHeadroom < 0 {
		return fmt.Errorf("%w: headroom", ErrMalformed)
	}
	for c := range 3 {
		if !finite(m.Min[c], m.Max[c], m.Gamma[c], m.BaseOffset[c], m.AlternateOffset[c]) {
			return fmt.Errorf("%w: channel %d is not finite", ErrMalformed, c)
		}
		if m.Gamma[c] <= 0 || m.Max[c] < m.Min[c] {
			return fmt.Errorf("%w: channel %d gamma %g, min %g, max %g", ErrMalformed, c, m.Gamma[c], m.Min[c], m.Max[c])
		}
		// A gain of more than 2^32 is not an image.
		if math.Abs(m.Min[c]) > 32 || math.Abs(m.Max[c]) > 32 || math.Abs(m.BaseOffset[c]) > 1 ||
			math.Abs(m.AlternateOffset[c]) > 1 {
			return fmt.Errorf("%w: channel %d out of range", ErrMalformed, c)
		}
	}
	return nil
}

// fill copies channel 0 to the others of a one-channel map.
func (m *Metadata) fill() {
	if m.Channels != 1 {
		return
	}
	for c := 1; c < 3; c++ {
		m.Min[c], m.Max[c], m.Gamma[c] = m.Min[0], m.Max[0], m.Gamma[0]
		m.BaseOffset[c], m.AlternateOffset[c] = m.BaseOffset[0], m.AlternateOffset[0]
	}
}

// ISO 21496-1 flags.
const (
	isoMultiChannel      = 0x80
	isoUseBaseColour     = 0x40
	isoCommonDenominator = 0x08
)

// ParseTmap parses the payload of an AVIF/HEIF tmap item: a version byte
// (0) followed by the ISO 21496-1 metadata.
func ParseTmap(b []byte) (Metadata, error) {
	if len(b) < 1 {
		return Metadata{}, fmt.Errorf("%w: empty tmap", ErrMalformed)
	}
	if b[0] != 0 {
		return Metadata{}, fmt.Errorf("%w: tmap version %d", ErrUnsupported, b[0])
	}
	return ParseISO(b[1:])
}

// ParseISO parses ISO 21496-1 gain-map metadata as stored after the tmap
// version byte and in the JPEG APP2 segment: minimum and writer versions,
// flags, then the headrooms and per-channel fractions (with or without a
// common denominator). Bytes after the metadata are ignored, as later
// writer versions may append fields.
func ParseISO(b []byte) (Metadata, error) {
	r := reader{b: b}
	minVersion := r.u16()
	r.u16() // writer version
	flags := r.u8()
	if r.bad {
		return Metadata{}, fmt.Errorf("%w: short header", ErrMalformed)
	}
	if minVersion != 0 {
		return Metadata{}, fmt.Errorf("%w: minimum version %d", ErrUnsupported, minVersion)
	}
	m := Metadata{Channels: 1, UseBaseColourSpace: flags&isoUseBaseColour != 0}
	if flags&isoMultiChannel != 0 {
		m.Channels = 3
	}
	if flags&isoCommonDenominator != 0 {
		d := r.u32()
		if d == 0 {
			return Metadata{}, fmt.Errorf("%w: zero denominator", ErrMalformed)
		}
		den := float64(d)
		m.BaseHeadroom = float64(r.u32()) / den
		m.AlternateHeadroom = float64(r.u32()) / den
		for c := range m.Channels {
			m.Min[c] = float64(r.s32()) / den
			m.Max[c] = float64(r.s32()) / den
			m.Gamma[c] = float64(r.u32()) / den
			m.BaseOffset[c] = float64(r.s32()) / den
			m.AlternateOffset[c] = float64(r.s32()) / den
		}
	} else {
		m.BaseHeadroom = r.ufrac()
		m.AlternateHeadroom = r.ufrac()
		for c := range m.Channels {
			m.Min[c] = r.sfrac()
			m.Max[c] = r.sfrac()
			m.Gamma[c] = r.ufrac()
			m.BaseOffset[c] = r.sfrac()
			m.AlternateOffset[c] = r.sfrac()
		}
	}
	if r.bad {
		return Metadata{}, fmt.Errorf("%w: truncated or zero denominator", ErrMalformed)
	}
	m.fill()
	if err := m.Validate(); err != nil {
		return Metadata{}, err
	}
	return m, nil
}

// FromUHDR converts libultrahdr's linear metadata (content boosts and HDR
// capacities as ratios, as vips uhdrload reports them) to log2 units.
func FromUHDR(minBoost, maxBoost, gamma, offsetSDR, offsetHDR [3]float64, capacityMin, capacityMax float64) (Metadata, error) {
	m := Metadata{Channels: 3, Gamma: gamma, BaseOffset: offsetSDR, AlternateOffset: offsetHDR}
	if capacityMin <= 0 || capacityMax <= 0 {
		return Metadata{}, fmt.Errorf("%w: capacity %g..%g", ErrMalformed, capacityMin, capacityMax)
	}
	m.BaseHeadroom = math.Log2(capacityMin)
	m.AlternateHeadroom = math.Log2(capacityMax)
	for c := range 3 {
		if minBoost[c] <= 0 || maxBoost[c] <= 0 {
			return Metadata{}, fmt.Errorf("%w: content boost %g..%g", ErrMalformed, minBoost[c], maxBoost[c])
		}
		m.Min[c] = math.Log2(minBoost[c])
		m.Max[c] = math.Log2(maxBoost[c])
	}
	// libultrahdr reports the base headroom as capacity min, which is 1
	// (log2 0) for an SDR base.
	m.BaseHeadroom = max(m.BaseHeadroom, 0)
	if err := m.Validate(); err != nil {
		return Metadata{}, err
	}
	return m, nil
}

// Coefficients returns, per channel, what the renderer computes with for
// weight w in [0, 1]: the gain map's code value g in [0, 1] becomes
// log2 gain = g^InvGamma · Scale + Offset.
func (m *Metadata) Coefficients(w float64) (invGamma, scale, offset [3]float64) {
	w = min(max(w, 0), 1)
	for c := range 3 {
		invGamma[c] = 1 / m.Gamma[c]
		scale[c] = (m.Max[c] - m.Min[c]) * w
		offset[c] = m.Min[c] * w
	}
	return invGamma, scale, offset
}

// reader reads big-endian fields; any short read sets bad and returns 0.
type reader struct {
	b   []byte
	bad bool
}

func (r *reader) take(n int) []byte {
	if r.bad || len(r.b) < n {
		r.bad = true
		return nil
	}
	p := r.b[:n]
	r.b = r.b[n:]
	return p
}

func (r *reader) u8() uint8 {
	if p := r.take(1); p != nil {
		return p[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	if p := r.take(2); p != nil {
		return binary.BigEndian.Uint16(p)
	}
	return 0
}

func (r *reader) u32() uint32 {
	if p := r.take(4); p != nil {
		return binary.BigEndian.Uint32(p)
	}
	return 0
}

func (r *reader) s32() int32 { return int32(r.u32()) } //nolint:gosec // two's complement by definition

// ufrac and sfrac read an unsigned or signed numerator and an unsigned
// denominator; a zero denominator is malformed.
func (r *reader) ufrac() float64 {
	n, d := r.u32(), r.u32()
	if d == 0 {
		r.bad = true
		return 0
	}
	return float64(n) / float64(d)
}

func (r *reader) sfrac() float64 {
	n, d := r.s32(), r.u32()
	if d == 0 {
		r.bad = true
		return 0
	}
	return float64(n) / float64(d)
}
