package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Paths of the classification tests (WP-110).
const (
	testClassifyModel    = "mobilenetv3_large_100.onnx"
	classifyFixturesDir  = "../../testdata/fixtures/classify"
	classifyExpectedFile = "../../testdata/classify/expected.json"
)

func testClassifyOptions() *Options {
	return &Options{
		Library:       testLibrary(),
		ModelsDir:     testModelsDir,
		ClassifyModel: testClassifyModel,
		Threads:       1,
	}
}

// classifyFixture is the path of a classification photo: the detection
// photos are classified too.
func classifyFixture(name string) string {
	if _, err := os.Stat(filepath.Join(fixturesDir, name)); err == nil {
		return filepath.Join(fixturesDir, name)
	}
	return filepath.Join(classifyFixturesDir, name)
}

// classifyExpected is testdata/classify/expected.json: the top 5 of the
// reference implementation (timm's evaluation transform in Pillow/NumPy,
// onnxruntime, softmax; reference.py).
type classifyExpected map[string]struct {
	Top5 [][3]any `json:"top5"`
}

type refClass struct {
	id    int
	name  string
	score float64
}

func loadClassifyExpected(t *testing.T) map[string][]refClass {
	t.Helper()
	b, err := os.ReadFile(classifyExpectedFile)
	if err != nil {
		t.Fatal(err)
	}
	var e classifyExpected
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]refClass, len(e))
	for name, v := range e {
		for _, c := range v.Top5 {
			id, _ := c[0].(float64)
			label, _ := c[1].(string)
			score, _ := c[2].(float64)
			out[name] = append(out[name], refClass{int(id), label, score})
		}
	}
	if len(out) < 10 {
		t.Fatalf("expected.json has %d photos", len(out))
	}
	return out
}

// top returns the k best class ids of c.
func top(c *Classification, k int) []int {
	ids := make([]int, len(c.Scores))
	for i := range ids {
		ids[i] = i
	}
	slices.SortStableFunc(ids, func(a, b int) int {
		switch {
		case c.Scores[a] > c.Scores[b]:
			return -1
		case c.Scores[a] < c.Scores[b]:
			return 1
		default:
			return 0
		}
	})
	return ids[:k]
}

// TestClassifyFixtures classifies the ten photos with the shipped model and
// compares with the reference implementation and with what the photos
// show: the Go preprocessing (libvips cubic resize, centre crop) must give
// the reference top 1 (or, where the reference hesitates between two
// classes, one of its top 2), keep the reference top 1 in its top 5 with a
// probability within 0.1, keep every reference top-5 class of probability
// 0.03 or more (below that the tail is near ties) and at least 3 of the
// five, and
// name the photographed subject first (in the top 5 for the red fox, which
// the model takes for a grey fox like the reference does).
func TestClassifyFixtures(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testClassifyOptions())
	want := loadClassifyExpected(t)
	truth := map[string]struct {
		id   int
		top1 bool
	}{
		"bald-eagle.jpg":       {22, true},
		"banana.jpg":           {954, true},
		"golden-retriever.jpg": {207, true},
		"monarch.jpg":          {323, true},
		"pizza.jpg":            {963, true},
		"space-shuttle.jpg":    {812, true},
		"red-fox.jpg":          {277, false},
		"cat-laptop.jpg":       {284, true}, // "Siamese cat" is the model's (and the reference's) reading
	}
	for name, ref := range want {
		t.Run(strings.TrimSuffix(name, ".jpg"), func(t *testing.T) {
			t.Parallel()
			var c *Classification
			withImageFile(t, classifyFixture(name), func(img *vips.Image) error {
				var err error
				c, err = d.Classify(t.Context(), img)
				return err
			})
			if len(c.Scores) != imagenetClassCount || len(c.Labels) != imagenetClassCount {
				t.Fatalf("%d scores, %d labels", len(c.Scores), len(c.Labels))
			}
			var sum float64
			for _, s := range c.Scores {
				sum += float64(s)
			}
			if math.Abs(sum-1) > 1e-3 {
				t.Errorf("probabilities sum to %v", sum)
			}
			got := top(c, 5)
			t.Logf("top 5: %s", describe(c, got))
			allowed := []int{ref[0].id}
			if ref[0].score-ref[1].score < 0.1 {
				allowed = append(allowed, ref[1].id)
			}
			if !slices.Contains(allowed, got[0]) {
				t.Errorf("top 1 %s, want one of %v", c.Labels[got[0]], allowed)
			}
			if !slices.Contains(got, ref[0].id) {
				t.Errorf("reference top 1 %s not in the top 5", ref[0].name)
			}
			if diff := math.Abs(float64(c.Scores[ref[0].id]) - ref[0].score); diff > 0.1 {
				t.Errorf("%s: probability %.4f, reference %.4f", ref[0].name, c.Scores[ref[0].id], ref[0].score)
			}
			shared := 0
			for _, r := range ref {
				in := slices.Contains(got, r.id)
				if in {
					shared++
				} else if r.score >= 0.03 {
					t.Errorf("reference class %s (%.4f) not in the top 5", r.name, r.score)
				}
				if c.Labels[r.id] != r.name {
					t.Errorf("class %d is %q, reference %q", r.id, c.Labels[r.id], r.name)
				}
			}
			if shared < 3 {
				t.Errorf("%d of the reference top 5 in the top 5", shared)
			}
			if tr, ok := truth[name]; ok {
				if tr.top1 && got[0] != tr.id || !slices.Contains(got, tr.id) {
					t.Errorf("subject %s not found (top 1 required: %v)", c.Labels[tr.id], tr.top1)
				}
			}
		})
	}
}

