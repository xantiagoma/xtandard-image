#!/bin/bash
# Builds WMF and EMF input for the `gpl` flavor (WP-108) into /opt/xtandard:
#   - libwmf (LGPL-2.0-or-later): WMF parser and SVG device; no X11, no plot; with
#     the 13 URW Type 1 fonts it installs (GPL), which it needs for text metrics; its
#     SVG device patched (patches/libwmf: SVG namespace, no DOCTYPE, exact colours
#     instead of the closest named colour, text escaped as XML)
#   - libemf2svg (GPL-2.0-only, with its vendored libUEMF): EMF (and EMF+) to SVG,
#     the library only
#   - xi-metafile2svg (docker/base/metafile, MIT source): the program the server runs as
#     a sandboxed subprocess, metafile on stdin, SVG on stdout (internal/metafile)
# and adds their licence texts to the bundle. It runs in the `gpl-deps` stage before
# build-gpl-deps.sh, which lists them in LICENSE-GPL.md and runs the gpl licence gate;
# slim and full never see these components (the strict gate refuses libemf2svg).
#
# Runs with BASH_ENV=bashrc.sh (compiler flags).

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"
# shellcheck source=versions-gpl.sh
source "$my_dir/versions-gpl.sh"

GPL_DEPS_SRC=${GPL_DEPS_SRC:-/root/gpl-deps}
TARGET_PATH=${XI_PREFIX:-/opt/xtandard}
LICENSES="$TARGET_PATH/share/licenses"
WMF_FONTS="$TARGET_PATH/share/libwmf/fonts"

print_build_stage() {
  echo ""
  echo "== Build $1 $2 ====================================="
  echo ""
}

# add_sbom name version cpe license: CycloneDX stub, same format as build-gpl-deps.sh.
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

print_build_stage libwmf "$LIBWMF_VERSION"
rm -rf /tmp/libwmf
cp -a "$GPL_DEPS_SRC/libwmf" /tmp/libwmf
cd /tmp/libwmf
for patch in "$my_dir"/patches/libwmf/*.patch; do
  git apply --verbose "$patch"
done
autoreconf -fi
# The heavy build (libwmf + libwmflite) for the SVG device; its bundled gd writes the
# PNGs of embedded bitmaps (inline in the SVG). No X11, no GNU plotutils, expat for the
# XML fontmap. libwmf reads its own fonts and fontmap from $WMF_FONTS (text metrics;
# a conversion fails without them) and no system or Ghostscript fontmap.
./configure \
  --prefix="$TARGET_PATH" \
  --enable-shared \
  --disable-static \
  --disable-dependency-tracking \
  --without-x \
  --disable-pixbuf \
  --with-expat="$TARGET_PATH" \
  --without-libxml2 \
  --with-freetype="$TARGET_PATH" \
  --with-png="$TARGET_PATH" \
  --with-jpeg="$TARGET_PATH" \
  --with-zlib="$TARGET_PATH" \
  --with-fontdir="$WMF_FONTS" \
  --with-sysfontmap=/nonexistent \
  --with-gsfontmap=/nonexistent \
  --with-gsfontdir=/nonexistent
make -j"$(nproc)"
make install-strip
# No programs or config scripts from libwmf: its two libraries, headers and fonts.
rm -rf "$TARGET_PATH"/bin/wmf2* "$TARGET_PATH/bin/libwmf-fontmap" "$TARGET_PATH/bin/libwmf-config"
test -r "$WMF_FONTS/fontmap" && test -r "$WMF_FONTS/NimbusSans-Regular.t1"
mkdir -p "$LICENSES/libwmf"
cp COPYING "$LICENSES/libwmf/COPYING"
cp fonts/LICENSE "$LICENSES/libwmf/fonts-LICENSE"
add_sbom libwmf "$LIBWMF_VERSION" "cpe:2.3:a:wvware:libwmf:$LIBWMF_VERSION:*:*:*:*:*:*:*" \
  "LGPL-2.0-or-later AND GPL-2.0-or-later"
cd /
rm -rf /tmp/libwmf

print_build_stage libemf2svg "$LIBEMF2SVG_VERSION"
rm -rf /tmp/libemf2svg
cp -a "$GPL_DEPS_SRC/libemf2svg" /tmp/libemf2svg
cd /tmp/libemf2svg
# The library only (LONLY): no emf2svg-conv, no tests; libUEMF from the vendored copy.
cmake -S . -B _build -G"Ninja" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$TARGET_PATH" \
  -DCMAKE_INSTALL_LIBDIR=lib \
  -DCMAKE_PREFIX_PATH="$TARGET_PATH" \
  -DLONLY=ON \
  -DUNITTEST=OFF \
  -DUSE_SYSTEM_LIBUEMF=OFF
ninja -C _build install/strip
mkdir -p "$LICENSES/libemf2svg"
cp LICENSE "$LICENSES/libemf2svg/LICENSE"
cp vendor/libuemf/COPYING "$LICENSES/libemf2svg/libUEMF-COPYING"
add_sbom libemf2svg "$LIBEMF2SVG_VERSION" "cpe:2.3:a:kakwa:libemf2svg:$LIBEMF2SVG_VERSION:*:*:*:*:*:*:*" GPL-2.0-only
cd /
rm -rf /tmp/libemf2svg

print_build_stage xi-metafile2svg "$LIBWMF_VERSION/$LIBEMF2SVG_VERSION"
# shellcheck disable=SC2086,SC2046 # CFLAGS, LDFLAGS and pkg-config output are lists of flags
cc $CFLAGS $LDFLAGS -O2 -Wall -Werror -o "$TARGET_PATH/bin/xi-metafile2svg" "$my_dir/metafile/xi-metafile2svg.c" \
  -DXI_METAFILE_VERSION="\"libwmf $LIBWMF_VERSION, libemf2svg $LIBEMF2SVG_VERSION\"" \
  -I"$TARGET_PATH/include" $(pkg-config --cflags freetype2) -L"$TARGET_PATH/lib" -lwmf -lwmflite -lemf2svg -lm
strip --strip-all "$TARGET_PATH/bin/xi-metafile2svg"
"$TARGET_PATH/bin/xi-metafile2svg" --version
# One conversion of each kind from the projects' own samples (text, so the fonts load).
# Output goes to files: `| grep -q` would end the pipe early (SIGPIPE under pipefail).
"$TARGET_PATH/bin/xi-metafile2svg" wmf 10000000 4096 < "$GPL_DEPS_SRC/libwmf/examples/text.wmf" > /tmp/smoke-wmf.svg
"$TARGET_PATH/bin/xi-metafile2svg" emf 10000000 4096 \
  < "$GPL_DEPS_SRC/libemf2svg/vendor/libuemf/test_libuemf_ref.emf" > /tmp/smoke-emf.svg
grep -q 'xmlns="http://www.w3.org/2000/svg"' /tmp/smoke-wmf.svg
grep -q 'xmlns="http://www.w3.org/2000/svg"' /tmp/smoke-emf.svg
rm -f /tmp/smoke-wmf.svg /tmp/smoke-emf.svg
