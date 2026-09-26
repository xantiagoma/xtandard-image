#!/bin/sh
# Runs the README quick start verbatim (ROADMAP WP-38): the fenced blocks
# tagged in the info string are extracted from README.md and executed as-is.
#
#   ```sh quickstart-build     run from the repo root, only with
#                              XI_QUICKSTART_BUILD=1 (CI); `task quickstart`
#                              builds the same tag with task release:build
#   ```sh quickstart           concatenated in order and run with `sh -eu` in a
#                              scratch directory where scripts/ points at this
#                              repo's scripts/ (the README assumes the clone
#                              as working directory)
#   ```yaml quickstart-compose the Dokploy/Coolify snippet: `docker compose up
#                              --wait` with XI_IMAGE=xtandard-image and test
#                              keys, must turn healthy
#
# Then checks what the commands wrote (thumb.webp, crop.avif, og.jpg,
# info.json) by magic bytes, because the README commands only fail on
# transport or HTTP errors.
#
# Usage: scripts/quickstart.sh [README.md]
# Needs docker (compose), curl, openssl, awk and od; the image
# `xtandard-image` must exist unless XI_QUICKSTART_BUILD=1.
set -eu
cd "$(dirname "$0")/.."
repo="$(pwd)"
readme="${1:-README.md}"
work="$(mktemp -d)"
project="xi-quickstart-$$"

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "--- container logs ---" >&2
    docker logs xtandard-image >&2 2>/dev/null || true
  fi
  docker rm -f xtandard-image >/dev/null 2>&1 || true
  if [ -f "$work/compose.yml" ]; then
    docker compose -f "$work/compose.yml" -p "$project" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

fail() {
  echo "quickstart: FAIL: $*" >&2
  exit 1
}

# blocks <info string>: the bodies of every fenced block whose info string is
# exactly <info string>, in order.
blocks() {
  awk -v want="$1" '
    !open && /^```/ { open = 1; keep = (substr($0, 4) == want); next }
    open && /^```[ \t]*$/ { open = 0; keep = 0; next }
    keep { print }
  ' "$readme"
}

magic() { head -c "$2" "$1" | od -An -tx1 | tr -d ' \n'; }

blocks 'sh quickstart-build' >"$work/build.sh"
blocks 'sh quickstart' >"$work/quickstart.sh"
blocks 'yaml quickstart-compose' >"$work/compose.yml"
[ -s "$work/quickstart.sh" ] || fail "no 'sh quickstart' blocks in $readme"
[ -s "$work/compose.yml" ] || fail "no 'yaml quickstart-compose' block in $readme"

# 1. build ---------------------------------------------------------------------
if [ "${XI_QUICKSTART_BUILD:-0}" = 1 ]; then
  [ -s "$work/build.sh" ] || fail "no 'sh quickstart-build' block in $readme"
  echo "quickstart: build"
  sed 's/^/  | /' "$work/build.sh"
  sh -eux "$work/build.sh"
fi
docker image inspect xtandard-image >/dev/null 2>&1 ||
  fail "image xtandard-image not found (task quickstart builds it)"

# 2. docker run blocks -------------------------------------------------------------
echo "quickstart: run"
sed 's/^/  | /' "$work/quickstart.sh"
ln -s "$repo/scripts" "$work/scripts"
(cd "$work" && sh -eux ./quickstart.sh)

for f in thumb.webp crop.avif og.jpg info.json; do
  [ -s "$work/$f" ] || fail "$f was not written"
done
[ "$(magic "$work/thumb.webp" 4)" = 52494646 ] || fail "thumb.webp is not RIFF/WebP"
case "$(head -c 12 "$work/crop.avif" | tail -c 8 | od -An -c | tr -d ' \n')" in
  ftypavif | ftypavis) ;;
  *) fail "crop.avif is not AVIF" ;;
esac
[ "$(magic "$work/og.jpg" 2)" = ffd8 ] || fail "og.jpg is not JPEG"
grep -q '"format":"jpeg"' "$work/info.json" || grep -q '"format": "jpeg"' "$work/info.json" ||
  fail "info.json does not report a JPEG: $(cat "$work/info.json")"
echo "quickstart: outputs ok (thumb.webp, crop.avif, og.jpg, info.json)"

# 3. compose snippet -------------------------------------------------------------------
echo "quickstart: compose snippet"
XI_IMAGE=xtandard-image \
  XI_SIGNING_KEYS="$(openssl rand -hex 32)" XI_SIGNING_SALTS="$(openssl rand -hex 32)" \
  docker compose -f "$work/compose.yml" -p "$project" up -d --wait --wait-timeout 60
echo "quickstart: compose service healthy"

echo "quickstart: PASS"
