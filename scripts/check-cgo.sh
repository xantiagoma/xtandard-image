#!/bin/sh
# Fails if any package other than internal/vips uses cgo (CLAUDE.md non-negotiable #3).
# depguard cannot enforce this: cgo rewrites `import "C"` before analysis runs.
set -eu
cd "$(dirname "$0")/.."
offenders=$(CGO_ENABLED=1 go list -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./... |
  grep -v '^github.com/xantiagoma/xtandard-image/internal/vips$' || true)
if [ -n "$offenders" ]; then
  echo 'only internal/vips may import "C"; offending packages:' >&2
  echo "$offenders" >&2
  exit 1
fi
