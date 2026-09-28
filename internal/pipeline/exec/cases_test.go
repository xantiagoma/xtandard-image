//go:build cgo

package exec_test

import "github.com/xantiagoma/xtandard-image/internal/transform"

// goldenCase is one processing request checked against a golden PNG in
// testdata/golden/exec/<name>.png (and, with XI_IMGPROXY_URL, against
// imgproxy's output for the same URL).
type goldenCase struct {
	name    string
	fixture string // testdata/fixtures/<fixture>
	options string // imgproxy processing options
	out     transform.Format
	// w, h are the expected size of one output frame; frames the expected
	// frame count (0 = 1).
	w, h, frames int
	// tolerance overrides golden.DefaultTolerance.
	tolerance float64
	// noImgproxy: open-source imgproxy has no equivalent (chained
	// pipelines are imgproxy Pro).
	noImgproxy bool
}

const (
	jpegPhoto = "jpeg/baseline.jpg"  // 320x240
	pngAlpha  = "png/rgba8.png"      // 128x96, graded alpha
	webpPhoto = "webp/lossy.webp"    // 320x240
	gifAnim   = "gif/animated.gif"   // 96x72, 6 frames
	webpAnim  = "webp/animated.webp" // 6 frames, alpha
	exif6     = "jpeg/exif-orientation-6-icc.jpg"
)

