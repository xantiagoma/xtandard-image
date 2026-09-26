package plan_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var fuzzFormats = [...]string{"jpeg", "png", "webp", "gif", "svg", "heif", "avif", "pdf", "tiff", ""}

// FuzzBuild checks that Build never panics and that every plan it returns
// is geometrically sound: positive sizes, crops inside the image they cut,
// and an Output that matches the steps.
func FuzzBuild(f *testing.F) {
	f.Add(1000, 600, 1, 1, uint8(0), false, uint8(1), 300, 200, 0.0, uint8(1), 0.0, 0.0, 1.0, 1.0, 1.0, false, false, false, 0, 0, 0, 0, 0, uint8(0), 0)
	f.Add(4000, 3000, 1, 6, uint8(0), false, uint8(2), 300, 300, 0.0, uint8(7), 10.0, -5.0, 2.0, 1.0, 1.0, true, true, true, 10, 500, 90, 0, 400, uint8(3), 0)
	f.Add(200, 150, 5, 1, uint8(3), true, uint8(3), 120, 120, 0.5, uint8(10), 0.3, 0.7, 1.5, 0.5, 2.0, false, true, false, 5, 0, 270, 2, 0, uint8(1), 100)
	f.Add(300, 200, 1, 8, uint8(4), false, uint8(4), 0, 90, 0.25, uint8(12), 0.9, 0.1, 3.0, 2.0, 0.5, true, false, true, 0, 0, 180, 1, 0, uint8(2), 50)
	f.Add(1, 1, 1, 5, uint8(1), false, uint8(5), 1, 1, 1.0, uint8(8), -1e9, 1e9, 1e-9, 1e9, 1e300, true, true, true, 1<<20, 1<<30, 90, 3, 1, uint8(0), 1)
	f.Add(1000, 600, 1, 6, uint8(0), false, uint8(2), 200, 200, 400.0, uint8(10), 800.0, 150.0, 1.0, 1.0, 1.0, false, false, false, 7, 3, 90, 1, 0, uint8(2), 0)
	f.Fuzz(func(t *testing.T, w, h, frames, orient int, format uint8, animated bool, rt uint8, rw, rh int,
		crop float64, grav uint8, gx, gy, dpr, zoomX, zoomY float64, enlarge, extend, exar bool,
		pad, minW, rotate, flip, mrd int, trim uint8, budget int,
	) {
		meta := plan.SourceMeta{
			Width: w, Height: h, Frames: frames, Orientation: orient, IsAnimated: animated,
			Format: fuzzFormats[int(format)%len(fuzzFormats)], HasAlpha: format%2 == 1,
		}
		meta.IsVector = meta.Format == "svg" || meta.Format == "pdf"
		p := transform.Pipeline{
			Resize: transform.Resize{
				Type: transform.ResizeType(rt % 7), Width: px(rw), Height: px(rh), MinWidth: minW,
				ZoomX: zoomX, ZoomY: zoomY, DPR: dpr, Enlarge: enlarge,
			},
			Gravity:  transform.Gravity{Type: transform.GravityType(grav % 16), XOffset: gx, YOffset: gy, FocusX: math.Mod(math.Abs(gx), 1), FocusY: math.Mod(math.Abs(gy), 1)},
			Extend:   transform.Extend{Enabled: extend},
			ExtendAR: transform.Extend{Enabled: exar},
			Rotate:   rotate,
			Flip:     transform.Flip{H: flip&1 != 0, V: flip&2 != 0},
		}
		if crop > 0 && !math.IsInf(crop, 0) {
			d := transform.Dim{Rel: crop}
			if crop >= 1 {
				d = transform.Dim{Px: int(min(crop, 1e9))}
			}
			p.Crop = &transform.Crop{Width: d, Height: d}
		}
		if pad > 0 {
			p.Padding = &transform.Padding{Top: pad, Right: pad / 2, Bottom: pad / 3, Left: pad / 4}
		}
		if trim%3 == 1 {
			p.Trim = &transform.Trim{Threshold: 10}
		}
		if trim%3 == 2 {
			// A pixel focus anywhere, even outside the source.
			x, y := int(min(math.Abs(gx), 1e7)), int(min(math.Abs(gy), 1e7))
			if g, ok := transform.FocusFromPixels(x, y, x+pad%100, y+minW%100); ok {
				p.Gravity = g
				if p.Crop != nil {
					p.Crop.Gravity = g
				}
			}
		}
		if g := p.Gravity.Type; g >= transform.GravitySmart && g != transform.GravityFocusPoint {
			// Smart, entropy and the detection gravities: a face and an
			// object anywhere in the source, objects positions, crop_objects
			// and the detection filters (WP-72/73).
			fx, fy := math.Mod(math.Abs(gx), 1), math.Mod(math.Abs(gy), 1)
			meta.Detections = &plan.Detections{
				Faces:   []plan.Detected{{Class: "face", Score: 1, Box: transform.Area{Left: fx / 2, Top: fy / 2, Width: fx / 2, Height: fy / 2}}},
				Objects: []plan.Detected{{Class: "dog", Score: 0.5, Box: transform.Area{Left: fy, Top: fx, Width: 1 - fy, Height: 1 - fx}}},
			}
			p.Gravity.ObjectsPosition = &transform.ObjectsPosition{
				Type: [...]transform.GravityType{
					transform.GravityCenter, transform.GravityNorth, transform.GravitySouth, transform.GravityEast,
					transform.GravityWest, transform.GravityNorthEast, transform.GravityNorthWest,
					transform.GravitySouthEast, transform.GravitySouthWest,
				}[(budget%9+9)%9],
				X: gy, Y: gx, Proportional: budget%11 == 0,
			}
			if budget%3 == 0 {
				p.Gravity.ObjectsPosition.Type = transform.GravityFocusPoint
			}
			if p.Crop != nil && flip&1 != 0 {
				p.Crop.Gravity = p.Gravity
			}
			if trim%3 == 0 && pad%2 == 1 {
				p.CropObjects = &transform.CropObjects{Scale: zoomX, Padding: pad % 1000}
			}
			if trim%3 == 0 && pad%4 == 2 {
				p.Gravity.Type, p.Gravity.FacePad = transform.GravityFace, zoomY
			}
			p.BlurDetections = &transform.DetectionFilter{Pixelate: pad % 20}
			p.DrawDetections = &transform.DetectionFilter{}
		}
		r := req(p)
		r.Limits.MaxResultDimension = mrd
		lim := plan.Limits{MaxSrcPixels: 50_000_000, MaxAnimationFrames: 64, MemoryBudget: int64(budget) << 20}

		pl, err := plan.Build(r, meta, lim)
		if err != nil {
			if _, ok := errors.AsType[*errs.Error](err); !ok {
				t.Fatalf("error is not *errs.Error: %v", err)
			}
			return
		}
		if err := checkPlan(pl); err != nil {
			t.Fatalf("%v\nplan: %+v", err, pl)
		}
	})
}

