// Package icns decodes Apple icon images (ICNS) in pure Go. A file is a list
// of typed entries; Decode picks the largest usable one:
//
//   - PNG or JPEG 2000 streams (ic04..ic14, icp4..icp6, the modern entries)
//     come back as raster.Decoded.Embedded for libvips to load;
//   - "ARGB" entries (ic04, ic05) are PackBits-style run-length coded planes;
//   - the classic 24-bit entries is32, il32, ih32 and it32 (16, 32, 48 and
//     128 px) are run-length coded RGB planes whose alpha is the matching
//     8-bit mask entry (s8mk, l8mk, h8mk, t8mk), opaque when absent.
//
// 1-, 4- and 8-bit palette icons (ICN#, icl4, icl8, ...) are ignored; every
// file with one of them also carries a 24-bit entry.
package icns

import (
	"bytes"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	magic     = "icns"
	headerLen = 8
	pngSig    = "\x89PNG\r\n\x1a\n"
	jp2Sig    = "\x00\x00\x00\x0cjP  \r\n\x87\n"
	j2kSig    = "\xff\x4f\xff\x51"
	// maxSide bounds an embedded stream's declared size (ic10 is 1024 px).
	maxSide = 1 << 14
)

// Kind is how an entry is coded.
type Kind uint8

// Entry kinds.
const (
	KindPNG  Kind = iota + 1 // a PNG stream
	KindJP2                  // a JPEG 2000 stream (JP2 box or raw codestream)
	KindARGB                 // run-length coded A, R, G, B planes
	KindRGB                  // run-length (or raw) coded R, G, B planes + optional mask
)

// Entry is one usable image of an icon file.
type Entry struct {
	Type          string // the four-character entry type ("ic08", "is32", ...)
	Kind          Kind
	Width, Height int
	data          []byte
	mask          []byte // KindRGB: the 8-bit alpha entry, nil when absent
}

// Options control Decode.
type Options struct {
	// MaxPixels bounds the chosen entry's Width*Height
	// (errs.KindLimitExceeded). Zero means raster.DefaultMaxPixels.
	MaxPixels int64
}

// Sniff reports whether head starts like an ICNS file: the "icns" magic and
// a total length of at least one entry header.
func Sniff(head []byte) bool {
	return len(head) >= headerLen && string(head[:4]) == magic &&
		binary.BigEndian.Uint32(head[4:]) >= 2*headerLen
}

// classicSize is the side of the classic 24-bit entries and their masks.
func classicSize(t string) (side int, mask string, ok bool) {
	switch t {
	case "is32":
		return 16, "s8mk", true
	case "il32":
		return 32, "l8mk", true
	case "ih32":
		return 48, "h8mk", true
	case "it32":
		return 128, "t8mk", true
	}
	return 0, "", false
}

// streamSize is the nominal side of the entries that hold a PNG, JPEG 2000
// or ARGB stream (the stream's own header is what counts).
func streamSize(t string) (int, bool) {
	switch t {
	case "icp4", "ic04":
		return 16, true
	case "icp5", "ic05", "ic11":
		return 32, true
	case "icp6", "ic12":
		return 64, true
	case "ic07":
		return 128, true
	case "ic08", "ic13":
		return 256, true
	case "ic09", "ic14":
		return 512, true
	case "ic10":
		return 1024, true
	}
	return 0, false
}

