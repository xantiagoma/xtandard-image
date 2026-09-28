//go:build cgo

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg" // responses are decoded with image.Decode
	_ "image/png"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/detect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/detectfix"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// fakeDetector answers with the reference detections of the detection
// fixtures (testdata/detect/expected.json), recognising a fixture by the
// size of the image it is given (the fixtures are 640 pixels wide, below
// the detection sample size, so they are detected at their own size). It
// counts its calls per model.
type fakeDetector struct {
	bySize        map[image.Point]*plan.Detections
	faces, objs   atomic.Int32
	failFaces     bool
	noFaces, noOb bool
	// classes is what Classify answers; nil: no classifier.
	classes    *detect.Classification
	classifies atomic.Int32
}

func newFakeDetector(t *testing.T) *fakeDetector {
	t.Helper()
	f := &fakeDetector{bySize: map[image.Point]*plan.Detections{}}
	for _, fx := range []string{detectfix.Crew, detectfix.Dogs, detectfix.Cat} {
		w, h := detectfix.Size(t, fx)
		f.bySize[image.Pt(w, h)] = detectfix.Load(t, fx)
	}
	return f
}

func (f *fakeDetector) HasFaces() bool      { return !f.noFaces }
func (f *fakeDetector) HasObjects() bool    { return !f.noOb }
func (f *fakeDetector) HasClassifier() bool { return f.classes != nil }

// Classify answers f.classes whatever the image, counting its calls.
func (f *fakeDetector) Classify(_ context.Context, img *vips.Image) (*detect.Classification, error) {
	f.classifies.Add(1)
	if img.Width() <= 0 {
		return nil, errs.New(errs.KindInternal, "fake: empty image")
	}
	return f.classes, nil
}

func (f *fakeDetector) Faces(_ context.Context, img *vips.Image) ([]detect.Detection, error) {
	f.faces.Add(1)
	if f.failFaces {
		return nil, errs.New(errs.KindInternal, "fake: face model failed")
	}
	d := f.bySize[image.Pt(img.Width(), img.Height())]
	if d == nil {
		return nil, nil
	}
	return pixelDetections(d.Faces, img.Width(), img.Height()), nil
}

func (f *fakeDetector) Objects(_ context.Context, img *vips.Image, _ []string) ([]detect.Detection, error) {
	f.objs.Add(1)
	d := f.bySize[image.Pt(img.Width(), img.Height())]
	if d == nil {
		return nil, nil
	}
	return pixelDetections(d.Objects, img.Width(), img.Height()), nil
}

func pixelDetections(ds []plan.Detected, w, h int) []detect.Detection {
	px := func(v float64, n int) int { return int(math.Round(v * float64(n))) }
	out := make([]detect.Detection, 0, len(ds))
	for _, d := range ds {
		x := detect.Detection{
			Class: d.Class, Score: float32(d.Score),
			Box: image.Rect(px(d.Box.Left, w), px(d.Box.Top, h), px(d.Box.Left+d.Box.Width, w), px(d.Box.Top+d.Box.Height, h)),
		}
		for _, l := range d.Landmarks {
			x.Landmarks = append(x.Landmarks, image.Pt(px(l[0], w), px(l[1], h)))
		}
		out = append(out, x)
	}
	return out
}

