package ssim

import (
	"fmt"
	"image"
)

// Reference is one image prepared for repeated comparisons, as auto-quality
// does when it compares the pre-encode image with several candidate encodes:
// its luma planes are computed once, and Compare reuses one scratch plane and
// skips the SSIM map. The result equals [Compare] (without Result.Map).
//
// A Reference is not safe for concurrent use.
type Reference struct {
	img          image.Image
	w, h         int
	opaque       bool
	white, black []float64 // black is computed on first need
	scratch      []float64
}

// NewReference prepares img for Compare.
func NewReference(img image.Image) (*Reference, error) {
	b := img.Bounds()
	if b.Empty() {
		return nil, ErrEmpty
	}
	r := &Reference{img: img, w: b.Dx(), h: b.Dy(), opaque: opaque(img)}
	r.white = make([]float64, r.w*r.h)
	r.scratch = make([]float64, r.w*r.h)
	luma(r.white, img, white)
	return r, nil
}

// Compare returns the SSIM between the reference and img, which must have the
// same size. Result.Map is nil.
func (r *Reference) Compare(img image.Image) (Result, error) {
	b := img.Bounds()
	if b.Dx() != r.w || b.Dy() != r.h {
		return Result{}, fmt.Errorf("%w: %dx%d vs %dx%d", ErrSizeMismatch, r.w, r.h, b.Dx(), b.Dy())
	}
	luma(r.scratch, img, white)
	res := comparePlanes(r.white, r.scratch, r.w, r.h, false)
	if r.opaque && opaque(img) {
		return res, nil
	}
	if r.black == nil {
		r.black = make([]float64, r.w*r.h)
		luma(r.black, r.img, black)
	}
	luma(r.scratch, img, black)
	if onBlack := comparePlanes(r.black, r.scratch, r.w, r.h, false); onBlack.SSIM < res.SSIM {
		res = onBlack
	}
	return res, nil
}
