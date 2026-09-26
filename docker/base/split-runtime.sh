#!/bin/bash
# Copies the part of /opt/xtandard that a runtime image needs into /runtime/opt/xtandard
# and runs the licence gate on it. Headers, pkg-config/CMake files, gettext/glib data,
# the plain (non thread-safe) libraw and every program except the named ones are left out.
#
# Usage: split-runtime.sh <program>...   e.g. `vips vipsheader` (slim),
#                                          `vips vipsheader ffmpeg ffprobe` (full)

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
src=${XI_PREFIX:-/opt/xtandard}
dst=/runtime$src

rm -rf /runtime
mkdir -p "$dst"
cp -a "$src/lib" "$src/bin" "$src/etc" "$src/share" "$dst/"
rm -rf "$dst/lib/pkgconfig" "$dst/lib/cmake" "$dst/lib/glib-2.0" "$dst/share/pkgconfig" \
  "$dst/share/aclocal" "$dst/share/gettext" "$dst/share/glib-2.0"
# libvips links only the thread-safe libraw_r.
rm -f "$dst"/lib/libraw.so*

keep=()
for program in "$@"; do
  keep+=(! -name "$program")
done
find "$dst/bin" -type f "${keep[@]}" -delete

"$my_dir/check-licenses.sh" "$dst/lib"
