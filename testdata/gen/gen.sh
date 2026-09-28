#!/bin/sh
# Regenerates testdata/fixtures/ and testdata/ssim/ from scratch.
#
# Requirements (tools only; nothing they ship ends up in the fixtures except
# the pixels we feed them): Go 1.27, libvips >= 8.18 with heif/jxl/jp2k/uhdr
# savers, ImageMagick 7 (`magick`), qpdf (PDF encryption), and macOS `sips`
# for HEIC (Apple's HEVC encoder, so no x265/GPL encoder is involved).
#
#   sh testdata/gen/gen.sh
#
# Outputs are deterministic for a given toolchain; encoder upgrades may change
# bytes, which is fine because goldens are compared perceptually.
set -eu
cd "$(dirname "$0")/../.."

F=testdata/fixtures
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
gen() { go run ./testdata/gen "$@"; }

rm -rf "$F" testdata/ssim
mkdir -p "$F"/jpeg "$F"/png "$F"/gif "$F"/webp "$F"/avif "$F"/heic "$F"/jxl \
	"$F"/tiff "$F"/bmp "$F"/ico "$F"/svg "$F"/jp2 "$F"/uhdr "$F"/watermark \
	"$F"/pdf "$F"/psd "$F"/dng "$F"/exr "$F"/hdr "$F"/pfm "$F"/ppm

# Masters (not committed).
gen photo 320 240 "$T/photo.png"
gen photo 160 120 "$T/photo-small.png"
gen graphic 96 72 "$T/graphic.png"

# JPEG: baseline with EXIF orientation 6 + embedded sRGB ICC (libvips'
# built-in CC0 compact profile); pixels are stored rotated 90 degrees
# counter-clockwise so the displayed image is upright.
vips rot "$T/photo.png" "$T/photo-rot.png" d270
vips jpegsave "$T/photo-rot.png" "$T/rot.jpg" --Q 85 --keep icc --profile srgb
gen exif6 "$T/rot.jpg" "$F/jpeg/exif-orientation-6-icc.jpg"
# JPEG with every metadata kind for the metadata policy tests: Display P3 ICC
# (libvips' built-in CC0 compact profile), Exif with orientation 6 (pixels
# stored rotated), Artist, Copyright and GPS, XMP with GPS, IPTC copyright.
vips rot "$T/photo-small.png" "$T/small-rot.png" d270
vips icc_transform "$T/small-rot.png" "$T/p3.v" p3 --input-profile srgb
vips jpegsave "$T/p3.v" "$T/p3.jpg" --Q 85 --keep icc
gen metadata "$T/p3.jpg" "$F/jpeg/metadata-p3.jpg"
vips jpegsave "$T/photo.png" "$F/jpeg/progressive.jpg" --Q 80 --interlace --keep none
vips jpegsave "$T/photo.png" "$F/jpeg/baseline.jpg" --Q 90 --keep none

# PNG: 8-bit RGBA, 16-bit RGB, 8-bit palette.
gen photo-alpha 128 96 "$F/png/rgba8.png"
gen photo16 96 72 "$F/png/rgb16.png"
gen palette 96 72 "$F/png/palette.png"
# PNG: the small photo on white with four different margins (trim).
gen bordered 56 42 6 20 12 4 "$F/png/bordered.png"

# Watermark: 48x32 RGBA logo (XI_PROCESSING_WATERMARK_SOURCE in tests).
gen watermark "$F/watermark/logo.png"
# Watermark: 40x40 RGBA badge (per-request watermark_url in tests).
gen watermark-badge "$F/watermark/badge.png"

# GIF: static and animated (6 frames).
gen gif "$F/gif/static.gif"
gen gif-anim "$F/gif/animated.gif"

# WebP: lossy, lossless, animated.
vips webpsave "$T/photo.png" "$F/webp/lossy.webp" --Q 75
vips webpsave "$T/graphic.png" "$F/webp/lossless.webp" --lossless
vips webpsave "$F/gif/animated.gif[n=-1]" "$F/webp/animated.webp" --Q 80

