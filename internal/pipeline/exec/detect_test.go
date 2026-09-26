//go:build cgo

package exec_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/detectfix"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// detectCase is a golden case on a detection fixture, with the reference
// detections of testdata/detect/expected.json in the job (what
// internal/app passes after running the models), so it needs no ONNX
// Runtime and runs on the slim builder too.
type detectCase struct {
	name, fixture, options string
	mutate                 func(p *transform.Pipeline) // nil: the options only
	w, h                   int
}

var detectCases = []detectCase{
	// Face and object gravity (WP-72).
	{name: "detect-face-gravity", fixture: detectfix.Crew, options: "rs:fill:240:120/g:obj:face", w: 240, h: 120},
	{name: "detect-smart-faces", fixture: detectfix.Crew, options: "rs:fill:120:240/g:sm", w: 120, h: 240},
	{name: "detect-obj-dog", fixture: detectfix.Dogs, options: "rs:fill:120:240/g:obj:dog", w: 120, h: 240},
	{name: "detect-objw", fixture: detectfix.Dogs, options: "rs:fill:120:240/g:objw:face:4:horse:1", w: 120, h: 240},
	{name: "detect-op-north", fixture: detectfix.Dogs, options: "rs:fill:240:100/g:obj:face/op:no:8", w: 240, h: 100},
	{name: "detect-op-prop", fixture: detectfix.Crew, options: "rs:fill:100:200/g:obj:person/op:prop", w: 100, h: 200},
	{name: "detect-crop-gravity", fixture: detectfix.Dogs, options: "c:200:200:obj:face/rs:fit:100:100", w: 100, h: 100},
	{name: "detect-crop-objects", fixture: detectfix.Cat, options: "c_obj:1.1:cat/rs:fit:200:200", w: 200, h: 159},
	{name: "detect-crop-objects-fill", fixture: detectfix.Cat, options: "c_obj:1:laptop/rs:fill:200:100", w: 200, h: 100},
	{name: "detect-face-pad", fixture: detectfix.Crew, options: "rs:fill:160:160:1", mutate: func(p *transform.Pipeline) {
		p.Gravity = transform.Gravity{Type: transform.GravityFace, FacePad: 1.5, FaceIndex: new(0)}
	}, w: 160, h: 160},
	{name: "detect-rotated-face", fixture: detectfix.Dogs, options: "rot:90/rs:fill:120:240/g:obj:face", w: 120, h: 240},
	// Detection filters (WP-73).
	{name: "detect-blur-faces", fixture: detectfix.Crew, options: "rs:fit:320:320/bd:8:face", w: 320, h: 252},
	{name: "detect-pixelate-faces", fixture: detectfix.Crew, options: "rs:fit:320:320", mutate: func(p *transform.Pipeline) {
		p.BlurDetections = &transform.DetectionFilter{Pixelate: 8, Classes: []string{"face"}}
	}, w: 320, h: 252},
	{name: "detect-draw", fixture: detectfix.Dogs, options: "rs:fit:320:320/dd:1", w: 320, h: 256},
	{name: "detect-draw-rotated", fixture: detectfix.Dogs, options: "rot:90/rs:fit:320:320/dd:1:face:dog", w: 256, h: 320},
	{name: "detect-draw-grey", fixture: detectfix.Cat, options: "rs:fit:320:320/mc:1/dd:1:animal", w: 320, h: 210},
}

func detectRequest(tb testing.TB, tc *detectCase) *transform.Request {
	tb.Helper()
	req := parse(tb, tc.options)
	if tc.mutate != nil {
		tc.mutate(&req.Pipelines[0])
		req.ApplyDefaults(transform.StandardDefaults())
		if err := req.Validate(); err != nil {
			tb.Fatal(err)
		}
	}
	return req
}

// processDetected runs req on a fixture with dets as its detections.
func processDetected(tb testing.TB, fixture string, req *transform.Request, dets *plan.Detections) output {
	tb.Helper()
	j, buf := job(tb, readFixture(tb, fixture), req, transform.FormatPNG)
	defer buf.Release()
	j.Meta.Detections = dets
	res, err := newExecutor(tb).Run(tb.Context(), j)
	if err != nil {
		tb.Fatalf("run: %v", err)
	}
	defer res.Body.Release()
	enc := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, enc)
	return output{res: res, img: img, frames: frames, bytes: enc}
}