// checkPlan walks the steps with the predicted sizes and checks the
// invariants.
func checkPlan(p *plan.Plan) error {
	if p.Load.Width <= 0 || p.Load.Height <= 0 {
		return fmt.Errorf("load size %dx%d", p.Load.Width, p.Load.Height)
	}
	w, h := p.Load.Width, p.Load.Height
	for i, s := range p.Steps {
		switch s := s.(type) {
		case plan.PreCropStep:
			if err := checkCrop(&s.CropGeometry, w, h); err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			w, h = s.W, s.H
		case plan.CropStep:
			if err := checkCrop(&s.CropGeometry, w, h); err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			w, h = s.W, s.H
		case plan.ResizeStep:
			if s.W <= 0 || s.H <= 0 || !(s.ScaleX > 0) || !(s.ScaleY > 0) {
				return fmt.Errorf("step %d: resize %+v", i, s)
			}
			w, h = s.W, s.H
		case plan.RotateStep:
			if s.Angle%90 != 0 || s.Angle <= 0 || s.Angle >= 360 {
				return fmt.Errorf("step %d: rotate %d", i, s.Angle)
			}
			if s.Angle%180 != 0 {
				w, h = h, w
			}
		case plan.ExtendStep:
			x, y, ew, eh, ok := s.Rect(w, h)
			if !ok || x < 0 || y < 0 || ew <= 0 || eh <= 0 || [4]int{x, y, ew, eh} != [4]int{s.X, s.Y, s.W, s.H} {
				return fmt.Errorf("step %d: extend %+v on %dx%d", i, s, w, h)
			}
			w, h = ew, eh
		case plan.PaddingStep:
			if s.Top < 0 || s.Right < 0 || s.Bottom < 0 || s.Left < 0 {
				return fmt.Errorf("step %d: padding %+v", i, s)
			}
			w, h = w+s.Left+s.Right, h+s.Top+s.Bottom
		default:
		}
	}
	if w != p.Output.Width || h != p.Output.Height || w <= 0 || h <= 0 {
		return fmt.Errorf("steps end at %dx%d, Output %dx%d", w, h, p.Output.Width, p.Output.Height)
	}
	if p.Cost.Pixels <= 0 || p.Cost.EstimatedBytes < p.Cost.Pixels {
		return fmt.Errorf("cost %+v", p.Cost)
	}
	for _, c := range p.Chained {
		if c.Output.Width <= 0 || c.Output.Height <= 0 {
			return fmt.Errorf("chained output %+v", c.Output)
		}
	}
	return nil
}

func checkCrop(c *plan.CropGeometry, w, h int) error {
	x, y, cw, ch, ok := c.Rect(w, h)
	if !ok || [4]int{x, y, cw, ch} != [4]int{c.X, c.Y, c.W, c.H} {
		return fmt.Errorf("crop %+v on %dx%d: Rect = %d,%d %dx%d %v", c, w, h, x, y, cw, ch, ok)
	}
	if x < 0 || y < 0 || cw <= 0 || ch <= 0 || x+cw > w || y+ch > h {
		return fmt.Errorf("crop %d,%d %dx%d outside %dx%d", x, y, cw, ch, w, h)
	}
	return nil
}

func BenchmarkBuild(b *testing.B) {
	cases := []struct {
		name string
		r    *transform.Request
		meta plan.SourceMeta
	}{
		{"fit_jpeg", req(transform.Pipeline{Resize: transform.Resize{Width: px(400), Height: px(300)}}), jpeg(4000, 3000)},
		{"fill_crop_rotate_pad", req(transform.Pipeline{
			Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(300), Height: px(300), DPR: 2},
			Crop:    &transform.Crop{Width: px(2000), Height: px(2000)},
			Gravity: transform.Gravity{Type: transform.GravityNorthEast, XOffset: 10}, Rotate: 90,
			Padding: &transform.Padding{Top: 10, Right: 10, Bottom: 10, Left: 10},
		}), plan.SourceMeta{Width: 4000, Height: 3000, Orientation: 6, Format: "jpeg"}},
		{"animated_gif", req(transform.Pipeline{Resize: transform.Resize{Width: px(100)}}), gifMeta(50)},
	}
	for i := range cases {
		c := &cases[i]
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := plan.Build(c.r, c.meta, plan.Limits{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
