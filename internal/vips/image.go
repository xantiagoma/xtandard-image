package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"
	"sync/atomic"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Image is a handle on one libvips image node. Pixels are computed lazily
// when a saver pulls them. Close it exactly once, inside the same Run that
// created it.
type Image struct {
	p *C.VipsImage
}

// LoadThumbnail decodes buf with shrink-on-load (vips_thumbnail_buffer) and
// resizes it to fit inside width×height, keeping the aspect ratio and never
// enlarging. A zero width or height means unbounded on that axis; both zero
// keeps the original size. EXIF orientation is applied. Loading uses
// fail_on=warning (untrusted input).
//
// The image holds its own reference to buf's memory, so buf may be released
// once LoadThumbnail returns. Errors: KindUnsupported (no enabled loader
// matches), KindCorrupt (the decoder failed), KindCanceled/KindTimeout (ctx
// done before the call).
func LoadThumbnail(ctx context.Context, buf *Buffer, width, height int) (*Image, error) {
	if err := ctxError(ctx, "vips: thumbnail"); err != nil {
		return nil, err
	}
	if buf == nil || buf.blob == nil || buf.Len() == 0 {
		return nil, errs.New(errs.KindUnsupported, "vips: empty source")
	}
	if width < 0 || height < 0 {
		return nil, errs.Newf(errs.KindInvalidURL, "vips: negative thumbnail size %dx%d", width, height)
	}
	if width == 0 {
		width = maxDimension
	}
	if height == 0 {
		height = maxDimension
	}

	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_thumbnail(buf.blob, C.int(width), C.int(height),
		C.VIPS_SIZE_DOWN, C.VIPS_FAIL_ON_WARNING, &out, &cerr)
	if err := opError(rc, "thumbnail", cerr); err != nil {
		return nil, err
	}
	return &Image{p: out}, nil
}

// Width returns the image width in pixels.
func (img *Image) Width() int { return int(C.xi_image_width(img.p)) }

// Height returns the image height in pixels.
func (img *Image) Height() int { return int(C.xi_image_height(img.p)) }

// Close unrefs the image. It is idempotent.
func (img *Image) Close() {
	if img.p != nil {
		C.xi_image_unref(img.p)
		img.p = nil
	}
}

// SaveJPEG encodes img as JPEG with the given quality (1–100, clamped),
// optimised Huffman tables and all metadata stripped. This is the sink: the
// whole pipeline runs here. Errors: KindInternal (saver failure),
// KindCanceled/KindTimeout (ctx done before the call).
func (img *Image) SaveJPEG(ctx context.Context, quality int) (*Blob, error) {
	if err := ctxError(ctx, "vips: jpegsave"); err != nil {
		return nil, err
	}
	quality = min(max(quality, MinQuality), MaxQuality)

	var p unsafe.Pointer
	var n C.size_t
	var cerr *C.char
	rc := C.xi_jpegsave(img.p, C.int(quality), &p, &n, &cerr)
	if err := opError(rc, "jpegsave", cerr); err != nil {
		return nil, err
	}
	return &Blob{p: p, n: int(n)}, nil
}

// Blob is encoded output in C memory allocated by libvips. Bytes exposes it
// without copying, so it can be written straight to the response; Release
// frees it.
type Blob struct {
	p        unsafe.Pointer
	n        int
	released atomic.Bool
}

// Bytes returns the encoded bytes, valid until Release.
func (b *Blob) Bytes() []byte {
	if b.p == nil || b.n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(b.p), b.n)
}

// Len returns the encoded size in bytes.
func (b *Blob) Len() int { return b.n }

// Release frees the memory. It is idempotent.
func (b *Blob) Release() {
	if b.released.CompareAndSwap(false, true) {
		C.xi_free(b.p)
	}
}
