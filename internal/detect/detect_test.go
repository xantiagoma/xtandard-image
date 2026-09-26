package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// expected is testdata/detect/expected.json: the detections of the
// reference implementations (OpenCV FaceDetectorYN for YuNet, the official
// YOLOX ONNXRuntime demo post-processing), made by reference.py.
type expected map[string]struct {
	Faces []struct {
		Box       [4]int   `json:"box"`
		Score     float32  `json:"score"`
		Landmarks [][2]int `json:"landmarks"`
	} `json:"faces"`
	Objects []struct {
		Class string  `json:"class"`
		Box   [4]int  `json:"box"`
		Score float32 `json:"score"`
	} `json:"objects"`
}

func loadExpected(t *testing.T) expected {
	t.Helper()
	b, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatal(err)
	}
	var e expected
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if len(e) < 3 {
		t.Fatalf("expected.json has %d fixtures", len(e))
	}
	return e
}

func rect(b [4]int) image.Rectangle { return image.Rect(b[0], b[1], b[2], b[3]) }

// rectIoU is the intersection over union of two pixel rectangles.
func rectIoU(a, b image.Rectangle) float64 {
	in := a.Intersect(b)
	if in.Empty() {
		return 0
	}
	area := func(r image.Rectangle) float64 { return float64(r.Dx()) * float64(r.Dy()) }
	return area(in) / (area(a) + area(b) - area(in))
}

// labeled is an expected box and its class.
type labeled struct {
	box   image.Rectangle
	class string
}

// match pairs each expected box with the unused detection of the same class
// that overlaps it most, and returns the IoU per expected box (0: missed).
func match(want []labeled, got []Detection) []float64 {
	used := make([]bool, len(got))
	ious := make([]float64, len(want))
	for i, w := range want {
		best := -1
		for j, g := range got {
			if used[j] || g.Class != w.class {
				continue
			}
			if v := rectIoU(w.box, g.Box); v > ious[i] {
				ious[i], best = v, j
			}
		}
		if best >= 0 {
			used[best] = true
		}
	}
	return ious
}

// minIoU is the ROADMAP acceptance threshold (WP-71).
const minIoU = 0.5

func TestFixtures(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testOptions())
	for name, exp := range loadExpected(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			withImage(t, name, func(img *vips.Image) error {
				faces, err := d.Faces(t.Context(), img)
				if err != nil {
					return err
				}
				objects, err := d.Objects(t.Context(), img, nil)
				if err != nil {
					return err
				}

				want := make([]labeled, 0, len(exp.Faces)+len(exp.Objects))
				for _, f := range exp.Faces {
					want = append(want, labeled{rect(f.Box), FaceClass})
				}
				for i, v := range match(want, faces) {
					t.Logf("face %v: IoU %.3f", want[i].box, v)
					if v < minIoU {
						t.Errorf("face %v: best IoU %.3f < %.1f; got %v", want[i].box, v, minIoU, faces)
					}
				}
				if len(faces) != len(exp.Faces) {
					t.Errorf("%d faces, reference has %d: %v", len(faces), len(exp.Faces), faces)
				}
				for i, f := range exp.Faces {
					checkLandmarks(t, rect(f.Box), f.Landmarks, faces, i)
				}

				want = want[:0]
				for _, o := range exp.Objects {
					want = append(want, labeled{rect(o.Box), o.Class})
				}
				for i, v := range match(want, objects) {
					t.Logf("%s %v: IoU %.3f", want[i].class, want[i].box, v)
					if v < minIoU {
						t.Errorf("%s %v: best IoU %.3f < %.1f; got %v", want[i].class, want[i].box, v, minIoU, objects)
					}
				}
				for _, o := range objects {
					if !o.Box.In(image.Rect(0, 0, img.Width(), img.Height())) || o.Box.Empty() {
						t.Errorf("%s box %v outside the %dx%d image", o.Class, o.Box, img.Width(), img.Height())
					}
					if !slices.Contains(ObjectClasses(), o.Class) || o.Landmarks != nil {
						t.Errorf("bad object %+v", o)
					}
				}
				for i := 1; i < len(objects); i++ {
					if objects[i].Score > objects[i-1].Score {
						t.Errorf("objects not sorted by score: %v", objects)
					}
				}
				return nil
			})
		})
	}
}