func TestDetectCache(t *testing.T) {
	t.Parallel()
	c := newDetectCache(2)
	k := func(b byte, model string) detectKey { return detectKey{sum: [32]byte{b}, model: model} }
	one := []plan.Detected{{Class: "face"}}
	c.put(k(1, modelFace), one)
	c.put(k(1, modelObject), nil)
	if got, ok := c.get(k(1, modelFace)); !ok || len(got) != 1 {
		t.Fatalf("get = %v, %v", got, ok)
	}
	// k(1, face) was used last: k(1, object) is evicted.
	c.put(k(2, modelFace), one)
	if _, ok := c.get(k(1, modelObject)); ok {
		t.Error("least recently used entry kept")
	}
	if _, ok := c.get(k(1, modelFace)); !ok {
		t.Error("recently used entry evicted")
	}
	if c.len() != 2 {
		t.Errorf("len = %d", c.len())
	}
	var nilCache *detectCache
	nilCache.put(k(1, modelFace), one)
	if _, ok := nilCache.get(k(1, modelFace)); ok || nilCache.len() != 0 {
		t.Error("nil cache stored something")
	}
	// Concurrent use (run with -race).
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 100 {
				c.put(detectKey{model: strconv.Itoa(i * j % 7)}, one)
				c.get(detectKey{model: strconv.Itoa(j % 7)})
			}
		})
	}
	wg.Wait()
	if c.len() > 2 {
		t.Errorf("len %d over the bound", c.len())
	}
}

// TestDetectionsForRequest: what forRequest asks the detector for, the
// errors of a deployment without it, the optional smart faces, the
// conversion to fractions and the cache shared by every request on the
// same bytes.
func TestDetectionsForRequest(t *testing.T) {
	data, err := os.ReadFile(golden.Fixture(t, detectfix.Dogs))
	if err != nil {
		t.Fatal(err)
	}
	src, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Release()
	ctx := t.Context()
	log := slog.New(slog.DiscardHandler)
	want := detectfix.Load(t, detectfix.Dogs)
	approx := cmpopts.EquateApprox(0, 1.0/640)

	// No detector (slim): required models are Unsupported, smart faces
	// are not detected.
	for _, d := range []*detections{nil, newDetections(nil, 8, true, codec.Limits{}, log)} {
		for _, needs := range []plan.DetectionNeeds{{Faces: true}, {Objects: true}} {
			_, err := d.forRequest(ctx, imageSource(src, codec.FormatJPEG), needs)
			if errs.KindOf(err) != errs.KindUnsupported || !errors.Is(err, detect.ErrUnavailable) {
				t.Errorf("%+v without a detector: %v", needs, err)
			}
			if e, ok := errors.AsType[*errs.Error](err); !ok || e.PublicMessage() == "" || e.PublicMessage() == errs.PublicMessage(errs.KindUnsupported) {
				t.Errorf("%+v: public message %v", needs, err)
			}
		}
		if got, err := d.forRequest(ctx, imageSource(src, codec.FormatJPEG), plan.DetectionNeeds{SmartFaces: true}); got != nil || err != nil {
			t.Errorf("smart without a detector: %v, %v", got, err)
		}
	}

	fake := newFakeDetector(t)
	d := newDetections(fake, 8, true, codec.Limits{}, log)
	got, err := d.forRequest(ctx, imageSource(src, codec.FormatJPEG), plan.DetectionNeeds{Faces: true})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want.Faces, got.Faces, approx); diff != "" || got.Objects != nil {
		t.Errorf("faces (-want +got):\n%s objects %v", diff, got.Objects)
	}
	// Objects run once, faces come from the cache.
	got, err = d.forRequest(ctx, imageSource(src, codec.FormatJPEG), plan.DetectionNeeds{Faces: true, Objects: true})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got, approx); diff != "" {
		t.Errorf("detections (-want +got):\n%s", diff)
	}
	if f, o := fake.faces.Load(), fake.objs.Load(); f != 1 || o != 1 {
		t.Errorf("detector calls: faces %d, objects %d; want 1, 1", f, o)
	}
	if d.cache.len() != 2 {
		t.Errorf("cache holds %d results", d.cache.len())
	}

	// Smart faces: detected when enabled, skipped when not, and a failure
	// falls back silently while a required model fails the request.
	other, err := vips.NewBufferFromReader(bytes.NewReader(append(bytes.Clone(data), 0)), 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if got, err := newDetections(fake, 0, false, codec.Limits{}, log).forRequest(ctx, imageSource(other, codec.FormatJPEG), plan.DetectionNeeds{SmartFaces: true}); got != nil || err != nil {
		t.Errorf("smart faces off: %v, %v", got, err)
	}
	failing := newFakeDetector(t)
	failing.failFaces = true
	fd := newDetections(failing, 0, true, codec.Limits{}, log)
	if got, err := fd.forRequest(ctx, imageSource(other, codec.FormatJPEG), plan.DetectionNeeds{SmartFaces: true}); got != nil || err != nil {
		t.Errorf("failing smart faces: %v, %v", got, err)
	}
	if _, err := fd.forRequest(ctx, imageSource(other, codec.FormatJPEG), plan.DetectionNeeds{Faces: true}); err == nil {
		t.Error("failing required faces: no error")
	}
	noFaces := newFakeDetector(t)
	noFaces.noFaces = true
	if got, err := newDetections(noFaces, 0, true, codec.Limits{}, log).forRequest(ctx, imageSource(other, codec.FormatJPEG), plan.DetectionNeeds{SmartFaces: true, Objects: true}); err != nil || got == nil || got.Faces != nil || len(got.Objects) != 3 {
		t.Errorf("objects without a face model: %+v, %v", got, err)
	}
	if noFaces.faces.Load() != 0 {
		t.Error("face model called without a face model")
	}
}

