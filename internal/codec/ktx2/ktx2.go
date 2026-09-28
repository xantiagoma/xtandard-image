// Package ktx2 reads KTX 2.0 textures (Khronos, https://registry.khronos.org/KTX/specs/2.0/ktxspec.v2.html):
// the first image, which is mipmap level 0 of the first array layer, the
// first (+X) face of a cube map or the first slice of a volume (WP-108).
//
// The container is parsed here, in pure Go. The payload is decoded by one
// of two routes:
//
//   - block-compressed (BC1-BC7, including BC6H) and uncompressed Vulkan
//     formats are decoded in Go by codec/dds, from a DirectDraw Surface
//     header written in front of the level's first image (the DXGI format
//     equivalent to the Vulkan one); a level supercompressed with zlib is
//     inflated in Go, one supercompressed with Zstandard through
//     Backend.Zstd;
//   - Basis Universal payloads (ETC1S/BasisLZ, UASTC, and the ASTC, XUASTC
//     and XUBC7 formats of Basis Universal 2.x, which a KTX2 file declares
//     with VK_FORMAT_UNDEFINED, an ASTC format or a Basis supercompression
//     scheme) go to Backend.Basis, the Basis Universal transcoder.
//
// ETC2/EAC, PVRTC and the remaining Vulkan formats are refused
// (errs.KindUnsupported). Everything is bounded: the header and level index
// are validated before anything is allocated, level 0 must lie inside the
// file, its decompressed size must be exactly what the format needs, and
// Options.MaxPixels caps the image.
package ktx2

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/codec/dds"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Identifier is the 12-byte KTX 2.0 file identifier: «KTX 20»\r\n\x1A\n.
const Identifier = "\xabKTX 20\xbb\r\n\x1a\n"

const (
	headerSize     = 80 // identifier, header and index
	levelEntrySize = 24
	// maxSide bounds each dimension (Vulkan's largest 2D image is 16384;
	// larger values are corrupt or hostile).
	maxSide = 1 << 16
	// maxLevels bounds the level index (log2(maxSide) + 1 is 17).
	maxLevels = 32
)

// Supercompression is the header's supercompressionScheme.
type Supercompression uint32

// Supercompression schemes (KTX 2.0 §3.1.2; 4 and up are Basis Universal
// 2.x's own).
const (
	SupercompressionNone    Supercompression = 0
	SupercompressionBasisLZ Supercompression = 1
	SupercompressionZstd    Supercompression = 2
	SupercompressionZlib    Supercompression = 3
)

// Config is what the header declares about the first image.
type Config struct {
	Width, Height int
	// VkFormat is the header's Vulkan format (0, VK_FORMAT_UNDEFINED, for
	// Basis Universal payloads).
	VkFormat uint32
	// Format names the payload ("BC7", "R8G8B8A8", "Basis Universal", ...),
	// for diagnostics.
	Format string
	Scheme Supercompression
	// Basis reports a payload that only the Basis Universal transcoder
	// decodes (Backend.Basis).
	Basis bool
	// Alpha, Grey and HDR describe what Decode returns for the Go route
	// (as codec/dds does); for a Basis payload they come from the
	// transcoder's header read (Backend.BasisInfo) when DecodeConfig is
	// given a Backend.
	Alpha, Grey, HDR bool
	// Levels, Layers, Faces and Depth are the header's counts (Layers and
	// Depth are 0 for a plain 2D texture).
	Levels, Layers, Faces, Depth int

	dxgi      uint32 // DXGI format of the Go route
	opaque    bool   // DDS alpha mode opaque: drop the alpha band
	expand    int    // 3-byte RGB/BGR formats: 1 RGB, 2 BGR (expanded to RGBA8)
	imageSize int64  // bytes of the first image (Go route)
	level0    levelIndex
}

type levelIndex struct {
	offset, length, uncompressed uint64
}

