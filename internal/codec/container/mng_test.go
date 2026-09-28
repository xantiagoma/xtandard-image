package container_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/container"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	sigMNG = "\x8aMNG\r\n\x1a\n"
	sigJNG = "\x8bJNG\r\n\x1a\n"
)

// stream is a PNG-style chunk stream under construction.
type stream struct{ b bytes.Buffer }

func newStream(sig string) *stream {
	s := &stream{}
	s.b.WriteString(sig)
	return s
}

func (s *stream) chunk(typ string, data []byte) *stream {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data))) //nolint:gosec // small test data
	s.b.Write(n[:])
	start := s.b.Len()
	s.b.WriteString(typ)
	s.b.Write(data)
	binary.BigEndian.PutUint32(n[:], crc32.ChecksumIEEE(s.b.Bytes()[start:]))
	s.b.Write(n[:])
	return s
}

// chunks appends the chunks of a PNG file (after its signature).
func (s *stream) chunks(p []byte) *stream {
	s.b.Write(p[8:])
	return s
}

func (s *stream) bytes() []byte { return s.b.Bytes() }

func mhdrOf(w, h uint32) []byte {
	d := make([]byte, 28)
	binary.BigEndian.PutUint32(d, w)
	binary.BigEndian.PutUint32(d[4:], h)
	return d
}

func jhdrOf(w, h uint32, colour, depth, alphaDepth, alphaComp byte) []byte {
	d := make([]byte, 16)
	binary.BigEndian.PutUint32(d, w)
	binary.BigEndian.PutUint32(d[4:], h)
	d[8], d[9], d[10], d[12], d[13] = colour, depth, 8, alphaDepth, alphaComp
	return d
}

func jpegOf(tb testing.TB, w, h int) []byte {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 3)
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

// greyIDAT is the IDAT data of a w×h 8-bit grey image of value v.
func greyIDAT(tb testing.TB, w, h int, v uint8) []byte {
	tb.Helper()
	g := image.NewGray(image.Rect(0, 0, w, h))
	for i := range g.Pix {
		g.Pix[i] = v
	}
	var b bytes.Buffer
	if err := png.Encode(&b, g); err != nil {
		tb.Fatal(err)
	}
	p := b.Bytes()[8:]
	var idat []byte
	for len(p) >= 12 {
		n := int(binary.BigEndian.Uint32(p))
		if string(p[4:8]) == "IDAT" {
			idat = append(idat, p[8:8+n]...)
		}
		p = p[12+n:]
	}
	return idat
}

func TestMNGFixtures(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures")
	tests := []struct {
		path  string
		kind  container.Kind
		cont  container.Content
		entry string
		jpeg  bool
		alpha bool
	}{
		{"mng/png.mng", container.KindMNG, container.ContentFrame, "first frame", false, false},
		{"mng/palette.mng", container.KindMNG, container.ContentFrame, "first frame", false, false},
		{"mng/frames.mng", container.KindMNG, container.ContentFrame, "first frame", false, false},
		{"mng/jng.mng", container.KindMNG, container.ContentFrame, "first frame", false, true},
		{"jng/jng.jng", container.KindJNG, container.ContentComposite, "JDAT", true, false},
		{"jng/jng-idat.jng", container.KindJNG, container.ContentComposite, "JDAT+IDAT", false, true},
		{"jng/jng-jdaa.jng", container.KindJNG, container.ContentComposite, "JDAT+JDAA", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join(root, tt.path))
			if err != nil {
				t.Fatal(err)
			}
			if k, ok := container.Detect(data); !ok || k != tt.kind {
				t.Errorf("Detect = %v, %v", k, ok)
			}
			out, src, err := extract(t, data, container.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			want := container.Source{Kind: tt.kind, Entry: tt.entry, Content: tt.cont}
			if diff := cmp.Diff(want, src); diff != "" {
				t.Errorf("source (-want +got):\n%s", diff)
			}
			var img image.Image
			if tt.jpeg {
				img, err = jpeg.Decode(bytes.NewReader(out))
			} else {
				img, err = png.Decode(bytes.NewReader(out))
			}
			if err != nil {
				t.Fatalf("embedded image: %v", err)
			}
			if b := img.Bounds(); b.Dx() != 48 || b.Dy() != 32 {
				t.Errorf("size %v, want 48x32", b)
			}
			if tt.alpha {
				// The fixtures fade out towards the corners.
				if _, _, _, a := img.At(0, 0).RGBA(); a == 0xffff {
					t.Errorf("corner alpha %d, want transparent", a)
				}
				if _, _, _, a := img.At(24, 16).RGBA(); a < 0xf000 { // JDAA alpha is lossy
					t.Errorf("centre alpha %d, want (nearly) opaque", a)
				}
			}
		})
	}
}

