//go:build cgo

package exec_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The geometry extras (WP-60) have no open-source imgproxy counterpart:
// crop_aspect_ratio is imgproxy Pro, and arbitrary rotation, border,
// radius and shape masks are not imgproxy options at all (they come from
// the imgix, ImageKit, Cloudinary and wsrv dialects). None of these cases
// runs in TestAgainstImgproxy.

var (
	blue    = transform.Color{B: 255, A: 255}
	red     = transform.Color{R: 255, A: 255}
	white   = transform.Color{R: 255, G: 255, B: 255, A: 255}
	shadow  = transform.Color{A: 128}
	orange  = transform.Color{R: 200, G: 100, B: 50, A: 255}
	orange5 = transform.Color{R: 200, G: 100, B: 50, A: 128}
)

func radiusAll(n int) *transform.Radius {
	return &transform.Radius{TopLeft: n, TopRight: n, BottomRight: n, BottomLeft: n}
}

func borderAll(n int, c transform.Color) *transform.Border {
	return &transform.Border{Top: n, Right: n, Bottom: n, Left: n, Color: c}
}

// extrasCases are the golden cases of every option on a photo, a PNG with
// graded alpha and an animated GIF (every frame processed, frame count
// kept).
var extrasCases = []struct {
	goldenCase
	set pipelineFunc
}{
	{name: "extras-car", fixture: jpegPhoto, options: "car:1/rs:fit:120:120", out: transform.FormatPNG, w: 120, h: 120},
	{name: "extras-car-enlarge", fixture: jpegPhoto, options: "c:100:50/car:1:1/rs:fit:100:100", out: transform.FormatPNG, w: 100, h: 100},
	{
		name: "extras-rotate-45", fixture: jpegPhoto, options: "rs:fit:100:100", out: transform.FormatPNG, w: 124, h: 124,
		set: func(p *transform.Pipeline) { p.RotateAny = 45 },
	},
	{
		name: "extras-rotate-bg", fixture: jpegPhoto, options: "rs:fit:100:100/bg:255:0:0", out: transform.FormatJPEG, w: 124, h: 115,
		set: func(p *transform.Pipeline) { p.RotateAny = -30 },
	},
	{
		name: "extras-border", fixture: jpegPhoto, options: "rs:fit:100:100", out: transform.FormatPNG, w: 112, h: 83,
		set: func(p *transform.Pipeline) {
			p.Border = &transform.Border{Top: 4, Right: 6, Bottom: 4, Left: 6, Color: blue}
		},
	},
	{
		name: "extras-radius", fixture: jpegPhoto, options: "rs:fit:100:100", out: transform.FormatPNG, w: 100, h: 75,
		set: func(p *transform.Pipeline) { p.Radius = radiusAll(20) },
	},
	{
		name: "extras-radius-max-jpeg", fixture: jpegPhoto, options: "rs:fit:100:100", out: transform.FormatJPEG, w: 100, h: 75,
		set: func(p *transform.Pipeline) { p.Radius = &transform.Radius{Max: true} },
	},
	{
		name: "extras-border-radius", fixture: jpegPhoto, options: "rs:fit:100:100", out: transform.FormatPNG, w: 108, h: 83,
		set: func(p *transform.Pipeline) {
			p.Border = borderAll(4, orange)
			p.Radius = &transform.Radius{TopLeft: 24, BottomRight: 24}
		},
	},
	{
		name: "extras-mask-star", fixture: jpegPhoto, options: "rs:fit:100:100", out: transform.FormatPNG, w: 78, h: 75,
		set: func(p *transform.Pipeline) { p.Mask = &transform.Mask{Shape: transform.MaskStar, Trim: true} },
	},
	{
		name: "extras-mask-ellipse-bg", fixture: jpegPhoto, options: "rs:fit:100:100", out: transform.FormatJPEG, w: 100, h: 75,
		set: func(p *transform.Pipeline) {
			p.Mask = &transform.Mask{Shape: transform.MaskEllipse, Background: &white}
		},
	},
	{
		name: "extras-png-rotate", fixture: pngAlpha, out: transform.FormatPNG, w: 153, h: 134,
		set: func(p *transform.Pipeline) { p.RotateAny = 20 },
	},
	{
		name: "extras-png-border", fixture: pngAlpha, out: transform.FormatPNG, w: 138, h: 106,
		set: func(p *transform.Pipeline) { p.Border = borderAll(5, shadow) },
	},
	{
		name: "extras-png-radius", fixture: pngAlpha, out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Radius = radiusAll(16) },
	},
	{
		name: "extras-png-mask-heart", fixture: pngAlpha, out: transform.FormatPNG, w: 128, h: 96,
		set: func(p *transform.Pipeline) { p.Mask = &transform.Mask{Shape: transform.MaskHeart} },
	},
	{name: "extras-png-car", fixture: pngAlpha, options: "car:2", out: transform.FormatPNG, w: 128, h: 64},
	{
		name: "extras-gif-rotate", fixture: gifAnim, out: transform.FormatGIF, w: 119, h: 119, frames: 6,
		set: func(p *transform.Pipeline) { p.RotateAny = 45 },
	},
	{
		name: "extras-gif-border", fixture: gifAnim, out: transform.FormatGIF, w: 102, h: 78, frames: 6,
		set: func(p *transform.Pipeline) { p.Border = borderAll(3, red) },
	},
	{
		name: "extras-gif-radius", fixture: gifAnim, out: transform.FormatGIF, w: 96, h: 72, frames: 6,
		set: func(p *transform.Pipeline) { p.Radius = radiusAll(12) },
	},
	{
		name: "extras-gif-mask", fixture: gifAnim, out: transform.FormatGIF, w: 72, h: 72, frames: 6,
		set: func(p *transform.Pipeline) { p.Mask = &transform.Mask{Shape: transform.MaskCircle, Trim: true} },
	},
	{name: "extras-gif-car", fixture: gifAnim, options: "car:1", out: transform.FormatGIF, w: 72, h: 72, frames: 6},
	{
		name: "extras-webp-all", fixture: webpAnim, options: "rs:fit:64:64", out: transform.FormatWebP, w: 78, h: 67, frames: 6,
		set: func(p *transform.Pipeline) {
			p.RotateAny = 15
			p.Mask = &transform.Mask{Shape: transform.MaskHexagon}
			p.Border = borderAll(2, orange5)
			p.Radius = radiusAll(8)
		},
	},
}

