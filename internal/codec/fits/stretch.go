package fits

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
)

// Auto screen transfer function (PixInsight AutoSTF, Siril autostretch).
const (
	// targetBackground is where the median lands.
	targetBackground = 0.25
	// shadowsClip is the shadows clip in normalised MADs below the median.
	shadowsClip = -2.8
	// madNorm makes the MAD a consistent estimator of the standard
	// deviation of normal data.
	madNorm = 1.4826
	// maxStatSamples bounds the subsample the median and MAD come from.
	maxStatSamples = 1 << 20
)

// mtf is the midtones transfer function: MTF(m, 0) = 0, MTF(m, m) = 0.5,
// MTF(m, 1) = 1.
func mtf(m, x float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	case x == m:
		return 0.5
	}
	return (m - 1) * x / ((2*m-1)*x - m)
}

// stf is the stretch fitted to an image: x = (v − lo)/(hi − lo), then
// MTF(mid, clamp((x − c0)/(1 − c0))).
type stf struct {
	lo, hi  float64
	c0, mid float64
	flat    bool // constant image: mid-grey
}

// fitSTF measures the drawn planes of src.
func fitSTF(src samples, n, planes int) stf {
	lo, hi := math.Inf(1), math.Inf(-1)
	for p := range planes {
		for i := range n {
			v := src(p, i)
			if isFinite(v) {
				lo, hi = min(lo, v), max(hi, v)
			}
		}
	}
	switch {
	case lo > hi: // no finite sample: everything draws black
		return stf{lo: 0, hi: 1, c0: 0, mid: 0.5}
	case lo == hi:
		return stf{flat: true}
	}
	total := n * planes
	step := max(1, (total+maxStatSamples-1)/maxStatSamples)
	xs := make([]float64, 0, min(total, maxStatSamples))
	for k := 0; k < total; k += step {
		v := src(k/n, k%n)
		if isFinite(v) {
			xs = append(xs, (v-lo)/(hi-lo))
		}
	}
	s := stf{lo: lo, hi: hi, mid: 0.5}
	if len(xs) == 0 {
		return s
	}
	med := median(xs)
	for i, x := range xs {
		xs[i] = math.Abs(x - med)
	}
	mad := madNorm * median(xs)
	s.c0 = min(max(med+shadowsClip*mad, 0), 1)
	if s.c0 >= 1 {
		s.c0 = 0
	}
	if med-s.c0 > 0 {
		if m := mtf(targetBackground, med-s.c0); m > 0 && m < 1 {
			s.mid = m
		}
	}
	return s
}

// apply maps a physical value to a grey level.
func (s *stf) apply(v float64) uint8 {
	if !isFinite(v) {
		return 0
	}
	if s.flat {
		return 128
	}
	x := (v - s.lo) / (s.hi - s.lo)
	t := min(max((x-s.c0)/(1-s.c0), 0), 1)
	return uint8(math.Round(255 * mtf(s.mid, t)))
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// median returns the median of xs, reordering it (quickselect).
func median(xs []float64) float64 {
	n := len(xs)
	if n%2 == 1 {
		return selectK(xs, n/2)
	}
	a := selectK(xs, n/2-1)
	b := selectK(xs[n/2:], 0) // the smallest of the upper half
	return (a + b) / 2
}

// selectK partially sorts xs so that xs[k] is the k-th smallest and every
// element before it is not larger; it returns xs[k]. Pivots are the median
// of three, so sorted and constant inputs stay linear on average.
func selectK(xs []float64, k int) float64 {
	lo, hi := 0, len(xs)-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		if xs[mid] < xs[lo] {
			xs[mid], xs[lo] = xs[lo], xs[mid]
		}
		if xs[hi] < xs[lo] {
			xs[hi], xs[lo] = xs[lo], xs[hi]
		}
		if xs[hi] < xs[mid] {
			xs[hi], xs[mid] = xs[mid], xs[hi]
		}
		pivot := xs[mid]
		i, j := lo, hi
		for i <= j {
			for xs[i] < pivot {
				i++
			}
			for xs[j] > pivot {
				j--
			}
			if i <= j {
				xs[i], xs[j] = xs[j], xs[i]
				i++
				j--
			}
		}
		switch {
		case k <= j:
			hi = j
		case k >= i:
			lo = i
		default:
			return xs[k]
		}
	}
	return xs[k]
}

// render draws the planes of src as 8-bit RGB, flipping the rows (FITS
// stores the bottom row first).
func render(src samples, w, h, planes int, identity bool) *raster.Raw {
	n := w * h
	out := &raster.Raw{Width: w, Height: h, Bands: 3, Data: make([]byte, n*3)}
	var s stf
	if !identity {
		s = fitSTF(src, n, planes)
	}
	for p := range 3 {
		plane := min(p, planes-1)
		for y := range h {
			row := out.Data[(h-1-y)*w*3:]
			for x := range w {
				v := src(plane, y*w+x)
				var g uint8
				if identity {
					if isFinite(v) {
						g = uint8(min(max(math.Round(v), 0), 255))
					}
				} else {
					g = s.apply(v)
				}
				row[x*3+p] = g
			}
		}
		if planes == 1 {
			// Grey: copy band 0 to 1 and 2.
			for i := range n {
				out.Data[i*3+1] = out.Data[i*3]
				out.Data[i*3+2] = out.Data[i*3]
			}
			break
		}
	}
	return out
}
