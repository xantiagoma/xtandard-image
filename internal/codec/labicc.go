package codec

import (
	"encoding/binary"
	"math"
)

// labD50Profile builds a minimal ICC v2 abstract profile for L*a*b* data
// relative to D50, the shape of lcms's cmsCreateLab2Profile(NULL): the
// A2B0 and B2A0 transforms are identity lut16 tables (2-point grids), with
// the D50 media white point and a description. As the input profile of
// vips_icc_transform (lcms), it maps TIFF CIELab, which is relative to D50,
// to the PCS and on to the output profile.
func labD50Profile() []byte {
	be := binary.BigEndian
	s15 := func(b []byte, v float64) []byte {
		return be.AppendUint32(b, uint32(int32(math.Round(v*65536)))) //nolint:gosec // G115: s15Fixed16 two's complement
	}
	const d50X, d50Y, d50Z = 0.9642, 1.0, 0.8249
	wtpt := s15(s15(s15(append([]byte("XYZ "), 0, 0, 0, 0), d50X), d50Y), d50Z)
	text := "L*a*b* D50 identity"
	desc := append([]byte("desc"), 0, 0, 0, 0)
	desc = be.AppendUint32(desc, uint32(len(text)+1)) //nolint:gosec // G115: a short constant
	desc = append(append(desc, text...), 0)
	desc = append(desc, make([]byte, 4+4+2+1+67)...) // no Unicode or ScriptCode text
	lut := append([]byte("mft2"), 0, 0, 0, 0, 3, 3, 2, 0)
	for i := range 9 { // identity matrix (unused for L*a*b* input)
		v := 0.0
		if i%4 == 0 {
			v = 1
		}
		lut = s15(lut, v)
	}
	lut = be.AppendUint16(be.AppendUint16(lut, 2), 2) // input and output table entries
	for range 3 {
		lut = be.AppendUint16(be.AppendUint16(lut, 0), 0xFFFF)
	}
	for i := range 8 { // the 2×2×2 grid maps every corner to itself
		for _, bit := range [3]int{i >> 2 & 1, i >> 1 & 1, i & 1} {
			lut = be.AppendUint16(lut, uint16(bit*0xFFFF)) //nolint:gosec // G115: bit is 0 or 1
		}
	}
	for range 3 {
		lut = be.AppendUint16(be.AppendUint16(lut, 0), 0xFFFF)
	}
	tags := [...]struct {
		sig  string
		data []byte
	}{{"desc", desc}, {"wtpt", wtpt}, {"A2B0", lut}, {"B2A0", lut}}
	off := 128 + 4 + 12*len(tags)
	table := be.AppendUint32(nil, uint32(len(tags)))
	var data []byte
	for _, t := range tags {
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
		table = append(table, t.sig...)
		table = be.AppendUint32(table, uint32(off+len(data))) //nolint:gosec // G115: a profile under 1 KiB
		table = be.AppendUint32(table, uint32(len(t.data)))   //nolint:gosec // G115: a profile under 1 KiB
		data = append(data, t.data...)
	}
	h := make([]byte, 0, 128+len(table)+len(data))
	h = be.AppendUint32(h, uint32(128+len(table)+len(data))) //nolint:gosec // G115: a profile under 1 KiB
	h = append(h, 0, 0, 0, 0, 2, 0x10, 0, 0)                 // preferred CMM (none), version 2.1
	h = append(h, "abstLab Lab "...)                         // abstract class, data space and PCS L*a*b*
	h = append(h, 7, 234, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0)      // 2026-01-01 00:00:00
	h = append(h, "acsp"...)
	h = append(h, make([]byte, 28)...) // platform, flags, device, attributes, intent
	h = s15(s15(s15(h, d50X), d50Y), d50Z)
	h = append(h, make([]byte, 128-len(h))...)
	return append(append(h, table...), data...)
}
