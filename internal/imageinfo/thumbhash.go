// ThumbHash encoder: a Go port of rgbaToThumbHash from the reference
// JavaScript implementation of https://github.com/evanw/thumbhash
// (js/thumbhash.js), Copyright (c) 2023 Evan Wallace, MIT License. It
// computes in float64 like JavaScript, so it is bit-exact with the
// reference (testdata/thumbhash).

package imageinfo

import (
	"errors"
	"math"
)

// MaxThumbhashSide is the largest input side the ThumbHash encoder takes;
// larger images must be downscaled first (Downscale).
const MaxThumbhashSide = 100

// ErrThumbhashSize is returned for an input larger than 100×100.
var ErrThumbhashSize = errors.New("imageinfo: thumbhash input must fit in 100x100")

// Thumbhash encodes m (at most 100×100, see Downscale) as a ThumbHash
// (imgproxy info `thumb_hash`, which reports it as upper-case hex).
func Thumbhash(m *Image) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	w, h := m.Width, m.Height
	if w > MaxThumbhashSide || h > MaxThumbhashSide {
		return nil, ErrThumbhashSize
	}
	rgba := m.Pix

	// Average colour, alpha-weighted.
	var avgR, avgG, avgB, avgA float64
	for i := range w * h {
		alpha := float64(rgba[i*4+3]) / 255
		avgR += float64(alpha / 255 * float64(rgba[i*4]))
		avgG += float64(alpha / 255 * float64(rgba[i*4+1]))
		avgB += float64(alpha / 255 * float64(rgba[i*4+2]))
		avgA += alpha
	}
	if avgA > 0 {
		avgR /= avgA
		avgG /= avgA
		avgB /= avgA
	}

	hasAlpha := avgA < float64(w*h)
	lLimit := 7.0 // fewer luminance bits with alpha
	if hasAlpha {
		lLimit = 5
	}
	maxWH := float64(max(w, h))
	lx := max(1, int(jsRound(lLimit*float64(w)/maxWH)))
	ly := max(1, int(jsRound(lLimit*float64(h)/maxWH)))

	// RGBA → LPQA, composited atop the average colour.
	l := make([]float64, w*h)
	p := make([]float64, w*h)
	q := make([]float64, w*h)
	a := make([]float64, w*h)
	for i := range w * h {
		alpha := float64(rgba[i*4+3]) / 255
		r := float64(avgR*(1-alpha)) + float64(alpha/255*float64(rgba[i*4]))
		g := float64(avgG*(1-alpha)) + float64(alpha/255*float64(rgba[i*4+1]))
		b := float64(avgB*(1-alpha)) + float64(alpha/255*float64(rgba[i*4+2]))
		l[i] = (r + g + b) / 3
		p[i] = (r+g)/2 - b
		q[i] = r - g
		a[i] = alpha
	}

	fx := make([]float64, w)
	encode := func(channel []float64, nx, ny int) (dc float64, ac []float64, scale float64) {
		for cy := range ny {
			for cx := 0; cx*ny < nx*(ny-cy); cx++ {
				f := 0.0
				for x := range w {
					fx[x] = math.Cos(math.Pi / float64(w) * float64(cx) * (float64(x) + 0.5))
				}
				for y := range h {
					fy := math.Cos(math.Pi / float64(h) * float64(cy) * (float64(y) + 0.5))
					for x := range w {
						f += float64(channel[x+y*w] * fx[x] * fy)
					}
				}
				f /= float64(w * h)
				if cx > 0 || cy > 0 {
					ac = append(ac, f)
					scale = max(scale, math.Abs(f))
				} else {
					dc = f
				}
			}
		}
		if scale > 0 {
			for i := range ac {
				ac[i] = 0.5 + float64(0.5/scale*ac[i])
			}
		}
		return dc, ac, scale
	}
	lDC, lAC, lScale := encode(l, max(3, lx), max(3, ly))
	pDC, pAC, pScale := encode(p, 3, 3)
	qDC, qAC, qScale := encode(q, 3, 3)
	var aDC, aScale float64
	var aAC []float64
	if hasAlpha {
		aDC, aAC, aScale = encode(a, 5, 5)
	}

	isLandscape := w > h
	header24 := int(jsRound(63*lDC)) | int(jsRound(31.5+float64(31.5*pDC)))<<6 | int(jsRound(31.5+float64(31.5*qDC)))<<12 |
		int(jsRound(31*lScale))<<18 | b2i(hasAlpha)<<23
	lSide := lx
	if isLandscape {
		lSide = ly
	}
	header16 := lSide | int(jsRound(63*pScale))<<3 | int(jsRound(63*qScale))<<9 | b2i(isLandscape)<<15

	acStart := 5
	if hasAlpha {
		acStart = 6
	}
	nAC := len(lAC) + len(pAC) + len(qAC) + len(aAC)
	hash := make([]byte, acStart+(nAC+1)/2)
	hash[0], hash[1], hash[2] = lo8(header24), lo8(header24>>8), lo8(header24>>16)
	hash[3], hash[4] = lo8(header16), lo8(header16>>8)
	if hasAlpha {
		hash[5] = lo8(int(jsRound(15*aDC)) | int(jsRound(15*aScale))<<4)
	}
	idx := 0
	for _, ac := range [][]float64{lAC, pAC, qAC, aAC} {
		for _, f := range ac {
			hash[acStart+idx>>1] |= lo8(int(jsRound(15*f)) << ((idx & 1) << 2))
			idx++
		}
	}
	return hash, nil
}

// lo8 is the low byte of v.
func lo8(v int) byte {
	return byte(v & 0xff)
}

// jsRound is JavaScript's Math.round: halves round towards +∞.
func jsRound(v float64) float64 { return math.Floor(v + 0.5) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
