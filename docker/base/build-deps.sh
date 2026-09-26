#!/bin/bash
# Builds every library from versions.sh into /opt/xtandard, collects their licenses
# and runs the license gate.
#
# Forked from imgproxy-docker-base (MIT, Copyright (c) 2017 Sergey Alexandrovich);
# see LICENSE.imgproxy-docker-base. Changes: /opt/xtandard prefix; added libspng,
# OpenJPEG, libultrahdr and jemalloc; explicit feature switches so optional GPL
# codecs (jbig, x265) can never be picked up by auto-detection; license bundle and
# SOURCES.md; license gate.

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"

print_build_stage() {
  echo ""
  echo "== Build $1 $2 ====================================="
  echo ""
}

DEPS_SRC=${DEPS_SRC:-/root/deps}
TARGET_PATH=${XI_PREFIX:-/opt/xtandard}
mkdir -p "$TARGET_PATH"

export CARGO_PROFILE_RELEASE_CODEGEN_UNITS=1
export CARGO_PROFILE_RELEASE_LTO=true

# Common CMake arguments.
CMAKE_ARGS=(
  -G"Ninja"
  -DCMAKE_BUILD_TYPE=Release
  -DCMAKE_INSTALL_PREFIX="$TARGET_PATH"
  -DCMAKE_INSTALL_LIBDIR=lib
  -DCMAKE_PREFIX_PATH="$TARGET_PATH"
)

# Common Meson arguments.
MESON_ARGS=(
  --buildtype=release
  --strip
  --wrap-mode=nofallback
  --prefix="$TARGET_PATH"
  --libdir=lib
)

# add_sbom name version cpe: writes a CycloneDX stub so scanners (syft/grype) see
# self-built libraries. CPEs: https://nvd.nist.gov/products/cpe/search/
add_sbom() {
  local name=$1 version=$2 cpe=$3
  local filename="$TARGET_PATH/share/sbom/$name.cdx.json"

  mkdir -p "$(dirname "$filename")"
  cat << EOF > "$filename"
{
  "\$schema": "https://cyclonedx.org/schema/bom-1.7.schema.json",
  "bomFormat": "CycloneDX",
  "specVersion": "1.7",
  "version": 1,
  "metadata": {
    "timestamp": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
    "component": {
      "type": "library",
      "name": "$name",
      "version": "$version",
      "cpe": "$cpe",
      "properties": [
        { "name": "syft:package:type", "value": "binary" }
      ]
    }
  }
}
EOF
}

print_build_stage zlib-ng "$ZLIB_VERSION"
cd "$DEPS_SRC/zlib"
CFLAGS="${CFLAGS} -O3" cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=TRUE \
  -DZLIB_COMPAT=TRUE \
  -DWITH_ARMV6=FALSE \
  -DWITH_GTEST=FALSE \
  -DZLIB_ENABLE_TESTS=FALSE
ninja -C _build install/strip
add_sbom zlib-ng "$ZLIB_VERSION" "cpe:2.3:a:zlib-ng:zlib-ng:$ZLIB_VERSION:*:*:*:*:*:*:*"

print_build_stage brotli "$BROTLI_VERSION"
cd "$DEPS_SRC/brotli"
CFLAGS="${CFLAGS} -O3" cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=TRUE \
  -DBROTLI_DISABLE_TESTS=TRUE
ninja -C _build install/strip
add_sbom brotli "$BROTLI_VERSION" "cpe:2.3:a:google:brotli:$BROTLI_VERSION:*:*:*:*:*:*:*"

print_build_stage ffi "$FFI_VERSION"
cd "$DEPS_SRC/ffi"
./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --disable-builddir \
  --disable-multi-os-directory \
  --disable-raw-api \
  --disable-docs
make install-strip -j"$(nproc)"
add_sbom ffi "$FFI_VERSION" "cpe:2.3:a:libffi_project:libffi:$FFI_VERSION:*:*:*:*:*:*:*"

print_build_stage pcre2 "$PCRE2_VERSION"
cd "$DEPS_SRC/pcre2"
CFLAGS="${CFLAGS} -O3" cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=TRUE \
  -DBUILD_STATIC_LIBS=OFF \
  -DPCRE2_BUILD_PCRE2GREP=OFF \
  -DPCRE2_BUILD_TESTS=OFF \
  -DPCRE2_SUPPORT_JIT=ON
