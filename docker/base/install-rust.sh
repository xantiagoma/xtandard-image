#!/bin/bash
# Installs a pinned Rust toolchain (build-time only: quantizr and librsvg) and cargo-c.
#
# Forked from imgproxy-docker-base (MIT, Copyright (c) 2017 Sergey Alexandrovich);
# see LICENSE.imgproxy-docker-base. Changes: pinned rustup-init with SHA256, pinned
# toolchain and cargo-c versions.

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"

case "$(uname -m)" in
  x86_64) TRIPLE=x86_64-unknown-linux-gnu; SHA=$RUSTUP_SHA256_AMD64 ;;
  aarch64) TRIPLE=aarch64-unknown-linux-gnu; SHA=$RUSTUP_SHA256_ARM64 ;;
  *) echo "Unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

# rustup dispatches on argv[0], so the binary must be named rustup-init.
init_dir="$(mktemp -d)"
init="$init_dir/rustup-init"
curl -fsSL --retry 5 --retry-delay 2 -o "$init" \
  "https://static.rust-lang.org/rustup/archive/${RUSTUP_VERSION}/${TRIPLE}/rustup-init"
echo "$SHA  $init" | sha256sum -c --quiet -
chmod +x "$init"
"$init" -y --no-modify-path --profile minimal --default-toolchain "$RUST_VERSION"
rm -rf "$init_dir"

export PATH="/root/.cargo/bin:$PATH"
cargo install --locked cargo-c --version "$CARGO_C_VERSION"
