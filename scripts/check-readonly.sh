#!/bin/sh
# Production-posture check of an app image (docs/RELEASE.md "Running
# read-only"), shared by scripts/verify-app-image.sh (WP-37) and
# scripts/acceptance.sh (WP-39): `docker run --read-only --tmpfs /tmp
# --cap-drop ALL --security-opt no-new-privileges`, then
#   - the Docker HEALTHCHECK turns healthy
#   - `xtandard-image health` exits 0, and non-zero against a dead port
#   - the rootfs rejects writes, /tmp accepts them
#
# Usage: scripts/check-readonly.sh [image]   (default $XI_IMAGE, else xtandard-image:dev)
set -eu

image="${1:-${XI_IMAGE:-xtandard-image:dev}}"
name="xi-readonly-$$"
failed=0

fail() {
  echo "check-readonly: FAIL: $*" >&2
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
if docker exec "$name" sh -c ': > /tmp/xi-write-test && rm /tmp/xi-write-test'; then
  echo "/tmp is writable"
else
  fail "/tmp tmpfs is not writable"
fi

[ "$failed" -eq 0 ] || exit 1
