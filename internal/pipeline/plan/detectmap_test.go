package plan_test

import (
	"image"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// approxBoxes compares mapped fractions (a pixel of a few hundred).
var approxBoxes = cmpopts.EquateApprox(0, 1e-9)

// exifDisplay maps a point of the stored image (fractions) to where the
// EXIF orientation o shows it: the TIFF/EXIF definitions, independent of
// the planner's rotate-then-flip steps.
func exifDisplay(o int, x, y float64) (float64, float64) {
	switch o {
	case 2:
		return 1 - x, y
	case 3:
		return 1 - x, 1 - y
	case 4:
		return x, 1 - y
	case 5:
		return y, x
	case 6:
		return 1 - y, x
	case 7:
		return 1 - y, 1 - x
	case 8:
		return y, 1 - x
	default:
		return x, y
	}
}

// mapBox maps a box's corners with f and returns their bounding box.
func mapBox(a transform.Area, f func(x, y float64) (float64, float64)) transform.Area {
	x0, y0 := f(a.Left, a.Top)
	x1, y1 := f(a.Left+a.Width, a.Top+a.Height)
	return area(min(x0, x1), min(y0, y1), max(x0, x1), max(y0, y1))
}

// TestDetectionsStored: for every EXIF orientation, the stored-orientation
// box Stored returns is shown by the orientation where the detector saw it.
func TestDetectionsStored(t *testing.T) {
	t.Parallel()
	shown := &plan.Detections{
		Faces:   []plan.Detected{{Class: "face", Score: 0.9, Box: area(0.1, 0.2, 0.3, 0.5), Landmarks: [][2]float64{{0.15, 0.3}}}},
		Objects: []plan.Detected{objectAt("dog", 0.6, 0.55, 0.95, 0.9)},
	}
	for o := 1; o <= 8; o++ {
		t.Run(strconv.Itoa(o), func(t *testing.T) {
			t.Parallel()
			stored := shown.Stored(o, true)
			back := func(d plan.Detected) plan.Detected {
				d.Box = mapBox(d.Box, func(x, y float64) (float64, float64) { return exifDisplay(o, x, y) })
				if d.Landmarks != nil {
					marks := make([][2]float64, 0, len(d.Landmarks)) // Stored(1) shares shown's
					for _, l := range d.Landmarks {
						x, y := exifDisplay(o, l[0], l[1])
						marks = append(marks, [2]float64{x, y})
					}
					d.Landmarks = marks
				}
				return d
			}
			got := &plan.Detections{Faces: []plan.Detected{back(stored.Faces[0])}, Objects: []plan.Detected{back(stored.Objects[0])}}
			if diff := cmp.Diff(shown, got, approxBoxes); diff != "" {
				t.Errorf("orientation %d (-shown +stored shown again):\n%s", o, diff)
			}
			if o != 1 && cmp.Equal(shown.Stored(o, false), shown) == false {
				t.Error("auto_rotate off maps the boxes")
			}
		})
	}
	var none *plan.Detections
	if none.Stored(6, true) != nil {
		t.Error("nil detections mapped to something")
	}
}

// TestDetectionsMapStep: each step's mapping, on a 200×100 image with one
// face at x 0.1–0.3, y 0.2–0.6 (pixels 20–60, 20–60).
func TestDetectionsMapStep(t *testing.T) {
	t.Parallel()
	face := area(0.1, 0.2, 0.3, 0.6)
	in := &plan.Detections{Faces: []plan.Detected{{Class: "face", Score: 0.9, Box: face}}}
	nw := plan.Gravity{Type: transform.GravityNorthWest}
	tests := []struct {
		name   string
		step   plan.Step
		want   *transform.Area // nil: dropped
		ow, oh int
		ok     bool
	}{
		{"colour", plan.ColourStep{}, &face, 200, 100, true},
		{"filters", plan.FilterStep{Blur: 3}, &face, 200, 100, true},
		{"resize", plan.ResizeStep{ScaleX: 0.5, ScaleY: 0.5, W: 100, H: 50}, &face, 100, 50, true},
		{"rotate 90", plan.RotateStep{Angle: 90}, new(area(0.4, 0.1, 0.8, 0.3)), 100, 200, true},
		{"rotate 180", plan.RotateStep{Angle: 180}, new(area(0.7, 0.4, 0.9, 0.8)), 200, 100, true},
		{"rotate 270", plan.RotateStep{Angle: 270}, new(area(0.2, 0.7, 0.6, 0.9)), 100, 200, true},
		{"flip h", plan.FlipStep{Horizontal: true}, new(area(0.7, 0.2, 0.9, 0.6)), 200, 100, true},
		{"flip v", plan.FlipStep{Vertical: true}, new(area(0.1, 0.4, 0.3, 0.8)), 200, 100, true},
		{"flip both", plan.FlipStep{Horizontal: true, Vertical: true}, new(area(0.7, 0.4, 0.9, 0.8)), 200, 100, true},
		// Pixels 10..110 × 10..60: the face 20–60 × 20–60 → 10–50 × 10–50.
		{
			"pre-crop",
			plan.PreCropStep{Width: 100, Height: 50, Gravity: plan.Gravity{Type: transform.GravityNorthWest, X: 10, Y: 10}, OffsetScale: 1},
			new(area(0.1, 0.2, 0.5, 1)), 100, 50, true,
		},
		// A crop through the face clips it: 0..40 × 0..100 keeps 20–40.
		{"crop clips", plan.CropStep{Width: 40, Gravity: nw}, new(area(0.5, 0.2, 1, 0.6)), 40, 100, true},
		{"crop drops", plan.CropStep{Width: 100, Gravity: plan.Gravity{Type: transform.GravityEast}}, nil, 100, 100, true},
		{"smart crop", plan.CropStep{Width: 100, Smart: plan.InterestingAttention}, nil, 100, 100, false},
		{"trim", plan.TrimStep{Threshold: 10}, nil, 200, 100, false},
		// Padding 10 around: 220×120, the face at 30–70 × 30–70.
		{"padding", plan.PaddingStep{Top: 10, Right: 10, Bottom: 10, Left: 10}, new(area(30.0/220, 0.25, 70.0/220, 70.0/120)), 220, 120, true},
		{"border", plan.BorderStep{Top: 10, Right: 10, Bottom: 10, Left: 10}, new(area(30.0/220, 0.25, 70.0/220, 70.0/120)), 220, 120, true},
		// Extend to 400×100 centred: x + 100.
		{
			"extend",
			plan.ExtendStep{Width: 400, Height: 100, Gravity: plan.Gravity{Type: transform.GravityCenter}},
			new(area(0.3, 0.2, 0.4, 0.6)), 400, 100, true,
		},
		{"extend nothing", plan.ExtendStep{Width: 100, Height: 50}, &face, 200, 100, true},
		{"rotate any 180", plan.RotateAnyStep{Angle: 180}, new(area(0.7, 0.4, 0.9, 0.8)), 200, 100, true},
		{"mask ellipse", plan.MaskStep{Shape: transform.MaskEllipse, Trim: true}, &face, 200, 100, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ow, oh, ok := in.MapStep(tc.step, 200, 100)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if ow != tc.ow || oh != tc.oh {
				t.Errorf("size %dx%d, want %dx%d", ow, oh, tc.ow, tc.oh)
			}
			var want []plan.Detected
			if tc.want != nil {
				want = []plan.Detected{{Class: "face", Score: 0.9, Box: *tc.want}}
			}
			if diff := cmp.Diff(want, got.Faces, approxBoxes, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("faces (-want +got):\n%s", diff)
			}
		})
	}

	// The executor's own rectangles (smart crop, trim).
	got := in.Crop(image.Rect(10, 10, 110, 60), 200, 100)
	if diff := cmp.Diff([]plan.Detected{{Class: "face", Score: 0.9, Box: area(0.1, 0.2, 0.5, 1)}}, got.Faces, approxBoxes); diff != "" {
		t.Errorf("Crop (-want +got):\n%s", diff)
	}
	// An arbitrary rotation: the bounding box of the rotated corners on
	// the grown canvas, inside it.
	rot, ow, oh, ok := in.MapStep(plan.RotateAnyStep{Angle: 30}, 200, 100)
	if !ok || ow <= 200 || oh <= 100 || len(rot.Faces) != 1 {
		t.Fatalf("rotate any 30: %+v %dx%d %v", rot, ow, oh, ok)
	}
	if b := rot.Faces[0].Box; b.Left < 0 || b.Top < 0 || b.Left+b.Width > 1 || b.Top+b.Height > 1 || b.Width <= face.Width*200/float64(ow) {
		t.Errorf("rotate any 30: box %+v", b)
	}
	var none *plan.Detections
	if d, _, _, ok := none.MapStep(plan.RotateStep{Angle: 90}, 10, 10); d != nil || !ok {
		t.Error("nil detections mapped to something")
	}
}