// Backend is what the caller provides for the parts that need C (codec
// binds it to internal/vips): the Basis Universal transcoder and Zstandard.
type Backend interface {
	// BasisInfo reads the header of a KTX2 file with a Basis payload.
	BasisInfo(ctx context.Context, data []byte) (BasisInfo, error)
	// Basis transcodes the first image of a KTX2 file with a Basis payload.
	// maxPixels bounds it before anything is decoded.
	Basis(ctx context.Context, data []byte, maxPixels int64) (BasisImage, error)
	// Zstd decompresses src into dst, which is exactly the expected size.
	Zstd(ctx context.Context, dst, src []byte) error
}

// BasisInfo is the transcoder's view of a Basis payload.
type BasisInfo struct {
	Width, Height int
	Alpha, HDR    bool
}

// BasisImage is a transcoded image: straight RGBA, 8-bit (RGBA8) or linear
// half floats (RGBAHalf, when HDR).
type BasisImage struct {
	BasisInfo
	RGBA8    []byte
	RGBAHalf []uint16
}

// Options control Decode.
type Options struct {
	// MaxPixels bounds Width*Height (errs.KindLimitExceeded). Zero means
	// raster.DefaultMaxPixels.
	MaxPixels int64
	// Backend decodes Basis payloads and Zstandard levels; without one they
	// fail with errs.KindUnsupported.
	Backend Backend
}

func corrupt(what string) error { return errs.New(errs.KindCorrupt, "ktx2: "+what) }

func unsupported(what string) error {
	return errs.WithPublic(errs.Newf(errs.KindUnsupported, "ktx2: %s is not supported", what),
		"this KTX2 texture format is not supported")
}

// Sniff reports whether head starts with the KTX 2.0 identifier.
func Sniff(head []byte) bool {
	return len(head) >= len(Identifier) && string(head[:len(Identifier)]) == Identifier
}

// DecodeConfig parses the header and level index of b. A Basis payload's
// Alpha and HDR come from be.BasisInfo when be is not nil. Unknown formats
// fail with errs.KindUnsupported, malformed headers with errs.KindCorrupt.
func DecodeConfig(ctx context.Context, b []byte, be Backend) (Config, error) {
	c, err := parse(b)
	if err != nil {
		return Config{}, err
	}
	if c.Basis && be != nil {
		info, err := be.BasisInfo(ctx, b)
		if err != nil {
			return Config{}, err
		}
		if info.Width != c.Width || info.Height != c.Height {
			return Config{}, corrupt("transcoder and header disagree on the size")
		}
		c.Alpha, c.HDR = info.Alpha, info.HDR
	}
	return c, nil
}

func parse(b []byte) (Config, error) {
	if !Sniff(b) {
		return Config{}, corrupt("bad identifier")
	}
	if len(b) < headerSize+levelEntrySize {
		return Config{}, corrupt("truncated header")
	}
	le := binary.LittleEndian
	c := Config{
		VkFormat: le.Uint32(b[12:]),
		Scheme:   Supercompression(le.Uint32(b[44:])),
	}
	typeSize := le.Uint32(b[16:])
	w, h, d := le.Uint32(b[20:]), le.Uint32(b[24:]), le.Uint32(b[28:])
	layers, faces, levels := le.Uint32(b[32:]), le.Uint32(b[36:]), le.Uint32(b[40:])
	switch {
	case w == 0 || w > maxSide || h > maxSide || d > maxSide:
		return Config{}, errs.Newf(errs.KindCorrupt, "ktx2: %dx%dx%d", w, h, d)
	case faces != 1 && faces != 6:
		return Config{}, corrupt("face count " + strconv.FormatUint(uint64(faces), 10))
	case levels > maxLevels:
		return Config{}, corrupt("level count " + strconv.FormatUint(uint64(levels), 10))
	case layers > maxSide:
		return Config{}, corrupt("layer count " + strconv.FormatUint(uint64(layers), 10))
	case typeSize > 8:
		return Config{}, corrupt("type size " + strconv.FormatUint(uint64(typeSize), 10))
	}
	if h == 0 {
		h = 1 // a 1D texture
	}
	c.Width, c.Height = int(w), int(h)
	c.Levels, c.Layers, c.Faces, c.Depth = int(levels), int(layers), int(faces), int(d)
	n := max(levels, 1)
	if int64(len(b)) < headerSize+int64(n)*levelEntrySize {
		return Config{}, corrupt("truncated level index")
	}
	c.level0 = levelIndex{
		offset:       le.Uint64(b[headerSize:]),
		length:       le.Uint64(b[headerSize+8:]),
		uncompressed: le.Uint64(b[headerSize+16:]),
	}
	if c.level0.length == 0 || c.level0.offset > uint64(len(b)) ||
		c.level0.length > uint64(len(b))-c.level0.offset {
		return Config{}, corrupt("level 0 lies outside the file")
	}
	if isBasis(c.VkFormat, c.Scheme) {
		c.Basis, c.Format = true, basisName(c.VkFormat)
		return c, nil
	}
	switch c.Scheme {
	case SupercompressionNone, SupercompressionZstd, SupercompressionZlib:
	default:
		return Config{}, corrupt("supercompression scheme " + strconv.FormatUint(uint64(c.Scheme), 10))
	}
	if err := c.setFormat(); err != nil {
		return Config{}, err
	}
	c.imageSize = c.firstImageSize()
	level := c.level0.length
	if c.Scheme != SupercompressionNone {
		level = c.level0.uncompressed
	}
	if uint64(c.imageSize) > level {
		return Config{}, corrupt("level 0 is shorter than its first image")
	}
	return c, nil
}

