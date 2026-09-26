#!/bin/bash
# Self-test of check-licenses.sh: a clean tree passes, every kind of injected GPL
# component is rejected. Runs anywhere with bash (CI job `license-gate`).

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
gate="$my_dir/check-licenses.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail=0

# new_fixture name: a lib dir that resembles a clean build plus a clean versions file.
new_fixture() {
  local dir="$work/$1"
  mkdir -p "$dir/lib/pkgconfig"
  printf '\x7fELF fake libvips NEEDED libheif.so.1 libquantizr.so\n' > "$dir/lib/libvips.so.42"
  printf 'Name: vips\nRequires.private: libheif quantizr spng\n' > "$dir/lib/pkgconfig/vips.pc"
  printf 'export VIPS_VERSION=8.18.6\nexport LIBHEIF_VERSION=1.23.5\nexport KVAZAAR_VERSION=2.3.2\n' > "$dir/versions.sh"
  echo "$dir"
}

expect() {
  local want=$1 name=$2 dir=$3 got=0
  "$gate" "$dir/lib" "$dir/versions.sh" > "$work/$name.log" 2>&1 || got=$?
  if [ "$got" -ne "$want" ]; then
    echo "FAIL $name: exit $got, want $want" >&2
    cat "$work/$name.log" >&2
    fail=1
  else
    echo "ok   $name (exit $got)$(grep -m1 -o 'FORBIDDEN.*' "$work/$name.log" | sed 's/^/: /')"
  fi
}

dir="$(new_fixture clean)"
expect 0 clean "$dir"

dir="$(new_fixture x265-file)"
printf '\x7fELF\n' > "$dir/lib/libx265.so.215"
expect 1 x265-file "$dir"

dir="$(new_fixture poppler-needed)"
printf '\x7fELF NEEDED libpoppler-glib.so.8\n' > "$dir/lib/libfoo.so.1"
expect 1 poppler-needed "$dir"

dir="$(new_fixture exiv2-symbol)"
printf '\x7fELF Exiv2::ImageFactory::open\n' > "$dir/lib/libbar.so.1"
expect 1 exiv2-symbol "$dir"

dir="$(new_fixture imagequant-file)"
printf '\x7fELF\n' > "$dir/lib/libimagequant.so.0"
expect 1 imagequant-file "$dir"

dir="$(new_fixture ffmpeg-gpl)"
printf '\x7fELF configuration: --enable-shared --enable-gpl --enable-libx265\n' > "$dir/lib/libavcodec.so.62"
expect 1 ffmpeg-gpl "$dir"

dir="$(new_fixture pc-requires)"
printf 'Name: heif\nRequires.private: x265 libde265\n' > "$dir/lib/pkgconfig/libheif.pc"
expect 1 pc-requires "$dir"

# WP-66 extras (LibRaw, PDFium, ImageMagick, OpenEXR) are allowed...
dir="$(new_fixture extras-clean)"
printf '\x7fELF NEEDED libraw_r.so.24 libpdfium.so libOpenEXR-3_4.so.33\n' > "$dir/lib/libvips.so.42"
mkdir -p "$dir/lib/vips-modules-8.18"
printf '\x7fELF NEEDED libMagickCore-7.Q16HDRI.so.10\n' > "$dir/lib/vips-modules-8.18/vips-magick.so"
printf '\x7fELF\n' > "$dir/lib/libMagickCore-7.Q16HDRI.so.10"
printf 'export LIBRAW_VERSION=0.22.2\nexport PDFIUM_VERSION=8066\nexport IMAGEMAGICK_VERSION=7.1.2-31\n' >> "$dir/versions.sh"
expect 0 extras-clean "$dir"

# ...but not ImageMagick built with GPL delegates, or LibRaw's GPL demosaic packs.
dir="$(new_fixture magick-fftw)"
printf '\x7fELF NEEDED libfftw3.so.3\n' > "$dir/lib/libMagickCore-7.Q16HDRI.so.10"
expect 1 magick-fftw "$dir"