// TestMNGFirstFrame: the first of two frames is extracted, byte for byte
// the same PNG as from a one-frame file.
func TestMNGFirstFrame(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "mng")
	var outs [2][]byte
	for i, p := range []string{"png.mng", "frames.mng"} {
		data, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		if outs[i], _, err = extract(t, data, container.Limits{}); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Error("frames.mng's first frame differs from png.mng's")
	}
}

// TestMNGGlobalPalette: an empty embedded PLTE takes the global palette
// (and tRNS).
func TestMNGGlobalPalette(t *testing.T) {
	t.Parallel()
	pal := color.Palette{color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 128}}
	src := image.NewPaletted(image.Rect(0, 0, 4, 2), pal)
	src.Pix = []uint8{0, 1, 0, 1, 1, 0, 1, 0}
	var b bytes.Buffer
	if err := png.Encode(&b, src); err != nil {
		t.Fatal(err)
	}
	s := newStream(sigMNG).chunk("MHDR", mhdrOf(4, 2))
	p := b.Bytes()[8:]
	var embedded []struct {
		typ  string
		data []byte
	}
	for len(p) >= 12 {
		n := int(binary.BigEndian.Uint32(p))
		embedded = append(embedded, struct {
			typ  string
			data []byte
		}{string(p[4:8]), p[8 : 8+n]})
		p = p[12+n:]
	}
	for _, c := range embedded {
		if c.typ == "PLTE" || c.typ == "tRNS" {
			s.chunk(c.typ, c.data) // global
		}
	}
	for _, c := range embedded {
		switch c.typ {
		case "PLTE":
			s.chunk("PLTE", nil)
		case "tRNS":
		default:
			s.chunk(c.typ, c.data)
		}
	}
	out, _, err := extract(t, s.chunk("MEND", nil).bytes(), container.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	got := color.NRGBAModel.Convert(img.At(1, 0))
	if diff := cmp.Diff(color.Color(color.NRGBA{0, 0, 255, 128}), got); diff != "" {
		t.Errorf("pixel (-want +got):\n%s", diff)
	}
}