func describe(c *Classification, ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, i := range ids {
		parts = append(parts, fmt.Sprintf("%s %.4f", c.Labels[i], c.Scores[i]))
	}
	return strings.Join(parts, ", ")
}

// TestClassifyLazy: the session is created by the first Classify, not by
// New, and the classifier shares the detector's runtime with the
// detection models.
func TestClassifyLazy(t *testing.T) {
	t.Parallel()
	opts := testOptions()
	opts.ClassifyModel = testClassifyModel
	opts.VerifyChecksums = true
	d := newTestDetector(t, opts)
	if !d.HasFaces() || !d.HasObjects() || !d.HasClassifier() {
		t.Fatal("want all three models")
	}
	info := func() ModelInfo {
		for _, m := range d.Models() {
			if m.Kind == modelClassify {
				return m
			}
		}
		t.Fatal("no classify model info")
		return ModelInfo{}
	}
	if m := info(); m.Input != "lazy" || m.Checksum != "verified" {
		t.Errorf("before first use: %+v", m)
	}
	withImage(t, "cat-laptop.jpg", func(img *vips.Image) error {
		_, err := d.Classify(t.Context(), img)
		return err
	})
	if m := info(); m.Input != "224x224" {
		t.Errorf("after first use: %+v", m)
	}
}

// TestClassifyUnavailable: a missing classifier disables classification
// only while the detection models are there, and is the whole detector's
// ErrUnavailable when it is the only model; a nil Detector has none.
func TestClassifyUnavailable(t *testing.T) {
	t.Parallel()
	var nilDet *Detector
	if nilDet.HasClassifier() || nilDet.ClassifyUnavailable() != nil {
		t.Error("nil Detector reports a classifier")
	}
	if _, err := nilDet.Classify(t.Context(), nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil Classify: %v", err)
	}
	dir := t.TempDir()
	d, err := New(&Options{Library: testLibrary(), ModelsDir: dir, ClassifyModel: "absent.onnx"})
	if !errors.Is(err, ErrUnavailable) || errs.KindOf(err) != errs.KindUnsupported || d != nil {
		t.Errorf("classifier only, missing: %v, %v", d, err)
	}

	opts := testOptions()
	opts.ClassifyModel = "absent.onnx"
	d = newTestDetector(t, opts)
	if d.HasClassifier() || !errors.Is(d.ClassifyUnavailable(), ErrUnavailable) || !d.HasFaces() {
		t.Errorf("missing classifier: has %v, reason %v", d.HasClassifier(), d.ClassifyUnavailable())
	}
	withImage(t, "cat-laptop.jpg", func(img *vips.Image) error {
		if _, err := d.Classify(t.Context(), img); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Classify without a classifier: %v", err)
		}
		return nil
	})
}

// TestClassifyBadModel: a model that is not a classifier (the face model),
// or a class names file that does not match the output, fails every
// Classify call with an Internal error, not a crash, and a corrupt names
// file fails New.
func TestClassifyBadModel(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(filepath.Join(testModelsDir, testClassifyModel)); err != nil {
		t.Skip("models not installed")
	}
	dir := t.TempDir()
	names := filepath.Join(dir, "names.txt")
	if err := os.WriteFile(names, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"face model", Options{ClassifyModel: testFaceModel}, "non-ImageNet model"},
		{"three names", Options{ClassifyModel: testClassifyModel, ClassifyLabels: names}, "class names file 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := tc.opts
			opts.Library, opts.ModelsDir = testLibrary(), testModelsDir
			d := newTestDetector(t, &opts)
			withImage(t, "cat-laptop.jpg", func(img *vips.Image) error {
				for range 2 {
					_, err := d.Classify(t.Context(), img)
					if errs.KindOf(err) != errs.KindInternal || !strings.Contains(err.Error(), tc.want) {
						t.Errorf("Classify: %v, want Internal with %q", err, tc.want)
					}
				}
				return nil
			})
		})
	}
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := testClassifyOptions()
	opts.ClassifyLabels = empty
	if _, err := New(opts); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("empty names file: %v", err)
	}
}