// checkLandmarks finds the detected face matching box and checks each
// landmark is within 15 % of the face width of the reference.
func checkLandmarks(t *testing.T, box image.Rectangle, want [][2]int, faces []Detection, i int) {
	t.Helper()
	var best *Detection
	bestIoU := 0.0
	for j := range faces {
		if v := rectIoU(box, faces[j].Box); v > bestIoU {
			best, bestIoU = &faces[j], v
		}
	}
	if best == nil || len(best.Landmarks) != len(want) {
		t.Errorf("face %d: no detection with %d landmarks", i, len(want))
		return
	}
	tol := 0.15 * float64(box.Dx())
	for k, p := range want {
		got := best.Landmarks[k]
		if d := math.Hypot(float64(got.X-p[0]), float64(got.Y-p[1])); d > tol {
			t.Errorf("face %d landmark %d: got %v, reference %v (%.1f px > %.1f)", i, k, got, p, d, tol)
		}
	}
}

// TestScaled runs a fixture through the resize path: downscaled (the
// letterbox scales it up to 640) and upscaled (it scales it down). The
// boxes must still match the reference, scaled, in the image's pixels.
func TestScaled(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testOptions())
	exp := loadExpected(t)["apollo11-crew.jpg"]
	for _, scale := range []float64{0.6, 1.7} {
		t.Run(strconv.FormatFloat(scale, 'f', -1, 64), func(t *testing.T) {
			t.Parallel()
			withImage(t, "apollo11-crew.jpg", func(img *vips.Image) error {
				ctx := t.Context()
				var a vips.Arena
				defer a.Close()
				scaled, err := a.Add(img.Resize(ctx, scale, scale, vips.KernelLanczos3))
				if err != nil {
					return err
				}
				sx := float64(scaled.Width()) / float64(img.Width())
				sy := float64(scaled.Height()) / float64(img.Height())
				scaleBox := func(b [4]int) image.Rectangle {
					return image.Rect(int(float64(b[0])*sx), int(float64(b[1])*sy), int(float64(b[2])*sx), int(float64(b[3])*sy))
				}
				faces, err := d.Faces(ctx, scaled)
				if err != nil {
					return err
				}
				objects, err := d.Objects(ctx, scaled, []string{"person"})
				if err != nil {
					return err
				}
				want := make([]labeled, 0, len(exp.Faces)+len(exp.Objects))
				for _, f := range exp.Faces {
					want = append(want, labeled{scaleBox(f.Box), FaceClass})
				}
				for _, o := range exp.Objects {
					want = append(want, labeled{scaleBox(o.Box), o.Class})
				}
				for i, v := range match(want, append(faces, objects...)) {
					t.Logf("%s %v: IoU %.3f", want[i].class, want[i].box, v)
					if v < minIoU {
						t.Errorf("%s %v: IoU %.3f < %.1f; faces %v, objects %v", want[i].class, want[i].box, v, minIoU, faces, objects)
					}
				}
				return nil
			})
		})
	}
}

// TestMirrored checks that boxes are in the pixel space of the image passed
// in: a horizontally flipped image yields mirrored boxes, and the same
// image tagged with EXIF orientation 2 yields the upright boxes once
// autorotated, as the pipeline does at load.
func TestMirrored(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testOptions())
	exp := loadExpected(t)["astronaut-dogs.jpg"]
	withImage(t, "astronaut-dogs.jpg", func(img *vips.Image) error {
		ctx := t.Context()
		var a vips.Arena
		defer a.Close()
		w := img.Width()
		flipped, err := a.Add(img.Flip(ctx, vips.DirectionHorizontal))
		if err != nil {
			return err
		}
		faces, err := d.Faces(ctx, flipped)
		if err != nil {
			return err
		}
		b := exp.Faces[0].Box
		mirrored := image.Rect(w-b[2], b[1], w-b[0], b[3])
		if v := match([]labeled{{mirrored, FaceClass}}, faces)[0]; v < minIoU {
			t.Errorf("mirrored face: IoU %.3f with %v; got %v", v, mirrored, faces)
		}

		tagged, err := a.Add(flipped.SetOrientation(ctx, 2))
		if err != nil {
			return err
		}
		upright, err := a.Add(tagged.Autorot(ctx))
		if err != nil {
			return err
		}
		if faces, err = d.Faces(ctx, upright); err != nil {
			return err
		}
		if v := match([]labeled{{rect(b), FaceClass}}, faces)[0]; v < minIoU {
			t.Errorf("autorotated face: IoU %.3f with %v; got %v", v, rect(b), faces)
		}
		return nil
	})
}

