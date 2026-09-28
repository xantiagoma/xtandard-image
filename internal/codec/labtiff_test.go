package codec_test

import (
	"encoding/binary"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
)

func TestParseLabTIFF(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		ok      bool
		bits    int
		pages   []bool
	}{
		{"tiff/lab8.tif", true, 8, []bool{true}},
		{"tiff/lab8-alpha.tif", true, 8, []bool{true}},
		{"tiff/lab8-icc.tif", true, 8, []bool{true}},
		{"tiff/lab16.tif", true, 16, []bool{true}},
		{"tiff/multipage.tif", false, 0, nil},
		{"jpeg/baseline.jpg", false, 0, nil},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			l, ok := codec.ParseLabTIFF(readFixture(t, tc.fixture))
			if ok != tc.ok || l.Bits != tc.bits || len(l.Pages) != len(tc.pages) {
				t.Fatalf("ParseLabTIFF = %+v, %v; want bits %d pages %v, %v", l, ok, tc.bits, tc.pages, tc.ok)
			}
			for i, p := range tc.pages {
				if l.Page(i) != p {
					t.Errorf("page %d lab = %v, want %v", i, l.Page(i), p)
				}
			}
		})
	}
}

// TestParseLabTIFFRefuses covers the CIELab files left to libvips: JPEG
// compression (libtiff converts those itself) and fewer than 3 samples.
func TestParseLabTIFFRefuses(t *testing.T) {
	t.Parallel()
	base := readFixture(t, "tiff/lab8.tif")
	set := func(tag uint16, v uint16) []byte {
		b := append([]byte(nil), base...)
		le := binary.LittleEndian
		off := int(le.Uint32(b[4:]))
		for i := range int(le.Uint16(b[off:])) {
			e := off + 2 + 12*i
			if le.Uint16(b[e:]) == tag {
				le.PutUint16(b[e+8:], v)
			}
		}
		return b
	}
	for name, b := range map[string][]byte{
		"jpeg":      set(259, 7),
		"1 sample":  set(277, 1),
		"12 bits":   set(258, 12),
		"rgb":       set(262, 2),
		"truncated": base[:20],
	} {
		if _, ok := codec.ParseLabTIFF(b); ok {
			t.Errorf("%s: ParseLabTIFF accepted it", name)
		}
	}
}

// FuzzParseLabTIFF: the IFD walk must not panic on any input and must only
// accept files whose first page it can relabel.
func FuzzParseLabTIFF(f *testing.F) {
	for _, name := range []string{"tiff/lab8.tif", "tiff/lab16.tif", "tiff/lab8-icc.tif", "tiff/multipage.tif"} {
		f.Add(readFixture(f, name)[:min(512, len(readFixture(f, name)))])
		f.Add(readFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		l, ok := codec.ParseLabTIFF(b)
		if !ok {
			return
		}
		if !l.Page(0) || (l.Bits != 8 && l.Bits != 16) || len(l.Pages) > 1024 {
			t.Fatalf("ParseLabTIFF = %+v", l)
		}
	})
}
