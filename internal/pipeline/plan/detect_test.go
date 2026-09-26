package plan_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// area is a detection box from fractions.
func area(x0, y0, x1, y1 float64) transform.Area {
	return transform.Area{Left: x0, Top: y0, Width: x1 - x0, Height: y1 - y0}
}

func faceAt(x0, y0, x1, y1 float64) plan.Detected {
	return plan.Detected{Class: transform.FaceClass, Score: 0.9, Box: area(x0, y0, x1, y1)}
}

func objectAt(class string, x0, y0, x1, y1 float64) plan.Detected {
	return plan.Detected{Class: class, Score: 0.8, Box: area(x0, y0, x1, y1)}
}

// withDets is meta with detections.
//
//nolint:gocritic // hugeParam: a test helper returning a modified copy
func withDets(meta plan.SourceMeta, faces []plan.Detected, objects ...plan.Detected) plan.SourceMeta {
	meta.Detections = &plan.Detections{Faces: faces, Objects: objects}
	return meta
}

func mustBuild(t *testing.T, r *transform.Request, meta *plan.SourceMeta) *plan.Plan {
	t.Helper()
	p, err := plan.Build(r, *meta, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// rects are the crop rectangles of a plan (pre-crop and result crop).
func rects(p *plan.Plan) [][4]int {
	var out [][4]int
	for _, s := range p.Steps {
		switch s := s.(type) {
		case plan.PreCropStep:
			out = append(out, [4]int{s.X, s.Y, s.W, s.H})
		case plan.CropStep:
			out = append(out, [4]int{s.X, s.Y, s.W, s.H})
		default:
		}
	}
	return out
}

func TestNeedsDetection(t *testing.T) {
	t.Parallel()
	g := func(t transform.GravityType, classes ...string) transform.Gravity {
		return transform.Gravity{Type: t, Classes: classes}
	}
	tests := []struct {
		name string
		r    *transform.Request
		want plan.DetectionNeeds
	}{
		{"none", req(transform.Pipeline{}), plan.DetectionNeeds{}},
		{"centre", req(transform.Pipeline{Gravity: g(transform.GravityCenter)}), plan.DetectionNeeds{}},
		{"smart", req(transform.Pipeline{Gravity: g(transform.GravitySmart)}), plan.DetectionNeeds{SmartFaces: true}},
		{"face", req(transform.Pipeline{Gravity: g(transform.GravityFace)}), plan.DetectionNeeds{Faces: true}},
		{"obj face", req(transform.Pipeline{Gravity: g(transform.GravityObject, "face")}), plan.DetectionNeeds{Faces: true}},
		{"obj dog", req(transform.Pipeline{Gravity: g(transform.GravityObject, "dog")}), plan.DetectionNeeds{Objects: true}},
		{"obj all", req(transform.Pipeline{Gravity: g(transform.GravityObject)}), plan.DetectionNeeds{Faces: true, Objects: true}},
		{"objw", req(transform.Pipeline{Gravity: transform.Gravity{
			Type: transform.GravityObjectWeighted, Classes: []string{"face", "cat"}, Weights: []float64{2, 1},
		}}), plan.DetectionNeeds{Faces: true, Objects: true}},
		{"crop gravity", req(transform.Pipeline{Crop: &transform.Crop{Width: px(10), Gravity: g(transform.GravityObject, "vehicle")}}), plan.DetectionNeeds{Objects: true}},
		{"crop objects", req(transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1, Classes: []string{"faces"}}}), plan.DetectionNeeds{Faces: true}},
		{"blur", req(transform.Pipeline{BlurDetections: &transform.DetectionFilter{Sigma: 5, Classes: []string{"face"}}}), plan.DetectionNeeds{Faces: true}},
		{"draw all", req(transform.Pipeline{DrawDetections: &transform.DetectionFilter{}}), plan.DetectionNeeds{Faces: true, Objects: true}},
		{"smart and face blur", req(transform.Pipeline{
			Gravity: g(transform.GravitySmart), BlurDetections: &transform.DetectionFilter{Classes: []string{"face"}},
		}), plan.DetectionNeeds{Faces: true}},
		{"smart and dog blur", req(transform.Pipeline{
			Gravity: g(transform.GravitySmart), BlurDetections: &transform.DetectionFilter{Classes: []string{"dog"}},
		}), plan.DetectionNeeds{Objects: true, SmartFaces: true}},
		{"chained only", req(transform.Pipeline{}, transform.Pipeline{Gravity: g(transform.GravityFace)}), plan.DetectionNeeds{}},
		{"info detect", &transform.Request{Kind: transform.KindInfo, Info: &transform.InfoOptions{DetectObjects: true, DetectClasses: []string{"face"}}}, plan.DetectionNeeds{Faces: true}},
		{"info crop", &transform.Request{Kind: transform.KindInfo, Info: &transform.InfoOptions{Crop: &transform.Crop{Width: px(1), Gravity: g(transform.GravityObject, "dog")}}}, plan.DetectionNeeds{Objects: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := plan.NeedsDetection(tc.r)
			if got != tc.want {
				t.Errorf("NeedsDetection = %+v, want %+v", got, tc.want)
			}
			if got.Any() != (tc.want != plan.DetectionNeeds{}) {
				t.Errorf("Any = %v", got.Any())
			}
		})
	}
}

