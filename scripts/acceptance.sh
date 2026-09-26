#!/bin/sh
# M1 acceptance suite (ROADMAP WP-39, the M1 release gate): runs the
# black-box Go suite in test/acceptance against ANY app image on the host
# architecture.
#
#   1. scripts/check-readonly.sh: docker run --read-only --tmpfs /tmp
#      --cap-drop ALL, HEALTHCHECK healthy, `health`, rootfs read-only
#   2. compose.acceptance.yml: nginx origin, RustFS seeded with
#      testdata/fixtures, the app configured through env only (fresh signing
#      and encryption keys per run) and a one-worker app-tiny
#   3. go test -tags acceptance ./test/acceptance/ on the host against the
#      published ports; the drain case sends SIGTERM to app-tiny itself
#   4. a plain summary (one line per case with its time) on stdout, in
#      $XI_ACC_SUMMARY if set, and in $GITHUB_STEP_SUMMARY on Actions
#
# Usage: scripts/acceptance.sh            (image from $XI_IMAGE, default xtandard-image:dev)
#   XI_IMAGE=ghcr.io/xantiagoma/xtandard-image:slim scripts/acceptance.sh
#   XI_APP_BASE=xtandard-base:dev-full XI_IMAGE=xtandard-image:full task release:build &&
#     XI_IMAGE=xtandard-image:full scripts/acceptance.sh   (the full flavor: TestDetection
#     then runs the models; against slim it checks every detection option is 422)
#   XI_ACC_RUN=TestSources scripts/acceptance.sh     (go test -run filter)
# Needs docker (compose), od and Go (go.mod toolchain; the suite is pure Go)
# on the host. A missing image is pulled.
set -eu
cd "$(dirname "$0")/.."

export XI_IMAGE="${XI_IMAGE:-xtandard-image:dev}"
project="xi-acc-$$"
tmp="$(mktemp -d)"
log="$tmp/go-test.log"
summary="$tmp/summary.txt"
started="$(date +%s)"

compose() { docker compose -f compose.acceptance.yml -p "$project" "$@"; }

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "--- app logs (last 50 lines) ---" >&2
    compose logs --no-color --tail 50 app app-tiny >&2 2>/dev/null || true
  fi
  compose --profile seed down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT INT TERM

fail() {
  echo "acceptance: FAIL: $*" >&2
  exit 1
}

hex() { od -An -tx1 -N"$1" /dev/urandom | tr -d ' \n'; }

# Fresh keys per run (compose.acceptance.yml requires them, `down` included).
XI_ACC_SIGNING_KEY="$(hex 32)"
XI_ACC_SIGNING_SALT="$(hex 16)"
XI_ACC_ENCRYPTION_KEY="$(hex 32)"
XI_ACC_IMGIX_TOKEN="$(hex 16)"
export XI_ACC_SIGNING_KEY XI_ACC_SIGNING_SALT XI_ACC_ENCRYPTION_KEY XI_ACC_IMGIX_TOKEN

# 0. image ----------------------------------------------------------------------
if ! docker image inspect "$XI_IMAGE" >/dev/null 2>&1; then
  echo "acceptance: pulling $XI_IMAGE"
  docker pull -q "$XI_IMAGE" >/dev/null
fi
image_id="$(docker image inspect -f '{{.Id}}' "$XI_IMAGE")"
version="$(docker run --rm "$XI_IMAGE" version)"
echo "acceptance: $XI_IMAGE ($image_id) on $(uname -m): $version"

# 1. read-only posture ------------------------------------------------------------
echo "== read-only rootfs (scripts/check-readonly.sh)"
sh scripts/check-readonly.sh "$XI_IMAGE" || fail "read-only rootfs check"

# 2. stack ------------------------------------------------------------------------
echo "== compose.acceptance.yml"
quiet() { "$@" >"$tmp/compose.log" 2>&1 || { cat "$tmp/compose.log" >&2; fail "$*"; }; }
quiet compose up -d --wait --wait-timeout 120 fixtures rustfs
quiet compose --profile seed run --rm -T s3-seed
quiet compose up -d --wait --wait-timeout 120 app app-tiny
echo "fixtures, rustfs (seeded), app and app-tiny healthy"

XI_ACC_APP="$(compose port app 8080)"
XI_ACC_ADMIN="$(compose port app 9090)"
XI_ACC_TINY="$(compose port app-tiny 8080)"
XI_ACC_TINY_ADMIN="$(compose port app-tiny 9090)"
XI_ACC_TINY_CONTAINER="$(compose ps -q app-tiny)"
XI_ACC_FIXTURES="$PWD/testdata/fixtures"
export XI_ACC_APP XI_ACC_ADMIN XI_ACC_TINY XI_ACC_TINY_ADMIN XI_ACC_TINY_CONTAINER XI_ACC_FIXTURES

# 3. suite ------------------------------------------------------------------------
echo "== go test -tags acceptance ./test/acceptance/"
set +e
CGO_ENABLED=0 go test -tags acceptance -count=1 -v -timeout 10m \
  -run "${XI_ACC_RUN:-.}" ./test/acceptance/ >"$log" 2>&1
status=$?
set -e

# 4. summary ----------------------------------------------------------------------
{
  echo "xtandard-image acceptance: $XI_IMAGE on $(uname -m)"
  echo "$version"
  echo
  grep -E '^ *--- (PASS|FAIL|SKIP)' "$log" | sed 's/^    /  /' || true
  echo
  pass="$(grep -cE '^ *--- PASS' "$log" || true)"
  failed="$(grep -cE '^ *--- FAIL' "$log" || true)"
  skip="$(grep -cE '^ *--- SKIP' "$log" || true)"
  echo "cases: $pass passed, $failed failed, $skip skipped; total $(($(date +%s) - started))s"
} >"$summary"

if [ "$status" -ne 0 ] || grep -qE '^ *--- SKIP' "$log"; then
  cat "$log" >&2
fi
cat "$summary"
if [ -n "${XI_ACC_SUMMARY:-}" ]; then
  cp "$summary" "$XI_ACC_SUMMARY"
fi
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  { echo '```'; cat "$summary"; echo '```'; } >>"$GITHUB_STEP_SUMMARY"
fi

[ "$status" -eq 0 ] || fail "go test exited $status"
# Every case needs its endpoint: a skip means the stack is misconfigured.
if grep -qE '^ *--- SKIP' "$log"; then
  fail "skipped cases"
fi
echo "acceptance: PASS ($XI_IMAGE, $(uname -m))"