# AVIF (libaom), HEIC (Apple VideoToolbox via sips), JPEG XL, JPEG 2000.
vips heifsave "$T/photo.png" "$F/avif/photo.avif" --Q 50 --compression av1
sips -s format heic -s formatOptions 60 "$T/photo.png" --out "$F/heic/photo.heic" >/dev/null
vips jxlsave "$T/photo.png" "$F/jxl/photo.jxl" --Q 75
vips jp2ksave "$T/photo-small.png" "$F/jp2/photo.jp2" --Q 60

# HEIC and AVIF with an embedded 80x60 thumbnail of a different picture
# (enforce_thumbnail). vips cannot write HEIF thumbnails: a small libheif
# program does, with kvazaar (HEVC) and libaom (AV1). Run in the base
# builder image, which has the libheif headers:
#   docker compose -f compose.dev.yml run --rm dev sh testdata/gen/gen.sh
if pkg-config --exists libheif 2>/dev/null; then
	cc -O2 -o "$T/heifthumb" testdata/gen/heifthumb/heifthumb.c $(pkg-config --cflags --libs libheif)
	gen graphic 160 120 "$T/graphic-thumb.png"
	vips rawsave "$T/photo.png" "$T/photo.raw"
	vips rawsave "$T/graphic-thumb.png" "$T/graphic-thumb.raw"
	"$T/heifthumb" hevc "$T/photo.raw" 320 240 "$T/graphic-thumb.raw" 160 120 80 "$F/heic/thumbnail.heic"
	"$T/heifthumb" av1 "$T/photo.raw" 320 240 "$T/graphic-thumb.raw" 160 120 80 "$F/avif/thumbnail.avif"
else
	echo "libheif development files missing: heic/thumbnail.heic and avif/thumbnail.avif not regenerated" >&2
fi

# TIFF: two pages, deflate.
magick "$T/photo-small.png" "$T/graphic.png" -compress zip "$F/tiff/multipage.tif"

# BMP: 24-bit uncompressed and 8-bit RLE8.
magick "$T/graphic.png" -type TrueColor BMP3:"$F/bmp/rgb24.bmp"
magick "$T/graphic.png" -colors 16 -type Palette -compress RLE BMP3:"$F/bmp/rle8.bmp"

# ICO: PNG-in-ICO (64x64) and BMP-in-ICO (32x32, 32-bit + AND mask).
gen ico-png "$F/ico/png-entry.ico"
gen ico-bmp "$F/ico/bmp-entry.ico"

# SVG: hand-written, kept in testdata/gen/logo.svg.
cp testdata/gen/logo.svg "$F/svg/logo.svg"

# UltraHDR JPEG: scRGB with highlights above 1.0 gives libultrahdr a gain map.
vips colourspace "$T/photo.png" "$T/lin.v" scrgb
vips linear "$T/lin.v" "$T/hdr.v" 2.5 0
vips uhdrsave "$T/hdr.v" "$F/uhdr/photo-ultrahdr.jpg" --Q 80

# PDF: three pages written by hand (vector, embedded photo, vector), and the
# same file encrypted with a user password by qpdf (Apache-2.0).
gen pdf "$F/pdf/pages.pdf"
qpdf --encrypt --user-password=secret --owner-password=owner --bits=256 -- \
	"$F/pdf/pages.pdf" "$F/pdf/password.pdf"

# PSD/PSB written by hand: two layers with the merged composite, PSB, and
# the PSD saved without a composite ("Maximize compatibility" off).
gen psd "$F/psd/layered.psd"
gen psb "$F/psd/psb-layered.psb"
gen psd-nocomposite "$F/psd/no-composite.psd"

