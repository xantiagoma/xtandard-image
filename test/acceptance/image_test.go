//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register the GIF decoder
	_ "image/jpeg" // register the JPEG decoder
	_ "image/png"  // register the PNG decoder

	_ "golang.org/x/image/bmp" // register the BMP decoder
	"golang.org/x/image/tiff"
	_ "golang.org/x/image/webp" // register the WebP decoder
)

// props are the properties the suite asserts on a response image.
type props struct {
	Format        string // jpeg, png, gif, webp, bmp, tiff, avif, heic, jxl, ico
	Width, Height int
}

// decodeProps identifies an encoded image and reads its dimensions. Formats
// the stdlib or x/image decode are fully decoded (pixels included), so a
// truncated body fails; AVIF/HEIC (ISOBMFF ftyp + ispe), JPEG XL (size
// header) and ICO (directory) are sniffed from their headers.
func decodeProps(b []byte) (props, error) {
	switch {
	case isISOBMFF(b):
		return isobmffProps(b)
	case bytes.HasPrefix(b, []byte{0xff, 0x0a}), bytes.HasPrefix(b, jxlContainerSig):
		return jxlProps(b)
	case bytes.HasPrefix(b, []byte{0, 0, 1, 0}):
		return icoProps(b)
	case bytes.HasPrefix(b, []byte("II*\x00")), bytes.HasPrefix(b, []byte("MM\x00*")):
		// libvips writes JPEG-compressed YCbCr TIFF, which x/image/tiff
		// cannot decode; fall back to the IFD for those.
		img, err := tiff.Decode(bytes.NewReader(b))
		if err == nil {
			return props{Format: "tiff", Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}, nil
		}
		if _, ok := errors.AsType[tiff.UnsupportedError](err); !ok {
			return props{}, fmt.Errorf("decode tiff: %w", err)
		}
		return tiffProps(b)
	}
	img, format, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return props{}, fmt.Errorf("decode: %w", err)
	}
	r := img.Bounds()
	return props{Format: format, Width: r.Dx(), Height: r.Dy()}, nil
}

var errShort = errors.New("truncated header")

// ---- ISOBMFF (AVIF, HEIC) ----------------------------------------------------

func isISOBMFF(b []byte) bool { return len(b) >= 12 && string(b[4:8]) == "ftyp" }

// isobmffProps maps the ftyp brands to avif/heic and reads the first ispe
// (image spatial extents) property: version/flags, width, height.
func isobmffProps(b []byte) (props, error) {
	size := int(binary.BigEndian.Uint32(b))
	if size < 16 || size > len(b) {
		return props{}, errShort
	}
	var p props
	for off := 8; off+4 <= size; off += 4 {
		if off == 12 {
			continue // minor_version
		}
		switch string(b[off : off+4]) {
		case "avif", "avis":
			p.Format = "avif"
		case "heic", "heix", "hevc", "heim", "heis", "hevm", "hevs":
			if p.Format == "" {
				p.Format = "heic"
			}
		}
		if p.Format == "avif" {
			break
		}
	}
	if p.Format == "" {
		return props{}, fmt.Errorf("unknown ftyp brands %q", b[8:size])
	}
	i := bytes.Index(b, []byte("ispe"))
	if i < 0 || i+16 > len(b) {
		return props{}, errors.New("no ispe box")
	}
	p.Width = int(binary.BigEndian.Uint32(b[i+8:]))
	p.Height = int(binary.BigEndian.Uint32(b[i+12:]))
	return p, nil
}

// ---- JPEG XL -------------------------------------------------------------------

var jxlContainerSig = []byte{0, 0, 0, 0x0c, 'J', 'X', 'L', ' ', 0x0d, 0x0a, 0x87, 0x0a}