ninja -C _build install/strip
add_sbom pcre2 "$PCRE2_VERSION" "cpe:2.3:a:pcre:pcre2:$PCRE2_VERSION:*:*:*:*:*:*:*"

print_build_stage glib "$GLIB_VERSION"
cd "$DEPS_SRC/glib"
meson setup _build "${MESON_ARGS[@]}" \
  -Dlibmount=disabled \
  -Dtests=false \
  -Dintrospection=disabled \
  -Dnls=disabled \
  -Dsysprof=disabled \
  -Dlibelf=disabled \
  -Dselinux=disabled \
  -Dxattr=false \
  -Dinstalled_tests=false \
  -Dglib_debug=disabled
ninja -C _build install
add_sbom glib "$GLIB_VERSION" "cpe:2.3:a:gnome:glib:$GLIB_VERSION:*:*:*:*:*:*:*"

print_build_stage highway "$HIGHWAY_VERSION"
cd "$DEPS_SRC/highway"
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=TRUE \
  -DBUILD_TESTING=OFF \
  -DHWY_ENABLE_EXAMPLES=FALSE \
  -DHWY_ENABLE_TESTS=FALSE \
  -DHWY_ENABLE_CONTRIB=FALSE
ninja -C _build install/strip
add_sbom highway "$HIGHWAY_VERSION" "cpe:2.3:a:google:highway:$HIGHWAY_VERSION:*:*:*:*:*:*:*"

print_build_stage quantizr "$QUANTIZR_VERSION"
cd "$DEPS_SRC/quantizr"
cargo cinstall \
  --release \
  --library-type=cdylib \
  --prefix="$TARGET_PATH" \
  --libdir="$TARGET_PATH/lib"
add_sbom quantizr "$QUANTIZR_VERSION" "cpe:2.3:a:darthsim:quantizr:$QUANTIZR_VERSION:*:*:*:*:*:*:*"

print_build_stage expat "$LIBEXPAT_VERSION"
cd "$DEPS_SRC/expat"
./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --without-xmlwf \
  --without-examples \
  --without-tests \
  --without-docbook
make install-strip -j"$(nproc)"
add_sbom expat "$LIBEXPAT_VERSION" "cpe:2.3:a:libexpat_project:libexpat:$LIBEXPAT_VERSION:*:*:*:*:*:*:*"

print_build_stage libxml2 "$LIBXML2_VERSION"
cd "$DEPS_SRC/libxml2"
meson setup _build "${MESON_ARGS[@]}" \
  -Dminimum=true
ninja -C _build install
add_sbom libxml2 "$LIBXML2_VERSION" "cpe:2.3:a:xmlsoft:libxml2:$LIBXML2_VERSION:*:*:*:*:*:*:*"

print_build_stage libexif "$LIBEXIF_VERSION"
cd "$DEPS_SRC/libexif"
autoreconf -fi
./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --disable-nls \
  --disable-docs
make install-strip -j"$(nproc)"
add_sbom libexif "$LIBEXIF_VERSION" "cpe:2.3:a:libexif_project:libexif:$LIBEXIF_VERSION:*:*:*:*:*:*:*"

print_build_stage lcms2 "$LCMS2_VERSION"
cd "$DEPS_SRC/lcms2"
CFLAGS="${CFLAGS} -O3" ./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --without-jpeg \
  --without-tiff
make install-strip -j"$(nproc)"
add_sbom lcms2 "$LCMS2_VERSION" "cpe:2.3:a:littlecms:little_cms_color_engine:$LCMS2_VERSION:*:*:*:*:*:*:*"

print_build_stage libjpeg-turbo "$LIBJPEGTURBO_VERSION"
cd "$DEPS_SRC/libjpeg-turbo"
cmake -B _build "${CMAKE_ARGS[@]}" \
  -DENABLE_SHARED=TRUE \
  -DENABLE_STATIC=FALSE \
  -DWITH_TURBOJPEG=FALSE \
  -DWITH_TOOLS=FALSE \
  -DWITH_TESTS=FALSE \
  -DWITH_JPEG8=1 \
  -DPNG_SUPPORTED=FALSE
