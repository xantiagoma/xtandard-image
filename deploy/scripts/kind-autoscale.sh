#!/bin/sh
# End-to-end autoscaling check in a throwaway kind cluster (task
# deploy:autoscale). Installs KEDA, a minimal Prometheus and the Helm chart
# (deploy/scripts/kind/values-autoscale.yaml), drives AVIF traffic with k6 for
# LOAD_SECONDS, then idles for IDLE_SECONDS, sampling the replica count, the
# KEDA metric and the 429 rate every 10 s. Deletes the cluster at the end
# (KEEP=1 keeps it).
#
#   XI_IMAGE=xtandard-image:main-gpl sh deploy/scripts/kind-autoscale.sh
#
# Needs Docker, kubectl and mise (kind and helm are run with pinned versions
# through `mise exec`). Publishes nothing.
set -eu
cd "$(dirname "$0")/../.."

: "${XI_IMAGE:=xtandard-image:main-gpl}"
: "${KIND_VERSION:=0.30.0}"
: "${HELM_VERSION:=3.19.0}"
: "${KEDA_CHART_VERSION:=2.20.2}"
: "${K6_IMAGE:=grafana/k6:1.3.0}"
: "${CLUSTER:=xi-autoscale}"
: "${RATE:=60}"
: "${LOAD_SECONDS:=240}"
: "${IDLE_SECONDS:=240}"

# mise must not read a project config (worktrees may be untrusted).
tools="$(mktemp -d)"
kind() { (cd "$tools" && mise exec "kind@$KIND_VERSION" -- kind "$@"); }
helm() { (cd "$tools" && mise exec "helm@$HELM_VERSION" -- helm "$@"); }
kubeconfig="$tools/kubeconfig"
export KUBECONFIG="$kubeconfig"
repo="$(pwd)"

cleanup() {
  if [ "${KEEP:-0}" != 1 ]; then kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true; fi
  rm -rf "$tools"
}
trap cleanup EXIT

log() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*"; }

log "create kind cluster $CLUSTER"
kind create cluster --name "$CLUSTER" --kubeconfig "$kubeconfig" --wait 120s >/dev/null
log "load images"
kind load docker-image "$XI_IMAGE" --name "$CLUSTER" >/dev/null
docker pull -q prom/prometheus:v3.7.3 >/dev/null
kind load docker-image prom/prometheus:v3.7.3 --name "$CLUSTER" >/dev/null
docker pull -q "$K6_IMAGE" >/dev/null
kind load docker-image "$K6_IMAGE" --name "$CLUSTER" >/dev/null

log "install KEDA $KEDA_CHART_VERSION"
helm repo add kedacore https://kedacore.github.io/charts --force-update >/dev/null
helm install keda kedacore/keda --version "$KEDA_CHART_VERSION" -n keda --create-namespace --wait --timeout 5m >/dev/null

log "install Prometheus"
kubectl apply -f deploy/scripts/kind/prometheus.yaml >/dev/null
kubectl -n monitoring rollout status deploy/prometheus --timeout 180s >/dev/null

log "install the chart"
kubectl create namespace xi >/dev/null
kubectl -n xi create configmap xi-fixtures --from-file=testdata/fixtures/jpeg/baseline.jpg >/dev/null
repo_name="${XI_IMAGE%:*}"
tag="${XI_IMAGE##*:}"
helm install xi "$repo/deploy/helm/xtandard-image" -n xi \
  -f "$repo/deploy/scripts/kind/values-autoscale.yaml" \
  --set image.repository="$repo_name" --set image.tag="$tag" --wait --timeout 5m >/dev/null
helm test xi -n xi >/dev/null && log "helm test passed"
kubectl -n xi wait --for=condition=Ready scaledobject/xi-xtandard-image --timeout 120s >/dev/null

prom() {
  kubectl -n monitoring exec deploy/prometheus -- wget -qO- \
    "http://localhost:9090/api/v1/query?query=$1" 2>/dev/null |
    sed -n 's/.*"value":\[[^,]*,"\([^"]*\)".*/\1/p'
}
# URL-encoded PromQL: per-pod jobs per worker (the KEDA trigger) and 429/s.
q_util='sum(avg_over_time(xi_queue_depth%7Bnamespace%3D%22xi%22%7D%5B1m%5D)%20%2F%20go_sched_gomaxprocs_threads%7Bnamespace%3D%22xi%22%7D)%20%2F%20count(up%7Bnamespace%3D%22xi%22%7D%3D%3D1)'
q_shed='sum(rate(xi_overload_total%7Bnamespace%3D%22xi%22%7D%5B1m%5D))'
q_rps='sum(rate(xi_requests_total%7Bnamespace%3D%22xi%22%7D%5B1m%5D))'

sample() {
  r="$(kubectl -n xi get deploy xi-xtandard-image -o jsonpath='{.status.replicas}/{.status.readyReplicas}')"
  log "phase=$1 replicas(total/ready)=$r jobs_per_worker=$(prom "$q_util") req_per_s=$(prom "$q_rps") shed_per_s=$(prom "$q_shed")"
}

log "start load: $RATE req/s of AVIF resizes for ${LOAD_SECONDS}s"
kubectl -n xi create configmap k6-script --from-file=deploy/observability/load/traffic.js >/dev/null
kubectl -n xi run k6 --image="$K6_IMAGE" --image-pull-policy=Never --restart=Never \
  --overrides='{"spec":{"volumes":[{"name":"s","configMap":{"name":"k6-script"}}],"containers":[{"name":"k6","image":"'"$K6_IMAGE"'","imagePullPolicy":"Never","args":["run","--quiet","/s/traffic.js"],"volumeMounts":[{"name":"s","mountPath":"/s"}],"env":[{"name":"TARGET","value":"http://xi-xtandard-image"},{"name":"RATE","value":"'"$RATE"'"},{"name":"DURATION","value":"'"$LOAD_SECONDS"'s"},{"name":"IMAGES","value":"jpeg/baseline.jpg"},{"name":"FORMATS","value":"avif"}]}]}}' >/dev/null

end=$(( $(date +%s) + LOAD_SECONDS ))
while [ "$(date +%s)" -lt "$end" ]; do sample load; sleep 10; done
end=$(( $(date +%s) + IDLE_SECONDS ))
while [ "$(date +%s)" -lt "$end" ]; do sample idle; sleep 10; done

kubectl -n xi logs k6 2>/dev/null | grep -E 'http_reqs|http_req_duration|http_req_failed' || true
kubectl -n xi get hpa
kubectl -n xi describe hpa | sed -n '/Events:/,$p' | tail -15
