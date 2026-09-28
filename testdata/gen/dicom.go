package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
)

// ---- DICOM (WP-108) ---------------------------------------------------------
//
// Synthetic DICOM Part 10 files for internal/codec/dicom: every value is
// made up here (the "patient" elements carry the marker xiPrivacyMarker so
// tests can check it never leaks), the pixels come from photo() or a
// computed phantom. No real patient data is involved.

// xiPrivacyMarker is written into every patient-identifying element.
const xiPrivacyMarker = "XIPRIVACYMARKER"

// Transfer syntaxes written.
const (
	dcmImplicitLE = "1.2.840.10008.1.2"
	dcmExplicitLE = "1.2.840.10008.1.2.1"
	dcmDeflated   = "1.2.840.10008.1.2.1.99"
	dcmExplicitBE = "1.2.840.10008.1.2.2"
	dcmRLE        = "1.2.840.10008.1.2.5"
	dcmJPEGBase   = "1.2.840.10008.1.2.4.50"
	dcmJ2KLossls  = "1.2.840.10008.1.2.4.90"
)

// dcmWriter writes dataset elements in one transfer syntax.
type dcmWriter struct {
	b        bytes.Buffer
	order    binary.ByteOrder
	explicit bool
}

func (w *dcmWriter) tag(t uint32) {
	var b [4]byte
	w.order.PutUint16(b[:], uint16(t>>16))
	w.order.PutUint16(b[2:], uint16(t))
	w.b.Write(b[:])
}

func (w *dcmWriter) u16(v uint16) {
	var b [2]byte
	w.order.PutUint16(b[:], v)
	w.b.Write(b[:])
}

func (w *dcmWriter) u32(v uint32) {
	var b [4]byte
	w.order.PutUint32(b[:], v)
	w.b.Write(b[:])
}

func dcmLongVR(vr string) bool {
	switch vr {
	case "OB", "OW", "OF", "SQ", "UN", "UT":
		return true
	}
	return false
}

// header writes an element header; length 0xFFFFFFFF is undefined.
func (w *dcmWriter) header(t uint32, vr string, n uint32) {
	w.tag(t)
	switch {
	case !w.explicit || t>>16 == 0xFFFE: // items and delimiters: no VR
		w.u32(n)
	case dcmLongVR(vr):
		w.b.WriteString(vr)
		w.u16(0)
		w.u32(n)
	default:
		w.b.WriteString(vr)
		w.u16(uint16(n))
	}
}

// el writes an element, padded to an even length.
func (w *dcmWriter) el(t uint32, vr string, v []byte) {
	if len(v)%2 == 1 {
		pad := byte(' ')
		if vr == "UI" || vr == "OB" || vr == "UN" {
			pad = 0
		}
		v = append(append([]byte{}, v...), pad)
	}
	w.header(t, vr, uint32(len(v)))
	w.b.Write(v)
}

func (w *dcmWriter) str(t uint32, vr, s string) { w.el(t, vr, []byte(s)) }

func (w *dcmWriter) us(t uint32, vs ...uint16) {
	b := make([]byte, 2*len(vs))
	for i, v := range vs {
		w.order.PutUint16(b[2*i:], v)
	}
	w.el(t, "US", b)
}

// patient writes made-up identifying elements, a private block and a
// nested undefined-length sequence, all carrying the privacy marker.
func (w *dcmWriter) patient(sopClass, uid string) {
	w.str(0x00080016, "UI", sopClass)
	w.str(0x00080018, "UI", uid)
	w.str(0x00080020, "DA", "19700101")
	w.str(0x00080060, "CS", "OT")
	w.str(0x00080080, "LO", "Hospital "+xiPrivacyMarker)
	w.str(0x00090010, "LO", "XI PRIVATE")
	w.el(0x00091001, "UN", []byte(xiPrivacyMarker+"-private"))
	w.str(0x00100010, "PN", "Doe^"+xiPrivacyMarker)
	w.str(0x00100020, "LO", "ID-"+xiPrivacyMarker)
	w.str(0x00100030, "DA", "19010101")
	// Other Patient IDs Sequence: undefined length, one undefined-length
	// item holding a Patient ID and a Rows element that must not be
	// mistaken for the image's.
	w.header(0x00101002, "SQ", 0xFFFFFFFF)
	w.header(0xFFFEE000, "", 0xFFFFFFFF)
	w.str(0x00100020, "LO", "SEQ-"+xiPrivacyMarker)
	w.us(0x00280010, 7)
	w.header(0xFFFEE00D, "", 0)
	w.header(0xFFFEE0DD, "", 0)
	w.str(0x00204000, "LT", "Comments "+xiPrivacyMarker)
}