func TestGoldenGeometryExtras(t *testing.T) {
	t.Parallel()
	for _, tc := range extrasCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processReq(t, readFixture(t, tc.fixture), tc.options, tc.set, tc.out)
			checkOutput(t, &tc.goldenCase, &o)
			if len(o.res.Skipped) != 0 {
				t.Errorf("Skipped = %q", o.res.Skipped)
			}
			golden.Compare(t, "exec/"+tc.name, o.img, golden.Options{Tolerance: tc.tolerance})
		})
	}
}

func nrgbaAt(img image.Image, x, y int) color.NRGBA {
	n, ok := img.(*image.NRGBA)
	if !ok {
		return color.NRGBA{}
	}
	return n.NRGBAAt(x, y)
}

// TestBorderPixels checks every border pixel exactly, per side, for an
// opaque and a translucent colour, and that the image inside is untouched.
func TestBorderPixels(t *testing.T) {
	t.Parallel()
	src := solidPNG(t, 20, 10, rgb(10, 20, 30))
	for _, tc := range []struct {
		name   string
		colour transform.Color
		want   color.NRGBA
	}{
		{name: "opaque", colour: orange, want: rgb(200, 100, 50)},
		{name: "translucent", colour: orange5, want: rgba(200, 100, 50, 128)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := processReq(t, src, "", func(p *transform.Pipeline) {
				p.Border = &transform.Border{Top: 2, Right: 3, Bottom: 4, Left: 5, Color: tc.colour}
			}, transform.FormatPNG)
			if o.res.Width != 28 || o.res.Height != 16 {
				t.Fatalf("size = %dx%d, want 28x16", o.res.Width, o.res.Height)
			}
			for y := range 16 {
				for x := range 28 {
					want := tc.want
					if x >= 5 && x < 25 && y >= 2 && y < 12 {
						want = rgb(10, 20, 30)
					}
					if got := nrgbaAt(o.img, x, y); got != want {
						t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
					}
				}
			}
		})
	}
}

