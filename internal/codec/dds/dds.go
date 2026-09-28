// Package dds decodes DirectDraw Surface (DDS) textures in pure Go: the
// first image of the file, which is mipmap level 0 of the first array
// element, the first (+X) face of a cube map or the first slice of a volume.
//
// Supported encodings:
//
//   - block compression BC1-BC7, from the legacy FourCCs (DXT1-DXT5, ATI1,
//     ATI2, BC4U/S, BC5U/S) or a DX10 header (DXGI_FORMAT_BC*), including
//     BC6H (unsigned and signed half-float HDR) and BC7;
//   - uncompressed legacy pixel formats described by bit masks (RGB, RGBA,
//     luminance and luminance+alpha at 8 to 32 bits per pixel);
//   - the common uncompressed DXGI formats: 8-bit RGBA, BGRA and BGRX
//     (UNORM and sRGB), R8, R8G8, A8, R10G10B10A2, 16-bit RGBA, 5:6:5,
//     5:5:5:1 and 4:4:4:4, and the half and single float formats.
//
// Low dynamic range textures decode to 8-bit RGB or RGBA (raster.Raw):
// one-channel formats (BC4, R8, luminance) as grey, two-channel ones (BC5,
// R8G8) with blue 0, signed (SNORM) values mapped from [-1, 1] to [0, 255].
// BC6H and the float formats decode to linear float RGB or RGBA
// (raster.Float), which the pipeline tone-maps like OpenEXR (Config.HDR).
//
// Decoding is bounded: the header is validated before anything is
// allocated, the pixel data must be present in full (so the output is at
// most 48 bytes per input byte), and Options.MaxPixels caps the image.
package dds

import (
	"encoding/binary"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	magic      = "DDS "
	headerSize = 124
	dataStart  = 4 + headerSize // after the magic and DDS_HEADER
	dx10Size   = 20
	// paletteBytes is the legacy 8-bit palette: 256 R, G, B, A entries.
	paletteBytes = 256 * 4
	// maxSide bounds each dimension (D3D12's largest texture is 16384;
	// larger values are corrupt or hostile).
	maxSide = 1 << 16
)

// DDS_PIXELFORMAT flags.
const (
	pfAlphaPixels = 0x1
	pfAlpha       = 0x2
	pfFourCC      = 0x4
	pfPalette8    = 0x20
	pfRGB         = 0x40
	pfYUV         = 0x200
	pfLuminance   = 0x20000
	pfBumpDuDv    = 0x80000
)

// DX10 alpha modes (DDS_HEADER_DXT10.miscFlags2).
const alphaModeOpaque = 3

// kind is how the pixels of the first image are encoded.
type kind uint8

const (
	kindBC1 kind = iota + 1
	kindBC2
	kindBC3
	kindBC4U
	kindBC4S
	kindBC5U
	kindBC5S
	kindBC6HU
	kindBC6HS
	kindBC7
	kindMasks    // legacy bit masks (uncompressed)
	kindPalette8 // legacy 8-bit palette
	kindRGBA8    // R8G8B8A8
	kindBGRA8    // B8G8R8A8
	kindBGRX8    // B8G8R8X8
	kindR8       // one channel
	kindA8       // alpha only: shown as grey
	kindRG8      // R8G8
	kindRGB10A2  // R10G10B10A2
	kindRGBA16   // R16G16B16A16 UNORM
	kindRGBA16F  // R16G16B16A16 half float
	kindRGBA32F  // R32G32B32A32 float
	kindRGB32F   // R32G32B32 float
	kindR16F     // one half-float channel
	kindR32F     // one float channel
)

