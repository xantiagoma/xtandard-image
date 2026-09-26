package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Save encodes img as format with the matching sub-struct of o (start from
// DefaultSaveOptions; nil means exactly that). This is the sink: the whole pipeline runs here, and
// ctx cancellation kills it at the next tile. The output is a Blob of
// libvips memory, written zero-copy and then Released.
//
// FormatHEIF encodes HEVC and is refused with KindUnsupported unless
// Config.HEICEncode is on. SVG, JPEG 2000, BMP and ICO are not saved here.
//
// Errors: KindInternal (the saver failed), KindCorrupt (the source's decoder
// failed while the pipeline ran), KindUnsupported (format not encodable
// here), KindCanceled/KindTimeout.
func (img *Image) Save(ctx context.Context, format Format, o *SaveOptions) (*Blob, error) {
	if err := ctxError(ctx, "vips: save"); err != nil {
		return nil, err
	}
	if o == nil {
		def := DefaultSaveOptions()
		o = &def
	}
	stop := KillOnCancel(ctx, img)
	defer stop()

	keep := C.int(o.Keep)
	var p unsafe.Pointer
	var n C.size_t
	var cerr *C.char
	var rc C.int
	var op string
	switch format {
	case FormatJPEG:
		op = "jpegsave"
		c := C.XiJpegOptions{
			quality:             C.int(quality(o.JPEG.Quality)),
			interlace:           cbool(o.JPEG.Interlace),
			optimize_coding:     cbool(o.JPEG.OptimizeCoding),
			subsample:           C.int(o.JPEG.Subsample),
			restart_interval:    C.int(max(o.JPEG.RestartInterval, 0)),
			trellis_quant:       cbool(o.JPEG.TrellisQuant),
			overshoot_deringing: cbool(o.JPEG.OvershootDeringing),
			optimize_scans:      cbool(o.JPEG.OptimizeScans),
			quant_table:         C.int(min(max(o.JPEG.QuantTable, 0), 8)),
		}
		rc = C.xi_jpegsave(img.p, &c, keep, &p, &n, &cerr)
	case FormatPNG:
		op = "pngsave"
		c := C.XiPngOptions{
			compression: C.int(min(max(o.PNG.Compression, 0), 9)),
			interlace:   cbool(o.PNG.Interlace),
			filter:      C.int(o.PNG.Filter),
			palette:     cbool(o.PNG.Palette),
			quality:     C.int(min(max(o.PNG.Quality, 0), MaxQuality)),
			dither:      C.double(min(max(o.PNG.Dither, 0), 1)),
			bitdepth:    C.int(o.PNG.BitDepth),
			effort:      C.int(min(max(o.PNG.Effort, 1), 10)),
		}
		rc = C.xi_pngsave(img.p, &c, keep, &p, &n, &cerr)
	case FormatWebP:
		op = "webpsave"
		c := C.XiWebpOptions{
			quality:         C.int(min(max(o.WebP.Quality, 0), MaxQuality)),
			lossless:        cbool(o.WebP.Lossless),
			near_lossless:   cbool(o.WebP.NearLossless),
			effort:          C.int(min(max(o.WebP.Effort, 0), 6)),
			smart_subsample: cbool(o.WebP.SmartSubsample),
			preset:          C.int(o.WebP.Preset),
			min_size:        cbool(o.WebP.MinSize),
			kmin:            C.int(max(o.WebP.KMin, 0)),
			kmax:            C.int(max(o.WebP.KMax, 0)),
			alpha_quality:   C.int(min(max(o.WebP.AlphaQuality, 0), MaxQuality)),
			mixed:           cbool(o.WebP.Mixed),
			smart_deblock:   cbool(o.WebP.SmartDeblock),
			exact:           cbool(o.WebP.Exact),
		}
		rc = C.xi_webpsave(img.p, &c, keep, &p, &n, &cerr)
	case FormatGIF:
		op = "gifsave"
		c := C.XiGifOptions{
			dither:                C.double(min(max(o.GIF.Dither, 0), 1)),
			effort:                C.int(min(max(o.GIF.Effort, 1), 10)),
			bitdepth:              C.int(min(max(o.GIF.BitDepth, 1), 8)),
			interframe_maxerror:   C.double(min(max(o.GIF.InterframeMaxError, 0), 32)),
			reuse:                 cbool(o.GIF.Reuse),
			interpalette_maxerror: C.double(min(max(o.GIF.InterpaletteMaxError, 0), 256)),
			interlace:             cbool(o.GIF.Interlace),
			keep_duplicate_frames: cbool(o.GIF.KeepDuplicateFrames),
		}
		rc = C.xi_gifsave(img.p, &c, keep, &p, &n, &cerr)
	case FormatAVIF, FormatHEIF:
		op = "heifsave"
		compression := C.int(C.VIPS_FOREIGN_HEIF_COMPRESSION_AV1)
		if format == FormatHEIF {
			compression = C.VIPS_FOREIGN_HEIF_COMPRESSION_HEVC
		}
		c := C.XiHeifOptions{
			quality:     C.int(quality(o.HEIF.Quality)),
			lossless:    cbool(o.HEIF.Lossless),
			effort:      C.int(min(max(o.HEIF.Effort, 0), 9)),
			subsample:   C.int(o.HEIF.Subsample),
			bitdepth:    C.int(o.HEIF.BitDepth),
			compression: compression,
		}
		rc = C.xi_heifsave(img.p, &c, keep, &p, &n, &cerr)
	case FormatJXL:
		op = "jxlsave"
		c := C.XiJxlOptions{
			quality:  C.int(min(max(o.JXL.Quality, 0), MaxQuality)),
			distance: C.double(min(max(o.JXL.Distance, 0), 25)),
			effort:   C.int(min(max(o.JXL.Effort, 1), 10)),
			lossless: cbool(o.JXL.Lossless),
			tier:     C.int(min(max(o.JXL.Tier, 0), 4)),
		}
		rc = C.xi_jxlsave(img.p, &c, keep, &p, &n, &cerr)
	case FormatTIFF:
		op = "tiffsave"
		c := C.XiTiffOptions{
			quality:     C.int(quality(o.TIFF.Quality)),
			compression: C.int(o.TIFF.Compression),
			predictor:   C.int(o.TIFF.Predictor),
			lossless:    cbool(o.TIFF.Lossless),
			level:       C.int(max(o.TIFF.Level, 0)),
			bitdepth:    C.int(o.TIFF.BitDepth),
		}
		rc = C.xi_tiffsave(img.p, &c, keep, &p, &n, &cerr)
	default:
		return nil, errs.Newf(errs.KindUnsupported, "vips: cannot save %s", format)
	}
	if err := stepError(ctx, rc, op, cerr, img.src); err != nil {
		return nil, err
	}
	return newBlob(p, int(n)), nil
}

// SaveJPEG encodes img as JPEG with the given quality (clamped to 1–100),
// optimised Huffman tables and all metadata stripped.
func (img *Image) SaveJPEG(ctx context.Context, q int) (*Blob, error) {
	o := DefaultSaveOptions()
	o.JPEG.Quality = q
	o.JPEG.OptimizeCoding = true
	return img.Save(ctx, FormatJPEG, &o)
}

// quality clamps a 1..100 quality.
func quality(q int) int { return min(max(q, MinQuality), MaxQuality) }
