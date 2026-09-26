package detect

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Paths of the full base image (docker/base, WP-70). Tests that need the
// runtime skip elsewhere (host, slim builder); TestUnavailable covers slim.
const (
	testModelsDir   = "/usr/share/xtandard/models"
	testFaceModel   = "face_detection_yunet_2023mar.onnx"
	testObjectModel = "yolox_s.onnx"
	fixturesDir     = "../../testdata/fixtures/detect"
	expectedFile    = "../../testdata/detect/expected.json"
)

func TestMain(m *testing.M) {
	cfg := vips.DefaultConfig()
	// Without cgo, libvips and ONNX Runtime are both unavailable: the
	// pure-Go tests still run and the rest skip.
	if err := vips.Init(&cfg); err != nil && !errors.Is(err, vips.ErrUnavailable) {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

func testLibrary() string {
	if runtime.GOOS == "darwin" {
		return "libonnxruntime.dylib"
	}
	return "libonnxruntime.so.1"
}

func testOptions() *Options {
	return &Options{
		Library:     testLibrary(),
		ModelsDir:   testModelsDir,
		FaceModel:   testFaceModel,
		ObjectModel: testObjectModel,
		Threads:     1,
	}
}

// newTestDetector loads both shipped models or skips when the runtime or
// the models are not installed.
func newTestDetector(tb testing.TB, opts *Options) *Detector {
	tb.Helper()
	d, err := New(opts)
	if errors.Is(err, ErrUnavailable) {
		tb.Skipf("detection unavailable here: %v", err)
	}
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(d.Close)
	return d
}

// withImage loads a fixture upright (EXIF orientation applied, as the
// pipeline does) and runs fn on it inside vips.Run.
func withImage(tb testing.TB, name string, fn func(img *vips.Image) error) {
	tb.Helper()
	ctx := tb.Context()
	f, err := os.Open(filepath.Join(fixturesDir, name))
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	buf, err := vips.NewBufferFromReader(f, 64<<20)
	if err != nil {
		tb.Fatal(err)
	}
	defer buf.Release()
	err = vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		img, err := a.Add(vips.Load(ctx, buf, nil))
		if err != nil {
			return err
		}
		if img, err = a.Add(img.Autorot(ctx)); err != nil {
			return err
		}
		return fn(img)
	})
	if err != nil {
		tb.Fatal(err)
	}
}
