#!/bin/bash
# Smoke-tests a built base image pair. Used by CI and for local verification.
#
# Usage: verify-image.sh [--flavor slim|full|gpl] <runtime-image> [<builder-image>]
#
# The flavor defaults to the runtime image's dev.xtandard.base.flavor label (slim when
# absent). full runs every slim check plus the FFmpeg, ONNX Runtime and model checks.
# gpl runs the full checks with the gate in its gpl mode (--allow-gpl versions-gpl.sh,
# licence bundle, LICENSE-GPL.md), expects the libx264/libx265 encoders instead of
# refusing them, and round-trips H.264 and HEVC MP4 files through ffprobe.

set -euo pipefail

usage="usage: verify-image.sh [--flavor slim|full|gpl] <runtime-image> [<builder-image>]"
flavor=
if [ "${1:-}" = --flavor ]; then
  flavor=${2:?$usage}
  shift 2
fi
runtime=${1:?$usage}
builder=${2:-}
my_dir="$(cd "$(dirname "$0")" && pwd)"
if [ -z "$flavor" ]; then
  flavor="$(docker image inspect -f '{{ index .Config.Labels "dev.xtandard.base.flavor" }}' "$runtime" 2>/dev/null || true)"
  flavor=${flavor:-slim}
fi
case "$flavor" in
  slim | full | gpl) echo "== flavor: $flavor" ;;
  *) echo "$usage" >&2; exit 2 ;;
esac

# The licence gate runs inside the images from this checkout; gpl adds its allowlist.
gate_mounts=(-v "$my_dir/check-licenses.sh:/usr/local/bin/check-licenses.sh:ro")
gate_gpl=
if [ "$flavor" = gpl ]; then
  gate_mounts+=(-v "$my_dir/versions-gpl.sh:/usr/local/share/xtandard-gate/versions-gpl.sh:ro")
  gate_gpl="--allow-gpl /usr/local/share/xtandard-gate/versions-gpl.sh"
fi

echo "== vips --vips-config ($runtime)"
config="$(docker run --rm "$runtime" vips --vips-config)"
echo "$config"

echo "== libvips operations"
ops="$(docker run --rm "$runtime" vips -l)"
missing=0
for op in jpegload_source jpegsave_target pngload_source pngsave_target gifload_source \
  gifsave_target webpload_source webpsave_target heifload_source heifsave_target \
  jxlload_source jxlsave_target tiffload_source tiffsave_target svgload_source \
  jp2kload_source jp2ksave_target uhdrload_source uhdrsave \
  dcrawload_source pdfload_source magickload_source openexrload; do
  if ! grep -qE "^ *[A-Za-z]+ \($op\)|\b$op\b" <<< "$ops"; then
    echo "MISSING operation: $op" >&2
    missing=1
  fi
done
# Magick is load-only (magick-features=load).
if grep -qE "\bmagicksave" <<< "$ops"; then
  echo "UNEXPECTED operation: magicksave" >&2
  missing=1
fi

echo "== libvips features"
for feature in "ICC profile support with lcms2: true" "EXIF metadata support with libexif: true" \
  "quantizr: true" "cgif: true" "libhwy: true" "libuhdr: true" "libopenjp2: true" \
  "RAW load with libraw_r: true" "PDF load with pdfium: true" "EXR load with OpenEXR: true" \
  "Magick load with MagickCore: true (dynamic module: true)"; do
  if ! grep -qF "$feature" <<< "$config"; then
    echo "MISSING feature: $feature" >&2
    missing=1
  fi
done
# PDF must go through PDFium (BSD); poppler is GPL (ADR 0006).
if grep -qi poppler <<< "$config"; then
  echo "UNEXPECTED: poppler in vips --vips-config" >&2
  missing=1
fi

echo "== PNG codec (libspng, not libpng)"
if ! docker run --rm "$runtime" sh -c "grep -qa libspng.so /opt/xtandard/lib/libvips.so.42 && ! grep -qa libpng16.so /opt/xtandard/lib/libvips.so.42"; then
  echo "libvips must link libspng directly and not libpng (DT_NEEDED)" >&2
  missing=1
fi

echo "== encode/decode round trips"
docker run --rm -i "$runtime" bash -euo pipefail <<'EOF'
cd /tmp
vips black base.v 96 64 --bands 3
vips linear base.v src.v 1 128 --uchar
for ext in jpg png gif webp tif jxl avif heic jp2; do
  vips copy src.v "out.$ext"
  vips copy "out.$ext" "back-$ext.v"
  echo "ok $ext"
