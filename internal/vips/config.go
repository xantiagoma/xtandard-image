package vips

import (
	"errors"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// ErrUnavailable is returned by every operation of a build without cgo.
var ErrUnavailable = errs.New(errs.KindInternal, "vips: built without cgo")

// Config configures Init. The zero value is not valid; start from
// DefaultConfig.
type Config struct {
	// Concurrency is vips_concurrency_set (XI_VIPS_CONCURRENCY). Keep it at 1:
	// parallelism comes from the request semaphore (ARCHITECTURE §11).
	Concurrency int
	// CacheMax, CacheMaxMem and CacheMaxFiles size the libvips operation
	// cache. All 0 (off) by default: we cache results, not operations.
	CacheMax      int
	CacheMaxMem   int64
	CacheMaxFiles int
	// Loaders lists the libvips loader base classes that stay enabled after
	// every loader is blocked. Nil means DefaultLoaders.
	Loaders []string
	// Savers lists the libvips saver base classes that stay enabled after
	// every saver is blocked. Nil means DefaultSavers.
	Savers []string
	// HEICEncode allows Save(FormatHEIF) to encode HEVC
	// (XI_FORMATS_HEIC_ENCODE, off by default). Decoding HEIC is always on.
	HEICEncode bool
}

// DefaultConfig returns concurrency 1, cache off, the M1 loader and saver
// sets and HEIC encoding off.
func DefaultConfig() Config {
	return Config{Concurrency: 1, Loaders: DefaultLoaders(), Savers: DefaultSavers()}
}

// DefaultLoaders returns the loader classes of the input formats
// (FORMATS.md): jpeg, png (spng), gif (nsgif), webp, heif/avif, jxl, tiff,
// svg, jp2k and UltraHDR (M1), then PDF (PDFium), ImageMagick, OpenEXR,
// Radiance and PPM/PFM (M2), then JPEG XR (the shim's jxrlib loader,
// WP-97), CMYK JPEG XL (the shim's libjxl loader for the files jxlload
// cannot read, WP-105), high-precision JPEG (the shim's libjpeg-turbo
// loader for 12-bit and lossless files, WP-106) and HEIF/AVIF image
// sequences (the shim's libheif loader, WP-104). The M2 loaders are
// unblocked by their buffer class only, the one the service uses, except
// OpenEXR, which has only a file loader (the shim spools the buffer for
// it). ImageMagick, OpenEXR, Radiance, PPM and the shim's loaders are
// untrusted: they are unblocked by name here; the shim refuses
// ImageMagick for anything but PSD/PSB and the legacy formats of its
// policy.xml (which registers no other coder) and the PPM loader for
// anything but a PNM or PFM header. ImageMagick's file loader is unblocked
// for the PICT and Photo CD coders the shim names explicitly (WP-108,
// xi_load_magick_coder, which checks their signatures and hands it a memfd
// spool); the service loads nothing else by file name but OpenEXR.
// Everything else, including untrusted loaders such as matload, fits or
// csv, stays blocked.
func DefaultLoaders() []string {
	return []string{
		"VipsForeignLoadJpeg",
		"VipsForeignLoadPng",
		"VipsForeignLoadNsgif",
		"VipsForeignLoadWebp",
		"VipsForeignLoadHeif",
		"VipsForeignLoadJxl",
		"VipsForeignLoadTiff",
		"VipsForeignLoadSvg",
		"VipsForeignLoadJp2k",
		"VipsForeignLoadUhdr",
		"VipsForeignLoadPdfBuffer",
		"VipsForeignLoadMagick7Buffer",
		"VipsForeignLoadMagick7File",
		"VipsForeignLoadOpenexr",
		"VipsForeignLoadRadBuffer",
		"VipsForeignLoadPpmBuffer",
		"XiForeignLoadJxrBuffer",
		"XiForeignLoadJxlCmykBuffer",
		"XiForeignLoadJpegHpBuffer",
		"XiForeignLoadHeifSeqBuffer",
	}
}

// DefaultSavers returns the saver classes behind Save: jpeg, png (spng),
// webp, gif (cgif), heif/avif, jxl and tiff, and UltraHDR for HDR JPEG
// (preserve_hdr, WP-104).
func DefaultSavers() []string {
	return []string{
		"VipsForeignSaveJpeg",
		"VipsForeignSaveSpng",
		"VipsForeignSaveWebp",
		"VipsForeignSaveCgif",
		"VipsForeignSaveHeif",
		"VipsForeignSaveJxl",
		"VipsForeignSaveTiff",
		"VipsForeignSaveUhdr",
	}
}

// Validate reports every invalid field.
func (c *Config) Validate() error {
	var list []error
	if c.Concurrency < 1 {
		list = append(list, fmt.Errorf("vips: concurrency must be >= 1, got %d", c.Concurrency))
	}
	if c.CacheMax < 0 || c.CacheMaxMem < 0 || c.CacheMaxFiles < 0 {
		list = append(list, errors.New("vips: cache limits must be >= 0"))
	}
	return errors.Join(list...)
}

// MemStats is a snapshot of libvips' tracked allocations.
type MemStats struct {
	Mem          int64 // vips_tracked_get_mem: bytes currently allocated
	MemHighwater int64 // vips_tracked_get_mem_highwater: peak of Mem
	Allocs       int64 // vips_tracked_get_allocs: live allocations
	Files        int64 // vips_tracked_get_files: open files
}

// OpError carries the libvips error text of one failed operation. It is
// always wrapped in an *errs.Error whose kind was chosen from Op; Msg is for
// logs only and never reaches clients.
type OpError struct {
	Op  string // "init", "load", "thumbnail", "resize", "jpegsave", "alloc", ...
	Msg string // copy of the vips error buffer at the failure site
}

func (e *OpError) Error() string {
	if e.Msg == "" {
		return "vips " + e.Op + " failed"
	}
	return "vips " + e.Op + ": " + e.Msg
}

// Quality bounds of the savers.
const (
	MinQuality = 1
	MaxQuality = 100
)

// maxDimension stands in for "unbounded" in thumbnail width or height
// (VIPS_MAX_COORD).
const maxDimension = 10_000_000
