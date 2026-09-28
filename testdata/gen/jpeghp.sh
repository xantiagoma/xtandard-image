#!/bin/sh
# Regenerates the high-precision JPEG fixtures (WP-106) in
# testdata/fixtures/jpeg/ and their reference decodes in
# testdata/golden/codec/jpeg-*-djpeg.p[gp]m. Run it on the host:
#
#   sh testdata/gen/jpeghp.sh
#
# Requirements: Go, curl and Docker with a base builder (default
# xtandard-base:dev-builder, or XI_BASE_IMAGE) for libvips and libjpeg-turbo.
# libjpeg-turbo's tools (cjpeg, djpeg) are built from the release pinned in
# docker/base/versions.sh, the one the base image ships, and only run here:
# nothing of them is committed but their output. testdata/gen/jpeghp/encode.c
# (against the base's libjpeg-turbo) writes what cjpeg cannot: CMYK and
# lossless YCbCr.
#
# The pixels are the 64×48 photo at 16 bits (gen photo16) and its naive CMYK
# separation (gen cmyk-raw). The references are djpeg's decodes (PNM at the
# file's precision) of the fixtures it can decode: libjpeg-turbo's own
# reference decoder, compared with the loader's output by
# internal/vips/jpeghp_test.go.
set -eu
cd "$(dirname "$0")/../.."

IMG=${XI_BASE_IMAGE:-xtandard-base:dev-builder}
F=testdata/fixtures/jpeg
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
gen() { go run ./testdata/gen "$@"; }

# shellcheck source=../../docker/base/versions.sh
. docker/base/versions.sh
curl -fsSL -o "$T/ljt.tar.gz" \
  "https://github.com/libjpeg-turbo/libjpeg-turbo/archive/refs/tags/${LIBJPEGTURBO_VERSION}.tar.gz"
sum=$( (sha256sum "$T/ljt.tar.gz" 2>/dev/null || shasum -a 256 "$T/ljt.tar.gz") | cut -d' ' -f1)
if [ "$sum" != "$LIBJPEGTURBO_SHA256" ]; then
  echo "libjpeg-turbo checksum mismatch: $sum" >&2
  exit 1
fi

gen photo16 64 48 "$T/photo16.png"
gen cmyk-raw 64 48 opaque "$T/cmyk.raw" "$T/cmyk.icc"
cp testdata/gen/jpeghp/encode.c "$T/"

docker run --rm -v "$T:/t" -w /t -e V="$LIBJPEGTURBO_VERSION" "$IMG" sh -eu -c '
apt-get update -qq >/dev/null
apt-get install -y -qq cmake ninja-build nasm >/dev/null
tar xzf ljt.tar.gz
cmake -S "libjpeg-turbo-$V" -B b -G Ninja -DCMAKE_BUILD_TYPE=Release \
  -DENABLE_SHARED=FALSE -DENABLE_STATIC=TRUE -DWITH_TURBOJPEG=FALSE >/dev/null
ninja -C b cjpeg-static djpeg-static >/dev/null
cc -O1 -o jpeghp encode.c -I/opt/xtandard/include -L/opt/xtandard/lib -ljpeg \
  -Wl,-rpath,/opt/xtandard/lib
C=b/cjpeg-static
D=b/djpeg-static

# Masters as 16-bit PNM for cjpeg, raw for the encoder.
vips copy photo16.png photo16.ppm
vips rot photo16.png rot.png d270
vips copy rot.png rot.ppm
vips colourspace photo16.png grey16.v grey16
vips copy grey16.v grey16.pgm
vips rawsave photo16.png photo16.raw
# libvips built-in sRGB profile (compact-icc-profiles, CC0), taken out of a
# JPEG by djpeg.
vips jpegsave photo16.png srgb.jpg --profile srgb --keep icc
$D -icc srgb.icc -outfile /dev/null srgb.jpg

mkdir -p out/ref
# 12-bit DCT: colour (YCbCr 4:2:0, SOF1), grey progressive (SOF2), and the
# photo stored rotated 90 degrees CCW with the sRGB profile (EXIF
# orientation 6 is added on the host).
$C -precision 12 -quality 95 -outfile out/rgb12.jpg photo16.ppm
$C -precision 12 -quality 95 -grayscale -progressive -outfile out/grey12-progressive.jpg photo16.ppm
$C -precision 12 -quality 90 -icc srgb.icc -outfile rot12-icc.jpg rot.ppm
./jpeghp 64 48 cmyk 12 cmyk.raw cmyk.icc out/cmyk12.jpg
# Lossless (SOF3): 16-bit RGB, 10-bit grey, 8- and 6-bit RGB (cjpeg stores
# RGB), 12-bit YCbCr and 16-bit CMYK (encoder).
$C -precision 16 -lossless 1 -outfile out/lossless16.jpg photo16.ppm
$C -precision 10 -lossless 7 -outfile out/lossless10-grey.jpg grey16.pgm
$C -lossless 4 -outfile out/lossless8.jpg photo16.ppm
$C -precision 6 -lossless 1 -outfile out/lossless6.jpg photo16.ppm
./jpeghp 64 48 ycc-ll 12 photo16.raw - out/lossless12-ycc.jpg
./jpeghp 64 48 cmyk-ll 16 cmyk.raw cmyk.icc out/lossless16-cmyk.jpg
cp rot12-icc.jpg out/

# Reference decodes (djpeg cannot convert CMYK or lossless YCbCr).
$D -pnm -outfile out/ref/jpeg-rgb12-djpeg.ppm out/rgb12.jpg
$D -pnm -outfile out/ref/jpeg-grey12-progressive-djpeg.pgm out/grey12-progressive.jpg
$D -pnm -outfile out/ref/jpeg-lossless16-djpeg.ppm out/lossless16.jpg
$D -pnm -outfile out/ref/jpeg-lossless10-grey-djpeg.pgm out/lossless10-grey.jpg
$D -pnm -outfile out/ref/jpeg-lossless8-djpeg.ppm out/lossless8.jpg
$D -pnm -outfile out/ref/jpeg-lossless6-djpeg.ppm out/lossless6.jpg
'

R=testdata/golden/codec
for f in rgb12 grey12-progressive cmyk12 lossless16 lossless10-grey lossless8 lossless6 \
  lossless12-ycc lossless16-cmyk; do
  cp "$T/out/$f.jpg" "$F/$f.jpg"
done
gen exif6 "$T/out/rot12-icc.jpg" "$F/rgb12-exif6-icc.jpg"
cp "$T"/out/ref/* "$R/"
echo "wrote $F (high-precision JPEG) and $R/jpeg-*-djpeg.p[gp]m"
