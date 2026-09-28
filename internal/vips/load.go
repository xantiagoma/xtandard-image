package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// sourceBlob validates buf for a load.
func sourceBlob(buf *Buffer) (*C.VipsBlob, error) {
	if buf == nil || buf.blob == nil || buf.Len() == 0 {
		return nil, errs.New(errs.KindUnsupported, "vips: empty source")
	}
	return buf.blob, nil
}

// loadError is opError for a loader run over buf: a load failure on a PDF
// whose trailer carries /Encrypt is reported as a password-protected
// document. PDFium failing is the confirmation (an owner-password-only file
// opens with the empty user password and never gets here); the trailer check
// replaces matching PDFium's text in libvips' process-global error buffer,
// which concurrent requests share.
//
// With a password (Buffer.WithPassword) the same failure means the password
// does not open the document: KindUnsupported with the public message
// "wrong document password". Neither message ever contains the password.
func loadError(rc C.int, op string, cerr *C.char, src *C.VipsBlob, buf *Buffer) error {
	err := opError(rc, op, cerr, src)
	if rc != C.XI_ERR_LOAD || !pdfEncrypted(buf.Bytes()) {
		return err
	}
	var msg string
	if oe, ok := errors.AsType[*OpError](err); ok {
		msg = oe.Msg
	}
	if buf.HasPassword() {
		return errs.WithPublic(errs.Wrap(errs.KindUnsupported,
			fmt.Errorf("%w: %w", errs.ErrWrongPassword, &OpError{Op: op, Msg: msg})), "wrong document password")
	}
	return errs.WithPublic(errs.Wrap(errs.KindUnsupported, &OpError{Op: op, Msg: msg}),
		"password-protected documents are not supported")
}

// Probe reads the header of buf without decoding pixels: the loader runs
// with sequential access and nothing is ever requested from it. Errors:
// KindUnsupported (no enabled loader matches), KindCorrupt (the header is
// unreadable), KindCanceled/KindTimeout (ctx done before the call).
func Probe(ctx context.Context, buf *Buffer) (Header, error) {
	return ProbeDensity(ctx, buf, 0)
}

// ProbeDensity is Probe with vector pages (SVG, PDF) measured at dpi
// (≤ 0 keeps the loader default, 72). A PDF that needs a password fails with
// KindUnsupported and a public message saying so.
func ProbeDensity(ctx context.Context, buf *Buffer, dpi float64) (Header, error) {
	if err := ctxError(ctx, "vips: probe"); err != nil {
		return Header{}, err
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return Header{}, err
	}
	var h C.XiHeader
	var cerr *C.char
	rc := C.xi_probe(blob, C.double(dpi), buf.password, &h, &cerr)
	if err := loadError(rc, "probe", cerr, nil, buf); err != nil {
		return Header{}, err
	}
	out := Header{
		Loader:         C.GoString(&h.loader[0]),
		Width:          int(h.width),
		Height:         int(h.height),
		Bands:          int(h.bands),
		BandFormat:     BandFormat(h.band_format),
		Interpretation: Interpretation(h.interpretation),
		Pages:          max(int(h.n_pages), 1),
		PageHeight:     int(h.height),
		Orientation:    int(h.orientation),
		HasAlpha:       h.has_alpha != 0,
		HasICC:         h.has_icc != 0,
		BitDepth:       int(h.bits),
		Loop:           int(h.loop),
	}
	out.Subformat = C.GoString(&h.subformat[0])
	out.Format = formatOfLoader(out.Loader, out.Subformat)
	if h.delay != nil {
		for _, d := range unsafe.Slice(h.delay, int(h.n_delay)) {
			out.Delay = append(out.Delay, int(d))
		}
		C.xi_free(unsafe.Pointer(h.delay))
	}
	out.IsAnimated = out.Pages > 1 && len(out.Delay) > 0
	return out, nil
}

// Load decodes buf with the loader libvips detects, applying the options
// that loader supports (see LoadOptions; nil is the zero value). Pixels are decoded lazily by the
// sink; the image holds its own reference to buf's memory, so buf may be
// released once Load returns.
//
// Errors: KindUnsupported (no enabled loader), KindCorrupt (the decoder
// failed), KindLimitExceeded (MaxPixels/MaxFrames), KindCanceled/KindTimeout.
func Load(ctx context.Context, buf *Buffer, opts *LoadOptions) (*Image, error) {
	if err := ctxError(ctx, "vips: load"); err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &LoadOptions{}
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return nil, err
	}
	o := loadOptions(opts)
	o.password = buf.password
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_load(blob, &o, &out, &cerr)
	if err := loadError(rc, "load", cerr, blob, buf); err != nil {
		return nil, err
	}
	return newImage(out, blob), nil
}

