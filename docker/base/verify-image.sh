#!/bin/bash
# Smoke-tests a built base image pair. Used by CI and for local verification.
#
# Usage: verify-image.sh <runtime-image> [<builder-image>]

set -euo pipefail

runtime=${1:?usage: verify-image.sh <runtime-image> [<builder-image>]}
builder=${2:-}
my_dir="$(cd "$(dirname "$0")" && pwd)"

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
docker run --rm -v "$my_dir/check-licenses.sh:/usr/local/bin/check-licenses.sh:ro" "$runtime" \
  bash /usr/local/bin/check-licenses.sh /opt/xtandard/lib
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

if [ "$missing" -ne 0 ]; then
  echo "verify-image: FAILED" >&2
  exit 1
fi
echo "verify-image: OK"