ninja -C _build install/strip
add_sbom libjpeg-turbo "$LIBJPEGTURBO_VERSION" "cpe:2.3:a:libjpeg-turbo:libjpeg-turbo:$LIBJPEGTURBO_VERSION:*:*:*:*:*:*:*"

print_build_stage libjxl "$LIBJXL_VERSION"
cd "$DEPS_SRC/libjxl"
cmake -B _build "${CMAKE_ARGS[@]}" \
  -DJPEGXL_STATIC=FALSE \
  -DBUILD_TESTING=OFF \
  -DJPEGXL_FORCE_SYSTEM_BROTLI=TRUE \
  -DJPEGXL_FORCE_SYSTEM_HWY=TRUE \
  -DJPEGXL_ENABLE_FUZZERS=FALSE \
  -DJPEGXL_ENABLE_DEVTOOLS=FALSE \
  -DJPEGXL_ENABLE_TOOLS=FALSE \
  -DJPEGXL_ENABLE_JPEGLI=FALSE \
  -DJPEGXL_ENABLE_JPEGLI_LIBJPEG=FALSE \
  -DJPEGXL_ENABLE_DOXYGEN=FALSE \
  -DJPEGXL_ENABLE_MANPAGES=FALSE \
  -DJPEGXL_ENABLE_BENCHMARK=FALSE \
  -DJPEGXL_ENABLE_EXAMPLES=FALSE \
  -DJPEGXL_BUNDLE_LIBPNG=FALSE \
  -DJPEGXL_ENABLE_JNI=FALSE \
  -DJPEGXL_ENABLE_SKCMS=FALSE \
  -DJPEGXL_ENABLE_SJPEG=FALSE \
  -DJPEGXL_ENABLE_OPENEXR=FALSE
ninja -C _build install/strip
add_sbom libjxl "$LIBJXL_VERSION" "cpe:2.3:a:libjxl_project:libjxl:$LIBJXL_VERSION:*:*:*:*:*:*:*"

print_build_stage libpng "$LIBPNG_VERSION"
cd "$DEPS_SRC/libpng"
./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --disable-tools
make install-strip -j"$(nproc)"
add_sbom libpng "$LIBPNG_VERSION" "cpe:2.3:a:libpng:libpng:$LIBPNG_VERSION:*:*:*:*:*:*:*"

print_build_stage libspng "$LIBSPNG_VERSION"
cd "$DEPS_SRC/libspng"
CFLAGS="${CFLAGS} -O3" meson setup _build "${MESON_ARGS[@]}" \
  -Dbuild_examples=false \
  -Ddev_build=false \
  -Dstatic_zlib=false
ninja -C _build install
add_sbom libspng "$LIBSPNG_VERSION" "cpe:2.3:a:libspng:libspng:$LIBSPNG_VERSION:*:*:*:*:*:*:*"

print_build_stage libwebp "$LIBWEBP_VERSION"
cd "$DEPS_SRC/libwebp"
./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --enable-libwebpmux \
  --enable-libwebpdemux \
  --disable-gl \
  --disable-sdl \
  --disable-png \
  --disable-jpeg \
  --disable-tiff \
  --disable-gif
make install-strip -j"$(nproc)"
add_sbom libwebp "$LIBWEBP_VERSION" "cpe:2.3:a:webmproject:libwebp:$LIBWEBP_VERSION:*:*:*:*:*:*:*"

print_build_stage libtiff "$LIBTIFF_VERSION"
cd "$DEPS_SRC/libtiff"
# jbig is disabled explicitly: JBIG-KIT is GPL.
cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=TRUE \
  -Dtiff-tools=FALSE \
  -Dtiff-tests=FALSE \
  -Dtiff-contrib=FALSE \
  -Dtiff-docs=FALSE \
  -Dtiff-deprecated=FALSE \
  -Dzlib=ON \
  -Djpeg=ON \
  -Dwebp=ON \
  -Djbig=OFF \
  -Dlzma=OFF \
  -Dzstd=OFF \
  -Dlerc=OFF \
  -Dlibdeflate=OFF \
  -Dold-jpeg=OFF \
  -Djpeg12=OFF
ninja -C _build install/strip
add_sbom libtiff "$LIBTIFF_VERSION" "cpe:2.3:a:libtiff:libtiff:$LIBTIFF_VERSION:*:*:*:*:*:*:*"

