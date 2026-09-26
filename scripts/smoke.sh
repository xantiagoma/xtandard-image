#!/bin/sh
# End-to-end smoke test of the app image (ROADMAP WP-00): starts the image
# from compose.smoke.yml next to a static origin and checks
#   - GET /insecure/rs:fit:64:64/plain/http://fixtures/jpeg/baseline.jpg → 200,
#     image/jpeg, SOI/EOI markers, fits in 64x64
#   - GET /healthz → 200
#   - admin GET /metrics contains xi_ metrics
#   - SIGTERM → exit code 0
#
# Usage: scripts/smoke.sh            (image from $XI_IMAGE, default xtandard-image:dev)
# Needs docker compose, curl and od on the host.
set -eu
cd "$(dirname "$0")/.."

export XI_IMAGE="${XI_IMAGE:-xtandard-image:dev}"
project="xi-smoke-$$"
tmp="$(mktemp -d)"

compose() { docker compose -f compose.smoke.yml -p "$project" "$@"; }

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "--- app logs ---" >&2
    compose logs app >&2 || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT INT TERM

fail() {
  echo "smoke: FAIL: $*" >&2
  exit 1
}

echo "smoke: starting $XI_IMAGE"
compose up -d --wait --wait-timeout 60

app="$(compose port app 8080)"
admin="$(compose port app 9090)"

# 1. image
url="http://$app/insecure/rs:fit:64:64/plain/http://fixtures/jpeg/baseline.jpg"
out="$(curl -sS -o "$tmp/out.jpg" -w '%{http_code} %{content_type}' "$url")"
[ "$out" = "200 image/jpeg" ] || fail "image: got '$out', want '200 image/jpeg'"
soi="$(head -c 2 "$tmp/out.jpg" | od -An -tx1 | tr -d ' \n')"
eoi="$(tail -c 2 "$tmp/out.jpg" | od -An -tx1 | tr -d ' \n')"
[ "$soi" = "ffd8" ] || fail "image: missing JPEG SOI marker (got $soi)"
[ "$eoi" = "ffd9" ] || fail "image: missing JPEG EOI marker (got $eoi)"

# Width/height from the first SOFn segment (baseline or progressive).
dims="$(od -An -v -tu1 "$tmp/out.jpg" | tr -s ' \n' '\n' | sed '/^$/d' | awk '
  { b[n++] = $1 }
  END {
    i = 2
    while (i + 8 < n) {
      if (b[i] != 255) { i++; continue }
      m = b[i + 1]
      if (m >= 192 && m <= 207 && m != 196 && m != 200 && m != 204) {
        printf "%d %d\n", b[i + 7] * 256 + b[i + 8], b[i + 5] * 256 + b[i + 6]
        exit
      }
      i += 2 + b[i + 2] * 256 + b[i + 3]
    }
  }')"
[ -n "$dims" ] || fail "image: no SOF segment"
w="${dims% *}"
h="${dims#* }"
[ "$w" -le 64 ] && [ "$h" -le 64 ] && [ "$w" -gt 0 ] && [ "$h" -gt 0 ] ||
  fail "image: ${w}x${h} does not fit 64x64"
echo "smoke: image ok (${w}x${h}, $(wc -c <"$tmp/out.jpg" | tr -d ' ') bytes)"

# 2. health
code="$(curl -sS -o /dev/null -w '%{http_code}' "http://$app/healthz")"
[ "$code" = "200" ] || fail "/healthz: got $code"
echo "smoke: /healthz ok"

# 3. metrics on the admin listener
curl -sS "http://$admin/metrics" >"$tmp/metrics"
grep -q '^xi_requests_total' "$tmp/metrics" || fail "/metrics: no xi_requests_total"
grep -q '^xi_vips_mem_bytes' "$tmp/metrics" || fail "/metrics: no xi_vips_mem_bytes"
echo "smoke: /metrics ok"

# 4. graceful shutdown on SIGTERM
cid="$(compose ps -q app)"
compose stop app >/dev/null
exit_code="$(docker inspect -f '{{.State.ExitCode}}' "$cid")"
[ "$exit_code" = "0" ] || fail "SIGTERM: exit code $exit_code, want 0"
echo "smoke: SIGTERM exit 0 ok"

echo "smoke: PASS ($(uname -m))"
