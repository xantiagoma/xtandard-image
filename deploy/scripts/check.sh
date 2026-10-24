#!/bin/sh
# Static checks of deploy/ (task deploy:check). Every tool runs from a pinned
# container image, so the host needs only Docker and a POSIX shell:
#
#   sh deploy/scripts/check.sh            # everything
#   sh deploy/scripts/check.sh rules      # promtool check + unit tests
#   sh deploy/scripts/check.sh helm       # helm lint + template (6 value sets) | kubeconform, refusals
#   sh deploy/scripts/check.sh kustomize  # kustomize build | kubeconform
#   sh deploy/scripts/check.sh names      # metric and XI_* names exist in the code
#   sh deploy/scripts/check.sh compose    # docker compose config of the local stack
#
# kubeconform downloads the Kubernetes schemas and the CRD schemas (KEDA,
# prometheus-operator) from the datreeio CRDs catalog, so it needs network.
set -eu
cd "$(dirname "$0")/../.."
root="$(pwd)"

: "${HELM_IMAGE:=alpine/helm:3.19.0}"
: "${KUBECONFORM_IMAGE:=ghcr.io/yannh/kubeconform:v0.7.0}"
: "${KUSTOMIZE_IMAGE:=registry.k8s.io/kustomize/kustomize:v5.7.1}"
: "${PROMETHEUS_IMAGE:=prom/prometheus:v3.7.3}"
: "${JQ_IMAGE:=ghcr.io/jqlang/jq:1.8.1}"
: "${KUBERNETES_VERSION:=1.33.0}"

CRDS='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'

drun() { docker run --rm -i -v "$root/deploy:/deploy:ro" -w /deploy "$@"; }

kubeconform() {
  drun "$KUBECONFORM_IMAGE" -strict -summary -kubernetes-version "$KUBERNETES_VERSION" \
    -schema-location default -schema-location "$CRDS" -
}

check_gen() {
  echo "== generated copies"
  sh deploy/scripts/gen.sh --check
  echo "up to date"
}

check_rules() {
  echo "== promtool"
  drun --entrypoint promtool "$PROMETHEUS_IMAGE" check rules observability/prometheus/rules.yml
  drun -w /deploy/observability/prometheus --entrypoint promtool "$PROMETHEUS_IMAGE" test rules rules.test.yml
  drun --entrypoint promtool "$PROMETHEUS_IMAGE" check config observability/prometheus/prometheus.yml
}

check_helm() {
  chart=/deploy/helm/xtandard-image
  src="--set config.XI_SOURCES=origin --set config.XI_SOURCES_ORIGIN_TYPE=http --set config.XI_SOURCES_ORIGIN_ALLOW=images.example.com"
  for v in ci/minimal-values.yaml ci/all-features-values.yaml ci/no-autoscaling-values.yaml \
    ../values-image.yaml ../values-video.yaml ../../scripts/kind/values-autoscale.yaml; do
    echo "== helm lint + template $v | kubeconform"
    drun "$HELM_IMAGE" lint --strict "$chart" -f "$chart/$v" >/dev/null
    drun "$HELM_IMAGE" template xi "$chart" --namespace xi -f "$chart/$v" | kubeconform
  done
  echo "== helm template refuses bad values"
  refuse() {
    # shellcheck disable=SC2086
    if drun "$HELM_IMAGE" template xi "$chart" "$@" >/dev/null 2>&1; then
      echo "rendered but should not: $*" >&2
      exit 1
    fi
  }
  refuse                                   # defaults: no sources
  refuse $src --set role=video             # video with the slim flavour
  refuse $src --set role=nope
  refuse $src --set autoscaling.minReplicas=5 --set autoscaling.maxReplicas=2
  refuse $src --set image.flavor=tiny      # values.schema.json
  echo "refused as expected"
}

