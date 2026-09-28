package gainmap

import "encoding/binary"

// maxInfe bounds the item entries HasTmap reads.
const maxInfe = 4096

// HasTmap reports whether an ISO BMFF file (AVIF, HEIF) declares a tone-map
// derived item (item type tmap): an ISO 21496-1 gain map. Only the
// top-level meta box's iinf is read; which images the item combines is the
// decoder's business (vips.LoadHEIFGainMap).
func HasTmap(src []byte) bool {
	meta, ok := child(src, "meta")
	if !ok || len(meta) < 4 {
		return false
	}
	iinf, ok := child(meta[4:], "iinf") // meta is a full box
	if !ok || len(iinf) < 6 {
		return false
	}
	entries := iinf[6:] // version, flags, 16-bit count
	if iinf[0] != 0 {
		if len(iinf) < 8 {
			return false
		}
		entries = iinf[8:]
	}
	off := 0
	for range maxInfe {
		typ, body, next, ok := box(entries, off)
		if !ok {
			return false
		}
		off = next
		if typ != "infe" || len(body) < 4 {
			continue
		}
		// infe version 2: item_ID(16) protection(16) type; version 3: item_ID(32).
		at := 4 + 4
		switch body[0] {
		case 2:
		case 3:
			at += 2
		default:
			continue
		}
		if len(body) >= at+4 && string(body[at:at+4]) == "tmap" {
			return true
		}
	}
	return false
}

// child returns the body of the first box of type typ in b.
func child(b []byte, typ string) ([]byte, bool) {
	off := 0
	for range maxInfe {
		t, body, next, ok := box(b, off)
		if !ok {
			return nil, false
		}
		if t == typ {
			return body, true
		}
		off = next
	}
	return nil, false
}

// box reads the ISO BMFF box at off in b: its type, body and the offset of
// the next box.
func box(b []byte, off int) (typ string, body []byte, next int, ok bool) {
	if off < 0 || off > len(b)-8 {
		return "", nil, 0, false
	}
	rest := b[off:]
	typ = string(rest[4:8])
	size, hdr := int(binary.BigEndian.Uint32(rest)), 8
	switch size {
	case 1:
		if len(rest) < 16 {
			return "", nil, 0, false
		}
		// A 64-bit size larger than what is left is refused below.
		size, hdr = len(rest)+1, 16
		if big := binary.BigEndian.Uint64(rest[8:]); big <= uint64(len(rest)) {
			size = int(big) //nolint:gosec // at most len(rest)
		}
	case 0:
		size = len(rest)
	}
	if size < hdr || size > len(rest) {
		return "", nil, 0, false
	}
	return typ, rest[hdr:size], off + size, true
}
