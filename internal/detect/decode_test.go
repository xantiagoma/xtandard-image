package detect

import (
	"encoding/json"
	"image"
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestIoU(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		a, b box
		want float32
	}{
		{"same", box{0, 0, 10, 10}, box{0, 0, 10, 10}, 1},
		{"half", box{0, 0, 10, 10}, box{5, 0, 15, 10}, 50.0 / 150},
		{"disjoint", box{0, 0, 10, 10}, box{10, 10, 20, 20}, 0},
		{"contained", box{0, 0, 10, 10}, box{2, 2, 7, 7}, 25.0 / 100},
		{"empty", box{0, 0, 0, 0}, box{0, 0, 0, 0}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := iou(tc.a, tc.b); math.Abs(float64(got-tc.want)) > 1e-6 {
				t.Errorf("iou = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNMS(t *testing.T) {
	t.Parallel()
	c := func(x1, score float32, class int) candidate {
		return candidate{box: box{x1, 0, x1 + 10, 10}, score: score, class: class}
	}
	nan := float32(math.NaN())
	for _, tc := range []struct {
		name       string
		in         []candidate
		classAware bool
		limit      int
		want       []float32 // kept scores
	}{
		{"overlap suppressed", []candidate{c(0, 0.5, 0), c(1, 0.9, 0), c(30, 0.7, 0)}, false, 10, []float32{0.9, 0.7}},
		{"class aware keeps other classes", []candidate{c(0, 0.5, 1), c(1, 0.9, 0)}, true, 10, []float32{0.9, 0.5}},
		{"agnostic across classes", []candidate{c(0, 0.5, 1), c(1, 0.9, 0)}, false, 10, []float32{0.9}},
		{"limit", []candidate{c(0, 0.5, 0), c(30, 0.9, 0), c(60, 0.7, 0)}, false, 2, []float32{0.9, 0.7}},
		{"non-finite dropped", []candidate{c(0, nan, 0), {box: box{0, 0, float32(math.Inf(1)), 1}, score: 1}, c(40, 0.2, 0)}, false, 10, []float32{0.2}},
		{"empty box dropped", []candidate{{box: box{5, 5, 5, 9}, score: 1}}, false, 10, []float32{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := make([]float32, 0, len(tc.in))
			for _, k := range nms(tc.in, 0.3, tc.classAware, tc.limit) {
				got = append(got, k.score)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("kept scores (-want +got):\n%s", diff)
			}
		})
	}
}

// yunetTensors builds zeroed YuNet outputs for a w×h input.
func yunetTensors(w, h int) map[string][]float32 {
	out := map[string][]float32{}
	for stride := minStride; stride <= maxStride; stride *= 2 {
		n := (w / stride) * (h / stride)
		cls, obj, bbox, kps := yunetOutputs(stride)
		out[cls], out[obj], out[bbox], out[kps] = make([]float32, n), make([]float32, n), make([]float32, 4*n), make([]float32, 10*n)
	}
	return out
}

func TestDecodeYuNet(t *testing.T) {
	t.Parallel()
	const w, h = 64, 32
	out := yunetTensors(w, h)
	// One face at stride 16, cell (col 2, row 1) of a 4×2 grid (index 6):
	// centre (2+0.5, 1+0.5)·16 = (40, 24), size e^0·16 = 16.
	out["cls_16"][6], out["obj_16"][6] = 0.81, 1
	copy(out["bbox_16"][24:], []float32{0.5, 0.5, 0, 0})
	for k := range 5 {
		out["kps_16"][60+2*k], out["kps_16"][60+2*k+1] = float32(k)/10, 0.25
	}
	// Below the threshold: sqrt(0.5·0.5) = 0.5.
	out["cls_8"][0], out["obj_8"][0] = 0.5, 0.5
	got, err := decodeYuNet(out, w, h, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d candidates, want 1: %+v", len(got), got)
	}
	want := candidate{
		box:       box{32, 16, 48, 32},
		score:     0.9,
		landmarks: [10]float32{32, 20, 33.6, 20, 35.2, 20, 36.8, 20, 38.4, 20},
	}
	if diff := cmp.Diff(want, got[0], cmp.AllowUnexported(candidate{}, box{}), cmp.Comparer(approx)); diff != "" {
		t.Errorf("candidate (-want +got):\n%s", diff)
	}

	if _, err := decodeYuNet(out, 60, 32, 0.6); err == nil {
		t.Error("accepted an input that is not a multiple of 32")
	}
	delete(out, "kps_32")
	if _, err := decodeYuNet(out, w, h, 0.6); err == nil {
		t.Error("accepted a missing output")
	}
}

func approx(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

func TestDecodeYOLOX(t *testing.T) {
	t.Parallel()
	const w, h, classes = 64, 32, 3
	anchors := 8*4 + 4*2 + 2*1
	out := make([]float32, anchors*(5+classes))
	// Anchor 33 is stride 16, cell 1 (col 1, row 0): centre (1+0.5, 0+0.5)·16
	// = (24, 8), size e^ln2·16 = 32 wide, 16 high.
	p := out[33*(5+classes):]
	copy(p, []float32{0.5, 0.5, float32(math.Ln2), 0, 0.9, 0.1, 0.8, 0.5})
	got, err := decodeYOLOX(out, w, h, classes, 0.3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d candidates, want 2 (classes 1 and 2): %+v", len(got), got)
	}
	opts := []cmp.Option{cmp.AllowUnexported(candidate{}, box{}), cmp.Comparer(approx)}
	want := []candidate{
		{box: box{8, 0, 40, 16}, score: 0.72, class: 1},
		{box: box{8, 0, 40, 16}, score: 0.45, class: 2},
	}
	if diff := cmp.Diff(want, got, opts...); diff != "" {
		t.Errorf("candidates (-want +got):\n%s", diff)
	}
	got, err = decodeYOLOX(out, w, h, classes, 0.3, []bool{false, false, true})
	if err != nil || len(got) != 1 || got[0].class != 2 {
		t.Errorf("class filter: %+v, %v", got, err)
	}
	if _, err := decodeYOLOX(out[1:], w, h, classes, 0.3, nil); err == nil {
		t.Error("accepted a short output")
	}
}

func TestFitInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		srcW, srcH, w, h, max int
		want                  geometry
	}{
		{"fixed, landscape down", 960, 754, 640, 640, 640, geometry{640, 640, 640, 502}},
		{"fixed, portrait up", 100, 200, 640, 640, 640, geometry{640, 640, 320, 640}},
		{"fixed, tiny sliver", 5000, 1, 640, 640, 640, geometry{640, 640, 640, 1}},
		{"dynamic, down", 1920, 1080, 0, 0, 640, geometry{640, 384, 640, 360}},
		{"dynamic, never up", 100, 50, 0, 0, 640, geometry{128, 64, 100, 50}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := fitInput(tc.srcW, tc.srcH, tc.w, tc.h, tc.max)
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(geometry{})); diff != "" {
				t.Errorf("geometry (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPackBGR(t *testing.T) {
	t.Parallel()
	// A 2×1 RGB image in rows of 3 pixels (stride 3), packed into 3×2.
	pix := []byte{1, 2, 3, 4, 5, 6, 99, 99, 99}
	dst := make([]float32, 3*3*2)
	if err := packBGR(dst, pix, 3, 2, 1, 3, 2, 114); err != nil {
		t.Fatal(err)
	}
	want := []float32{
		3, 6, 114, 114, 114, 114, // B
		2, 5, 114, 114, 114, 114, // G
		1, 4, 114, 114, 114, 114, // R
	}
	if diff := cmp.Diff(want, dst); diff != "" {
		t.Errorf("tensor (-want +got):\n%s", diff)
	}
	for _, bad := range []struct{ w, h, tw, th, n int }{
		{4, 1, 3, 2, 18}, // wider than the tensor
		{2, 1, 3, 2, 17}, // tensor size
		{2, 3, 3, 2, 18}, // taller than the tensor
	} {
		if err := packBGR(make([]float32, bad.n), pix, 3, bad.w, bad.h, bad.tw, bad.th, 0); err == nil {
			t.Errorf("packBGR accepted %+v", bad)
		}
	}
	if err := packBGR(dst, pix[:4], 3, 2, 1, 3, 2, 0); err == nil {
		t.Error("packBGR accepted short pixels")
	}
}

func TestParseChecksums(t *testing.T) {
	t.Parallel()
	const digest = "8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4"
	listing := "garbage\n" + strings.ToUpper(digest) + "  face.onnx\n" + digest + " *bin.onnx\nzz  bad.onnx\n"
	for name, want := range map[string]string{"face.onnx": digest, "bin.onnx": digest, "bad.onnx": "", "none.onnx": ""} {
		got, err := parseChecksums(strings.NewReader(listing), name)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestInfoJSON(t *testing.T) {
	t.Parallel()
	dets := []Detection{
		{Box: image.Rect(10, 20, 60, 120), Score: 0.9876543, Class: FaceClass, Landmarks: []image.Point{{20, 40}, {40, 40}, {30, 60}, {22, 80}, {38, 80}}},
		{Box: image.Rect(0, 0, 200, 100), Score: 0.5, Class: "dog"},
	}
	b, err := json.Marshal(NewInfo(dets, 200, 200))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"objects":[` +
		`{"class_id":0,"class_name":"face","confidence":0.987654,"left":0.05,"top":0.1,"width":0.25,"height":0.5,` +
		`"landmarks":[[0.1,0.2],[0.2,0.2],[0.15,0.3],[0.11,0.4],[0.19,0.4]]},` +
		`{"class_id":16,"class_name":"dog","confidence":0.5,"left":0,"top":0,"width":1,"height":0.5}]}`
	if string(b) != want {
		t.Errorf("JSON\n got %s\nwant %s", b, want)
	}
	if b, _ := json.Marshal(NewInfo(nil, 0, 0)); string(b) != `{"objects":[]}` {
		t.Errorf("empty: %s", b)
	}
}

func TestClasses(t *testing.T) {
	t.Parallel()
	c := ObjectClasses()
	if len(c) != 80 || c[0] != "person" || c[79] != "toothbrush" || ClassID("dog") != 16 || ClassID("face") != 0 || ClassID("x") != -1 {
		t.Errorf("classes: %d %q %q %d", len(c), c[0], c[79], ClassID("dog"))
	}
	c[0] = "mutated"
	if ObjectClasses()[0] != "person" {
		t.Error("ObjectClasses shares its backing array")
	}
}

func TestInputToSource(t *testing.T) {
	t.Parallel()
	in := &input{srcW: 960, srcH: 754, resW: 640, resH: 502}
	got := in.toSource([]candidate{
		{box: box{100, 100, 200, 200}, score: 0.9, landmarks: [10]float32{150, 150}},
		{box: box{-50, -50, 700, 700}, score: 0.8, class: 16},
		{box: box{700, 600, 800, 640}, score: 0.7},
	}, false)
	want := []Detection{
		{Box: image.Rect(150, 150, 300, 300), Score: 0.9, Class: "person"},
		{Box: image.Rect(0, 0, 960, 754), Score: 0.8, Class: "dog"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("detections (-want +got):\n%s", diff)
	}
	faces := in.toSource([]candidate{{box: box{0, 0, 10, 10}, score: 1, landmarks: [10]float32{-5, 5, 9999, 5}}}, true)
	if faces[0].Class != FaceClass || faces[0].Landmarks[0] != image.Pt(0, 8) || faces[0].Landmarks[1] != image.Pt(959, 8) {
		t.Errorf("face: %+v", faces[0])
	}
}
