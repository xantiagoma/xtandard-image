package codec_test

import (
	"encoding/binary"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// dosEPS wraps ps and a preview in a DOS EPS binary header.
func dosEPS(ps, tiff []byte) []byte {
	var h [30]byte
	copy(h[:], "\xc5\xd0\xd3\xc6")
	le := binary.LittleEndian
	le.PutUint32(h[4:], 30)
	le.PutUint32(h[8:], uint32(len(ps))) //nolint:gosec // test data is small
	if tiff != nil {
		le.PutUint32(h[20:], uint32(30+len(ps))) //nolint:gosec // test data is small
		le.PutUint32(h[24:], uint32(len(tiff)))  //nolint:gosec // test data is small
	}
	return append(append(h[:], ps...), tiff...)
}

func TestParseEPSHeader(t *testing.T) {
	t.Parallel()
	box := func(llx, lly, urx, ury float64) codec.EPSBox {
		return codec.EPSBox{LLX: llx, LLY: lly, URX: urx, URY: ury}
	}
	ps := "%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 10 10\n%%EndComments\n0 0 10 10 rectfill\n"
	tests := []struct {
		name string
		data []byte
		want codec.EPSHeader
		kind errs.Kind
	}{
		{
			"bounding box", []byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 10 20 110 70\n%%EndComments\n"),
			codec.EPSHeader{Box: box(10, 20, 110, 70), Pages: 1, EPSF: true},
			0,
		},
		{
			"hires wins, even after", []byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 11 11\n%%HiResBoundingBox: 0.5 0.25 10.5 10.75\n"),
			codec.EPSHeader{Box: box(0.5, 0.25, 10.5, 10.75), Pages: 1, EPSF: true},
			0,
		},
		{
			"first header value wins", []byte("%!PS-Adobe-3.0\n%%BoundingBox: 0 0 5 5\n%%BoundingBox: 0 0 9 9\n"),
			codec.EPSHeader{Box: box(0, 0, 5, 5), Pages: 1},
			0,
		},
		{
			"atend from the trailer", []byte("%!PS-Adobe-3.0\n%%BoundingBox: (atend)\n%%Pages: (atend)\n%%EndComments\nshowpage\n%%Trailer\n%%BoundingBox: 1 2 30 40\n%%Pages: 4\n%%EOF\n"),
			codec.EPSHeader{Box: box(1, 2, 30, 40), Pages: 4},
			0,
		},
		{
			"comments end at the first code line", []byte("%!PS-Adobe-3.0\n%%Pages: 2\n/x 1 def\n%%BoundingBox: 0 0 9 9\n"),
			codec.EPSHeader{Box: box(0, 0, 612, 792), Pages: 2},
			0,
		},
		{
			"document media", []byte("%!PS-Adobe-3.0\n%%DocumentMedia: A4 595 842 0 () ()\n%%Pages: 3\n"),
			codec.EPSHeader{Box: box(0, 0, 595, 842), Pages: 3},
			0,
		},
		{"no box: letter", []byte("%!PS\n"), codec.EPSHeader{Box: box(0, 0, 612, 792), Pages: 1}, 0},
		{
			"mac line ends", []byte("%!PS-Adobe-3.0 EPSF-3.0\r%%BoundingBox: 0 0 20 30\r%%EndComments\r"),
			codec.EPSHeader{Box: box(0, 0, 20, 30), Pages: 1, EPSF: true},
			0,
		},
		{"ctrl-d", []byte("\x04%!PS-Adobe-3.0\n%%BoundingBox: 0 0 20 30\n"), codec.EPSHeader{Box: box(0, 0, 20, 30), Pages: 1}, 0},
		{
			"illustrator", []byte("%!PS-Adobe-3.0\n%%Creator: Adobe Illustrator(R) 8.0\n%%BoundingBox: 0 0 20 30\n"),
			codec.EPSHeader{Box: box(0, 0, 20, 30), Pages: 1, Illustrator: true},
			0,
		},
		{
			"illustrator private comment", []byte("%!PS-Adobe-3.0\n%AI5_FileFormat 4.0\n%%BoundingBox: 0 0 20 30\n"),
			codec.EPSHeader{Box: box(0, 0, 20, 30), Pages: 1, Illustrator: true},
			0,
		},
		{
			"dos eps", dosEPS([]byte(ps), []byte("II*\x00preview")),
			codec.EPSHeader{Box: box(0, 0, 10, 10), Pages: 1, EPSF: true, PSOffset: 30, PSLength: len(ps), PreviewOffset: 30 + len(ps), PreviewLength: 11},
			0,
		},
		{
			"dos eps preview not tiff", dosEPS([]byte(ps), []byte("GIF89a")),
			codec.EPSHeader{Box: box(0, 0, 10, 10), Pages: 1, EPSF: true, PSOffset: 30, PSLength: len(ps)},
			0,
		},
		{"dos eps section outside the file", dosEPS([]byte(ps), nil)[:40], codec.EPSHeader{}, errs.KindCorrupt},
		{"dos eps short header", []byte("\xc5\xd0\xd3\xc6\x1e\x00\x00\x00"), codec.EPSHeader{}, errs.KindCorrupt},
		{"not postscript", []byte("hello"), codec.EPSHeader{}, errs.KindCorrupt},
		{"empty box", []byte("%!PS\n%%BoundingBox: 10 10 10 10\n"), codec.EPSHeader{}, errs.KindCorrupt},
		{"inverted box", []byte("%!PS\n%%BoundingBox: 10 10 0 0\n"), codec.EPSHeader{}, errs.KindCorrupt},
		{"huge box", []byte("%!PS\n%%BoundingBox: 0 0 100000 100\n"), codec.EPSHeader{}, errs.KindCorrupt},
		{"nan box", []byte("%!PS\n%%BoundingBox: 0 0 NaN 100\n"), codec.EPSHeader{}, errs.KindCorrupt},
		{"garbage box", []byte("%!PS\n%%BoundingBox: a b c d\n"), codec.EPSHeader{}, errs.KindCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := codec.ParseEPSHeader(tt.data)
			if tt.kind != 0 {
				if errs.KindOf(err) != tt.kind {
					t.Fatalf("ParseEPSHeader error %v, want kind %s", err, tt.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEPSHeader: %v", err)
			}
			want := tt.want
			if want.PSLength == 0 {
				want.PSLength = len(tt.data)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("ParseEPSHeader (-want +got):\n%s", diff)
			}
		})
	}
}

// FuzzParseEPSHeader: the parser never panics, and a header it accepts
// describes sections inside the data and a positive, bounded page.
func FuzzParseEPSHeader(f *testing.F) {
	for _, name := range []string{"eps/simple.eps", "eps/pages.ps", "eps/illustrator.ai", "eps/preview.eps"} {
		f.Add(readFixture(f, name))
	}
	f.Add([]byte("%!PS-Adobe-3.0\n%%BoundingBox: (atend)\n%%EndComments\n%%Trailer\n%%BoundingBox: 1 2 3 4\n"))
	f.Add(dosEPS([]byte("%!PS\n"), []byte("II*\x00")))
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := codec.ParseEPSHeader(b)
		if err != nil {
			return
		}
		if ps := h.PostScript(b); len(ps) == 0 {
			t.Fatalf("empty PostScript section %+v", h)
		}
		_ = h.TIFFPreview(b)
		if w, ht := h.Box.Width(), h.Box.Height(); !(w > 0 && ht > 0 && w <= 40000 && ht <= 40000) {
			t.Fatalf("page %gx%g", w, ht)
		}
		if h.Pages < 1 {
			t.Fatalf("pages %d", h.Pages)
		}
	})
}
