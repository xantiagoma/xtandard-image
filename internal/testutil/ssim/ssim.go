// Package ssim computes the structural similarity index (SSIM) and the
// distance DSSIM = (1 - SSIM) / 2 between two images.
//
// The metric follows Z. Wang, A. C. Bovik, H. R. Sheikh and E. P. Simoncelli,
// "Image Quality Assessment: From Error Visibility to Structural Similarity",
// IEEE Transactions on Image Processing 13(4), 2004, section III-C, exactly as
// the authors' reference MATLAB code ssim_index.m does it: an 11x11 circular
// Gaussian window with standard deviation 1.5 (normalised to unit sum), K1 =
// 0.01, K2 = 0.03, L = 255, evaluated at every pixel where the window fits
// ("valid" filtering) and averaged into the mean SSIM. The reference code's
// optional pre-downsampling is not applied. It is written from the paper; it
// is not derived from the AGPL `dssim` tool (ADR 0006).
//
// SSIM is computed on luma: Y = 0.299 R + 0.587 G + 0.114 B (ITU-R BT.601, the
// weights of MATLAB's rgb2gray used by the paper) on non-linear 8-bit-scale
// values. It ignores chroma, which is a deliberate trade-off: the metric is
// used for golden tests and auto-quality, where geometry and detail errors
// dominate.
//
// Alpha: when either image has a non-opaque pixel, both images are composited
// onto white and, separately, onto black, and the lower of the two SSIM
// scores is reported. A difference that only shows in transparent areas
// (colour under alpha 0) is ignored, as it is invisible; a difference in the
// alpha channel itself shows against at least one of the two backgrounds.
//
// Images narrower or shorter than the window are compared with one global
// window of uniform weights.
//
// The package is pure Go and allocation-light (one luma plane per image plus
// an 11-row ring buffer), so it can also back auto-quality (ARCHITECTURE §8).
package ssim

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
)

// Window parameters of Wang et al. 2004, section III-C.
const (
	windowSize = 11
	radius     = windowSize / 2
	sigma      = 1.5
	k1         = 0.01
	k2         = 0.03
	dynRange   = 255.0
	c1         = (k1 * dynRange) * (k1 * dynRange)
	c2         = (k2 * dynRange) * (k2 * dynRange)
)

// ErrSizeMismatch is returned when the two images have different dimensions.
var ErrSizeMismatch = errors.New("ssim: image sizes differ")

// ErrEmpty is returned for an image with no pixels.
var ErrEmpty = errors.New("ssim: empty image")

// Result is the outcome of [Compare].
type Result struct {
	// SSIM is the mean structural similarity in [-1, 1]; 1 means identical.
	SSIM float64
	// DSSIM is (1 - SSIM) / 2: 0 means identical, larger is more different.
	DSSIM float64
	// Map holds the local SSIM of every window position, row-major,
	// MapWidth x MapHeight. Position (0, 0) is the window centred on pixel
	// (Offset, Offset) of the input.
	Map                 []float32
	MapWidth, MapHeight int
	Offset              int
}

// DSSIM converts an SSIM score into the distance (1 - ssim) / 2.
func DSSIM(ssim float64) float64 { return (1 - ssim) / 2 }

// Compare returns the SSIM between a and b, which must have the same size.
// Bounds origins may differ; pixels are matched relative to Min.
func Compare(a, b image.Image) (Result, error) {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return Result{}, fmt.Errorf("%w: %dx%d vs %dx%d", ErrSizeMismatch, ab.Dx(), ab.Dy(), bb.Dx(), bb.Dy())
	}
	if ab.Empty() {
		return Result{}, ErrEmpty
	}
	w, h := ab.Dx(), ab.Dy()
	la, lb := make([]float64, w*h), make([]float64, w*h)

	luma(la, a, white)
	luma(lb, b, white)
	res := comparePlanes(la, lb, w, h)
	if opaque(a) && opaque(b) {
		return res, nil
	}
	luma(la, a, black)
	luma(lb, b, black)
	if onBlack := comparePlanes(la, lb, w, h); onBlack.SSIM < res.SSIM {
		res = onBlack
	}
	return res, nil
}

type background uint32

const (
	black background = 0
	white background = 0xff
)

// luma writes the BT.601 luma of img, composited on bg, into dst on a 0..255
// scale. Channels are first reduced to 8 bits the way Go's colour models do
// (c >> 8 of the premultiplied 16-bit value), so an image and its 8-bit copy
// (for example a golden PNG) have identical luma.
func luma(dst []float64, img image.Image, bg background) {
	b := img.Bounds()
	w := b.Dx()
	put := func(i int, r, g, bl, a uint32) {
		// Colours are alpha-premultiplied: over(bg) = c + bg*(1-a).
		k := uint32(bg) * (0xff - a) / 0xff
		dst[i] = 0.299*float64(r+k) + 0.587*float64(g+k) + 0.114*float64(bl+k)
	}
	switch img := img.(type) {
	case *image.Gray:
		for y := range b.Dy() {
			row := img.Pix[y*img.Stride : y*img.Stride+w]
			for x, v := range row {
				c := uint32(v)
				put(y*w+x, c, c, c, 0xff)
			}
		}
	case *image.RGBA:
		for y := range b.Dy() {
			row := img.Pix[y*img.Stride : y*img.Stride+4*w]
			for x := range w {
				p := row[4*x : 4*x+4 : 4*x+4]
				put(y*w+x, uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3]))
			}
		}
	case image.RGBA64Image: // every other stdlib type, without per-pixel allocation
		for y := range b.Dy() {
			for x := range w {
				c := img.RGBA64At(b.Min.X+x, b.Min.Y+y)
				put(y*w+x, uint32(c.R>>8), uint32(c.G>>8), uint32(c.B>>8), uint32(c.A>>8))
			}
		}
	default:
		for y := range b.Dy() {
			for x := range w {
				r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				put(y*w+x, r>>8, g>>8, bl>>8, a>>8)
			}
		}
	}
}