// Config is what the header declares about the first image.
type Config struct {
	Width, Height int
	// Format names the encoding ("BC1", "BC6H_UF16", "B8G8R8A8", "RGB
	// masks", ...), for diagnostics.
	Format string
	// Alpha reports that Decode returns an alpha band.
	Alpha bool
	// Grey reports a one-channel texture (decoded as equal R, G and B).
	Grey bool
	// HDR reports float samples (BC6H, the float formats): Decode returns
	// raster.Float.
	HDR bool

	kind   kind
	offset int // start of the pixel data
	// Legacy bit masks (kindMasks).
	bpp        int
	masks      [4]uint32 // R, G, B, A
	blockBytes int       // bytes per 4x4 block, or 0 for uncompressed kinds
	pixelBytes int       // bytes per pixel of uncompressed kinds
}

// Options control Decode.
type Options struct {
	// MaxPixels bounds Width*Height (errs.KindLimitExceeded). Zero means
	// raster.DefaultMaxPixels.
	MaxPixels int64
}

func corrupt(what string) error { return errs.New(errs.KindCorrupt, "dds: "+what) }

func unsupported(format string) error {
	return errs.WithPublic(errs.Newf(errs.KindUnsupported, "dds: %s textures are not supported", format),
		"this DDS pixel format is not supported")
}

// Sniff reports whether head starts like a DDS file: the "DDS " magic and
// a 124-byte header.
func Sniff(head []byte) bool {
	return len(head) >= dataStart && string(head[:4]) == magic &&
		binary.LittleEndian.Uint32(head[4:]) == headerSize
}

