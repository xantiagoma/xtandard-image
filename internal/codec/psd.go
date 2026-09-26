package codec

import (
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// PSDHeader is what the first sections of a Photoshop file declare. It is
// read in Go so that probing a PSD never runs ImageMagick, which decodes the
// whole file even for a header.
type PSDHeader struct {
	// PSB is set for the large-document format (version 2).
	PSB bool
	// Width and Height are the canvas size.
	Width, Height int
	// Channels counts the channels of the merged image, alpha included.
	Channels int
	// Depth is the bits per channel: 1, 8, 16 or 32.
	Depth int
	// Mode is the colour mode: 0 bitmap, 1 greyscale, 2 indexed, 3 RGB,
	// 4 CMYK, 7 multichannel, 8 duotone, 9 Lab.
	Mode int
	// Layers is the number of layers in the layer section (0 for flat files).
	Layers int
	// Composite reports whether the file carries a real merged image:
	// Photoshop's "Maximize compatibility". Without it the merged image data
	// is blank and a reader has to composite the layers.
	Composite bool
	// ICC reports an embedded colour profile (resource 0x040F).
	ICC bool
}

// PSD colour modes with their colour channel counts.
const (
	psdModeBitmap    = 0
	psdModeGrey      = 1
	psdModeIndexed   = 2
	psdModeRGB       = 3
	psdModeCMYK      = 4
	psdModeMulti     = 7
	psdModeDuotone   = 8
	psdModeLab       = 9
	psdMaxDimension  = 300_000 // PSB limit; PSD's is 30 000
	psdVersionInfo   = 0x0421
	psdICCProfile    = 0x040F
	psdResourceBlock = "8BIM"
)

// ParsePSDHeader reads the header, the image resources and the layer count
// of a PSD or PSB file without touching pixel data. Truncated or
// inconsistent headers fail with errs.KindCorrupt.
func ParsePSDHeader(b []byte) (PSDHeader, error) {
	be := binary.BigEndian
	if len(b) < 26 || string(b[:4]) != "8BPS" {
		return PSDHeader{}, errs.New(errs.KindCorrupt, "codec: psd: bad signature")
	}
	var h PSDHeader
	switch be.Uint16(b[4:]) {
	case 1:
	case 2:
		h.PSB = true
	default:
		return PSDHeader{}, errs.New(errs.KindCorrupt, "codec: psd: bad version")
	}
	h.Channels = int(be.Uint16(b[12:]))
	h.Height = int(be.Uint32(b[14:]))
	h.Width = int(be.Uint32(b[18:]))
	h.Depth = int(be.Uint16(b[22:]))
	h.Mode = int(be.Uint16(b[24:]))
	switch {
	case h.Channels < 1 || h.Channels > 56:
		return PSDHeader{}, errs.Newf(errs.KindCorrupt, "codec: psd: %d channels", h.Channels)
	case h.Width < 1 || h.Height < 1 || h.Width > psdMaxDimension || h.Height > psdMaxDimension:
		return PSDHeader{}, errs.Newf(errs.KindCorrupt, "codec: psd: %dx%d", h.Width, h.Height)
	case h.Depth != 1 && h.Depth != 8 && h.Depth != 16 && h.Depth != 32:
		return PSDHeader{}, errs.Newf(errs.KindCorrupt, "codec: psd: depth %d", h.Depth)
	}

	r := psdReader{b: b, off: 26}
	// Colour mode data (palette, duotone spec).
	if _, ok := r.section(false); !ok {
		return PSDHeader{}, errs.New(errs.KindCorrupt, "codec: psd: truncated colour mode data")
	}
	res, ok := r.section(false)
	if !ok {
		return PSDHeader{}, errs.New(errs.KindCorrupt, "codec: psd: truncated image resources")
	}
	h.Composite = true
	parsePSDResources(res, &h)
	// Layer and mask information: its first field is the layer info section,
	// which starts with the layer count (negative when the first alpha
	// channel holds the merged transparency).
	lm, ok := r.section(h.PSB)
	if !ok {
		return PSDHeader{}, errs.New(errs.KindCorrupt, "codec: psd: truncated layer section")
	}
	lr := psdReader{b: lm}
	if li, ok := lr.section(h.PSB); ok && len(li) >= 2 {
		// A signed 16-bit count: its magnitude is the number of layers.
		n := int(be.Uint16(li))
		if n >= 1<<15 {
			n = 1<<16 - n
		}
		h.Layers = n
	}
	return h, nil
}

// HasAlpha reports whether the merged image has more channels than its
// colour mode needs: the extra one is transparency.
func (h *PSDHeader) HasAlpha() bool {
	switch h.Mode {
	case psdModeRGB, psdModeLab:
		return h.Channels > 3
	case psdModeCMYK:
		return h.Channels > 4
	case psdModeGrey, psdModeDuotone, psdModeIndexed:
		return h.Channels > 1
	default:
		return false
	}
}

// ColorSpace is the colour space the decoded merged image has.
func (h *PSDHeader) ColorSpace() ColorSpace {
	switch h.Mode {
	case psdModeRGB, psdModeIndexed:
		return ColorSpaceSRGB
	case psdModeGrey, psdModeBitmap, psdModeDuotone:
		return ColorSpaceGrey
	case psdModeCMYK:
		return ColorSpaceCMYK
	case psdModeLab:
		return ColorSpaceLab
	case psdModeMulti:
		return ColorSpaceOther
	default:
		return ColorSpaceUnknown
	}
}

// parsePSDResources walks the image resource blocks for the version info
// (hasRealMergedData) and the ICC profile.
func parsePSDResources(res []byte, h *PSDHeader) {
	be := binary.BigEndian
	for p := 0; p+8 <= len(res); {
		if string(res[p:p+4]) != psdResourceBlock {
			return
		}
		id := be.Uint16(res[p+4:])
		p += 6
		// Pascal name, padded to an even length (length byte included).
		nameLen := int(res[p]) + 1
		p += nameLen + nameLen%2
		if p+4 > len(res) {
			return
		}
		size := int(be.Uint32(res[p:]))
		p += 4
		if size < 0 || size > len(res)-p {
			return
		}
		data := res[p : p+size]
		switch id {
		case psdVersionInfo:
			if len(data) >= 5 && data[4] == 0 {
				h.Composite = false
			}
		case psdICCProfile:
			h.ICC = size > 0
		}
		p += size + size%2
	}
}

// psdReader reads length-prefixed sections.
type psdReader struct {
	b   []byte
	off int
}

// section reads a 4-byte (8 with long) length and returns that many bytes.
func (r *psdReader) section(long bool) ([]byte, bool) {
	n := 4
	if long {
		n = 8
	}
	if r.off+n > len(r.b) {
		return nil, false
	}
	var size uint64
	if long {
		size = binary.BigEndian.Uint64(r.b[r.off:])
	} else {
		size = uint64(binary.BigEndian.Uint32(r.b[r.off:]))
	}
	r.off += n
	rest := r.b[r.off:]
	if size > uint64(len(rest)) {
		return nil, false
	}
	s := rest[:size]
	r.off += len(s)
	return s, true
}