// fakeClasses is a four-class classification for the fake classifier.
func fakeClasses() *detect.Classification {
	return &detect.Classification{
		Labels: []string{"tench", "Golden Retriever", "tabby cat", "laptop computer"},
		Scores: []float32{0.05, 0.1, 0.6, 0.25},
	}
}

// TestClassifyForRequest: classification is optional (nil without a
// classifier), shares the detection thumbnail and cache, carries the
// configured threshold and cap, and a failing classifier is an error.
func TestClassifyForRequest(t *testing.T) {
	data, err := os.ReadFile(golden.Fixture(t, detectfix.Cat))
	if err != nil {
		t.Fatal(err)
	}
	src, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Release()
	ctx := t.Context()
	log := slog.New(slog.DiscardHandler)
	needs := plan.DetectionNeeds{Classify: true}

	// No classifier (slim, or detection models only): nothing, no error.
	for _, d := range []*detections{nil, newDetections(nil, 8, true, codec.Limits{}, log), newDetections(newFakeDetector(t), 8, true, codec.Limits{}, log)} {
		if got, err := d.forRequest(ctx, imageSource(src, codec.FormatJPEG), needs); got != nil || err != nil {
			t.Errorf("without a classifier: %+v, %v", got, err)
		}
	}

	fake := newFakeDetector(t)
	fake.classes = fakeClasses()
	d := newDetections(fake, 8, true, codec.Limits{}, log)
	d.classifyMinScore, d.classifyTopKMax = 0.01, 3
	got, err := d.forRequest(ctx, imageSource(src, codec.FormatJPEG), needs)
	if err != nil {
		t.Fatal(err)
	}
	want := &plan.Classification{Labels: fake.classes.Labels, Scores: fake.classes.Scores, MinScore: 0.01, MaxTopK: 3}
	if got.Faces != nil || got.Objects != nil || !cmp.Equal(want, got.Classes) {
		t.Errorf("classification %+v, want %+v", got, want)
	}
	// Detection and classification together: one more model call each,
	// the classification from the cache.
	got, err = d.forRequest(ctx, imageSource(src, codec.FormatJPEG), plan.DetectionNeeds{Objects: true, Classify: true})
	if err != nil || got.Classes == nil || len(got.Objects) == 0 {
		t.Fatalf("objects and classes: %+v, %v", got, err)
	}
	if c, o := fake.classifies.Load(), fake.objs.Load(); c != 1 || o != 1 {
		t.Errorf("calls: classify %d, objects %d; want 1 and 1", c, o)
	}
	if d.cache.len() != 2 {
		t.Errorf("cache holds %d results", d.cache.len())
	}

	failing := newFakeDetector(t)
	failing.classes = fakeClasses()
	failing.failFaces = true
	fd := newDetections(failing, 0, true, codec.Limits{}, log)
	if _, err := fd.forRequest(ctx, imageSource(src, codec.FormatJPEG), plan.DetectionNeeds{Faces: true, Classify: true}); err == nil {
		t.Error("failing faces with classify: no error")
	}
}