// jxlProps reads the SizeHeader of a bare codestream or of the jxlc/jxlp
// box of a container (ISO/IEC 18181-1 §D.2).
func jxlProps(b []byte) (props, error) {
	cs := b
	if bytes.HasPrefix(b, jxlContainerSig) {
		cs = nil
		for off := 0; off+8 <= len(b); {
			size := int(binary.BigEndian.Uint32(b[off:]))
			typ := string(b[off+4 : off+8])
			const hdr = 8
			if size == 1 {
				return props{}, errors.New("jxl: 64-bit box sizes are not supported")
			}
			if size == 0 {
				size = len(b) - off
			}
			if size < hdr || off+size > len(b) {
				return props{}, errShort
			}
			switch typ {
			case "jxlc":
				cs = b[off+hdr : off+size]
			case "jxlp":
				cs = b[off+hdr+4 : off+size] // 4-byte part index
			}
			if cs != nil {
				break
			}
			off += size
		}
	}
	if len(cs) < 2 || cs[0] != 0xff || cs[1] != 0x0a {
		return props{}, errors.New("no JPEG XL codestream")
	}
	r := &bitReader{b: cs[2:]}
	var w, h int
	if r.bits(1) == 1 { // small
		h = int(r.bits(5)+1) * 8
		if ratio := r.bits(3); ratio != 0 {
			w = jxlRatio(h, ratio)
		} else {
			w = int(r.bits(5)+1) * 8
		}
	} else {
		h = r.u32size()
		if ratio := r.bits(3); ratio != 0 {
			w = jxlRatio(h, ratio)
		} else {
			w = r.u32size()
		}
	}
	if r.err {
		return props{}, errShort
	}
	return props{Format: "jxl", Width: w, Height: h}, nil
}

// jxlRatio applies the SizeHeader aspect ratio code (1..7).
func jxlRatio(h int, ratio uint32) int {
	num := [...]int{1, 12, 4, 3, 16, 5, 2}
	den := [...]int{1, 10, 3, 2, 9, 4, 1}
	return h * num[ratio-1] / den[ratio-1]
}

// bitReader reads JPEG XL's LSB-first bit stream.
type bitReader struct {
	b   []byte
	pos int
	err bool
}

func (r *bitReader) bits(n int) uint32 {
	var v uint32
	for i := range n {
		if r.pos/8 >= len(r.b) {
			r.err = true
			return 0
		}
		bit := (r.b[r.pos/8] >> (r.pos % 8)) & 1
		v |= uint32(bit) << i
		r.pos++
	}
	return v
}

// u32size is U32(Bits(9)+1, Bits(13)+1, Bits(18)+1, Bits(30)+1).
func (r *bitReader) u32size() int {
	n := [...]int{9, 13, 18, 30}[r.bits(2)]
	return int(r.bits(n)) + 1
}

// ---- TIFF ----------------------------------------------------------------------

// tiffProps reads ImageWidth/ImageLength from IFD0 and checks that the
// strips (StripOffsets + StripByteCounts, one strip or an array) lie inside
// the file.
func tiffProps(b []byte) (props, error) {
	var bo binary.ByteOrder = binary.LittleEndian
	if b[0] == 'M' {
		bo = binary.BigEndian
	}
	if len(b) < 8 {
		return props{}, errShort
	}
	ifd := int(bo.Uint32(b[4:]))
	if ifd+2 > len(b) {
		return props{}, errShort
	}
	n := int(bo.Uint16(b[ifd:]))
	if ifd+2+12*n > len(b) {
		return props{}, errShort
	}
	// field is the value of a single SHORT/LONG, else the offset.
	type entry struct{ typ, count, field uint32 }
	tags := map[uint16]entry{}
	for i := range n {
		e := b[ifd+2+12*i:]
		typ := uint32(bo.Uint16(e[2:]))
		field := bo.Uint32(e[8:])
		if typ == 3 && bo.Uint32(e[4:]) == 1 {
			field = uint32(bo.Uint16(e[8:]))
		}
		tags[bo.Uint16(e)] = entry{typ: typ, count: bo.Uint32(e[4:]), field: field}
	}
	w, h := tags[256], tags[257]
	if w.field == 0 || h.field == 0 {
		return props{}, errors.New("tiff: no ImageWidth/ImageLength")
	}
	off, cnt := tags[273], tags[279]
	if off.count == 0 || off.count != cnt.count {
		return props{}, errors.New("tiff: no strips")
	}
	if off.count == 1 && int(off.field)+int(cnt.field) > len(b) {
		return props{}, errShort
	}
	return props{Format: "tiff", Width: int(w.field), Height: int(h.field)}, nil
}

// ---- ICO -----------------------------------------------------------------------

// icoProps reads the first directory entry (a 0 byte means 256) and checks
// that its image data lies inside the file.
func icoProps(b []byte) (props, error) {
	if len(b) < 22 || binary.LittleEndian.Uint16(b[4:]) == 0 {
		return props{}, errShort
	}
	dim := func(v byte) int {
		if v == 0 {
			return 256
		}
		return int(v)
	}
	e := b[6:22]
	size := int(binary.LittleEndian.Uint32(e[8:]))
	off := int(binary.LittleEndian.Uint32(e[12:]))
	if off < 22 || off+size > len(b) {
		return props{}, errShort
	}
	return props{Format: "ico", Width: dim(e[0]), Height: dim(e[1])}, nil
}
