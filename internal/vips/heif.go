package vips

/*
#cgo pkg-config: libheif
#cgo LDFLAGS: -lm
#cgo nocallback xi_heif_live
#cgo noescape xi_heif_gainmap
#cgo nocallback xi_heif_gainmap
#cgo noescape xi_avif_hdr_save
#cgo nocallback xi_avif_hdr_save
#cgo noescape xi_uhdr_gainmap
#cgo nocallback xi_uhdr_gainmap
#cgo noescape xi_uhdrsave
#cgo nocallback xi_uhdrsave
#cgo noescape xi_math2_const
#cgo nocallback xi_math2_const
#include "shim.h"
*/
import "C"

import (
	"context"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// HEIF and AVIF image sequences are decoded by heif.c, a libvips loader
// class (heifseqload_buffer) over libheif's sequence API that the shim
// registers ahead of heifload (WP-104). Like the CMYK JPEG XL loader it
// needs no Go API: sequences go through Probe, Load and Thumbnail as
// FormatAVIF or FormatHEIF animations (page, n, page-height, delay, loop).

// heifLive counts the live libheif contexts and decoded buffers, whose
// memory libvips does not track (leak tests, through export_test.go).
func heifLive() int { return int(C.xi_heif_live()) }

// LoadHEIFGainMap decodes the gain map of buf's ISO 21496-1 tone-map
// derived item (tmap) whose base image is the primary image: the gain
// map's code values as 8-bit RGB (a one-channel map repeats its channel),
// and the tmap payload, which codec/gainmap parses. It returns nil, nil
// when the file has none.
//
// Errors: KindCorrupt (libheif cannot read the file or decode the gain
// map), KindCanceled/KindTimeout.
func LoadHEIFGainMap(ctx context.Context, buf *Buffer) (*Image, []byte, error) {
	if err := ctxError(ctx, "vips: heif gain map"); err != nil {
		return nil, nil, err
	}
	blob, err := sourceBlob(buf)
	if err != nil {
		return nil, nil, err
	}
	var out *C.VipsImage
	var meta unsafe.Pointer
	var n C.size_t
	var cerr *C.char
	rc := C.xi_heif_gainmap(blob, &out, &meta, &n, &cerr)
	if err := loadError(rc, "heif gain map", cerr, nil, buf); err != nil {
		return nil, nil, err
	}
	if out == nil {
		return nil, nil, nil
	}
	payload := C.GoBytes(meta, C.int(n))
	C.xi_free(meta)
	return newImage(out, blob), payload, nil
}

// UHDRGainMap returns the gain map uhdrload attached to img, decoded (one or
// three bands, 8-bit), and its metadata; ok is false when img has none.
func (img *Image) UHDRGainMap(ctx context.Context) (gm *Image, meta UHDRGainMap, ok bool, err error) {
	if err := ctxError(ctx, "vips: uhdr gain map"); err != nil {
		return nil, meta, false, err
	}
	var out *C.VipsImage
	var p C.XiUhdrGainMap
	var cerr *C.char
	rc := C.xi_uhdr_gainmap(img.p, &out, &p, &cerr)
	if rc != C.XI_OK {
		return nil, meta, false, stepError(ctx, rc, "uhdr gain map", cerr, img.src)
	}
	if out == nil {
		return nil, meta, false, nil
	}
	for i := range 3 {
		meta.MinContentBoost[i] = float64(p.min_boost[i])
		meta.MaxContentBoost[i] = float64(p.max_boost[i])
		meta.Gamma[i] = float64(p.gamma[i])
		meta.OffsetSDR[i] = float64(p.offset_sdr[i])
		meta.OffsetHDR[i] = float64(p.offset_hdr[i])
	}
	meta.CapacityMin = float64(p.capacity_min)
	meta.CapacityMax = float64(p.capacity_max)
	return img.derive(out), meta, true, nil
}

// Pow raises every band to the power e[band] (vips_math2_const pow; one
// value applies to every band, and a one-band image with three exponents
// gives three bands). The result is float.
func (img *Image) Pow(ctx context.Context, e []float64) (*Image, error) {
	return img.math2Const(ctx, "pow", C.VIPS_OPERATION_MATH2_POW, e)
}

// Exp2 is 2 raised to every sample (vips_math2_const wop), as float.
func (img *Image) Exp2(ctx context.Context) (*Image, error) {
	return img.math2Const(ctx, "exp2", C.VIPS_OPERATION_MATH2_WOP, []float64{2})
}

func (img *Image) math2Const(ctx context.Context, name string, op C.int, c []float64) (*Image, error) {
	if err := ctxError(ctx, "vips: "+name); err != nil {
		return nil, err
	}
	if len(c) == 0 {
		return nil, errs.Newf(errs.KindInternal, "vips: %s without constants", name)
	}
	cc := make([]C.double, len(c))
	for i, v := range c {
		cc[i] = C.double(v)
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_math2_const(img.p, op, &cc[0], C.int(len(cc)), &out, &cerr)
	return img.op(ctx, name, rc, out, cerr)
}

// saveHDR encodes a linear scRGB float image for the HDR outputs
// (preserve_hdr, FORMATS.md §1.4): AVIF as BT.2100 PQ with BT.2020
// primaries (xi_avif_hdr_save) and JPEG as UltraHDR, whose SDR base and gain
// map libultrahdr derives (uhdrsave). ok is false for other formats, which
// Save encodes as usual.
func (img *Image) saveHDR(format Format, o *SaveOptions) (rc C.int, op string, p unsafe.Pointer, n C.size_t, cerr *C.char, ok bool) {
	switch format {
	case FormatAVIF:
		bits := o.HEIF.BitDepth
		if bits != 10 {
			bits = 12
		}
		c := C.XiAvifHdrOptions{
			quality:  C.int(quality(o.HEIF.Quality)),
			effort:   C.int(min(max(o.HEIF.Effort, 0), 9)),
			lossless: cbool(o.HEIF.Lossless),
			bitdepth: C.int(bits),
		}
		rc = C.xi_avif_hdr_save(img.p, &c, &p, &n, &cerr)
		return rc, "avif hdr save", p, n, cerr, true
	case FormatJPEG:
		rc = C.xi_uhdrsave(img.p, C.int(quality(o.JPEG.Quality)), C.int(o.Keep), &p, &n, &cerr)
		return rc, "uhdrsave", p, n, cerr, true
	default:
		return 0, "", nil, 0, nil, false
	}
}

// isHDR reports a linear scRGB float image: what fromHDR hands the savers
// with preserve_hdr.
func (img *Image) isHDR() bool {
	return img.Interpretation() == InterpretationScRGB && img.BandFormat() == BandFormatFloat
}