check_kustomize() {
  for o in deploy/kubernetes/overlays/*/; do
    o="${o%/}"
    echo "== kustomize build ${o#deploy/} | kubeconform"
    drun "$KUSTOMIZE_IMAGE" build "/$o" | kubeconform
  done
}

check_names() {
  echo "== metric names"
  known="$(mktemp)"
  trap 'rm -f "$known"' EXIT
  # xi_* from the code; histogram series; the Go and process collectors'
  # series the rules and the dashboard use (client_golang defaults).
  grep -hoE 'Name: +"[a-z_]+"' internal/obs/metrics/*.go | sed -E 's/.*"(.*)"/xi_\1/' | sort -u >"$known"
  for suffix in _bucket _count _sum; do
    sed "s/\$/$suffix/" "$known"
  done >>"$known.h"
  cat "$known.h" >>"$known" && rm -f "$known.h"
  printf '%s\n' up go_sched_gomaxprocs_threads go_gc_gomemlimit_bytes go_memstats_heap_inuse_bytes \
    go_goroutines process_resident_memory_bytes process_cpu_seconds_total process_start_time_seconds >>"$known"
  bad=0
  for f in deploy/observability/prometheus/rules.yml deploy/observability/grafana/dashboards/xtandard-image.json \
    deploy/kubernetes/components/keda/scaledobject.yaml deploy/kubernetes/components/hpa-queue-metric/kustomization.yaml \
    deploy/helm/xtandard-image/templates/scaledobject.yaml docs/OPERATIONS.md; do
    for m in $(grep -oE '(^|[^:a-zA-Z_*])(xi|go|process)_[a-z0-9_]*[a-z0-9]' "$f" | sed -E 's/^[^a-z]//' | sort -u); do
      case "$m" in
        xi_video_transform | xi_video_spool | xi_sig) continue ;; # prose prefixes ("xi_video_transform_*"), the xi_sig query parameter
      esac
      if ! grep -qx "$m" "$known"; then
        echo "$f: unknown metric $m" >&2
        bad=1
      fi
    done
  done
  echo "== XI_* variable names"
  # node_modules, .sst and .cache hold the WP-149 tooling's installed packages and downloads.
  for f in $(find deploy \( -name node_modules -o -name .sst -o -name .cache \) -prune -o -type f \( -name '*.yaml' -o -name '*.yml' -o -name '*.tpl' -o -name '*.json' \) -print) docs/OPERATIONS.md; do
    for v in $(grep -oE 'XI_[A-Z0-9_]*[A-Z0-9]' "$f" | sort -u); do
      case "$v" in
        XI_IMAGE | XI_CLIENTS_WORKFLOW | XI_DEPLOY_WORKFLOW | XI_XTANDARD_IMAGE_SERVICE_HOST) continue ;; # tooling variables; an injected service-link name (OPERATIONS §7)
        XI_SOURCES_ORIGIN_*|XI_SOURCES_BENCH_*|XI_SOURCES_WEB_* | XI_SOURCES_LOCAL_*) v="XI_SOURCES_<NAME>_${v#XI_SOURCES_*_}" ;;
      esac
      grep -qF "\`$v" docs/CONFIG.md || grep -qF "$v\`" docs/CONFIG.md || {
        # "XI_CACHE_RESULT_DISK_PATH / _BYTES"-style rows.
        stem="${v%_*}"
        grep -qF "\`${stem}_" docs/CONFIG.md && grep -qF "_${v##*_}\`" docs/CONFIG.md
      } || {
        echo "$f: $v is not in docs/CONFIG.md" >&2
        bad=1
      }
    done
  done
  [ "$bad" = 0 ] && echo "all names exist"
  return "$bad"
}

check_compose() {
  echo "== docker compose config"
  docker compose -f deploy/observability/compose.yml config --quiet
  echo "valid"
}

check_json() {
  echo "== dashboard JSON"
  for f in deploy/observability/grafana/dashboards/*.json deploy/helm/xtandard-image/values.schema.json; do
    docker run --rm -i "$JQ_IMAGE" empty <"$f"
    echo "$f: valid JSON"
  done
}

what="${1:-all}"
case "$what" in
  all) check_gen; check_json; check_names; check_rules; check_kustomize; check_helm; check_compose ;;
  gen) check_gen ;;
  json) check_json ;;
  names) check_names ;;
  rules) check_rules ;;
  helm) check_helm ;;
  kustomize) check_kustomize ;;
  compose) check_compose ;;
  *) echo "usage: $0 [all|gen|json|names|rules|helm|kustomize|compose]" >&2; exit 2 ;;
esac