// TestClassifyInfo serves /info classify end to end with the fake
// classifier: imgproxy Pro's shape, the class filter, a 400 for an
// unknown class, one classification per source (cached), and detection
// and classification in one request.
func TestClassifyInfo(t *testing.T) {
	// Not parallel: see TestDetectDialects.
	fake := newFakeDetector(t)
	fake.classes = fakeClasses()
	a := start(t, detectDialectsConfig(t, ""), hooks{detector: fake})
	base := "http://" + a.public.String() + "/info/insecure" + noDefaults
	for _, tc := range []struct {
		path   string
		status int
		want   string
	}{
		{
			"/cl:2/plain/detect/cat-laptop.jpg", http.StatusOK,
			`{"classification":[{"class_id":2,"name":"tabby cat","confidence":0.6},{"class_id":3,"name":"laptop computer","confidence":0.25}]}`,
		},
		{
			"/classify:5:Golden_Retriever:TENCH/plain/detect/cat-laptop.jpg", http.StatusOK,
			`{"classification":[{"class_id":1,"name":"Golden Retriever","confidence":0.1},{"class_id":0,"name":"tench","confidence":0.05}]}`,
		},
		{"/cl:1:unicorn/plain/detect/cat-laptop.jpg", http.StatusBadRequest, ""},
		{"/cl:0/plain/detect/cat-laptop.jpg", http.StatusOK, `{}`},
	} {
		r := get(t, base+tc.path)
		if r.status != tc.status {
			t.Errorf("GET %s: %d %s, want %d", tc.path, r.status, r.body, tc.status)
			continue
		}
		if tc.want == "" {
			continue
		}
		var got, want any
		if err := json.Unmarshal(r.body, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got, cmpopts.EquateApprox(0, 1e-6)); diff != "" {
			t.Errorf("GET %s (-want +got):\n%s", tc.path, diff)
		}
		if ig := r.header.Get("X-Xi-Ignored"); ig != "" {
			t.Errorf("GET %s: X-Xi-Ignored %q", tc.path, ig)
		}
	}
	if n := fake.classifies.Load(); n != 1 {
		t.Errorf("classifier ran %d times, want once (cached)", n)
	}
	r := get(t, base+"/do:1/cl:1/plain/detect/astronaut-dogs.jpg")
	var body struct {
		Objects        []json.RawMessage `json:"objects"`
		Classification []json.RawMessage `json:"classification"`
	}
	if err := json.Unmarshal(r.body, &body); r.status != http.StatusOK || err != nil || len(body.Objects) == 0 || len(body.Classification) != 1 {
		t.Errorf("do:1/cl:1: %d %s", r.status, r.body)
	}
	if c := fake.classifies.Load(); c != 2 {
		t.Errorf("classifier ran %d times, want 2 (two sources)", c)
	}
	if !codec.VideoAvailable() {
		return
	}
	// A video is classified on the frame detection uses (WP-91).
	r = get(t, base+"/cl:1/plain/"+faceVideo)
	if err := json.Unmarshal(r.body, &body); r.status != http.StatusOK || err != nil || len(body.Classification) != 1 {
		t.Errorf("video cl:1: %d %s", r.status, r.body)
	}
	if c := fake.classifies.Load(); c != 3 {
		t.Errorf("classifier ran %d times, want 3", c)
	}
}

