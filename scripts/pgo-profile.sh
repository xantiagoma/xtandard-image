#!/bin/sh
# Collects cmd/xtandard-image/default.pgo (go-standards §9, docs/RELEASE.md):
# starts the app image from compose.smoke.yml with pprof on the admin
# listener, drives it with a mixed resize/convert load over the fixtures and
# takes a CPU profile of PGO_SECONDS (default 30) from inside the container.
#
# Usage: scripts/pgo-profile.sh [output]   (default cmd/xtandard-image/default.pgo)
#   XI_IMAGE      image to profile (default xtandard-image:dev)
#   PGO_SECONDS   profile duration (default 30)
#   PGO_WORKERS   concurrent load loops (default 8)
#
# Profile the image you are about to release; a profile collected from a PGO
# build is fine (Go documents iterative profiles as stable). Merge several
# profiles with `go tool pprof -proto a.pprof b.pprof > default.pgo`.
# Needs docker compose and curl on the host.
set -eu
cd "$(dirname "$0")/.."

out="${1:-cmd/xtandard-image/default.pgo}"
export XI_IMAGE="${XI_IMAGE:-xtandard-image:dev}"
export XI_ADMIN_PPROF=true
seconds="${PGO_SECONDS:-30}"
workers="${PGO_WORKERS:-8}"
project="xi-pgo-$$"
tmp="$(mktemp -d)"

compose() { docker compose -f compose.smoke.yml -p "$project" "$@"; }

cleanup() {
  status=$?
  touch "$tmp/stop"
  wait 2>/dev/null || true
  if [ "$status" -ne 0 ]; then
    compose logs app >&2 || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT INT TERM

echo "pgo: starting $XI_IMAGE"
compose up -d --wait --wait-timeout 60
app="$(compose port app 8080)"
admin="$(compose port app 9090)"

# Representative M1 traffic: the common resize path into every encoder plus
# decoders for each fixture family.
o="http://fixtures"
cat >"$tmp/paths" <<EOF
/insecure/rs:fit:64:64/plain/$o/jpeg/baseline.jpg
/insecure/rs:fit:384:256/plain/$o/jpeg/baseline.jpg@webp
/insecure/rs:fill:256:256/plain/$o/jpeg/progressive.jpg@avif
/insecure/rs:fit:640:640/q:80/plain/$o/jpeg/exif-orientation-6-icc.jpg
/insecure/rs:fit:256:256/q:70/plain/$o/png/rgba8.png@webp
/insecure/rs:fit:128:128/plain/$o/png/palette.png
/insecure/rs:fit:256:256/plain/$o/png/rgb16.png@jpg
/insecure/rs:fit:384:384/plain/$o/webp/lossy.webp@jpg
/insecure/rs:fit:256:256/plain/$o/webp/lossless.webp@png
/insecure/rs:fit:128:128/plain/$o/gif/animated.gif
/insecure/rs:fit:128:128/plain/$o/gif/static.gif@png
/insecure/rs:fit:256:256/plain/$o/avif/photo.avif@webp
/insecure/rs:fit:256:256/plain/$o/heic/photo.heic@jpg
/insecure/rs:fit:256:256/plain/$o/jxl/photo.jxl@jpg
/insecure/rs:fit:256:256/plain/$o/jxl/photo.jxl@jxl
/insecure/rs:fit:128:128/plain/$o/svg/logo.svg@png
/insecure/rs:fit:256:256/plain/$o/tiff/multipage.tif@jpg
/insecure/rs:fit:384:384/plain/$o/uhdr/photo-ultrahdr.jpg
/insecure/rs:fit:256:256/plain/$o/jp2/photo.jp2@webp
EOF

# Keep only paths the server answers with 200, so the profile is not
# dominated by error handling.
: >"$tmp/ok"
while read -r path; do
  code="$(curl -sS -o /dev/null -w '%{http_code}' "http://$app$path" || echo 000)"
  if [ "$code" = 200 ]; then
    echo "$path" >>"$tmp/ok"
  else
    echo "pgo: skipping $path ($code)" >&2
  fi
done <"$tmp/paths"
[ -s "$tmp/ok" ] || { echo "pgo: no path returned 200" >&2; exit 1; }
echo "pgo: load mix of $(wc -l <"$tmp/ok" | tr -d ' ') paths, $workers workers"

sed "s|^|url = \"http://$app|; s|\$|\"\noutput = /dev/null|" "$tmp/ok" >"$tmp/curlrc"
i=0
while [ "$i" -lt "$workers" ]; do
  (while [ ! -e "$tmp/stop" ]; do curl -sS -K "$tmp/curlrc" >/dev/null 2>&1 || true; done) &
  i=$((i + 1))
done

echo "pgo: collecting a ${seconds}s CPU profile"
curl -sS --fail -o "$tmp/cpu.pprof" "http://$admin/debug/pprof/profile?seconds=$seconds"
touch "$tmp/stop"
wait

# pprof protos are gzip-compressed.
magic="$(head -c 2 "$tmp/cpu.pprof" | od -An -tx1 | tr -d ' \n')"
[ "$magic" = "1f8b" ] || { echo "pgo: profile is not a gzip pprof proto" >&2; exit 1; }
mkdir -p "$(dirname "$out")"
cp "$tmp/cpu.pprof" "$out"
echo "pgo: wrote $out ($(wc -c <"$out" | tr -d ' ') bytes)"
