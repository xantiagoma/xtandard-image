#!/usr/bin/env bash
# shellcheck disable=SC2015  # ok() never fails: `check && ok || bad` is an if-then-else here.
# The chart values the Terraform (deploy/terraform/kubernetes-helm) and Pulumi (deploy/pulumi)
# Kubernetes programs pass to deploy/helm/xtandard-image: each program reads them from its
# committed values.<fleet>.json, and this check
#   1. proves the program's values equal the file when no override is set (no drift):
#      `tofu console` on local.values, and the Pulumi chartValues() function;
#   2. renders the chart with each file (helm lint --strict + helm template | kubeconform -strict).
# helm and kubeconform are WP-148's pinned images, read from deploy/scripts/check.sh.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

pin() { sed -n "s/^: \"\${$1:=\(.*\)}\"\$/\1/p" deploy/scripts/check.sh; }
helm_image="$(pin HELM_IMAGE)"
kubeconform_image="$(pin KUBECONFORM_IMAGE)"
k8s_version="$(pin KUBERNETES_VERSION)"
tofu_image="${XI_TF_IMAGE:-ghcr.io/opentofu/opentofu:1.13.2}"
[[ -n "$helm_image" && -n "$kubeconform_image" && -n "$k8s_version" ]] || { echo "cannot read the pins in deploy/scripts/check.sh" >&2; exit 2; }
crds='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
cache="${DEPLOY_CHECK_CACHE:-$root/deploy/.cache}/terraform-plugins"
mkdir -p "$cache"

# json_eq <a> <b>: same JSON value (key order ignored).
json_eq() { bun -e 'const [a, b] = process.argv.slice(1).map((f) => JSON.parse(require("fs").readFileSync(f, "utf8")));
  const norm = (v) => Array.isArray(v) ? v.map(norm) : v && typeof v === "object" ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, norm(v[k])])) : v;
  process.exit(JSON.stringify(norm(a)) === JSON.stringify(norm(b)) ? 0 : 1);' "$1" "$2"; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp" deploy/terraform/kubernetes-helm/.terraform deploy/terraform/kubernetes-helm/.terraform.lock.hcl' EXIT

# 1a. Terraform: local.values == values.<fleet>.json with every override variable null.
tf() {
  docker run --rm -i -u "$(id -u):$(id -g)" -v "$root/deploy:/deploy" -v "$cache:/plugins" \
    -e TF_PLUGIN_CACHE_DIR=/plugins -e TF_IN_AUTOMATION=1 -e HOME=/tmp \
    -w /deploy/terraform/kubernetes-helm "$tofu_image" "$@"
}
tf init -backend=false -input=false -no-color >/dev/null
for fleet in image video; do
  echo 'jsonencode(local.values)' \
    | tf console -var fleet="$fleet" -var signing_keys=x -var signing_salts=x -var stream_keys=x \
    | bun -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(0, "utf8")))' >"$tmp/tf-$fleet.json"
  json_eq "$tmp/tf-$fleet.json" "deploy/terraform/kubernetes-helm/values.$fleet.json" \
    && ok "terraform/kubernetes-helm local.values == values.$fleet.json" \
    || bad "terraform/kubernetes-helm local.values differs from values.$fleet.json: $(cat "$tmp/tf-$fleet.json")"
done

# 1b. Pulumi: chartValues() with no config == values.<fleet>.json.
for fleet in image video; do
  (cd deploy/pulumi && bun -e "import { chartValues } from './chart-values.ts';
    process.stdout.write(JSON.stringify(chartValues({ fleet: '$fleet' })))") >"$tmp/pulumi-$fleet.json"
  json_eq "$tmp/pulumi-$fleet.json" "deploy/pulumi/values.$fleet.json" \
    && ok "pulumi chartValues({fleet: $fleet}) == values.$fleet.json" \
    || bad "pulumi chartValues differs from values.$fleet.json: $(cat "$tmp/pulumi-$fleet.json")"
done

# 2. Render the chart with each file.
for f in deploy/terraform/kubernetes-helm/values.image.json deploy/terraform/kubernetes-helm/values.video.json \
  deploy/pulumi/values.image.json deploy/pulumi/values.video.json; do
  if docker run --rm -v "$root/deploy:/deploy:ro" -w /deploy "$helm_image" \
      lint --strict /deploy/helm/xtandard-image -f "/$f" >/dev/null \
    && docker run --rm -v "$root/deploy:/deploy:ro" -w /deploy "$helm_image" \
      template xi /deploy/helm/xtandard-image --namespace xi -f "/$f" \
      | docker run --rm -i "$kubeconform_image" -strict -summary -kubernetes-version "$k8s_version" \
        -schema-location default -schema-location "$crds" -; then
    ok "$f (helm lint --strict, helm template | kubeconform -strict)"
  else
    bad "$f does not render"
  fi
done
exit "$fail"