// TestGoldenDetections: face and object gravity, objects positions,
// crop_objects, face pads and the detection filters on the detection
// fixtures, compared perceptually with testdata/golden/exec.
func TestGoldenDetections(t *testing.T) {
	t.Parallel()
	for _, tc := range detectCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processDetected(t, tc.fixture, detectRequest(t, &tc), detectfix.Load(t, tc.fixture))
			if b := o.img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
				t.Errorf("size %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.w, tc.h)
			}
			if len(o.res.Skipped) != 0 {
				t.Errorf("Skipped = %q", o.res.Skipped)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: 0.03})
		})
	}
}

// TestDetectionGravityMoves: the face gravity crop differs from the
// attention crop the same request gets without detections, and without
// detections the filters change nothing and are reported.
func TestDetectionGravityMoves(t *testing.T) {
	t.Parallel()
	req := parse(t, "rs:fill:240:120/g:obj:face")
	with := processDetected(t, detectfix.Crew, req, detectfix.Load(t, detectfix.Crew))
	without := processDetected(t, detectfix.Crew, req, nil)
	if sameImage(with.img, without.img) {
		t.Error("face gravity cropped like the smart fallback")
	}

	req = parse(t, "rs:fit:200:200/bd:8/dd:1")
	plain := processDetected(t, detectfix.Crew, parse(t, "rs:fit:200:200"), nil)
	none := processDetected(t, detectfix.Crew, req, nil)
	if !sameImage(plain.img, none.img) {
		t.Error("detection filters without detections changed the image")
	}
	if len(none.res.Skipped) != 1 || none.res.Skipped[0] != "detections" {
		t.Errorf("Skipped = %q, want [detections]", none.res.Skipped)
	}
}

// TestDetectionBlurOnlyBoxes: blurring and pixelating the faces leaves
// every pixel outside their boxes unchanged.
func TestDetectionBlurOnlyBoxes(t *testing.T) {
	t.Parallel()
	dets := detectfix.Load(t, detectfix.Crew)
	plain := processDetected(t, detectfix.Crew, parse(t, "rs:fit:320:320"), dets)
	for _, f := range []*transform.DetectionFilter{{Sigma: 8, Classes: []string{"face"}}, {Pixelate: 6, Classes: []string{"face"}}} {
		req := parse(t, "rs:fit:320:320")
		req.Pipelines[0].BlurDetections = f
		got := processDetected(t, detectfix.Crew, req, dets)
		b := got.img.Bounds()
		inside := func(x, y int) bool {
			for _, d := range dets.Faces {
				r := image.Rect(
					int(d.Box.Left*float64(b.Dx()))-1, int(d.Box.Top*float64(b.Dy()))-1,
					int((d.Box.Left+d.Box.Width)*float64(b.Dx()))+2, int((d.Box.Top+d.Box.Height)*float64(b.Dy()))+2)
				if image.Pt(x, y).In(r) {
					return true
				}
			}
			return false
		}
		changedIn, changedOut := 0, 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if got.img.At(x, y) == plain.img.At(x, y) {
					continue
				}
				if inside(x, y) {
					changedIn++
				} else {
					changedOut++
				}
			}
		}
		if changedOut != 0 || changedIn == 0 {
			t.Errorf("%+v: %d pixels changed outside the faces, %d inside", *f, changedOut, changedIn)
		}
	}
}

func sameImage(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if a.At(x, y) != b.At(x, y) {
				return false
			}
		}
	}
	return true
}

// TestDetectionsNoLeak runs the detection gravities and filters (blur,
// pixelate, draw with labels) with libvips leak checking on. Not parallel:
// the counters are global.
func TestDetectionsNoLeak(t *testing.T) {
	const cycles = 120
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)
	e := newExecutor(t)
	type prepared struct {
		data []byte
		req  *transform.Request
		dets *plan.Detections
	}
	var jobs []prepared
	for _, name := range []string{"detect-face-gravity", "detect-crop-objects", "detect-blur-faces", "detect-pixelate-faces", "detect-draw", "detect-draw-grey"} {
		for i := range detectCases {
			if tc := &detectCases[i]; tc.name == name {
				jobs = append(jobs, prepared{readFixture(t, tc.fixture), detectRequest(t, tc), detectfix.Load(t, tc.fixture)})
			}
		}
	}
	cycle := func(i int) {
		p := &jobs[i%len(jobs)]
		j, buf := job(t, p.data, p.req, transform.FormatPNG)
		defer buf.Release()
		j.Meta.Detections = p.dets
		res, err := e.Run(t.Context(), j)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		res.Body.Release()
	}
	for i := range 2 * len(jobs) {
		cycle(i)
	}
	before := leakcheck.Take()
	for i := range cycles {
		cycle(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "runs")
}
