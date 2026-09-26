// Package golden compares images produced by tests with reference PNGs in
// testdata/golden/ using the perceptual distance DSSIM (package ssim), never
// encoded bytes (go-standards §8).
//
// Typical use from an executor test:
//
//	got := runPipeline(t, golden.Fixture(t, "jpeg/baseline.jpg"), opts)
//	golden.Compare(t, "exec/resize-fit-200", got, golden.Options{})
//
// Regenerating goldens: run the tests with GOLDEN_UPDATE=1, or pass -update
// to a test binary that imports this package:
//
//	GOLDEN_UPDATE=1 go test ./...
//	go test ./internal/pipeline/exec -update
//
// `go test ./... -args -update` only works when every package in the pattern
// imports golden, because other test binaries reject the unknown flag; the
// environment variable works everywhere.
//
// On a mismatch the test fails with the score and writes <name>.got.png,
// <name>.want.png and <name>.diff.png (a DSSIM heat map, see
// [ssim.Result.HeatMap]) to $GOLDEN_OUT when set, else to t.ArtifactDir()
// (kept with `go test -artifacts`).
package golden

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	_ "image/gif"  // registers the GIF decoder for Decode
	_ "image/jpeg" // registers the JPEG decoder for Decode

	_ "golang.org/x/image/bmp"  // registers the BMP decoder for Decode
	_ "golang.org/x/image/tiff" // registers the TIFF decoder for Decode
	_ "golang.org/x/image/webp" // registers the WebP decoder for Decode

	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
)

// DefaultTolerance is the DSSIM above which Compare fails when
// Options.Tolerance is zero. It is calibrated in golden_test.go on the photo
// fixtures (which carry sensor-like noise, the hardest case for JPEG): a
// stdlib JPEG q85 re-encode scores 0.003-0.008 and passes, a 1 px shift
// scores 0.023-0.081 and fails. Deterministic steps that should reproduce a
// golden almost exactly can pass a tighter Options.Tolerance.
//
// The scale is this package's single-scale luma DSSIM; it is not comparable
// with numbers printed by the `dssim` tool (multi-scale, L*a*b*).
const DefaultTolerance = 0.015

// Environment variables read by the harness. Tests are the one place that
// may read the environment directly (go-standards §8, integration tests).
const (
	EnvUpdate = "GOLDEN_UPDATE" // "1" or "true": rewrite goldens instead of comparing
	EnvOut    = "GOLDEN_OUT"    // directory for got/want/diff PNGs on failure
)

// updateFlag is the -update test flag. A test flag has to be registered while
// the test binary initialises, before testing parses the command line, so it
// is the package's one package-level variable (see the WP-36 deviations).
var updateFlag = flag.Bool("update", false, "rewrite testdata/golden images instead of comparing (see package golden)")

// Options tune one comparison. The zero value is the default.
type Options struct {
	// Tolerance is the maximum DSSIM; 0 means DefaultTolerance.
	Tolerance float64
	// Dir is the golden directory; empty means <module root>/testdata/golden.
	Dir string
	// Update forces writing the golden, as -update / GOLDEN_UPDATE=1 do.
	Update bool
}

// Compare fails t when got differs from testdata/golden/<name>.png by more
// than the tolerance, or when the golden is missing. name may contain slashes
// to group goldens by package. In update mode it writes got as the golden
// (creating directories) and logs the previous score when there was one.
func Compare(tb testing.TB, name string, got image.Image, opts Options) {
	tb.Helper()
	dir := opts.Dir
	if dir == "" {
		dir = filepath.Join(ModuleRoot(tb), "testdata", "golden")
	}
	path := filepath.Join(dir, filepath.FromSlash(name)+".png")
	tol := opts.Tolerance
	if tol == 0 {
		tol = DefaultTolerance
	}

	want, err := readPNG(path)
	if opts.Update || updateRequested() {
		if err == nil {
			if res, cerr := ssim.Compare(want, got); cerr == nil {
				tb.Logf("golden %s: previous DSSIM %.6f", name, res.DSSIM)
			}
		}
		if err := writePNG(path, got); err != nil {
			tb.Errorf("golden %s: write: %v", name, err)
			return
		}
		tb.Logf("golden %s: updated %s", name, path)
		return
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			tb.Errorf("golden %s: %s does not exist; run with GOLDEN_UPDATE=1 (or -update) to create it", name, path)
		} else {
			tb.Errorf("golden %s: %v", name, err)
		}
		return
	}
	gs, ws := got.Bounds().Size(), want.Bounds().Size()
	if gs != ws {
		tb.Errorf("golden %s: size %dx%d, want %dx%d", name, gs.X, gs.Y, ws.X, ws.Y)
		dump(tb, name, got, want, nil)
		return
	}
	res, err := ssim.Compare(want, got)
	if err != nil {
		tb.Errorf("golden %s: %v", name, err)
		return
	}
	if res.DSSIM > tol {
		tb.Errorf("golden %s: DSSIM %.6f > tolerance %.6f (SSIM %.6f)", name, res.DSSIM, tol, res.SSIM)
		dump(tb, name, got, want, res.HeatMap())
		return
	}
	tb.Logf("golden %s: DSSIM %.6f (tolerance %.6f)", name, res.DSSIM, tol)
}

func updateRequested() bool {
	if *updateFlag {
		return true
	}
	v, _ := os.LookupEnv(EnvUpdate)
	return v == "1" || v == "true"
}

// dump writes got, want and the heat map next to each other for inspection.
func dump(tb testing.TB, name string, got, want, diff image.Image) {
	tb.Helper()
	dir, ok := os.LookupEnv(EnvOut)
	if !ok || dir == "" {
		dir = tb.ArtifactDir()
	}
	base := filepath.Join(dir, filepath.FromSlash(name))
	files := []struct {
		suffix string
		img    image.Image
	}{{".got.png", got}, {".want.png", want}, {".diff.png", diff}}
	for _, f := range files {
		if f.img == nil {
			continue
		}
		if err := writePNG(base+f.suffix, f.img); err != nil {
			tb.Errorf("golden %s: %v", name, err)
			return
		}
	}
	tb.Logf("golden %s: wrote %s.{got,want,diff}.png", name, base)
}

func readPNG(path string) (image.Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

// ModuleRoot returns the directory holding go.mod, found by walking up from
// the test's working directory (its package directory).
func ModuleRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("golden: go.mod not found above the working directory")
		}
		dir = parent
	}
}

// Fixture returns the path of testdata/fixtures/<name> (for example
// "jpeg/baseline.jpg") and fails t when it does not exist.
func Fixture(tb testing.TB, name string) string {
	tb.Helper()
	p := filepath.Join(ModuleRoot(tb), "testdata", "fixtures", filepath.FromSlash(name))
	if _, err := os.Stat(p); err != nil {
		tb.Fatalf("fixture %s: %v", name, err)
	}
	return p
}

// Decode decodes a PNG, JPEG, GIF, WebP, BMP or TIFF file with the Go
// decoders (formats libvips-free tests can read) and fails t on error.
func Decode(tb testing.TB, path string) image.Image {
	tb.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		tb.Fatalf("decode %s: %v", path, err)
	}
	return img
}
