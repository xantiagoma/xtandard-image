#!/bin/sh
# Fails if any package other than internal/vips and internal/video/ffmpeg uses
# cgo (CLAUDE.md non-negotiable #3, amended by ADR 0010 for the build-tagged
# FFmpeg shim). depguard cannot enforce this: cgo rewrites `import "C"` before
# analysis runs. Both build variants are checked: without tags and with
# `-tags ffmpeg` (the full image).
set -eu
cd "$(dirname "$0")/.."
allowed='^github.com/xantiagoma/xtandard-image/internal/(vips|video/ffmpeg)$'
offenders=$(
  {
    CGO_ENABLED=1 go list -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...
    CGO_ENABLED=1 go list -tags ffmpeg -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...
  } | sort -u | grep -Ev "$allowed" || true
)
if [ -n "$offenders" ]; then
  echo 'only internal/vips and internal/video/ffmpeg (ADR 0010) may import "C"; offending packages:' >&2
  echo "$offenders" >&2
  exit 1
fi
# Without the ffmpeg tag the FFmpeg package must be pure Go (the stub).
if CGO_ENABLED=1 go list -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./internal/video/ffmpeg | grep -q .; then
  echo 'internal/video/ffmpeg uses cgo without the ffmpeg build tag' >&2
  exit 1
fi