func TestMNGErrors(t *testing.T) {
	t.Parallel()
	photo := pngOf(t, 4, 4)
	jp := jpegOf(t, 8, 8)
	mng := func() *stream { return newStream(sigMNG).chunk("MHDR", mhdrOf(4, 4)) }
	jng := func(hdr []byte) *stream { return newStream(sigJNG).chunk("JHDR", hdr) }
	badCRC := mng().chunks(photo).bytes()
	badCRC[len(badCRC)-1] ^= 1
	noIEND := mng().chunks(photo[:len(photo)-12]).chunk("MEND", nil).bytes()
	tests := []struct {
		name string
		data []byte
		lim  container.Limits
		want errs.Kind
	}{
		{"mng without image", mng().chunk("MEND", nil).bytes(), container.Limits{}, errs.KindUnsupported},
		{"mng without MEND", mng().bytes(), container.Limits{}, errs.KindUnsupported},
		{"delta png", mng().chunk("DHDR", make([]byte, 4)).bytes(), container.Limits{}, errs.KindUnsupported},
		{"basi", mng().chunk("BASI", make([]byte, 13)).bytes(), container.Limits{}, errs.KindUnsupported},
		{"bad crc", badCRC, container.Limits{}, errs.KindCorrupt},
		{"truncated", mng().chunks(photo).bytes()[:60], container.Limits{}, errs.KindCorrupt},
		{"embedded png without IEND", noIEND, container.Limits{}, errs.KindCorrupt},
		{"bad chunk type", mng().chunk("A1B2", nil).bytes(), container.Limits{}, errs.KindCorrupt},
		{"empty PLTE without global", mng().chunk("IHDR", []byte{0, 0, 0, 1, 0, 0, 0, 1, 8, 3, 0, 0, 0}).
			chunk("PLTE", nil).chunk("IEND", nil).bytes(), container.Limits{}, errs.KindCorrupt},
		{"mng frame over MaxBytes", mng().chunks(photo).bytes(), container.Limits{MaxBytes: 32}, errs.KindLimitExceeded},
		{"jng 12-bit", jng(jhdrOf(8, 8, 10, 12, 0, 0)).bytes(), container.Limits{}, errs.KindUnsupported},
		{"jng over MaxPixels", jng(jhdrOf(1<<16, 1<<16, 10, 8, 0, 0)).bytes(), container.Limits{}, errs.KindLimitExceeded},
		{"jng bad colour type", jng(jhdrOf(8, 8, 9, 8, 0, 0)).bytes(), container.Limits{}, errs.KindCorrupt},
		{"jng alpha depth without alpha", jng(jhdrOf(8, 8, 10, 8, 8, 0)).bytes(), container.Limits{}, errs.KindCorrupt},
		{"jng jpeg alpha depth 4", jng(jhdrOf(8, 8, 14, 8, 4, 8)).bytes(), container.Limits{}, errs.KindCorrupt},
		{"jng zero size", jng(jhdrOf(0, 8, 10, 8, 0, 0)).bytes(), container.Limits{}, errs.KindCorrupt},
		{"jng without IEND", jng(jhdrOf(8, 8, 10, 8, 0, 0)).chunk("JDAT", jp).bytes(), container.Limits{}, errs.KindCorrupt},
		{
			"jng JDAT not jpeg", jng(jhdrOf(8, 8, 10, 8, 0, 0)).chunk("JDAT", []byte("nope")).chunk("IEND", nil).bytes(),
			container.Limits{},
			errs.KindCorrupt,
		},
		{"jng size mismatch", jng(jhdrOf(9, 8, 14, 8, 8, 0)).chunk("IDAT", greyIDAT(t, 9, 8, 7)).
			chunk("JDAT", jp).chunk("IEND", nil).bytes(), container.Limits{}, errs.KindCorrupt},
		{
			"jng alpha without IDAT", jng(jhdrOf(8, 8, 14, 8, 8, 0)).chunk("JDAT", jp).chunk("IEND", nil).bytes(),
			container.Limits{},
			errs.KindCorrupt,
		},
		{"jng bad alpha data", jng(jhdrOf(8, 8, 14, 8, 8, 0)).chunk("IDAT", []byte{1, 2, 3}).
			chunk("JDAT", jp).chunk("IEND", nil).bytes(), container.Limits{}, errs.KindCorrupt},
		{
			"jng over MaxBytes", jng(jhdrOf(8, 8, 10, 8, 0, 0)).chunk("JDAT", jp).chunk("IEND", nil).bytes(),
			container.Limits{MaxBytes: 64},
			errs.KindLimitExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := extract(t, tt.data, tt.lim)
			if got := errs.KindOf(err); err == nil || got != tt.want {
				t.Errorf("err %v (kind %v), want kind %v", err, got, tt.want)
			}
		})
	}
}