print_build_stage openjpeg "$OPENJPEG_VERSION"
cd "$DEPS_SRC/openjpeg"
CFLAGS="${CFLAGS} -O3" cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=ON \
  -DBUILD_STATIC_LIBS=OFF \
  -DBUILD_CODEC=OFF \
  -DBUILD_TESTING=OFF \
  -DBUILD_DOC=OFF
ninja -C _build install/strip
add_sbom openjpeg "$OPENJPEG_VERSION" "cpe:2.3:a:uclouvain:openjpeg:$OPENJPEG_VERSION:*:*:*:*:*:*:*"

print_build_stage libultrahdr "$LIBULTRAHDR_VERSION"
cd "$DEPS_SRC/libultrahdr"
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=TRUE \
  -DUHDR_BUILD_EXAMPLES=FALSE \
  -DUHDR_BUILD_TESTS=FALSE \
  -DUHDR_BUILD_DEPS=FALSE \
  -DUHDR_ENABLE_HEIF=FALSE \
  -DUHDR_ENABLE_GLES=FALSE \
  -DUHDR_MAX_DIMENSION=65500
ninja -C _build install/strip
add_sbom libultrahdr "$LIBULTRAHDR_VERSION" "cpe:2.3:a:google:libultrahdr:$LIBULTRAHDR_VERSION:*:*:*:*:*:*:*"

print_build_stage cgif "$CGIF_VERSION"
cd "$DEPS_SRC/cgif"
CFLAGS="${CFLAGS} -O3" meson setup _build "${MESON_ARGS[@]}" \
  -Dexamples=false \
  -Dtests=false
ninja -C _build install
add_sbom cgif "$CGIF_VERSION" "cpe:2.3:a:dloebl:cgif:$CGIF_VERSION:*:*:*:*:*:*:*"

print_build_stage libde265 "$LIBDE265_VERSION"
cd "$DEPS_SRC/libde265"
CFLAGS="${CFLAGS} -O3 -pthread" CXXFLAGS="${CXXFLAGS} -O3 -pthread" \
LDFLAGS="${LDFLAGS} -pthread" \
cmake -B _build "${CMAKE_ARGS[@]}" \
  --preset=release \
  -DBUILD_SHARED_LIBS=1 \
  -DENABLE_DECODER=OFF \
  -DENABLE_ENCODER=OFF \
  -DENABLE_SDL=OFF
ninja -C _build install/strip
add_sbom libde265 "$LIBDE265_VERSION" "cpe:2.3:a:struktur:libde265:$LIBDE265_VERSION:*:*:*:*:*:*:*"

print_build_stage kvazaar "$KVAZAAR_VERSION"
cd "$DEPS_SRC/kvazaar"
./autogen.sh
./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static
make install-strip -j"$(nproc)"
add_sbom kvazaar "$KVAZAAR_VERSION" "cpe:2.3:a:ultravideo:kvazaar:$KVAZAAR_VERSION:*:*:*:*:*:*:*"

print_build_stage dav1d "$DAV1D_VERSION"
cd "$DEPS_SRC/dav1d"
meson setup _build "${MESON_ARGS[@]}" \
  -Denable_tools=false \
  -Denable_tests=false \
  -Denable_examples=false
ninja -C _build install
add_sbom dav1d "$DAV1D_VERSION" "cpe:2.3:a:videolan:dav1d:$DAV1D_VERSION:*:*:*:*:*:*:*"

print_build_stage aom "$AOM_VERSION"
cd "$DEPS_SRC/aom"
cmake -B _build "${CMAKE_ARGS[@]}" \
  -DBUILD_SHARED_LIBS=1 \
  -DENABLE_DOCS=0 \
  -DENABLE_TESTS=0 \
  -DENABLE_TESTDATA=0 \
  -DENABLE_TOOLS=0 \
  -DENABLE_EXAMPLES=0 \
  -DCONFIG_WEBM_IO=0
ninja -C _build install/strip
add_sbom aom "$AOM_VERSION" "cpe:2.3:a:aomedia:aomedia:$AOM_VERSION:*:*:*:*:*:*:*"