// DecodeConfig parses the headers of b (DDS_HEADER and, when the FourCC
// says DX10, DDS_HEADER_DXT10). Unknown encodings fail with
// errs.KindUnsupported, malformed headers with errs.KindCorrupt. It does not
// check that the pixel data is present (Decode does).
func DecodeConfig(b []byte) (Config, error) {
	if !Sniff(b) {
		return Config{}, corrupt("bad header")
	}
	le := binary.LittleEndian
	c := Config{
		Height: int(min(le.Uint32(b[12:]), maxSide+1)),
		Width:  int(min(le.Uint32(b[16:]), maxSide+1)),
		offset: dataStart,
	}
	if c.Width < 1 || c.Height < 1 || c.Width > maxSide || c.Height > maxSide {
		return Config{}, errs.Newf(errs.KindCorrupt, "dds: %dx%d", c.Width, c.Height)
	}
	flags := le.Uint32(b[80:])
	fourCC := string(b[84:88])
	switch {
	case flags&pfFourCC != 0 && fourCC == "DX10":
		if len(b) < dataStart+dx10Size {
			return Config{}, corrupt("truncated DX10 header")
		}
		c.offset = dataStart + dx10Size
		dxgi := le.Uint32(b[dataStart:])
		opaque := le.Uint32(b[dataStart+16:])&0x7 == alphaModeOpaque
		if err := c.setDXGI(dxgi, opaque); err != nil {
			return Config{}, err
		}
	case flags&pfFourCC != 0:
		if err := c.setFourCC(fourCC, le.Uint32(b[84:]), flags); err != nil {
			return Config{}, err
		}
	default:
		if err := c.setMasks(b, flags); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

// set records the encoding of a DX10 or FourCC texture.
func (c *Config) set(k kind, name string, alpha bool) {
	c.kind, c.Format, c.Alpha = k, name, alpha
	switch k {
	case kindBC1, kindBC4U, kindBC4S:
		c.blockBytes = 8
	case kindBC2, kindBC3, kindBC5U, kindBC5S, kindBC6HU, kindBC6HS, kindBC7:
		c.blockBytes = 16
	case kindR8, kindA8:
		c.pixelBytes = 1
	case kindRG8, kindR16F:
		c.pixelBytes = 2
	case kindRGBA8, kindBGRA8, kindBGRX8, kindRGB10A2, kindR32F:
		c.pixelBytes = 4
	case kindRGBA16, kindRGBA16F:
		c.pixelBytes = 8
	case kindRGB32F:
		c.pixelBytes = 12
	case kindRGBA32F:
		c.pixelBytes = 16
	default:
	}
	switch k {
	case kindBC4U, kindBC4S, kindR8, kindA8, kindR16F, kindR32F:
		c.Grey = true
	default:
	}
	switch k {
	case kindBC6HU, kindBC6HS, kindRGBA16F, kindRGBA32F, kindRGB32F, kindR16F, kindR32F:
		c.HDR = true
	default:
	}
}

// DXGI_FORMAT values.
const (
	dxgiR32G32B32A32Typeless = 1
	dxgiR32G32B32A32Float    = 2
	dxgiR32G32B32Typeless    = 5
	dxgiR32G32B32Float       = 6
	dxgiR16G16B16A16Typeless = 9
	dxgiR16G16B16A16Float    = 10
	dxgiR16G16B16A16UNorm    = 11
	dxgiR10G10B10A2Typeless  = 23
	dxgiR10G10B10A2UNorm     = 24
	dxgiR8G8B8A8Typeless     = 27
	dxgiR8G8B8A8UNorm        = 28
	dxgiR8G8B8A8UNormSRGB    = 29
	dxgiR32Float             = 41
	dxgiR8G8Typeless         = 48
	dxgiR8G8UNorm            = 49
	dxgiR16Float             = 54
	dxgiR8Typeless           = 60
	dxgiR8UNorm              = 61
	dxgiA8UNorm              = 65
	dxgiBC1Typeless          = 70
	dxgiBC1UNormSRGB         = 72
	dxgiBC2Typeless          = 73
	dxgiBC2UNormSRGB         = 75
	dxgiBC3Typeless          = 76
	dxgiBC3UNormSRGB         = 78
	dxgiBC4Typeless          = 79
	dxgiBC4UNorm             = 80
	dxgiBC4SNorm             = 81
	dxgiBC5Typeless          = 82
	dxgiBC5UNorm             = 83
	dxgiBC5SNorm             = 84
	dxgiB5G6R5UNorm          = 85
	dxgiB5G5R5A1UNorm        = 86
	dxgiB8G8R8A8UNorm        = 87
	dxgiB8G8R8X8UNorm        = 88
	dxgiB8G8R8A8Typeless     = 90
	dxgiB8G8R8A8UNormSRGB    = 91
	dxgiB8G8R8X8Typeless     = 92
	dxgiB8G8R8X8UNormSRGB    = 93
	dxgiBC6HTypeless         = 94
	dxgiBC6HUF16             = 95
	dxgiBC6HSF16             = 96
	dxgiBC7Typeless          = 97
	dxgiBC7UNormSRGB         = 99
	dxgiB4G4R4A4UNorm        = 115
)

// setDXGI maps a DXGI_FORMAT. opaque is the DX10 header's
// DDS_ALPHA_MODE_OPAQUE, which drops the alpha band of formats that have one.
func (c *Config) setDXGI(f uint32, opaque bool) error {
	alpha := !opaque
	switch {
	case f >= dxgiBC1Typeless && f <= dxgiBC1UNormSRGB:
		c.set(kindBC1, "BC1", alpha)
	case f >= dxgiBC2Typeless && f <= dxgiBC2UNormSRGB:
		c.set(kindBC2, "BC2", alpha)
	case f >= dxgiBC3Typeless && f <= dxgiBC3UNormSRGB:
		c.set(kindBC3, "BC3", alpha)
	case f == dxgiBC4Typeless || f == dxgiBC4UNorm:
		c.set(kindBC4U, "BC4_UNORM", false)
	case f == dxgiBC4SNorm:
		c.set(kindBC4S, "BC4_SNORM", false)
	case f == dxgiBC5Typeless || f == dxgiBC5UNorm:
		c.set(kindBC5U, "BC5_UNORM", false)
	case f == dxgiBC5SNorm:
		c.set(kindBC5S, "BC5_SNORM", false)
	case f == dxgiBC6HTypeless || f == dxgiBC6HUF16:
		c.set(kindBC6HU, "BC6H_UF16", false)
	case f == dxgiBC6HSF16:
		c.set(kindBC6HS, "BC6H_SF16", false)
	case f >= dxgiBC7Typeless && f <= dxgiBC7UNormSRGB:
		c.set(kindBC7, "BC7", alpha)
	case f >= dxgiR8G8B8A8Typeless && f <= dxgiR8G8B8A8UNormSRGB:
		c.set(kindRGBA8, "R8G8B8A8", alpha)
	case f == dxgiB8G8R8A8UNorm || f == dxgiB8G8R8A8Typeless || f == dxgiB8G8R8A8UNormSRGB:
		c.set(kindBGRA8, "B8G8R8A8", alpha)
	case f == dxgiB8G8R8X8UNorm || f == dxgiB8G8R8X8Typeless || f == dxgiB8G8R8X8UNormSRGB:
		c.set(kindBGRX8, "B8G8R8X8", false)
	case f == dxgiR8Typeless || f == dxgiR8UNorm:
		c.set(kindR8, "R8", false)
	case f == dxgiA8UNorm:
		c.set(kindA8, "A8", false)
	case f == dxgiR8G8Typeless || f == dxgiR8G8UNorm:
		c.set(kindRG8, "R8G8", false)
	case f == dxgiR10G10B10A2Typeless || f == dxgiR10G10B10A2UNorm:
		c.set(kindRGB10A2, "R10G10B10A2", alpha)
	case f == dxgiR16G16B16A16UNorm:
		c.set(kindRGBA16, "R16G16B16A16", alpha)
	case f == dxgiR16G16B16A16Float || f == dxgiR16G16B16A16Typeless:
		c.set(kindRGBA16F, "R16G16B16A16_FLOAT", alpha)
	case f == dxgiR32G32B32A32Float || f == dxgiR32G32B32A32Typeless:
		c.set(kindRGBA32F, "R32G32B32A32_FLOAT", alpha)
	case f == dxgiR32G32B32Float || f == dxgiR32G32B32Typeless:
		c.set(kindRGB32F, "R32G32B32_FLOAT", false)
	case f == dxgiR16Float:
		c.set(kindR16F, "R16_FLOAT", false)
	case f == dxgiR32Float:
		c.set(kindR32F, "R32_FLOAT", false)
	case f == dxgiB5G6R5UNorm:
		c.setMaskFormat("B5G6R5", 16, [4]uint32{0xF800, 0x07E0, 0x001F, 0})
	case f == dxgiB5G5R5A1UNorm:
		c.setMaskFormat("B5G5R5A1", 16, [4]uint32{0x7C00, 0x03E0, 0x001F, 0x8000})
		c.Alpha = alpha
	case f == dxgiB4G4R4A4UNorm:
		c.setMaskFormat("B4G4R4A4", 16, [4]uint32{0x0F00, 0x00F0, 0x000F, 0xF000})
		c.Alpha = alpha
	default:
		return unsupported("DXGI format " + strconv.FormatUint(uint64(f), 10))
	}
	return nil
}

// Legacy D3DFMT codes stored as a numeric FourCC.
const (
	d3dA16B16G16R16  = 36
	d3dR16F          = 111
	d3dA16B16G16R16F = 113
	d3dR32F          = 114
	d3dA32B32G32R32F = 116
)

// setFourCC maps a legacy FourCC (or numeric D3DFMT code).
func (c *Config) setFourCC(fourCC string, code, flags uint32) error {
	switch fourCC {
	case "DXT1":
		c.set(kindBC1, "BC1", flags&pfAlphaPixels != 0)
		return nil
	case "DXT2", "DXT3":
		c.set(kindBC2, "BC2", true)
		return nil
	case "DXT4", "DXT5":
		c.set(kindBC3, "BC3", true)
		return nil
	case "ATI1", "BC4U":
		c.set(kindBC4U, "BC4_UNORM", false)
		return nil
	case "BC4S":
		c.set(kindBC4S, "BC4_SNORM", false)
		return nil
	case "ATI2", "BC5U":
		c.set(kindBC5U, "BC5_UNORM", false)
		return nil
	case "BC5S":
		c.set(kindBC5S, "BC5_SNORM", false)
		return nil
	default:
	}
	switch code {
	case d3dA16B16G16R16:
		c.set(kindRGBA16, "R16G16B16A16", true)
	case d3dR16F:
		c.set(kindR16F, "R16_FLOAT", false)
	case d3dA16B16G16R16F:
		c.set(kindRGBA16F, "R16G16B16A16_FLOAT", true)
	case d3dR32F:
		c.set(kindR32F, "R32_FLOAT", false)
	case d3dA32B32G32R32F:
		c.set(kindRGBA32F, "R32G32B32A32_FLOAT", true)
	default:
		return unsupported("FourCC " + printable(fourCC))
	}
	return nil
}

// setMasks reads an uncompressed legacy pixel format: bit count and the R,
// G, B and A masks of DDS_PIXELFORMAT.
func (c *Config) setMasks(b []byte, flags uint32) error {
	le := binary.LittleEndian
	bpp := int(le.Uint32(b[88:]))
	masks := [4]uint32{le.Uint32(b[92:]), le.Uint32(b[96:]), le.Uint32(b[100:]), le.Uint32(b[104:])}
	if flags&(pfYUV|pfBumpDuDv) != 0 {
		return unsupported("YUV or bump-map")
	}
	if flags&pfPalette8 != 0 {
		// 256 RGBA entries, then one index per pixel.
		c.kind, c.Format, c.pixelBytes = kindPalette8, "palette8", 1
		c.Alpha = flags&pfAlphaPixels != 0
		return nil
	}
	if bpp != 8 && bpp != 16 && bpp != 24 && bpp != 32 {
		return unsupported("uncompressed " + strconv.Itoa(bpp) + "-bit")
	}
	if bpp < 32 {
		all := uint32(1)<<bpp - 1
		for i := range masks {
			masks[i] &= all
		}
	}
	if flags&pfAlphaPixels == 0 {
		masks[3] = 0
	}
	if flags&pfLuminance != 0 && masks[0] == 0 {
		// Some writers store the luminance mask out of the pixel
		// (0xFF000000 for 8 bits): take the bits alpha leaves.
		masks[0] = (uint32(1)<<bpp - 1) &^ masks[3]
	}
	alphaOnly := le.Uint32(b[104:]) & (uint32(1)<<bpp - 1)
	switch {
	case flags&pfLuminance != 0 && masks[0] != 0:
		masks[1], masks[2] = masks[0], masks[0]
		c.setMaskFormat("luminance masks", bpp, masks)
		c.Grey = masks[3] == 0
	case flags&pfRGB != 0 && (masks[0]|masks[1]|masks[2]) != 0:
		c.setMaskFormat("RGB masks", bpp, masks)
	case flags&pfAlpha != 0 && alphaOnly != 0:
		// Alpha only: shown as grey.
		c.setMaskFormat("alpha mask", bpp, [4]uint32{alphaOnly, alphaOnly, alphaOnly, 0})
		c.Grey = true
	default:
		return unsupported("uncompressed pixel format")
	}
	return nil
}

func (c *Config) setMaskFormat(name string, bpp int, masks [4]uint32) {
	c.kind, c.Format = kindMasks, name
	c.bpp, c.masks, c.pixelBytes = bpp, masks, bpp/8
	c.Alpha = masks[3] != 0
}

// dataSize is the number of bytes of the first image.
func (c *Config) dataSize() int64 {
	w, h := int64(c.Width), int64(c.Height)
	if c.blockBytes > 0 {
		return (w + 3) / 4 * ((h + 3) / 4) * int64(c.blockBytes)
	}
	if c.kind == kindPalette8 {
		return paletteBytes + w*h
	}
	return w * h * int64(c.pixelBytes)
}

// Decode decodes the first image of a DDS file: raster.Decoded.Raw for low
// dynamic range textures, raster.Decoded.Float when Config.HDR.
func Decode(b []byte, o Options) (raster.Decoded, error) {
	c, err := DecodeConfig(b)
	if err != nil {
		return raster.Decoded{}, err
	}
	limit := o.MaxPixels
	if limit <= 0 {
		limit = raster.DefaultMaxPixels
	}
	if int64(c.Width)*int64(c.Height) > limit {
		return raster.Decoded{}, errs.Newf(errs.KindLimitExceeded, "dds: %dx%d exceeds %d pixels", c.Width, c.Height, limit)
	}
	if int64(len(b)-c.offset) < c.dataSize() {
		return raster.Decoded{}, corrupt("truncated pixel data")
	}
	src := b[c.offset:]
	if c.HDR {
		return raster.Decoded{Float: decodeFloat(&c, src)}, nil
	}
	return raster.Decoded{Raw: decode8(&c, src)}, nil
}

// bands is the band count of the decoded image.
func (c *Config) bands() int {
	if c.Alpha {
		return 4
	}
	return 3
}

// decode8 decodes a low dynamic range texture; src holds at least
// c.dataSize() bytes.
func decode8(c *Config, src []byte) *raster.Raw {
	bands := c.bands()
	out := &raster.Raw{Width: c.Width, Height: c.Height, Bands: bands, Data: make([]byte, c.Width*c.Height*bands)}
	if c.blockBytes > 0 {
		var px [16][4]uint8
		decodeBlocks(c, src, func(block []byte, bx, by int) {
			switch c.kind {
			case kindBC1:
				decodeBC1(block, &px, false)
			case kindBC2:
				decodeBC2(block, &px)
			case kindBC3:
				decodeBC3(block, &px)
			case kindBC4U, kindBC4S:
				decodeBC4(block, &px, c.kind == kindBC4S)
			case kindBC5U, kindBC5S:
				decodeBC5(block, &px, c.kind == kindBC5S)
			default: // kindBC7
				decodeBC7(block, &px)
			}
			put8(out, &px, bx, by)
		})
		return out
	}
	if c.kind == kindPalette8 {
		pal, idx := src[:paletteBytes], src[paletteBytes:]
		for i := range c.Width * c.Height {
			e := int(idx[i]) * 4
			copy(out.Data[i*bands:i*bands+bands], pal[e:e+bands])
		}
		return out
	}
	var p [4]uint8
	for y := range c.Height {
		row := src[y*c.Width*c.pixelBytes:]
		dst := out.Data[y*c.Width*bands:]
		for x := range c.Width {
			pixel8(c, row[x*c.pixelBytes:], &p)
			copy(dst[x*bands:x*bands+bands], p[:bands])
		}
	}
	return out
}

// decodeBlocks calls fn for every 4x4 block of the first image, with the
// block's top-left pixel.
func decodeBlocks(c *Config, src []byte, fn func(block []byte, x, y int)) {
	bw := (c.Width + 3) / 4
	for by := range (c.Height + 3) / 4 {
		for bx := range bw {
			i := (by*bw + bx) * c.blockBytes
			fn(src[i:i+c.blockBytes], bx*4, by*4)
		}
	}
}

// put8 stores a decoded 4x4 block at (x0, y0), clipped to the image.
func put8(out *raster.Raw, px *[16][4]uint8, x0, y0 int) {
	for j := range 4 {
		y := y0 + j
		if y >= out.Height {
			return
		}
		for i := range 4 {
			x := x0 + i
			if x >= out.Width {
				break
			}
			o := (y*out.Width + x) * out.Bands
			copy(out.Data[o:o+out.Bands], px[j*4+i][:out.Bands])
		}
	}
}

// pixel8 decodes one uncompressed pixel to RGBA.
func pixel8(c *Config, s []byte, p *[4]uint8) {
	le := binary.LittleEndian
	switch c.kind {
	case kindRGBA8:
		*p = [4]uint8{s[0], s[1], s[2], s[3]}
	case kindBGRA8:
		*p = [4]uint8{s[2], s[1], s[0], s[3]}
	case kindBGRX8:
		*p = [4]uint8{s[2], s[1], s[0], 255}
	case kindR8, kindA8:
		*p = [4]uint8{s[0], s[0], s[0], 255}
	case kindRG8:
		*p = [4]uint8{s[0], s[1], 0, 255}
	case kindRGB10A2:
		v := le.Uint32(s)
		*p = [4]uint8{scale(v&0x3FF, 10), scale(v>>10&0x3FF, 10), scale(v>>20&0x3FF, 10), scale(v>>30, 2)}
	case kindRGBA16:
		for i := range 4 {
			p[i] = uint8((uint32(le.Uint16(s[2*i:])) + 128) / 257) //nolint:gosec // G115: 16 bits / 257 fits
		}
	default: // kindMasks
		var v uint32
		for i := range c.pixelBytes {
			v |= uint32(s[i]) << (8 * i)
		}
		for i, m := range c.masks {
			p[i] = maskValue(v, m)
		}
		if c.masks[3] == 0 {
			p[3] = 255
		}
	}
}

// maskValue extracts the channel selected by mask m from v and scales it
// to 8 bits.
func maskValue(v, m uint32) uint8 {
	if m == 0 {
		return 0
	}
	shift := 0
	for m>>shift&1 == 0 {
		shift++
	}
	bits := 0
	for bits+shift < 32 && m>>(shift+bits)&1 == 1 {
		bits++
	}
	return scale(v&m>>shift&(1<<bits-1), bits)
}

// scale maps an n-bit value to 8 bits, rounding.
func scale(v uint32, n int) uint8 {
	if n >= 32 {
		return uint8(v >> 24)
	}
	maxV := uint64(1)<<n - 1
	return uint8((uint64(v)*255 + maxV/2) / maxV) //nolint:gosec // G115: v <= maxV
}

// decodeFloat decodes an HDR texture; src holds at least c.dataSize()
// bytes.
func decodeFloat(c *Config, src []byte) *raster.Float {
	bands := c.bands()
	out := &raster.Float{Width: c.Width, Height: c.Height, Bands: bands, Data: make([]float32, c.Width*c.Height*bands)}
	if c.blockBytes > 0 {
		var px [16][3]float32
		decodeBlocks(c, src, func(block []byte, x0, y0 int) {
			decodeBC6H(block, &px, c.kind == kindBC6HS)
			for j := range 4 {
				y := y0 + j
				if y >= c.Height {
					return
				}
				for i := range 4 {
					x := x0 + i
					if x >= c.Width {
						break
					}
					copy(out.Data[(y*c.Width+x)*3:], px[j*4+i][:])
				}
			}
		})
		return out
	}
	var p [4]float32
	for y := range c.Height {
		row := src[y*c.Width*c.pixelBytes:]
		dst := out.Data[y*c.Width*bands:]
		for x := range c.Width {
			pixelFloat(c.kind, row[x*c.pixelBytes:], &p)
			copy(dst[x*bands:x*bands+bands], p[:bands])
		}
	}
	return out
}

// pixelFloat decodes one uncompressed float pixel to RGBA.
func pixelFloat(k kind, s []byte, p *[4]float32) {
	le := binary.LittleEndian
	f32 := func(i int) float32 { return float32frombits(le.Uint32(s[4*i:])) }
	f16 := func(i int) float32 { return halfToFloat(le.Uint16(s[2*i:])) }
	switch k {
	case kindRGBA16F:
		*p = [4]float32{f16(0), f16(1), f16(2), f16(3)}
	case kindRGBA32F:
		*p = [4]float32{f32(0), f32(1), f32(2), f32(3)}
	case kindRGB32F:
		*p = [4]float32{f32(0), f32(1), f32(2), 1}
	case kindR16F:
		v := f16(0)
		*p = [4]float32{v, v, v, 1}
	default: // kindR32F
		v := f32(0)
		*p = [4]float32{v, v, v, 1}
	}
}

// printable quotes a FourCC, replacing non-printable bytes.
func printable(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c < 0x20 || c > 0x7E {
			b[i] = '?'
		}
	}
	return `"` + string(b) + `"`
}
