#!/bin/bash
# License gate (ADR 0006). Fails when a forbidden (GPL/AGPL) component shows up in
# the built library directory or in the pinned versions file.
#
# Usage: check-licenses.sh <lib-dir> [versions.sh]
#
# Checks:
#   1. file names under <lib-dir> (libx265.so*, poppler, exiv2, libimagequant, ...)
#   2. binary contents of every file under <lib-dir>: DT_NEEDED / SONAME strings of
#      forbidden libraries, their well-known entry points, and FFmpeg's
#      "--enable-gpl" configure string
#   3. pkg-config files under <lib-dir> requiring a forbidden package
#   4. every line of versions.sh (a forbidden dependency must never be pinned)
#
# Exit status: 0 when clean, 1 when a forbidden component is found, 2 on usage error.

set -uo pipefail

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "usage: $0 <lib-dir> [versions.sh]" >&2
  exit 2
fi

lib_dir=$1
versions_file=${2:-}

if [ ! -d "$lib_dir" ]; then
  echo "check-licenses: $lib_dir is not a directory" >&2
  exit 2
fi

# Component names that must never appear (case-insensitive):
#   x265, x264 (GPL encoders), poppler (GPL PDF), exiv2 (GPL metadata),
#   libimagequant (GPL-3 since 2.12; use quantizr), jbig-kit (GPL),
#   Ghostscript (AGPL), GPL FFmpeg builds.
name_pattern='x265|x264|poppler|exiv2|imagequant|jbig|ghostscript|libgs\.so|ffmpeg-gpl'

# Strings that only exist inside binaries built from or linked against those components.
content_pattern='libx265\.so|x265_encoder_open|libx264\.so|x264_encoder_open|libpoppler|poppler_document_new|libexiv2|Exiv2::|libimagequant\.so|libjbig|jbg_enc_init|libgs\.so|gsapi_new_instance|--enable-gpl|--enable-libx26[45]'

failures=0

report() {
  echo "check-licenses: FORBIDDEN $1" >&2
  failures=$((failures + 1))
}

# 1. File names.
while IFS= read -r path; do
  report "file name: $path"
done < <(find "$lib_dir" -print | grep -iE "$name_pattern")

# 2. Binary contents (covers DT_NEEDED entries without needing readelf).
while IFS= read -r path; do
  report "content of $path matches: $(grep -aoE -m1 -- "$content_pattern" "$path" | head -n1)"
done < <(find "$lib_dir" -type f -exec grep -laE -- "$content_pattern" {} + 2>/dev/null)

# 3. pkg-config requirements.
while IFS= read -r path; do
  report "pkg-config requirement in $path: $(grep -iE '^Requires' "$path" | grep -ioE "$name_pattern" | head -n1)"
done < <(find "$lib_dir" -type f -name '*.pc' -exec grep -ilE "^Requires.*($name_pattern)" {} + 2>/dev/null)

# 4. versions.sh.
if [ -n "$versions_file" ]; then
  if [ ! -f "$versions_file" ]; then
    echo "check-licenses: $versions_file not found" >&2
    exit 2
  fi
  while IFS= read -r line; do
    report "pinned in $versions_file: $line"
  done < <(grep -inE -- "$name_pattern|enable-gpl" "$versions_file")
fi

if [ "$failures" -gt 0 ]; then
  echo "check-licenses: $failures forbidden component(s) found; see docs/adr/0006-license-policy.md" >&2
  exit 1
fi

echo "check-licenses: OK ($lib_dir${versions_file:+, $versions_file})"