# Camera RAW: synthetic DNGs (16-bit RGGB mosaic + JPEG preview in IFD0,
# linear-sRGB colour matrix, camera metadata). Landscape with a half-size
# preview; portrait (Orientation 6, stored rotated) with a quarter-size one.
gen dng 192 128 96 64 1 "$F/dng/landscape.dng"
gen dng 128 192 48 32 6 "$F/dng/portrait-orientation-6.dng"
# The landscape with a mosaic LibRaw cannot demosaic (WP-101): labelled JPEG
# XL (a placeholder decoder without the Adobe DNG SDK), and labelled deflate
# (the unpack fails). Both keep the 96x64 preview.
gen dng 192 128 96 64 1 "$F/dng/jxl-undecodable.dng" 52546
gen dng 192 128 96 64 1 "$F/dng/deflate-corrupt.dng" 8

# HDR inputs written in Go from the procedural scene in linear light (a sun
# at 40, ~5% of the pixels above 1.0; `gen hdr-stats W H -` prints the
# numbers): OpenEXR RGBA half and RGB float (uncompressed scanlines; libvips
# has no OpenEXR saver), Radiance under both magic lines, a colour PFM, and
# the photo as an 8-bit binary PPM.
gen exr-rgba-half 80 60 "$F/exr/rgba-half.exr"
gen exr-rgb-float 48 32 "$F/exr/rgb-float.exr"
gen radiance 96 64 "$F/hdr/scene.hdr"
gen rgbe 48 32 "$F/hdr/rgbe.hdr"
gen pfm 64 48 "$F/pfm/scene.pfm"
gen ppm 96 64 "$F/ppm/photo.ppm"

# WP-97 long-tail inputs from 64x48 / 48x36 masters (FORMATS.md 1.6).
gen photo 64 48 "$T/p64.png"
gen photo-alpha 64 48 "$T/pa64.png"
gen graphic 48 36 "$T/g48.png"
mkdir -p "$F"/jxr "$F"/tga "$F"/pcx "$F"/dds "$F"/qoi "$F"/sgi "$F"/sun "$F"/xbm "$F"/xpm \
	"$F"/pam "$F"/wbmp "$F"/icns
magick "$T/g48.png" -threshold 50% -compress none PBM:"$F/ppm/graphic-ascii.pbm"
magick "$T/g48.png" -threshold 50% PBM:"$F/ppm/graphic.pbm"
magick "$T/p64.png" -colorspace Gray -compress none PGM:"$F/ppm/photo-ascii.pgm"
magick "$T/p64.png" -compress none PPM:"$F/ppm/photo-ascii.ppm"
magick "$T/pa64.png" TGA:"$F/tga/alpha.tga"
magick "$T/p64.png" -compress RLE TGA:"$F/tga/photo-rle.tga"
magick "$T/p64.png" PCX:"$F/pcx/photo.pcx"
magick "$T/p64.png" -define dds:compression=dxt1 -define dds:mipmaps=0 DDS:"$F/dds/photo-dxt1.dds"
magick "$T/pa64.png" -define dds:compression=dxt5 DDS:"$F/dds/alpha-dxt5.dds"
magick "$T/pa64.png" QOI:"$F/qoi/alpha.qoi"
magick "$T/p64.png" SGI:"$F/sgi/photo.sgi"
magick "$T/pa64.png" SGI:"$F/sgi/alpha.sgi"
magick "$T/p64.png" SUN:"$F/sun/photo.ras"
magick "$T/pa64.png" PAM:"$F/pam/alpha.pam"
magick "$T/g48.png" -threshold 50% XBM:"$F/xbm/graphic.xbm"
magick "$T/g48.png" -colors 8 XPM:"$F/xpm/graphic.xpm"
magick "$T/g48.png" -threshold 50% WBMP:"$F/wbmp/graphic.wbmp"
gen icns "$F/icns/icon.icns"
# EPS/PostScript (WP-99): hand-written PostScript (testdata/gen/eps) and a DOS
# EPS binary file with a TIFF preview.
mkdir -p "$F"/eps
cp testdata/gen/eps/simple.eps testdata/gen/eps/pages.ps testdata/gen/eps/illustrator.ai "$F/eps/"
gen eps-preview "$F/eps/preview.eps"
# JPEG XR needs jxrlib's JxrEncApp, which the base image does not ship: build it
# from the pinned jxrlib source (docker/base/versions.sh JXRLIB_COMMIT, the same
# patches) and set JXRENCAPP to it; without it the JPEG XR fixtures are not
# regenerated (restore them from version control after this script).
if [ -n "${JXRENCAPP:-}" ]; then
	magick "$T/p64.png" -type TrueColor BMP3:"$T/p64.bmp"
	magick "$T/pa64.png" -compress None "$T/pa64.tif"
	magick "$T/p64.png" -depth 16 -compress None "$T/p64-16.tif"
	magick "$T/p64.png" -colorspace Gray -depth 8 "$T/p64.pgm"
	gen radiance 48 32 "$T/scene48.hdr"
	"$JXRENCAPP" -i "$T/p64.bmp" -o "$F/jxr/photo.jxr" -q 0.9
	"$JXRENCAPP" -i "$T/pa64.tif" -o "$F/jxr/alpha.jxr" -q 0.9 -a 2
	"$JXRENCAPP" -i "$T/p64-16.tif" -o "$F/jxr/rgb16.jxr" -q 0.9
	"$JXRENCAPP" -i "$T/p64.pgm" -o "$F/jxr/gray.jxr" -q 0.9
	"$JXRENCAPP" -i "$T/scene48.hdr" -o "$F/jxr/hdr.jxr" -q 0.9