// detectDialectsConfig mounts imgproxy, imgix, cloudinary (cloud "demo"),
// imagekit, cloudflare, bunny, thumbor and awssih on the fixtures,
// unsigned; extra is appended to the YAML.
func detectDialectsConfig(t *testing.T, extra string) *config.Config {
	t.Helper()
	root := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	yml := `server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: ""
observability:
  debug_headers: true
sources:
  fx:
    type: fs
    root: ` + strconv.Quote(root) + `
dialects:
  imgproxy:
    source: fx
  imgix:
    enabled: true
    source: fx
  cloudinary:
    enabled: true
    clouds:
      demo: fx
  imagekit:
    enabled: true
    source: fx
  cloudflare:
    enabled: true
    source: fx
  bunny:
    enabled: true
    source: fx
  thumbor:
    enabled: true
    source: fx
  awssih:
    enabled: true
    source: fx
security:
  unsigned_policy: allow
` + extra
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{ConfigFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return &cfg
}

// sih is an AWS SIH path for a JSON request.
func sih(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return "/sih/" + base64.StdEncoding.EncodeToString(b)
}

// detectDialectCases are the face and object options of every dialect on
// the detection fixtures (golden testdata/golden/app/detect-<name>.png).
func detectDialectCases(t *testing.T) []struct{ name, path string } {
	t.Helper()
	const crew, dogs, cat = "detect/apollo11-crew.jpg", "detect/astronaut-dogs.jpg", "detect/cat-laptop.jpg"
	return []struct{ name, path string }{
		{"imgproxy-obj-face", "/insecure/rs:fill:240:120/g:obj:face/plain/" + crew + "@png"},
		{"imgproxy-sm-faces", "/insecure/rs:fill:120:240/g:sm/plain/" + crew + "@png"},
		{"imgproxy-objw", "/insecure/rs:fill:120:240/g:objw:face:4:horse:1/op:we:10/plain/" + dogs + "@png"},
		{"imgproxy-crop-objects", "/insecure/c_obj:1.2:cat/rs:fit:200:200/plain/" + cat + "@png"},
		{"imgproxy-bd", "/insecure/rs:fit:320:320/bd:10:face/plain/" + crew + "@png"},
		{"imgproxy-dd", "/insecure/rs:fit:320:320/dd:1:person:dog/plain/" + dogs + "@png"},
		{"imgix-crop-faces", "/imgix/" + crew + "?w=240&h=120&fit=crop&crop=faces&fm=png"},
		{"imgix-crop-object", "/imgix/" + dogs + "?w=120&h=240&fit=crop&crop=dog&fm=png"},
		{"imgix-facearea", "/imgix/" + crew + "?w=160&h=160&fit=facearea&facepad=2&faceindex=2&fm=png"},
		{"imgix-face-pixel", "/imgix/" + crew + "?w=320&face-pixel=10&fm=png"},
		{"imgix-face-blur", "/imgix/" + crew + "?w=320&face-blur=60&fm=png"},
		{"cloudinary-thumb-face", "/cloudinary/demo/image/upload/c_thumb,g_face,w_150,h_150,f_png/" + dogs},
		{"cloudinary-fill-faces", "/cloudinary/demo/image/upload/c_fill,g_faces,w_240,h_120,f_png/" + crew},
		{"cloudinary-auto-dog", "/cloudinary/demo/image/upload/c_fill,g_auto:dog,w_120,h_240,f_png/" + dogs},
		{"cloudinary-pixelate-faces", "/cloudinary/demo/image/upload/w_320,e_pixelate_faces:12,f_png/" + crew},
		{"cloudinary-blur-faces", "/cloudinary/demo/image/upload/w_320,e_blur_faces:800,f_png/" + crew},
		{"imagekit-fo-face", "/imagekit/" + crew + "?tr=w-240,h-120,fo-face,f-png"},
		{"imagekit-fo-dog-zoom", "/imagekit/" + dogs + "?tr=w-150,h-150,fo-dog,z-0.8,f-png"},
		{"cloudflare-gravity-face", "/cdn-cgi/image/width=150,height=150,fit=cover,gravity=face,zoom=0.5,format=png/" + dogs},
		{"bunny-face-crop", "/bunny/" + crew + "?face_crop=200,200&width=100&format=png"},
		{"thumbor-smart", "/thumbor/unsafe/120x240/smart/filters:format(png)/" + crew},
		{"awssih-smart-crop", sih(t, map[string]any{"key": dogs, "edits": map[string]any{"smartCrop": map[string]any{"padding": 20}, "toFormat": "png"}})},
	}
}

// TestDetectDialects serves every dialect's face and object options end to
// end with a fake detector (the reference detections), compares each
// response with its golden, and checks the detector ran once per model
// and source (the cache), /info detect_objects included.
func TestDetectDialects(t *testing.T) {
	// Not parallel: run re-applies libvips' process-global allowlists.
	fake := newFakeDetector(t)
	a := start(t, detectDialectsConfig(t, ""), hooks{detector: fake})
	base := "http://" + a.public.String()
	for _, tc := range detectDialectCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			r := get(t, base+tc.path)
			if r.status != http.StatusOK {
				t.Fatalf("GET %s: %d %q", tc.path, r.status, r.body)
			}
			if ig := r.header.Get("X-Xi-Ignored"); ig != "" {
				t.Errorf("X-Xi-Ignored = %q", ig)
			}
			img, _, err := image.Decode(bytes.NewReader(r.body))
			if err != nil {
				t.Fatal(err)
			}
			golden.Compare(t, "app/detect-"+tc.name, img, golden.Options{Tolerance: 0.03})
		})
	}
	// Faces on the crew and the dogs, objects on the dogs and the cat:
	// once per model and source, whatever the number of requests.
	if f, o := fake.faces.Load(), fake.objs.Load(); f != 2 || o != 2 {
		t.Errorf("detector calls: faces %d, objects %d; want 2 and 2 (cached per source)", f, o)
	}

	t.Run("info", func(t *testing.T) {
		r := get(t, base+"/info/insecure"+noDefaults+"/do:1/plain/detect/astronaut-dogs.jpg")
		if r.status != http.StatusOK {
			t.Fatalf("GET: %d %s", r.status, r.body)
		}
		var body struct {
			Objects []struct {
				ClassID   int          `json:"class_id"`
				ClassName string       `json:"class_name"`
				Left      float64      `json:"left"`
				Width     float64      `json:"width"`
				Landmarks [][2]float64 `json:"landmarks"`
			} `json:"objects"`
		}
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatal(err)
		}
		want := detectfix.Load(t, detectfix.Dogs)
		all := append(append([]plan.Detected(nil), want.Faces...), want.Objects...)
		if len(body.Objects) != len(all) {
			t.Fatalf("objects %s, want %d", r.body, len(all))
		}
		for i, o := range body.Objects {
			w := all[i]
			if o.ClassName != w.Class || o.ClassID != transform.ObjectClassID(w.Class) && w.Class != "face" ||
				math.Abs(o.Left-w.Box.Left) > 1.0/640 || math.Abs(o.Width-w.Box.Width) > 2.0/640 || len(o.Landmarks) != len(w.Landmarks) {
				t.Errorf("object %d = %+v, want %+v", i, o, w)
			}
		}
		// imgix fm=json&faces=1: the faces only.
		r = get(t, base+"/imgix/detect/apollo11-crew.jpg?fm=json&faces=1")
		if err := json.Unmarshal(r.body, &body); r.status != http.StatusOK || err != nil || len(body.Objects) != 3 {
			t.Errorf("imgix faces: %d %s", r.status, r.body)
		}
		for _, o := range body.Objects {
			if o.ClassName != "face" || len(o.Landmarks) != 5 {
				t.Errorf("imgix faces: %+v", o)
			}
		}
	})
	if f, o := fake.faces.Load(), fake.objs.Load(); f != 2 || o != 2 {
		t.Errorf("after /info: faces %d, objects %d; want the cached 2 and 2", f, o)
	}
}

