#!/bin/sh
# Regenerates testdata/fixtures/dng/jxl-landscape.dng (WP-103): the synthetic
# landscape.dng (gen dng, CC0) rewritten by Adobe's dng_validate with its raw
# mosaic compressed as lossless JPEG XL (DNG 1.7, Compression 52546), the
# format of Lightroom's "JPEG XL" DNG export. Run it on the host:
#
#   sh testdata/gen/jxl-dng.sh
#
# Requirements: curl, unzip and Docker with a base builder (default
# xtandard-base:dev-builder, or XI_BASE_IMAGE) for libjxl and libjpeg-turbo.
# dng_validate is built from the DNG SDK release pinned in
# docker/base/versions-gpl.sh, with docker/base/patches/dng-sdk, and only run here:
# nothing of it is committed but its output.
set -eu
cd "$(dirname "$0")/../.."

IMG=${XI_BASE_IMAGE:-xtandard-base:dev-builder}
F=testdata/fixtures/dng
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

# shellcheck source=../../docker/base/versions-gpl.sh
. docker/base/versions-gpl.sh
curl -fsSL -o "$T/sdk.zip" "https://download.adobe.com/pub/adobe/dng/${DNGSDK_ARCHIVE}"
sum=$( (sha256sum "$T/sdk.zip" 2>/dev/null || shasum -a 256 "$T/sdk.zip") | cut -d' ' -f1)
if [ "$sum" != "$DNGSDK_SHA256" ]; then
  echo "DNG SDK checksum mismatch: $sum" >&2
  exit 1
fi
unzip -q "$T/sdk.zip" 'dng_sdk_1_7_1/dng_sdk/source/*' -d "$T"
cp "$F/landscape.dng" "$T/"

docker run --rm -v "$T:/t" -v "$PWD/docker/base/patches/dng-sdk:/patches:ro" -w /t "$IMG" sh -eu -c '
cd dng_sdk_1_7_1/dng_sdk
for p in /patches/*.patch; do git apply "$p"; done
mkdir -p /t/obj
for src in source/*.cpp; do
  case "$src" in */dng_xmp_sdk.cpp | */dng_update_meta.cpp) continue ;; esac
  echo "$src"
done | xargs -P"$(nproc)" -I{} sh -c "g++ -O1 -w -std=c++17 -DqLinux=1 -DqDNGThreadSafe=1 \
  -DqDNGUseXMP=0 -DqDNGXMPFiles=0 -DqDNGXMPDocOps=0 -DqDNGUseLibJPEG=1 -DqDNGValidateTarget=1 \
  -DNDEBUG -I/opt/xtandard/include -c {} -o /t/obj/\$(basename {} .cpp).o"
g++ -o /t/dng_validate /t/obj/*.o -L/opt/xtandard/lib -Wl,-rpath,/opt/xtandard/lib \
  -ljxl -ljxl_threads -ljpeg -lz -lpthread
cd /t
./dng_validate -losslessJXL -dng /t/jxl-landscape /t/landscape.dng
'
cp "$T/jxl-landscape.dng" "$F/jxl-landscape.dng"
echo "wrote $F/jxl-landscape.dng"