else
	echo "JXRENCAPP not set: jxr/*.jxr not regenerated (restore testdata/fixtures/jxr)" >&2
fi

# Designer containers (WP-107) that no permissive sample exists for, written to
# the applications' layout by gen (testdata/gen/designer.go). GIMP XCF,
# OpenRaster and Krita files come from the applications themselves:
# testdata/gen/designer.sh (Docker) regenerates xcf/, ora/, kra/ and their
# GIMP renders.
mkdir -p "$F"/procreate "$F"/sketch "$F"/pdn "$F"/svgz
gen procreate "$F/procreate/thumbnail.procreate"
gen sketch "$F/sketch/preview.sketch"
gen pdn "$F/pdn/thumbnail.pdn"
gen svgz "$F/svg/logo.svg" "$F/svgz/logo.svgz"
if [ -z "${XI_SKIP_DESIGNER:-}" ]; then
	sh testdata/gen/designer.sh
fi

# WP-105: DX10 DDS (own BCn encoders), CIE L*a*b* TIFF (own writer) and CMYK
# JPEG XL (libjxl through gen/jxlcmyk/encode.c; needs libjxl and pkg-config).
gen dds-bc1 64 48 "$F/dds/bc1-dx10.dds"
gen dds-bc4 64 48 "$F/dds/bc4.dds"
gen dds-bc7 64 48 "$F/dds/bc7-alpha.dds"
gen dds-bc6h 48 32 "$F/dds/bc6h-scene.dds"
gen lab-tiff8 64 48 "$F/tiff/lab8.tif"
gen lab-tiff8-alpha 64 48 "$F/tiff/lab8-alpha.tif"
gen lab-tiff8-icc 64 48 "$F/tiff/lab8-icc.tif"
gen lab-tiff16 64 48 "$F/tiff/lab16.tif"
cc -O1 -o "$T/jxlcmyk" testdata/gen/jxlcmyk/encode.c $(pkg-config --cflags --libs libjxl)
gen cmyk-raw 64 48 opaque "$T/cmyk.raw" "$T/cmyk.icc"
"$T/jxlcmyk" 64 48 4 "$T/cmyk.raw" "$T/cmyk.icc" "$F/jxl/cmyk.jxl"
gen cmyk-raw 64 48 alpha "$T/cmyk-alpha.raw" "$T/cmyk.icc"
"$T/jxlcmyk" 64 48 5 "$T/cmyk-alpha.raw" "$T/cmyk.icc" "$F/jxl/cmyk-alpha.jxl"
# Reference decodes for internal/codec (Pillow, NumPy): run by hand,
# testdata/golden/codec/reference.py.

# SSIM reference set for internal/testutil/ssim.
gen ssim-refs testdata/ssim