// TestJNGAlphaDepths decodes 1-, 2-, 4- and 16-bit PNG-compressed alpha and
// the 8-bit part of a 20-bit JNG.
func TestJNGAlphaDepths(t *testing.T) {
	t.Parallel()
	jp := jpegOf(t, 8, 8)
	for _, depth := range []byte{1, 2, 4, 16} {
		t.Run(string(rune('0'+depth%10)), func(t *testing.T) {
			t.Parallel()
			// All-ones samples: fully opaque whatever the depth.
			rowBytes := (8*int(depth) + 7) / 8
			raw := make([]byte, 0, 8*(rowBytes+1))
			for range 8 {
				raw = append(raw, 0)
				raw = append(raw, bytes.Repeat([]byte{0xff}, rowBytes)...)
			}
			data := newStream(sigJNG).chunk("JHDR", jhdrOf(8, 8, 14, 8, depth, 0)).
				chunk("IDAT", deflateZlib(t, raw)).chunk("JDAT", jp).chunk("IEND", nil).bytes()
			out, _, err := extract(t, data, container.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, a := img.At(3, 3).RGBA(); a != 0xffff {
				t.Errorf("alpha %d, want opaque", a)
			}
		})
	}
	t.Run("20-bit", func(t *testing.T) {
		t.Parallel()
		data := newStream(sigJNG).chunk("JHDR", jhdrOf(8, 8, 10, 20, 0, 0)).
			chunk("JDAT", jp).chunk("JSEP", nil).chunk("JDAT", []byte("12-bit part")).chunk("IEND", nil).bytes()
		out, src, err := extract(t, data, container.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, jp) || src.Entry != "JDAT" {
			t.Errorf("got %d bytes from %q, want the 8-bit JPEG", len(out), src.Entry)
		}
	})
}

func deflateZlib(tb testing.TB, p []byte) []byte {
	tb.Helper()
	d := deflate(tb, p)
	sum := make([]byte, 4)
	var a, b uint32 = 1, 0
	for _, c := range p {
		a = (a + uint32(c)) % 65521
		b = (b + a) % 65521
	}
	binary.BigEndian.PutUint32(sum, b<<16|a)
	return append(append([]byte{0x78, 0xda}, d...), sum...)
}

// TestMNGDetectOnly: a signature without its header chunk is not a
// container.
func TestMNGDetectOnly(t *testing.T) {
	t.Parallel()
	for _, data := range [][]byte{
		[]byte(sigMNG),
		newStream(sigMNG).chunk("IHDR", make([]byte, 13)).bytes(),
		newStream(sigJNG).chunk("JHDR", make([]byte, 15)).bytes(),
	} {
		if k, ok := container.Detect(data); ok {
			t.Errorf("Detect(%q) = %v", data[:min(len(data), 16)], k)
		}
		if emb, err := container.Open(data, container.Limits{}); emb != nil || err != nil {
			t.Errorf("Open = %v, %v; want nil, nil", emb, err)
		}
	}
}

// fixCRCs rewrites the CRC of every whole chunk after the 8-byte
// signature, so that mutated chunk contents reach the parsers.
func fixCRCs(b []byte) []byte {
	b = bytes.Clone(b)
	for off := 8; off+12 <= len(b); {
		n := int64(binary.BigEndian.Uint32(b[off:]))
		if n > int64(len(b)-off-12) {
			break
		}
		end := off + 12 + int(n)
		binary.BigEndian.PutUint32(b[end-4:], crc32.ChecksumIEEE(b[off+4:end-4]))
		off = end
	}
	return b
}

// FuzzOpenMNG fuzzes MNG and JNG streams with valid CRCs.
func FuzzOpenMNG(f *testing.F) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures")
	for _, p := range []string{
		"mng/png.mng", "mng/palette.mng", "mng/frames.mng", "mng/jng.mng",
		"jng/jng.jng", "jng/jng-idat.jng", "jng/jng-jdaa.jng",
	} {
		if b, err := os.ReadFile(filepath.Join(root, p)); err == nil {
			f.Add(b)
		}
	}
	lim := container.Limits{MaxBytes: 1 << 20, MaxPixels: 1 << 16}
	f.Fuzz(func(t *testing.T, data []byte) {
		data = fixCRCs(data)
		emb, err := container.Open(data, lim)
		if err != nil {
			switch errs.KindOf(err) {
			case errs.KindCorrupt, errs.KindUnsupported, errs.KindLimitExceeded:
				return
			default:
				t.Fatalf("error kind %v: %v", errs.KindOf(err), err)
			}
		}
		if emb == nil {
			return
		}
		if int64(emb.Size()) > lim.MaxBytes {
			t.Fatalf("embedded image of %d bytes over the %d limit", emb.Size(), lim.MaxBytes)
		}
		dst := make([]byte, emb.Size())
		if err := emb.ReadInto(dst); err != nil {
			t.Fatalf("read: %v", err)
		}
	})
}
