#!/usr/bin/env sh
# Checks the licences of the Go client's runtime dependency closure against the
# ADR 0006 allowlist (MIT, BSD, Apache-2.0, ISC; ADR 0013).
#
# The runtime closure is every module the non-test packages import, outside
# the standard library and this module. Today it is empty: the client uses
# only the standard library (.golangci.yml's depguard enforces that too), so
# the script reports that and passes. If a module is ever added, its LICENSE
# (or COPYING) file in the module cache is classified; anything not on the
# allowlist fails. Exceptions: none.
#
# Run from clients/go: sh scripts/check-licenses.sh (task licenses).
set -eu

self=$(go list -m)
mods=$(go list -deps -f '{{if not .Standard}}{{with .Module}}{{.Path}}@{{.Version}}{{end}}{{end}}' ./... |
  grep -v "^${self}@" | sort -u || true)

if [ -z "$mods" ]; then
  echo "ok: no third-party runtime modules (standard library only)"
  exit 0
fi

bad=0
for m in $mods; do
  path=${m%@*}
  dir=$(go list -m -f '{{.Dir}}' "$path")
  file=$(ls "$dir" | grep -iE '^(licen[cs]e|copying)(\.(md|txt))?$' | head -n 1 || true)
  if [ -z "$file" ]; then
    echo "NOT ALLOWED $m: no licence file"
    bad=1
    continue
  fi
  text=$(cat "$dir/$file")
  case "$text" in
  *"Permission is hereby granted, free of charge"*) lic=MIT ;;
  *"Apache License"*"Version 2.0"*) lic=Apache-2.0 ;;
  *"Redistribution and use in source and binary forms"*) lic=BSD ;;
  *"Permission to use, copy, modify, and/or distribute this software"*) lic=ISC ;;
  *) lic="" ;;
  esac
  if [ -n "$lic" ]; then
    echo "ok          $m: $lic"
  else
    echo "NOT ALLOWED $m: unrecognised licence in $file"
    bad=1
  fi
done
exit $bad
