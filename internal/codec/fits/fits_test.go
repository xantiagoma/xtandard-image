package fits_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/fits"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "fits", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// card is one 80-column header card.
func card(key string, value any) string {
	var v string
	switch x := value.(type) {
	case bool:
		v = fmt.Sprintf("%20s", map[bool]string{true: "T", false: "F"}[x])
	case int:
		v = fmt.Sprintf("%20d", x)
	case string:
		v = fmt.Sprintf("'%-8s'", x)
	default:
		v = fmt.Sprint(x)
	}
	return fmt.Sprintf("%-8s= %-70s", key, v)
}

// hdu builds one HDU: the cards, END, padding and the padded data.
func hdu(cards []string, data []byte) []byte {
	h := strings.Join(cards, "") + fmt.Sprintf("%-80s", "END")
	h += strings.Repeat(" ", (2880-len(h)%2880)%2880)
	out := append([]byte(h), data...)
	return append(out, make([]byte, (2880-len(out)%2880)%2880)...)
}

// primary builds a primary image HDU with BITPIX and the given axes.
func primary(bitpix int, data []byte, axes ...int) []byte {
	c := make([]string, 0, 3+len(axes))
	c = append(c, card("SIMPLE", true), card("BITPIX", bitpix), card("NAXIS", len(axes)))
	for i, a := range axes {
		c = append(c, card(fmt.Sprintf("NAXIS%d", i+1), a))
	}
	return hdu(c, data)
}

func TestSniff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []byte
		want bool
	}{
		{"fixture", fixture(t, "u8.fits"), true},
		{"false", []byte(card("SIMPLE", false)), false},
		{"short", []byte("SIMPLE  =                    T"), false},
		{"other", []byte(card("XTENSION", "IMAGE")), false},
		{"empty", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := fits.Sniff(tc.in); got != tc.want {
				t.Errorf("Sniff = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecodeConfig(t *testing.T) {
	t.Parallel()
	grey := func(bitpix, hdu int, compressed bool) fits.Config {
		return fits.Config{Width: 64, Height: 48, Planes: 1, Bitpix: bitpix, HDU: hdu, Compressed: compressed}
	}
	tests := []struct {
		file string
		want fits.Config
	}{
		{"u8.fits", grey(8, 0, false)},
		{"u16.fits", grey(16, 0, false)},
		{"f32-nan.fits", grey(-32, 0, false)},
		{"f64.fits", grey(-64, 0, false)},
		{"rgb16.fits", fits.Config{Width: 64, Height: 48, Colour: true, Planes: 3, Bitpix: 16}},
		{"mef-i32.fits", grey(32, 2, false)},
		{"rice-u16.fits.fz", grey(16, 1, true)},
		{"rice-f32.fits.fz", grey(-32, 1, true)},
		{"gzip2-f32.fits.fz", grey(-32, 1, true)},
		{"dither2-f32.fits.fz", grey(-32, 1, true)},
		{"gzip1-mef-i32.fits.fz", grey(32, 2, true)},
		{"hcompress-u16.fits.fz", grey(16, 1, true)},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			got, err := fits.DecodeConfig(fixture(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("DecodeConfig (-want +got):\n%s", diff)
			}
		})
	}
}

func decode(t *testing.T, b []byte) *raster.Raw {
	t.Helper()
	d, err := fits.Decode(b, fits.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Raw == nil || d.Float != nil || d.Embedded != nil {
		t.Fatalf("Decode = %+v, want Raw only", d)
	}
	if err := d.Raw.Validate(); err != nil {
		t.Fatal(err)
	}
	return d.Raw
}

// mean returns the mean of each band.
func mean(r *raster.Raw) [3]float64 {
	var s [3]float64
	for i, v := range r.Data {
		s[i%3] += float64(v)
	}
	n := float64(r.Width * r.Height)
	return [3]float64{s[0] / n, s[1] / n, s[2] / n}
}

func TestDecodeFixtures(t *testing.T) {
	t.Parallel()
	for _, file := range []string{
		"u8.fits", "u16.fits", "f32-nan.fits", "f64.fits", "rgb16.fits", "mef-i32.fits",
		"rice-u16.fits.fz", "rice-f32.fits.fz", "gzip2-f32.fits.fz", "dither2-f32.fits.fz",
		"gzip1-mef-i32.fits.fz",
	} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			r := decode(t, fixture(t, file))
			if r.Width != 64 || r.Height != 48 || r.Bands != 3 {
				t.Fatalf("decoded %dx%dx%d, want 64x48x3", r.Width, r.Height, r.Bands)
			}
			m := mean(r)
			// Every stretched image lands its median near a quarter of full
			// scale; the identity-drawn u8 is the photo's own brightness.
			for b, v := range m {
				if v < 20 || v > 200 {
					t.Errorf("band %d mean %.1f out of range", b, v)
				}
			}
			if file != "rgb16.fits" && (m[0] != m[1] || m[1] != m[2]) {
				t.Errorf("grey image has different bands: %v", m)
			}
		})
	}
}

func TestIdentity8Upright(t *testing.T) {
	t.Parallel()
	b := fixture(t, "u8.fits")
	r := decode(t, b)
	// The data unit starts after one header block; FITS row 0 is the
	// bottom row, so output row 47 is data row 0.
	data := b[2880:]
	for _, p := range [][2]int{{0, 0}, {10, 5}, {63, 47}, {32, 24}} {
		x, y := p[0], p[1]
		want := data[(47-y)*64+x]
		if got := r.Data[(y*64+x)*3]; got != want {
			t.Errorf("pixel (%d,%d) = %d, want %d", x, y, got, want)
		}
	}
}