// TestClassifyCanceled: a canceled context stops the classification.
func TestClassifyCanceled(t *testing.T) {
	t.Parallel()
	d := newTestDetector(t, testClassifyOptions())
	withImage(t, "cat-laptop.jpg", func(img *vips.Image) error {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := d.Classify(ctx, img); errs.KindOf(err) != errs.KindCanceled {
			t.Errorf("canceled: %v (kind %v)", err, errs.KindOf(err))
		}
		return nil
	})
}

// TestClassifyObserver: each classification is observed as "classify".
func TestClassifyObserver(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	opts := testClassifyOptions()
	opts.Observer = rec
	d := newTestDetector(t, opts)
	withImage(t, "cat-laptop.jpg", func(img *vips.Image) error {
		_, err := d.Classify(t.Context(), img)
		return err
	})
	if diff := cmp.Diff([]string{"classify:true"}, rec.calls); diff != "" {
		t.Errorf("observed (-want +got):\n%s", diff)
	}
}

// TestClassifyNoLeak runs 100 classifications and checks libvips' tracked
// memory and live objects return to their baseline and the RSS stops
// growing after warm-up (as TestNoLeak does for detection).
func TestClassifyNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	d := newTestDetector(t, testClassifyOptions())
	ctx := t.Context()
	run := func(n int) {
		t.Helper()
		withImage(t, "astronaut-dogs.jpg", func(img *vips.Image) error {
			for range n {
				if _, err := d.Classify(ctx, img); err != nil {
					return err
				}
			}
			return nil
		})
	}
	run(10)
	before, objs, rss := vips.ReadMemStats(), vips.LiveObjects(), residentBytes(t)
	run(100)
	if after := vips.ReadMemStats(); after.Mem > before.Mem {
		t.Errorf("vips tracked memory grew from %d to %d bytes", before.Mem, after.Mem)
	}
	if n := vips.LiveObjects(); n > objs {
		t.Errorf("live vips objects grew from %d to %d", objs, n)
	}
	if grown := residentBytes(t) - rss; rss > 0 && grown > 32<<20 {
		t.Errorf("RSS grew by %d MiB over 100 classifications", grown>>20)
	}
}

// Pure Go: no runtime needed.

func TestClassifyScale(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		w, h, tw, th int
		short        float64 // want the short side scaled to about this
	}{
		{640, 419, 224, 224, 256},
		{419, 640, 224, 224, 256},
		{256, 256, 224, 224, 256},
		{10, 10, 224, 224, 256},     // tiny: upscaled
		{4000, 30, 224, 224, 256},   // panorama: the short side still fits
		{1000, 1000, 300, 200, 342}, // non-square input: the width fills first
	} {
		s := classifyScale(tc.w, tc.h, tc.tw, tc.th)
		if short := float64(min(tc.w, tc.h)) * s; math.Abs(short-tc.short) > 1 {
			t.Errorf("%dx%d → %dx%d: short side %.1f, want %.0f", tc.w, tc.h, tc.tw, tc.th, short, tc.short)
		}
		if float64(tc.w)*s < float64(tc.tw) || float64(tc.h)*s < float64(tc.th) {
			t.Errorf("%dx%d scaled by %v does not cover %dx%d", tc.w, tc.h, s, tc.tw, tc.th)
		}
	}
}

