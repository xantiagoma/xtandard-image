#!/bin/bash
# Builds the `gpl` flavor additions (WP-70b) on top of the finished full /opt/xtandard:
#   - x264 (GPL-2.0-or-later): H.264 encoder library, 8- and 10-bit, no CLI
#   - x265 (GPL-2.0-or-later): HEVC encoder library, 8-bit Main, no CLI
#   - FFmpeg rebuilt over the full one with exactly the full configuration (read back
#     from share/licenses/ffmpeg/BUILDCONF.txt) plus --enable-gpl, --enable-libx264,
#     --enable-libx265 and the libx264/libx265 encoders; the result is GPL-2.0-or-later
#   - Ghostscript (AGPL-3.0-or-later, WP-99): the `gs` program alone, static, minimal
#     devices, run by the server as a sandboxed subprocess when EPS input is enabled
# then extends the licence bundle and SOURCES.md, writes share/licenses/LICENSE-GPL.md
# and runs the licence gate in its gpl mode (--allow-gpl versions-gpl.sh).
#
# Runs in the `gpl-deps` stage, which starts from `full-deps` (BASH_ENV=bashrc.sh sets
# the flags). The FFmpeg source tree is the full flavor's pinned tarball.

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"
# shellcheck source=versions-full.sh
source "$my_dir/versions-full.sh"
# shellcheck source=versions-gpl.sh
source "$my_dir/versions-gpl.sh"

GPL_DEPS_SRC=${GPL_DEPS_SRC:-/root/gpl-deps}
TARGET_PATH=${XI_PREFIX:-/opt/xtandard}
ORT_PREFIX=${ORT_PREFIX:-/usr/local}
MODELS_DIR=${MODELS_DIR:-/usr/share/xtandard/models}
LICENSES="$TARGET_PATH/share/licenses"

print_build_stage() {
  echo ""
  echo "== Build $1 $2 ====================================="
  echo ""
}

# add_sbom name version cpe license: CycloneDX stub, same format as build-deps.sh plus
# the licence.
add_sbom() {
  local name=$1 version=$2 cpe=$3 license=$4
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
      "licenses": [ { "expression": "$license" } ],
      "properties": [
        { "name": "syft:package:type", "value": "binary" }
      ]
    }
  }
}
EOF
}

print_build_stage x264 "$X264_VERSION"
cd "$GPL_DEPS_SRC/x264"
# Library only: no CLI, no input/output helpers (lavf, ffms, gpac, lsmash, swscale),
# no OpenCL lookahead. --bit-depth=all puts the 8- and 10-bit encoders in one library.
# x264 puts $CFLAGS after its own -O3, so the -Os baseline from bashrc.sh is replaced.
CFLAGS="${CFLAGS//-Os/-O3}" ./configure \
  --prefix="$TARGET_PATH" \
  --libdir="$TARGET_PATH/lib" \
  --enable-shared \
  --enable-pic \
  --disable-cli \
  --disable-opencl \
  --disable-avs \
  --disable-swscale \
  --disable-lavf \
  --disable-ffms \
  --disable-gpac \
  --disable-lsmash \
  --bit-depth=all \
  --chroma-format=all
make -j"$(nproc)"
make install
add_sbom x264 "$X264_VERSION" "cpe:2.3:a:videolan:x264:$X264_VERSION:*:*:*:*:*:*:*" GPL-2.0-or-later

print_build_stage x265 "$X265_VERSION"
cd "$GPL_DEPS_SRC/x265"
# 8-bit Main/Main Still Picture only (the GIF->video and HEVC output paths); no CLI,
# no libnuma, no HDR10+ (it would pull in a C++ JSON parser for nothing).
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" \
cmake -S source -B _build \
  -G"Ninja" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$TARGET_PATH" \
  -DCMAKE_INSTALL_LIBDIR=lib \
  -DENABLE_SHARED=ON \
  -DENABLE_CLI=OFF \
  -DENABLE_LIBNUMA=OFF \
  -DENABLE_HDR10_PLUS=OFF \
  -DENABLE_PIC=ON \
  -DENABLE_TESTS=OFF
ninja -C _build install/strip
add_sbom x265 "$X265_VERSION" "cpe:2.3:a:multicorewareinc:x265:$X265_VERSION:*:*:*:*:*:*:*" GPL-2.0-or-later