// Entries lists the usable entries in file order. Entries that lie outside
// the file or do not parse are left out; a file with none fails with
// errs.KindCorrupt.
func Entries(b []byte) ([]Entry, error) {
	if !Sniff(b) {
		return nil, corrupt("missing icns header")
	}
	end := min(int(binary.BigEndian.Uint32(b[4:])), len(b))
	blocks := map[string][]byte{}
	var order []string
	for p := headerLen; p+headerLen <= end; {
		t := string(b[p : p+4])
		n := int(binary.BigEndian.Uint32(b[p+4:]))
		if n < headerLen || n > end-p {
			break
		}
		if _, seen := blocks[t]; !seen {
			order = append(order, t)
		}
		blocks[t] = b[p+headerLen : p+n]
		p += n
	}
	var entries []Entry
	for _, t := range order {
		data := blocks[t]
		if side, mask, ok := classicSize(t); ok {
			e := Entry{Type: t, Kind: KindRGB, Width: side, Height: side, data: data}
			if m := blocks[mask]; len(m) == side*side {
				e.mask = m
			}
			entries = append(entries, e)
			continue
		}
		side, ok := streamSize(t)
		if !ok {
			continue
		}
		if e, ok := streamEntry(t, side, data); ok {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil, corrupt("no usable icon entry")
	}
	return entries, nil
}

// streamEntry classifies a modern entry by its content.
func streamEntry(t string, side int, data []byte) (Entry, bool) {
	e := Entry{Type: t, Width: side, Height: side, data: data}
	switch {
	case bytes.HasPrefix(data, []byte(pngSig)):
		// IHDR is the first chunk: length(4) "IHDR" width height.
		if len(data) < 24 || string(data[12:16]) != "IHDR" {
			return Entry{}, false
		}
		e.Kind = KindPNG
		e.Width = int(binary.BigEndian.Uint32(data[16:]))
		e.Height = int(binary.BigEndian.Uint32(data[20:]))
	case bytes.HasPrefix(data, []byte(jp2Sig)), bytes.HasPrefix(data, []byte(j2kSig)):
		e.Kind = KindJP2
		// The JP2 image header box: "ihdr" height width.
		head := data[:min(len(data), 256)]
		if i := bytes.Index(head, []byte("ihdr")); i >= 0 && i+12 <= len(head) {
			e.Height = int(binary.BigEndian.Uint32(head[i+4:]))
			e.Width = int(binary.BigEndian.Uint32(head[i+8:]))
		}
	case bytes.HasPrefix(data, []byte("ARGB")) && (t == "ic04" || t == "ic05"):
		e.Kind = KindARGB
		e.data = data[4:]
	default:
		return Entry{}, false
	}
	if e.Width <= 0 || e.Height <= 0 || e.Width > maxSide || e.Height > maxSide {
		return Entry{}, false
	}
	return e, true
}

// Pick returns the largest entry by area, a stream (PNG, JPEG 2000) winning
// ties over run-length coded pixels. entries must not be empty.
func Pick(entries []Entry) Entry {
	best := entries[0]
	for _, e := range entries[1:] {
		a, b := int64(e.Width)*int64(e.Height), int64(best.Width)*int64(best.Height)
		if a > b || (a == b && e.Kind < best.Kind) {
			best = e
		}
	}
	return best
}

// HasAlpha reports whether the entry carries transparency. PNG and JPEG 2000
// streams are assumed to (icons almost always do).
func (e *Entry) HasAlpha() bool {
	return e.Kind != KindRGB || e.mask != nil
}

// Decode picks the largest entry and decodes it: a PNG or JPEG 2000 entry
// comes back as Embedded, a run-length coded one as RGBA Raw. Malformed
// input fails with errs.KindCorrupt and an entry over MaxPixels with
// errs.KindLimitExceeded; Decode never panics.
func Decode(b []byte, o Options) (raster.Decoded, error) {
	entries, err := Entries(b)
	if err != nil {
		return raster.Decoded{}, err
	}
	e := Pick(entries)
	limit := o.MaxPixels
	if limit <= 0 {
		limit = raster.DefaultMaxPixels
	}
	if int64(e.Width)*int64(e.Height) > limit {
		return raster.Decoded{}, errs.Newf(errs.KindLimitExceeded, "icns: %dx%d exceeds %d pixels", e.Width, e.Height, limit)
	}
	switch e.Kind {
	case KindPNG, KindJP2:
		return raster.Decoded{Embedded: e.data}, nil
	case KindARGB:
		return decodePlanes(&e, e.data, []int{3, 0, 1, 2})
	default:
		data := e.data
		if e.Type == "it32" && len(data) >= 4 && binary.BigEndian.Uint32(data) == 0 {
			data = data[4:] // it32 starts with four zero bytes
		}
		return decodePlanes(&e, data, []int{0, 1, 2})
	}
}

// decodePlanes decodes consecutive planes into RGBA; channels gives each
// plane's position in a pixel (0 R, 1 G, 2 B, 3 A). Classic RGB entries are
// run-length coded unless they hold exactly the raw planes.
func decodePlanes(e *Entry, data []byte, channels []int) (raster.Decoded, error) {
	n := e.Width * e.Height
	out := make([]byte, n*4)
	for i := 3; i < len(out); i += 4 {
		out[i] = 255
	}
	raw := e.Kind == KindRGB && len(data) == n*len(channels)
	p := 0
	plane := make([]byte, n)
	for _, ch := range channels {
		if raw {
			copy(plane, data[p:p+n])
			p += n
		} else {
			var ok bool
			if p, ok = unpack(plane, data, p); !ok {
				return raster.Decoded{}, corrupt("truncated run-length data in " + e.Type)
			}
		}
		for i, v := range plane {
			out[i*4+ch] = v
		}
	}
	if e.mask != nil {
		for i, v := range e.mask {
			out[i*4+3] = v
		}
	}
	return raster.Decoded{Raw: &raster.Raw{Width: e.Width, Height: e.Height, Bands: 4, Data: out}}, nil
}

// unpack fills dst from Apple's icon run-length coding at src[p:]: a byte c
// below 0x80 is followed by c+1 literal bytes, any other byte by one value
// repeated c-0x80+3 times. It returns the index after the plane.
func unpack(dst, src []byte, p int) (int, bool) {
	for o := 0; o < len(dst); {
		if p >= len(src) {
			return p, false
		}
		c := int(src[p])
		p++
		if c < 0x80 {
			n := c + 1
			if p+n > len(src) || o+n > len(dst) {
				return p, false
			}
			copy(dst[o:], src[p:p+n])
			p += n
			o += n
			continue
		}
		n := c - 0x80 + 3
		if p >= len(src) || o+n > len(dst) {
			return p, false
		}
		v := src[p]
		p++
		for i := range n {
			dst[o+i] = v
		}
		o += n
	}
	return p, true
}

func corrupt(msg string) error { return errs.New(errs.KindCorrupt, "icns: "+msg) }