// TestDetectionGravityMatchesFocus: a centred face or object placement
// crops exactly like the focus point at the centre of the detections' box,
// for every EXIF orientation, several source and result sizes, the result
// crop and the pre-crop.
func TestDetectionGravityMatchesFocus(t *testing.T) {
	t.Parallel()
	sources := [][2]int{{1000, 600}, {600, 1000}, {4000, 3000}, {333, 777}}
	boxes := [][2]int{{100, 100}, {300, 50}, {40, 400}}
	faces := [][4]float64{{0.1, 0.1, 0.2, 0.3}, {0.7, 0.2, 0.9, 0.4}, {0.4, 0.8, 0.6, 1}}
	for _, src := range sources {
		for orient := 1; orient <= 8; orient++ {
			for _, box := range boxes {
				for _, f := range faces {
					meta := jpeg(src[0], src[1])
					meta.Orientation = orient
					dmeta := withDets(meta, []plan.Detected{faceAt(f[0], f[1], f[2], f[3])})
					focus := fracGravity((f[0]+f[2])/2, (f[1]+f[3])/2)
					name := fmt.Sprintf("%dx%d/o%d/%dx%d/%v", src[0], src[1], orient, box[0], box[1], f)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						for _, gt := range []transform.GravityType{transform.GravityFace, transform.GravityObject, transform.GravitySmart} {
							fill := func(g transform.Gravity) transform.Pipeline {
								return transform.Pipeline{
									Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(box[0]), Height: px(box[1])},
									Gravity: g,
								}
							}
							got := mustBuild(t, req(fill(transform.Gravity{Type: gt})), &dmeta)
							want := mustBuild(t, req(fill(*focus)), &meta)
							if diff := cmp.Diff(rects(want), rects(got)); diff != "" {
								t.Fatalf("%v result crop differs from the focus point (-focus +detected):\n%s", gt, diff)
							}
							crop := func(g transform.Gravity) transform.Pipeline {
								return transform.Pipeline{Crop: &transform.Crop{Width: px(box[0]), Height: px(box[1]), Gravity: g}}
							}
							got = mustBuild(t, req(crop(transform.Gravity{Type: gt})), &dmeta)
							want = mustBuild(t, req(crop(*focus)), &meta)
							if diff := cmp.Diff(rects(want), rects(got)); diff != "" {
								t.Fatalf("%v pre-crop differs from the focus point (-focus +detected):\n%s", gt, diff)
							}
						}
					})
				}
			}
		}
	}
}