// loadWith is Load with the loader named (a libvips type name such as
// "VipsForeignLoadMagick7Buffer") instead of detected. A blocked loader
// still fails with KindUnsupported, but ImageMagick is not restricted to
// PSD here: tests use it to check ImageMagick's own policy.
func loadWith(ctx context.Context, loader string, buf *Buffer, opts *LoadOptions) (*Image, error) {
	if err := ctxError(ctx, "vips: load"); err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &LoadOptions{}
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return nil, err
	}
	if loader == "" {
		return nil, errs.New(errs.KindUnsupported, "vips: no loader named")
	}
	o := loadOptions(opts)
	o.password = buf.password
	name := cstr(loader)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_load_with(cptr(name), blob, &o, &out, &cerr)
	if err := loadError(rc, "load", cerr, blob, buf); err != nil {
		return nil, err
	}
	return newImage(out, blob), nil
}

// LoadMagickCoder decodes buf with ImageMagick's coder c (PICT, Photo CD;
// WP-108). libvips' buffer loader would let ImageMagick guess the coder from
// the first bytes, and PICT's 512-byte header looks like TGA to it, so the
// shim checks c's signature, spools buf to a memfd and opens it as
// "PICT:<path>". ImageMagick decodes the whole file in this call; opts
// applies like for Load (MaxPixels is checked on the decoded size).
//
// Errors: KindUnsupported (not that format, or no ImageMagick), KindCorrupt
// (the coder failed, including its policy.xml resource limits),
// KindLimitExceeded, KindCanceled/KindTimeout.
func LoadMagickCoder(ctx context.Context, buf *Buffer, c MagickCoder, opts *LoadOptions) (*Image, error) {
	if err := ctxError(ctx, "vips: load"); err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &LoadOptions{}
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return nil, err
	}
	o := loadOptions(opts)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_load_magick_coder(C.int(c), blob, &o, &out, &cerr)
	if err := loadError(rc, "load", cerr, blob, buf); err != nil {
		return nil, err
	}
	return newImage(out, blob), nil
}

func loadOptions(opts *LoadOptions) C.XiLoadOptions {
	o := C.XiLoadOptions{
		shrink:         C.int(opts.Shrink),
		scale:          C.double(opts.Scale),
		dpi:            C.double(opts.Density),
		page:           C.int(opts.Page),
		n:              C.int(opts.NPages),
		access:         C.int(opts.Access),
		fail_on:        failOn(opts.FailOn),
		autorotate:     cbool(opts.Autorotate),
		heif_thumbnail: cbool(opts.HEIFThumbnail),
		max_pixels:     C.longlong(opts.MaxPixels),
		max_frames:     C.int(opts.MaxFrames),
	}
	for i, v := range opts.Background[:min(len(opts.Background), len(o.background))] {
		o.background[i] = C.double(v)
		o.n_background = C.int(i + 1)
	}
	return o
}

// Thumbnail loads buf with shrink-on-load and resizes it in one libvips
// operation (vips_thumbnail_buffer), the fastest path for plain resizes.
// Loader options (Page, NPages) are passed to whichever loader matches.
// Errors are those of Load.
func Thumbnail(ctx context.Context, buf *Buffer, opts *ThumbnailOptions) (*Image, error) {
	if err := ctxError(ctx, "vips: thumbnail"); err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &ThumbnailOptions{}
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return nil, err
	}
	if opts.Width < 0 || opts.Height < 0 {
		return nil, errs.Newf(errs.KindInvalidURL, "vips: negative thumbnail size %dx%d",
			opts.Width, opts.Height)
	}
	w, h := opts.Width, opts.Height
	if w == 0 {
		w = maxDimension
	}
	if h == 0 {
		h = maxDimension
	}
	o := C.XiThumbnailOptions{
		width:     C.int(w),
		height:    C.int(h),
		size:      C.int(opts.Size),
		no_rotate: cbool(opts.NoRotate),
		crop:      C.int(opts.Crop),
		linear:    cbool(opts.Linear),
		intent:    C.int(opts.Intent),
		fail_on:   failOn(opts.FailOn),
		page:      C.int(opts.Page),
		n:         C.int(opts.NPages),
		password:  buf.password,
	}
	in, exp := cstr(opts.ImportProfile), cstr(opts.ExportProfile)
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_thumbnail(blob, &o, cptr(in), cptr(exp), &out, &cerr)
	if err := loadError(rc, "thumbnail", cerr, blob, buf); err != nil {
		return nil, err
	}
	return newImage(out, blob), nil
}

// LoadThumbnail is Thumbnail fitting inside width×height without ever
// enlarging (SizeDown), with EXIF auto-rotation and fail_on=warning. A zero
// width or height means unbounded on that axis.
func LoadThumbnail(ctx context.Context, buf *Buffer, width, height int) (*Image, error) {
	return Thumbnail(ctx, buf, &ThumbnailOptions{Width: width, Height: height, Size: SizeDown})
}

func failOn(f FailOn) C.int {
	switch f {
	case FailOnError:
		return C.VIPS_FAIL_ON_ERROR
	case FailOnTruncated:
		return C.VIPS_FAIL_ON_TRUNCATED
	case FailOnNone:
		return C.VIPS_FAIL_ON_NONE
	default:
		return C.VIPS_FAIL_ON_WARNING
	}
}
