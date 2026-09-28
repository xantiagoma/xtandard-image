package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"os"
)

// ---- KTX2 (WP-108) ----------------------------------------------------------
//
// Minimal KTX 2.0 writer for the Go-route fixtures of internal/codec/ktx2:
// one level, one layer, one face, the block-compressed payloads of the DDS
// fixtures' encoders, and uncompressed RGBA8/RGB8 (raw, zlib, or a level
// file compressed by the zstd CLI). The Basis Universal fixtures are made
// by the basisu tool (gen.sh).

// Vulkan formats written.
const (
	vkR8G8B8UNorm   = 23
	vkR8G8B8A8SRGB  = 43
	vkBC1RGBUNorm   = 131
	vkBC4UNorm      = 139
	vkBC6HUFloat    = 143
	vkBC7SRGB       = 146
	ktx2SchemeNone  = 0
	ktx2SchemeZstd  = 2
	ktx2SchemeZlib  = 3
	ktx2HeaderBytes = 80 + 24 // identifier, header, index, one level entry
)

// writeKTX2 writes a KTX 2.0 file with one level: level is the stored (maybe
// supercompressed) data, raw its uncompressed length. The DFD is the
// minimal 4-byte one (dfdTotalSize only); readers of the tested formats
// take the format from vkFormat.
func writeKTX2(out string, w, h int, vk, scheme uint32, level []byte, raw int) error {
	le := binary.LittleEndian
	var b bytes.Buffer
	b.WriteString("\xabKTX 20\xbb\r\n\x1a\n")
	u32 := func(v uint32) { _ = binary.Write(&b, le, v) }
	u64 := func(v uint64) { _ = binary.Write(&b, le, v) }
	typeSize := uint32(1)
	u32(vk)
	u32(typeSize)
	u32(uint32(w)) //nolint:gosec // fixture sizes
	u32(uint32(h)) //nolint:gosec // fixture sizes
	u32(0)         // depth
	u32(0)         // layers
	u32(1)         // faces
	u32(1)         // levels
	u32(scheme)
	dfdOff := uint32(ktx2HeaderBytes)
	u32(dfdOff)
	u32(4) // dfdByteLength
	u32(0) // kvd
	u32(0)
	u64(0) // sgd
	u64(0)
	levelOff := uint64(dfdOff) + 4
	levelOff = (levelOff + 15) &^ 15 // align the level
	u64(levelOff)
	u64(uint64(len(level)))
	u64(uint64(raw)) //nolint:gosec // fixture sizes
	u32(4) // DFD: dfdTotalSize
	for uint64(b.Len()) < levelOff {
		b.WriteByte(0)
	}
	b.Write(level)
	return os.WriteFile(out, b.Bytes(), 0o644)
}

func rgbaPixels(img image.Image) *image.NRGBA {
	n := image.NewNRGBA(img.Bounds())
	draw.Draw(n, n.Bounds(), img, img.Bounds().Min, draw.Src)
	return n
}

// ktx2Fixture writes kind at w×h to out. "ktx2-rgba8-level" writes the raw
// RGBA8 level (for the zstd CLI) and "ktx2-rgba8-zstd" wraps that
// compressed level (in) into a KTX2 file.
func ktx2Fixture(kind string, w, h int, out, in string) error {
	switch kind {
	case "ktx2-bc1":
		return writeKTX2(out, w, h, vkBC1RGBUNorm, ktx2SchemeNone, encodeBC1(photo(w, h)), 0)
	case "ktx2-bc4":
		return writeKTX2(out, w, h, vkBC4UNorm, ktx2SchemeNone, encodeBC4(photo(w, h)), 0)
	case "ktx2-bc7":
		return writeKTX2(out, w, h, vkBC7SRGB, ktx2SchemeNone, encodeBC7(withAlpha(photo(w, h))), 0)
	case "ktx2-bc6h":
		return writeKTX2(out, w, h, vkBC6HUFloat, ktx2SchemeNone, encodeBC6H(hdrImage(w, h, 3), w, h, 3), 0)
	case "ktx2-rgba8":
		return writeKTX2(out, w, h, vkR8G8B8A8SRGB, ktx2SchemeNone, rgbaPixels(withAlpha(photo(w, h))).Pix, 0)
	case "ktx2-rgb8":
		px := rgbaPixels(photo(w, h)).Pix
		rgb := make([]byte, 0, w*h*3)
		for i := 0; i+3 < len(px); i += 4 {
			rgb = append(rgb, px[i], px[i+1], px[i+2])
		}
		return writeKTX2(out, w, h, vkR8G8B8UNorm, ktx2SchemeNone, rgb, 0)
	case "ktx2-rgba8-zlib":
		px := rgbaPixels(withAlpha(photo(w, h))).Pix
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		if _, err := zw.Write(px); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		return writeKTX2(out, w, h, vkR8G8B8A8SRGB, ktx2SchemeZlib, z.Bytes(), len(px))
	case "ktx2-rgba8-level":
		return os.WriteFile(out, rgbaPixels(withAlpha(photo(w, h))).Pix, 0o644)
	case "ktx2-rgba8-zstd":
		level, err := os.ReadFile(in) //nolint:gosec // generator input
		if err != nil {
			return err
		}
		return writeKTX2(out, w, h, vkR8G8B8A8SRGB, ktx2SchemeZstd, level, w*h*4)
	default:
		return fmt.Errorf("unknown KTX2 fixture %q", kind)
	}
}