dir="$(new_fixture magick-djvu)"
printf '\x7fELF ddjvu_document_create_by_filename\n' > "$dir/lib/libMagickCore-7.Q16HDRI.so.10"
expect 1 magick-djvu "$dir"

dir="$(new_fixture versions-demosaic-gpl)"
printf 'export LIBRAW_DEMOSAIC_PACK_GPL3_VERSION=0.18.8\n' >> "$dir/versions.sh"
expect 1 versions-demosaic-gpl "$dir"

# An injected GPL tarball pinned in versions.sh.
dir="$(new_fixture versions-x265)"
printf 'export X265_VERSION=4.1\nexport X265_SHA256=deadbeef\n' >> "$dir/versions.sh"
expect 1 versions-x265 "$dir"

dir="$(new_fixture versions-imagequant)"
printf 'export LIBIMAGEQUANT_VERSION=4.3.4\n' >> "$dir/versions.sh"
expect 1 versions-imagequant "$dir"

# --- full flavor (WP-70): --ffmpeg, --ldd, --models ----------------------------

# expect_args want name args...: runs the gate with explicit arguments.
expect_args() {
  local want=$1 name=$2 got=0
  shift 2
  "$gate" "$@" > "$work/$name.log" 2>&1 || got=$?
  if [ "$got" -ne "$want" ]; then
    echo "FAIL $name: exit $got, want $want" >&2
    cat "$work/$name.log" >&2
    fail=1
  else
    echo "ok   $name (exit $got)$(grep -m1 -o 'FORBIDDEN.*' "$work/$name.log" | sed 's/^/: /')"
  fi
}

# fake_ffmpeg path buildconf license: a stand-in for `ffmpeg -buildconf` / `ffmpeg -L`.
fake_ffmpeg() {
  cat > "$1" <<EOF
#!/bin/bash
case "\$*" in
  *-buildconf*) printf '  configuration:\n    %s\n' $2 ;;
  *-L*) echo "$3" ;;
esac
EOF
  chmod +x "$1"
}
lgpl='FFmpeg is free software; you can redistribute it and/or modify it under the terms of the GNU Lesser General Public License'
gpl='FFmpeg is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License'

# fake_ldd: prints the NEEDED-like lines of a fixture file as ldd would resolve them.
cat > "$work/fake-ldd" <<'EOF'
#!/bin/bash
grep -aoE 'lib[A-Za-z0-9_+.-]+\.so[.0-9]*( => not found)?' "$1" | sed 's/^/\t/'
EOF
chmod +x "$work/fake-ldd"

