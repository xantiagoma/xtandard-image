#!/bin/bash
# Rebuilds LibRaw (WP-103) with its optional decoders and installs it over the plain
# build that build-deps.sh made for libvips (same sources, same SONAME).
#
# Usage: build-libraw.sh [--with-sdks]
#
# Every flavour (no argument; `deps` stage, after build-jxrlib.sh, its own layer):
#   - X3F tools (Sigma Foveon X3F; BSD-3-Clause code inside LibRaw's tree).
# gpl flavour only (--with-sdks; `gpl-deps` stage; owner decision 2026-09-28), the
# X3F tools plus:
#   - the Adobe DNG SDK (DNG 1.7 JPEG XL raw data; Adobe DNG SDK licence, pins in
#     versions-gpl.sh), built against the base's libjxl and libjpeg-turbo, without its
#     XMP SDK,
#   - the GoPro GPR SDK's VC-5 decoder (GoPro .GPR; Apache-2.0 OR MIT), which LibRaw
#     reaches through the DNG SDK.
#   Both SDKs are compiled into static, PIC, hidden-visibility archives linked into
#   libraw.so/libraw_r.so only, so they export nothing and nothing else links them.
#   Patches: patches/libraw (the C API owns a dng_host), patches/dng-sdk (no XMP),
#   patches/gpr (DNG SDK 1.7 reader interface, hardening). The licence gate then runs
#   in its gpl mode, the only one that accepts the DNG SDK.
#
# Sources: $DEPS_SRC/libraw (download-deps.sh) and, with --with-sdks, $LIBRAW_DEPS_SRC
# (download-libraw-deps.sh). Runs with BASH_ENV=bashrc.sh (compiler flags).

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"

with_sdks=0
case "${1:-}" in
  --with-sdks)
    with_sdks=1
    # shellcheck source=versions-gpl.sh
    source "$my_dir/versions-gpl.sh"
    ;;
  '') ;;
  *) echo "usage: $0 [--with-sdks]" >&2; exit 2 ;;
esac

DEPS_SRC=${DEPS_SRC:-/root/deps}
LIBRAW_DEPS_SRC=${LIBRAW_DEPS_SRC:-/root/libraw-deps}
TARGET_PATH=${XI_PREFIX:-/opt/xtandard}
BUILD=/tmp/xi-libraw
jobs="$(nproc)"

# add_sbom name version cpe: CycloneDX stub, same format as build-deps.sh.
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

