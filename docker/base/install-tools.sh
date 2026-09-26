#!/bin/bash
# Installs the Go development toolchain of the builder stage: Go, golangci-lint and
# go-task, each verified against the SHA256 pinned in versions.sh.
#
# Derived from imgproxy-docker-base install-go.sh (MIT, Copyright (c) 2017 Sergey
# Alexandrovich); see LICENSE.imgproxy-docker-base.

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) echo "Unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
ARCH_UPPER=${ARCH^^}

# fetch_verified url sha256 -> path of the downloaded file
fetch_verified() {
  local url=$1 expected=$2 out
  out="$(mktemp)"
  curl -fsSL --retry 5 --retry-delay 2 -o "$out" "$url"
  echo "$expected  $out" | sha256sum -c --quiet - >&2
  echo "$out"
}

sha_var="GOLANG_SHA256_${ARCH_UPPER}"
go_tar="$(fetch_verified "https://go.dev/dl/go${GOLANG_VERSION}.linux-${ARCH}.tar.gz" "${!sha_var}")"
rm -rf /usr/local/go
tar -xzf "$go_tar" -C /usr/local
rm -f "$go_tar"

sha_var="GOLANGCI_LINT_SHA256_${ARCH_UPPER}"
lint_tar="$(fetch_verified "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${ARCH}.tar.gz" "${!sha_var}")"
tar -xzf "$lint_tar" -C /usr/local/bin --strip-components=1 \
  "golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${ARCH}/golangci-lint"
rm -f "$lint_tar"

sha_var="TASK_SHA256_${ARCH_UPPER}"
task_tar="$(fetch_verified "https://github.com/go-task/task/releases/download/v${TASK_VERSION}/task_linux_${ARCH}.tar.gz" "${!sha_var}")"
tar -xzf "$task_tar" -C /usr/local/bin task
rm -f "$task_tar"

/usr/local/go/bin/go version
/usr/local/bin/golangci-lint version
/usr/local/bin/task --version
