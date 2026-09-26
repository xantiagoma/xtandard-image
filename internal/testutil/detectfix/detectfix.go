// Package detectfix loads the reference detections of the detection
// fixtures (testdata/detect/expected.json, WP-71) as planner detections,
// so tests of face and object gravity and of the detection filters
// (WP-72/73) run deterministically without ONNX Runtime.
package detectfix

import (
	"cmp"
	"encoding/json"
	"image"
	_ "image/jpeg" // the fixtures are JPEG
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Fixtures of testdata/fixtures/detect.
const (
	Crew = "detect/apollo11-crew.jpg"  // three faces, three persons
	Dogs = "detect/astronaut-dogs.jpg" // a face, a person, a dog, a horse
	Cat  = "detect/cat-laptop.jpg"     // a cat, a laptop, a bed; no face
)

type expected struct {
	Faces []struct {
		Box       [4]float64   `json:"box"`
		Score     float64      `json:"score"`
		Landmarks [][2]float64 `json:"landmarks"`
	} `json:"faces"`
	Objects []struct {
		Class string     `json:"class"`
		Box   [4]float64 `json:"box"`
		Score float64    `json:"score"`
	} `json:"objects"`
}

// Size is the pixel size of a fixture (they carry no EXIF orientation).
func Size(tb testing.TB, fixture string) (w, h int) {
	tb.Helper()
	f, err := os.Open(golden.Fixture(tb, fixture))
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		tb.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

// Load returns the reference detections of a fixture (for example Crew)
// as fractions of the image, faces and objects best first, clipped to the
// image like internal/detect clips them.
func Load(tb testing.TB, fixture string) *plan.Detections {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join(golden.ModuleRoot(tb), "testdata", "detect", "expected.json"))
	if err != nil {
		tb.Fatal(err)
	}
	var all map[string]expected
	if err := json.Unmarshal(b, &all); err != nil {
		tb.Fatal(err)
	}
	e, ok := all[filepath.Base(fixture)]
	if !ok {
		tb.Fatalf("detectfix: no expectations for %s", fixture)
	}
	w, h := Size(tb, fixture)
	area := func(box [4]float64) transform.Area {
		x0, y0 := min(max(box[0], 0), float64(w)), min(max(box[1], 0), float64(h))
		x1, y1 := min(max(box[2], 0), float64(w)), min(max(box[3], 0), float64(h))
		return transform.Area{Left: x0 / float64(w), Top: y0 / float64(h), Width: (x1 - x0) / float64(w), Height: (y1 - y0) / float64(h)}
	}
	d := &plan.Detections{}
	for _, f := range e.Faces {
		p := plan.Detected{Class: transform.FaceClass, Score: f.Score, Box: area(f.Box)}
		for _, l := range f.Landmarks {
			p.Landmarks = append(p.Landmarks, [2]float64{l[0] / float64(w), l[1] / float64(h)})
		}
		d.Faces = append(d.Faces, p)
	}
	for _, o := range e.Objects {
		d.Objects = append(d.Objects, plan.Detected{Class: o.Class, Score: o.Score, Box: area(o.Box)})
	}
	sortBest(d.Faces)
	sortBest(d.Objects)
	return d
}

func sortBest(ds []plan.Detected) {
	slices.SortStableFunc(ds, func(a, b plan.Detected) int { return cmp.Compare(b.Score, a.Score) })
}
