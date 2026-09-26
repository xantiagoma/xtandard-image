package bmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// rgba converts a Raw to straight RGBA samples (alpha 255 for 3 bands).
func rgba(r *raster.Raw) []byte {
	out := make([]byte, 0, r.Width*r.Height*4)
	for i := 0; i < len(r.Data); i += r.Bands {
		a := byte(0xFF)
		if r.Bands == 4 {
			a = r.Data[i+3]
		}
		out = append(out, r.Data[i], r.Data[i+1], r.Data[i+2], a)
	}
	return out
}

// imageRGBA converts any image.Image to straight RGBA samples.
func imageRGBA(m image.Image) (w, h int, pix []byte) {
	b := m.Bounds()
	pix = make([]byte, 0, b.Dx()*b.Dy()*4)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c, _ := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
			pix = append(pix, c.R, c.G, c.B, c.A)
		}
	}
	return b.Dx(), b.Dy(), pix
}

func decodePNG(tb testing.TB, b []byte) image.Image {
	tb.Helper()
	m, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		tb.Fatal(err)
	}
	return m
}

// diffPixels compares RGBA sample slices with a per-sample tolerance and
// describes the first mismatch.
func diffPixels(w int, got, want []byte, tol int) string {
	if len(got) != len(want) {
		return fmt.Sprintf("got %d samples, want %d", len(got), len(want))
	}
	for i := range got {
		if d := int(got[i]) - int(want[i]); d > tol || d < -tol {
			p := i / 4
			return fmt.Sprintf("pixel (%d,%d) channel %d: got %d, want %d (tolerance %d)",
				p%w, p/w, i%4, got[i], want[i], tol)
		}
	}
	return ""
}

func wantKind(t *testing.T, err error, k errs.Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %v", k)
	}
	if got := errs.KindOf(err); got != k {
		t.Fatalf("error kind %v, want %v (%v)", got, k, err)
	}
}

// dib describes a bitmap to assemble by hand, for the variants ImageMagick
// does not write.
type dib struct {
	hdrLen      int // 12, 40, 52, 56, 64, 108 or 124
	w, h        int32
	bpp         uint16
	planes      uint16 // 0 means 1
	compression uint32
	imageSize   uint32
	numColors   uint32
	masks       []uint32 // after a 40-byte header, or in the header when hdrLen >= 52
	palette     [][3]byte
	pixels      []byte
	offset      int64 // bfOffBits override; 0 computes it
}

func (d *dib) info() []byte {
	b := make([]byte, d.hdrLen, d.hdrLen+16+4*len(d.palette))
	le := binary.LittleEndian
	le.PutUint32(b, u32(int64(d.hdrLen)))
	planes := d.planes
	if planes == 0 {
		planes = 1
	}
	if d.hdrLen == coreHeaderLen {
		put16(b[4:], d.w)
		put16(b[6:], d.h)
		le.PutUint16(b[8:], planes)
		le.PutUint16(b[10:], d.bpp)
	} else {
		put32(b[4:], d.w)
		put32(b[8:], d.h)
		le.PutUint16(b[12:], planes)
		le.PutUint16(b[14:], d.bpp)
		if d.hdrLen >= 20 {
			le.PutUint32(b[16:], d.compression)
		}
		if d.hdrLen >= 24 {
			le.PutUint32(b[20:], d.imageSize)
		}
		if d.hdrLen >= 36 {
			le.PutUint32(b[32:], d.numColors)
		}
		if d.hdrLen >= v2HeaderLen && d.hdrLen != os2v2HeaderLen {
			for i, m := range d.masks {
				le.PutUint32(b[40+4*i:], m)
			}
		}
	}
	if d.hdrLen == infoHeaderLen {
		for _, m := range d.masks {
			b = le.AppendUint32(b, m)
		}
	}
	entry := 4
	if d.hdrLen == coreHeaderLen {
		entry = 3
	}
	for _, c := range d.palette {
		b = append(b, c[2], c[1], c[0]) // BGR
		if entry == 4 {
			b = append(b, 0)
		}
	}
	return b
}

// file returns a complete BMP file.
func (d *dib) file() []byte {
	info := d.info()
	off := int64(fileHeaderLen + len(info))
	if d.offset != 0 {
		off = d.offset
	}
	b := make([]byte, fileHeaderLen, fileHeaderLen+len(info)+len(d.pixels))
	b[0], b[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(b[2:], u32(int64(cap(b))))
	binary.LittleEndian.PutUint32(b[10:], u32(off))
	b = append(b, info...)
	return append(b, d.pixels...)
}

// icon returns the DIB as stored in an ICO entry (doubled height).
func (d *dib) icon() []byte {
	c := *d
	c.h *= 2
	return append(c.info(), c.pixels...)
}

// put32 stores v in two's complement, as a header's signed fields are.
func put32(b []byte, v int32) {
	if _, err := binary.Encode(b, binary.LittleEndian, v); err != nil {
		panic(err)
	}
}

// put16 stores the low 16 bits of v (BITMAPCOREHEADER sizes).
func put16(b []byte, v int32) {
	var t [4]byte
	put32(t[:], v)
	copy(b, t[:2])
}

// rawImage wraps a Raw as an image.Image.
func rawImage(r *raster.Raw) image.Image {
	m := image.NewNRGBA(image.Rect(0, 0, r.Width, r.Height))
	copy(m.Pix, rgba(r))
	return m
}