func TestFlip(t *testing.T) {
	t.Parallel()
	// A 1x2 16-bit image: the first stored row (bottom) is dark, the
	// second (top) bright.
	d := make([]byte, 4)
	binary.BigEndian.PutUint16(d[2:], 1000)
	r := decode(t, primary(16, d, 1, 2))
	if r.Data[0] <= r.Data[3] {
		t.Errorf("top %d, bottom %d: want the top brighter", r.Data[0], r.Data[3])
	}
}

func TestNaNDrawnBlack(t *testing.T) {
	t.Parallel()
	r := decode(t, fixture(t, "f32-nan.fits"))
	// The generator wrote NaN at FITS x, y in [2, 6): output rows 42..45.
	for y := 42; y < 46; y++ {
		for x := 2; x < 6; x++ {
			if v := r.Data[(y*64+x)*3]; v != 0 {
				t.Fatalf("NaN pixel (%d,%d) = %d, want 0", x, y, v)
			}
		}
	}
	if v := r.Data[(40*64+10)*3]; v == 0 {
		t.Error("finite pixel drawn black")
	}
}

func TestBlankDrawnBlack(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"mef-i32.fits", "gzip1-mef-i32.fits.fz"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			r := decode(t, fixture(t, file))
			// BLANK at FITS x < 3, y >= 45: output rows 0..2.
			for y := range 3 {
				for x := range 3 {
					if v := r.Data[(y*64+x)*3]; v != 0 {
						t.Fatalf("BLANK pixel (%d,%d) = %d, want 0", x, y, v)
					}
				}
			}
		})
	}
}

func TestLosslessTilesMatch(t *testing.T) {
	t.Parallel()
	tests := []struct{ plain, packed string }{
		{"u16.fits", "rice-u16.fits.fz"},
		{"mef-i32.fits", "gzip1-mef-i32.fits.fz"},
	}
	for _, tc := range tests {
		t.Run(tc.packed, func(t *testing.T) {
			t.Parallel()
			want := decode(t, fixture(t, tc.plain))
			got := decode(t, fixture(t, tc.packed))
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("tile-compressed decode differs (-plain +packed):\n%s", diff)
			}
		})
	}
}

func TestColour(t *testing.T) {
	t.Parallel()
	m := mean(decode(t, fixture(t, "rgb16.fits")))
	if m[0] == m[1] && m[1] == m[2] {
		t.Errorf("colour image drawn grey: %v", m)
	}
}

func TestCubeDrawsFirstPlane(t *testing.T) {
	t.Parallel()
	// Two 2x1 planes: plane 0 is a ramp, plane 1 constant; only plane 0
	// shows, as grey.
	d := make([]byte, 8)
	binary.BigEndian.PutUint16(d[2:], 100)
	binary.BigEndian.PutUint16(d[4:], 7)
	binary.BigEndian.PutUint16(d[6:], 7)
	b := primary(16, d, 2, 1, 2)
	c, err := fits.DecodeConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Planes != 2 || c.Colour {
		t.Fatalf("config %+v, want 2 planes, not colour", c)
	}
	r := decode(t, b)
	if r.Data[0] >= r.Data[3] || r.Data[3] != r.Data[4] || r.Data[4] != r.Data[5] {
		t.Errorf("pixels %v: want a grey ramp from plane 0", r.Data)
	}
}

func TestConstantImageMidGrey(t *testing.T) {
	t.Parallel()
	d := make([]byte, 4*4)
	for i := range 4 {
		binary.BigEndian.PutUint32(d[4*i:], math.Float32bits(3.5))
	}
	r := decode(t, primary(-32, d, 2, 2))
	for i, v := range r.Data {
		if v != 128 {
			t.Fatalf("sample %d = %d, want 128", i, v)
		}
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	u8 := fixture(t, "u8.fits")
	emptyPrimary := primary(8, nil)
	tests := []struct {
		name string
		in   []byte
		opts fits.Options
		kind errs.Kind
	}{
		{"not fits", []byte("hello"), fits.Options{}, errs.KindCorrupt},
		{"truncated header", u8[:1000], fits.Options{}, errs.KindCorrupt},
		{"truncated data", u8[:2880+100], fits.Options{}, errs.KindCorrupt},
		{"no END", []byte(card("SIMPLE", true) + strings.Repeat(" ", 2800)), fits.Options{}, errs.KindCorrupt},
		{"bad bitpix", primary(12, make([]byte, 4), 2, 2), fits.Options{}, errs.KindCorrupt},
		{"no image", emptyPrimary, fits.Options{}, errs.KindUnsupported},
		{"four axes", primary(8, make([]byte, 8), 2, 2, 1, 2), fits.Options{}, errs.KindUnsupported},
		{"hcompress", fixture(t, "hcompress-u16.fits.fz"), fits.Options{}, errs.KindUnsupported},
		{"limit", u8, fits.Options{MaxPixels: 100}, errs.KindLimitExceeded},
		{"huge axes", primary(8, nil, 1<<21, 1), fits.Options{}, errs.KindCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fits.Decode(tc.in, tc.opts)
			if err == nil {
				t.Fatal("Decode succeeded")
			}
			if got := errs.KindOf(err); got != tc.kind {
				t.Errorf("kind %v, want %v (%v)", got, tc.kind, err)
			}
		})
	}
}
