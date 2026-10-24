#!/usr/bin/env bash
# shellcheck disable=SC2015  # ok() never fails: `check && ok || bad` is an if-then-else here.
# Installs (bun, frozen lockfiles) and type-checks the Pulumi, SST and CDK programs, generates
# SST's platform types (`sst install`, no AWS credentials) and runs `cdk synth` offline. Each
# directory is its own project with its own bun.lock, outside the root Bun workspace.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }
# No credentials reach these tools.
noaws=(env -u AWS_PROFILE -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN
  AWS_EC2_METADATA_DISABLED=true CDK_DISABLE_VERSION_CHECK=1)

for p in pulumi sst cdk; do
  dir="$root/deploy/$p"
  (cd "$dir" && bun install --frozen-lockfile --silent) || { bad "deploy/$p: bun install"; continue; }
  if [[ $p == sst ]]; then
    (cd "$dir" && "${noaws[@]}" bunx --bun sst install >/dev/null) || { bad "deploy/sst: sst install"; continue; }
  fi
  (cd "$dir" && bun run --silent typecheck) && ok "deploy/$p (tsc --noEmit)" || bad "deploy/$p typecheck"
done

out="$(mktemp -d)"
(cd "$root/deploy/cdk" && "${noaws[@]}" bunx cdk synth --quiet --no-notices -o "$out" >/dev/null) \
  && [[ -s "$out/XtandardImage.template.json" ]] && ok "deploy/cdk (cdk synth, no credentials)" || bad "deploy/cdk synth"
rm -rf "$out"
exit "$fail"
