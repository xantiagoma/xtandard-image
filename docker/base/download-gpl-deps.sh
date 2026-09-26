#!/bin/bash
# Downloads, verifies (SHA256) and unpacks the sources the `gpl` flavor (WP-70b) adds:
# x264 and x265 (versions-gpl.sh). FFmpeg itself is the full flavor's tarball
# (versions-full.sh), reused from the full-deps-src stage.
#
# Same conventions as download-full-deps.sh (kept separate so the slim and full stages
# never change when a gpl-only pin moves).
#
# Environment:
#   GPL_DEPS_SRC      where sources are unpacked (default /root/gpl-deps)
#   PRINT_CHECKSUMS=1 do not verify or unpack; print NAME_SHA256=<sum> lines instead
#                     (used by update-checksums.sh)

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions-gpl.sh
source "$my_dir/versions-gpl.sh"

GPL_DEPS_SRC=${GPL_DEPS_SRC:-/root/gpl-deps}
PRINT_CHECKSUMS=${PRINT_CHECKSUMS:-0}
mkdir -p "$GPL_DEPS_SRC"

sha256() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# fetch sha-var dir url: download, verify against ${!sha-var} and unpack a tarball
# (strip 1 component); with PRINT_CHECKSUMS=1 print the checksum line instead.
fetch() {
  local sha_var=$1 dir=$2 url=$3 expected=${!1:-} file actual
  file="$(mktemp "${TMPDIR:-/tmp}/xi-gpl.XXXXXX")"
  echo "== Download $url" >&2
  curl -fsSL --retry 5 --retry-delay 2 -o "$file" "$url"
  actual="$(sha256 "$file")"
  if [ "$PRINT_CHECKSUMS" = "1" ]; then
    echo "${sha_var}=${actual}"
    rm -f "$file"
    return
  fi
  if [ "$actual" != "$expected" ]; then
    echo "SHA256 mismatch for $url" >&2
    echo "  expected: ${expected:-<empty>}" >&2
    echo "  actual:   $actual" >&2
    rm -f "$file"
    exit 1
  fi
  echo "| $(basename "$url") | $url | \`$actual\` |" >> "$GPL_DEPS_SRC/SOURCES.rows"
  rm -rf "${GPL_DEPS_SRC:?}/$dir"
  mkdir -p "$GPL_DEPS_SRC/$dir"
  tar -xf "$file" -C "$GPL_DEPS_SRC/$dir" --strip-components=1
  rm -f "$file"
}

if [ "$PRINT_CHECKSUMS" != "1" ]; then
  : > "$GPL_DEPS_SRC/SOURCES.rows"
fi

fetch X264_SHA256 x264 \
  "https://code.videolan.org/videolan/x264/-/archive/${X264_COMMIT}/x264-${X264_COMMIT}.tar.bz2"
fetch X265_SHA256 x265 \
  "https://download.videolan.org/pub/videolan/x265/x265_${X265_VERSION}.tar.gz"