print_build_stage libheif "$LIBHEIF_VERSION"
cd "$DEPS_SRC/libheif"
# Every optional codec is switched explicitly; x265 (GPL) must stay off.
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" \
cmake -B _build "${CMAKE_ARGS[@]}" \
  --preset=release-noplugins \
  -DBUILD_SHARED_LIBS=1 \
  -DBUILD_TESTING=OFF \
  -DWITH_EXAMPLES=0 \
  -DWITH_GDK_PIXBUF=0 \
  -DWITH_LIBDE265=1 \
  -DWITH_KVAZAAR=1 \
  -DWITH_DAV1D=1 \
  -DWITH_AOM_ENCODER=1 \
  -DWITH_AOM_DECODER=0 \
  -DWITH_X265=0 \
  -DWITH_X264=0 \
  -DWITH_OpenH264_DECODER=0 \
  -DWITH_OpenH264_ENCODER=0 \
  -DWITH_UNCOMPRESSED_CODEC=0 \
  -DWITH_RAV1E=0 \
  -DWITH_SvtEnc=0 \
  -DWITH_FFMPEG_DECODER=0 \
  -DWITH_JPEG_DECODER=0 \
  -DWITH_JPEG_ENCODER=0 \
  -DWITH_OpenJPEG_DECODER=0 \
  -DWITH_OpenJPEG_ENCODER=0 \
  -DWITH_OPENJPH_ENCODER=0 \
  -DWITH_OPENJPH_DECODER=0 \
  -DWITH_UVG266=0 \
  -DWITH_VVDEC=0 \
  -DWITH_VVENC=0
ninja -C _build install/strip
add_sbom libheif "$LIBHEIF_VERSION" "cpe:2.3:a:struktur:libheif:$LIBHEIF_VERSION:*:*:*:*:*:*:*"

print_build_stage freetype "$FREETYPE_VERSION"
cd "$DEPS_SRC/freetype"
meson setup _build "${MESON_ARGS[@]}" \
  -Dzlib=enabled \
  -Dpng=disabled \
  -Dharfbuzz=disabled \
  -Dbrotli=disabled \
  -Dbzip2=disabled \
  -Dtests=disabled
ninja -C _build install
add_sbom freetype "$FREETYPE_VERSION" "cpe:2.3:a:freetype:freetype:$FREETYPE_VERSION:*:*:*:*:*:*:*"

print_build_stage fontconfig "$FONTCONFIG_VERSION"
cd "$DEPS_SRC/fontconfig"
# Fonts live in the image under $TARGET_PATH/share/fonts; the cache goes to /tmp so
# the non-root runtime user can write it on a read-only root filesystem.
meson setup _build "${MESON_ARGS[@]}" \
  -Ddoc=disabled \
  -Dnls=disabled \
  -Dtests=disabled \
  -Dtools=disabled \
  -Dcache-build=disabled \
  -Dxml-backend=expat \
  -Ddefault-fonts-dirs="$TARGET_PATH/share/fonts" \
  -Dadditional-fonts-dirs=no \
  -Dcache-dir=/tmp/fontconfig
ninja -C _build install
add_sbom fontconfig "$FONTCONFIG_VERSION" "cpe:2.3:a:fontconfig_project:fontconfig:$FONTCONFIG_VERSION:*:*:*:*:*:*:*"

print_build_stage harfbuzz "$HARFBUZZ_VERSION"
cd "$DEPS_SRC/harfbuzz"
meson setup _build "${MESON_ARGS[@]}" \
  -Dgobject=disabled \
  -Dicu=disabled \
  -Dtests=disabled \
  -Dintrospection=disabled \
  -Ddocs=disabled \
  -Dbenchmark=disabled \
  -Dutilities=disabled \
  -Dgpu=disabled \
  -Dgpu_demo=disabled
ninja -C _build install
rm -f "$TARGET_PATH"/lib/libharfbuzz-subset*
add_sbom harfbuzz "$HARFBUZZ_VERSION" "cpe:2.3:a:harfbuzz_project:harfbuzz:$HARFBUZZ_VERSION:*:*:*:*:*:*:*"

print_build_stage pixman "$PIXMAN_VERSION"
cd "$DEPS_SRC/pixman"
meson setup _build "${MESON_ARGS[@]}" \
  -Dlibpng=disabled \
  -Dgtk=disabled \
  -Dopenmp=disabled \
  -Ddemos=disabled \
  -Dtests=disabled
