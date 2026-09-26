#!/bin/sh
# Regenerates testdata/fixtures/ and testdata/ssim/ from scratch.
#
# Requirements (tools only; nothing they ship ends up in the fixtures except
# the pixels we feed them): Go 1.27, libvips >= 8.18 with heif/jxl/jp2k/uhdr
# savers, ImageMagick 7 (`magick`), and macOS `sips` for HEIC (Apple's HEVC
# encoder, so no x265/GPL encoder is involved).
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
	"$F"/tiff "$F"/bmp "$F"/ico "$F"/svg "$F"/jp2 "$F"/uhdr

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
vips jpegsave "$T/photo.png" "$F/jpeg/progressive.jpg" --Q 80 --interlace --keep none
vips jpegsave "$T/photo.png" "$F/jpeg/baseline.jpg" --Q 90 --keep none

# PNG: 8-bit RGBA, 16-bit RGB, 8-bit palette.
gen photo-alpha 128 96 "$F/png/rgba8.png"
gen photo16 96 72 "$F/png/rgb16.png"
gen palette 96 72 "$F/png/palette.png"

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

# SSIM reference set for internal/testutil/ssim.
gen ssim-refs testdata/ssim
