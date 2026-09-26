#!/bin/sh
# Every static gate CI runs on a pull request (CLAUDE.md "Commands").
# Used by `task lint` (host) and `task docker:lint` (base image builder).
set -eu
cd "$(dirname "$0")/.."

golangci-lint fmt --diff
golangci-lint run ./...
go vet ./...

fix="$(go fix -diff ./...)"
if [ -n "$fix" ]; then
  echo "go fix -diff is not empty:" >&2
  echo "$fix" >&2
  exit 1
fi

go mod tidy -diff
go -C tools mod tidy -diff
sh scripts/check-cgo.sh
