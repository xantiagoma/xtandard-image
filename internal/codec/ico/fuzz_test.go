package ico

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// FuzzDecodeICO checks that Decode never panics, fails only with Corrupt or
// LimitExceeded, and returns exactly one well-formed result.
func FuzzDecodeICO(f *testing.F) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.ico"))
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, -1)
	}
	f.Add(build(typeIcon, dib4()), 0)
	f.Add(build(typeCursor, dib4(), pngBytes(f, testRaw(3, 2, 4))), 1)
	enc, err := Encode(testRaw(16, 16, 4), testRaw(5, 3, 3), testRaw(60, 2, 4))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(enc, -1)

	f.Fuzz(func(t *testing.T, data []byte, page int) {
		o := Options{MaxPixels: 1 << 16, UsePage: page >= 0, Page: page, MinWidth: 20, MinHeight: 20}
		d, err := Decode(data, o)
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
		if _, err := Entries(data); err != nil {
			t.Fatalf("Decode succeeded but Entries failed: %v", err)
		}
	})
}