// Vulkan formats (VkFormat) read here.
const (
	vkUndefined             = 0
	vkR5G6B5UNormPack16     = 4
	vkA1R5G5B5UNormPack16   = 8
	vkR8UNorm               = 9
	vkR8SRGB                = 15
	vkR8G8UNorm             = 16
	vkR8G8SRGB              = 22
	vkR8G8B8UNorm           = 23
	vkR8G8B8SRGB            = 29
	vkB8G8R8UNorm           = 30
	vkB8G8R8SRGB            = 36
	vkR8G8B8A8UNorm         = 37
	vkR8G8B8A8SRGB          = 43
	vkB8G8R8A8UNorm         = 44
	vkB8G8R8A8SRGB          = 50
	vkA2B10G10R10UNormPack  = 64
	vkR16SFloat             = 76
	vkR16G16B16A16UNorm     = 91
	vkR16G16B16A16SFloat    = 97
	vkR32SFloat             = 100
	vkR32G32B32SFloat       = 106
	vkR32G32B32A32SFloat    = 109
	vkBC1RGBUNorm           = 131
	vkBC1RGBSRGB            = 132
	vkBC1RGBAUNorm          = 133
	vkBC1RGBASRGB           = 134
	vkBC2UNorm              = 135
	vkBC2SRGB               = 136
	vkBC3UNorm              = 137
	vkBC3SRGB               = 138
	vkBC4UNorm              = 139
	vkBC4SNorm              = 140
	vkBC5UNorm              = 141
	vkBC5SNorm              = 142
	vkBC6HUFloat            = 143
	vkBC6HSFloat            = 144
	vkBC7UNorm              = 145
	vkBC7SRGB               = 146
	vkETC2First             = 147 // ETC2 and EAC: 147-156
	vkETC2Last              = 156
	vkASTCFirst             = 157 // ASTC LDR 4x4 UNORM .. 12x12 SRGB
	vkASTCLast              = 184
	vkASTCSFloatFirst       = 1000066000 // ASTC HDR (VK_EXT_texture_compression_astc_hdr)
	vkASTCSFloatLast        = 1000066013
	vkPVRTCFirst            = 1000054000
	vkPVRTCLast             = 1000054007
	basisSchemeFirstPrivate = 4
)

// isBasis reports the payloads only the Basis Universal transcoder reads:
// VK_FORMAT_UNDEFINED (ETC1S, UASTC), ASTC, and the BasisLZ and Basis 2.x
// supercompression schemes.
func isBasis(vk uint32, s Supercompression) bool {
	return vk == vkUndefined || s == SupercompressionBasisLZ || s >= basisSchemeFirstPrivate ||
		(vk >= vkASTCFirst && vk <= vkASTCLast) || (vk >= vkASTCSFloatFirst && vk <= vkASTCSFloatLast)
}

