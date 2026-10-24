#!/usr/bin/env bash
# terraform/OpenTofu fmt, init (no backend) and validate, then tflint, for every root module in
# deploy/terraform/*, in pinned containers. No cloud credentials are needed.
#
#   deploy/check/terraform.sh [module ...]
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
# XI_TF_IMAGE=hashicorp/terraform:1.17.0 runs the same checks with Terraform (BUSL-1.1).
tofu_image="${XI_TF_IMAGE:-ghcr.io/opentofu/opentofu:1.13.2}"
tflint_image="ghcr.io/terraform-linters/tflint:v0.65.0"
cache="${XI_DEPLOY_CACHE:-$root/deploy/.cache}/terraform-plugins"
mkdir -p "$cache"

mods=("$@")
if [[ ${#mods[@]} -eq 0 ]]; then
  for d in "$root"/deploy/terraform/*/; do mods+=("$(basename "$d")"); done
fi

tofu() { # $1 = module, rest = args
  local m="$1"; shift
  docker run --rm -u "$(id -u):$(id -g)" \
    -v "$root/deploy:/deploy" -v "$cache:/plugins" \
    -e TF_PLUGIN_CACHE_DIR=/plugins -e TF_IN_AUTOMATION=1 -e HOME=/tmp \
    -w "/deploy/terraform/$m" "$tofu_image" "$@"
}

tflint() { # $1 = module, rest = args
  local m="$1"; shift
  docker run --rm -u "$(id -u):$(id -g)" \
    -v "$root/deploy:/deploy" -v "$cache/../tflint-plugins:/tflint-plugins" \
    -e TFLINT_PLUGIN_DIR=/tflint-plugins -e HOME=/tmp ${GITHUB_TOKEN:+-e GITHUB_TOKEN} \
    -w "/deploy/terraform/$m" "$tflint_image" --config=/deploy/terraform/.tflint.hcl "$@"
}

mkdir -p "$cache/../tflint-plugins"
fail=0
for m in "${mods[@]}"; do
  echo "== terraform/$m"
  if tofu "$m" fmt -check -diff -recursive \
    && tofu "$m" init -backend=false -input=false -no-color >/dev/null \
    && tofu "$m" validate -no-color; then
    if tflint "$m" --init >/dev/null && tflint "$m" --format=compact; then
      echo "ok   terraform/$m (validate, tflint)"
    else
      echo "FAIL terraform/$m tflint"; fail=1
    fi
  else
    echo "FAIL terraform/$m"; fail=1
  fi
  rm -rf "$root/deploy/terraform/$m/.terraform" "$root/deploy/terraform/$m/.terraform.lock.hcl"
done
exit "$fail"