print_build_stage ffmpeg "$FFMPEG_VERSION (gpl)"
cd "$GPL_DEPS_SRC/ffmpeg"
# The full build's exact configure line (the switches in BUILDCONF.txt; `-buildconf`
# does not always print one per line, so it is split on whitespace), with
# --disable-gpl swapped for --enable-gpl and the two GPL encoders added. Everything
# else (demuxers, decoders, filters, no audio, no network, no nonfree, no version3)
# stays identical to full by construction.
# `-buildconf` shell-quotes values with commas (--enable-demuxer=''\''mov,...'\''');
# no switch value contains a quote or a backslash, so both are stripped.
full_conf=()
while IFS= read -r flag; do
  flag=${flag//\'/}
  flag=${flag//\\/}
  case "$flag" in
    --disable-gpl) full_conf+=(--enable-gpl) ;;
    --*) full_conf+=("$flag") ;;
  esac
done < <(tr -s '[:space:]' '\n' < "$LICENSES/ffmpeg/BUILDCONF.txt")
if [ "${#full_conf[@]}" -lt 20 ] || ! printf '%s\n' "${full_conf[@]}" | grep -qx -- --enable-gpl; then
  echo "Could not read the full FFmpeg configuration from $LICENSES/ffmpeg/BUILDCONF.txt" >&2
  exit 1
fi
./configure "${full_conf[@]}" \
  --enable-libx264 \
  --enable-libx265 \
  --enable-encoder=libx264,libx265
make -j"$(nproc)"
# Same component lists as full: every name is a plain identifier (no quoting left over)
# and every decoder, demuxer, muxer, parser, bsf, protocol and encoder of the full build
# is still enabled (filters have no CONFIG_ macro for the built-in buffer/buffersink).
for kind in decoder demuxer muxer parser filter bsf protocol encoder; do
  for name in $(printf '%s\n' "${full_conf[@]}" | sed -n "s/^--enable-$kind=//p" | tr ',' ' '); do
    if [[ ! $name =~ ^[a-z0-9_]+$ ]]; then
      echo "FFmpeg gpl build: unexpected $kind name '$name' read from BUILDCONF.txt" >&2
      exit 1
    fi
    [ "$kind" = filter ] && continue
    if ! grep -qE "^#define CONFIG_${name^^}_${kind^^} 1" config.h config_components.h 2>/dev/null; then
      echo "FFmpeg gpl build lost $kind $name from the full configuration" >&2
      exit 1
    fi
  done
done
make install
add_sbom ffmpeg "$FFMPEG_VERSION" "cpe:2.3:a:ffmpeg:ffmpeg:$FFMPEG_VERSION:*:*:*:*:*:*:*" GPL-2.0-or-later

print_build_stage ghostscript "$GHOSTSCRIPT_VERSION"
cd "$GPL_DEPS_SRC/ghostscript"
# One program, /opt/xtandard/bin/gs (WP-99, ADR 0011): the PostScript interpreter run by
# the server as a sandboxed subprocess, never linked. Only the raster devices the server
# uses (pngalpha, png16m) and bbox; no X11, CUPS, dbus, GTK, fontconfig, libpaper,
# IJS, threads, contributed drivers, PDF interpreter (PDF is PDFium's), JBIG2, JPX,
# TIFF or OCR. Its own copies of FreeType, libjpeg, libpng, zlib, lcms2mt and brotli are
# linked statically, and its resources and bundled fonts are compiled in (%rom%), so gs
# needs nothing but libc and reads no font or resource from the file system. The
# sources it must not use are removed so configure cannot pick them up, and the build
# ignores /opt/xtandard's headers, libraries and pkg-config files.
rm -rf cups ijs jbig2dec openjpeg tesseract leptonica tiff extract
env -u CPPFLAGS -u CXXFLAGS PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR=/nonexistent \
  CFLAGS="${CFLAGS//-fPIC/} -D_FORTIFY_SOURCE=2" LDFLAGS="-Wl,--gc-sections -Wl,-z,relro,-z,now" \
  ./configure \
    --prefix="$TARGET_PATH" \
    --without-x \
    --disable-cups \
    --disable-dbus \
    --disable-gtk \
    --disable-fontconfig \
    --disable-threading \
    --disable-contrib \
    --disable-openjpeg \
    --without-libpaper \
    --without-libidn \
    --with-libiconv=no \
    --without-ijs \
    --without-jbig2dec \
    --without-pdf \
    --without-libtiff \
    --without-tesseract \
    --without-pdftoraster \
    --without-urf \
    --without-so \
    --without-cal \
    --with-fontpath= \
    --with-drivers=png16m,pngalpha,bbox \
    --enable-mkromfs-quiet