func basisName(vk uint32) string {
	if vk == vkUndefined {
		return "Basis Universal"
	}
	return "ASTC"
}

// DXGI_FORMAT values of the DDS header written for the Go route.
const (
	dxgiR32G32B32A32Float = 2
	dxgiR32G32B32Float    = 6
	dxgiR16G16B16A16Float = 10
	dxgiR16G16B16A16UNorm = 11
	dxgiR10G10B10A2UNorm  = 24
	dxgiR8G8B8A8UNorm     = 28
	dxgiR8G8B8A8UNormSRGB = 29
	dxgiR32Float          = 41
	dxgiR8G8UNorm         = 49
	dxgiR16Float          = 54
	dxgiR8UNorm           = 61
	dxgiBC1UNorm          = 71
	dxgiBC1UNormSRGB      = 72
	dxgiBC2UNorm          = 74
	dxgiBC2UNormSRGB      = 75
	dxgiBC3UNorm          = 77
	dxgiBC3UNormSRGB      = 78
	dxgiBC4UNorm          = 80
	dxgiBC4SNorm          = 81
	dxgiBC5UNorm          = 83
	dxgiBC5SNorm          = 84
	dxgiB5G6R5UNorm       = 85
	dxgiB5G5R5A1UNorm     = 86
	dxgiB8G8R8A8UNorm     = 87
	dxgiB8G8R8A8UNormSRGB = 91
	dxgiBC6HUF16          = 95
	dxgiBC6HSF16          = 96
	dxgiBC7UNorm          = 98
	dxgiBC7UNormSRGB      = 99
)

// vkToDXGI maps the Vulkan formats of the Go route: the DXGI format, its
// name, and whether its alpha is dropped (BC1 without alpha).
func vkToDXGI(vk uint32) (dxgi uint32, name string, opaque bool, ok bool) {
	switch vk {
	case vkR5G6B5UNormPack16:
		return dxgiB5G6R5UNorm, "R5G6B5", true, true
	case vkA1R5G5B5UNormPack16:
		return dxgiB5G5R5A1UNorm, "A1R5G5B5", false, true
	case vkR8UNorm, vkR8SRGB:
		return dxgiR8UNorm, "R8", true, true
	case vkR8G8UNorm, vkR8G8SRGB:
		return dxgiR8G8UNorm, "R8G8", true, true
	case vkR8G8B8A8UNorm:
		return dxgiR8G8B8A8UNorm, "R8G8B8A8", false, true
	case vkR8G8B8A8SRGB:
		return dxgiR8G8B8A8UNormSRGB, "R8G8B8A8", false, true
	case vkB8G8R8A8UNorm:
		return dxgiB8G8R8A8UNorm, "B8G8R8A8", false, true
	case vkB8G8R8A8SRGB:
		return dxgiB8G8R8A8UNormSRGB, "B8G8R8A8", false, true
	case vkA2B10G10R10UNormPack:
		return dxgiR10G10B10A2UNorm, "A2B10G10R10", false, true
	case vkR16SFloat:
		return dxgiR16Float, "R16_SFLOAT", true, true
	case vkR16G16B16A16UNorm:
		return dxgiR16G16B16A16UNorm, "R16G16B16A16", false, true
	case vkR16G16B16A16SFloat:
		return dxgiR16G16B16A16Float, "R16G16B16A16_SFLOAT", false, true
	case vkR32SFloat:
		return dxgiR32Float, "R32_SFLOAT", true, true
	case vkR32G32B32SFloat:
		return dxgiR32G32B32Float, "R32G32B32_SFLOAT", true, true
	case vkR32G32B32A32SFloat:
		return dxgiR32G32B32A32Float, "R32G32B32A32_SFLOAT", false, true
	case vkBC1RGBUNorm:
		return dxgiBC1UNorm, "BC1", true, true
	case vkBC1RGBSRGB:
		return dxgiBC1UNormSRGB, "BC1", true, true
	case vkBC1RGBAUNorm:
		return dxgiBC1UNorm, "BC1", false, true
	case vkBC1RGBASRGB:
		return dxgiBC1UNormSRGB, "BC1", false, true
	case vkBC2UNorm:
		return dxgiBC2UNorm, "BC2", false, true
	case vkBC2SRGB:
		return dxgiBC2UNormSRGB, "BC2", false, true
	case vkBC3UNorm:
		return dxgiBC3UNorm, "BC3", false, true
	case vkBC3SRGB:
		return dxgiBC3UNormSRGB, "BC3", false, true
	case vkBC4UNorm:
		return dxgiBC4UNorm, "BC4", true, true
	case vkBC4SNorm:
		return dxgiBC4SNorm, "BC4_SNORM", true, true
	case vkBC5UNorm:
		return dxgiBC5UNorm, "BC5", true, true
	case vkBC5SNorm:
		return dxgiBC5SNorm, "BC5_SNORM", true, true
	case vkBC6HUFloat:
		return dxgiBC6HUF16, "BC6H_UFLOAT", true, true
	case vkBC6HSFloat:
		return dxgiBC6HSF16, "BC6H_SFLOAT", true, true
	case vkBC7UNorm:
		return dxgiBC7UNorm, "BC7", false, true
	case vkBC7SRGB:
		return dxgiBC7UNormSRGB, "BC7", false, true
	default:
		return 0, "", false, false
	}
}