// dcmImage describes the image module written.
type dcmImage struct {
	w, h, samples, frames   int
	bitsAlloc, bitsStored   int
	signed, planar          bool
	photometric             string
	intercept, slope        string
	windowC, windowW, func_ string
}

func (w *dcmWriter) image(im dcmImage) {
	w.us(0x00280002, uint16(im.samples))
	w.str(0x00280004, "CS", im.photometric)
	if im.samples > 1 {
		p := uint16(0)
		if im.planar {
			p = 1
		}
		w.us(0x00280006, p)
	}
	if im.frames > 1 {
		w.str(0x00280008, "IS", fmt.Sprint(im.frames))
	}
	w.us(0x00280010, uint16(im.h))
	w.us(0x00280011, uint16(im.w))
	w.us(0x00280100, uint16(im.bitsAlloc))
	w.us(0x00280101, uint16(im.bitsStored))
	w.us(0x00280102, uint16(im.bitsStored-1))
	s := uint16(0)
	if im.signed {
		s = 1
	}
	w.us(0x00280103, s)
	if im.windowC != "" {
		w.str(0x00281050, "DS", im.windowC)
		w.str(0x00281051, "DS", im.windowW)
	}
	if im.intercept != "" {
		w.str(0x00281052, "DS", im.intercept)
		w.str(0x00281053, "DS", im.slope)
	}
	if im.func_ != "" {
		w.str(0x00281056, "CS", im.func_)
	}
}

// pixels writes native pixel data.
func (w *dcmWriter) pixels(vr string, data []byte) { w.el(0x7FE00010, vr, data) }

// encapsulated writes encapsulated pixel data: the Basic Offset Table and
// the fragments.
func (w *dcmWriter) encapsulated(bot []uint32, frags [][]byte) {
	w.header(0x7FE00010, "OB", 0xFFFFFFFF)
	w.header(0xFFFEE000, "", uint32(4*len(bot)))
	for _, o := range bot {
		w.u32(o)
	}
	for _, f := range frags {
		if len(f)%2 == 1 {
			f = append(append([]byte{}, f...), 0)
		}
		w.header(0xFFFEE000, "", uint32(len(f)))
		w.b.Write(f)
	}
	w.header(0xFFFEE0DD, "", 0)
}

// dcmFile assembles preamble, file meta information and dataset.
func dcmFile(ts string, dataset []byte) []byte {
	var m dcmWriter
	m.order, m.explicit = binary.LittleEndian, true
	m.el(0x00020001, "OB", []byte{0, 1})
	m.str(0x00020002, "UI", "1.2.840.10008.5.1.4.1.1.7")
	m.str(0x00020003, "UI", "2.25.108")
	m.str(0x00020010, "UI", ts)
	m.str(0x00020012, "UI", "2.25.1")
	var out bytes.Buffer
	out.Write(make([]byte, 128))
	out.WriteString("DICM")
	var g dcmWriter
	g.order, g.explicit = binary.LittleEndian, true
	g.el(0x00020000, "UL", binary.LittleEndian.AppendUint32(nil, uint32(m.b.Len())))
	out.Write(g.b.Bytes())
	out.Write(m.b.Bytes())
	out.Write(dataset)
	return out.Bytes()
}

func newDataset(ts string) *dcmWriter {
	w := &dcmWriter{order: binary.LittleEndian, explicit: ts != dcmImplicitLE}
	if ts == dcmExplicitBE {
		w.order = binary.BigEndian
	}
	w.patient("1.2.840.10008.5.1.4.1.1.7", "2.25.108")
	return w
}

