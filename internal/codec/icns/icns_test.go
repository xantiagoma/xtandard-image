package icns

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// pack is the inverse of unpack: runs of 3+ equal bytes become repeats,
// everything else literals (at most 128 at a time).
func pack(src []byte) []byte {
	var out []byte
	for i := 0; i < len(src); {
		run := 1
		for i+run < len(src) && src[i+run] == src[i] && run < 130 {
			run++
		}
		if run >= 3 {
			out = append(out, byte(0x80+run-3), src[i])
			i += run
			continue
		}
		start := i
		for i < len(src) && i-start < 128 {
			if i+2 < len(src) && src[i] == src[i+1] && src[i] == src[i+2] {
				break
			}
			i++
		}
		out = append(out, byte(i-start-1)) //nolint:gosec // G115: 0..127, a literal run length
		out = append(out, src[start:i]...)
	}
	return out
}

func block(t string, data []byte) []byte {
	b := make([]byte, 8, 8+len(data))
	copy(b, t)
	binary.BigEndian.PutUint32(b[4:], uint32(8+len(data))) //nolint:gosec // G115: small test blocks
	return append(b, data...)
}

func file(blocks ...[]byte) []byte {
	body := bytes.Join(blocks, nil)
	b := make([]byte, 8, 8+len(body))
	copy(b, magic)
	binary.BigEndian.PutUint32(b[4:], uint32(8+len(body))) //nolint:gosec // G115: small test files
	return append(b, body...)
}

// planes returns 16×16 R, G, B and A planes of a small pattern.
func planes() (r, g, b, a []byte) {
	const side = 16
	n := side * side
	r, g, b, a = make([]byte, n), make([]byte, n), make([]byte, n), make([]byte, n)
	for i := range n {
		x, y := i%side, i/side
		r[i], g[i], b[i], a[i] = byte(x*16), byte(y*16), 200, byte(255-x*8)
	}
	return r, g, b, a
}

func pngOf(tb testing.TB, side int) []byte {
	tb.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnpackRoundTrip(t *testing.T) {
	t.Parallel()
	r, g, b, a := planes()
	for _, p := range [][]byte{r, g, b, a, bytes.Repeat([]byte{7}, 300), {1, 2, 3}} {
		got := make([]byte, len(p))
		end, ok := unpack(got, pack(p), 0)
		if !ok || end != len(pack(p)) || !bytes.Equal(got, p) {
			t.Errorf("unpack(pack(%d bytes)) = %v, %d, %v", len(p), ok, end, got[:min(8, len(got))])
		}
	}
}

func TestDecode(t *testing.T) {
	t.Parallel()
	r, g, b, a := planes()
	rgb := append(append(pack(r), pack(g)...), pack(b)...)
	argb := append([]byte("ARGB"), append(append(append(pack(a), pack(r)...), pack(g)...), pack(b)...)...)
	for _, tc := range []struct {
		name     string
		data     []byte
		embedded bool
		kind     Kind
		w, h     int
		alpha    []byte // expected alpha plane (nil: opaque)
	}{
		{"classic with mask", file(block("is32", rgb), block("s8mk", a)), false, KindRGB, 16, 16, a},
		{"classic without mask", file(block("is32", rgb)), false, KindRGB, 16, 16, nil},
		{"classic raw planes", file(block("is32", append(append(append([]byte{}, r...), g...), b...))), false, KindRGB, 16, 16, nil},
		{"it32 zero prefix", file(block("it32", append([]byte{0, 0, 0, 0}, bytes.Repeat(pack(make([]byte, 128*128)), 3)...))), false, KindRGB, 128, 128, nil},
		{"argb", file(block("ic04", argb)), false, KindARGB, 16, 16, a},
		{"png wins", file(block("is32", rgb), block("ic07", pngOf(t, 128)), block("TOC ", nil)), true, KindPNG, 128, 128, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entries, err := Entries(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			e := Pick(entries)
			if e.Kind != tc.kind || e.Width != tc.w || e.Height != tc.h {
				t.Errorf("picked %s kind %d %dx%d, want kind %d %dx%d", e.Type, e.Kind, e.Width, e.Height, tc.kind, tc.w, tc.h)
			}
			d, err := Decode(tc.data, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.embedded {
				if d.Embedded == nil || !bytes.HasPrefix(d.Embedded, []byte(pngSig)) {
					t.Errorf("want the embedded PNG, got %+v", d)
				}
				return
			}
			if err := d.Raw.Validate(); err != nil {
				t.Fatal(err)
			}
			if tc.w == 16 {
				px := d.Raw.Data[4*(3*16+5):][:3] // (5, 3)
				if diff := cmp.Diff([]byte{r[3*16+5], g[3*16+5], b[3*16+5]}, px); diff != "" {
					t.Errorf("pixel (5,3) (-want +got):\n%s", diff)
				}
			}
			for i := 3; i < len(d.Raw.Data); i += 4 {
				want := byte(255)
				if tc.alpha != nil {
					want = tc.alpha[i/4]
				}
				if d.Raw.Data[i] != want {
					t.Fatalf("alpha of pixel %d = %d, want %d", i/4, d.Raw.Data[i], want)
				}
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	r, _, _, _ := planes()
	for _, tc := range []struct {
		name string
		data []byte
		o    Options
		want errs.Kind
	}{
		{"not icns", []byte("PNG not really an icns"), Options{}, errs.KindCorrupt},
		{"no usable entry", file(block("ICN#", make([]byte, 256))), Options{}, errs.KindCorrupt},
		{"truncated runs", file(block("is32", pack(r))), Options{}, errs.KindCorrupt},
		{"limit", file(block("ic07", pngOf(t, 128))), Options{MaxPixels: 1000}, errs.KindLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode(tc.data, tc.o); errs.KindOf(err) != tc.want {
				t.Errorf("Decode: %v (kind %s), want %s", err, errs.KindOf(err), tc.want)
			}
		})
	}
}

func TestFixture(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "icns", "icon.icns"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Entries(data)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Type)
	}
	if diff := cmp.Diff([]string{"is32", "ic07"}, got); diff != "" {
		t.Errorf("entries (-want +got):\n%s", diff)
	}
	if e := Pick(entries); e.Type != "ic07" || e.Width != 128 {
		t.Errorf("picked %s %dx%d, want ic07 128x128", e.Type, e.Width, e.Height)
	}
}

// FuzzDecode checks that Decode never panics, fails only with Corrupt or
// LimitExceeded, and returns exactly one well-formed result.
func FuzzDecode(f *testing.F) {
	r, g, b, a := planes()
	rgb := append(append(pack(r), pack(g)...), pack(b)...)
	f.Add(file(block("is32", rgb), block("s8mk", a)))
	f.Add(file(block("ic04", append([]byte("ARGB"), append(pack(a), rgb...)...))))
	f.Add(file(block("ic07", pngOf(f, 32))))
	f.Add(file(block("ic09", []byte(jp2Sig+"\x00\x00\x00\x2djp2h\x00\x00\x00\x16ihdr\x00\x00\x00\x40\x00\x00\x00\x40"))))
	if data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "icns", "icon.icns")); err == nil {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Decode(data, Options{MaxPixels: 1 << 16})
		if err != nil {
			if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindLimitExceeded {
				t.Fatalf("error kind %v: %v", k, err)
			}
			return
		}
		if (d.Raw == nil) == (d.Embedded == nil) {
			t.Fatalf("want exactly one of Raw and Embedded, got %+v", d)
		}
		if d.Raw != nil {
			if err := d.Raw.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