// setFormat resolves the Go route's format and what Decode returns.
func (c *Config) setFormat() error {
	switch {
	case c.VkFormat >= vkETC2First && c.VkFormat <= vkETC2Last:
		return unsupported("ETC2/EAC (VkFormat " + strconv.FormatUint(uint64(c.VkFormat), 10) + ")")
	case c.VkFormat >= vkPVRTCFirst && c.VkFormat <= vkPVRTCLast:
		return unsupported("PVRTC (VkFormat " + strconv.FormatUint(uint64(c.VkFormat), 10) + ")")
	}
	switch c.VkFormat {
	case vkR8G8B8UNorm, vkR8G8B8SRGB:
		c.expand, c.dxgi, c.opaque, c.Format = 1, dxgiR8G8B8A8UNorm, true, "R8G8B8"
	case vkB8G8R8UNorm, vkB8G8R8SRGB:
		c.expand, c.dxgi, c.opaque, c.Format = 2, dxgiR8G8B8A8UNorm, true, "B8G8R8"
	default:
		dxgi, name, opaque, ok := vkToDXGI(c.VkFormat)
		if !ok {
			return unsupported("VkFormat " + strconv.FormatUint(uint64(c.VkFormat), 10))
		}
		c.dxgi, c.Format, c.opaque = dxgi, name, opaque
	}
	// What codec/dds reports for that DXGI format.
	dc, err := dds.DecodeConfig(ddsHeader(c.Width, c.Height, c.dxgi, c.opaque))
	if err != nil {
		return err
	}
	c.Alpha, c.Grey, c.HDR = dc.Alpha, dc.Grey, dc.HDR
	return nil
}

// firstImageSize is the byte size of one image of level 0 in the stored
// format (tightly packed rows, KTX 2.0 §3.9.6).
func (c *Config) firstImageSize() int64 {
	w, h := int64(c.Width), int64(c.Height)
	switch c.dxgi {
	case dxgiBC1UNorm, dxgiBC1UNormSRGB, dxgiBC4UNorm, dxgiBC4SNorm:
		return (w + 3) / 4 * ((h + 3) / 4) * 8
	case dxgiBC2UNorm, dxgiBC2UNormSRGB, dxgiBC3UNorm, dxgiBC3UNormSRGB, dxgiBC5UNorm, dxgiBC5SNorm,
		dxgiBC6HUF16, dxgiBC6HSF16, dxgiBC7UNorm, dxgiBC7UNormSRGB:
		return (w + 3) / 4 * ((h + 3) / 4) * 16
	}
	if c.expand != 0 {
		return w * h * 3
	}
	return w * h * int64(bytesPerPixel(c.dxgi))
}