// phantomHU is a CT-like phantom in Hounsfield units: air, a soft-tissue
// disc and a bone ring.
func phantomHU(w, h, x, y int) int {
	dx, dy := float64(x)-float64(w)/2, float64(y)-float64(h)/2
	r := math.Hypot(dx, dy) / (float64(min(w, h)) / 2)
	switch {
	case r > 0.95:
		return -1000
	case r > 0.8:
		return 700
	default:
		return 40 + int(60*dx/float64(w))
	}
}

// photoRGB is photo() as interleaved or planar 8-bit RGB.
func photoRGB(w, h int, planar bool) []byte {
	img := photo(w, h)
	out := make([]byte, w*h*3)
	for i := range w * h {
		for c := range 3 {
			v := img.Pix[4*i+c]
			if planar {
				out[c*w*h+i] = v
			} else {
				out[3*i+c] = v
			}
		}
	}
	return out
}

// photoGrey is photo()'s luma.
func photoGrey(w, h int) []byte {
	img := photo(w, h)
	out := make([]byte, w*h)
	for i := range out {
		p := img.Pix[4*i:]
		out[i] = byte((299*int(p[0]) + 587*int(p[1]) + 114*int(p[2])) / 1000)
	}
	return out
}

// packBits encodes one RLE segment (literal runs only, plus replicate runs
// for repeats of three or more).
func packBits(src []byte) []byte {
	var out []byte
	for i := 0; i < len(src); {
		j := i + 1
		for j < len(src) && j-i < 128 && src[j] == src[i] {
			j++
		}
		if j-i >= 3 {
			out = append(out, byte(int8(1-(j-i))), src[i])
			i = j
			continue
		}
		k := i
		for k < len(src) && k-i < 128 && (k+2 >= len(src) || src[k] != src[k+1] || src[k] != src[k+2]) {
			k++
		}
		if k == i {
			k = i + 1
		}
		out = append(out, byte(k-i-1))
		out = append(out, src[i:k]...)
		i = k
	}
	if len(out)%2 == 1 {
		out = append(out, 0x80) // no-op, keeps the segment even
	}
	return out
}

// rleFrame encodes byte planes (each w*h bytes, most significant first).
func rleFrame(planes [][]byte) []byte {
	hdr := make([]byte, 64)
	binary.LittleEndian.PutUint32(hdr, uint32(len(planes)))
	body := []byte{}
	for i, p := range planes {
		binary.LittleEndian.PutUint32(hdr[4+4*i:], uint32(64+len(body)))
		body = append(body, packBits(p)...)
	}
	return append(hdr, body...)
}

func dicomJPEG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	return b.Bytes(), err
}