// TestDetectionGravityRotateFlip: the boxes are in the source before the
// rotate and flip options, which move them with the image.
func TestDetectionGravityRotateFlip(t *testing.T) {
	t.Parallel()
	face := faceAt(0.7, 0.2, 0.9, 0.3) // centre 0.8, 0.25
	tests := []struct {
		name   string
		rotate int
		flip   transform.Flip
		fx, fy float64 // the centre in the rotated and flipped image
	}{
		{"none", 0, transform.Flip{}, 0.8, 0.25},
		{"rotate 90", 90, transform.Flip{}, 0.75, 0.8},
		{"rotate 180", 180, transform.Flip{}, 0.2, 0.75},
		{"rotate 270", 270, transform.Flip{}, 0.25, 0.2},
		{"flip h", 0, transform.Flip{H: true}, 0.2, 0.25},
		{"flip v", 0, transform.Flip{V: true}, 0.8, 0.75},
		{"rotate 90 flip h", 90, transform.Flip{H: true}, 0.25, 0.8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := jpeg(1000, 600)
			for _, crop := range []bool{false, true} {
				mk := func(g transform.Gravity) transform.Pipeline {
					p := transform.Pipeline{
						Rotate: tc.rotate, Flip: tc.flip, Gravity: g,
						Resize: transform.Resize{Type: transform.ResizeFill, Width: px(200), Height: px(200)},
					}
					if crop {
						p.Resize = transform.Resize{}
						p.Crop = &transform.Crop{Width: px(200), Height: px(150), Gravity: g}
					}
					return p
				}
				got := mustBuild(t, req(mk(transform.Gravity{Type: transform.GravityFace})), new(withDets(meta, []plan.Detected{face})))
				want := mustBuild(t, req(mk(*fracGravity(tc.fx, tc.fy))), &meta)
				if diff := cmp.Diff(rects(want), rects(got)); diff != "" {
					t.Errorf("crop %v (-focus +face):\n%s", crop, diff)
				}
			}
		})
	}
}

// TestDetectionGravityResultAfterPreCrop: the result crop places the
// boxes relative to the pre-crop.
func TestDetectionGravityResultAfterPreCrop(t *testing.T) {
	t.Parallel()
	meta := jpeg(1000, 1000)
	face := faceAt(0.55, 0.1, 0.65, 0.2) // centre 600, 150
	p := transform.Pipeline{
		Crop:    &transform.Crop{Width: px(600), Height: px(600), Gravity: transform.Gravity{Type: transform.GravityNorthEast}},
		Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(100), Height: px(300)},
		Gravity: transform.Gravity{Type: transform.GravityFace},
	}
	got := mustBuild(t, req(p), new(withDets(meta, []plan.Detected{face})))
	// Pre-crop at 400,0 600×600: the face centre is at 200,150 of it, i.e.
	// 1/3, 1/4.
	p.Gravity = *fracGravity(200.0/600, 150.0/600)
	want := mustBuild(t, req(p), &meta)
	if diff := cmp.Diff(rects(want), rects(got)); diff != "" {
		t.Errorf("(-focus +face):\n%s", diff)
	}
}

