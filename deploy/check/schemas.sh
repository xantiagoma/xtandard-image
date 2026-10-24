#!/usr/bin/env bash
# shellcheck disable=SC2015  # ok() never fails: `check && ok || bad` is an if-then-else here.
# Offline validation of every platform file in deploy/ that has a schema or a parser, in pinned
# tools (docs/DEPLOY.md "Verification"). Downloads schemas into deploy/.cache; no account needed.
#
#   deploy/check/schemas.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
cache="${DEPLOY_CHECK_CACHE:-$root/deploy/.cache}/schemas"
mkdir -p "$cache"
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

# Pinned tools.
uv_image="ghcr.io/astral-sh/uv:0.9.5-python3.13-bookworm-slim"
kubeconform_image="ghcr.io/yannh/kubeconform:v0.7.0"
nomad_image="hashicorp/nomad:2.0.7"
# modal: the client deploy/modal/app.py is written for (loads the app; no credentials needed).
py_deps=(--with pyyaml==6.0.3 --with botocore==1.43.118 --with check-jsonschema==0.38.2 --with modal==1.6.2)

# py <args...>: python with the pinned packages, in the uv container (or a local uv).
py() {
  if command -v uv >/dev/null 2>&1; then
    uv run -q --no-project --python 3.13 "${py_deps[@]}" "$@"
  else
    docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e UV_CACHE_DIR=/tmp/uv -v "$root:$root" -w "$root" \
      "$uv_image" uv run -q --no-project --python 3.13 "${py_deps[@]}" "$@"
  fi
}

fetch() { # url file
  [[ -s "$cache/$2" ]] || curl -fsSL -o "$cache/$2" "$1"
}

# --- Compose files: docker compose config (Compose Specification) ---------------------------
export XI_SIGNING_KEYS=00 XI_SIGNING_SALTS=00 XI_VIDEO_TRANSFORM_STREAM_KEYS=00
for f in deploy/compose/compose.yml deploy/compose/compose.split.yml deploy/coolify/compose.yml \
  deploy/openship/docker-compose.yml; do
  docker compose -f "$f" config -q && ok "$f (docker compose config)" || bad "$f"
done
# Swarm stacks: docker stack config renders them offline with the stack schema (v3.x).
for f in deploy/swarm/stack.yml deploy/dokploy/compose.yml; do
  docker stack config -c "$f" >/dev/null && ok "$f (docker stack config)" || bad "$f"
done
# nginx.conf of the split setup.
docker run --rm -v "$root/deploy/compose/nginx.conf:/etc/nginx/nginx.conf:ro" --add-host image:127.0.0.1 \
  --add-host video:127.0.0.1 nginx:1.29-alpine nginx -t -q && ok "deploy/compose/nginx.conf (nginx -t)" \
  || bad "deploy/compose/nginx.conf"

# --- JSON Schemas: Fly (SchemaStore), Railway, Render -----------------------------------------
fetch https://www.schemastore.org/fly.json fly.json
fetch https://railway.com/railway.schema.json railway.json
fetch https://render.com/schema/render.yaml.json render.json
for f in deploy/fly/fly.toml deploy/fly/fly.video.toml; do
  py check-jsonschema --schemafile "$cache/fly.json" "$f" && ok "$f (SchemaStore fly.json)" || bad "$f"
done
py check-jsonschema --schemafile "$cache/railway.json" deploy/railway/railway.json \
  && ok "deploy/railway/railway.json (railway.schema.json)" || bad "deploy/railway/railway.json"
py check-jsonschema --schemafile "$cache/render.json" deploy/render/render.yaml \
  && ok "deploy/render/render.yaml (render.yaml.json)" || bad "deploy/render/render.yaml"

# --- OpenAPI-derived: DigitalOcean app spec, Azure Container Apps (strict unknown keys) -------
fetch https://api-engineering.nyc3.cdn.digitaloceanspaces.com/spec-ci/DigitalOcean-public.v2.yaml do-openapi.yaml
py python -I deploy/check/validate.py openapi "$cache/do-openapi.yaml" app_spec deploy/digitalocean/app.yaml || fail=1
py python -I deploy/check/validate.py openapi \
  https://raw.githubusercontent.com/Azure/azure-rest-api-specs/main/specification/app/resource-manager/Microsoft.App/ContainerApps/stable/2026-01-01/openapi.json \
  ContainerApp deploy/azure-container-apps/containerapp.yaml || fail=1

# --- AWS CLI inputs against botocore's service models ----------------------------------------
while read -r svc op file; do
  py python -I deploy/check/validate.py botocore "$svc" "$op" "$file" || fail=1