func TestPackRGB(t *testing.T) {
	t.Parallel()
	// Two pixels: pure red and mid grey.
	pix := []byte{255, 0, 0, 128, 128, 128}
	for _, tc := range []struct {
		name string
		norm Normalization
		nhwc bool
		want []float32
	}{
		{"none nchw", NormalizeNone, false, []float32{255, 128, 0, 128, 0, 128}},
		{"none nhwc", NormalizeNone, true, []float32{255, 0, 0, 128, 128, 128}},
		{"half", NormalizeHalf, false, []float32{1, 128.0 / 255, 0, 128.0 / 255, 0, 128.0 / 255}},
		{"full", NormalizeFull, true, []float32{1, -1, -1, 128/127.5 - 1, 128/127.5 - 1, 128/127.5 - 1}},
		{"imagenet", NormalizeImageNet, false, []float32{
			(1 - 0.485) / 0.229, (128.0/255 - 0.485) / 0.229,
			(0 - 0.456) / 0.224, (128.0/255 - 0.456) / 0.224,
			(0 - 0.406) / 0.225, (128.0/255 - 0.406) / 0.225,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dst := make([]float32, 6)
			if err := packRGB(dst, pix, 2, 1, tc.nhwc, tc.norm); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, dst, cmp.Comparer(func(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-5 })); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
	if err := packRGB(make([]float32, 5), pix, 2, 1, false, NormalizeNone); err == nil {
		t.Error("short tensor: no error")
	}
	if err := packRGB(make([]float32, 6), pix[:5], 2, 1, false, NormalizeNone); err == nil {
		t.Error("short pixels: no error")
	}
}

func TestProbabilities(t *testing.T) {
	t.Parallel()
	near := cmp.Comparer(func(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-6 })
	// Already a distribution: kept.
	if diff := cmp.Diff([]float32{0.25, 0.75}, probabilities([]float32{0.25, 0.75}), near); diff != "" {
		t.Errorf("distribution: %s", diff)
	}
	// Logits: softmax, stable for large values.
	got := probabilities([]float32{1000, 1000, -math.MaxFloat32})
	if diff := cmp.Diff([]float32{0.5, 0.5, 0}, got, near); diff != "" {
		t.Errorf("logits: %s", diff)
	}
	// NaN and infinities never panic and never produce NaN.
	for _, in := range [][]float32{
		{float32(math.NaN()), 1},
		{float32(math.Inf(1)), 0},
		{float32(math.Inf(-1)), float32(math.Inf(-1))},
		{},
	} {
		for _, p := range probabilities(in) {
			if math.IsNaN(float64(p)) || p < 0 || p > 1 {
				t.Errorf("probabilities(%v) = %v", in, p)
			}
		}
	}
}

func TestImageNetClasses(t *testing.T) {
	t.Parallel()
	c := ImageNetClasses()
	if len(c) != imagenetClassCount {
		t.Fatalf("%d classes", len(c))
	}
	seen := map[string]bool{}
	for i, name := range c {
		key := strings.ToLower(name)
		if name == "" || name != strings.TrimSpace(name) || strings.Contains(name, "_") || seen[key] {
			t.Errorf("class %d %q is empty, padded, has an underscore or is a duplicate", i, name)
		}
		seen[key] = true
	}
	for id, name := range map[int]string{0: "tench", 22: "bald eagle", 281: "tabby cat", 812: "space shuttle", 999: "toilet paper"} {
		if c[id] != name {
			t.Errorf("class %d = %q, want %q", id, c[id], name)
		}
	}
}

func TestParseClassNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"a\nb\n", []string{"a", "b"}, false},
		{" a \r\nb", []string{"a", "b"}, false},
		{"a\n\n\n", []string{"a"}, false},
		{"a\n\nb\n", nil, true},
		{"", nil, true},
		{"\n\n", nil, true},
	} {
		got, err := parseClassNames(strings.NewReader(tc.in))
		if (err != nil) != tc.wantErr || !slices.Equal(got, tc.want) {
			t.Errorf("parseClassNames(%q) = %q, %v", tc.in, got, err)
		}
	}
}

// FuzzParseClassNames: any file parses or fails cleanly, and every name
// is non-empty and trimmed.
func FuzzParseClassNames(f *testing.F) {
	f.Add("tench\ngoldfish\n")
	f.Add("a\n\nb")
	f.Add("\xff\x00\n")
	f.Fuzz(func(t *testing.T, s string) {
		names, err := parseClassNames(strings.NewReader(s))
		if err != nil {
			return
		}
		if len(names) == 0 {
			t.Fatal("no names and no error")
		}
		for _, n := range names {
			if n == "" || n != strings.TrimSpace(n) {
				t.Fatalf("name %q", n)
			}
		}
	})
}

// FuzzProbabilities: any model output gives probabilities in [0, 1].
func FuzzProbabilities(f *testing.F) {
	f.Add([]byte{0, 0, 128, 63, 0, 0, 0, 0})
	f.Add([]byte{0, 0, 192, 127, 0, 0, 128, 127})
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, p := range probabilities(floats(data)) {
			if math.IsNaN(float64(p)) || p < 0 || p > 1 {
				t.Fatalf("probability %v", p)
			}
		}
	})
}
