#!/bin/bash
# Downloads, verifies (SHA256) and unpacks every library source listed in versions.sh.
#
# Forked from imgproxy-docker-base (MIT, Copyright (c) 2017 Sergey Alexandrovich);
# see LICENSE.imgproxy-docker-base. Changes: SHA256 verification of every tarball,
# release tarballs instead of git clones, SOURCES.md generation, and the additional
# libraries libspng, OpenJPEG, libultrahdr and jemalloc (WP-05), LibRaw, Imath,
# OpenEXR, ImageMagick and prebuilt PDFium (WP-66), jxrlib (WP-97).
#
# Environment:
#   DEPS_SRC          where sources are unpacked (default /root/deps)
#   PRINT_CHECKSUMS=1 do not verify or unpack; print NAME_SHA256=<sum> lines instead
#                     (used by update-checksums.sh)

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"

DEPS_SRC=${DEPS_SRC:-/root/deps}
PRINT_CHECKSUMS=${PRINT_CHECKSUMS:-0}
mkdir -p "$DEPS_SRC"

snake_version() {
  echo "${1//./_}"
}

minor_version() {
  echo "$1" | sed -E 's/^([0-9]+\.[0-9]+).*/\1/'
}

sha256() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# fetch VAR dir url [sha-var] [strip]
#   VAR      prefix of the VAR_VERSION / VAR_SHA256 variables in versions.sh
#   dir      directory name under $DEPS_SRC
#   url      source tarball URL
#   sha-var  checksum variable (default VAR_SHA256; per-architecture binaries)
#   strip    leading path components to strip (default 1; 0 for flat archives)
fetch() {
  local var=$1 dir=$2 url=$3
  local version_var="${var}_VERSION" sha_var="${4:-${var}_SHA256}" strip=${5:-1}
  local version=${!version_var} expected=${!sha_var:-}
  local archive
  archive="$(mktemp "${TMPDIR:-/tmp}/xi-dep.XXXXXX")"

  echo "== Download $dir $version" >&2
  curl -fsSL --retry 5 --retry-delay 2 -o "$archive" "$url"
  local actual
  actual="$(sha256 "$archive")"

  if [ "$PRINT_CHECKSUMS" = "1" ]; then
    echo "${sha_var}=${actual}"
    rm -f "$archive"
    return
  fi

  if [ "$actual" != "$expected" ]; then
    echo "SHA256 mismatch for $dir $version ($url)" >&2
    echo "  expected: ${expected:-<empty>}" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi

  rm -rf "${DEPS_SRC:?}/$dir"
  mkdir -p "$DEPS_SRC/$dir"
  tar -xf "$archive" -C "$DEPS_SRC/$dir" --strip-components="$strip"
  rm -f "$archive"
  echo "| $dir | $version | $url | \`$actual\` |" >> "$DEPS_SRC/SOURCES.rows"
}

if [ "$PRINT_CHECKSUMS" != "1" ]; then
  : > "$DEPS_SRC/SOURCES.rows"
fi

fetch ZLIB zlib \
  "https://github.com/zlib-ng/zlib-ng/archive/refs/tags/${ZLIB_VERSION}.tar.gz"
fetch BROTLI brotli \
  "https://github.com/google/brotli/archive/refs/tags/v${BROTLI_VERSION}.tar.gz"
fetch FFI ffi \
  "https://github.com/libffi/libffi/releases/download/v${FFI_VERSION}/libffi-${FFI_VERSION}.tar.gz"
fetch PCRE2 pcre2 \
  "https://github.com/PCRE2Project/pcre2/releases/download/pcre2-${PCRE2_VERSION}/pcre2-${PCRE2_VERSION}.tar.gz"
fetch GLIB glib \
  "https://download.gnome.org/sources/glib/$(minor_version "$GLIB_VERSION")/glib-${GLIB_VERSION}.tar.xz"
fetch HIGHWAY highway \
  "https://github.com/google/highway/archive/refs/tags/${HIGHWAY_VERSION}.tar.gz"
fetch QUANTIZR quantizr \
  "https://github.com/DarthSim/quantizr/archive/refs/tags/v${QUANTIZR_VERSION}.tar.gz"
fetch LIBEXPAT expat \
  "https://github.com/libexpat/libexpat/releases/download/R_$(snake_version "$LIBEXPAT_VERSION")/expat-${LIBEXPAT_VERSION}.tar.gz"
fetch LIBXML2 libxml2 \
  "https://download.gnome.org/sources/libxml2/$(minor_version "$LIBXML2_VERSION")/libxml2-${LIBXML2_VERSION}.tar.xz"
fetch LIBEXIF libexif \
  "https://github.com/libexif/libexif/archive/refs/tags/libexif-$(snake_version "$LIBEXIF_VERSION")-release.tar.gz"
fetch LCMS2 lcms2 \
  "https://github.com/mm2/Little-CMS/releases/download/lcms${LCMS2_VERSION}/lcms2-${LCMS2_VERSION}.tar.gz"
fetch LIBJPEGTURBO libjpeg-turbo \
  "https://github.com/libjpeg-turbo/libjpeg-turbo/archive/refs/tags/${LIBJPEGTURBO_VERSION}.tar.gz"
fetch LIBJXL libjxl \
  "https://github.com/libjxl/libjxl/archive/refs/tags/v${LIBJXL_VERSION}.tar.gz"
fetch LIBPNG libpng \
  "https://download.sourceforge.net/libpng/libpng-${LIBPNG_VERSION}.tar.xz"
