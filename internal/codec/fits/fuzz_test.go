package fits_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/fits"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// addFixtures seeds f with every FITS fixture.
func addFixtures(f *testing.F) {
	f.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "testdata", "fixtures", "fits", "*"))
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
}

// checkKind fails on an error outside the kinds the decoder may return.
func checkKind(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	switch errs.KindOf(err) {
	case errs.KindCorrupt, errs.KindUnsupported, errs.KindLimitExceeded:
	default:
		t.Fatalf("unexpected error kind %v: %v", errs.KindOf(err), err)
	}
}

func FuzzDecodeConfig(f *testing.F) {
	addFixtures(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := fits.DecodeConfig(b)
		checkKind(t, err)
		if err == nil && (c.Width < 1 || c.Height < 1 || c.Planes < 1) {
			t.Fatalf("DecodeConfig accepted %+v", c)
		}
	})
}

func FuzzDecode(f *testing.F) {
	addFixtures(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := fits.Decode(b, fits.Options{MaxPixels: 1 << 16})
		checkKind(t, err)
		if err != nil {
			return
		}
		if d.Raw == nil {
			t.Fatal("no pixels")
		}
		if err := d.Raw.Validate(); err != nil {
			t.Fatal(err)
		}
	})
}
