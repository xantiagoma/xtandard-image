#!/bin/sh
# Generates the benchmark fixtures into bench/fixtures/ (gitignored). Runs in
# the base image builder (Go + libvips CLI); bench/run.sh calls it through
# compose.dev.yml:
#
#   docker compose -f compose.dev.yml run --rm -T dev sh bench/fixtures.sh
#
# The pixels come from testdata/gen (procedural, CC0, see
# testdata/LICENSES.md), so no third-party image is involved:
#   photo.jpg   3072x2048 (6.3 MP) photo, libvips jpegsave Q85, baseline
#               (sizes avoid y/h == 0.72 exactly, where testdata/gen recurses forever)
#   alpha.png   1280x853 RGBA photo with an alpha gradient
#   anim.gif    480x320, 12 frames (a pan across the photo), cgif
set -eu
cd "$(dirname "$0")/.."

out=bench/fixtures
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$out"

go run ./testdata/gen photo 3072 2048 "$tmp/photo.png"
vips jpegsave "$tmp/photo.png" "$out/photo.jpg" --Q 85 --keep none

go run ./testdata/gen photo-alpha 1280 853 "$out/alpha.png"

# 12 frames of 480x320 panning across a 1229x819 downscale of the photo.
vips resize "$tmp/photo.png" "$tmp/small.v" 0.4
frames=""
i=0
while [ "$i" -lt 12 ]; do
  vips crop "$tmp/small.v" "$tmp/f$i.v" $((i * 60)) $((i * 40)) 480 320
  frames="${frames:+$frames }$tmp/f$i.v"
  i=$((i + 1))
done
vips arrayjoin "$frames" "$tmp/strip.v" --across 1
vips gifsave "$tmp/strip.v" "$out/anim.gif" --page-height 320

ls -l "$out"
