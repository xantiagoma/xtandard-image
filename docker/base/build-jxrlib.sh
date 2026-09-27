#!/bin/bash
# Builds jxrlib (JPEG XR, WP-97) into /opt/xtandard with the patches in patches/jxrlib,
# then reruns the licence gate. It runs in the `deps` stage right after build-deps.sh,
# as its own layer, so that changing a jxrlib patch does not rebuild every other
# library. libvips does not link jxrlib: the app's shim does (internal/vips/jxr.c).
# download-deps.sh fetched the source (and wrote its SOURCES.md row) and build-deps.sh
# already copied its LICENSE into the licence bundle.
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
echo "== Build jxrlib $JXRLIB_VERSION ====================================="
echo ""
# A pristine copy: the patches apply to the downloaded tree only once.
rm -rf /tmp/jxrlib
cp -a "$DEPS_SRC/jxrlib" /tmp/jxrlib
cd /tmp/jxrlib
# JPEG XR for the app's own libvips loader (internal/vips/jxr.c, WP-97). jxrlib's
# Makefile also builds the sample apps and a test library; only the codec (libjpegxr)
# and the glue API (libjxrglue) are built here, as shared libraries with Debian's
# SONAMEs and Debian's libjxr.pc layout (headers in include/jxrlib). -DNDEBUG turns off
# the library's asserts on stream contents: an assert would abort the process on a
# hostile file instead of failing the decode.
for patch in "$my_dir"/patches/jxrlib/*.patch; do
  git apply --verbose "$patch"
done
jxr_flags=(-O2 -w -D__ANSI__ -DDISABLE_PERF_MEASUREMENT -DNDEBUG -Icommon/include
  -Iimage/sys -Ijxrgluelib)
jxr_codec=(image/sys/{adapthuff,image,strcodec,strPredQuant,strTransform,perfTimerANSI}.c
  image/decode/{decode,postprocess,segdec,strdec,strInvTransform,strPredQuantDec,JXRTranscode}.c
  image/encode/{encode,segenc,strenc,strFwdTransform,strPredQuantEnc}.c)
jxr_glue=(jxrgluelib/{JXRGlue,JXRMeta,JXRGluePFC,JXRGlueJxr}.c)
jxr_obj() { echo "_build/$(basename "${1%.c}").o"; }
mkdir -p _build
for src in "${jxr_codec[@]}" "${jxr_glue[@]}"; do
  # shellcheck disable=SC2086 # CFLAGS is a list of flags
  cc $CFLAGS "${jxr_flags[@]}" -c "$src" -o "$(jxr_obj "$src")"
done
codec_objs=() glue_objs=()
for src in "${jxr_codec[@]}"; do codec_objs+=("$(jxr_obj "$src")"); done
for src in "${jxr_glue[@]}"; do glue_objs+=("$(jxr_obj "$src")"); done
# shellcheck disable=SC2086 # LDFLAGS is a list of flags
cc -shared $LDFLAGS -Wl,-soname,libjpegxr.so.0 -o "$TARGET_PATH/lib/libjpegxr.so.0.1" \
  "${codec_objs[@]}" -lm
ln -sf libjpegxr.so.0.1 "$TARGET_PATH/lib/libjpegxr.so.0"
ln -sf libjpegxr.so.0 "$TARGET_PATH/lib/libjpegxr.so"
# shellcheck disable=SC2086
cc -shared $LDFLAGS -Wl,-soname,libjxrglue.so.0 -o "$TARGET_PATH/lib/libjxrglue.so.0.1" \
  "${glue_objs[@]}" -L"$TARGET_PATH/lib" -ljpegxr -lm
ln -sf libjxrglue.so.0.1 "$TARGET_PATH/lib/libjxrglue.so.0"
ln -sf libjxrglue.so.0 "$TARGET_PATH/lib/libjxrglue.so"
strip --strip-unneeded "$TARGET_PATH"/lib/libjpegxr.so.0.1 "$TARGET_PATH"/lib/libjxrglue.so.0.1
mkdir -p "$TARGET_PATH/include/jxrlib"
install -m 0644 jxrgluelib/JXRGlue.h jxrgluelib/JXRMeta.h image/sys/windowsmediaphoto.h \
  common/include/*.h "$TARGET_PATH/include/jxrlib/"
cat > "$TARGET_PATH/lib/pkgconfig/libjxr.pc" << EOF
prefix=$TARGET_PATH
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: libjxr
Description: JPEG XR codec (jxrlib $JXRLIB_VERSION)
Version: $JXRLIB_VERSION
Libs: -L\${libdir} -ljxrglue -ljpegxr
Libs.private: -lm
Cflags: -I\${includedir}/jxrlib -D__ANSI__ -DDISABLE_PERF_MEASUREMENT
EOF
add_sbom jxrlib "$JXRLIB_VERSION" "cpe:2.3:a:microsoft:jxrlib:$JXRLIB_VERSION:*:*:*:*:*:*:*"

cd /
rm -rf /tmp/jxrlib

# --- License gate --------------------------------------------------------------
test -n "$(ls -A "$TARGET_PATH/share/licenses/jxrlib")"
"$my_dir/check-licenses.sh" "$TARGET_PATH/lib" "$my_dir/versions.sh"
