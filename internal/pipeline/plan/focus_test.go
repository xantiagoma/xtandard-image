package plan_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// pixelGravity is a focus at the source pixel (x, y).
func pixelGravity(x, y int) *transform.Gravity {
	g, _ := transform.FocusFromPixels(x, y, x, y)
	return &g
}

func fracGravity(fx, fy float64) *transform.Gravity {
	return &transform.Gravity{Type: transform.GravityFocusPoint, FocusX: fx, FocusY: fy}
}

// buildBoth plans the pipeline made by mk with a pixel and with a
// fractional gravity and fails unless the plans are identical.
func buildBoth(t *testing.T, meta *plan.SourceMeta, pixel, frac *transform.Gravity, mk func(g transform.Gravity) transform.Pipeline) *plan.Plan {
	t.Helper()
	got, err := plan.Build(req(mk(*pixel)), *meta, plan.Limits{})
	if err != nil {
		t.Fatalf("pixel focus: %v", err)
	}
	want, err := plan.Build(req(mk(*frac)), *meta, plan.Limits{})
	if err != nil {
		t.Fatalf("fractional focus: %v", err)
	}
	// Fractions may differ in the last bit (1-0.8 against 0.2); every
	// rectangle and size must match exactly.
	if diff := cmp.Diff(want, got, cmpopts.EquateApprox(0, 1e-12)); diff != "" {
		t.Fatalf("pixel focus plan differs from the fractional one (-frac +pixel):\n%s", diff)
	}
	return got
}

// TestPixelFocusMatchesFraction: a focus in source pixels plans exactly
// like the fractional focus at the same place, for every EXIF orientation
// (pixels are in the displayed image, so 5–8 swap the sides first), for
// several source and result sizes, with shrink-on-load, and for the
// pre-crop gravity.
func TestPixelFocusMatchesFraction(t *testing.T) {
	t.Parallel()
	sources := [][2]int{{1000, 600}, {600, 1000}, {4000, 3000}, {333, 777}}
	boxes := [][2]int{{100, 100}, {300, 50}, {40, 400}}
	points := [][2]float64{{0, 0}, {0.8, 0.3}, {0.25, 0.9}, {1, 1}}
	for _, src := range sources {
		for orient := 1; orient <= 8; orient++ {
			dw, dh := src[0], src[1]
			if orient >= 5 {
				dw, dh = dh, dw
			}
			for _, box := range boxes {
				for _, pt := range points {
					x, y := int(pt[0]*float64(dw)), int(pt[1]*float64(dh))
					fx, fy := float64(x)/float64(dw), float64(y)/float64(dh)
					meta := jpeg(src[0], src[1])
					meta.Orientation = orient
					name := fmt.Sprintf("%dx%d/o%d/%dx%d/%d,%d", src[0], src[1], orient, box[0], box[1], x, y)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						fill := func(g transform.Gravity) transform.Pipeline {
							return transform.Pipeline{
								Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(box[0]), Height: px(box[1])},
								Gravity: g,
							}
						}
						p := buildBoth(t, &meta, pixelGravity(x, y), fracGravity(fx, fy), fill)
						if len(stepsOf[plan.CropStep](p.Steps)) == 0 && src[0]*box[1] != src[1]*box[0] {
							t.Errorf("no result crop in %+v", p.Steps)
						}
						// Pre-crop only (source space, before resize).
						crop := func(g transform.Gravity) transform.Pipeline {
							return transform.Pipeline{Crop: &transform.Crop{Width: px(box[0]), Height: px(box[1]), Gravity: g}}
						}
						buildBoth(t, &meta, pixelGravity(x, y), fracGravity(fx, fy), crop)
					})
				}
			}
		}
	}
}