// chainedFace is the pipeline-2 face placement test: pipeline 1 crops the
// left half of a 400×200 source (EXIF-oriented as o), pipeline 2 fills
// 50×50 on the face.
func chainedFace(o int) (*transform.Request, plan.SourceMeta) {
	// The source as displayed is 400×200 with a face at x 0.3–0.4 (in the
	// left half), y 0.25–0.5. Stored, a 90° orientation is 200×400.
	w, h := 400, 200
	if o >= 5 {
		w, h = h, w
	}
	meta := withDets(plan.SourceMeta{Width: w, Height: h, Format: "jpeg", Orientation: o}, []plan.Detected{faceAt(0.3, 0.25, 0.4, 0.5)})
	r := req(
		transform.Pipeline{Crop: &transform.Crop{Width: rel(0.5), Gravity: transform.Gravity{Type: transform.GravityWest}}},
		transform.Pipeline{
			Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(50), Height: px(50)},
			Gravity: transform.Gravity{Type: transform.GravityFace},
		},
	)
	return r, meta
}

// TestChainedDetections: a detection gravity in a chained pipeline places
// the crop on the source's boxes mapped through the pipeline before it,
// for every EXIF orientation, in Build's prediction and in Chain.
func TestChainedDetections(t *testing.T) {
	t.Parallel()
	for o := 1; o <= 8; o++ {
		t.Run(strconv.Itoa(o), func(t *testing.T) {
			t.Parallel()
			r, meta := chainedFace(o)
			p := mustBuild(t, r, &meta)
			// Pipeline 1 keeps the left 200×200 (displayed): the face is
			// at x 0.6–0.8, y 0.25–0.5 of it.
			if got := [2]int{p.Output.Width, p.Output.Height}; got != [2]int{200, 200} {
				t.Fatalf("pipeline 1 output %v", got)
			}
			want := &plan.Detections{Faces: []plan.Detected{faceAt(0.6, 0.25, 0.8, 0.5)}}
			got := plan.SourceDetections(r, &meta, p).MapPlan(p, p.Load.Width, p.Load.Height)
			if diff := cmp.Diff(want, got, approxBoxes); diff != "" {
				t.Fatalf("mapped (-want +got):\n%s", diff)
			}
			// Pipeline 2 (fill 50×50 on 200×200, scale 0.25) centres the
			// face: x 0.7 × 50 = 35 → the whole width is kept, nothing to
			// crop. Its gravity is the face, not the smart fallback.
			c, err := plan.Chain(r, meta, plan.Limits{}, 1, plan.Input{Width: 200, Height: 200, Detections: got}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Unplanned) != 0 {
				t.Errorf("Unplanned = %v", c.Unplanned)
			}
			if pc := p.Chained[0]; len(pc.Unplanned) != 0 || !cmp.Equal(rects(pc), rects(c)) {
				t.Errorf("prediction %v %v, chain %v", rects(pc), pc.Unplanned, rects(c))
			}
		})
	}

	// A 2:1 result on the 200×200 input (fill 100×50 at scale 0.5): the
	// crop of 100×100 → 100×50 is placed on the face centre y 0.375 ×
	// 100 = 37.5 → top 12.5, rounded.
	r, meta := chainedFace(6)
	r.Pipelines[1].Resize = transform.Resize{Type: transform.ResizeFill, Width: px(100), Height: px(50)}
	d := &plan.Detections{Faces: []plan.Detected{faceAt(0.6, 0.25, 0.8, 0.5)}}
	c, err := plan.Chain(r, meta, plan.Limits{}, 1, plan.Input{Width: 200, Height: 200, Detections: d}, nil)
	if err != nil {
		t.Fatal(err)
	}
	crops := stepsOf[plan.CropStep](c.Steps)
	if len(crops) != 1 || crops[0].Gravity.Type != transform.GravityObject {
		t.Fatalf("crops %+v", crops)
	}
	x, y, w, h, _ := crops[0].Rect(100, 100)
	if x != 0 || (y != 12 && y != 13) || w != 100 || h != 50 {
		t.Errorf("face crop %d,%d %dx%d, want 0,12 100x50", x, y, w, h)
	}
	// Without detections (a step could not carry them) the face gravity
	// falls back to the smart crop and says so.
	c, err = plan.Chain(r, meta, plan.Limits{}, 1, plan.Input{Width: 200, Height: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if crops := stepsOf[plan.CropStep](c.Steps); len(crops) != 1 || crops[0].Smart == plan.InterestingNone || len(c.Unplanned) != 1 || c.Unplanned[0] != "detections" {
		t.Errorf("without detections: %+v %v", crops, c.Unplanned)
	}
}

// TestChainedDetectionsGeometry: the prediction maps the boxes through
// the first pipeline's rotate, flip, resize, padding and extend, and drops
// them after a smart crop or a trim (the executor maps what it cut).
func TestChainedDetectionsGeometry(t *testing.T) {
	t.Parallel()
	face := faceAt(0.1, 0.2, 0.3, 0.6) // on a 200×100 source
	meta := withDets(jpeg(200, 100), []plan.Detected{face})
	second := transform.Pipeline{Gravity: transform.Gravity{Type: transform.GravityFace}}
	tests := []struct {
		name  string
		first transform.Pipeline
		want  *transform.Area
	}{
		{"none", transform.Pipeline{}, new(face.Box)},
		{"resize", transform.Pipeline{Resize: transform.Resize{Width: px(100)}}, new(face.Box)},
		{"rotate 90", transform.Pipeline{Rotate: 90}, new(area(0.4, 0.1, 0.8, 0.3))},
		{"flip", transform.Pipeline{Flip: transform.Flip{H: true}}, new(area(0.7, 0.2, 0.9, 0.6))},
		{"rotate and flip", transform.Pipeline{Rotate: 90, Flip: transform.Flip{V: true}}, new(area(0.4, 0.7, 0.8, 0.9))},
		{"padding", transform.Pipeline{Padding: &transform.Padding{Top: 10, Right: 10, Bottom: 10, Left: 10}}, new(area(30.0/220, 0.25, 70.0/220, 70.0/120))},
		{"extend", transform.Pipeline{
			Resize: transform.Resize{Width: px(400), Height: px(100)},
			Extend: transform.Extend{Enabled: true, Gravity: transform.Gravity{Type: transform.GravityCenter}},
		}, new(area(0.3, 0.2, 0.4, 0.6))},
		{"smart crop", transform.Pipeline{
			Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(50), Height: px(50)},
			Gravity: transform.Gravity{Type: transform.GravitySmart},
		}, nil},
		{"trim", transform.Pipeline{Trim: &transform.Trim{Threshold: 10}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := meta
			if tc.first.Gravity.Type == transform.GravitySmart {
				m.Detections = &plan.Detections{} // smart gravity without faces: attention
			}
			p := mustBuild(t, req(tc.first, second), &m)
			got := plan.SourceDetections(req(tc.first, second), &m, p).MapPlan(p, p.Load.Width, p.Load.Height)
			if tc.want == nil {
				if got != nil && len(got.Faces) > 0 {
					t.Errorf("mapped %+v, want dropped", got)
				}
				if u := p.Chained[0].Unplanned; len(u) != 1 || u[0] != "detections" {
					t.Errorf("chained Unplanned = %v", u)
				}
				return
			}
			want := &plan.Detections{Faces: []plan.Detected{{Class: face.Class, Score: face.Score, Box: *tc.want}}}
			if diff := cmp.Diff(want, got, approxBoxes); diff != "" {
				t.Errorf("mapped (-want +got):\n%s", diff)
			}
			if u := p.Chained[0].Unplanned; len(u) != 0 {
				t.Errorf("chained Unplanned = %v", u)
			}
		})
	}
}