func bytesPerPixel(dxgi uint32) int {
	switch dxgi {
	case dxgiR8UNorm:
		return 1
	case dxgiR8G8UNorm, dxgiR16Float, dxgiB5G6R5UNorm, dxgiB5G5R5A1UNorm:
		return 2
	case dxgiR16G16B16A16UNorm, dxgiR16G16B16A16Float:
		return 8
	case dxgiR32G32B32Float:
		return 12
	case dxgiR32G32B32A32Float:
		return 16
	default: // 8-bit RGBA/BGRA, R10G10B10A2, R32F
		return 4
	}
}

// DDS header constants (codec/dds reads the same layout).
const (
	ddsHeaderSize   = 124
	ddsDX10Size     = 20
	ddsFlags        = 0x1 | 0x2 | 0x4 | 0x1000 // CAPS, HEIGHT, WIDTH, PIXELFORMAT
	ddsPFFourCC     = 0x4
	ddsCapsTexture  = 0x1000
	ddsTexture2D    = 3
	ddsAlphaOpaque  = 3
	ddsAlphaDefault = 0
)

// ddsHeader is "DDS ", a DDS_HEADER and a DDS_HEADER_DXT10 describing one
// width×height image of the DXGI format.
func ddsHeader(w, h int, dxgi uint32, opaque bool) []byte {
	le := binary.LittleEndian
	b := make([]byte, 4+ddsHeaderSize+ddsDX10Size)
	copy(b, "DDS ")
	le.PutUint32(b[4:], ddsHeaderSize)
	le.PutUint32(b[8:], ddsFlags)
	le.PutUint32(b[12:], uint32(h)) //nolint:gosec // bounded by maxSide
	le.PutUint32(b[16:], uint32(w)) //nolint:gosec // bounded by maxSide
	le.PutUint32(b[28:], 1)         // mipmap count
	le.PutUint32(b[76:], 32)        // DDS_PIXELFORMAT size
	le.PutUint32(b[80:], ddsPFFourCC)
	copy(b[84:], "DX10")
	le.PutUint32(b[108:], ddsCapsTexture)
	le.PutUint32(b[128:], dxgi)
	le.PutUint32(b[132:], ddsTexture2D)
	le.PutUint32(b[140:], 1) // array size
	mode := uint32(ddsAlphaDefault)
	if opaque {
		mode = ddsAlphaOpaque
	}
	le.PutUint32(b[144:], mode)
	return b
}

// Decode decodes the first image of b: raster.Raw (8-bit RGB or RGBA) or,
// for HDR payloads (BC6H, float formats, UASTC HDR, ASTC HDR), linear
// raster.Float, which the pipeline tone-maps like OpenEXR.
func Decode(ctx context.Context, b []byte, o Options) (raster.Decoded, error) {
	c, err := parse(b)
	if err != nil {
		return raster.Decoded{}, err
	}
	maxPixels := o.MaxPixels
	if maxPixels <= 0 {
		maxPixels = raster.DefaultMaxPixels
	}
	if int64(c.Width)*int64(c.Height) > maxPixels {
		return raster.Decoded{}, errs.Newf(errs.KindLimitExceeded, "ktx2: %dx%d exceeds the pixel limit of %d",
			c.Width, c.Height, maxPixels)
	}
	if c.Basis {
		if o.Backend == nil {
			return raster.Decoded{}, unsupported("Basis Universal payload without a transcoder")
		}
		img, err := o.Backend.Basis(ctx, b, maxPixels)
		if err != nil {
			return raster.Decoded{}, err
		}
		return basisDecoded(&img, c.Width, c.Height)
	}
	level, err := c.level(ctx, b, o.Backend)
	if err != nil {
		return raster.Decoded{}, err
	}
	img := level[:c.imageSize]
	if c.expand != 0 {
		img = expandRGB(img, c.expand == 2)
	}
	d := append(ddsHeader(c.Width, c.Height, c.dxgi, c.opaque), img...)
	return dds.Decode(d, dds.Options{MaxPixels: maxPixels})
}