fetch LIBSPNG libspng \
  "https://github.com/randy408/libspng/archive/refs/tags/v${LIBSPNG_VERSION}.tar.gz"
fetch LIBWEBP libwebp \
  "https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-${LIBWEBP_VERSION}.tar.gz"
fetch LIBTIFF libtiff \
  "https://download.osgeo.org/libtiff/tiff-${LIBTIFF_VERSION}.tar.gz"
fetch OPENJPEG openjpeg \
  "https://github.com/uclouvain/openjpeg/archive/refs/tags/v${OPENJPEG_VERSION}.tar.gz"
fetch LIBULTRAHDR libultrahdr \
  "https://github.com/google/libultrahdr/archive/refs/tags/v${LIBULTRAHDR_VERSION}.tar.gz"
fetch CGIF cgif \
  "https://github.com/dloebl/cgif/archive/refs/tags/v${CGIF_VERSION}.tar.gz"
fetch LIBDE265 libde265 \
  "https://github.com/strukturag/libde265/releases/download/v${LIBDE265_VERSION}/libde265-${LIBDE265_VERSION}.tar.gz"
fetch KVAZAAR kvazaar \
  "https://github.com/ultravideo/kvazaar/archive/refs/tags/v${KVAZAAR_VERSION}.tar.gz"
fetch DAV1D dav1d \
  "https://downloads.videolan.org/pub/videolan/dav1d/${DAV1D_VERSION}/dav1d-${DAV1D_VERSION}.tar.xz"
fetch AOM aom \
  "https://storage.googleapis.com/aom-releases/libaom-${AOM_VERSION}.tar.gz"
fetch LIBHEIF libheif \
  "https://github.com/strukturag/libheif/releases/download/v${LIBHEIF_VERSION}/libheif-${LIBHEIF_VERSION}.tar.gz"
fetch FREETYPE freetype \
  "https://download.sourceforge.net/freetype/freetype-${FREETYPE_VERSION}.tar.xz"
fetch FONTCONFIG fontconfig \
  "https://gitlab.freedesktop.org/api/v4/projects/890/packages/generic/fontconfig/${FONTCONFIG_VERSION}/fontconfig-${FONTCONFIG_VERSION}.tar.xz"
fetch HARFBUZZ harfbuzz \
  "https://github.com/harfbuzz/harfbuzz/releases/download/${HARFBUZZ_VERSION}/harfbuzz-${HARFBUZZ_VERSION}.tar.xz"
fetch PIXMAN pixman \
  "https://cairographics.org/releases/pixman-${PIXMAN_VERSION}.tar.gz"
fetch CAIRO cairo \
  "https://cairographics.org/releases/cairo-${CAIRO_VERSION}.tar.xz"
fetch FRIBIDI fribidi \
  "https://github.com/fribidi/fribidi/releases/download/v${FRIBIDI_VERSION}/fribidi-${FRIBIDI_VERSION}.tar.xz"
fetch PANGO pango \
  "https://download.gnome.org/sources/pango/$(minor_version "$PANGO_VERSION")/pango-${PANGO_VERSION}.tar.xz"
fetch LIBRSVG librsvg \
  "https://download.gnome.org/sources/librsvg/$(minor_version "$LIBRSVG_VERSION")/librsvg-${LIBRSVG_VERSION}.tar.xz"
fetch JEMALLOC jemalloc \
  "https://github.com/jemalloc/jemalloc/releases/download/${JEMALLOC_VERSION}/jemalloc-${JEMALLOC_VERSION}.tar.bz2"
fetch LIBRAW libraw \
  "https://www.libraw.org/data/LibRaw-${LIBRAW_VERSION}.tar.gz"
fetch IMATH imath \
  "https://github.com/AcademySoftwareFoundation/Imath/archive/refs/tags/v${IMATH_VERSION}.tar.gz"
fetch OPENEXR openexr \
  "https://github.com/AcademySoftwareFoundation/openexr/archive/refs/tags/v${OPENEXR_VERSION}.tar.gz"
fetch IMAGEMAGICK imagemagick \
  "https://github.com/ImageMagick/ImageMagick/archive/refs/tags/${IMAGEMAGICK_VERSION}.tar.gz"
fetch JXRLIB jxrlib \
  "https://github.com/4creators/jxrlib/archive/${JXRLIB_COMMIT}.tar.gz"

# PDFium is a prebuilt shared library per architecture (flat archive, no top directory).
pdfium_url() {
  echo "https://github.com/bblanchon/pdfium-binaries/releases/download/chromium%2F${PDFIUM_VERSION}/pdfium-linux-$1.tgz"
}
if [ "$PRINT_CHECKSUMS" = "1" ]; then
  fetch PDFIUM pdfium "$(pdfium_url x64)" PDFIUM_SHA256_AMD64 0
  fetch PDFIUM pdfium "$(pdfium_url arm64)" PDFIUM_SHA256_ARM64 0
else
  case "$(uname -m)" in
    x86_64) fetch PDFIUM pdfium "$(pdfium_url x64)" PDFIUM_SHA256_AMD64 0 ;;
    aarch64) fetch PDFIUM pdfium "$(pdfium_url arm64)" PDFIUM_SHA256_ARM64 0 ;;
    *) echo "No prebuilt PDFium for $(uname -m)" >&2; exit 1 ;;
  esac
fi

fetch VIPS vips \
  "https://github.com/libvips/libvips/releases/download/v${VIPS_VERSION}/vips-${VIPS_VERSION}.tar.xz"