# new_models dir: two models with MIT and Apache-2.0 licences and valid checksums.
new_models() {
  local dir=$1
  mkdir -p "$dir"
  printf 'onnx yunet' > "$dir/face_detection_yunet_2023mar.onnx"
  printf 'MIT License\n\nPermission is hereby granted, free of charge, ...\n' > "$dir/face_detection_yunet_2023mar.LICENSE"
  printf 'onnx yolox' > "$dir/yolox_s.onnx"
  printf '                                 Apache License\n                           Version 2.0, January 2004\n' > "$dir/yolox_s.LICENSE"
  (cd "$dir" && if command -v sha256sum > /dev/null; then sha256sum ./*.onnx; else shasum -a 256 ./*.onnx; fi | sed 's| \./| |' > SHA256SUMS)
}

dir="$(new_fixture full-clean)"
printf '\x7fELF NEEDED libavcodec.so.62 libvpx.so.12 libaom.so.3 libdav1d.so.7 configuration: --enable-shared --disable-gpl --disable-nonfree\n' > "$dir/lib/libavformat.so.62"
fake_ffmpeg "$dir/ffmpeg" "--enable-shared --disable-gpl --disable-nonfree --enable-libvpx --enable-libaom --enable-libdav1d" "$lgpl"
new_models "$dir/models"
expect_args 0 full-clean --ffmpeg "$dir/ffmpeg" --models "$dir/models" "$dir/lib" "$dir/versions.sh"
LDD="$work/fake-ldd" expect_args 0 full-clean-ldd --ldd "$dir/lib"

dir="$(new_fixture full-ffmpeg-gpl-buildconf)"
fake_ffmpeg "$dir/ffmpeg" "--enable-shared --enable-gpl --enable-libx264" "$gpl"
expect_args 1 full-ffmpeg-gpl-buildconf --ffmpeg "$dir/ffmpeg" "$dir/lib"

dir="$(new_fixture full-ffmpeg-nonfree)"
fake_ffmpeg "$dir/ffmpeg" "--enable-shared --enable-nonfree" "$lgpl"
expect_args 1 full-ffmpeg-nonfree --ffmpeg "$dir/ffmpeg" "$dir/lib"

dir="$(new_fixture full-ffmpeg-gpl-license)"
fake_ffmpeg "$dir/ffmpeg" "--enable-shared" "$gpl"
expect_args 1 full-ffmpeg-gpl-license --ffmpeg "$dir/ffmpeg" "$dir/lib"

dir="$(new_fixture full-ldd-x264)"
# The x264 dependency only shows up in what ldd resolves (a library outside lib-dir).
printf '\x7fELF\n' > "$dir/lib/libavcodec.so.62"
cat > "$work/fake-ldd-x264" <<'EOF'
#!/bin/bash
printf '\tlibx264.so.164 => /usr/lib/libx264.so.164 (0x0000)\n'
EOF
chmod +x "$work/fake-ldd-x264"
LDD="$work/fake-ldd-x264" expect_args 1 full-ldd-x264 --ldd "$dir/lib"

dir="$(new_fixture full-ldd-not-found)"
printf '\x7fELF NEEDED libvpx.so.12 => not found\n' > "$dir/lib/libavcodec.so.62"
LDD="$work/fake-ldd" expect_args 1 full-ldd-not-found --ldd "$dir/lib"

dir="$(new_fixture full-postproc)"
printf '\x7fELF\n' > "$dir/lib/libpostproc.so.58"
expect_args 1 full-postproc "$dir/lib"

dir="$(new_fixture full-model-no-license)"
new_models "$dir/models"
rm "$dir/models/yolox_s.LICENSE"
expect_args 1 full-model-no-license --models "$dir/models" "$dir/lib"

dir="$(new_fixture full-model-agpl)"
new_models "$dir/models"
printf 'GNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3\nGNU Affero General Public License\n' > "$dir/models/yolox_s.LICENSE"
expect_args 1 full-model-agpl --models "$dir/models" "$dir/lib"

dir="$(new_fixture full-model-noncommercial)"
new_models "$dir/models"
printf 'Free for non-commercial research use.\n' > "$dir/models/yolox_s.LICENSE"
expect_args 1 full-model-noncommercial --models "$dir/models" "$dir/lib"

dir="$(new_fixture full-model-checksum)"
new_models "$dir/models"
printf 'tampered' > "$dir/models/yolox_s.onnx"
expect_args 1 full-model-checksum --models "$dir/models" "$dir/lib"

dir="$(new_fixture full-models-empty)"
mkdir -p "$dir/models"
expect_args 1 full-models-empty --models "$dir/models" "$dir/lib"

dir="$(new_fixture full-versions-nonfree)"
printf 'export FFMPEG_CONFIGURE_EXTRA=--enable-nonfree\n' >> "$dir/versions.sh"
expect 1 full-versions-nonfree "$dir"

# The real pinned versions files must pass.
mkdir -p "$work/empty-lib"
for versions_file in versions.sh versions-full.sh; do
  if "$gate" "$work/empty-lib" "$my_dir/$versions_file" > /dev/null; then
    echo "ok   real-$versions_file"
  else
    echo "FAIL real-$versions_file contains a forbidden component" >&2
    fail=1
  fi
done

# --- gpl flavor (WP-70b): --allow-gpl, --licenses ----------------------------------

# new_gpl_file path [components]: a versions-gpl.sh stand-in.
new_gpl_file() {
  cat > "$1" <<EOF
export X264_VERSION=0.165-b35605ac
export X265_VERSION=4.2
export XI_GPL_COMPONENTS="${2:-x264=GPL-2.0-or-later x265=GPL-2.0-or-later ffmpeg=GPL-2.0-or-later}"
XI_COMPONENT_LICENSES='
vips LGPL-2.1-or-later
ffmpeg GPL-2.0-or-later
x264 GPL-2.0-or-later
x265 GPL-2.0-or-later
libjpeg-turbo IJG AND BSD-3-Clause AND Zlib
'
export XI_COMPONENT_LICENSES
EOF
}

# new_gpl_fixture name: a fixture shaped like the gpl image (x264, x265, GPL FFmpeg),
# with its licence bundle and a clean gpl allowlist.
new_gpl_fixture() {
  local dir
  dir="$(new_fixture "$1")"
  printf '\x7fELF NEEDED libx264.so.165 libx265.so.215 libvpx.so.12 configuration: --enable-shared --enable-gpl --disable-nonfree --enable-libx264 --enable-libx265\n' > "$dir/lib/libavcodec.so.62"
  printf '\x7fELF x264_encoder_open_165\n' > "$dir/lib/libx264.so.165"
  printf '\x7fELF x265_encoder_open_215\n' > "$dir/lib/libx265.so.215"
  printf 'Name: libavcodec\nRequires.private: x264 x265 vpx\n' > "$dir/lib/pkgconfig/libavcodec.pc"
  fake_ffmpeg "$dir/ffmpeg" "--enable-shared --enable-gpl --disable-nonfree --enable-libx264 --enable-libx265 --enable-libvpx" "$gpl"
  mkdir -p "$dir/licenses/vips" "$dir/licenses/ffmpeg" "$dir/licenses/x264" "$dir/licenses/x265" "$dir/licenses/libjpeg-turbo"
  printf '# Sources\n' > "$dir/licenses/SOURCES.md"
  printf '# GPL notice\nThe combined FFmpeg is GPL-2.0-or-later.\n' > "$dir/licenses/LICENSE-GPL.md"
  printf 'export X264_VERSION=0.165\nexport X265_VERSION=4.2\n' >> "$dir/versions.sh"
  new_gpl_file "$dir/versions-gpl.sh"
  echo "$dir"
}

# gpl_args dir: the arguments the gpl build passes to the gate.
gpl_args() {
  echo --allow-gpl "$1/versions-gpl.sh" --licenses "$1/licenses" --ffmpeg "$1/ffmpeg" "$1/lib" "$1/versions.sh" "$1/versions-gpl.sh"
}

dir="$(new_gpl_fixture gpl-clean)"
# shellcheck disable=SC2046
expect_args 0 gpl-clean $(gpl_args "$dir")
LDD="$work/fake-ldd-x264" expect_args 0 gpl-clean-ldd-x264 --allow-gpl "$dir/versions-gpl.sh" --ldd "$dir/lib"
# The very same tree fails without --allow-gpl: slim and full stay strict.
expect_args 1 gpl-tree-strict --ffmpeg "$dir/ffmpeg" "$dir/lib" "$dir/versions.sh"

dir="$(new_gpl_fixture gpl-agpl-entry)"
new_gpl_file "$dir/versions-gpl.sh" "x264=AGPL-3.0-only x265=GPL-2.0-or-later ffmpeg=GPL-2.0-or-later"
# shellcheck disable=SC2046
expect_args 1 gpl-agpl-entry $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-unknown-licence-entry)"
new_gpl_file "$dir/versions-gpl.sh" "x264=GPL-2.0-or-later x265=WTFPL-ish ffmpeg=GPL-2.0-or-later"
# shellcheck disable=SC2046
expect_args 1 gpl-unknown-licence-entry $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-poppler-entry)"
new_gpl_file "$dir/versions-gpl.sh" "x264=GPL-2.0-or-later x265=GPL-2.0-or-later ffmpeg=GPL-2.0-or-later poppler=GPL-2.0-or-later"
printf '\x7fELF\n' > "$dir/lib/libpoppler.so.140"
# shellcheck disable=SC2046
expect_args 1 gpl-poppler-entry $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-unlisted-x265)"
new_gpl_file "$dir/versions-gpl.sh" "x264=GPL-2.0-or-later ffmpeg=GPL-2.0-or-later"
# shellcheck disable=SC2046
expect_args 1 gpl-unlisted-x265 $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-nonfree)"
fake_ffmpeg "$dir/ffmpeg" "--enable-shared --enable-gpl --enable-nonfree --enable-libx264" "$gpl"
# shellcheck disable=SC2046
expect_args 1 gpl-nonfree $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-version3)"
fake_ffmpeg "$dir/ffmpeg" "--enable-shared --enable-gpl --enable-version3 --enable-libx264" "$gpl"
# shellcheck disable=SC2046
expect_args 1 gpl-version3 $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-ffmpeg-not-gpl)"
fake_ffmpeg "$dir/ffmpeg" "--enable-shared --disable-gpl" "$lgpl"
# shellcheck disable=SC2046
expect_args 1 gpl-ffmpeg-not-gpl $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-postproc)"
printf '\x7fELF\n' > "$dir/lib/libpostproc.so.58"
# shellcheck disable=SC2046
expect_args 1 gpl-postproc $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-xvid)"
printf '\x7fELF NEEDED libxvidcore.so.4\n' > "$dir/lib/libavformat.so.62"
# shellcheck disable=SC2046
expect_args 1 gpl-xvid $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-bundle-unknown)"
mkdir -p "$dir/licenses/mystery-codec"
# shellcheck disable=SC2046
expect_args 1 gpl-bundle-unknown $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-bundle-agpl)"
printf "XI_COMPONENT_LICENSES=\"\$XI_COMPONENT_LICENSES\nmystery-codec AGPL-3.0-or-later\n\"\n" >> "$dir/versions-gpl.sh"
mkdir -p "$dir/licenses/mystery-codec"
# shellcheck disable=SC2046
expect_args 1 gpl-bundle-agpl $(gpl_args "$dir")

dir="$(new_gpl_fixture gpl-no-notice)"
rm "$dir/licenses/LICENSE-GPL.md"
# shellcheck disable=SC2046
expect_args 1 gpl-no-notice $(gpl_args "$dir")

dir="$(new_fixture gpl-licenses-needs-allow)"
mkdir -p "$dir/licenses"
expect_args 2 gpl-licenses-needs-allow --licenses "$dir/licenses" "$dir/lib"

# The real versions-gpl.sh is refused by the strict gate and accepted by --allow-gpl.
if "$gate" "$work/empty-lib" "$my_dir/versions-gpl.sh" > /dev/null 2>&1; then
  echo "FAIL real-versions-gpl.sh passes the strict gate" >&2
  fail=1
else
  echo "ok   real-versions-gpl.sh refused by the strict gate"
fi
if "$gate" --allow-gpl "$my_dir/versions-gpl.sh" "$work/empty-lib" "$my_dir/versions.sh" \
  "$my_dir/versions-full.sh" "$my_dir/versions-gpl.sh" > /dev/null; then
  echo "ok   real-versions-gpl.sh with --allow-gpl"
else
  echo "FAIL real-versions-gpl.sh with --allow-gpl" >&2
  fail=1
fi

exit "$fail"