# apply_patches dir: git apply every patch of patches/<dir> in the current directory.
apply_patches() {
  local patch
  for patch in "$my_dir/patches/$1"/*.patch; do
    git apply --verbose "$patch"
  done
}

rm -rf "$BUILD"
mkdir -p "$BUILD/obj/dng-sdk" "$BUILD/obj/gpr" "$BUILD/lib"
libraw_cppflags=(-DUSE_X3FTOOLS)
libraw_libs=()
want_caps=LIBRAW_CAPS_X3FTOOLS

if [ "$with_sdks" = 1 ]; then
  # The DNG SDK's configuration, shared by every file that includes its headers (the
  # SDK, the GPR reader and LibRaw's glue): Linux, thread-safe, no XMP SDK, libjpeg for
  # lossy JPEG tiles, no validation code. NDEBUG turns its asserts off.
  DNG_DEFS=(-DqLinux=1 -DqDNGThreadSafe=1 -DqDNGUseXMP=0 -DqDNGXMPFiles=0 -DqDNGXMPDocOps=0
    -DqDNGUseLibJPEG=1 -DqDNGValidateTarget=0 -DqDNGExperimental=1 -DNDEBUG)
  # shellcheck disable=SC2206 # CFLAGS/CXXFLAGS are flag lists
  SDK_CFLAGS=($CFLAGS -O2 -w -fvisibility=hidden)
  # shellcheck disable=SC2206
  SDK_CXXFLAGS=($CXXFLAGS -O2 -w -std=c++17 -fvisibility=hidden -fvisibility-inlines-hidden)

  echo ""
  echo "== Build Adobe DNG SDK $DNGSDK_VERSION ====================================="
  echo ""
  cp -a "$LIBRAW_DEPS_SRC/dng-sdk" "$BUILD/dng-sdk"
  cd "$BUILD/dng-sdk"
  apply_patches dng-sdk
  DNG_SRC="$BUILD/dng-sdk/source"
  # Everything but the dng_validate tool, the XMP SDK glue and the metadata updater
  # (writing only, needs XMP).
  dng_files=()
  for src in "$DNG_SRC"/*.cpp; do
    case "$(basename "$src")" in
      dng_validate.cpp | dng_xmp_sdk.cpp | dng_update_meta.cpp) continue ;;
    esac
    dng_files+=("$src")
  done
  printf '%s\n' "${dng_files[@]}" | xargs -P"$jobs" -I{} sh -c \
    'g++ "$@" -c "{}" -o "'"$BUILD"'/obj/dng-sdk/$(basename "{}" .cpp).o"' _ \
    "${SDK_CXXFLAGS[@]}" "${DNG_DEFS[@]}" -I"$TARGET_PATH/include"
  ar rcs "$BUILD/lib/libxi_dngsdk.a" "$BUILD"/obj/dng-sdk/*.o

  echo ""
  echo "== Build GoPro GPR SDK $GPR_VERSION (VC-5 decoder) ==========================="
  echo ""
  cp -a "$LIBRAW_DEPS_SRC/gpr" "$BUILD/gpr"
  cd "$BUILD/gpr"
  apply_patches gpr
  GPR_LIB="$BUILD/gpr/source/lib"
  GPR_DEFS=(-DGPR_READING=1 -DGPR_WRITING=0 -DGPR_JPEG_AVAILABLE=0 -DNDEBUG)
  GPR_INC=(-I"$GPR_LIB/common/private" -I"$GPR_LIB/common/public" -I"$GPR_LIB/vc5_common"
    -I"$GPR_LIB/vc5_decoder" -I"$GPR_LIB/gpr_sdk/private" -I"$GPR_LIB/gpr_sdk/public")
  for src in "$GPR_LIB"/common/private/gpr_{allocator,buffer}.c "$GPR_LIB"/vc5_common/*.c \
    "$GPR_LIB"/vc5_decoder/*.c; do
    obj="$BUILD/obj/gpr/$(basename "$(dirname "$src")")_$(basename "$src" .c).o"
    gcc "${SDK_CFLAGS[@]}" -std=gnu99 "${GPR_DEFS[@]}" "${GPR_INC[@]}" -c "$src" -o "$obj"
  done
  for src in "$GPR_LIB"/common/private/gpr_buffer_auto.cpp \
    "$GPR_LIB"/gpr_sdk/private/gpr_{read_image,utils}.cpp; do
    g++ "${SDK_CXXFLAGS[@]}" "${GPR_DEFS[@]}" "${DNG_DEFS[@]}" "${GPR_INC[@]}" -I"$DNG_SRC" \
      -I"$TARGET_PATH/include" -c "$src" -o "$BUILD/obj/gpr/$(basename "$src" .cpp).o"
  done
  ar rcs "$BUILD/lib/libxi_gpr.a" "$BUILD"/obj/gpr/*.o

  libraw_cppflags+=(-DUSE_DNGSDK -DUSE_GPRSDK "${DNG_DEFS[@]}" -I"$DNG_SRC"
    -I"$GPR_LIB/gpr_sdk/private" -I"$GPR_LIB/common/private" -I"$GPR_LIB/common/public"
    -I"$GPR_LIB/vc5_common" -I"$GPR_LIB/vc5_decoder")
  libraw_libs+=("$BUILD/lib/libxi_gpr.a" "$BUILD/lib/libxi_dngsdk.a" -ljxl -ljxl_threads -lpthread)
  want_caps="LIBRAW_CAPS_X3FTOOLS | LIBRAW_CAPS_DNGSDK | LIBRAW_CAPS_GPRSDK"
fi

echo ""
if [ "$with_sdks" = 1 ]; then
  echo "== Build LibRaw $LIBRAW_VERSION (X3F tools, DNG SDK, GPR SDK) ================"
else
  echo "== Build LibRaw $LIBRAW_VERSION (X3F tools) =================================="
fi
echo ""
cp -a "$DEPS_SRC/libraw" "$BUILD/libraw"
cd "$BUILD/libraw"
# The tree build-deps.sh configured and built: start clean.
make distclean > /dev/null
# The LibRaw patch only matters with USE_DNGSDK (a dng_host per C API handle).
if [ "$with_sdks" = 1 ]; then
  apply_patches libraw
fi
# Same options as the plain build (build-deps.sh), plus the decoders.
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" \
  CPPFLAGS="${CPPFLAGS} ${libraw_cppflags[*]}" \
  LIBS="${libraw_libs[*]}" \
  ./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --disable-openmp \
  --disable-examples \
  --enable-jpeg \
  --enable-zlib \
  --enable-lcms
make -j"$jobs"
make install-strip
rm -f "$TARGET_PATH"/lib/libraw*.la

# The installed library must report exactly the decoders of this flavour
# (LIBRAW_CAPS_*): X3F everywhere, the DNG and GPR SDKs only with --with-sdks.
cat > "$BUILD/caps.c" << EOF
#include <stdio.h>
#include <libraw.h>
int main(void) {
  unsigned caps = libraw_capabilities();
  unsigned sdks = LIBRAW_CAPS_X3FTOOLS | LIBRAW_CAPS_DNGSDK | LIBRAW_CAPS_GPRSDK;
  unsigned want = $want_caps;
  printf("libraw %s, %d cameras, capabilities 0x%x\\n", libraw_version(), libraw_cameraCount(), caps);
  return (caps & sdks) == want ? 0 : 1;
}
EOF
# shellcheck disable=SC2086 # CFLAGS/LDFLAGS are flag lists
cc $CFLAGS -I"$TARGET_PATH/include/libraw" "$BUILD/caps.c" -o "$BUILD/caps" $LDFLAGS -lraw_r
"$BUILD/caps"

# --- Licences, SOURCES.md and SBOM ---------------------------------------------
LICENSES="$TARGET_PATH/share/licenses"
# The X3F tools' BSD-3-Clause notice is the header comment of their source file.
awk '/^\/\* Library for accessing X3F Files/ { on = 1 } on { print } on && /\*\// { exit }' \
  "$BUILD/libraw/src/x3f/x3f_utils_patched.cpp" > "$LICENSES/libraw/LICENSE.x3f-tools"
grep -q 'Redistribution and use' "$LICENSES/libraw/LICENSE.x3f-tools"
add_sbom libraw "$LIBRAW_VERSION" "cpe:2.3:a:libraw:libraw:$LIBRAW_VERSION:*:*:*:*:*:*:*"
if [ "$with_sdks" = 1 ]; then
  mkdir -p "$LICENSES/dng-sdk" "$LICENSES/gpr"
  cp "$LIBRAW_DEPS_SRC/dng-sdk/LICENSE.txt" "$LICENSES/dng-sdk/"
  cp "$LIBRAW_DEPS_SRC"/gpr/{LICENSE.txt,LICENSE-APACHE,LICENSE-MIT} "$LICENSES/gpr/"
  {
    echo ""
    echo "## gpl flavor: LibRaw's SDKs (WP-103)"
    echo ""
    echo "Compiled into libraw_r by build-libraw.sh --with-sdks (pins in versions-gpl.sh)."
    echo ""
    echo "| File | Source | SHA256 |"
    echo "|---|---|---|"
    cat "$LIBRAW_DEPS_SRC/SOURCES.rows"
  } >> "$LICENSES/SOURCES.md"
  add_sbom dng-sdk "$DNGSDK_VERSION" "cpe:2.3:a:adobe:dng_software_development_kit:$DNGSDK_VERSION:*:*:*:*:*:*:*"
  add_sbom gpr "$GPR_VERSION" "cpe:2.3:a:gopro:gpr:$GPR_VERSION:*:*:*:*:*:*:*"
fi

cd /
rm -rf "$BUILD"

# --- License gate --------------------------------------------------------------
# Strict everywhere but gpl: a DNG SDK in libraw_r (or pinned in versions.sh) fails.
if [ "$with_sdks" = 1 ]; then
  "$my_dir/check-licenses.sh" --allow-gpl "$my_dir/versions-gpl.sh" "$TARGET_PATH/lib" \
    "$my_dir/versions.sh" "$my_dir/versions-gpl.sh"
else
  "$my_dir/check-licenses.sh" "$TARGET_PATH/lib" "$my_dir/versions.sh"
fi
