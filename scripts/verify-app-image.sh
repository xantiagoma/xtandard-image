#!/bin/sh
# Verifies a built app image against the release contract (WP-37,
# docs/RELEASE.md):
#   1. `version` runs and reports the expected version (XI_EXPECT_VERSION)
#   2. image config: UID 65532, ports 8080/9090, HEALTHCHECK, OCI labels
#      (per flavor: dev.xtandard.flavor matches the base's
#      dev.xtandard.base.flavor and XI_EXPECT_FLAVOR when set; licences MIT
#      for slim/full, the GPL/AGPL expression for gpl; the description names
#      the flavor), ARCHITECTURE §14 environment
#   3. compressed size ≤ XI_MAX_COMPRESSED_MB (default 110 for slim, 170 for
#      full and gpl; docker save | gzip)
#   4. scripts/check-readonly.sh: read-only rootfs with a tmpfs /tmp, all
#      capabilities dropped: the server starts, the Docker HEALTHCHECK turns
#      healthy, `health` exits 0, the rootfs refuses writes and /tmp accepts
#      them; `health` against a dead port exits non-zero
#   5. scripts/smoke.sh against the image (compose, read-only + tmpfs /tmp)
#
# Usage: scripts/verify-app-image.sh [image]   (default $XI_IMAGE, else xtandard-image:dev)
# Needs docker (compose), curl, gzip and od on the host.
set -eu
cd "$(dirname "$0")/.."

image="${1:-${XI_IMAGE:-xtandard-image:dev}}"
# Default by flavor once the labels are read (ARCHITECTURE §14): 110 slim, 170 full/gpl.
max_mb="${XI_MAX_COMPRESSED_MB:-}"
failed=0

fail() {
  echo "verify-app-image: FAIL: $*" >&2
  failed=1
}

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
# Flavor labels (WP-118): dev.xtandard.flavor must match the base the image
# was built on (its dev.xtandard.base.flavor label, inherited) and
# XI_EXPECT_FLAVOR when set; the licences and description follow the flavor.
label() { inspect "{{index .Config.Labels \"$1\"}}"; }
flavor="$(label dev.xtandard.flavor)"
base_flavor="$(label dev.xtandard.base.flavor)"
[ -n "$flavor" ] || fail "label dev.xtandard.flavor missing"
[ -z "$base_flavor" ] || [ "$flavor" = "$base_flavor" ] ||
  fail "dev.xtandard.flavor is '$flavor' but the base is '$base_flavor' (build with --build-arg XI_FLAVOR=$base_flavor)"
[ -z "${XI_EXPECT_FLAVOR:-}" ] || [ "$flavor" = "$XI_EXPECT_FLAVOR" ] ||
  fail "dev.xtandard.flavor is '$flavor', want XI_EXPECT_FLAVOR=$XI_EXPECT_FLAVOR"
if [ -z "$max_mb" ]; then
  case "$flavor" in full | gpl) max_mb=170 ;; *) max_mb=110 ;; esac
fi
case "$flavor" in
  slim | full) want_licenses=MIT ;;
  gpl) want_licenses="GPL-2.0-or-later AND AGPL-3.0-or-later AND MIT AND LicenseRef-Adobe-DNG-SDK" ;;
  *) want_licenses="(unknown flavor '$flavor')" ;;
esac
licenses="$(label org.opencontainers.image.licenses)"
[ "$licenses" = "$want_licenses" ] || fail "licenses label '$licenses', want '$want_licenses' for $flavor"
case "$(label org.opencontainers.image.description)" in
  *"$flavor flavor"*) ;;
  *) fail "description does not name the $flavor flavor" ;;
esac
env="$(inspect '{{range .Config.Env}}{{println .}}{{end}}')"
for kv in LD_LIBRARY_PATH=/opt/xtandard/lib LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 \
  MALLOC_ARENA_MAX=2 MALLOC_CONF=narenas:2 MAGICK_CONFIGURE_PATH=/opt/xtandard/etc/ImageMagick-7 \
  FONTCONFIG_PATH=/opt/xtandard/etc/fonts VIPS_BLOCK_UNTRUSTED=1 XI_SERVER_ADDR=:8080; do
  echo "$env" | grep -qxF "$kv" || fail "ENV $kv missing"
done
echo "user 65532:65532, ports $ports, healthcheck $hc, flavor $flavor ($licenses), labels and env ok"

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
sh scripts/check-readonly.sh "$image" || fail "read-only rootfs / health check"

# 5. smoke ---------------------------------------------------------------------------
echo "== smoke"
XI_IMAGE="$image" sh scripts/smoke.sh || fail "smoke test"

if [ "$failed" -ne 0 ]; then
  echo "verify-app-image: FAILED" >&2
  exit 1
fi
echo "verify-app-image: OK ($image, ${mb} MB compressed)"
