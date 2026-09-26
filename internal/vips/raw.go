package vips

/*
#cgo pkg-config: libraw_r
#cgo noescape xi_raw_probe
#cgo nocallback xi_raw_probe
#cgo noescape xi_raw_load
#cgo nocallback xi_raw_load
#cgo nocallback xi_raw_cancel
#cgo nocallback xi_raw_live
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"context"
	"time"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// ProbeRaw reads the header of a camera RAW file with LibRaw
// (libraw_open_buffer, then libraw_adjust_sizes_info_only for the output
// size): dimensions, orientation, camera metadata and the embedded
// previews. Nothing is unpacked or decoded, and no libvips memory is used.
//
// Errors: KindUnsupported (LibRaw does not know the file), KindCorrupt (the
// header is unreadable), KindLimitExceeded (LibRaw's own size limits),
// KindCanceled/KindTimeout (ctx done before the call).
func ProbeRaw(ctx context.Context, buf *Buffer) (RawHeader, error) {
	if err := ctxError(ctx, "vips: raw probe"); err != nil {
		return RawHeader{}, err
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return RawHeader{}, err
	}
	var h C.XiRawHeader
	var cerr *C.char
	rc := C.xi_raw_probe(blob, &h, &cerr)
	if err := opError(rc, "raw_probe", cerr, nil); err != nil {
		return RawHeader{}, err
	}
	out := RawHeader{
		Width:          int(h.width),
		Height:         int(h.height),
		RawWidth:       int(h.raw_width),
		RawHeight:      int(h.raw_height),
		Flip:           int(h.flip),
		Colors:         int(h.colors),
		DNG:            h.dng != 0,
		Make:           C.GoString(&h.make[0]),
		Model:          C.GoString(&h.model[0]),
		Software:       C.GoString(&h.software[0]),
		Lens:           C.GoString(&h.lens[0]),
		ISO:            float64(h.iso),
		ShutterSeconds: float64(h.shutter),
		Aperture:       float64(h.aperture),
		FocalLength:    float64(h.focal_len),
	}
	if h.year > 0 {
		out.Captured = time.Date(int(h.year), time.Month(h.month), int(h.day), int(h.hour),
			int(h.minute), int(h.second), 0, time.UTC)
	}
	for _, t := range h.thumbs[:int(h.n_thumbs)] {
		out.Thumbnails = append(out.Thumbnails, RawThumbnail{
			JPEG:   t.jpeg != 0,
			Width:  int(t.width),
			Height: int(t.height),
			Offset: int64(t.offset),
			Length: int(t.length),
		})
	}
	return out, nil
}

// rawLive counts the live LibRaw handles and demosaiced buffers, whose
// memory libvips does not track (leak tests, through export_test.go).
func rawLive() int { return int(C.xi_raw_live()) }

// RawLoad unpacks and demosaics a camera RAW file with LibRaw into an
// 8-bit sRGB image (grey for monochrome sensors) in the stored orientation:
// camera white balance, the camera's colour matrix to sRGB, no rotation
// (RawHeader.Flip tells the caller how to orient it). The pixels are
// decoded during the call; the image owns them.
//
// A cancelled ctx stops LibRaw at its next processing stage.
//
// Errors: KindUnsupported, KindCorrupt (the data cannot be decoded),
// KindLimitExceeded (MaxPixels or LibRaw's memory limit),
// KindCanceled/KindTimeout.
func RawLoad(ctx context.Context, buf *Buffer, opts *RawOptions) (*Image, error) {
	if err := ctxError(ctx, "vips: raw load"); err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &RawOptions{}
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return nil, err
	}
	// The flag lives in C memory: LibRaw's progress callback polls it on
	// this thread while the AfterFunc sets it from another.
	flag := (*C.int)(C.calloc(1, C.size_t(unsafe.Sizeof(C.int(0)))))
	if flag == nil {
		return nil, errs.New(errs.KindInternal, "vips: raw load: out of memory")
	}
	defer C.free(unsafe.Pointer(flag))
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(cancelled)
		C.xi_raw_cancel(flag)
	})
	o := C.XiRawOptions{
		half_size:  cbool(opts.HalfSize),
		max_pixels: C.longlong(opts.MaxPixels),
		cancel:     flag,
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_raw_load(blob, &o, &out, &cerr)
	// A started AfterFunc must finish before the flag is freed.
	if !stop() {
		<-cancelled
	}
	if err := stepError(ctx, rc, "raw_load", cerr, nil); err != nil {
		return nil, err
	}
	return newImage(out, nil), nil
}
