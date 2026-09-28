package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"math"

	"golang.org/x/image/draw"
)

// ---- synthetic DNG ------------------------------------------------------------
//
// writeDNG writes a minimal DNG 1.4 with the photo as a 16-bit RGGB Bayer
// mosaic (uncompressed, in a SubIFD) and a baseline JPEG preview of the
// same scene in IFD0, the layout cameras use. The "camera" is linear sRGB:
// ColorMatrix1 is the XYZ → linear sRGB matrix (D65) and AsShotNeutral is
// 1,1,1, so LibRaw's demosaic reproduces the photo's colours. Camera
// metadata (Make, Model, exposure, ISO, lens, date) lives in IFD0 and the
// Exif IFD like a camera writes it.
//
// w×h is the displayed size and pw×ph the stored size of the preview.
// orientation is the TIFF Orientation of the file (1 or 6); the pixels are
// stored so that applying it shows the photo upright (6: stored rotated 90°
// counter-clockwise, so the mosaic is h×w).
//
// compression is the mosaic's TIFF Compression tag: 1 (uncompressed) for a
// decodable file. Any other value labels the same uncompressed bytes, which
// makes a DNG LibRaw can read but not demosaic: 52546 (JPEG XL, a
// placeholder decoder without the Adobe DNG SDK) exercises the preview-only
// path (FORMATS.md §2).
func writeDNG(path string, w, h, pw, ph int, orientation, compression uint16) error {
	disp := photo(w, h)
	stored := disp
	if orientation == 6 {
		stored = rotCCW(disp)
	}
	sw, sh := stored.Bounds().Dx(), stored.Bounds().Dy()

	// The preview: the stored image scaled to pw×ph, JPEG q85.
	pv := image.NewRGBA(image.Rect(0, 0, pw, ph))
	draw.CatmullRom.Scale(pv, pv.Bounds(), stored, stored.Bounds(), draw.Src, nil)
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, pv, &jpeg.Options{Quality: 85}); err != nil {
		return err
	}

	// The mosaic: RGGB, linear light, 16 bits.
	cfa := make([]byte, 0, sw*sh*2)
	for y := range sh {
		for x := range sw {
			c := stored.RGBAAt(x, y)
			var v uint8
			switch {
			case y%2 == 0 && x%2 == 0:
				v = c.R
			case y%2 == 1 && x%2 == 1:
				v = c.B
			default:
				v = c.G
			}
			cfa = binary.LittleEndian.AppendUint16(cfa, uint16(math.Round(srgbToLinear(v)*60000)))
		}
	}

	srats := func(tag uint16, v ...float64) ifdEntry {
		b := make([]byte, 0, len(v)*8)
		for _, x := range v {
			b = binary.LittleEndian.AppendUint32(b, uint32(int32(math.Round(x*10000))))
			b = binary.LittleEndian.AppendUint32(b, 10000)
		}
		return ifdEntry{tag: tag, typ: 10, count: uint32(len(v)), data: b, ifd: -1}
	}
	long := func(tag uint16, v uint32) ifdEntry {
		return ifdEntry{tag: tag, typ: 4, count: 1, data: binary.LittleEndian.AppendUint32(nil, v), ifd: -1}
	}
	shorts := func(tag uint16, v ...uint16) ifdEntry {
		b := make([]byte, 0, len(v)*2)
		for _, x := range v {
			b = binary.LittleEndian.AppendUint16(b, x)
		}
		return ifdEntry{tag: tag, typ: 3, count: uint32(len(v)), data: b, ifd: -1}
	}
	bytesEntry := func(tag uint16, v ...byte) ifdEntry {
		return ifdEntry{tag: tag, typ: 1, count: uint32(len(v)), data: v, ifd: -1}
	}
	blobRef := func(tag uint16, blob int) ifdEntry { // offset of a blob, patched in
		return ifdEntry{tag: tag, typ: 4, count: 1, ifd: -1, blob: blob}
	}
	ratio := func(tag uint16, num, den uint32) ifdEntry {
		b := binary.LittleEndian.AppendUint32(nil, num)
		return ifdEntry{tag: tag, typ: 5, count: 1, data: binary.LittleEndian.AppendUint32(b, den), ifd: -1}
	}

	const (
		blobJPEG = 1
		blobCFA  = 2
	)
	ifds := [][]ifdEntry{
		{ // IFD0: the preview and the DNG/camera tags (sorted by tag)
			long(0x00FE, 1),
			long(0x0100, uint32(pw)),
			long(0x0101, uint32(ph)),
			shorts(0x0102, 8, 8, 8),
			shorts(0x0103, 7),
			shorts(0x0106, 6),
			asciiEntry(0x010F, "xtandard"),
			asciiEntry(0x0110, "Synthetic DNG"),
			blobRef(0x0111, blobJPEG),
			shorts(0x0112, orientation),
			shorts(0x0115, 3),
			long(0x0116, uint32(ph)),
			long(0x0117, uint32(jb.Len())),
			asciiEntry(0x0131, "testdata/gen"),
			{tag: 0x014A, typ: 4, count: 1, ifd: 1},
			{tag: 0x8769, typ: 4, count: 1, ifd: 2},
			bytesEntry(0xC612, 1, 4, 0, 0),
			bytesEntry(0xC613, 1, 1, 0, 0),
			asciiEntry(0xC614, "xtandard Synthetic DNG"),
			srats(0xC621,
				3.2404542, -1.5371385, -0.4985314,
				-0.9692660, 1.8760108, 0.0415560,
				0.0556434, -0.2040259, 1.0572252),
			ratio(0xC628, 1, 1),
			shorts(0xC65A, 21),
		},
		{ // SubIFD: the raw mosaic
			long(0x00FE, 0),
			long(0x0100, uint32(sw)),
			long(0x0101, uint32(sh)),
			shorts(0x0102, 16),
			shorts(0x0103, compression),
			shorts(0x0106, 32803),
			blobRef(0x0111, blobCFA),
			shorts(0x0115, 1),
			long(0x0116, uint32(sh)),
			long(0x0117, uint32(len(cfa))),
			shorts(0x011C, 1),
			shorts(0x828D, 2, 2),
			bytesEntry(0x828E, 0, 1, 1, 2),
			bytesEntry(0xC616, 0, 1, 2), // CFAPlaneColor
			long(0xC61D, 65535),         // WhiteLevel
		},
		{ // Exif IFD
			ratio(0x829A, 1, 250),
			ratio(0x829D, 56, 10),
			shorts(0x8827, 200),
			asciiEntry(0x9003, "2024:05:06 07:08:09"),
			ratio(0x920A, 35, 1),
			asciiEntry(0xA434, "Synthetic 35mm F5.6"),
		},
	}
	// AsShotNeutral needs three values.
	for i, e := range ifds[0] {
		if e.tag == 0xC628 {
			b := make([]byte, 0, 24)
			for range 3 {
				b = binary.LittleEndian.AppendUint32(b, 1)
				b = binary.LittleEndian.AppendUint32(b, 1)
			}
			ifds[0][i] = ifdEntry{tag: 0xC628, typ: 5, count: 3, data: b, ifd: -1}
		}
	}
	return write(path, tiffFile(ifds, [][]byte{nil, jb.Bytes(), cfa}))
}

