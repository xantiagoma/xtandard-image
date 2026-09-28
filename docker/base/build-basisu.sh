#!/bin/bash
# Builds libxibasisu.so (KTX2 textures, WP-108) into /opt/xtandard: the Basis Universal
# transcoder (Binomial LLC, Apache-2.0) with patches/basisu, and the app's C wrapper
# basisu/xibasisu.{h,cpp} (MIT), then reruns the licence gate. It runs in the `deps`
# stage after build-jxrlib.sh, as its own layer. libvips does not link it: the app's
# shim does (internal/vips/ktx2.c). download-deps.sh fetched the source (and wrote its
# SOURCES.md row) and build-deps.sh already copied its LICENSE, NOTICE and LICENSES/
# into the licence bundle.
#
# Only transcoder/basisu_transcoder.cpp is compiled: no encoder, no tools, no OpenCL,
# and not the archive's zstd copy (Zstandard supercompression links the base's
# libzstd). Every symbol but the three xi_basisu_* functions is hidden.
#
# Runs with BASH_ENV=bashrc.sh (compiler flags).

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"

DEPS_SRC=${DEPS_SRC:-/root/deps}
TARGET_PATH=${XI_PREFIX:-/opt/xtandard}

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

echo ""
echo "== Build Basis Universal transcoder $BASISU_VERSION ======================"
echo ""
# A pristine copy: the patches apply to the downloaded tree only once.
rm -rf /tmp/basisu
cp -a "$DEPS_SRC/basisu" /tmp/basisu
cd /tmp/basisu
for patch in "$my_dir"/patches/basisu/*.patch; do
  git apply --verbose "$patch"
done
# -DNDEBUG: the transcoder's asserts would abort the process on a hostile file.
# Exceptions stay on: the patched container_abort() throws std::bad_alloc, which the
# wrapper turns into an error code.
basisu_flags=(-O2 -std=c++17 -fPIC -fvisibility=hidden -fvisibility-inlines-hidden -DNDEBUG
  -DBASISD_SUPPORT_KTX2=1 -DBASISD_SUPPORT_KTX2_ZSTD=1 "-DXI_BASISU_VERSION=\"$BASISU_VERSION\""
  -Itranscoder -I"$TARGET_PATH/include" -I"$my_dir/basisu")
mkdir -p _build
# shellcheck disable=SC2086 # CXXFLAGS is a list of flags
g++ $CXXFLAGS "${basisu_flags[@]}" -c transcoder/basisu_transcoder.cpp -o _build/basisu_transcoder.o
# shellcheck disable=SC2086
g++ $CXXFLAGS "${basisu_flags[@]}" -c "$my_dir/basisu/xibasisu.cpp" -o _build/xibasisu.o
# shellcheck disable=SC2086 # LDFLAGS is a list of flags
g++ -shared $LDFLAGS -Wl,-soname,libxibasisu.so.2 -o "$TARGET_PATH/lib/libxibasisu.so.$BASISU_VERSION" \
  _build/basisu_transcoder.o _build/xibasisu.o -L"$TARGET_PATH/lib" -lzstd
ln -sf "libxibasisu.so.$BASISU_VERSION" "$TARGET_PATH/lib/libxibasisu.so.2"
ln -sf libxibasisu.so.2 "$TARGET_PATH/lib/libxibasisu.so"
strip --strip-unneeded "$TARGET_PATH/lib/libxibasisu.so.$BASISU_VERSION"
install -m 0644 "$my_dir/basisu/xibasisu.h" "$TARGET_PATH/include/xibasisu.h"
cat > "$TARGET_PATH/lib/pkgconfig/xibasisu.pc" << EOF
prefix=$TARGET_PATH
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: xibasisu
Description: KTX2 decoding through the Basis Universal transcoder $BASISU_VERSION (xtandard-image wrapper)
Version: $BASISU_VERSION
Libs: -L\${libdir} -lxibasisu
Cflags: -I\${includedir}
EOF
add_sbom basis-universal "$BASISU_VERSION" "cpe:2.3:a:binomial:basis_universal:$BASISU_VERSION:*:*:*:*:*:*:*"

# Exactly the wrapper's functions are exported.
exported="$(nm -D --defined-only "$TARGET_PATH/lib/libxibasisu.so.$BASISU_VERSION" | awk '$2 == "T" { print $3 }' | sort | xargs)"
if [ "$exported" != "xi_basisu_ktx2_decode xi_basisu_ktx2_info xi_basisu_version" ]; then
  echo "libxibasisu exports more than the wrapper: $exported" >&2
  exit 1
fi

cd /
rm -rf /tmp/basisu

# --- License gate --------------------------------------------------------------
test -n "$(ls -A "$TARGET_PATH/share/licenses/basisu")"
"$my_dir/check-licenses.sh" "$TARGET_PATH/lib" "$my_dir/versions.sh"