// opaque reports whether every pixel of img is fully opaque.
func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}

// kernel returns the normalised 1-D Gaussian; the 2-D window of the paper is
// its outer product, so filtering is separable.
func kernel() [windowSize]float64 {
	var k [windowSize]float64
	var sum float64
	for i := range k {
		d := float64(i - radius)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

// local is the SSIM of one window from its weighted moments (paper eq. 13).
// The expression is symmetric so identical inputs give exactly 1.
func local(mux, muy, exx, eyy, exy float64) float64 {
	sxx := exx - mux*mux
	syy := eyy - muy*muy
	sxy := exy - mux*muy
	return ((2*mux*muy + c1) * (2*sxy + c2)) / ((mux*mux + muy*muy + c1) * (sxx + syy + c2))
}

// comparePlanes computes SSIM on two w x h luma planes.
func comparePlanes(x, y []float64, w, h int) Result {
	if w < windowSize || h < windowSize {
		return global(x, y)
	}
	k := kernel()
	mw, mh := w-2*radius, h-2*radius

	// ring holds the horizontally filtered moments of the last windowSize
	// input rows: slot s, moment q at ring[(s*moments+q)*mw:].
	const moments = 5
	ring := make([]float64, windowSize*moments*mw)
	acc := make([]float64, moments*mw)
	hfilter := func(row int) {
		slot := ring[(row%windowSize)*moments*mw:][:moments*mw]
		mx, my := slot[0:mw], slot[mw:2*mw]
		mxx, myy, mxy := slot[2*mw:3*mw], slot[3*mw:4*mw], slot[4*mw:5*mw]
		xr, yr := x[row*w:(row+1)*w], y[row*w:(row+1)*w]
		for i := range mw {
			var sx, sy, sxx, syy, sxy float64
			xs, ys := xr[i:i+windowSize:i+windowSize], yr[i:i+windowSize:i+windowSize]
			for j, kj := range k {
				a, b := xs[j], ys[j]
				sx += kj * a
				sy += kj * b
				sxx += kj * a * a
				syy += kj * b * b
				sxy += kj * a * b
			}
			mx[i], my[i], mxx[i], myy[i], mxy[i] = sx, sy, sxx, syy, sxy
		}
	}

	res := Result{Map: make([]float32, mw*mh), MapWidth: mw, MapHeight: mh, Offset: radius}
	for row := range windowSize - 1 {
		hfilter(row)
	}
	var total float64
	for oy := range mh {
		hfilter(oy + windowSize - 1)
		clear(acc)
		for j, kj := range k {
			slot := ring[((oy+j)%windowSize)*moments*mw:][:moments*mw]
			for i, v := range slot {
				acc[i] += kj * v
			}
		}
		out := res.Map[oy*mw : (oy+1)*mw]
		for i := range mw {
			s := local(acc[i], acc[mw+i], acc[2*mw+i], acc[3*mw+i], acc[4*mw+i])
			out[i] = float32(s)
			total += s
		}
	}
	res.SSIM = total / float64(mw*mh)
	res.DSSIM = DSSIM(res.SSIM)
	return res
}

// global compares images smaller than the window with a single window of
// uniform weights over the whole image.
func global(x, y []float64) Result {
	n := float64(len(x))
	var sx, sy, sxx, syy, sxy float64
	for i := range x {
		a, b := x[i], y[i]
		sx += a
		sy += b
		sxx += a * a
		syy += b * b
		sxy += a * b
	}
	s := local(sx/n, sy/n, sxx/n, syy/n, sxy/n)
	return Result{SSIM: s, DSSIM: DSSIM(s), Map: []float32{float32(s)}, MapWidth: 1, MapHeight: 1}
}

// HeatMap renders the local distance (1 - SSIM) / 2 of r.Map as an image the
// size of the map: black is identical, then red, yellow and white as the
// distance grows (white at 0.05 and above, on a square-root scale so small
// differences stay visible).
func (r Result) HeatMap() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, r.MapWidth, r.MapHeight))
	for i, s := range r.Map {
		t := math.Sqrt(math.Max(0, math.Min(1, DSSIM(float64(s))/0.05)))
		ch := func(off float64) uint8 { return uint8(255 * math.Max(0, math.Min(1, 3*t-off))) }
		img.SetRGBA(i%r.MapWidth, i/r.MapWidth, color.RGBA{ch(0), ch(1), ch(2), 255})
	}
	return img
}