// TestObjectsPosition: objects_position places the box in the crop.
// Source 2000×1000 filled into 1000×1000 (no resize): the result crop is
// 1000 wide, the box spans x 1200–1400.
func TestObjectsPosition(t *testing.T) {
	t.Parallel()
	meta := withDets(jpeg(2000, 1000), nil, objectAt("dog", 0.6, 0.4, 0.7, 0.6))
	tests := []struct {
		name string
		op   *transform.ObjectsPosition
		x    int
	}{
		{"centre", nil, 800},
		{"ce explicit", &transform.ObjectsPosition{Type: transform.GravityCenter}, 800},
		{"ce offset right", &transform.ObjectsPosition{Type: transform.GravityCenter, X: 100}, 700},
		{"west", &transform.ObjectsPosition{Type: transform.GravityWest}, 1000}, // clamped to 2000-1000
		{"west relative", &transform.ObjectsPosition{Type: transform.GravityWest, X: -0.1}, 1000},
		{"east", &transform.ObjectsPosition{Type: transform.GravityEast}, 400},
		{"east offset", &transform.ObjectsPosition{Type: transform.GravityEast, X: 100}, 500},
		{"south-east", &transform.ObjectsPosition{Type: transform.GravitySouthEast, X: 50}, 450},
		{"focus point", &transform.ObjectsPosition{Type: transform.GravityFocusPoint, X: 0.75, Y: 0.5}, 550},
		{"proportional", &transform.ObjectsPosition{Proportional: true}, 650},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := transform.Pipeline{
				Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(1000), Height: px(1000)},
				Gravity: transform.Gravity{Type: transform.GravityObject, Classes: []string{"dog"}, ObjectsPosition: tc.op},
			}
			got := rects(mustBuild(t, req(p), &meta))
			if len(got) != 1 || got[0] != [4]int{tc.x, 0, 1000, 1000} {
				t.Errorf("crop = %v, want x %d", got, tc.x)
			}
		})
	}

	// Vertically: a 1000×2000 source, the box spans y 400–600.
	vmeta := withDets(jpeg(1000, 2000), nil, objectAt("dog", 0.4, 0.2, 0.6, 0.3))
	for _, tc := range []struct {
		op *transform.ObjectsPosition
		y  int
	}{
		{&transform.ObjectsPosition{Type: transform.GravityNorth}, 400},
		{&transform.ObjectsPosition{Type: transform.GravityNorth, Y: 100}, 300},
		{&transform.ObjectsPosition{Type: transform.GravitySouth}, 0},
		{&transform.ObjectsPosition{Type: transform.GravityNorthWest, Y: 0.05}, 300},
	} {
		p := transform.Pipeline{
			Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(1000), Height: px(1000)},
			Gravity: transform.Gravity{Type: transform.GravityObject, ObjectsPosition: tc.op},
		}
		got := rects(mustBuild(t, req(p), &vmeta))
		if len(got) != 1 || got[0] != [4]int{0, tc.y, 1000, 1000} {
			t.Errorf("%+v: crop = %v, want y %d", *tc.op, got, tc.y)
		}
	}
}

// TestObjectsPositionDPR: pixel offsets follow the DPR like gravity
// offsets.
func TestObjectsPositionDPR(t *testing.T) {
	t.Parallel()
	meta := withDets(jpeg(4000, 1000), nil, objectAt("dog", 0.6, 0.4, 0.7, 0.6))
	p := transform.Pipeline{
		Resize: transform.Resize{Type: transform.ResizeFill, Width: px(500), Height: px(500), DPR: 2},
		Gravity: transform.Gravity{Type: transform.GravityObject, ObjectsPosition: &transform.ObjectsPosition{
			Type: transform.GravityWest, X: 50,
		}},
	}
	// Scaled to 4000×1000 (1000/1000), box x 2400–2800, west + 100 px.
	got := rects(mustBuild(t, req(p), &meta))
	if len(got) != 1 || got[0] != [4]int{2300, 0, 1000, 1000} {
		t.Errorf("crop = %v", got)
	}
}

func TestObjectWeighted(t *testing.T) {
	t.Parallel()
	meta := withDets(jpeg(2000, 1000), []plan.Detected{faceAt(0.1, 0.4, 0.15, 0.5)},
		objectAt("cat", 0.8, 0.4, 0.85, 0.5))
	mk := func(classes []string, weights []float64) transform.Pipeline {
		return transform.Pipeline{
			Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(500), Height: px(1000)},
			Gravity: transform.Gravity{Type: transform.GravityObjectWeighted, Classes: classes, Weights: weights},
		}
	}
	x := func(p transform.Pipeline) int {
		r := rects(mustBuild(t, req(p), &meta))
		if len(r) != 1 {
			t.Fatalf("crops %v", r)
		}
		return r[0][0]
	}
	// Equal areas: the weighted centre is 1/4 of the way from the face at
	// weight 3:1 (x 250 and 1650 → 600), 1:3 → 1300, 1:1 → 950.
	for _, tc := range []struct {
		w    []float64
		want int // the centre minus 250
	}{
		{[]float64{3, 1}, 350},
		{[]float64{1, 3}, 1050},
		{[]float64{1, 1}, 700},
		{[]float64{0, 1}, 1400},
	} {
		if got := x(mk([]string{"face", "cat"}, tc.w)); got != tc.want {
			t.Errorf("weights %v: x = %d, want %d", tc.w, got, tc.want)
		}
	}
	// No classes: every detection with weight 1.
	if got := x(mk(nil, nil)); got != 700 {
		t.Errorf("no classes: x = %d", got)
	}
	// Only weight-0 classes: nothing, the smart crop.
	p := req(mk([]string{"face"}, []float64{0}))
	if c := stepsOf[plan.CropStep](mustBuild(t, p, &meta).Steps); len(c) != 1 || c[0].Smart != plan.InterestingAttention {
		t.Errorf("zero weights: %+v", c)
	}
}

