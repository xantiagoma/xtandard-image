package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Text renders o.Markup as a 4-band sRGB uchar image (black where the
// markup sets no colour, transparent around the glyphs) with the ink
// extents as its size. dpi is the resolution it was rendered at: o.DPI,
// or the one autofit chose. The image is in memory and has no source.
func Text(ctx context.Context, o *TextOptions) (img *Image, dpi int, err error) {
	if err := ctxError(ctx, "vips: text"); err != nil {
		return nil, 0, err
	}
	if o == nil || o.Markup == "" || o.Width < 0 || o.Height < 0 || o.DPI < 0 || (o.Height > 0 && o.Width == 0) {
		return nil, 0, errs.New(errs.KindInternal, "vips: invalid text options")
	}
	text, font, file := cstr(o.Markup), cstr(o.Font), cstr(o.FontFile)
	var out *C.VipsImage
	var cerr *C.char
	var fit C.int
	rc := C.xi_text(cptr(text), cptr(font), cptr(file), C.int(o.Width), C.int(o.Height), C.int(o.DPI),
		&out, &fit, &cerr)
	if err := opError(rc, "text", cerr, nil); err != nil {
		return nil, 0, err
	}
	return newImage(out, nil), int(fit), nil
}