func TestObjectClassFilter(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testOptions())
	withImage(t, "astronaut-dogs.jpg", func(img *vips.Image) error {
		dogs, err := d.Objects(t.Context(), img, []string{"dog", "cat"})
		if err != nil {
			return err
		}
		if len(dogs) == 0 {
			t.Error("no dog detected")
		}
		for _, o := range dogs {
			if o.Class != "dog" && o.Class != "cat" {
				t.Errorf("filter leaked %s", o.Class)
			}
		}
		_, err = d.Objects(t.Context(), img, []string{"unicorn"})
		if errs.KindOf(err) != errs.KindInvalidURL {
			t.Errorf("unknown class: %v, want InvalidURL", err)
		}
		_, err = d.Objects(t.Context(), img, []string{FaceClass})
		if errs.KindOf(err) != errs.KindInvalidURL {
			t.Errorf("face as an object class: %v, want InvalidURL", err)
		}
		return nil
	})
}

// TestGreyAlphaAndPages feeds images the models do not take as they are: a
// grey image with alpha and a two-page image (detected on page one).
func TestGreyAlphaAndPages(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testOptions())
	withImage(t, "apollo11-crew.jpg", func(img *vips.Image) error {
		ctx := t.Context()
		var a vips.Arena
		defer a.Close()
		grey, err := a.Add(img.Colourspace(ctx, vips.InterpretationBW))
		if err != nil {
			return err
		}
		greyAlpha, err := a.Add(grey.AddAlpha(ctx))
		if err != nil {
			return err
		}
		faces, err := d.Faces(ctx, greyAlpha)
		if err != nil {
			return err
		}
		if len(faces) != 3 {
			t.Errorf("grey+alpha: %d faces, want 3: %v", len(faces), faces)
		}

		black, err := a.Add(vips.Black(ctx, img.Width(), img.Height(), 3))
		if err != nil {
			return err
		}
		pages, err := a.Add(img.Join(ctx, black, vips.DirectionVertical))
		if err != nil {
			return err
		}
		if pages, err = a.Add(pages.SetPageHeight(ctx, img.Height())); err != nil {
			return err
		}
		if faces, err = d.Faces(ctx, pages); err != nil {
			return err
		}
		if len(faces) != 3 {
			t.Errorf("two pages: %d faces, want 3: %v", len(faces), faces)
		}
		for _, f := range faces {
			if f.Box.Max.Y > img.Height() {
				t.Errorf("face %v beyond page one", f.Box)
			}
		}
		return nil
	})
}

func TestCanceled(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testOptions())
	withImage(t, "cat-laptop.jpg", func(img *vips.Image) error {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := d.Objects(ctx, img, nil)
		if errs.KindOf(err) != errs.KindCanceled {
			t.Errorf("canceled: %v (kind %v), want Canceled", err, errs.KindOf(err))
		}
		return nil
	})
}

// TestUnavailable is the slim-image contract: without the library or the
// models, New fails with ErrUnavailable (kind Unsupported) and never
// crashes; a nil Detector reports the same.
func TestUnavailable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, f := range []string{testFaceModel, testObjectModel} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("not a model"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"missing library", Options{Library: "/nonexistent/libonnxruntime.so.1", ModelsDir: dir, FaceModel: testFaceModel, ObjectModel: testObjectModel}},
		{"missing models", Options{Library: testLibrary(), ModelsDir: filepath.Join(dir, "none"), FaceModel: testFaceModel, ObjectModel: testObjectModel}},
		{"missing object model", Options{Library: testLibrary(), ModelsDir: dir, FaceModel: testFaceModel, ObjectModel: "absent.onnx"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := New(&tc.opts)
			if !errors.Is(err, ErrUnavailable) || errs.KindOf(err) != errs.KindUnsupported || d != nil {
				t.Fatalf("New = %v, %v; want ErrUnavailable (Unsupported)", d, err)
			}
		})
	}

	var d *Detector
	if _, err := d.Faces(t.Context(), nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Faces: %v", err)
	}
	if _, err := d.Objects(t.Context(), nil, nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Objects: %v", err)
	}
	if d.HasFaces() || d.HasObjects() || d.Models() != nil || d.Version() != "" {
		t.Error("nil Detector reports models")
	}
	d.Close()
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.onnx")
	if err := os.WriteFile(junk, []byte("not a model"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(&Options{Library: testLibrary()}); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("no models: %v, want a configuration error", err)
	}
	if _, err := New(&Options{FaceModel: junk}); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("no library: %v, want a configuration error", err)
	}
	// A corrupt model is a configuration error, not "unavailable", once the
	// runtime loads.
	_, err := New(&Options{Library: testLibrary(), FaceModel: junk})
	if errors.Is(err, ErrUnavailable) {
		t.Skipf("runtime unavailable: %v", err)
	}
	if err == nil || errs.KindOf(err) != errs.KindInternal {
		t.Errorf("corrupt model: %v, want Internal", err)
	}
	// The face model where the object model belongs is rejected at startup.
	face := filepath.Join(testModelsDir, testFaceModel)
	if _, err := os.Stat(face); err != nil {
		t.Skip("models not installed")
	}
	if _, err := New(&Options{Library: testLibrary(), ObjectModel: face}); err == nil || !strings.Contains(err.Error(), "YOLOX") {
		t.Errorf("YuNet as the object model: %v", err)
	}
}