// TestDetectionFallback: without detections, or with none of the
// classes, detection gravities are the smart crop.
func TestDetectionFallback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		meta plan.SourceMeta
		g    transform.Gravity
	}{
		{"not detected", jpeg(1000, 500), transform.Gravity{Type: transform.GravityFace}},
		{"no faces", withDets(jpeg(1000, 500), nil, objectAt("dog", 0.1, 0.1, 0.2, 0.2)), transform.Gravity{Type: transform.GravityFace}},
		{"other class", withDets(jpeg(1000, 500), nil, objectAt("dog", 0.1, 0.1, 0.2, 0.2)), transform.Gravity{Type: transform.GravityObject, Classes: []string{"cat"}}},
		{"face index past the faces", withDets(jpeg(1000, 500), []plan.Detected{faceAt(0.1, 0.1, 0.2, 0.2)}), transform.Gravity{Type: transform.GravityFace, FaceIndex: new(1)}},
		{"smart without faces", withDets(jpeg(1000, 500), nil, objectAt("dog", 0.1, 0.1, 0.2, 0.2)), transform.Gravity{Type: transform.GravitySmart}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFill, Width: px(100), Height: px(100)}, Gravity: tc.g}
			c := stepsOf[plan.CropStep](mustBuild(t, req(p), &tc.meta).Steps)
			if len(c) != 1 || c[0].Smart != plan.InterestingAttention {
				t.Errorf("crop %+v, want the attention smart crop", c)
			}
		})
	}
}

func TestFaceIndex(t *testing.T) {
	t.Parallel()
	meta := withDets(jpeg(2000, 1000), []plan.Detected{faceAt(0.8, 0.4, 0.9, 0.5), faceAt(0.1, 0.4, 0.2, 0.5)})
	for _, tc := range []struct {
		i    *int
		want int
	}{
		{nil, 500}, // union 200–1800: centred
		{new(0), 1000},
		{new(1), 0},
	} {
		p := transform.Pipeline{
			Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(1000), Height: px(1000)},
			Gravity: transform.Gravity{Type: transform.GravityFace, FaceIndex: tc.i},
		}
		if r := rects(mustBuild(t, req(p), &meta)); len(r) != 1 || r[0][0] != tc.want {
			t.Errorf("index %v: crops %v, want x %d", tc.i, r, tc.want)
		}
	}
}