// TestDetectUnavailable: with detection off (the slim image's situation)
// every face and object option answers 422, and smart gravity falls back
// to the attention crop.
func TestDetectUnavailable(t *testing.T) {
	// Not parallel: see TestDetectDialects.
	a := start(t, detectDialectsConfig(t, "detect:\n  enabled: \"false\"\n"), hooks{})
	base := "http://" + a.public.String()
	for _, tc := range detectDialectCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			r := get(t, base+tc.path)
			want := http.StatusUnprocessableEntity
			if tc.name == "imgproxy-sm-faces" || tc.name == "thumbor-smart" {
				want = http.StatusOK
			}
			if r.status != want {
				t.Errorf("GET %s: %d %q, want %d", tc.path, r.status, r.body, want)
			}
		})
	}
	if r := get(t, base+"/info/insecure/do:1/plain/detect/astronaut-dogs.jpg"); r.status != http.StatusUnprocessableEntity {
		t.Errorf("/info do:1: %d %s", r.status, r.body)
	}
	// Classification is optional: answered without it, and said so.
	r := get(t, base+"/info/insecure"+noDefaults+"/cl:3/plain/detect/astronaut-dogs.jpg")
	if r.status != http.StatusOK || string(r.body) != "{}" ||
		r.header.Get("X-Xi-Ignored") != "classify (classification needs the full image)" {
		t.Errorf("/info cl:3: %d %s, X-Xi-Ignored %q", r.status, r.body, r.header.Get("X-Xi-Ignored"))
	}
	// An unknown class is a client error before anything is detected.
	if r := get(t, base+"/insecure/rs:fill:10:10/g:obj:unicorn/plain/detect/astronaut-dogs.jpg"); r.status != http.StatusBadRequest {
		t.Errorf("unknown class: %d %s", r.status, r.body)
	}
}

