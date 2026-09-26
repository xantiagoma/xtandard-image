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
    echo "ok   $name (exit $got)"
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

# An injected GPL tarball pinned in versions.sh.
dir="$(new_fixture versions-x265)"
printf 'export X265_VERSION=4.1\nexport X265_SHA256=deadbeef\n' >> "$dir/versions.sh"
expect 1 versions-x265 "$dir"

dir="$(new_fixture versions-imagequant)"
printf 'export LIBIMAGEQUANT_VERSION=4.3.4\n' >> "$dir/versions.sh"
expect 1 versions-imagequant "$dir"

# The real pinned versions file must pass.
mkdir -p "$work/empty-lib"
if "$gate" "$work/empty-lib" "$my_dir/versions.sh" > /dev/null; then
  echo "ok   real-versions.sh"
else
  echo "FAIL real-versions.sh contains a forbidden component" >&2
  fail=1
fi

exit "$fail"