// TestRadiusAntialiasing checks rounded corners pixel by pixel: transparent
// outside the arc, the source colour at full alpha inside, and
// intermediate alpha values (with the colour unchanged) on the arc.
func TestRadiusAntialiasing(t *testing.T) {
	t.Parallel()
	in := rgb(10, 200, 30)
	o := processReq(t, solidPNG(t, 64, 64, in), "", func(p *transform.Pipeline) { p.Radius = radiusAll(20) }, transform.FormatPNG)
	at := func(x, y int) color.NRGBA { return nrgbaAt(o.img, x, y) }
	for _, p := range [][2]int{{0, 0}, {63, 0}, {0, 63}, {63, 63}, {2, 2}} {
		if a := at(p[0], p[1]).A; a != 0 {
			t.Errorf("alpha at %v = %d, want 0", p, a)
		}
	}
	for _, p := range [][2]int{{32, 32}, {32, 0}, {0, 32}, {63, 32}, {32, 63}, {20, 20}} {
		if got := at(p[0], p[1]); got != in {
			t.Errorf("pixel at %v = %v, want %v", p, got, in)
		}
	}
	partial := 0
	for y := range 20 {
		for x := range 20 {
			c := at(x, y)
			if c.A == 0 || c.A == 255 {
				continue
			}
			partial++
			if d := max(diff8(c.R, in.R), diff8(c.G, in.G), diff8(c.B, in.B)); d > 2 {
				t.Errorf("edge pixel (%d,%d) = %v: colour changed", x, y, c)
			}
		}
	}
	// A 20 px quarter circle crosses about 2 × 20 pixels.
	if partial < 20 {
		t.Errorf("%d anti-aliased pixels on the corner, want ≥ 20", partial)
	}
	// Alpha grows monotonically along the diagonal through the arc.
	var prev uint8
	for i := range 12 {
		a := at(i, i).A
		if a < prev {
			t.Errorf("alpha along the diagonal falls at %d: %d after %d", i, a, prev)
		}
		prev = a
	}
	if a := at(5, 5).A; a == 0 || a == 255 {
		t.Errorf("alpha at (5,5) = %d, want an intermediate value (arc at 5.86)", a)
	}

	// JPEG: the corners are flattened onto white (within JPEG's chroma
	// error next to saturated green).
	j := processReq(t, solidPNG(t, 64, 64, in), "", func(p *transform.Pipeline) { p.Radius = radiusAll(20) }, transform.FormatJPEG)
	if c := nrgbaAt(j.img, 0, 0); c.R < 230 || c.G < 230 || c.B < 230 {
		t.Errorf("JPEG corner = %v, want white", c)
	}
}

func diff8(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestRotateCanvas checks the canvas of a 45° rotation (libvips
// similarity grows it to the rotated bounding box) and its fill.
func TestRotateCanvas(t *testing.T) {
	t.Parallel()
	in := rgb(10, 200, 30)
	src := solidPNG(t, 100, 50, in)
	o := processReq(t, src, "", func(p *transform.Pipeline) { p.RotateAny = 45 }, transform.FormatPNG)
	if o.res.Width != 106 || o.res.Height != 106 {
		t.Fatalf("size = %dx%d, want 106x106", o.res.Width, o.res.Height)
	}
	if a := nrgbaAt(o.img, 0, 0).A; a != 0 {
		t.Errorf("corner alpha = %d, want 0", a)
	}
	if c := nrgbaAt(o.img, 53, 53); c != in {
		t.Errorf("centre = %v, want %v", c, in)
	}
	// With a background the corners take its exact colour.
	o = processReq(t, src, "bg:255:0:0", func(p *transform.Pipeline) { p.RotateAny = 45 }, transform.FormatPNG)
	if c := nrgbaAt(o.img, 0, 0); c != rgb(255, 0, 0) {
		t.Errorf("corner with background = %v, want red", c)
	}
	if c := nrgbaAt(o.img, 53, 53); c != in {
		t.Errorf("centre with background = %v, want %v", c, in)
	}
}

// TestShapeMask applies masks to a generated fixture (a 64×48 PNG split
// into two colours) and checks alpha and background pixel by pixel.
func TestShapeMask(t *testing.T) {
	t.Parallel()
	left, right := rgb(250, 10, 10), rgb(10, 10, 250)
	fx := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			c := left
			if x >= 32 {
				c = right
			}
			fx.SetNRGBA(x, y, c)
		}
	}
	src := encodePNG(t, fx)

	// Circle, trimmed: 48×48, transparent corners, opaque centre,
	// anti-aliased rim.
	o := processReq(t, src, "", func(p *transform.Pipeline) {
		p.Mask = &transform.Mask{Shape: transform.MaskCircle, Trim: true}
	}, transform.FormatPNG)
	if o.res.Width != 48 || o.res.Height != 48 {
		t.Fatalf("circle size = %dx%d, want 48x48", o.res.Width, o.res.Height)
	}
	if a := nrgbaAt(o.img, 0, 0).A; a != 0 {
		t.Errorf("circle corner alpha = %d, want 0", a)
	}
	// Trimmed by 8 px: x 8..55 of the source; column 23 is still left.
	if c := nrgbaAt(o.img, 23, 24); c != left {
		t.Errorf("circle centre-left = %v, want %v", c, left)
	}
	if c := nrgbaAt(o.img, 24, 24); c != right {
		t.Errorf("circle centre-right = %v, want %v", c, right)
	}
	partial := 0
	for x := range 48 {
		if a := nrgbaAt(o.img, x, 0).A; a > 0 && a < 255 {
			partial++
		}
	}
	if partial == 0 {
		t.Error("no anti-aliased pixels on the circle's top edge")
	}

	// Ellipse over an opaque background on JPEG: corners are exactly the
	// background, the centre the source.
	o = processReq(t, src, "", func(p *transform.Pipeline) {
		p.Mask = &transform.Mask{Shape: transform.MaskEllipse, Background: &blue}
	}, transform.FormatPNG)
	if o.res.Width != 64 || o.res.Height != 48 {
		t.Fatalf("ellipse size = %dx%d", o.res.Width, o.res.Height)
	}
	if c := nrgbaAt(o.img, 0, 0); c != rgb(0, 0, 255) {
		t.Errorf("ellipse corner = %v, want the background", c)
	}
	if c := nrgbaAt(o.img, 10, 24); c != left {
		t.Errorf("ellipse inside = %v, want %v", c, left)
	}

	// A translucent source keeps its own alpha inside the shape.
	half := solidPNG(t, 40, 40, rgba(0, 0, 0, 100))
	o = processReq(t, half, "", func(p *transform.Pipeline) {
		p.Mask = &transform.Mask{Shape: transform.MaskSquare}
	}, transform.FormatPNG)
	if c := nrgbaAt(o.img, 20, 20); diff8(c.A, 100) > 1 {
		t.Errorf("square centre alpha = %d, want 100", c.A)
	}
	if a := nrgbaAt(o.img, 1, 1).A; a != 0 {
		t.Errorf("square corner alpha = %d, want 0", a)
	}
}