func TestCropObjects(t *testing.T) {
	t.Parallel()
	// PNG: no shrink-on-load, so the pre-crops are in source pixels.
	png := plan.SourceMeta{Width: 1000, Height: 1000, Format: "png"}
	dets := withDets(png, []plan.Detected{faceAt(0.2, 0.3, 0.4, 0.5)}, objectAt("dog", 0.6, 0.6, 0.8, 0.9))
	fill := func(w, h int) transform.Resize {
		return transform.Resize{Type: transform.ResizeFill, Width: px(w), Height: px(h)}
	}
	tests := []struct {
		name   string
		p      transform.Pipeline
		meta   plan.SourceMeta
		crops  [][4]int
		output [2]int
	}{
		{
			"face",
			transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1, Classes: []string{"face"}}},
			dets,
			[][4]int{{200, 300, 200, 200}},
			[2]int{200, 200},
		},
		{
			"scaled",
			transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1.5, Classes: []string{"face"}}},
			dets,
			[][4]int{{150, 250, 300, 300}},
			[2]int{300, 300},
		},
		{
			"padded",
			transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1, Classes: []string{"face"}, Padding: 10}},
			dets,
			[][4]int{{190, 290, 220, 220}},
			[2]int{220, 220},
		},
		{
			"every class",
			transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1}},
			dets,
			[][4]int{{200, 300, 600, 600}},
			[2]int{600, 600},
		},
		{
			"index",
			transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1, Index: new(1)}},
			dets,
			[][4]int{{600, 600, 200, 300}},
			[2]int{200, 300},
		},
		{
			"index past the end",
			transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1, Index: new(2)}},
			dets,
			nil,
			[2]int{1000, 1000},
		},
		{
			"grown to fill's aspect",
			transform.Pipeline{Resize: fill(400, 200), CropObjects: &transform.CropObjects{Scale: 1, Classes: []string{"face"}}},
			dets,
			[][4]int{{100, 300, 400, 200}},
			[2]int{400, 200},
		},
		{
			"kept inside the image",
			transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 3, Classes: []string{"dog"}}},
			dets,
			[][4]int{{400, 100, 600, 900}},
			[2]int{600, 900},
		},
		{"precedence over crop", transform.Pipeline{
			Crop:        &transform.Crop{Width: px(10), Height: px(10)},
			CropObjects: &transform.CropObjects{Scale: 1, Classes: []string{"face"}},
		}, dets, [][4]int{{200, 300, 200, 200}}, [2]int{200, 200}},
		{"nothing detected keeps crop", transform.Pipeline{
			Crop:        &transform.Crop{Width: px(10), Height: px(10)},
			CropObjects: &transform.CropObjects{Scale: 1, Classes: []string{"cat"}},
		}, dets, [][4]int{{496, 496, 10, 10}}, [2]int{10, 10}},
		{
			"face pad",
			transform.Pipeline{Resize: fill(100, 100), Gravity: transform.Gravity{Type: transform.GravityFace, FacePad: 2}},
			dets,
			[][4]int{{100, 200, 400, 400}},
			[2]int{100, 100},
		},
		{
			"face pad index",
			transform.Pipeline{Resize: fill(100, 100), Gravity: transform.Gravity{Type: transform.GravityFace, FacePad: 1, FaceIndex: new(0)}},
			dets,
			[][4]int{{200, 300, 200, 200}},
			[2]int{100, 100},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustBuild(t, req(tc.p), &tc.meta)
			if diff := cmp.Diff(tc.crops, rects(p)); diff != "" {
				t.Errorf("crops (-want +got):\n%s", diff)
			}
			if got := [2]int{p.Output.Width, p.Output.Height}; got != tc.output {
				t.Errorf("output %v, want %v", got, tc.output)
			}
		})
	}
	// With EXIF orientation 6 the boxes are in the displayed image: the
	// pre-crop in stored pixels is rotated back.
	meta := jpeg(1000, 500) // displayed 500×1000
	meta.Orientation = 6
	meta = withDets(meta, []plan.Detected{faceAt(0.2, 0.1, 0.4, 0.2)}) // displayed x 100–200, y 100–200
	got := rects(mustBuild(t, req(transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1}}), &meta))
	// Displayed (100,100)-(200,200) is stored (100,300)-(200,400).
	if diff := cmp.Diff([][4]int{{100, 300, 100, 100}}, got); diff != "" {
		t.Errorf("orientation 6 (-want +got):\n%s", diff)
	}
	if p := mustBuild(t, req(transform.Pipeline{CropObjects: &transform.CropObjects{Scale: 1}}), new(jpeg(10, 10))); len(p.Unplanned) != 1 || p.Unplanned[0] != "detections" {
		t.Errorf("without detections Unplanned = %v", p.Unplanned)
	}
}