ninja -C _build install
add_sbom pixman "$PIXMAN_VERSION" "cpe:2.3:a:pixman:pixman:$PIXMAN_VERSION:*:*:*:*:*:*:*"

print_build_stage cairo "$CAIRO_VERSION"
cd "$DEPS_SRC/cairo"
meson setup _build "${MESON_ARGS[@]}" \
  -Dfontconfig=enabled \
  -Dfreetype=enabled \
  -Dpng=enabled \
  -Dquartz=disabled \
  -Dtee=disabled \
  -Dxcb=disabled \
  -Dxlib=disabled \
  -Dzlib=disabled \
  -Dtests=disabled \
  -Dspectre=disabled \
  -Dsymbol-lookup=disabled \
  -Dlzo=disabled \
  -Dgtk_doc=false
ninja -C _build install
add_sbom cairo "$CAIRO_VERSION" "cpe:2.3:a:cairographics:cairo:$CAIRO_VERSION:*:*:*:*:*:*:*"

print_build_stage fribidi "$FRIBIDI_VERSION"
cd "$DEPS_SRC/fribidi"
meson setup _build "${MESON_ARGS[@]}" \
  -Ddocs=false \
  -Dbin=false \
  -Dtests=false
ninja -C _build install
add_sbom fribidi "$FRIBIDI_VERSION" "cpe:2.3:a:gnu:fribidi:$FRIBIDI_VERSION:*:*:*:*:*:*:*"

print_build_stage pango "$PANGO_VERSION"
cd "$DEPS_SRC/pango"
meson setup _build "${MESON_ARGS[@]}" \
  -Ddocumentation=false \
  -Dintrospection=disabled \
  -Dbuild-testsuite=false \
  -Dbuild-examples=false \
  -Dfontconfig=enabled \
  -Dfreetype=enabled \
  -Dcairo=enabled \
  -Dxft=disabled
ninja -C _build install
add_sbom pango "$PANGO_VERSION" "cpe:2.3:a:gnome:pango:$PANGO_VERSION:*:*:*:*:*:*:*"

print_build_stage librsvg "$LIBRSVG_VERSION"
cd "$DEPS_SRC/librsvg"
# For some reason, librsvg fails to build without -ldl (imgproxy-docker-base note).
CFLAGS="${CFLAGS} -ldl" CXXFLAGS="${CXXFLAGS} -ldl" \
LDFLAGS="${LDFLAGS} -ldl" \
meson setup _build "${MESON_ARGS[@]}" \
  -Dintrospection=disabled \
  -Dpixbuf=disabled \
  -Dpixbuf-loader=disabled \
  -Drsvg-convert=disabled \
  -Ddocs=disabled \
  -Dvala=disabled \
  -Dtests=false \
  -Davif=disabled
ninja -C _build install
add_sbom librsvg "$LIBRSVG_VERSION" "cpe:2.3:a:gnome:librsvg:$LIBRSVG_VERSION:*:*:*:*:*:*:*"

print_build_stage jemalloc "$JEMALLOC_VERSION"
cd "$DEPS_SRC/jemalloc"
# 64 KiB pages on arm64 so the same binary runs on 4K and 64K page kernels.
JEMALLOC_PAGE_ARGS=()
if [ "$(uname -m)" = "aarch64" ]; then
  JEMALLOC_PAGE_ARGS=(--with-lg-page=16)
fi
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" ./configure \
  --prefix="$TARGET_PATH" \
  --disable-static \
  --disable-cxx \
  --disable-doc \
  --disable-debug \
  "${JEMALLOC_PAGE_ARGS[@]}"
make -j"$(nproc)" build_lib_shared
make install_lib_shared install_lib_pc install_include
strip --strip-unneeded "$TARGET_PATH"/lib/libjemalloc.so.2
add_sbom jemalloc "$JEMALLOC_VERSION" "cpe:2.3:a:jemalloc:jemalloc:$JEMALLOC_VERSION:*:*:*:*:*:*:*"