// TestDetectVideoComposite: a video sprite or animation (vtt, vta) is
// assembled from many frames, so it is never detected: face and object
// gravities fall back to the smart crop, the detection filters draw
// nothing, and the detector is not called (full image; video needs
// FFmpeg).
func TestDetectVideoComposite(t *testing.T) {
	// Not parallel: see TestDetectDialects.
	if !codec.VideoAvailable() {
		t.Skip("video needs FFmpeg (the full image)")
	}
	fake := newFakeDetector(t)
	a := start(t, detectDialectsConfig(t, ""), hooks{detector: fake})
	base := "http://" + a.public.String()
	for _, path := range []string{
		"/insecure/vtt:-1:2:2:160:0/rs:fill:100:100/g:obj:face/plain/video/clip-av1.mp4@jpg",
		"/insecure/vtt:-1:2:2:160:0/rs:fill:100:100/g:sm/plain/video/clip-av1.mp4@jpg",
		"/insecure/vta:-1:0:3:160:0/c_obj:1:cat/bd:10:face/dd:1:person/plain/video/clip-av1.mp4@webp",
	} {
		r := get(t, base+path)
		if r.status != http.StatusOK {
			t.Errorf("GET %s: %d %q", path, r.status, r.body)
		}
	}
	if f, o := fake.faces.Load(), fake.objs.Load(); f != 0 || o != 0 {
		t.Errorf("detector calls on video composites: faces %d, objects %d; want none", f, o)
	}
}