done
cat > in.svg <<'SVG'
<svg xmlns="http://www.w3.org/2000/svg" width="64" height="32"><rect width="64" height="32" fill="red"/><text x="4" y="20">xi</text></svg>
SVG
vips copy in.svg svg.png
echo "ok svg"

# PDF through PDFium: a one-page 72x36 pt document (PDFium rebuilds the missing xref).
printf '%s\n' '%PDF-1.4' '1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj' \
  '2 0 obj <</Type /Pages /Kids [3 0 R] /Count 1>> endobj' \
  '3 0 obj <</Type /Page /Parent 2 0 R /MediaBox [0 0 72 36]>> endobj' \
  'trailer <</Root 1 0 R>>' '%%EOF' > in.pdf
vips pdfload in.pdf pdf.v --dpi 144
test "$(vipsheader -f width pdf.v)x$(vipsheader -f height pdf.v)" = 144x72
vips copy in.pdf pdf.png
echo "ok pdf (pdfium)"
EOF

echo "== ImageMagick: PSD/PSB only (policy.xml)"
docker run --rm -i "$runtime" bash -euo pipefail <<'EOF'
cd /tmp
policy=/opt/xtandard/etc/ImageMagick-7/policy.xml
grep -q '<policy domain="coder" rights="none" pattern="\*"/>' "$policy"
grep -q '<policy domain="coder" rights="read" pattern="{PSD,PSB}"/>' "$policy"
echo "policy $policy denies every coder except PSD/PSB"

# bytes N...: writes each number as one byte.
bytes() {
  printf "$(printf '\\x%02x' "$@")"
}
# psd W H: an 8-bit RGB PSD with raw (uncompressed) planar image data on stdout.
# Header, then empty colour-mode, image-resources and layer sections, compression 0.
psd() {
  local w=$1 h=$2 c x y
  printf '8BPS'
  bytes 0 1 0 0 0 0 0 0 0 3 0 0 $((h >> 8)) $((h & 255)) 0 0 $((w >> 8)) $((w & 255)) 0 8 0 3
  bytes 0 0 0 0 0 0 0 0 0 0 0 0 0 0
  for c in 0 1 2; do
    for ((y = 0; y < h; y++)); do
      for ((x = 0; x < w; x++)); do
        bytes $(((x * 255 / w + y * 8 + c * 85) % 256))
      done
    done
  done
}
psd 24 16 > in.psd
vips magickload in.psd psd.v
test "$(vipsheader -f width psd.v)x$(vipsheader -f height psd.v)x$(vipsheader -f bands psd.v)" = 24x16x3
vips copy in.psd psd.png
echo "ok psd through magickload (and the generic loader)"

# ImageMagick has built-in GIF and PNM coders that need no delegate library; the
# policy must stop them (and everything else) from reaching the magick loader.
grep -qa RegisterGIFImage /opt/xtandard/lib/libMagickCore-7.*.so.*
vips black base.v 16 16 --bands 3
for ext in jpg png gif ppm tif webp; do
  vips copy base.v "in.$ext"
  if vips magickload "in.$ext" "magick-$ext.v" 2>/dev/null; then
    echo "magickload accepted $ext; the PSD-only policy is not in effect" >&2
    exit 1
  fi
  vips copy "in.$ext" "native-$ext.v"
  echo "ok $ext refused by magickload, still loaded by its native loader"
done
EOF

if [ -n "$builder" ]; then
  echo "== OpenEXR round trip (written in $builder, read in $runtime)"
  work="$(mktemp -d)"
  chmod 0777 "$work"
  docker run --rm -i -v "$work:/work" "$builder" bash -euo pipefail <<'EOF'
cd /tmp
cat > mkexr.c <<'C'
#include <ImfCRgbaFile.h>

