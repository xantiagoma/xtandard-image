package wbmp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func TestDecode(t *testing.T) {
	t.Parallel()
	// 10×2: rows padded to 2 bytes; row 0 alternates, row 1 is all white.
	data := []byte{0, 0, 10, 2, 0b10101010, 0b10000000, 0xFF, 0xC0}
	d, err := Decode(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Raw.Validate(); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i := 0; i < len(d.Raw.Data); i += 3 {
		got = append(got, d.Raw.Data[i]/255)
	}
	want := []byte{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("pixels (-want +got):\n%s", diff)
	}
}

func TestDecodeConfig(t *testing.T) {
	t.Parallel()
	// A width of 200 needs two bytes: 0x81 0x48.
	wide := append([]byte{0, 0, 0x81, 0x48, 1}, make([]byte, 25)...)
	for _, tc := range []struct {
		name string
		data []byte
		w, h int
		err  bool
	}{
		{"multi-byte width", wide, 200, 1, false},
		{"trailing byte", append(append([]byte{}, wide...), 0), 0, 0, true},
		{"short", wide[:len(wide)-1], 0, 0, true},
		{"type 1", []byte{1, 0, 1, 1, 0}, 0, 0, true},
		{"extension header", []byte{0, 0x80, 1, 1, 0}, 0, 0, true},
		{"zero height", []byte{0, 0, 1, 0, 0}, 0, 0, true},
		{"endless integer", []byte{0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 1}, 0, 0, true},
		{"icon directory", []byte{0, 0, 1, 0, 1, 0, 16, 16}, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := DecodeConfig(tc.data)
			if (err != nil) != tc.err {
				t.Fatalf("DecodeConfig: %v, want error %v", err, tc.err)
			}
			if err != nil {
				if errs.KindOf(err) != errs.KindCorrupt {
					t.Errorf("kind %s, want corrupt", errs.KindOf(err))
				}
				return
			}
			if c.Width != tc.w || c.Height != tc.h {
				t.Errorf("%dx%d, want %dx%d", c.Width, c.Height, tc.w, tc.h)
			}
		})
	}
}

func TestLimit(t *testing.T) {
	t.Parallel()
	data := append([]byte{0, 0, 0x81, 0x48, 1}, make([]byte, 25)...)
	if _, err := Decode(data, Options{MaxPixels: 100}); errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("Decode: %v, want limit_exceeded", err)
	}
}

func TestFixture(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "wbmp", "graphic.wbmp"))
	if err != nil {
		t.Fatal(err)
	}
	if !Sniff(data) {
		t.Fatal("fixture not sniffed")
	}
	d, err := Decode(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Raw.Width != 48 || d.Raw.Height != 36 {
		t.Errorf("%dx%d, want 48x36", d.Raw.Width, d.Raw.Height)
	}
}

// FuzzDecode checks that Decode never panics, fails only with Corrupt or
// LimitExceeded, and agrees with Sniff.
func FuzzDecode(f *testing.F) {
	f.Add([]byte{0, 0, 10, 2, 0b10101010, 0b10000000, 0xFF, 0xC0})
	f.Add(append([]byte{0, 0, 0x81, 0x48, 1}, make([]byte, 25)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Decode(data, Options{MaxPixels: 1 << 16})
		if err != nil {
			if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindLimitExceeded {
				t.Fatalf("error kind %v: %v", k, err)
			}
			return
		}
		if !Sniff(data) {
			t.Fatal("decoded but not sniffed")
		}
		if err := d.Raw.Validate(); err != nil {
			t.Fatal(err)
		}
	})
}
