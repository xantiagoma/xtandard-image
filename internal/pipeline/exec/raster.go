package exec

import (
	"cmp"
	"math"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
)

// coverageRows is the number of sub-scanlines sampled per pixel row.
// Horizontally, each span's coverage is exact; vertically it is quantised
// to 1/64 (at most 4 alpha levels off on near-horizontal edges). A 1080p
// ellipse takes about 6 ms (BenchmarkCoverage).
const coverageRows = 64

// edge is a non-horizontal polygon edge with y0 < y1.
type edge struct{ x0, y0, x1, y1 float64 }

func (e *edge) xAt(y float64) float64 {
	return e.x0 + (y-e.y0)*(e.x1-e.x0)/(e.y1-e.y0)
}

// coverage rasterises the closed polygon o into a w×h 8-bit alpha mask,
// 255 inside and 0 outside, anti-aliased by area coverage: every pixel row
// is sampled at 64 sub-scanlines, and on each the polygon's spans (even-odd
// rule) add their exact horizontal overlap with every pixel. Pixels fully
// inside are exactly 255, so the mask never alters the interior. The mask
// is computed in Go because libvips' draw operations do not anti-alias;
// it is generated at the frame's pixel size, after DPR scaling, so edges
// are smooth at any size.
func coverage(o plan.Outline, w, h int) []byte {
	out := make([]byte, w*h)
	if len(o) < 3 || w <= 0 || h <= 0 {
		return out
	}
	edges := make([]edge, 0, len(o))
	for i, p := range o {
		q := o[(i+1)%len(o)]
		switch {
		case p.Y < q.Y:
			edges = append(edges, edge{p.X, p.Y, q.X, q.Y})
		case p.Y > q.Y:
			edges = append(edges, edge{q.X, q.Y, p.X, p.Y})
		default: // horizontal: never crosses a sub-scanline
		}
	}
	slices.SortFunc(edges, func(a, b edge) int { return cmp.Compare(a.y0, b.y0) })

	const weight = 1.0 / coverageRows
	partial := make([]float64, w)
	runs := make([]float64, w+1) // difference array of fully covered runs
	xs := make([]float64, 0, 8)
	active := make([]int, 0, 8)
	next := 0
	span := func(xa, xb float64) {
		xa, xb = max(xa, 0), min(xb, float64(w))
		if xb <= xa {
			return
		}
		ia, ib := int(xa), int(xb)
		if ia == ib {
			partial[ia] += (xb - xa) * weight
			return
		}
		partial[ia] += (float64(ia+1) - xa) * weight
		runs[ia+1] += weight
		runs[ib] -= weight
		if ib < w {
			partial[ib] += (xb - float64(ib)) * weight
		}
	}
	for row := range h {
		clear(partial)
		clear(runs)
		for k := range coverageRows {
			y := float64(row) + (float64(k)+0.5)*weight
			for next < len(edges) && edges[next].y0 <= y {
				active = append(active, next)
				next++
			}
			active = slices.DeleteFunc(active, func(i int) bool { return edges[i].y1 <= y })
			xs = xs[:0]
			for _, i := range active {
				xs = append(xs, edges[i].xAt(y))
			}
			slices.Sort(xs)
			for i := 0; i+1 < len(xs); i += 2 {
				span(xs[i], xs[i+1])
			}
		}
		line := out[row*w : (row+1)*w]
		run := 0.0
		for x := range line {
			run += runs[x]
			v := math.Round((run + partial[x]) * 255)
			line[x] = uint8(min(max(v, 0), 255))
		}
	}
	return out
}