print_build_stage vips "$VIPS_VERSION"
cd "$DEPS_SRC/vips"
# Every loader is required (build fails if a dependency is missing); every GPL or
# out-of-scope loader is disabled (LibRaw, PDFium, ImageMagick and OpenEXR are WP-66).
# libvips 8.18 uses libspng only when libpng is not a dependency, so png is disabled
# here (libpng is still built for cairo and librsvg); docs/FORMATS.md requires spng.
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" \
meson setup _build "${MESON_ARGS[@]}" \
  -Ddocs=false \
  -Dcpp-docs=false \
  -Dexamples=false \
  -Dcplusplus=false \
  -Dintrospection=disabled \
  -Dvapi=false \
  -Dmodules=disabled \
  -Djpeg=enabled \
  -Duhdr=enabled \
  -Dpng=disabled \
  -Dspng=enabled \
  -Dwebp=enabled \
  -Dtiff=enabled \
  -Dheif=enabled \
  -Djpeg-xl=enabled \
  -Dopenjpeg=enabled \
  -Dcgif=enabled \
  -Dquantizr=enabled \
  -Dimagequant=disabled \
  -Drsvg=enabled \
  -Dpangocairo=enabled \
  -Dfontconfig=enabled \
  -Dlcms=enabled \
  -Dexif=enabled \
  -Dhighway=enabled \
  -Dzlib=enabled \
  -Dnsgif=true \
  -Dpoppler=disabled \
  -Dpdfium=disabled \
  -Dmagick=disabled \
  -Draw=disabled \
  -Dopenexr=disabled \
  -Dopenslide=disabled \
  -Dmatio=disabled \
  -Dnifti=disabled \
  -Dcfitsio=disabled \
  -Dfftw=disabled \
  -Dorc=disabled \
  -Darchive=disabled
ninja -C _build install
add_sbom vips "$VIPS_VERSION" "cpe:2.3:a:libvips:libvips:$VIPS_VERSION:*:*:*:*:*:*:*"

# --- Cleanup -------------------------------------------------------------------
rm -rf "$TARGET_PATH"/lib/*.a "$TARGET_PATH"/lib/*.la
rm -rf "$TARGET_PATH"/share/{doc,man,gtk-doc,locale,gettext,aclocal,bash-completion,installed-tests,thumbnailers}
find "$TARGET_PATH/bin" -type f ! -name 'vips*' -delete

# --- Fonts (OFL / Bitstream Vera licences, text rendering for SVG and watermarks) ---
mkdir -p "$TARGET_PATH/share/fonts/dejavu"
cp /usr/share/fonts/truetype/dejavu/DejaVuSans*.ttf "$TARGET_PATH/share/fonts/dejavu/"

# --- License bundle and SOURCES.md (LGPL source offer, ADR 0006) ----------------
LICENSES="$TARGET_PATH/share/licenses"
mkdir -p "$LICENSES"
for dir in "$DEPS_SRC"/*/; do
  name="$(basename "$dir")"
  mkdir -p "$LICENSES/$name"
  find "$dir" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' \
    -o -iname 'COPYRIGHT*' -o -iname 'NOTICE*' -o -iname 'PATENTS*' -o -iname 'README.ijg' \
    -o -iname 'LEGAL*' \) -exec cp {} "$LICENSES/$name/" \;
  # REUSE-style projects (glib, libexif) keep their license texts in LICENSES/.
  if [ -d "$dir/LICENSES" ]; then
    cp -r "$dir/LICENSES" "$LICENSES/$name/"
  fi
  if [ -z "$(ls -A "$LICENSES/$name")" ]; then
    echo "No license file found for $name" >&2
    exit 1
  fi
done
cp /usr/share/doc/fonts-dejavu-core/copyright "$LICENSES/fonts-dejavu.copyright"
{
  echo "# Sources of the libraries in /opt/xtandard"
  echo ""
  echo "Built by docker/base/build-deps.sh (xtandard-image, flavor ${XI_BASE_FLAVOR}, revision ${XI_BASE_REVISION})."
  echo "LGPL components are dynamically linked; their exact sources are listed below."
  echo ""
  echo "| Component | Version | Source | SHA256 |"
  echo "|---|---|---|---|"
  cat "$DEPS_SRC/SOURCES.rows"
} > "$LICENSES/SOURCES.md"

# --- License gate --------------------------------------------------------------
"$my_dir/check-licenses.sh" "$TARGET_PATH/lib" "$my_dir/versions.sh"
