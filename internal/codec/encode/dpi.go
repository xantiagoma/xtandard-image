package encode

import (
	"context"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// mmPerInch converts DPI to the pixels per millimetre libvips stores.
const mmPerInch = 25.4

// setResolution replaces the resolution of img with dpi (imgproxy Pro
// `dpi`) before the save: PNG writes it as pHYs, TIFF as its resolution
// tags, JPEG into the Exif block when metadata is kept (and, whatever the
// metadata policy, into a JFIF header: jpegDensity). WebP, AVIF, HEIC,
// JPEG XL and GIF have no resolution field. Resolution is header data, not
// metadata: strip_metadata does not remove it. dpi ≤ 0 keeps the image's.
func setResolution(ctx context.Context, a *vips.Arena, img *vips.Image, dpi int) (*vips.Image, error) {
	if dpi <= 0 {
		return img, nil
	}
	res := float64(dpi) / mmPerInch
	return a.Add(img.Copy(ctx, vips.CopyOptions{XRes: res, YRes: res}))
}

// jpegDensity makes a JPEG libvips saved carry dpi. libvips built with
// libexif writes the resolution into the Exif block only (vips2jpeg.c:
// "only write the JFIF headers if we have no EXIF"), so a JPEG whose
// metadata was stripped has none at all. When the file has an Exif APP1 the
// resolution is already there; otherwise a JFIF APP0 segment with the
// density in dots per inch is inserted after SOI (18 bytes; the body is
// copied once, only for dpi requests). A JFIF APP0 already present is
// patched in place.
func jpegDensity(body Body, dpi int) Body {
	b := body.Bytes()
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return body
	}
	d := uint16(min(max(dpi, 0), 0xFFFF))
	if len(b) >= 18 && b[2] == 0xFF && b[3] == 0xE0 && string(b[6:11]) == "JFIF\x00" {
		setJFIFDensity(b[2:], d)
		return body
	}
	if hasEXIF(b) {
		return body
	}
	app0 := []byte{0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 0, 0, 0, 0, 0}
	setJFIFDensity(app0, d)
	out := make([]byte, 0, len(b)+len(app0))
	out = append(out, b[:2]...)
	out = append(out, app0...)
	out = append(out, b[2:]...)
	body.Release()
	return &goBody{b: out}
}

// setJFIFDensity sets the units (dots per inch) and densities of the JFIF
// APP0 segment starting at seg (its marker).
func setJFIFDensity(seg []byte, d uint16) {
	// marker (2), length (2), "JFIF\0" (5), version (2), units, Xdensity,
	// Ydensity.
	seg[11] = 1
	binary.BigEndian.PutUint16(seg[12:], d)
	binary.BigEndian.PutUint16(seg[14:], d)
}

// hasEXIF reports an Exif APP1 segment among the JPEG header segments.
func hasEXIF(b []byte) bool {
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF || b[i+1] == 0xDA || b[i+1] == 0xD9 {
			return false
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if b[i+1] == 0xE1 && i+10 <= len(b) && string(b[i+4:i+10]) == "Exif\x00\x00" {
			return true
		}
		if n < 2 {
			return false
		}
		i += 2 + n
	}
	return false
}