// level returns level 0's bytes, decompressed.
func (c *Config) level(ctx context.Context, b []byte, be Backend) ([]byte, error) {
	src := b[c.level0.offset : c.level0.offset+c.level0.length]
	switch c.Scheme {
	case SupercompressionNone:
		return src, nil
	case SupercompressionZstd, SupercompressionZlib:
	default:
		return nil, corrupt("supercompression scheme")
	}
	// The format fixes the level's size: one image per layer, face and
	// slice. A level declared larger, or more than 1024 times its compressed
	// size (a bomb), is corrupt; zlib inflates only the first image.
	n := c.level0.uncompressed
	images := uint64(max(c.Layers, 1)) * uint64(c.Faces) * uint64(max(c.Depth, 1))
	if n < uint64(c.imageSize) || n > images*uint64(c.imageSize) || n/1024 > c.level0.length {
		return nil, corrupt("uncompressed level size")
	}
	if c.Scheme == SupercompressionZlib {
		zr, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, errs.Wrapf(errs.KindCorrupt, err, "ktx2: zlib level")
		}
		dst := make([]byte, c.imageSize)
		if _, err := io.ReadFull(zr, dst); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return nil, corrupt("zlib level is shorter than its first image")
			}
			return nil, errs.Wrapf(errs.KindCorrupt, err, "ktx2: zlib level")
		}
		return dst, nil
	}
	if be == nil {
		return nil, unsupported("Zstandard supercompression without a decoder")
	}
	dst := make([]byte, n)
	if err := be.Zstd(ctx, dst, src); err != nil {
		return nil, err
	}
	return dst, nil
}

// expandRGB turns tightly packed 3-byte RGB (or BGR) pixels into RGBA8.
func expandRGB(src []byte, bgr bool) []byte {
	out := make([]byte, len(src)/3*4)
	for i, j := 0, 0; i+2 < len(src); i, j = i+3, j+4 {
		r, g, b := src[i], src[i+1], src[i+2]
		if bgr {
			r, b = b, r
		}
		out[j], out[j+1], out[j+2], out[j+3] = r, g, b, 255
	}
	return out
}

// basisDecoded turns the transcoder's RGBA into what the pipeline takes:
// 8-bit RGB(A), alpha kept only when the payload has it, or linear float
// RGB for HDR.
func basisDecoded(img *BasisImage, w, h int) (raster.Decoded, error) {
	if img.Width != w || img.Height != h {
		return raster.Decoded{}, corrupt("transcoded image size differs from the header")
	}
	px := w * h
	if img.HDR {
		if len(img.RGBAHalf) != px*4 {
			return raster.Decoded{}, corrupt("short transcoded image")
		}
		f := &raster.Float{Width: w, Height: h, Bands: 3, Data: make([]float32, px*3)}
		for i := range px {
			for k := range 3 {
				f.Data[i*3+k] = halfToFloat(img.RGBAHalf[i*4+k])
			}
		}
		return raster.Decoded{Float: f}, nil
	}
	if len(img.RGBA8) != px*4 {
		return raster.Decoded{}, corrupt("short transcoded image")
	}
	if img.Alpha {
		return raster.Decoded{Raw: &raster.Raw{Width: w, Height: h, Bands: 4, Data: img.RGBA8}}, nil
	}
	rgb := make([]byte, px*3)
	for i := range px {
		copy(rgb[i*3:i*3+3], img.RGBA8[i*4:i*4+3])
	}
	return raster.Decoded{Raw: &raster.Raw{Width: w, Height: h, Bands: 3, Data: rgb}}, nil
}

// halfToFloat converts IEEE 754 binary16 bits to float32.
func halfToFloat(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h >> 10 & 0x1F)
	man := uint32(h & 0x3FF)
	switch exp {
	case 0:
		if man == 0 {
			return math.Float32frombits(sign)
		}
		f := float32(man) / (1 << 24) // subnormal: man × 2^-24
		if sign != 0 {
			f = -f
		}
		return f
	case 0x1F:
		return math.Float32frombits(sign | 0xFF<<23 | man<<13)
	default:
		return math.Float32frombits(sign | (exp+127-15)<<23 | man<<13)
	}
}