// TestDetectModels runs the shipped models end to end (the full image;
// skipped where detection is unavailable, where TestDetectUnavailable
// covers the behaviour): /info finds the reference detections, and face
// gravity crops like the golden made from them.
func TestDetectModels(t *testing.T) {
	// Not parallel: see TestDetectDialects.
	cfg := detectDialectsConfig(t, "")
	if d, err := buildDetector(t.Context(), cfg, nil, slog.New(slog.DiscardHandler)); err != nil || d == nil {
		t.Skipf("detection unavailable here (%v)", err)
	} else {
		d.Close()
	}
	a := start(t, cfg, hooks{})
	base := "http://" + a.public.String()
	r := get(t, base+"/info/insecure"+noDefaults+"/do:1/plain/detect/apollo11-crew.jpg")
	if r.status != http.StatusOK {
		t.Fatalf("/info: %d %s", r.status, r.body)
	}
	var raw struct {
		Objects []struct {
			ClassName string  `json:"class_name"`
			Left      float64 `json:"left"`
			Top       float64 `json:"top"`
			Width     float64 `json:"width"`
			Height    float64 `json:"height"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(r.body, &raw); err != nil {
		t.Fatal(err)
	}
	want := detectfix.Load(t, detectfix.Crew)
	for _, w := range append(append([]plan.Detected(nil), want.Faces...), want.Objects...) {
		best := 0.0
		for _, o := range raw.Objects {
			if o.ClassName != w.Class {
				continue
			}
			got := transform.Area{Left: o.Left, Top: o.Top, Width: o.Width, Height: o.Height}
			best = max(best, iou(got, w.Box))
		}
		if best < 0.5 {
			t.Errorf("reference %s %+v not found (best IoU %.2f) in %s", w.Class, w.Box, best, r.body)
		}
	}
	for _, tc := range detectDialectCases(t)[:2] {
		img := getPNG(t, base+tc.path)
		golden.Compare(t, "app/detect-"+tc.name, img, golden.Options{Tolerance: 0.05})
	}
	// WP-110: the shipped classifier reads the cat on the laptop.
	r = get(t, base+"/info/insecure"+noDefaults+"/cl:5/plain/detect/cat-laptop.jpg")
	var classes struct {
		Classification []struct {
			ClassID    int     `json:"class_id"`
			Name       string  `json:"name"`
			Confidence float64 `json:"confidence"`
		} `json:"classification"`
	}
	if err := json.Unmarshal(r.body, &classes); r.status != http.StatusOK || err != nil || len(classes.Classification) != 5 {
		t.Fatalf("/info cl:5: %d %s", r.status, r.body)
	}
	if c := classes.Classification[0]; c.ClassID != 284 || c.Name != "Siamese cat" || c.Confidence < 0.4 {
		t.Errorf("top class %+v, want Siamese cat (the reference reading, testdata/classify)", c)
	}
	if !codec.VideoAvailable() {
		return
	}
	// WP-91: the models find the crew's faces on the frames of the face
	// clips (the rotated one upright), and face gravity crops like on the
	// photo.
	for _, src := range []string{faceVideo, faceVideoRotated} {
		r := get(t, base+"/info/insecure"+noDefaults+"/do:1/plain/"+src)
		if r.status != http.StatusOK {
			t.Fatalf("/info %s: %d %s", src, r.status, r.body)
		}
		raw.Objects = nil
		if err := json.Unmarshal(r.body, &raw); err != nil {
			t.Fatal(err)
		}
		for _, w := range want.Faces {
			best := 0.0
			for _, o := range raw.Objects {
				best = max(best, iou(transform.Area{Left: o.Left, Top: o.Top, Width: o.Width, Height: o.Height}, w.Box))
			}
			if best < 0.5 {
				t.Errorf("%s: face %+v not found (best IoU %.2f) in %s", src, w.Box, best, r.body)
			}
		}
		tc := detectDialectCases(t)[0]
		img := getPNG(t, base+strings.ReplaceAll(tc.path, detectfix.Crew, src))
		golden.Compare(t, "app/detect-"+tc.name, img, golden.Options{Tolerance: 0.06})
	}
}

func iou(a, b transform.Area) float64 {
	x0, y0 := max(a.Left, b.Left), max(a.Top, b.Top)
	x1, y1 := min(a.Left+a.Width, b.Left+b.Width), min(a.Top+a.Height, b.Top+b.Height)
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	in := (x1 - x0) * (y1 - y0)
	return in / (a.Width*a.Height + b.Width*b.Height - in)
}