// writeDICOMFixtures writes the internal/codec/dicom fixtures into dir. j2k,
// when not empty, is a lossless 16-bit grey JPEG 2000 codestream of the
// phantom (vips jp2ksave, see gen.sh) wrapped as ct-j2k.dcm.
func writeDICOMFixtures(dir, j2k string) error {
	const w, h = 64, 48
	files := map[string][]byte{}
	le := binary.LittleEndian

	// CT-like: 16 bits allocated, 12 stored, unsigned, intercept -1024,
	// window 40/400; the unused high bits carry noise that must be masked.
	{
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{
			w: w, h: h, samples: 1, frames: 1, bitsAlloc: 16, bitsStored: 12,
			photometric: "MONOCHROME2", intercept: "-1024", slope: "1", windowC: "40\\50", windowW: "400\\500",
		})
		px := make([]byte, 2*w*h)
		for y := range h {
			for x := range w {
				v := uint16(phantomHU(w, h, x, y)+1024) | uint16((x*7+y)%16)<<12
				le.PutUint16(px[2*(y*w+x):], v)
			}
		}
		d.pixels("OW", px)
		files["ct-window.dcm"] = dcmFile(dcmExplicitLE, d.b.Bytes())
	}
	// Explicit big endian, signed 16-bit HU stored directly, no window.
	{
		d := newDataset(dcmExplicitBE)
		d.image(dcmImage{
			w: w, h: h, samples: 1, frames: 1, bitsAlloc: 16, bitsStored: 16, signed: true,
			photometric: "MONOCHROME2",
		})
		px := make([]byte, 2*w*h)
		for y := range h {
			for x := range w {
				binary.BigEndian.PutUint16(px[2*(y*w+x):], uint16(int16(phantomHU(w, h, x, y))))
			}
		}
		d.pixels("OW", px)
		files["ct-bigendian.dcm"] = dcmFile(dcmExplicitBE, d.b.Bytes())
	}
	// MONOCHROME1 8-bit, implicit VR little endian.
	{
		d := newDataset(dcmImplicitLE)
		d.image(dcmImage{w: w, h: h, samples: 1, frames: 1, bitsAlloc: 8, bitsStored: 8, photometric: "MONOCHROME1"})
		d.pixels("OB", photoGrey(w, h))
		files["mono1-implicit.dcm"] = dcmFile(dcmImplicitLE, d.b.Bytes())
	}
	// RGB 8-bit, colour-by-plane.
	{
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{w: w, h: h, samples: 3, frames: 1, bitsAlloc: 8, bitsStored: 8, planar: true, photometric: "RGB"})
		d.pixels("OB", photoRGB(w, h, true))
		files["rgb-planar.dcm"] = dcmFile(dcmExplicitLE, d.b.Bytes())
	}
	// Deflated explicit VR little endian, RGB interleaved.
	{
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{w: w, h: h, samples: 3, frames: 1, bitsAlloc: 8, bitsStored: 8, photometric: "RGB"})
		d.pixels("OB", photoRGB(w, h, false))
		var z bytes.Buffer
		fw, err := flate.NewWriter(&z, flate.BestCompression)
		if err != nil {
			return err
		}
		if _, err := fw.Write(d.b.Bytes()); err != nil {
			return err
		}
		if err := fw.Close(); err != nil {
			return err
		}
		files["rgb-deflated.dcm"] = dcmFile(dcmDeflated, z.Bytes())
	}
	// RLE Lossless: RGB 8-bit (three segments) and the CT phantom 16-bit.
	{
		d := newDataset(dcmRLE)
		d.image(dcmImage{w: w, h: h, samples: 3, frames: 1, bitsAlloc: 8, bitsStored: 8, planar: true, photometric: "RGB"})
		rgb := photoRGB(w, h, true)
		d.encapsulated(nil, [][]byte{rleFrame([][]byte{rgb[:w*h], rgb[w*h : 2*w*h], rgb[2*w*h:]})})
		files["rgb-rle.dcm"] = dcmFile(dcmRLE, d.b.Bytes())

		d = newDataset(dcmRLE)
		d.image(dcmImage{
			w: w, h: h, samples: 1, frames: 1, bitsAlloc: 16, bitsStored: 16,
			photometric: "MONOCHROME2", intercept: "-1024", slope: "1", windowC: "40", windowW: "400",
		})
		hi, lo := make([]byte, w*h), make([]byte, w*h)
		for y := range h {
			for x := range w {
				v := uint16(phantomHU(w, h, x, y) + 1024)
				hi[y*w+x], lo[y*w+x] = byte(v>>8), byte(v)
			}
		}
		d.encapsulated(nil, [][]byte{rleFrame([][]byte{hi, lo})})
		files["ct-rle.dcm"] = dcmFile(dcmRLE, d.b.Bytes())
	}
	// Two native frames, 8-bit grey: frame 0 the photo, frame 1 black.
	{
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{w: w, h: h, samples: 1, frames: 2, bitsAlloc: 8, bitsStored: 8, photometric: "MONOCHROME2"})
		d.pixels("OB", append(photoGrey(w, h), make([]byte, w*h)...))
		files["grey-2frames.dcm"] = dcmFile(dcmExplicitLE, d.b.Bytes())
	}
	// JPEG baseline YBR_FULL_422, the codestream split in two fragments.
	jp, err := dicomJPEG(photo(w, h))
	if err != nil {
		return err
	}
	{
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{w: w, h: h, samples: 3, frames: 1, bitsAlloc: 8, bitsStored: 8, photometric: "YBR_FULL_422"})
		half := len(jp) / 2 &^ 1
		d.encapsulated(nil, [][]byte{jp[:half], jp[half:]})
		files["jpeg-baseline.dcm"] = dcmFile(dcmJPEGBase, d.b.Bytes())
	}
	// Two JPEG frames with a Basic Offset Table; frame 0 in two fragments.
	{
		black, err := dicomJPEG(image.NewRGBA(image.Rect(0, 0, w, h)))
		if err != nil {
			return err
		}
		half := len(jp) / 2 &^ 1
		f0a, f0b := jp[:half], jp[half:]
		pad := func(b []byte) int { return len(b) + len(b)%2 }
		second := uint32(8 + pad(f0a) + 8 + pad(f0b))
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{w: w, h: h, samples: 3, frames: 2, bitsAlloc: 8, bitsStored: 8, photometric: "YBR_FULL_422"})
		d.encapsulated([]uint32{0, second}, [][]byte{f0a, f0b, black})
		files["jpeg-2frames-bot.dcm"] = dcmFile(dcmJPEGBase, d.b.Bytes())
	}
	// PALETTE COLOR 8-bit with 16-bit lookup tables (a heat map).
	{
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{w: w, h: h, samples: 1, frames: 1, bitsAlloc: 8, bitsStored: 8, photometric: "PALETTE COLOR"})
		var r, g, b []byte
		for i := range 256 {
			r = le.AppendUint16(r, uint16(min(255, 3*i))*257)
			g = le.AppendUint16(g, uint16(max(0, min(255, 3*i-255)))*257)
			b = le.AppendUint16(b, uint16(max(0, 3*i-510))*257)
		}
		for _, t := range []uint32{0x00281101, 0x00281102, 0x00281103} {
			d.us(t, 256, 0, 16)
		}
		d.el(0x00281201, "OW", r)
		d.el(0x00281202, "OW", g)
		d.el(0x00281203, "OW", b)
		d.pixels("OB", photoGrey(w, h))
		files["palette.dcm"] = dcmFile(dcmExplicitLE, d.b.Bytes())
	}
	if j2k != "" {
		cs, err := os.ReadFile(j2k)
		if err != nil {
			return err
		}
		if cs, err = j2kCodestream(cs); err != nil {
			return err
		}
		d := newDataset(dcmExplicitLE)
		d.image(dcmImage{
			w: w, h: h, samples: 1, frames: 1, bitsAlloc: 16, bitsStored: 16,
			photometric: "MONOCHROME2", intercept: "-1024", slope: "1", windowC: "40", windowW: "400",
		})
		d.encapsulated(nil, [][]byte{cs})
		files["ct-j2k.dcm"] = dcmFile(dcmJ2KLossls, d.b.Bytes())
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writePhantomPNG writes the CT phantom (HU + 1024) as a 16-bit grey PNG,
// the input of the JPEG 2000 codestream (gen.sh).
func writePhantomPNG(out string) error {
	const w, h = 64, 48
	img := image.NewGray16(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint16(phantomHU(w, h, x, y) + 1024)
			img.Pix[2*(y*w+x)], img.Pix[2*(y*w+x)+1] = byte(v>>8), byte(v)
		}
	}
	return writePNG(out, img)
}

// j2kCodestream returns the raw codestream of a JP2 file (its jp2c box),
// which is what DICOM encapsulates, or b itself when it is one already.
func j2kCodestream(b []byte) ([]byte, error) {
	for off := 0; off+8 <= len(b); {
		size := int(binary.BigEndian.Uint32(b[off:]))
		typ := string(b[off+4 : off+8])
		if size == 0 {
			size = len(b) - off
		}
		if size < 8 || off+size > len(b) {
			break
		}
		if typ == "jp2c" {
			return b[off+8 : off+size], nil
		}
		off += size
	}
	if len(b) >= 4 && string(b[:4]) == "\xff\x4f\xff\x51" {
		return b, nil
	}
	return nil, fmt.Errorf("dicom: no JPEG 2000 codestream in %d bytes", len(b))
}
