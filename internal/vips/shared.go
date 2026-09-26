package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"
	"sync"
)

// Shared is a rendered image that any number of Runs may read at the same
// time: an input decoded once per process, such as the configured watermark.
// It is a plain memory image with no metadata fields and no link to the
// pipeline that produced it, so nothing a request does to its own images
// (kill flags, progress signals, unrefs) reaches it. libvips links images
// under its global lock, so using the same input from several threads is
// safe (the operation cache relies on the same property).
//
// Share creates it inside one Run; every Run that uses it takes its own
// handle with Image. Close it once no Run can use it any more.
type Shared struct {
	p    *C.VipsImage
	once sync.Once
}

// Share renders img into a new Shared image. It is a sink: ctx
// cancellation kills it.
func Share(ctx context.Context, img *Image) (*Shared, error) {
	if err := ctxError(ctx, "vips: share"); err != nil {
		return nil, err
	}
	stop := KillOnCancel(ctx, img)
	defer stop()
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_share(img.p, &out, &cerr)
	if err := stepError(ctx, rc, "share", cerr, img.src); err != nil {
		return nil, err
	}
	return &Shared{p: out}, nil
}

// Image returns a new handle on the shared pixels for the calling Run.
// Close it inside that Run like any other image (an Arena does).
func (s *Shared) Image() *Image {
	C.xi_image_ref(s.p)
	return newImage(s.p, nil)
}

// Width returns the width in pixels.
func (s *Shared) Width() int { return int(C.xi_image_width(s.p)) }

// Height returns the height in pixels.
func (s *Shared) Height() int { return int(C.xi_image_height(s.p)) }

// Bands returns the number of bands, alpha included.
func (s *Shared) Bands() int { return int(C.xi_image_bands(s.p)) }

// HasAlpha reports whether the last band is alpha.
func (s *Shared) HasAlpha() bool { return C.xi_image_has_alpha(s.p) != 0 }

// Close drops the Shared reference; handles returned by Image stay valid
// until they are closed. It is idempotent and nil-safe.
func (s *Shared) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		C.xi_image_unref(s.p)
		s.p = nil
	})
}