func TestDetectionFilters(t *testing.T) {
	t.Parallel()
	meta := withDets(jpeg(1000, 500), []plan.Detected{faceAt(0.1, 0.2, 0.2, 0.4)}, objectAt("dog", 0.4, 0.5, 0.7, 0.9))
	p := transform.Pipeline{
		Crop:           &transform.Crop{Width: px(500), Height: px(500), Gravity: transform.Gravity{Type: transform.GravityWest}},
		BlurDetections: &transform.DetectionFilter{Classes: []string{"face"}},
		DrawDetections: &transform.DetectionFilter{},
	}
	pl := mustBuild(t, req(p), &meta)
	fs := stepsOf[plan.FilterStep](pl.Steps)
	if len(fs) != 1 || fs[0].Detections == nil {
		t.Fatalf("filter steps %+v", fs)
	}
	opt := cmpopts.EquateApprox(0, 1e-9)
	want := &plan.DetectionBlur{Sigma: plan.DefaultDetectionSigma, Areas: []transform.Area{area(0.2, 0.2, 0.4, 0.4)}}
	if diff := cmp.Diff(want, fs[0].Detections, opt); diff != "" {
		t.Errorf("blur (-want +got):\n%s", diff)
	}
	dd := stepsOf[plan.DrawDetectionsStep](pl.Steps)
	wantDraw := []plan.DrawDetectionsStep{{Boxes: []plan.DetectionBox{
		{Area: area(0.2, 0.2, 0.4, 0.4), Class: "face", Score: 0.9},
		{Area: area(0.8, 0.5, 1, 0.9), Class: "dog", Score: 0.8}, // clipped by the crop
	}}}
	if diff := cmp.Diff(wantDraw, dd, opt); diff != "" {
		t.Errorf("draw (-want +got):\n%s", diff)
	}
	// Draw comes after the colour adjustments, before the canvas grows.
	p.Padding = &transform.Padding{Top: 10}
	p.Grayscale = true
	steps := mustBuild(t, req(p), &meta).Steps
	var order []string
	for _, s := range steps {
		switch s.(type) {
		case plan.ColourAdjustStep:
			order = append(order, "colour")
		case plan.DrawDetectionsStep:
			order = append(order, "draw")
		case plan.PaddingStep:
			order = append(order, "padding")
		default:
		}
	}
	if diff := cmp.Diff([]string{"colour", "draw", "padding"}, order); diff != "" {
		t.Errorf("step order (-want +got):\n%s", diff)
	}

	// Pixelate, and nothing of the classes: no step.
	p = transform.Pipeline{BlurDetections: &transform.DetectionFilter{Pixelate: 8, Classes: []string{"face"}}}
	fs = stepsOf[plan.FilterStep](mustBuild(t, req(p), &meta).Steps)
	if len(fs) != 1 || fs[0].Detections == nil || fs[0].Detections.Pixelate != 8 || fs[0].Detections.Sigma != 0 {
		t.Errorf("pixelate %+v", fs)
	}
	p.BlurDetections.Classes = []string{"cat"}
	p.DrawDetections = &transform.DetectionFilter{Classes: []string{"cat"}}
	pl = mustBuild(t, req(p), &meta)
	if len(stepsOf[plan.FilterStep](pl.Steps)) != 0 || len(stepsOf[plan.DrawDetectionsStep](pl.Steps)) != 0 || len(pl.Unplanned) != 0 {
		t.Errorf("no cats: %+v %v", pl.Steps, pl.Unplanned)
	}
	// Chained pipelines have no detections.
	pl = mustBuild(t, req(transform.Pipeline{}, transform.Pipeline{BlurDetections: &transform.DetectionFilter{Sigma: 3}}), &meta)
	if len(pl.Chained) != 1 || len(pl.Chained[0].Unplanned) != 1 || pl.Chained[0].Unplanned[0] != "detections" {
		t.Errorf("chained Unplanned = %v", pl.Chained[0].Unplanned)
	}
}

func TestInfoCropDetections(t *testing.T) {
	t.Parallel()
	dets := &plan.Detections{Objects: []plan.Detected{objectAt("dog", 0.7, 0.1, 0.8, 0.2)}}
	c := &transform.Crop{Width: px(100), Height: px(100), Gravity: transform.Gravity{Type: transform.GravityObject}}
	x, y, w, h, approx := plan.InfoCrop(c, nil, 400, 400, dets)
	if x != 250 || y != 10 || w != 100 || h != 100 || approx {
		t.Errorf("centred: %d,%d %dx%d %v", x, y, w, h, approx)
	}
	x, y, _, _, _ = plan.InfoCrop(c, &transform.ObjectsPosition{Type: transform.GravityNorthWest}, 400, 400, dets)
	if x != 280 || y != 40 {
		t.Errorf("north-west: %d,%d", x, y)
	}
	if _, _, _, _, approx = plan.InfoCrop(c, nil, 400, 400, nil); !approx {
		t.Error("without detections the crop is approximated")
	}
}
