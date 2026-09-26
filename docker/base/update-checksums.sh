#!/bin/bash
# Recomputes every *_SHA256 value in versions.sh and versions-full.sh from the
# pinned versions. Run after bumping a version; review the diff before committing.
#
# Usage: docker/base/update-checksums.sh

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
versions_file="$my_dir/versions.sh"
# shellcheck source=versions.sh
source "$versions_file"

sha256_url() {
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/xi-sum.XXXXXX")"
  curl -fsSL --retry 5 --retry-delay 2 -o "$tmp" "$1"
  if command -v sha256sum >/dev/null; then
    sha256sum "$tmp" | cut -d' ' -f1
  else
    shasum -a 256 "$tmp" | cut -d' ' -f1
  fi
  rm -f "$tmp"
}

sums="$(mktemp "${TMPDIR:-/tmp}/xi-sums.XXXXXX")"
trap 'rm -f "$sums"' EXIT

{
  PRINT_CHECKSUMS=1 DEPS_SRC="$(mktemp -d)" "$my_dir/download-deps.sh"
  echo "GOLANG_SHA256_AMD64=$(sha256_url "https://go.dev/dl/go${GOLANG_VERSION}.linux-amd64.tar.gz")"
  echo "GOLANG_SHA256_ARM64=$(sha256_url "https://go.dev/dl/go${GOLANG_VERSION}.linux-arm64.tar.gz")"
  echo "GOLANGCI_LINT_SHA256_AMD64=$(sha256_url "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64.tar.gz")"
  echo "GOLANGCI_LINT_SHA256_ARM64=$(sha256_url "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-arm64.tar.gz")"
  echo "TASK_SHA256_AMD64=$(sha256_url "https://github.com/go-task/task/releases/download/v${TASK_VERSION}/task_linux_amd64.tar.gz")"
  echo "TASK_SHA256_ARM64=$(sha256_url "https://github.com/go-task/task/releases/download/v${TASK_VERSION}/task_linux_arm64.tar.gz")"
  echo "RUSTUP_SHA256_AMD64=$(sha256_url "https://static.rust-lang.org/rustup/archive/${RUSTUP_VERSION}/x86_64-unknown-linux-gnu/rustup-init")"
  echo "RUSTUP_SHA256_ARM64=$(sha256_url "https://static.rust-lang.org/rustup/archive/${RUSTUP_VERSION}/aarch64-unknown-linux-gnu/rustup-init")"
} > "$sums"

# Full flavor (WP-70): FFmpeg, libvpx, ONNX Runtime (both architectures) and models.
PRINT_CHECKSUMS=1 FULL_DEPS_SRC="$(mktemp -d)" "$my_dir/download-full-deps.sh" >> "$sums"

for file in "$versions_file" "$my_dir/versions-full.sh"; do
  awk -F= '
    NR == FNR { sum[$1] = $2; next }
    /^export [A-Z0-9_]+_SHA256[A-Z0-9_]*=/ {
      name = substr($1, 8)
      if (name in sum) { print "export " name "=" sum[name]; next }
    }
    { print }
  ' "$sums" "$file" > "$file.new"
  mv "$file.new" "$file"
  echo "Updated checksums in $file"
done
