#!/bin/bash
# Downloads, verifies (SHA256) and unpacks the SDKs LibRaw is built with (WP-103):
# the Adobe DNG SDK and the GoPro GPR SDK (versions-gpl.sh). LibRaw itself, and its X3F
# tools, come from download-deps.sh. It runs in its own stage (libraw-deps-src), which
# only the gpl-deps stage copies from (owner decision 2026-09-28: the DNG SDK ships in
# the gpl flavor only), so the 80 MB DNG SDK zip never enters any image and slim and
# full never depend on it: only the parts `build-libraw.sh --with-sdks` compiles, and
# the licence texts, are kept.
#
# Environment:
#   LIBRAW_DEPS_SRC   where sources are unpacked (default /root/libraw-deps)
#   PRINT_CHECKSUMS=1 do not verify or unpack; print NAME_SHA256=<sum> lines instead
#                     (used by update-checksums.sh)

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions-gpl.sh
source "$my_dir/versions-gpl.sh"

LIBRAW_DEPS_SRC=${LIBRAW_DEPS_SRC:-/root/libraw-deps}
PRINT_CHECKSUMS=${PRINT_CHECKSUMS:-0}

sha256() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# fetch sha-var name version url: download into $archive and verify it against
# ${!sha-var}; with PRINT_CHECKSUMS=1 print the checksum line and return 1.
archive=
fetch() {
  local sha_var=$1 name=$2 version=$3 url=$4 expected=${!1:-} actual
  archive="$(mktemp "${TMPDIR:-/tmp}/xi-libraw.XXXXXX")"
  echo "== Download $name $version" >&2
  curl -fsSL --retry 5 --retry-delay 2 -o "$archive" "$url"
  actual="$(sha256 "$archive")"
  if [ "$PRINT_CHECKSUMS" = "1" ]; then
    echo "${sha_var}=${actual}"
    rm -f "$archive"
    return 1
  fi
  if [ "$actual" != "$expected" ]; then
    echo "SHA256 mismatch for $name $version ($url)" >&2
    echo "  expected: ${expected:-<empty>}" >&2
    echo "  actual:   $actual" >&2
    rm -f "$archive"
    exit 1
  fi
  echo "| $name | $version | $url | \`$actual\` |" >> "$LIBRAW_DEPS_SRC/SOURCES.rows"
}

if [ "$PRINT_CHECKSUMS" != "1" ]; then
  mkdir -p "$LIBRAW_DEPS_SRC"
  : > "$LIBRAW_DEPS_SRC/SOURCES.rows"
fi

# Adobe DNG SDK: the SDK sources and its licence. The bundled libjxl, XMP SDK and
# libjpeg copies are not used (the base's libjxl and libjpeg-turbo are; XMP is off).
if fetch DNGSDK_SHA256 dng-sdk "$DNGSDK_VERSION" \
  "https://download.adobe.com/pub/adobe/dng/${DNGSDK_ARCHIVE}"; then
  rm -rf "${LIBRAW_DEPS_SRC:?}/dng-sdk" /tmp/xi-dngsdk
  mkdir -p "$LIBRAW_DEPS_SRC/dng-sdk" /tmp/xi-dngsdk
  unzip -q "$archive" 'dng_sdk_1_7_1/LICENSE.txt' 'dng_sdk_1_7_1/DNG_ReadMe.txt' \
    'dng_sdk_1_7_1/dng_sdk/source/*' -d /tmp/xi-dngsdk
  mv /tmp/xi-dngsdk/dng_sdk_1_7_1/LICENSE.txt /tmp/xi-dngsdk/dng_sdk_1_7_1/DNG_ReadMe.txt \
    /tmp/xi-dngsdk/dng_sdk_1_7_1/dng_sdk/source "$LIBRAW_DEPS_SRC/dng-sdk/"
  rm -rf /tmp/xi-dngsdk "$archive"
fi

# GoPro GPR SDK: only the VC-5 decoder and the DNG reader glue LibRaw calls.
if fetch GPR_SHA256 gpr "$GPR_VERSION" \
  "https://github.com/gopro/gpr/archive/${GPR_COMMIT}.tar.gz"; then
  rm -rf "${LIBRAW_DEPS_SRC:?}/gpr"
  mkdir -p "$LIBRAW_DEPS_SRC/gpr"
  tar -xf "$archive" -C "$LIBRAW_DEPS_SRC/gpr" --strip-components=1 \
    "gpr-${GPR_COMMIT}/LICENSE.txt" "gpr-${GPR_COMMIT}/LICENSE-APACHE" \
    "gpr-${GPR_COMMIT}/LICENSE-MIT" "gpr-${GPR_COMMIT}/AUTHORS.md" \
    "gpr-${GPR_COMMIT}/source/lib/common" "gpr-${GPR_COMMIT}/source/lib/vc5_common" \
    "gpr-${GPR_COMMIT}/source/lib/vc5_decoder" "gpr-${GPR_COMMIT}/source/lib/gpr_sdk"
  rm -f "$archive"
fi
