package video

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// grid is a small frame of distinct pixels, to check orientations by
// moving pixels like the filters do.
type grid [][]int

func newGrid(w, h int) grid {
	g := make(grid, h)
	for y := range g {
		g[y] = make([]int, w)
		for x := range g[y] {
			g[y][x] = y*w + x
		}
	}
	return g
}

func (g grid) hflip() grid {
	out := newGrid(len(g[0]), len(g))
	for y := range g {
		for x := range g[y] {
			out[y][len(g[y])-1-x] = g[y][x]
		}
	}
	return out
}

func (g grid) vflip() grid {
	out := newGrid(len(g[0]), len(g))
	for y := range g {
		copy(out[len(g)-1-y], g[y])
	}
	return out
}

// clock rotates 90° clockwise.
func (g grid) clock() grid {
	h, w := len(g), len(g[0])
	out := newGrid(h, w)
	for y := range g {
		for x := range g[y] {
			out[x][h-1-y] = g[y][x]
		}
	}
	return out
}

// apply runs libavfilter filters on g (transpose's directions as
// documented: clock_flip is a clockwise turn then a vertical flip).
func (g grid) apply(t *testing.T, filters []string) grid {
	t.Helper()
	for _, f := range filters {
		switch f {
		case "hflip":
			g = g.hflip()
		case "vflip":
			g = g.vflip()
		case "transpose=clock":
			g = g.clock()
		case "transpose=cclock":
			g = g.clock().clock().clock()
		case "transpose=clock_flip":
			g = g.clock().vflip()
		case "transpose=cclock_flip":
			g = g.clock().clock().clock().vflip()
		default:
			t.Fatalf("unknown filter %q", f)
		}
	}
	return g
}

// TestOrientation: the filters of a display matrix composed with a
// request's rotation and flips move pixels like applying each in turn.
func TestOrientation(t *testing.T) {
	t.Parallel()
	for _, srcFlip := range []bool{false, true} {
		for srcRot := 0; srcRot < 360; srcRot += 90 {
			for rot := 0; rot < 360; rot += 90 {
				for _, fh := range []bool{false, true} {
					for _, fv := range []bool{false, true} {
						want := newGrid(3, 2)
						if srcFlip {
							want = want.hflip()
						}
						for range (srcRot + rot) / 90 {
							want = want.clock()
						}
						if fh {
							want = want.hflip()
						}
						if fv {
							want = want.vflip()
						}
						o := orientation{flip: srcFlip, rot: srcRot}.rotate(rot).flipH(fh).flipV(fv)
						if got := newGrid(3, 2).apply(t, o.filters()); !cmp.Equal(got, want) {
							t.Errorf("source (%v,%d) rotate %d flips %v/%v: %v gives %v, want %v",
								srcFlip, srcRot, rot, fh, fv, o.filters(), got, want)
						}
					}
				}
			}
		}
	}
}