int main(int argc, char **argv) {
	enum { W = 8, H = 4 };
	ImfRgba px[W * H];
	for (int i = 0; i < W * H; i++) {
		ImfFloatToHalf((float) i / (W * H), &px[i].r);
		ImfFloatToHalf(0.5f, &px[i].g);
		ImfFloatToHalf(2.0f, &px[i].b);
		ImfFloatToHalf(1.0f, &px[i].a);
	}
	ImfHeader *hdr = ImfNewHeader();
	ImfHeaderSetDataWindow(hdr, 0, 0, W - 1, H - 1);
	ImfHeaderSetDisplayWindow(hdr, 0, 0, W - 1, H - 1);
	ImfOutputFile *out = ImfOpenOutputFile(argv[1], hdr, IMF_WRITE_RGBA);
	if (!out)
		return 1;
	ImfOutputSetFrameBuffer(out, px, 1, W);
	ImfOutputWritePixels(out, H);
	ImfCloseOutputFile(out);
	ImfDeleteHeader(hdr);
	return 0;
}
C
gcc -o mkexr mkexr.c $(pkg-config --cflags --libs OpenEXR)
./mkexr /work/in.exr
chmod 0644 /work/in.exr
EOF
  docker run --rm -i -v "$work:/work:ro" "$runtime" bash -euo pipefail <<'EOF'
cd /tmp
vips openexrload /work/in.exr exr.v
test "$(vipsheader -f width exr.v)x$(vipsheader -f height exr.v)x$(vipsheader -f bands exr.v)" = 8x4x4
vips copy /work/in.exr exr.png
echo "ok exr (OpenEXR)"
EOF
  rm -rf "$work"
fi

echo "== runtime environment"
docker run --rm "$runtime" bash -euo pipefail -c '
  test "$(id -u):$(id -g)" = "65532:65532" && echo "user $(id -un) $(id -u):$(id -g)"
  grep -q libjemalloc /proc/self/maps && echo "jemalloc preloaded ($LD_PRELOAD)"
  test "$MALLOC_ARENA_MAX" = 2 && test "$VIPS_WARNING" = 0 && echo "MALLOC_ARENA_MAX=2 VIPS_WARNING=0"
  if ldd /opt/xtandard/lib/*.so* /opt/xtandard/lib/vips-modules-*/*.so /opt/xtandard/bin/vips | grep "not found"; then exit 1; fi
  echo "all shared objects resolve"
  echo "libs in /opt/xtandard/lib: $(ls /opt/xtandard/lib | wc -l)"
  test -s /opt/xtandard/share/licenses/SOURCES.md && echo "license bundle present"
'

echo "== license gate on the runtime image"
# shellcheck disable=SC2086 # $gate_gpl is empty or two words
docker run --rm "${gate_mounts[@]}" "$runtime" \
  bash /usr/local/bin/check-licenses.sh $gate_gpl /opt/xtandard/lib
docker run --rm "$runtime" bash -euo pipefail -c '
  for name in libraw imagemagick openexr imath pdfium; do
    test -n "$(ls -A "/opt/xtandard/share/licenses/$name")"
  done
  echo "licence texts present for LibRaw, ImageMagick, OpenEXR, Imath, PDFium"
'

if [ -n "$builder" ]; then
  echo "== builder toolchain ($builder)"
  docker run --rm "$builder" sh -c 'go version && golangci-lint version && task --version && pkg-config --modversion vips'
  echo "== builder cgo build"
  docker run --rm -i "$builder" bash -euo pipefail <<'EOF'
mkdir -p /tmp/cgotest && cd /tmp/cgotest
cat > go.mod <<'MOD'
module cgotest

go 1.27.0
MOD
cat > main.go <<'GO'
package main

// #cgo pkg-config: vips
// #include <vips/vips.h>
import "C"

import "fmt"

func main() {
	if C.vips_init(C.CString("cgotest")) != 0 {
		panic("vips_init failed")
	}
	fmt.Println("cgo libvips", C.GoString(C.vips_version_string()))
}
GO
go build -o cgotest . && ./cgotest
EOF
fi