// tiffFile lays out little-endian IFDs (the first is IFD0) with their
// out-of-line values, then the blobs; entries with blob > 0 get the offset
// of blobs[blob].
func tiffFile(ifds [][]ifdEntry, blobs [][]byte) []byte {
	offsets := make([]uint32, len(ifds))
	off := uint32(8)
	for i, ifd := range ifds {
		offsets[i] = off
		off += 2 + uint32(len(ifd))*12 + 4
		for _, e := range ifd {
			if len(e.data) > 4 {
				off += uint32(len(e.data) + len(e.data)%2)
			}
		}
	}
	blobOff := make([]uint32, len(blobs))
	for i, b := range blobs {
		blobOff[i] = off
		off += uint32(len(b) + len(b)%2)
	}
	out := []byte("II*\x00\x08\x00\x00\x00")
	for i, ifd := range ifds {
		data := offsets[i] + 2 + uint32(len(ifd))*12 + 4
		var extra []byte
		out = binary.LittleEndian.AppendUint16(out, uint16(len(ifd)))
		for _, e := range ifd {
			out = binary.LittleEndian.AppendUint16(out, e.tag)
			out = binary.LittleEndian.AppendUint16(out, e.typ)
			out = binary.LittleEndian.AppendUint32(out, e.count)
			switch {
			case e.ifd >= 0:
				out = binary.LittleEndian.AppendUint32(out, offsets[e.ifd])
			case e.blob > 0:
				out = binary.LittleEndian.AppendUint32(out, blobOff[e.blob])
			case len(e.data) > 4:
				out = binary.LittleEndian.AppendUint32(out, data+uint32(len(extra)))
				extra = append(extra, e.data...)
				if len(e.data)%2 == 1 {
					extra = append(extra, 0)
				}
			default:
				v := make([]byte, 4)
				copy(v, e.data)
				out = append(out, v...)
			}
		}
		out = binary.LittleEndian.AppendUint32(out, 0)
		out = append(out, extra...)
	}
	for _, b := range blobs {
		out = append(out, b...)
		if len(b)%2 == 1 {
			out = append(out, 0)
		}
	}
	return out
}

func srgbToLinear(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// rotCCW rotates img 90° counter-clockwise.
func rotCCW(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			out.SetRGBA(y, b.Dx()-1-x, img.RGBAAt(x, y))
		}
	}
	return out
}
