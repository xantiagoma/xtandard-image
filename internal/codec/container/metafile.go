package container

import "encoding/binary"

// Windows metafiles (WP-108) are recognised here, by content, like the
// other documents the server replaces before the probe; the conversion to
// SVG needs the gpl image's converter (internal/metafile), which the server
// runs.
//
//   - WMF: a placeable header (key 0x9AC6CDD7, a non-empty bounding box and
//     a units-per-inch value) or a bare META_HEADER (type 1 memory or 2
//     disk, header size 9 words, version 0x0100 or 0x0300);
//   - EMF: an EMR_HEADER record (type 1, size ≥ 88) with the " EMF"
//     signature at offset 40.

const (
	wmfPlaceableKey  = 0x9AC6CDD7
	wmfPlaceableSize = 22
	emfSignature     = 0x464D4520 // " EMF"
	emfHeaderMin     = 88
)

// DetectMetafile reports a WMF or EMF file.
func DetectMetafile(data []byte) (Kind, bool) {
	switch {
	case isEMF(data):
		return KindEMF, true
	case isWMF(data):
		return KindWMF, true
	}
	return KindNone, false
}

func isEMF(b []byte) bool {
	le := binary.LittleEndian
	return len(b) >= emfHeaderMin && le.Uint32(b) == 1 && le.Uint32(b[4:]) >= emfHeaderMin &&
		le.Uint32(b[40:]) == emfSignature
}

func isWMF(b []byte) bool {
	le := binary.LittleEndian
	if len(b) >= 4 && le.Uint32(b) == wmfPlaceableKey {
		if len(b) < wmfPlaceableSize+18 {
			return false
		}
		left, top := int16(le.Uint16(b[6:])), int16(le.Uint16(b[8:]))       //nolint:gosec // WMF stores int16
		right, bottom := int16(le.Uint16(b[10:])), int16(le.Uint16(b[12:])) //nolint:gosec // WMF stores int16
		if right == left || bottom == top || le.Uint16(b[14:]) == 0 {
			return false
		}
		b = b[wmfPlaceableSize:]
	}
	if len(b) < 18 {
		return false
	}
	typ, size, version := le.Uint16(b), le.Uint16(b[2:]), le.Uint16(b[4:])
	return (typ == 1 || typ == 2) && size == 9 && (version == 0x0100 || version == 0x0300)
}