if [ "$flavor" = full ] || [ "$flavor" = gpl ]; then
  echo "== $flavor: FFmpeg build configuration ($runtime)"
  docker run --rm "$runtime" bash -euo pipefail -c '
    ffmpeg -hide_banner -version | head -n 1
    ffmpeg -hide_banner -buildconf
    ffmpeg -hide_banner -L | head -n 3
  '

  echo "== $flavor: licence gate (ffmpeg -buildconf, ldd, models) on $runtime"
  docker run --rm "${gate_mounts[@]}" -e "XI_GATE_GPL=$gate_gpl" -e "XI_FLAVOR=$flavor" "$runtime" \
    bash -euo pipefail -c '
      licenses=/opt/xtandard/share/licenses
      bundle=
      [ "$XI_FLAVOR" = gpl ] && bundle="--licenses $licenses"
      # shellcheck disable=SC2086 # $XI_GATE_GPL and $bundle are empty or two words
      bash /usr/local/bin/check-licenses.sh $XI_GATE_GPL $bundle --ffmpeg /opt/xtandard/bin/ffmpeg \
        --ldd --models /usr/share/xtandard/models /opt/xtandard/lib
      bash /usr/local/bin/check-licenses.sh --ldd /usr/local/lib
      for name in ffmpeg libvpx onnxruntime models; do
        test -n "$(ls -A "$licenses/$name")"
      done
      grep -q "GNU LESSER GENERAL PUBLIC LICENSE" $licenses/ffmpeg/COPYING.LGPLv2.1
      grep -q "^## full flavor" $licenses/SOURCES.md
      echo "licence texts present for FFmpeg (LGPL-2.1), libvpx, ONNX Runtime and the models"
      if [ "$XI_FLAVOR" = gpl ]; then
        grep -q "GNU GENERAL PUBLIC LICENSE" $licenses/ffmpeg/COPYING.GPLv2
        grep -q "GNU GENERAL PUBLIC LICENSE" $licenses/x264/COPYING
        grep -q "GNU GENERAL PUBLIC LICENSE" $licenses/x265/COPYING
        grep -q "^## gpl flavor" $licenses/SOURCES.md
        grep -q "GPL-2.0-or-later" $licenses/LICENSE-GPL.md
        grep -q "^| x264 | GPL-2.0-or-later |" $licenses/LICENSE-GPL.md
        grep -q "^| x265 | GPL-2.0-or-later |" $licenses/LICENSE-GPL.md
        grep -q "^| ffmpeg | GPL-2.0-or-later |" $licenses/LICENSE-GPL.md
        grep -q "patent" $licenses/LICENSE-GPL.md
        echo "GPL texts (FFmpeg, x264, x265), SOURCES.md gpl section and LICENSE-GPL.md present"
        echo "LICENSE-GPL.md lists $(grep -c "^| [a-z]" $licenses/LICENSE-GPL.md) components"
      fi
    '

  echo "== $flavor: codecs"
  docker run --rm -i -e "XI_FLAVOR=$flavor" "$runtime" bash -euo pipefail <<'EOF'
decoders="$(ffmpeg -hide_banner -decoders)"
encoders="$(ffmpeg -hide_banner -encoders)"
for dec in h264 hevc vp8 vp9 libdav1d gif mpeg4 mjpeg png rawvideo; do
  grep -qE "^ V[^ ]* $dec " <<< "$decoders" || { echo "MISSING decoder $dec" >&2; exit 1; }
done
want="libvpx libvpx-vp9 libaom-av1 png rawvideo"
# full: no H.264/HEVC encoder of any kind (GPL x264/x265, patents: ROADMAP open questions
# 1-2). gpl: exactly x264 and x265 (ADR 0006, 2026-09-27), no other H.264/HEVC encoder.
unexpected="libx26[45]|libopenh264|h264|hevc|libkvazaar"
if [ "$XI_FLAVOR" = gpl ]; then
  want+=" libx264 libx265"
  unexpected="libx264rgb|libopenh264|h264|hevc|libkvazaar"
fi
for enc in $want; do
  grep -qE "^ V[^ ]* $enc " <<< "$encoders" || { echo "MISSING encoder $enc" >&2; exit 1; }
done
if grep -E "^ V[^ ]* ($unexpected)" <<< "$encoders"; then
  echo "UNEXPECTED H.264/HEVC encoder" >&2
  exit 1
fi
# Video only: no audio decoders or encoders.
if grep -E "^ A[^ ]* [^= ]" <<< "$decoders$encoders"; then
  echo "UNEXPECTED audio codec" >&2
  exit 1
fi
if [ "$XI_FLAVOR" = gpl ]; then
  echo "decoders: h264 hevc vp8 vp9 av1(dav1d) gif mpeg4 mjpeg png rawvideo; encoders: vp8 vp9 av1(aom) h264(x264) hevc(x265) png rawvideo; no audio"
else
  echo "decoders: h264 hevc vp8 vp9 av1(dav1d) gif mpeg4 mjpeg png rawvideo; encoders: vp8 vp9 av1(aom) png rawvideo; no H.264/HEVC/audio"