done <<'EOF'
ecs RegisterTaskDefinition deploy/aws-ecs/task-definition.json
ecs CreateService deploy/aws-ecs/service.json
application-autoscaling RegisterScalableTarget deploy/aws-ecs/scalable-target.json
application-autoscaling PutScalingPolicy deploy/aws-ecs/scaling-policy-cpu.json
application-autoscaling PutScalingPolicy deploy/aws-ecs/scaling-policy-requests.json
apprunner CreateService deploy/aws-app-runner/create-service.json
apprunner CreateAutoScalingConfiguration deploy/aws-app-runner/autoscaling.json
EOF

# --- Cloud Run service YAML: Knative Serving v1 schema (Cloud Run's own annotations unchecked) -
for f in deploy/cloud-run/service.yaml deploy/cloud-run/service.video.yaml; do
  docker run --rm -v "$root/deploy:/deploy:ro" "$kubeconform_image" -strict -summary \
    -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
    "/$f" && ok "$f (kubeconform, Knative Service v1)" || bad "$f"
done

# --- Modal app: loads with the pinned client, no deprecated API, one endpoint per env dict -----
py python -I deploy/check/validate.py modal deploy/modal/app.py || fail=1

# --- Nomad ------------------------------------------------------------------------------------
docker run --rm -v "$root/deploy/nomad:/job:ro" -e NOMAD_ADDR=http://127.0.0.1:1 "$nomad_image" \
  job validate /job/xtandard-image.nomad.hcl && ok "deploy/nomad (nomad job validate)" || bad "deploy/nomad"

# --- The server accepts every platform's environment ------------------------------------------
# Each container's XI_* variables go through `xtandard-image config validate` in the image
# (unknown XI_* variables, bad values and inconsistent settings fail startup the same way).
# XI_IMAGE: a full or gpl image, so video-role settings validate too.
image="${XI_IMAGE:-xtandard-image:main-gpl}"
if docker image inspect "$image" >/dev/null 2>&1; then
  envdir="$(mktemp -d "$cache/env.XXXXXX")"
  trap 'rm -rf "$envdir"' EXIT
  docker compose -f deploy/compose/compose.split.yml config --format json >"$envdir/split.json"
  docker compose -f deploy/compose/compose.yml config --format json >"$envdir/single.json"
  docker compose -f deploy/coolify/compose.yml config --format json >"$envdir/coolify.json"
  docker stack config -c deploy/swarm/stack.yml >"$envdir/swarm.yaml"
  docker stack config -c deploy/dokploy/compose.yml >"$envdir/dokploy.yaml"
  while read -r kind file label; do
    out="$envdir/$(echo "$label" | tr '/.' '__')"
    mkdir -p "$out"
    py python -I deploy/check/validate.py env "$kind" "$file" "$out" || { bad "$label: env extraction"; continue; }
    for e in "$out"/*.env; do
      [[ -e "$e" ]] || continue
      if res="$(docker run --rm --env-file "$e" "$image" config validate 2>&1)" && ! grep -q "unknown environment" <<<"$res"; then
        ok "$label [container $(basename "$e" .env)] (config validate in $image)"
      else
        bad "$label [container $(basename "$e" .env)]: $res"
      fi
    done
  done <<EOF
compose $envdir/single.json deploy/compose/compose.yml
compose $envdir/split.json deploy/compose/compose.split.yml
compose $envdir/coolify.json deploy/coolify/compose.yml
compose $envdir/swarm.yaml deploy/swarm/stack.yml
compose $envdir/dokploy.yaml deploy/dokploy/compose.yml
fly deploy/fly/fly.toml deploy/fly/fly.toml
fly deploy/fly/fly.video.toml deploy/fly/fly.video.toml
knative deploy/cloud-run/service.yaml deploy/cloud-run/service.yaml
knative deploy/cloud-run/service.video.yaml deploy/cloud-run/service.video.yaml
aca deploy/azure-container-apps/containerapp.yaml deploy/azure-container-apps/containerapp.yaml
render deploy/render/render.yaml deploy/render/render.yaml
do deploy/digitalocean/app.yaml deploy/digitalocean/app.yaml
ecs deploy/aws-ecs/task-definition.json deploy/aws-ecs/task-definition.json
apprunner deploy/aws-app-runner/create-service.json deploy/aws-app-runner/create-service.json
modal deploy/modal/app.py deploy/modal/app.py
EOF
  rm -rf "$envdir"
else
  echo "skip config validate: image $image not present (set XI_IMAGE)"
fi

exit "$fail"