var goldenCases = []goldenCase{
	// Resizing types on JPEG, PNG with alpha and WebP.
	{name: "jpeg-fit", fixture: jpegPhoto, options: "rs:fit:200:200", out: transform.FormatPNG, w: 200, h: 150},
	{name: "jpeg-fill", fixture: jpegPhoto, options: "rs:fill:150:150", out: transform.FormatPNG, w: 150, h: 150},
	{name: "jpeg-fill-down", fixture: jpegPhoto, options: "rs:fill-down:400:120", out: transform.FormatPNG, w: 320, h: 96},
	{name: "jpeg-force", fixture: jpegPhoto, options: "rs:force:120:200", out: transform.FormatPNG, w: 120, h: 200},
	{name: "jpeg-auto", fixture: jpegPhoto, options: "rs:auto:100:200", out: transform.FormatPNG, w: 100, h: 75},
	{name: "jpeg-to-jpeg", fixture: jpegPhoto, options: "rs:fit:160:160", out: transform.FormatJPEG, w: 160, h: 120},
	{name: "png-fit", fixture: pngAlpha, options: "rs:fit:64:64", out: transform.FormatPNG, w: 64, h: 48},
	{name: "png-fill", fixture: pngAlpha, options: "rs:fill:60:60", out: transform.FormatPNG, w: 60, h: 60},
	{name: "png-fill-down", fixture: pngAlpha, options: "rs:fill-down:200:48", out: transform.FormatPNG, w: 128, h: 31},
	{name: "png-force", fixture: pngAlpha, options: "rs:force:50:80", out: transform.FormatPNG, w: 50, h: 80},
	{name: "png-auto", fixture: pngAlpha, options: "rs:auto:48:96", out: transform.FormatPNG, w: 48, h: 36},
	{name: "png-enlarge", fixture: pngAlpha, options: "rs:fit:256:256:1", out: transform.FormatPNG, w: 256, h: 192},
	{name: "webp-fit", fixture: webpPhoto, options: "rs:fit:200:200", out: transform.FormatPNG, w: 200, h: 150},
	{name: "webp-fill", fixture: webpPhoto, options: "rs:fill:150:150", out: transform.FormatPNG, w: 150, h: 150},
	{name: "webp-fill-down", fixture: webpPhoto, options: "rs:fill-down:400:120", out: transform.FormatPNG, w: 320, h: 96},
	{name: "webp-force", fixture: webpPhoto, options: "rs:force:120:200", out: transform.FormatPNG, w: 120, h: 200},
	{name: "webp-auto", fixture: webpPhoto, options: "rs:auto:100:200", out: transform.FormatPNG, w: 100, h: 75},
	{name: "webp-to-webp", fixture: webpPhoto, options: "rs:fit:160:160", out: transform.FormatWebP, w: 160, h: 120},

	// Shrink-on-load (JPEG 1/2) and DPR.
	{name: "jpeg-shrink-on-load", fixture: jpegPhoto, options: "rs:fit:100:0", out: transform.FormatPNG, w: 100, h: 75},
	{name: "jpeg-dpr2", fixture: jpegPhoto, options: "rs:fit:80:80/dpr:2", out: transform.FormatPNG, w: 160, h: 120},

	// Orientation, rotate, flip.
	{name: "exif6-upright", fixture: exif6, options: "rs:fit:160:160", out: transform.FormatPNG, w: 160, h: 120},
	{name: "exif6-fill-north", fixture: exif6, options: "rs:fill:100:100/g:no", out: transform.FormatPNG, w: 100, h: 100},
	{name: "rotate-90", fixture: jpegPhoto, options: "rs:fit:120:120/rot:90", out: transform.FormatPNG, w: 90, h: 120},
	{name: "flip-h", fixture: jpegPhoto, options: "rs:fit:120:120/fl:1:0", out: transform.FormatPNG, w: 120, h: 90},

	// Crops: gravity, offsets, focus point, smart, pre-crop.
	{name: "fill-south-east", fixture: jpegPhoto, options: "rs:fill:100:100/g:soea", out: transform.FormatPNG, w: 100, h: 100},
	{name: "fill-west-offset", fixture: jpegPhoto, options: "rs:fill:100:100/g:we:10:0", out: transform.FormatPNG, w: 100, h: 100},
	{name: "focus-point", fixture: jpegPhoto, options: "rs:fill:100:100/g:fp:0.8:0.3", out: transform.FormatPNG, w: 100, h: 100},
	{name: "smart-crop", fixture: jpegPhoto, options: "rs:fill:100:100/g:sm", out: transform.FormatPNG, w: 100, h: 100},
	{name: "pre-crop", fixture: jpegPhoto, options: "c:160:120:nowe/rs:fit:80:80", out: transform.FormatPNG, w: 80, h: 60},
	{name: "pre-crop-exif6", fixture: exif6, options: "c:160:120:nowe", out: transform.FormatPNG, w: 160, h: 120},

	// Extend, padding, flatten.
	{name: "extend-background", fixture: jpegPhoto, options: "rs:fit:400:400/ex:1/bg:200:30:30", out: transform.FormatPNG, w: 400, h: 400},
	{name: "extend-transparent", fixture: pngAlpha, options: "rs:fit:160:160/ex:1:so", out: transform.FormatPNG, w: 160, h: 160},
	{name: "extend-aspect-ratio", fixture: jpegPhoto, options: "rs:fit:100:100/exar:1", out: transform.FormatPNG, w: 100, h: 100},
	{name: "padding", fixture: pngAlpha, options: "rs:fit:64:64/pd:10:20/bg:0:0:255", out: transform.FormatPNG, w: 104, h: 68},
	{name: "padding-transparent", fixture: jpegPhoto, options: "rs:fit:64:64/pd:8", out: transform.FormatPNG, w: 80, h: 64},
	{name: "flatten-jpeg", fixture: pngAlpha, options: "rs:fit:96:96", out: transform.FormatJPEG, w: 96, h: 72},
	{name: "flatten-background", fixture: pngAlpha, options: "rs:fit:96:96/bg:0:128:0", out: transform.FormatPNG, w: 96, h: 72},

	// Animations keep their frames through resize and crops.
	{name: "gif-fill", fixture: gifAnim, options: "rs:fill:48:48", out: transform.FormatGIF, w: 48, h: 48, frames: 6},
	{name: "gif-crop-extend", fixture: gifAnim, options: "c:64:48/rs:fit:40:40/ex:1/bg:255:255:255", out: transform.FormatGIF, w: 40, h: 40, frames: 6},
	{name: "webp-anim-fill", fixture: webpAnim, options: "rs:fill:48:32", out: transform.FormatWebP, w: 48, h: 32, frames: 6},

	// Other loaders.
	{name: "bmp", fixture: "bmp/rgb24.bmp", options: "rs:fill:48:48", out: transform.FormatPNG, w: 48, h: 48},
	{name: "ico", fixture: "ico/png-entry.ico", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 32},
	{name: "svg-scale", fixture: "svg/logo.svg", options: "rs:fit:192:192", out: transform.FormatPNG},
	{name: "svg-enlarge", fixture: "svg/logo.svg", options: "rs:fit:192:192:1", out: transform.FormatPNG, w: 192, h: 144},
	{name: "png16", fixture: "png/rgb16.png", options: "rs:fit:48:48", out: transform.FormatPNG, w: 48, h: 36},
	{name: "heic", fixture: "heic/photo.heic", options: "rs:fit:80:80", out: transform.FormatPNG, w: 80, h: 60},

	// Long-tail inputs (WP-97): JPEG XR (jxrlib), ImageMagick's legacy
	// coders, Netpbm through ppmload and the Go WBMP and ICNS loaders.
	// imgproxy (open source) reads none of them.
	{name: "jxr", fixture: "jxr/photo.jxr", options: "rs:fill:40:40", out: transform.FormatPNG, w: 40, h: 40, noImgproxy: true},
	{name: "jxr-alpha", fixture: "jxr/alpha.jxr", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "jxr-rgb16", fixture: "jxr/rgb16.jxr", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "tga-alpha", fixture: "tga/alpha.tga", options: "rs:fill:40:40", out: transform.FormatPNG, w: 40, h: 40, noImgproxy: true},
	{name: "tga-rle", fixture: "tga/photo-rle.tga", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "pcx", fixture: "pcx/photo.pcx", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dds-dxt5", fixture: "dds/alpha-dxt5.dds", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	// WP-105: DX10 DDS through the Go BCn decoder, CIE L*a*b* TIFF and
	// CMYK JPEG XL (black extra channel); libvips 8.18 reads none of them.
	{name: "dds-bc1-dx10", fixture: "dds/bc1-dx10.dds", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dds-bc4", fixture: "dds/bc4.dds", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dds-bc7-alpha", fixture: "dds/bc7-alpha.dds", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "tiff-lab8", fixture: "tiff/lab8.tif", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "tiff-lab8-alpha", fixture: "tiff/lab8-alpha.tif", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "tiff-lab16", fixture: "tiff/lab16.tif", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "jxl-cmyk", fixture: "jxl/cmyk.jxl", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "jxl-cmyk-alpha", fixture: "jxl/cmyk-alpha.jxl", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "qoi", fixture: "qoi/alpha.qoi", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "sgi", fixture: "sgi/alpha.sgi", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "sun", fixture: "sun/photo.ras", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "xbm", fixture: "xbm/graphic.xbm", options: "rs:fit:24:24", out: transform.FormatPNG, w: 24, h: 18, noImgproxy: true},
	{name: "xpm", fixture: "xpm/graphic.xpm", options: "rs:fit:24:24", out: transform.FormatPNG, w: 24, h: 18, noImgproxy: true},
	{name: "pam", fixture: "pam/alpha.pam", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "pbm-ascii", fixture: "ppm/graphic-ascii.pbm", options: "rs:fit:24:24", out: transform.FormatPNG, w: 24, h: 18, noImgproxy: true},
	{name: "pgm-ascii", fixture: "ppm/photo-ascii.pgm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "wbmp", fixture: "wbmp/graphic.wbmp", options: "rs:fit:24:24", out: transform.FormatPNG, w: 24, h: 18, noImgproxy: true},
	{name: "icns", fixture: "icns/icon.icns", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 32, noImgproxy: true},
	{name: "icns-to-webp", fixture: "icns/icon.icns", options: "rs:fit:64:64", out: transform.FormatWebP, w: 64, h: 64, noImgproxy: true},

	// GIMP XCF (WP-107): the visible layers flattened (ImageMagick decodes
	// each layer; offsets, opacity, blend modes and groups in Go).
	{name: "xcf", fixture: "xcf/layers.xcf", options: "rs:fit:80:80", out: transform.FormatPNG, w: 80, h: 60, noImgproxy: true},
	{name: "xcf-gray", fixture: "xcf/gray.xcf", options: "rs:fit:80:80", out: transform.FormatPNG, w: 80, h: 60, noImgproxy: true},
	{name: "xcf-alpha", fixture: "xcf/alpha.xcf", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "xcf-to-jpeg", fixture: "xcf/alpha.xcf", options: "rs:fit:32:32/bg:255:255:255", out: transform.FormatJPEG, w: 32, h: 24, noImgproxy: true},

	// WP-108: KTX2 through the Go BCn/uncompressed route and the Basis
	// Universal transcoder (ETC1S, UASTC with Zstandard, UASTC HDR
	// tone-mapped), FITS stretched for display, DICOM windowed (native,
	// RLE, JPEG and JPEG 2000 frames, palette, MONOCHROME1), PICT through
	// ImageMagick's named coder.
	{name: "ktx2-bc7-alpha", fixture: "ktx2/bc7.ktx2", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "ktx2-bc6h", fixture: "ktx2/bc6h.ktx2", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "ktx2-rgba8-zstd", fixture: "ktx2/rgba8-zstd.ktx2", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "ktx2-etc1s", fixture: "ktx2/etc1s.ktx2", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "ktx2-uastc-zstd", fixture: "ktx2/uastc-zstd.ktx2", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "ktx2-uastc-hdr", fixture: "ktx2/uastc-hdr.ktx2", options: "rs:fit:48:48", out: transform.FormatPNG, w: 48, h: 32, noImgproxy: true},
	{name: "fits-u16", fixture: "fits/u16.fits", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "fits-rgb16", fixture: "fits/rgb16.fits", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "fits-rice-f32", fixture: "fits/rice-f32.fits.fz", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "fits-mef", fixture: "fits/mef-i32.fits", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dicom-ct-window", fixture: "dicom/ct-window.dcm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dicom-ct-rle", fixture: "dicom/ct-rle.dcm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dicom-ct-j2k", fixture: "dicom/ct-j2k.dcm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dicom-jpeg", fixture: "dicom/jpeg-baseline.dcm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dicom-mono1", fixture: "dicom/mono1-implicit.dcm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dicom-palette", fixture: "dicom/palette.dcm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "dicom-rgb-rle", fixture: "dicom/rgb-rle.dcm", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},
	{name: "pict", fixture: "pict/photo.pct", options: "rs:fit:32:32", out: transform.FormatPNG, w: 32, h: 24, noImgproxy: true},

	// Chained pipelines.
	{name: "chained", fixture: jpegPhoto, options: "rs:fill:100:80/-/rot:90", out: transform.FormatPNG, w: 80, h: 100, noImgproxy: true},
}