fi
EOF

  if [ "$flavor" = gpl ]; then
    echo "== gpl: raw/GIF -> H.264 MP4 (libx264) and HEVC MP4 (libx265), decode back"
    docker run --rm -i "$runtime" bash -euo pipefail <<'EOF'
cd /tmp
w=64 h=48 n=12
head -c $((w * h * 3 * n)) /dev/urandom > frames.rgb
raw=(-hide_banner -loglevel error -f rawvideo -pix_fmt rgb24 -s "${w}x${h}" -r 12 -i frames.rgb)
ffmpeg "${raw[@]}" -c:v libx264 -pix_fmt yuv420p -preset veryfast -crf 23 -movflags +faststart h264.mp4
ffmpeg "${raw[@]}" -c:v libx265 -pix_fmt yuv420p -preset veryfast -crf 28 -x265-params log-level=error \
  -tag:v hvc1 -movflags +faststart hevc.mp4
for f in h264.mp4:h264:High hevc.mp4:hevc:Main; do
  IFS=: read -r file codec profile <<< "$f"
  info="$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,profile,width,height,nb_read_frames \
    -count_frames -of default=noprint_wrappers=1:nokey=1 "$file" | tr '\n' ' ')"
  echo "$file: $info$(ffprobe -v error -show_entries format=format_name -of csv=p=0 "$file")"
  grep -q "^$codec $profile $w $h $n " <<< "$info"
  ffprobe -v error -show_entries format=format_name -of csv=p=0 "$file" | grep -q mp4
  ffmpeg -hide_banner -loglevel error -i "$file" -f rawvideo -pix_fmt rgb24 "back-$file.rgb"
  test "$(stat -c %s "back-$file.rgb")" -eq $((w * h * 3 * n))
done
ffprobe -v error -show_entries stream=codec_tag_string -of csv=p=0 hevc.mp4 | grep -qx hvc1
echo "decoded h264 (High) and hevc (Main, hvc1) back to $n frames"

# Animated source: libvips writes a GIF, FFmpeg turns it into H.264 MP4 (WP-76 path).
vips black black.v $w $h --bands 3
vips linear black.v grey.v 1 128 --uchar
vips copy grey.v in.gif
ffmpeg -hide_banner -loglevel error -i in.gif -c:v libx264 -pix_fmt yuv420p -movflags +faststart gif.mp4
ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 gif.mp4 | grep -qx h264
echo "ok gif -> h264 mp4"
EOF
  fi

  echo "== $flavor: GIF/raw -> WebM (VP8, VP9) and AV1-in-MP4, decode back, thumbnail"
  docker run --rm -i "$runtime" bash -euo pipefail <<'EOF'
