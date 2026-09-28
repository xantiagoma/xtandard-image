package vips

/*
#cgo pkg-config: xibasisu libzstd
#cgo noescape xi_basisu_ktx2_info
#cgo nocallback xi_basisu_ktx2_info
#cgo noescape xi_basisu_ktx2_decode
#cgo nocallback xi_basisu_ktx2_decode
#cgo noescape xi_zstd_decompress
#cgo nocallback xi_zstd_decompress
#include <stdlib.h>
#include "shim.h"
#include <xibasisu.h>
*/
import "C"

import (
	"context"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// KTX2 textures (WP-108) are parsed in Go (codec/ktx2); the two things that
// need C are here: the Basis Universal transcoder, for payloads only it can
// decode (ETC1S/BasisLZ, UASTC, ASTC, XUASTC, XUBC7), and Zstandard, for
// supercompressed levels of the other formats. Neither keeps a pointer to
// the input after the call, which may be Go memory.

// basisError maps the wrapper's status codes.
func basisError(rc C.int, op string) error {
	switch rc {
	case C.XI_BASISU_OK:
		return nil
	case C.XI_BASISU_UNSUPPORTED:
		return errs.WithPublic(errs.Newf(errs.KindUnsupported, "vips: %s: not a Basis Universal KTX2 payload", op),
			"this KTX2 texture format is not supported")
	case C.XI_BASISU_LIMIT:
		return errs.Newf(errs.KindLimitExceeded, "vips: %s: image exceeds the pixel limit", op)
	case C.XI_BASISU_NOMEM:
		return errs.Newf(errs.KindCorrupt, "vips: %s: allocation failed (corrupt or hostile texture)", op)
	default:
		return errs.Newf(errs.KindCorrupt, "vips: %s: the Basis Universal transcoder refused the texture", op)
	}
}

func basisInfo(ci *C.xi_basisu_info) BasisInfo {
	return BasisInfo{
		Width:  int(ci.width),
		Height: int(ci.height),
		Alpha:  ci.has_alpha != 0,
		HDR:    ci.hdr != 0,
		Format: int(ci.format),
	}
}

// BasisKTX2Info reads the header of a KTX2 file with a Basis Universal
// payload without transcoding anything.
func BasisKTX2Info(ctx context.Context, data []byte) (BasisInfo, error) {
	if err := ctxError(ctx, "vips: ktx2 info"); err != nil {
		return BasisInfo{}, err
	}
	if len(data) == 0 {
		return BasisInfo{}, basisError(C.XI_BASISU_UNSUPPORTED, "ktx2 info")
	}
	var ci C.xi_basisu_info
	rc := C.xi_basisu_ktx2_info(unsafe.Pointer(unsafe.SliceData(data)), C.size_t(len(data)), &ci)
	if err := basisError(rc, "ktx2 info"); err != nil {
		return BasisInfo{}, err
	}
	return basisInfo(&ci), nil
}

// DecodeBasisKTX2 transcodes the first image (level 0, layer 0, face 0) of
// a KTX2 file with a Basis Universal payload. maxPixels > 0 bounds the
// image before anything is decoded (errs.KindLimitExceeded).
func DecodeBasisKTX2(ctx context.Context, data []byte, maxPixels int64) (BasisImage, error) {
	if err := ctxError(ctx, "vips: ktx2 decode"); err != nil {
		return BasisImage{}, err
	}
	if len(data) == 0 {
		return BasisImage{}, basisError(C.XI_BASISU_UNSUPPORTED, "ktx2 decode")
	}
	var ci C.xi_basisu_info
	var out unsafe.Pointer
	var n C.size_t
	rc := C.xi_basisu_ktx2_decode(unsafe.Pointer(unsafe.SliceData(data)), C.size_t(len(data)),
		C.uint64_t(max(maxPixels, 0)), &out, &n, &ci)
	if err := basisError(rc, "ktx2 decode"); err != nil {
		return BasisImage{}, err
	}
	defer C.free(out)
	img := BasisImage{BasisInfo: basisInfo(&ci)}
	px := img.Width * img.Height
	if img.HDR {
		if int(n) != px*8 {
			return BasisImage{}, errs.Newf(errs.KindInternal, "vips: ktx2 decode: %d bytes for %dx%d", n, img.Width, img.Height)
		}
		img.RGBAHalf = make([]uint16, px*4)
		copy(img.RGBAHalf, unsafe.Slice((*uint16)(out), px*4))
		return img, nil
	}
	if int(n) != px*4 {
		return BasisImage{}, errs.Newf(errs.KindInternal, "vips: ktx2 decode: %d bytes for %dx%d", n, img.Width, img.Height)
	}
	img.RGBA8 = make([]byte, px*4)
	copy(img.RGBA8, unsafe.Slice((*byte)(out), px*4))
	return img, nil
}

// ZstdDecompress decompresses the Zstandard frame src into dst, which must
// be exactly the decompressed size (a KTX2 level's uncompressedByteLength):
// a frame that decodes to more or fewer bytes is errs.KindCorrupt.
func ZstdDecompress(dst, src []byte) error {
	if len(dst) == 0 || len(src) == 0 {
		return errs.New(errs.KindCorrupt, "vips: zstd: empty frame")
	}
	var got C.size_t
	rc := C.xi_zstd_decompress(unsafe.Pointer(unsafe.SliceData(dst)), C.size_t(len(dst)),
		unsafe.Pointer(unsafe.SliceData(src)), C.size_t(len(src)), &got)
	if rc != 0 || int(got) != len(dst) {
		return errs.Newf(errs.KindCorrupt, "vips: zstd: frame does not decode to %d bytes", len(dst))
	}
	return nil
}