func TestChecksums(t *testing.T) {
	t.Parallel()
	opts := testOptions()
	opts.VerifyChecksums = true
	d := newTestDetector(t, opts)
	for _, m := range d.Models() {
		if m.Checksum != "verified" || m.Input != "640x640" {
			t.Errorf("%+v, want a verified 640x640 model", m)
		}
	}

	// A copy with a wrong SHA256SUMS entry fails; an unlisted one passes.
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join(testModelsDir, testFaceModel))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, testFaceModel), src, 0o600); err != nil { //nolint:gosec // G703: a constant name in t.TempDir()
		t.Fatal(err)
	}
	sums := strings.Repeat("0", 64) + "  " + testFaceModel + "\n"
	if err := os.WriteFile(filepath.Join(dir, checksumsFile), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}
	opts = &Options{Library: testLibrary(), ModelsDir: dir, FaceModel: testFaceModel, VerifyChecksums: true}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("mismatch: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, checksumsFile)); err != nil {
		t.Fatal(err)
	}
	d = newTestDetector(t, opts)
	if m := d.Models(); len(m) != 1 || m[0].Checksum != "unlisted" {
		t.Errorf("unlisted: %+v", m)
	}
}

type recorder struct{ calls []string }

func (r *recorder) ObserveDetect(model string, d time.Duration) {
	r.calls = append(r.calls, model+":"+strconv.FormatBool(d > 0))
}

func TestObserver(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	opts := testOptions()
	opts.Observer = rec
	d := newTestDetector(t, opts)
	withImage(t, "cat-laptop.jpg", func(img *vips.Image) error {
		if _, err := d.Faces(t.Context(), img); err != nil {
			return err
		}
		_, err := d.Objects(t.Context(), img, nil)
		return err
	})
	if got := fmt.Sprint(rec.calls); got != "[face:true object:true]" {
		t.Errorf("observed %s", got)
	}
}

// TestNoLeak runs 200 detections (every tenth an object detection, the
// rest faces, to keep the race-enabled run short) and checks that libvips' tracked memory
// and live objects return to their baseline, and that the process RSS
// (ONNX Runtime allocates with malloc) stops growing after warm-up.
func TestNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	d := newTestDetector(t, testOptions())
	ctx := t.Context()
	run := func(n int) {
		t.Helper()
		withImage(t, "astronaut-dogs.jpg", func(img *vips.Image) error {
			for i := range n {
				var err error
				if i%10 == 0 {
					_, err = d.Objects(ctx, img, nil)
				} else {
					_, err = d.Faces(ctx, img)
				}
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	run(10) // warm-up: ORT's first runs allocate its kernels' caches
	before, objs, rss := vips.ReadMemStats(), vips.LiveObjects(), residentBytes(t)
	run(200)
	after := vips.ReadMemStats()
	if after.Mem > before.Mem {
		t.Errorf("vips tracked memory grew from %d to %d bytes", before.Mem, after.Mem)
	}
	if n := vips.LiveObjects(); n > objs {
		t.Errorf("live vips objects grew from %d to %d", objs, n)
	}
	if grown := residentBytes(t) - rss; rss > 0 && grown > 64<<20 {
		t.Errorf("RSS grew by %d MiB over 200 detections", grown>>20)
	}
}

// residentBytes reads the process RSS from /proc (0 elsewhere).
func residentBytes(t *testing.T) int64 {
	t.Helper()
	if runtime.GOOS != "linux" {
		return 0
	}
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * int64(os.Getpagesize())
}