cd /tmp
w=64 h=48 n=12
head -c $((w * h * 3 * n)) /dev/urandom > frames.rgb
raw=(-hide_banner -loglevel error -f rawvideo -pix_fmt rgb24 -s "${w}x${h}" -r 12 -i frames.rgb)
ffmpeg "${raw[@]}" -c:v libvpx -b:v 200k vp8.webm
ffmpeg "${raw[@]}" -c:v libvpx-vp9 -pix_fmt yuv420p -row-mt 1 -deadline realtime vp9.webm
ffmpeg "${raw[@]}" -c:v libaom-av1 -pix_fmt yuv420p -cpu-used 8 -usage realtime -movflags +faststart av1.mp4
probe() {
  ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -show_entries format=format_name \
    -of default=noprint_wrappers=1:nokey=1 "$1" | tr '\n' ' '
}
for f in vp8.webm:vp8 vp9.webm:vp9 av1.mp4:av1; do
  file=${f%%:*} codec=${f##*:}
  info="$(probe "$file")"
  echo "$file: $info"
  grep -q "^$codec $w $h " <<< "$info"
  ffmpeg -hide_banner -loglevel error -i "$file" -f rawvideo -pix_fmt rgb24 "back-$file.rgb"
  test "$(stat -c %s "back-$file.rgb")" -eq $((w * h * 3 * n))
done
ffprobe -v error -show_entries stream=codec_name -of csv=p=0 av1.mp4 | grep -qx av1
ffprobe -v error -show_entries format=format_name -of csv=p=0 av1.mp4 | grep -q mp4
echo "decoded vp8, vp9 and av1 (libdav1d) back to $n frames"

# Animated source: libvips writes a GIF, FFmpeg turns it into VP9 WebM.
vips black black.v $w $h --bands 3
vips linear black.v grey.v 1 128 --uchar
vips copy grey.v in.gif
ffmpeg -hide_banner -loglevel error -i in.gif -c:v libvpx-vp9 -pix_fmt yuv420p gif.webm
probe gif.webm | grep -q "^vp9 $w $h "
echo "ok gif -> vp9 webm"

# Thumbnail: first frame as PNG, read by libvips.
ffmpeg -hide_banner -loglevel error -i vp9.webm -frames:v 1 -vf "scale=32:-2" thumb.png
test "$(vipsheader -f width thumb.png)x$(vipsheader -f height thumb.png)" = 32x24
echo "ok thumbnail (scale filter, png encoder) read by libvips"
EOF

  echo "== $flavor: ONNX Runtime and models ($runtime)"
  docker run --rm "$runtime" bash -euo pipefail -c '
    ldconfig -p | grep -q "libonnxruntime.so.1 " && echo "ldconfig: $(ldconfig -p | grep libonnxruntime.so.1 | head -n 1 | sed "s/^[[:space:]]*//")"
    test ! -e /usr/local/lib/libonnxruntime_providers_shared.so
    test ! -e /usr/local/include/onnxruntime
    cd /usr/share/xtandard/models && sha256sum -c SHA256SUMS
    ls -l /usr/share/xtandard/models
  '

  if [ -n "$builder" ]; then
    echo "== $flavor builder: cgo against FFmpeg and ONNX Runtime, inference on both models ($builder)"
    docker run --rm -i -e "XI_FLAVOR=$flavor" "$builder" bash -euo pipefail <<'EOF'
mkdir -p /tmp/cgofull && cd /tmp/cgofull
cat > go.mod <<'MOD'
module cgofull

go 1.27.0
MOD
cat > main.go <<'GO'
package main

/*
#cgo pkg-config: libavformat libavcodec libavutil libswscale libavfilter libonnxruntime
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavfilter/avfilter.h>
#include <libavutil/avutil.h>
#include <libswscale/swscale.h>
#include <onnxruntime_c_api.h>

static int has_encoder(const char *name) { return avcodec_find_encoder_by_name(name) != NULL; }
static int has_decoder(const char *name) { return avcodec_find_decoder_by_name(name) != NULL; }
static int has_sws(void) {
	struct SwsContext *c = sws_getContext(64, 48, AV_PIX_FMT_RGB24, 32, 24, AV_PIX_FMT_YUV420P, SWS_BILINEAR, NULL, NULL, NULL);
	if (!c) return 0;
	sws_freeContext(c);
	return 1;
}

static const OrtApi *ort;

#define TRY(call) do { OrtStatus *err_ = (call); if (err_) return ort->GetErrorMessage(err_); } while (0)
static OrtEnv *env;

static const char *ort_init(void) {
	ort = OrtGetApiBase()->GetApi(ORT_API_VERSION);
	if (!ort) return "GetApi failed";
	OrtStatus *st = ort->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "verify", &env);
	return st ? ort->GetErrorMessage(st) : NULL;
}

static const char *ort_version(void) { return OrtGetApiBase()->GetVersionString(); }

// ort_run loads a model, feeds zeros to its single input (dynamic dims -> 1 / 320)
// and writes a summary of the input and output shapes to out.
static const char *ort_run(const char *path, char *out, size_t n) {
	OrtStatus *st;
	OrtSessionOptions *so;
	OrtSession *s;
	OrtAllocator *alloc;
	if ((st = ort->CreateSessionOptions(&so))) return ort->GetErrorMessage(st);
	TRY(ort->SetIntraOpNumThreads(so, 1));
	if ((st = ort->CreateSession(env, path, so, &s))) return ort->GetErrorMessage(st);
	TRY(ort->GetAllocatorWithDefaultOptions(&alloc));

	size_t nin, nout;
	TRY(ort->SessionGetInputCount(s, &nin));
	TRY(ort->SessionGetOutputCount(s, &nout));
	if (nin != 1) return "expected one input";

	OrtTypeInfo *ti;
	const OrtTensorTypeAndShapeInfo *tsi;
	int64_t dims[8];
	size_t nd;
	TRY(ort->SessionGetInputTypeInfo(s, 0, &ti));
	TRY(ort->CastTypeInfoToTensorInfo(ti, &tsi));
	TRY(ort->GetDimensionsCount(tsi, &nd));
	if (nd != 4) return "expected an NCHW input";
	TRY(ort->GetDimensions(tsi, dims, nd));
	size_t count = 1;
	for (size_t i = 0; i < nd; i++) {
		if (dims[i] <= 0) dims[i] = i == 0 ? 1 : 320;
		count *= (size_t)dims[i];
	}
	char *in_name, *out_name;
	TRY(ort->SessionGetInputName(s, 0, alloc, &in_name));
	TRY(ort->SessionGetOutputName(s, 0, alloc, &out_name));

	float *zeros = calloc(count, sizeof(float));
	OrtMemoryInfo *mi;
	OrtValue *input = NULL, *output = NULL;
	TRY(ort->CreateCpuMemoryInfo(OrtArenaAllocator, OrtMemTypeDefault, &mi));
	if ((st = ort->CreateTensorWithDataAsOrtValue(mi, zeros, count * sizeof(float), dims, nd,
			ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, &input))) return ort->GetErrorMessage(st);
	const char *in_names[] = {in_name};
	const char *out_names[] = {out_name};
	if ((st = ort->Run(s, NULL, in_names, (const OrtValue *const *)&input, 1, out_names, 1, &output)))
		return ort->GetErrorMessage(st);

	OrtTensorTypeAndShapeInfo *oti;
	int64_t odims[8];
	size_t ond;
	TRY(ort->GetTensorTypeAndShape(output, &oti));
	TRY(ort->GetDimensionsCount(oti, &ond));
	TRY(ort->GetDimensions(oti, odims, ond));
	int k = snprintf(out, n, "inputs=%zu outputs=%zu %s=[%lld,%lld,%lld,%lld] %s=[", nin, nout, in_name,
		(long long)dims[0], (long long)dims[1], (long long)dims[2], (long long)dims[3], out_name);
	for (size_t i = 0; i < ond && k < (int)n; i++)
		k += snprintf(out + k, n - k, i ? ",%lld" : "%lld", (long long)odims[i]);
	if (k < (int)n) snprintf(out + k, n - k, "]");
	free(zeros);
	return NULL;
}
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

func main() {
	fmt.Println("ffmpeg", C.GoString(C.av_version_info()), "avcodec", C.avcodec_version(), "avformat", C.avformat_version(),
		"avfilter", C.avfilter_version())
	license := C.GoString(C.avcodec_license())
	fmt.Println("license", license)
	encoders := []string{"libvpx", "libvpx-vp9", "libaom-av1"}
	wantLicense := "LGPL version 2.1 or later"
	if os.Getenv("XI_FLAVOR") == "gpl" {
		encoders = append(encoders, "libx264", "libx265")
		wantLicense = "GPL version 2 or later"
	}
	if license != wantLicense {
		panic("avcodec_license() = " + license + ", want " + wantLicense)
	}
	for _, name := range encoders {
		cs := C.CString(name)
		if C.has_encoder(cs) == 0 {
			panic("missing encoder " + name)
		}
		C.free(unsafe.Pointer(cs))
	}
	for _, name := range []string{"h264", "hevc", "vp8", "vp9", "libdav1d"} {
		cs := C.CString(name)
		if C.has_decoder(cs) == 0 {
			panic("missing decoder " + name)
		}
		C.free(unsafe.Pointer(cs))
	}
	if C.has_sws() == 0 {
		panic("sws_getContext failed")
	}
	fmt.Println("encoders", encoders, "decoders h264/hevc/vp8/vp9/dav1d, swscale: ok")

	if msg := C.ort_init(); msg != nil {
		panic(C.GoString(msg))
	}
	fmt.Println("onnxruntime", C.GoString(C.ort_version()))
	buf := (*C.char)(C.malloc(512))
	defer C.free(unsafe.Pointer(buf))
	for _, model := range os.Args[1:] {
		cs := C.CString(model)
		if msg := C.ort_run(cs, buf, 512); msg != nil {
			panic(model + ": " + C.GoString(msg))
		}
		C.free(unsafe.Pointer(cs))
		fmt.Println(model, C.GoString(buf))
	}
}
GO
go build -o cgofull . && ./cgofull /usr/share/xtandard/models/*.onnx
EOF
  fi
fi

if [ "$missing" -ne 0 ]; then
  echo "verify-image: FAILED" >&2
  exit 1
fi
echo "verify-image: OK ($flavor)"
