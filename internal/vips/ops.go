package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Processing operations. Each returns a new lazy Image that references its
// input (close both), checks ctx before crossing into C, and fails with
// KindInternal for invalid arguments, or KindCorrupt when the source's
// decoder failed while the operation computed pixels.
//
// Multi-frame images (a strip with page-height, see LoadOptions.NPages)
// are handled per frame where it matters: Crop, Embed, Gravity, Flip
// (vertical), Rot, Autorot, Similarity and Affine transform every frame and
// fix page-height; Resize keeps page-height a whole number of rows.
// ExtractArea, SmartCrop and Trim/FindTrim work on the whole strip, and the
// latter two refuse multi-frame images.

// op finishes one operation: classify the error or wrap the output.
func (img *Image) op(ctx context.Context, name string, rc C.int, out *C.VipsImage, cerr *C.char) (*Image, error) {
	if err := stepError(ctx, rc, name, cerr, img.src); err != nil {
		return nil, err
	}
	return img.derive(out), nil
}

// Resize scales by hscale horizontally and vscale vertically (0 means
// hscale) with kernel. Animated strips keep an exact page-height.
func (img *Image) Resize(ctx context.Context, hscale, vscale float64, kernel Kernel) (*Image, error) {
	if err := ctxError(ctx, "vips: resize"); err != nil {
		return nil, err
	}
	if vscale == 0 {
		vscale = hscale
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_resize(img.p, C.double(hscale), C.double(vscale), C.int(kernel), &out, &cerr)
	return img.op(ctx, "resize", rc, out, cerr)
}

// ExtractArea cuts r out of the whole image (all frames as one strip).
func (img *Image) ExtractArea(ctx context.Context, r image.Rectangle) (*Image, error) {
	if err := ctxError(ctx, "vips: extract_area"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_extract_area(img.p, C.int(r.Min.X), C.int(r.Min.Y), C.int(r.Dx()), C.int(r.Dy()),
		&out, &cerr)
	return img.op(ctx, "extract_area", rc, out, cerr)
}

// Crop cuts r out of every frame (r is in frame coordinates).
func (img *Image) Crop(ctx context.Context, r image.Rectangle) (*Image, error) {
	if err := ctxError(ctx, "vips: crop"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_crop(img.p, C.int(r.Min.X), C.int(r.Min.Y), C.int(r.Dx()), C.int(r.Dy()),
		&out, &cerr)
	return img.op(ctx, "crop", rc, out, cerr)
}

// Embed places every frame at (x, y) on a width×height canvas filled per
// extend; background is used with ExtendBackground (nil: black).
func (img *Image) Embed(ctx context.Context, x, y, width, height int, extend Extend, background []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: embed"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_embed(img.p, C.int(x), C.int(y), C.int(width), C.int(height), C.int(extend),
		cdoubles(background), C.int(len(background)), &out, &cerr)
	return img.op(ctx, "embed", rc, out, cerr)
}

// Gravity places every frame on a width×height canvas anchored at dir,
// cropping when the canvas is smaller.
func (img *Image) Gravity(ctx context.Context, dir Compass, width, height int, extend Extend, background []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: gravity"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_gravity(img.p, C.int(dir), C.int(width), C.int(height), C.int(extend),
		cdoubles(background), C.int(len(background)), &out, &cerr)
	return img.op(ctx, "gravity", rc, out, cerr)
}

// Flip mirrors every frame.
func (img *Image) Flip(ctx context.Context, dir Direction) (*Image, error) {
	if err := ctxError(ctx, "vips: flip"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_flip(img.p, C.int(dir), &out, &cerr)
	return img.op(ctx, "flip", rc, out, cerr)
}

// Rot rotates every frame clockwise by a right angle. 90° and 270° need
// random access to the input.
func (img *Image) Rot(ctx context.Context, angle Angle) (*Image, error) {
	if err := ctxError(ctx, "vips: rot"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_rot(img.p, C.int(angle), &out, &cerr)
	return img.op(ctx, "rot", rc, out, cerr)
}

// Autorot applies the EXIF orientation to every frame and removes the tag.
func (img *Image) Autorot(ctx context.Context) (*Image, error) {
	if err := ctxError(ctx, "vips: autorot"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_autorot(img.p, &out, &cerr)
	return img.op(ctx, "autorot", rc, out, cerr)
}

// SmartCrop crops a single-frame image to width×height around its most
// interesting region. It computes (needs random access) and is killed when
// ctx ends.
func (img *Image) SmartCrop(ctx context.Context, width, height int, interesting Interesting) (*Image, error) {
	if err := ctxError(ctx, "vips: smartcrop"); err != nil {
		return nil, err
	}
	stop := KillOnCancel(ctx, img)
	defer stop()
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_smartcrop(img.p, C.int(width), C.int(height), C.int(interesting), &out, &cerr)
	return img.op(ctx, "smartcrop", rc, out, cerr)
}

// Flatten removes the alpha band by blending onto background, given on the
// 8-bit scale (scaled for 16-bit images; nil: black). Without alpha it
// returns a plain copy.
func (img *Image) Flatten(ctx context.Context, background []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: flatten"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_flatten(img.p, cdoubles(background), C.int(len(background)), &out, &cerr)
	return img.op(ctx, "flatten", rc, out, cerr)
}

// Premultiply multiplies the colour bands by alpha (output is float).
func (img *Image) Premultiply(ctx context.Context) (*Image, error) {
	if err := ctxError(ctx, "vips: premultiply"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_premultiply(img.p, &out, &cerr)
	return img.op(ctx, "premultiply", rc, out, cerr)
}

// Unpremultiply divides the colour bands by alpha and casts back to the
// input's band format.
func (img *Image) Unpremultiply(ctx context.Context) (*Image, error) {
	if err := ctxError(ctx, "vips: unpremultiply"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_unpremultiply(img.p, &out, &cerr)
	return img.op(ctx, "unpremultiply", rc, out, cerr)
}

// Colourspace converts to interpretation (no ICC; see ICCTransform).
func (img *Image) Colourspace(ctx context.Context, interpretation Interpretation) (*Image, error) {
	if err := ctxError(ctx, "vips: colourspace"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_colourspace(img.p, C.int(interpretation), &out, &cerr)
	return img.op(ctx, "colourspace", rc, out, cerr)
}

// ICCImport converts to the PCS (Lab) with the embedded profile when
// embedded is true and present, else profile (a path or built-in name such
// as "srgb"; empty: libvips' default for the interpretation).
func (img *Image) ICCImport(ctx context.Context, profile string, intent Intent, embedded bool) (*Image, error) {
	if err := ctxError(ctx, "vips: icc_import"); err != nil {
		return nil, err
	}
	p := cstr(profile)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_icc_import(img.p, cptr(p), C.int(intent), cbool(embedded), &out, &cerr)
	return img.op(ctx, "icc_import", rc, out, cerr)
}

// ICCExport converts from the PCS to profile (empty: sRGB) at depth bits
// (8 or 16).
func (img *Image) ICCExport(ctx context.Context, profile string, intent Intent, depth int) (*Image, error) {
	if err := ctxError(ctx, "vips: icc_export"); err != nil {
		return nil, err
	}
	p := cstr(profile)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_icc_export(img.p, cptr(p), C.int(intent), C.int(depth), &out, &cerr)
	return img.op(ctx, "icc_export", rc, out, cerr)
}

// ICCTransform converts to profile (required), using the embedded input
// profile when embedded is true, at depth bits (8 or 16).
func (img *Image) ICCTransform(ctx context.Context, profile string, intent Intent, embedded bool, depth int) (*Image, error) {
	if err := ctxError(ctx, "vips: icc_transform"); err != nil {
		return nil, err
	}
	if profile == "" {
		return nil, errs.New(errs.KindInternal, "vips: icc_transform needs a profile")
	}
	p := cstr(profile)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_icc_transform(img.p, cptr(p), C.int(intent), cbool(embedded), C.int(depth), &out, &cerr)
	return img.op(ctx, "icc_transform", rc, out, cerr)
}

// Cast converts the band format, clipping out-of-range values.
func (img *Image) Cast(ctx context.Context, format BandFormat) (*Image, error) {
	if err := ctxError(ctx, "vips: cast"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_cast(img.p, C.int(format), &out, &cerr)
	return img.op(ctx, "cast", rc, out, cerr)
}

// Copy returns a new node over the same pixels with header changes.
func (img *Image) Copy(ctx context.Context, opts CopyOptions) (*Image, error) {
	if err := ctxError(ctx, "vips: copy"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_copy(img.p, C.int(opts.Interpretation), C.double(opts.XRes), C.double(opts.YRes),
		C.int(opts.PageHeight), &out, &cerr)
	return img.op(ctx, "copy", rc, out, cerr)
}

// Gaussblur blurs with a gaussian of sigma.
func (img *Image) Gaussblur(ctx context.Context, sigma float64) (*Image, error) {
	if err := ctxError(ctx, "vips: gaussblur"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_gaussblur(img.p, C.double(sigma), &out, &cerr)
	return img.op(ctx, "gaussblur", rc, out, cerr)
}

// Sharpen applies vips_sharpen (start from DefaultSharpenOptions).
func (img *Image) Sharpen(ctx context.Context, o SharpenOptions) (*Image, error) {
	if err := ctxError(ctx, "vips: sharpen"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_sharpen(img.p, C.double(o.Sigma), C.double(o.X1), C.double(o.Y2), C.double(o.Y3),
		C.double(o.M1), C.double(o.M2), &out, &cerr)
	return img.op(ctx, "sharpen", rc, out, cerr)
}

// Composite2 blends overlay onto img at (x, y) with mode.
func (img *Image) Composite2(ctx context.Context, overlay *Image, mode BlendMode, x, y int) (*Image, error) {
	if err := ctxError(ctx, "vips: composite2"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_composite2(img.p, overlay.p, C.int(mode), C.int(x), C.int(y), &out, &cerr)
	return img.op(ctx, "composite2", rc, out, cerr)
}

// Replicate tiles img across × down times.
func (img *Image) Replicate(ctx context.Context, across, down int) (*Image, error) {
	if err := ctxError(ctx, "vips: replicate"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_replicate(img.p, C.int(across), C.int(down), &out, &cerr)
	return img.op(ctx, "replicate", rc, out, cerr)
}

// Join places other to the right of (horizontal) or below (vertical) img.
func (img *Image) Join(ctx context.Context, other *Image, dir Direction) (*Image, error) {
	if err := ctxError(ctx, "vips: join"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_join(img.p, other.p, C.int(dir), &out, &cerr)
	return img.op(ctx, "join", rc, out, cerr)
}

// Arrayjoin lays imgs out in a grid across images wide (1 stacks them into
// a strip, as for animation frames). The result attributes errors to the
// first image's source.
func Arrayjoin(ctx context.Context, imgs []*Image, across int) (*Image, error) {
	if err := ctxError(ctx, "vips: arrayjoin"); err != nil {
		return nil, err
	}
	if len(imgs) == 0 {
		return nil, errs.New(errs.KindInternal, "vips: arrayjoin of no images")
	}
	ps := imagePointers(imgs)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_arrayjoin(&ps[0], C.int(len(ps)), C.int(across), &out, &cerr)
	return imgs[0].op(ctx, "arrayjoin", rc, out, cerr)
}

// Linear computes img × a + b per band (one value applies to every band).
// a and b must have the same length.
func (img *Image) Linear(ctx context.Context, a, b []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: linear"); err != nil {
		return nil, err
	}
	if len(a) == 0 || len(a) != len(b) {
		return nil, errs.Newf(errs.KindInternal, "vips: linear needs equal non-empty a and b, got %d and %d",
			len(a), len(b))
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_linear(img.p, cdoubles(a), cdoubles(b), C.int(len(a)), &out, &cerr)
	return img.op(ctx, "linear", rc, out, cerr)
}

// Invert computes the photographic negative (max - pixel).
func (img *Image) Invert(ctx context.Context) (*Image, error) {
	if err := ctxError(ctx, "vips: invert"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_invert(img.p, &out, &cerr)
	return img.op(ctx, "invert", rc, out, cerr)
}

// Bandjoin appends the bands of imgs, in order, into one image.
func Bandjoin(ctx context.Context, imgs []*Image) (*Image, error) {
	if err := ctxError(ctx, "vips: bandjoin"); err != nil {
		return nil, err
	}
	if len(imgs) == 0 {
		return nil, errs.New(errs.KindInternal, "vips: bandjoin of no images")
	}
	ps := imagePointers(imgs)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_bandjoin(&ps[0], C.int(len(ps)), &out, &cerr)
	return imgs[0].op(ctx, "bandjoin", rc, out, cerr)
}

// BandjoinConst appends constant bands.
func (img *Image) BandjoinConst(ctx context.Context, c []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: bandjoin_const"); err != nil {
		return nil, err
	}
	if len(c) == 0 {
		return nil, errs.New(errs.KindInternal, "vips: bandjoin_const of no constants")
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_bandjoin_const(img.p, cdoubles(c), C.int(len(c)), &out, &cerr)
	return img.op(ctx, "bandjoin_const", rc, out, cerr)
}

// ExtractBand returns n bands starting at band.
func (img *Image) ExtractBand(ctx context.Context, band, n int) (*Image, error) {
	if err := ctxError(ctx, "vips: extract_band"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_extract_band(img.p, C.int(band), C.int(max(n, 1)), &out, &cerr)
	return img.op(ctx, "extract_band", rc, out, cerr)
}

// AddAlpha appends an opaque alpha band.
func (img *Image) AddAlpha(ctx context.Context) (*Image, error) {
	if err := ctxError(ctx, "vips: addalpha"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_addalpha(img.p, &out, &cerr)
	return img.op(ctx, "addalpha", rc, out, cerr)
}

// FindTrim returns the bounding box of the pixels that differ from
// background by more than threshold (vips_find_trim), comparing in sRGB
// with transparency flattened. background nil uses the top-left pixel.
// lineArt enables line-art mode. It computes the whole image (needs random
// access), is killed when ctx ends, and refuses multi-frame images. An
// empty rectangle means nothing but background.
func (img *Image) FindTrim(ctx context.Context, threshold float64, background []float64, lineArt bool) (image.Rectangle, error) {
	if err := ctxError(ctx, "vips: find_trim"); err != nil {
		return image.Rectangle{}, err
	}
	stop := KillOnCancel(ctx, img)
	defer stop()
	var left, top, width, height C.int
	var cerr *C.char
	rc := C.xi_find_trim(img.p, C.double(threshold), cdoubles(background), C.int(len(background)),
		cbool(lineArt), &left, &top, &width, &height, &cerr)
	if err := stepError(ctx, rc, "find_trim", cerr, img.src); err != nil {
		return image.Rectangle{}, err
	}
	return image.Rect(int(left), int(top), int(left+width), int(top+height)), nil
}

// Trim crops img to FindTrim's box. An image that is all background comes
// back unchanged (as a new node).
func (img *Image) Trim(ctx context.Context, threshold float64, background []float64, lineArt bool) (*Image, error) {
	r, err := img.FindTrim(ctx, threshold, background, lineArt)
	if err != nil {
		return nil, err
	}
	if r.Empty() {
		return img.Copy(ctx, CopyOptions{})
	}
	return img.ExtractArea(ctx, r)
}

// Similarity scales and rotates every frame by an arbitrary angle in
// degrees (clockwise), filling uncovered pixels with background (nil:
// black). Needs random access.
func (img *Image) Similarity(ctx context.Context, scale, angle float64, background []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: similarity"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_similarity(img.p, C.double(scale), C.double(angle), cdoubles(background),
		C.int(len(background)), &out, &cerr)
	return img.op(ctx, "similarity", rc, out, cerr)
}

// Affine transforms every frame by the matrix [a b; c d]. Needs random
// access.
func (img *Image) Affine(ctx context.Context, a, b, c, d float64, background []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: affine"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_affine(img.p, C.double(a), C.double(b), C.double(c), C.double(d),
		cdoubles(background), C.int(len(background)), &out, &cerr)
	return img.op(ctx, "affine", rc, out, cerr)
}

// imagePointers returns the C handles of imgs. The slice is Go memory
// holding C pointers only, which cgo may pass.
func imagePointers(imgs []*Image) []*C.VipsImage {
	ps := make([]*C.VipsImage, len(imgs))
	for i, img := range imgs {
		ps[i] = img.p
	}
	return ps
}
