package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"
	"image"
	"unsafe"

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

// Colourspace converts to interpretation (no ICC; see ICCTransform), except
// that CMYK is imported with its embedded profile, else libvips' built-in
// CMYK profile, as libvips does, but with black point compensation like
// every ICC conversion of this package (ICCImport).
func (img *Image) Colourspace(ctx context.Context, interpretation Interpretation) (*Image, error) {
	if err := ctxError(ctx, "vips: colourspace"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_colourspace(img.p, C.int(interpretation), &out, &cerr)
	return img.op(ctx, "colourspace", rc, out, cerr)
}

// ICCImport converts to the PCS with the embedded profile when embedded is
// true and present, else profile (a path or built-in name such as "srgb";
// empty: libvips' default for the interpretation). The PCS is the embedded
// profile's own (XYZ or Lab, as imgproxy picks it); Lab otherwise.
//
// ICCImport, ICCExport and ICCTransform always use lcms black point
// compensation (Photoshop's "relative colorimetric + BPC" default): a
// source black above L* 0, as in press CMYK profiles, maps to the
// destination's black instead of leaving lifted, flat shadows. It changes
// nothing between profiles whose black points are both zero.
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

// ProfileLoad returns the ICC profile libvips knows by name: a built-in
// ("srgb", "sgrey", "p3", "cmyk") or a file path. "none" returns nil. The
// profile is copied into Go memory (a few hundred bytes for the built-ins).
func ProfileLoad(ctx context.Context, name string) ([]byte, error) {
	if err := ctxError(ctx, "vips: profile_load"); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errs.New(errs.KindInternal, "vips: profile_load needs a name")
	}
	n := cstr(name)
	var blob *C.VipsBlob
	var data unsafe.Pointer
	var size C.size_t
	var cerr *C.char
	rc := C.xi_profile_load(cptr(n), &blob, &data, &size, &cerr)
	if err := stepError(ctx, rc, "profile_load", cerr, nil); err != nil {
		return nil, err
	}
	if blob == nil {
		return nil, nil
	}
	defer C.xi_blob_unref(blob)
	return C.GoBytes(data, C.int(size)), nil
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

// Shrink averages hshrink×vshrink blocks of pixels into one (vips_shrink, a
// box filter; the size is rounded down). It works on the whole strip.
func (img *Image) Shrink(ctx context.Context, hshrink, vshrink int) (*Image, error) {
	if err := ctxError(ctx, "vips: shrink"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_shrink(img.p, C.int(hshrink), C.int(vshrink), &out, &cerr)
	return img.op(ctx, "shrink", rc, out, cerr)
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

// Colour operations. They keep the input's band format: float intermediate
// results are rounded to nearest and clamped to the format's range.

// CastRound converts img to format, rounding float pixels to nearest (Cast
// truncates) and clamping to the format's range.
func (img *Image) CastRound(ctx context.Context, format BandFormat) (*Image, error) {
	if err := ctxError(ctx, "vips: cast_round"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_cast_round(img.p, C.int(format), &out, &cerr)
	return img.op(ctx, "cast_round", rc, out, cerr)
}

// MapLUT maps every pixel of img through lut, a 256×1 uchar image
// (vips_maplut). A one-band lut maps every band the same way, a lut with as
// many bands as img maps each band through its own, and a one-band img
// mapped through an n-band lut gives n bands.
func (img *Image) MapLUT(ctx context.Context, lut *Image) (*Image, error) {
	if err := ctxError(ctx, "vips: maplut"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_maplut(img.p, lut.p, &out, &cerr)
	return img.op(ctx, "maplut", rc, out, cerr)
}

// Recomb multiplies every pixel, as a column vector of img's bands, by the
// matrix m of rows rows and Bands() columns in row-major order
// (vips_recomb). The result has rows bands in img's band format.
func (img *Image) Recomb(ctx context.Context, m []float64, rows int) (*Image, error) {
	if err := ctxError(ctx, "vips: recomb"); err != nil {
		return nil, err
	}
	cols := img.Bands()
	if rows <= 0 || len(m) != rows*cols {
		return nil, errs.Newf(errs.KindInternal, "vips: recomb needs a %d×%d matrix, got %d values",
			rows, cols, len(m))
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_recomb(img.p, cdoubles(m), C.int(rows), C.int(cols), &out, &cerr)
	return img.op(ctx, "recomb", rc, out, cerr)
}

// Mix blends img towards other, img × (1 − t) + other × t per band, in
// img's band format. Both images have the same size and bands.
func (img *Image) Mix(ctx context.Context, other *Image, t float64) (*Image, error) {
	if err := ctxError(ctx, "vips: mix"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_mix(img.p, other.p, C.double(t), &out, &cerr)
	return img.op(ctx, "mix", rc, out, cerr)
}

// Decode unpacks coded pixels (Radiance RGBE, LabQ) to float bands
// (vips_image_decode); an uncoded image comes back unchanged.
func (img *Image) Decode(ctx context.Context) (*Image, error) {
	if err := ctxError(ctx, "vips: decode"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_decode(img.p, &out, &cerr)
	return img.op(ctx, "decode", rc, out, cerr)
}

// Clamp limits every band to [lo, hi] (vips_clamp); the band format is kept.
func (img *Image) Clamp(ctx context.Context, lo, hi float64) (*Image, error) {
	if err := ctxError(ctx, "vips: clamp"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_clamp(img.p, C.double(lo), C.double(hi), &out, &cerr)
	return img.op(ctx, "clamp", rc, out, cerr)
}

// Multiply is img × other per pixel and band (vips_multiply); float inputs
// give float.
func (img *Image) Multiply(ctx context.Context, other *Image) (*Image, error) {
	if err := ctxError(ctx, "vips: multiply"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_multiply(img.p, other.p, &out, &cerr)
	return img.op(ctx, "multiply", rc, out, cerr)
}

// Divide is img / other per pixel and band as float (vips_divide; x / 0 is
// 0).
func (img *Image) Divide(ctx context.Context, other *Image) (*Image, error) {
	if err := ctxError(ctx, "vips: divide"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_divide(img.p, other.p, &out, &cerr)
	return img.op(ctx, "divide", rc, out, cerr)
}

// GradientMask returns a one-band uchar width×height image whose pixel
// (x, y) is round(opacity × clamp(ax·x + ay·y + b, 0, 255)): a linear ramp
// for gradient overlays.
func GradientMask(ctx context.Context, width, height int, ax, ay, b, opacity float64) (*Image, error) {
	if err := ctxError(ctx, "vips: gradient_mask"); err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 {
		return nil, errs.Newf(errs.KindInternal, "vips: gradient mask of %dx%d", width, height)
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_gradient_mask(C.int(width), C.int(height), C.double(ax), C.double(ay), C.double(b),
		C.double(opacity), &out, &cerr)
	if err := opError(rc, "gradient_mask", cerr, nil); err != nil {
		return nil, err
	}
	return newImage(out, nil), nil
}

// Conv3 convolves every band with the 3×3 row-major matrix m as it is
// (no scale or offset) in float precision, repeating the edge pixels, and
// rounds back to img's band format.
func (img *Image) Conv3(ctx context.Context, m *[9]float64) (*Image, error) {
	if err := ctxError(ctx, "vips: conv"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_conv3(img.p, cdoubles(m[:]), &out, &cerr)
	return img.op(ctx, "conv", rc, out, cerr)
}

// Unsharp is the classic unsharp mask img + amount × (img − gaussblur(img,
// sigma)) on every band, in img's band format. A negative amount blurs.
func (img *Image) Unsharp(ctx context.Context, sigma, amount float64) (*Image, error) {
	if err := ctxError(ctx, "vips: unsharp"); err != nil {
		return nil, err
	}
	if !(sigma > 0) {
		return nil, errs.Newf(errs.KindInternal, "vips: unsharp sigma %g", sigma)
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_unsharp(img.p, C.double(sigma), C.double(amount), &out, &cerr)
	return img.op(ctx, "unsharp", rc, out, cerr)
}

// Insert places sub over img with its top-left corner at (x, y), clipped to
// img. Both have the same bands; the result has img's size.
func (img *Image) Insert(ctx context.Context, sub *Image, x, y int) (*Image, error) {
	if err := ctxError(ctx, "vips: insert"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_insert(img.p, sub.p, C.int(x), C.int(y), &out, &cerr)
	return img.op(ctx, "insert", rc, out, cerr)
}

// BlendMask blends img towards over by the one-band uchar mask:
// img × (1 − m/255) + over × m/255, in over's band format. The three images
// have the same size; img and over the same bands.
func (img *Image) BlendMask(ctx context.Context, mask, over *Image) (*Image, error) {
	if err := ctxError(ctx, "vips: blend_mask"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_blend_mask(mask.p, over.p, img.p, &out, &cerr)
	return img.op(ctx, "blend_mask", rc, out, cerr)
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