// TestGeometryExtrasNoLeak runs the extras on a still and an animation
// with libvips leak checking on (tolerances as TestChainedNoLeak). Not
// parallel: the counters are global.
func TestGeometryExtrasNoLeak(t *testing.T) {
	const (
		cycles      = 120
		slackBytes  = 1 << 20
		slackAllocs = 64
		slackObjs   = 16
	)
	vips.SetLeakCheck(true)
	defer vips.SetLeakCheck(false)
	e := newExecutor(t)
	all := func(p *transform.Pipeline) {
		p.RotateAny = 30
		p.Mask = &transform.Mask{Shape: transform.MaskStar, Trim: true, Background: &shadow}
		p.Border = borderAll(3, orange5)
		p.Radius = radiusAll(6)
	}
	jobs := []struct {
		data    []byte
		options string
		out     transform.Format
	}{
		{readFixture(t, jpegPhoto), "car:1:1/rs:fit:80:80", transform.FormatJPEG},
		{readFixture(t, pngAlpha), "rs:fit:60:60", transform.FormatPNG},
		{readFixture(t, gifAnim), "rs:fit:40:40", transform.FormatGIF},
	}
	cycle := func(i int) {
		jb := jobs[i%len(jobs)]
		req := parse(t, jb.options)
		all(&req.Pipelines[0])
		req.ApplyDefaults(transform.StandardDefaults())
		j, buf := job(t, jb.data, req, jb.out)
		defer buf.Release()
		res, err := e.Run(t.Context(), j)
		if err != nil {
			t.Fatalf("run %q: %v", jb.options, err)
		}
		res.Body.Release()
	}
	for i := range 2 * len(jobs) {
		cycle(i)
	}
	before, objects := vips.ReadMemStats(), vips.LiveObjects()
	for i := range cycles {
		cycle(i)
	}
	after, objectsAfter := vips.ReadMemStats(), vips.LiveObjects()
	if after.Mem-before.Mem > slackBytes || after.Allocs-before.Allocs > slackAllocs ||
		after.Files != before.Files || objectsAfter-objects > slackObjs {
		t.Errorf("after %d runs: mem %d → %d B, allocs %d → %d, files %d → %d, objects %d → %d",
			cycles, before.Mem, after.Mem, before.Allocs, after.Allocs, before.Files, after.Files, objects, objectsAfter)
	}
}