// TestPixelFocusRotateFlip: the pixel is in the source before the
// pipeline's rotate and flip, which move it like the image.
func TestPixelFocusRotateFlip(t *testing.T) {
	t.Parallel()
	const w, h, x, y = 1000, 600, 800, 150 // fractions 0.8, 0.25
	tests := []struct {
		name   string
		rotate int
		flip   transform.Flip
		fx, fy float64 // in the rotated and flipped image
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
		for _, orient := range []int{1, 6} {
			t.Run(fmt.Sprintf("%s/o%d", tc.name, orient), func(t *testing.T) {
				t.Parallel()
				meta := jpeg(w, h)
				if orient == 6 { // stored 600x1000, displayed 1000x600
					meta = jpeg(h, w)
				}
				meta.Orientation = orient
				mk := func(g transform.Gravity) transform.Pipeline {
					return transform.Pipeline{
						Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(200), Height: px(200)},
						Gravity: g, Rotate: tc.rotate, Flip: tc.flip,
						Crop: &transform.Crop{Width: px(500), Height: px(500), Gravity: g},
					}
				}
				// The pre-crop is 500x500 of the rotated source: the result
				// crop's fraction is relative to it, so compare the
				// pre-crop-free pipeline for the result crop...
				noCrop := func(g transform.Gravity) transform.Pipeline {
					p := mk(g)
					p.Crop = nil
					return p
				}
				buildBoth(t, &meta, pixelGravity(x, y), fracGravity(tc.fx, tc.fy), noCrop)
				// ...and a pre-crop with a compass gravity for the pre-crop.
				cropOnly := func(g transform.Gravity) transform.Pipeline {
					p := mk(transform.Gravity{Type: transform.GravityCenter})
					p.Crop.Gravity = g
					return p
				}
				buildBoth(t, &meta, pixelGravity(x, y), fracGravity(tc.fx, tc.fy), cropOnly)
			})
		}
	}
}

// TestPixelFocusAfterPreCrop: with a manual crop (thumbor's
// AxB:CxD), the focus of the result crop is relative to the crop, like
// thumbor subtracting the crop origin from its focal points.
func TestPixelFocusAfterPreCrop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		l, t, cw, ch   int
		x, y           int
		fx, fy         float64
		orient, srcW   int
		srcH, box0, b1 int
	}{
		{"inside", 100, 50, 400, 300, 400, 200, 0.75, 0.5, 1, 1000, 600, 100, 100},
		{"left of crop clamps", 100, 50, 400, 300, 20, 200, 0, 0.5, 1, 1000, 600, 100, 100},
		{"below crop clamps", 100, 50, 400, 300, 300, 590, 0.5, 1, 1, 1000, 600, 100, 100},
		{"exif 6 displayed pixels", 100, 50, 400, 300, 400, 200, 0.75, 0.5, 6, 600, 1000, 300, 60},
		{"exif 8 wide box", 0, 0, 500, 500, 125, 375, 0.25, 0.75, 8, 600, 1000, 400, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := jpeg(tc.srcW, tc.srcH)
			meta.Orientation = tc.orient
			mk := func(g transform.Gravity) transform.Pipeline {
				return transform.Pipeline{
					Resize:  transform.Resize{Type: transform.ResizeFill, Width: px(tc.box0), Height: px(tc.b1)},
					Gravity: g,
					Crop: &transform.Crop{
						Width: px(tc.cw), Height: px(tc.ch),
						Gravity: transform.Gravity{Type: transform.GravityNorthWest, XOffset: float64(tc.l), YOffset: float64(tc.t)},
					},
				}
			}
			buildBoth(t, &meta, pixelGravity(tc.x, tc.y), fracGravity(tc.fx, tc.fy), mk)
		})
	}
}

// TestPixelFocusRectangle: a rectangle focuses on its centre, rounded
// down like thumbor, and a chained pipeline reads pixels of the previous
// result.
func TestPixelFocusRectangle(t *testing.T) {
	t.Parallel()
	meta := jpeg(1000, 800)
	rect, _ := transform.FocusFromPixels(146, 206, 279, 360) // centre 212,283
	fill := func(g transform.Gravity) transform.Pipeline {
		return transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFill, Width: px(100), Height: px(300)}, Gravity: g}
	}
	buildBoth(t, &meta, &rect, fracGravity(0.212, 283.0/800), fill)

	first := transform.Pipeline{Resize: transform.Resize{Width: px(500)}} // 500x400
	chain := func(g transform.Gravity) *transform.Request {
		return req(first, fill(g))
	}
	got, err := plan.Build(chain(*pixelGravity(100, 300)), meta, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := plan.Build(chain(*fracGravity(0.2, 0.75)), meta, plan.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("chained pixel focus (-frac +pixel):\n%s", diff)
	}
}
