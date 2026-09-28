package codec

import (
	"context"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Macintosh PICT and Kodak Photo CD (WP-108) are decoded by ImageMagick's
// PICT and PCD coders, named explicitly (vips.LoadMagickCoder): libvips'
// buffer loader lets ImageMagick guess the coder from the first bytes, and
// PICT's 512-byte application header (usually zeros) looks like a TGA file
// to it. Their headers are read here, in Go, so a probe never runs
// ImageMagick and the pixel limits hold before it does.

// PICT layout: a 512-byte application header, the 2-byte picture size, the
// picture frame (top, left, bottom, right; big-endian int16), then the
// version opcode: 0x11 0x01 (version 1) or 0x0011 0x02FF (version 2).
const (
	pictFrame   = 514
	pictVersion = 522
	// pictMaxOps bounds the header opcodes walked before the first drawing
	// opcode.
	pictMaxOps = 256
)

// Photo CD layout: "PCD_IPI" at 2048, the rotation in the low two bits of
// byte 0x0E02, the Base image (768×512 YCC 4:2:0) at sector 96.
const (
	pcdMagic      = "PCD_IPI"
	pcdMagicAt    = 2048
	pcdRotationAt = 0x0E02
	pcdBaseEnd    = 0x30000 + 768*512*3/2
	pcdBaseWidth  = 768
	pcdBaseHeight = 512
)

// isPICT recognises a PICT file with its 512-byte header: a non-empty
// frame with non-negative coordinates, then a version 1 or 2 opcode. The
// shim's xi_is_pict checks the same before ImageMagick runs.
func isPICT(b []byte) bool {
	if len(b) < pictVersion+6 {
		return false
	}
	top, left, bottom, right := pictRect(b[pictFrame:])
	if top < 0 || left < 0 || bottom <= top || right <= left {
		return false
	}
	v := b[pictVersion:]
	return (v[0] == 0x11 && v[1] == 0x01) || (v[0] == 0x00 && v[1] == 0x11 && v[2] == 0x02 && v[3] == 0xFF)
}

// isPCD recognises a Photo CD image pack holding at least the Base image.
func isPCD(b []byte) bool {
	return len(b) >= pcdBaseEnd && string(b[pcdMagicAt:pcdMagicAt+len(pcdMagic)]) == pcdMagic
}

func pictRect(b []byte) (top, left, bottom, right int) {
	be := binary.BigEndian
	return int(int16(be.Uint16(b))), int(int16(be.Uint16(b[2:]))), //nolint:gosec // PICT stores int16
		int(int16(be.Uint16(b[4:]))), int(int16(be.Uint16(b[6:]))) //nolint:gosec // PICT stores int16
}

// PICTHeader is what a PICT file's header declares.
type PICTHeader struct {
	// Width and Height are the canvas ImageMagick draws on: the picture
	// frame, or the clip rectangle when the header opcodes set one before
	// the first drawing opcode (a version 2 file drawn at 144 DPI has a
	// 72 DPI frame and a clip at its real size).
	Width, Height int
	// Version is 1 or 2.
	Version int
}

// ParsePICTHeader reads the header and the opcodes before the first
// drawing opcode. Malformed headers fail with errs.KindCorrupt.
func ParsePICTHeader(b []byte) (PICTHeader, error) {
	if !isPICT(b) {
		return PICTHeader{}, legacyCorrupt(FormatPICT, "bad header")
	}
	top, left, bottom, right := pictRect(b[pictFrame:])
	h := PICTHeader{Width: right - left, Height: bottom - top, Version: 1}
	pos := pictVersion + 2
	if b[pictVersion] == 0 {
		h.Version, pos = 2, pictVersion+4
	}
	be := binary.BigEndian
	for range pictMaxOps {
		var op int
		switch h.Version {
		case 1:
			if pos >= len(b) {
				return h, nil
			}
			op, pos = int(b[pos]), pos+1
		default:
			pos += pos & 1 // version 2 opcodes are word-aligned
			if pos+2 > len(b) {
				return h, nil
			}
			op, pos = int(be.Uint16(b[pos:])), pos+2
		}
		n, ok := pictHeaderOpLen(op, b, pos)
		if !ok {
			return h, nil // the first drawing opcode: the canvas is settled
		}
		if op == 0x0001 && pos+10 <= len(b) && be.Uint16(b[pos:]) == 10 {
			t, l, bo, r := pictRect(b[pos+2:])
			if t >= 0 && l >= 0 { // ImageMagick ignores a clip with a negative origin
				if bo <= t || r <= l {
					return PICTHeader{}, legacyCorrupt(FormatPICT, "empty clip rectangle")
				}
				h.Width, h.Height = r-l, bo-t
			}
		}
		if n < 0 || pos+n > len(b) {
			return PICTHeader{}, legacyCorrupt(FormatPICT, "truncated opcode")
		}
		pos += n
	}
	return h, nil
}

// pictHeaderOpLen is the data length of the state-setting opcodes that
// precede drawing in a PICT file (Inside Macintosh: Imaging With QuickDraw,
// appendix A), reading variable lengths at pos. ok is false for every other
// opcode.
func pictHeaderOpLen(op int, b []byte, pos int) (int, bool) {
	u16 := func(at int) int {
		if at+2 > len(b) {
			return -1
		}
		return int(binary.BigEndian.Uint16(b[at:]))
	}
	switch op {
	case 0x0000, 0x001C, 0x001E: // NOP, HiliteMode, DefHilite
		return 0, true
	case 0x0004: // TxFace
		return 1, true
	case 0x0003, 0x0005, 0x0008, 0x000D, 0x0015, 0x0016, 0x00A0: // TxFont, TxMode, PnMode, TxSize, PnLocHFrac, ChExtra, ShortComment
		return 2, true
	case 0x0006, 0x0007, 0x000B, 0x000C, 0x000E, 0x000F: // SpExtra, PnSize, OvSize, Origin, FgColor, BkColor
		return 4, true
	case 0x001A, 0x001B, 0x001D, 0x001F: // RGBFgCol, RGBBkCol, HiliteColor, OpColor
		return 6, true
	case 0x0002, 0x0009, 0x000A, 0x0010: // BkPat, PnPat, FillPat, TxRatio
		return 8, true
	case 0x0C00: // HeaderOp (version 2)
		return 24, true
	case 0x0001: // Clip: a region, its size (itself included) first
		n := u16(pos)
		if n < 2 {
			return -1, true
		}
		return n, true
	case 0x00A1: // LongComment: kind, size, data
		n := u16(pos + 2)
		if n < 0 {
			return -1, true
		}
		return 4 + n, true
	default:
		return 0, false
	}
}

// PCDHeader is what a Photo CD image pack declares about its Base image.
type PCDHeader struct {
	// Width and Height are the Base image's, 768×512, or 512×768 when the
	// pack says the picture is rotated (ImageMagick turns it upright).
	Width, Height int
}

// ParsePCDHeader reads the image pack header.
func ParsePCDHeader(b []byte) (PCDHeader, error) {
	if !isPCD(b) {
		return PCDHeader{}, legacyCorrupt(FormatPCD, "not a Photo CD image pack with a Base image")
	}
	if r := b[pcdRotationAt] & 0x03; r == 1 || r == 3 {
		return PCDHeader{Width: pcdBaseHeight, Height: pcdBaseWidth}, nil
	}
	return PCDHeader{Width: pcdBaseWidth, Height: pcdBaseHeight}, nil
}

// probePICT and probePCD describe the decoded image from the Go header:
// ImageMagick draws both as 8-bit RGB.
func probePICT(data []byte) (ImageMeta, error) {
	h, err := ParsePICTHeader(data)
	if err != nil {
		return ImageMeta{}, err
	}
	return magickCoderMeta(FormatPICT, h.Width, h.Height), nil
}

func probePCD(data []byte) (ImageMeta, error) {
	h, err := ParsePCDHeader(data)
	if err != nil {
		return ImageMeta{}, err
	}
	return magickCoderMeta(FormatPCD, h.Width, h.Height), nil
}

func magickCoderMeta(f Format, w, h int) ImageMeta {
	return ImageMeta{
		Format:      f,
		Width:       w,
		Height:      h,
		Frames:      1,
		Pages:       1,
		PageHeight:  h,
		Orientation: 1,
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  ColorSpaceSRGB,
	}
}

// magickCoder is the ImageMagick coder of a PICT or Photo CD source.
func magickCoder(f Format) (vips.MagickCoder, bool) {
	switch f {
	case FormatPICT:
		return vips.MagickPICT, true
	case FormatPCD:
		return vips.MagickPCD, true
	default:
		return 0, false
	}
}

// LoadMagickCoder decodes a PICT or Photo CD source (f) with its ImageMagick
// coder, as 8-bit sRGB without alpha of exactly the probed size: the few
// PICT files whose clip rectangle changes after the first drawing opcode
// are cropped or extended (black) to the size the probe reported, which the
// plan was made for.
func LoadMagickCoder(ctx context.Context, a *vips.Arena, buf *vips.Buffer, f Format, failOn vips.FailOn) (*vips.Image, error) {
	coder, ok := magickCoder(f)
	if !ok {
		return nil, errs.Newf(errs.KindInternal, "codec: %s has no named ImageMagick coder", f)
	}
	var w, h int
	switch f {
	case FormatPICT:
		ph, err := ParsePICTHeader(buf.Bytes())
		if err != nil {
			return nil, err
		}
		w, h = ph.Width, ph.Height
	default:
		ph, err := ParsePCDHeader(buf.Bytes())
		if err != nil {
			return nil, err
		}
		w, h = ph.Width, ph.Height
	}
	img, err := a.Add(vips.LoadMagickCoder(ctx, buf, coder, &vips.LoadOptions{Access: vips.AccessRandom, FailOn: failOn}))
	if err != nil {
		return nil, err
	}
	if img.HasAlpha() {
		// ImageMagick reads the fourth component of a 32-bit PICT pixmap as
		// alpha; QuickDraw never drew with it (it is usually zero). Drop it.
		if img, err = a.Add(img.ExtractBand(ctx, 0, img.Bands()-1)); err != nil {
			return nil, err
		}
	}
	if img.Interpretation() != vips.InterpretationSRGB || img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
		if img.BandFormat() != vips.BandFormatUchar {
			if img, err = a.Add(img.CastRound(ctx, vips.BandFormatUchar)); err != nil {
				return nil, err
			}
		}
	}
	if img.Width() != w || img.Height() != h {
		return a.Add(img.Embed(ctx, 0, 0, w, h, vips.ExtendBlack, nil))
	}
	return img, nil
}
