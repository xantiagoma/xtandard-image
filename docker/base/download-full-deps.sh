#!/bin/bash
# Downloads, verifies (SHA256) and unpacks the components of the `full` flavor
# (WP-70) listed in versions-full.sh: libvpx, FFmpeg, libopus and LAME sources, the prebuilt ONNX
# Runtime for this architecture, and the detection and classification models with
# their licences.
#
# Same conventions as download-deps.sh (kept separate so the slim stages never
# change when a full-only pin moves).
#
# Environment:
#   FULL_DEPS_SRC     where sources are unpacked (default /root/full-deps)
#   PRINT_CHECKSUMS=1 do not verify or unpack; print NAME_SHA256=<sum> lines instead
#                     (used by update-checksums.sh)

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions-full.sh
source "$my_dir/versions-full.sh"

FULL_DEPS_SRC=${FULL_DEPS_SRC:-/root/full-deps}
PRINT_CHECKSUMS=${PRINT_CHECKSUMS:-0}
mkdir -p "$FULL_DEPS_SRC"

sha256() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# download sha-var url: downloads url to a temporary file, verifies it against
# ${!sha-var} and prints the file name (or, with PRINT_CHECKSUMS=1, prints the
# checksum line and nothing else).
download() {
  local sha_var=$1 url=$2 expected=${!1:-} file actual
  file="$(mktemp "${TMPDIR:-/tmp}/xi-full.XXXXXX")"
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
  echo "| $(basename "$url") | $url | \`$actual\` |" >> "$FULL_DEPS_SRC/SOURCES.rows"
  echo "$file"
}

# fetch sha-var dir url: download + verify + unpack a tarball (strip 1 component).
fetch() {
  local sha_var=$1 dir=$2 url=$3 file
  file="$(download "$sha_var" "$url")"
  [ "$PRINT_CHECKSUMS" = "1" ] && { echo "$file"; return; }
  rm -rf "${FULL_DEPS_SRC:?}/$dir"
  mkdir -p "$FULL_DEPS_SRC/$dir"
  tar -xf "$file" -C "$FULL_DEPS_SRC/$dir" --strip-components=1
  rm -f "$file"
}

# fetch_file sha-var dest url: download + verify a single file to $FULL_DEPS_SRC/dest.
fetch_file() {
  local sha_var=$1 dest=$2 url=$3 file
  file="$(download "$sha_var" "$url")"
  [ "$PRINT_CHECKSUMS" = "1" ] && { echo "$file"; return; }
  mkdir -p "$(dirname "$FULL_DEPS_SRC/$dest")"
  mv "$file" "$FULL_DEPS_SRC/$dest"
  chmod 0644 "$FULL_DEPS_SRC/$dest"
}

if [ "$PRINT_CHECKSUMS" != "1" ]; then
  : > "$FULL_DEPS_SRC/SOURCES.rows"
fi

fetch LIBVPX_SHA256 libvpx \
  "https://github.com/webmproject/libvpx/archive/refs/tags/v${LIBVPX_VERSION}.tar.gz"
fetch FFMPEG_SHA256 ffmpeg \
  "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"
# Audio encoders (WP-120): Opus (BSD-3-Clause) and LAME (LGPL-2.0-or-later).
fetch OPUS_SHA256 opus \
  "https://downloads.xiph.org/releases/opus/opus-${OPUS_VERSION}.tar.gz"
fetch LAME_SHA256 lame \
  "https://downloads.sourceforge.net/project/lame/lame/${LAME_VERSION}/lame-${LAME_VERSION}.tar.gz"

# ONNX Runtime: the official CPU build per architecture.
onnxruntime_url() {
  echo "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-$1-${ONNXRUNTIME_VERSION}.tgz"
}
if [ "$PRINT_CHECKSUMS" = "1" ]; then
  fetch ONNXRUNTIME_SHA256_AMD64 onnxruntime "$(onnxruntime_url x64)"
  fetch ONNXRUNTIME_SHA256_ARM64 onnxruntime "$(onnxruntime_url aarch64)"
else
  case "$(uname -m)" in
    x86_64) fetch ONNXRUNTIME_SHA256_AMD64 onnxruntime "$(onnxruntime_url x64)" ;;
    aarch64) fetch ONNXRUNTIME_SHA256_ARM64 onnxruntime "$(onnxruntime_url aarch64)" ;;
    *) echo "No prebuilt ONNX Runtime for $(uname -m)" >&2; exit 1 ;;
  esac
fi

# Models: <name>.onnx next to <name>.LICENSE (check-licenses.sh --models enforces it).
yunet_base="https://media.githubusercontent.com/media/opencv/opencv_zoo/${YUNET_COMMIT}/models/face_detection_yunet"
yunet_raw="https://raw.githubusercontent.com/opencv/opencv_zoo/${YUNET_COMMIT}/models/face_detection_yunet"
fetch_file YUNET_SHA256 "models/face_detection_yunet_${YUNET_VERSION}.onnx" \
  "${yunet_base}/face_detection_yunet_${YUNET_VERSION}.onnx"
fetch_file YUNET_LICENSE_SHA256 "models/face_detection_yunet_${YUNET_VERSION}.LICENSE" \
  "${yunet_raw}/LICENSE"
fetch_file YOLOX_SHA256 models/yolox_s.onnx \
  "https://github.com/Megvii-BaseDetection/YOLOX/releases/download/${YOLOX_VERSION}/yolox_s.onnx"
fetch_file YOLOX_LICENSE_SHA256 models/yolox_s.LICENSE \
  "https://raw.githubusercontent.com/Megvii-BaseDetection/YOLOX/${YOLOX_VERSION}/LICENSE"
classifier_path="Computer_Vision/mobilenetv3_large_100_Opset17_timm/mobilenetv3_large_100_Opset17.onnx"
fetch_file CLASSIFIER_SHA256 models/mobilenetv3_large_100.onnx \
  "https://media.githubusercontent.com/media/onnx/models/${CLASSIFIER_COMMIT}/${classifier_path}"
fetch_file CLASSIFIER_LICENSE_SHA256 models/mobilenetv3_large_100.LICENSE \
  "https://raw.githubusercontent.com/onnx/models/${CLASSIFIER_COMMIT}/LICENSE"