env -u CPPFLAGS -u CXXFLAGS make -j"$(nproc)" gs
install -m 0755 bin/gs "$TARGET_PATH/bin/gs"
strip --strip-all "$TARGET_PATH/bin/gs"
# The device list is exactly what the server needs, and nothing but libc is linked.
devices="$("$TARGET_PATH/bin/gs" -h | sed -n '/^Available devices:/,/^Search path:/p' | sed '1d;$d' | xargs)"
if [ "$devices" != "bbox nullpage png16m pngalpha" ]; then
  echo "gs has unexpected devices: $devices" >&2
  exit 1
fi
if ldd "$TARGET_PATH/bin/gs" | grep -vE '(linux-vdso|libc\.so|libm\.so|ld-linux)'; then
  echo "gs links more than libc" >&2
  exit 1
fi
add_sbom ghostscript "$GHOSTSCRIPT_VERSION" \
  "cpe:2.3:a:artifex:ghostscript:$GHOSTSCRIPT_VERSION:*:*:*:*:*:*:*" AGPL-3.0-or-later

# --- Cleanup (as in build-full-deps.sh) -------------------------------------------
rm -rf "$TARGET_PATH"/lib/*.a "$TARGET_PATH"/lib/*.la
rm -rf "$TARGET_PATH"/share/{doc,man,ffmpeg}
find "$TARGET_PATH/bin" -type f ! -name 'vips*' ! -name ffmpeg ! -name ffprobe ! -name gs -delete
find "$TARGET_PATH/lib" -maxdepth 1 -type f -name 'libx264.so.*' -exec strip --strip-unneeded {} +

# --- Licences, SOURCES.md and the GPL notice ------------------------------------------
for name in x264 x265; do
  mkdir -p "$LICENSES/$name"
  cp "$GPL_DEPS_SRC/$name/COPYING" "$LICENSES/$name/"
done
# The FFmpeg libraries are GPL now: ship the GPL text next to the LGPL one.
cp "$GPL_DEPS_SRC/ffmpeg/COPYING.GPLv2" "$LICENSES/ffmpeg/"
# Ghostscript: its licence statement, the AGPL-3.0 text, and the licences of what is
# compiled into gs (FreeType, IJG libjpeg, libpng, zlib, lcms2mt, brotli, the Droid
# Sans Fallback font, Adobe's CMaps).
gs_src="$GPL_DEPS_SRC/ghostscript"
mkdir -p "$LICENSES/ghostscript"
cp "$gs_src/LICENSE" "$LICENSES/ghostscript/LICENSE"
cp "$gs_src/doc/COPYING" "$LICENSES/ghostscript/COPYING.AGPLv3"
cp "$gs_src/DroidSansFallback.NOTICE" "$LICENSES/ghostscript/DroidSansFallback.NOTICE"
cp "$gs_src/freetype/docs/FTL.TXT" "$LICENSES/ghostscript/freetype-FTL.TXT"
cp "$gs_src/jpeg/README" "$LICENSES/ghostscript/libjpeg-README"
cp "$gs_src/libpng/LICENSE" "$LICENSES/ghostscript/libpng-LICENSE"
cp "$gs_src/zlib/LICENSE" "$LICENSES/ghostscript/zlib-LICENSE"
cp "$gs_src/lcms2mt/COPYING" "$LICENSES/ghostscript/lcms2mt-COPYING"
cp "$gs_src/brotli/LICENSE" "$LICENSES/ghostscript/brotli-LICENSE"
sed -n '/%%Copyright/p' "$gs_src/Resource/CMap/Identity-H" > "$LICENSES/ghostscript/Adobe-CMaps-LICENSE"
{
  echo "Ghostscript ${GHOSTSCRIPT_VERSION}, unmodified, built by docker/base/build-gpl-deps.sh"
  echo "(xtandard-image gpl flavor revision ${XI_BASE_GPL_REVISION}) as /opt/xtandard/bin/gs."
  echo "Devices: $devices"
  echo ""
  "$TARGET_PATH/bin/gs" -h
} > "$LICENSES/ghostscript/BUILD.txt"
"$TARGET_PATH/bin/ffmpeg" -hide_banner -buildconf > "$LICENSES/ffmpeg/BUILDCONF.txt"
{
  echo ""
  echo "## gpl flavor (revision ${XI_BASE_GPL_REVISION})"
  echo ""
  echo "Added by docker/base/build-gpl-deps.sh (versions-gpl.sh). x264 and x265 are"
  echo "GPL-2.0-or-later and FFmpeg is rebuilt from the tarball above with --enable-gpl,"
  echo "so the FFmpeg libraries and CLIs in this image are GPL-2.0-or-later"
  echo "(share/licenses/LICENSE-GPL.md). Ghostscript (bin/gs) is built unmodified from"
  echo "its release tarball below and is AGPL-3.0-or-later."
  echo ""
  echo "| File | Source | SHA256 |"
  echo "|---|---|---|"
  cat "$GPL_DEPS_SRC/SOURCES.rows"
} >> "$LICENSES/SOURCES.md"

notice="$LICENSES/LICENSE-GPL.md"
{
  cat << EOF
# xtandard-image base image, gpl flavor (revision ${XI_BASE_GPL_REVISION})

This image contains GPL software. As distributed, the FFmpeg libraries
(\`libavcodec\`, \`libavformat\`, \`libavutil\`, \`libswscale\`, \`libavfilter\`) and the
\`ffmpeg\`/\`ffprobe\` programs in \`${TARGET_PATH}\` are one combined work under the
**GNU General Public License, version 2 or later (GPL-2.0-or-later)**: FFmpeg
${FFMPEG_VERSION} is configured with \`--enable-gpl\` and links x264 and x265, which are
GPL-2.0-or-later. Anything that links these libraries and is distributed must comply
with the GPL. The full configuration is in \`ffmpeg/BUILDCONF.txt\`; the licence texts
are \`ffmpeg/COPYING.GPLv2\`, \`x264/COPYING\` and \`x265/COPYING\`.

It also contains **Ghostscript ${GHOSTSCRIPT_VERSION}**, the program
\`${TARGET_PATH}/bin/gs\`, under the **GNU Affero General Public License, version 3 or
later (AGPL-3.0-or-later)** (\`ghostscript/LICENSE\`, \`ghostscript/COPYING.AGPLv3\`). It
is a separate program: nothing links it, and the xtandard-image server only runs it as
a subprocess, and only when \`XI_FORMATS_EPS_ENABLED=true\` (off by default). It is built
unmodified from the release tarball in \`SOURCES.md\` (\`ghostscript/BUILD.txt\` lists
its configuration). Whoever distributes this image, or runs it for users over a network
with EPS rendering enabled, must be able to offer that corresponding source; changing
Ghostscript brings AGPL section 13's obligation to offer the modified source to every
network user. The ADR docs/adr/0011-ghostscript-subprocess.md explains the setup.

The xtandard-image source code stays under the MIT License. The slim and full images
contain no GPL or AGPL code; use one of them if you cannot accept these terms.

H.264 and HEVC are covered by patent pools (for example Via LA and Access Advance).
This image does not grant, and its authors do not provide, any patent licence.
Whoever deploys or distributes it is responsible for reviewing the licences and
patents that apply to their use.

Complete corresponding source: every tarball listed with its URL and SHA256 in
\`SOURCES.md\`, and the build scripts in \`docker/base/\` of
https://github.com/xantiagoma/xtandard-image at the revision in the image labels.

## Components

| Component | Licence (SPDX) | Licence files |
|---|---|---|
EOF
  while read -r component expression; do
    [ -n "$component" ] || continue
    [ -e "$LICENSES/$component" ] || continue
    echo "| ${component} | ${expression} | \`${component}\` |"
  done <<< "$XI_COMPONENT_LICENSES"
  cat << EOF

LGPL components are dynamically linked. Models (\`models/\`) are data under their own
licences, and ONNX Runtime (\`onnxruntime/\`) is loaded from ${ORT_PREFIX}/lib.
EOF
} > "$notice"

# --- Trees for the final images ------------------------------------------------------
# /full-extra (ONNX Runtime and models) comes unchanged from full-deps.

# --- License gate (gpl mode) ---------------------------------------------------------
"$my_dir/check-licenses.sh" --allow-gpl "$my_dir/versions-gpl.sh" --licenses "$LICENSES" \
  --ffmpeg "$TARGET_PATH/bin/ffmpeg" --ghostscript "$TARGET_PATH/bin/gs" --ldd --models "$MODELS_DIR" \
  "$TARGET_PATH/lib" "$my_dir/versions.sh" "$my_dir/versions-full.sh" "$my_dir/versions-gpl.sh"
"$my_dir/check-licenses.sh" --ldd "$ORT_PREFIX/lib"
