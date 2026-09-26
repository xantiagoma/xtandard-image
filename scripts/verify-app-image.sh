#!/bin/sh
# Verifies a built app image against the release contract (WP-37,
# docs/RELEASE.md):
#   1. `version` runs and reports the expected version (XI_EXPECT_VERSION)
#   2. image config: UID 65532, ports 8080/9090, HEALTHCHECK, OCI labels,
#      ARCHITECTURE §14 environment
#   3. compressed size ≤ XI_MAX_COMPRESSED_MB (default 110; docker save | gzip)
#   4. read-only rootfs with a tmpfs /tmp, all capabilities dropped: the
#      server starts, the Docker HEALTHCHECK turns healthy, `health` exits 0,
#      the rootfs refuses writes and /tmp accepts them; `health` against a
#      dead port exits non-zero
#   5. scripts/smoke.sh against the image (compose, read-only + tmpfs /tmp)
#
# Usage: scripts/verify-app-image.sh [image]   (default $XI_IMAGE, else xtandard-image:dev)
# Needs docker (compose), curl, gzip and od on the host.
set -eu
cd "$(dirname "$0")/.."

image="${1:-${XI_IMAGE:-xtandard-image:dev}}"
max_mb="${XI_MAX_COMPRESSED_MB:-110}"
name="xi-verify-$$"
failed=0

fail() {
  echo "verify-app-image: FAIL: $*" >&2
  failed=1
}

cleanup() {
  status=$?
  if [ "$status" -ne 0 ] || [ "$failed" -ne 0 ]; then
    docker logs "$name" >&2 2>/dev/null || true
  fi
  docker rm -f "$name" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

echo "== image $image"

# 1. version -------------------------------------------------------------------
echo "== version"
version_out="$(docker run --rm "$image" version)" || fail "version exited non-zero"
echo "$version_out"
case "$version_out" in
  "xtandard-image ${XI_EXPECT_VERSION:-}"*) ;;
  *) fail "version output does not start with 'xtandard-image ${XI_EXPECT_VERSION:-}'" ;;
esac

# 2. image config ------------------------------------------------------------------
echo "== image config"
inspect() { docker image inspect -f "$1" "$image"; }
[ "$(inspect '{{.Config.User}}')" = "65532:65532" ] || fail "user is '$(inspect '{{.Config.User}}')', want 65532:65532"
# shellcheck disable=SC2016 # Go template, not shell
ports="$(inspect '{{range $p, $_ := .Config.ExposedPorts}}{{$p}} {{end}}')"
case "$ports" in *8080/tcp*9090/tcp*) ;; *) fail "exposed ports '$ports', want 8080/tcp and 9090/tcp" ;; esac
hc="$(inspect '{{if .Config.Healthcheck}}{{json .Config.Healthcheck.Test}}{{end}}')"
case "$hc" in *xtandard-image*health*) ;; *) fail "HEALTHCHECK '$hc' does not run 'xtandard-image health'" ;; esac
for label in source version revision licenses description; do
  value="$(inspect "{{index .Config.Labels \"org.opencontainers.image.$label\"}}")"
  [ -n "$value" ] || fail "label org.opencontainers.image.$label missing"
done
[ "$(inspect '{{index .Config.Labels "org.opencontainers.image.licenses"}}')" = MIT ] || fail "licenses label is not MIT"
env="$(inspect '{{range .Config.Env}}{{println .}}{{end}}')"
for kv in LD_LIBRARY_PATH=/opt/xtandard/lib LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 \
  MALLOC_ARENA_MAX=2 MAGICK_CONFIGURE_PATH=/opt/xtandard/etc/ImageMagick-7 \
  FONTCONFIG_PATH=/opt/xtandard/etc/fonts VIPS_BLOCK_UNTRUSTED=1 XI_SERVER_ADDR=:8080; do
  echo "$env" | grep -qxF "$kv" || fail "ENV $kv missing"
done
echo "user 65532:65532, ports $ports, healthcheck $hc, labels and env ok"

# 3. compressed size -----------------------------------------------------------------
echo "== compressed size"
bytes="$(docker save "$image" | gzip -6 -c | wc -c | tr -d ' ')"
limit=$((max_mb * 1000 * 1000))
mb="$(awk -v b="$bytes" 'BEGIN { printf "%.1f", b / 1000000 }')"
uncompressed="$(inspect '{{.Size}}')"
echo "compressed ${mb} MB ($bytes bytes; docker save | gzip), limit ${max_mb} MB; uncompressed $uncompressed bytes"
[ "$bytes" -le "$limit" ] || fail "compressed size ${mb} MB exceeds ${max_mb} MB"

# 4. read-only rootfs + health ------------------------------------------------------------
echo "== read-only rootfs, tmpfs /tmp, health"
docker run -d --name "$name" \
  --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --cap-drop ALL --security-opt no-new-privileges \
  -e XI_DEV_MODE=true \
  "$image" >/dev/null

status=starting
i=0
while [ "$i" -lt 60 ]; do
  status="$(docker inspect -f '{{.State.Health.Status}}' "$name" 2>/dev/null || echo gone)"
  running="$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null || echo false)"
  [ "$status" = healthy ] && break
  [ "$running" = true ] || break
  sleep 1
  i=$((i + 1))
done
[ "$status" = healthy ] || fail "HEALTHCHECK status '$status' after ${i}s (read-only rootfs)"
echo "HEALTHCHECK healthy after ${i}s"

if docker exec "$name" /usr/local/bin/xtandard-image health; then
  echo "health exit 0"
else
  fail "health exited $?"
fi
if docker exec "$name" /usr/local/bin/xtandard-image health --url http://127.0.0.1:1/healthz --timeout 1s 2>/dev/null; then
  fail "health against a dead port exited 0"
else
  echo "health against a dead port exits non-zero"
fi
if docker exec "$name" sh -c ': > /usr/local/bin/xi-write-test' 2>/dev/null; then
  fail "rootfs is writable"
else
  echo "rootfs rejects writes"
fi
docker exec "$name" sh -c ': > /tmp/xi-write-test && rm /tmp/xi-write-test' || fail "/tmp tmpfs is not writable"
echo "/tmp is writable"
docker rm -f "$name" >/dev/null

# 5. smoke ---------------------------------------------------------------------------
echo "== smoke"
XI_IMAGE="$image" sh scripts/smoke.sh || fail "smoke test"

if [ "$failed" -ne 0 ]; then
  echo "verify-app-image: FAILED" >&2
  exit 1
fi
echo "verify-app-image: OK ($image, ${mb} MB compressed)"
