#!/bin/sh
# Runs every FuzzXxx target in the module for FUZZTIME each (default 60s,
# go-standards §8). Go can only fuzz one target per invocation, hence the loop.
set -eu
cd "$(dirname "$0")/.."
fuzztime="${FUZZTIME:-60s}"
status=0
for pkg in $(go list ./...); do
  for target in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
    echo "fuzz $pkg $target ($fuzztime)"
    go test -run='^$' -fuzz="^${target}\$" -fuzztime="$fuzztime" "$pkg" || status=1
  done
done
exit "$status"
