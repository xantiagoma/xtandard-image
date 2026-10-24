#!/usr/bin/env bash
# shellcheck disable=SC2015  # ok() never fails: `check && ok || bad` is an if-then-else here.
# Runs deploy/compose (single and image/video split) and, when the host is not a swarm node,
# deploy/swarm in a throwaway single-node swarm, against a local image, then tears everything
# down. Checks health, an image URL, and that video paths reach the video fleet.
#
#   XI_IMAGE=xtandard-image:main-gpl deploy/check/compose-run.sh
#
# Env: XI_IMAGE (default xtandard-image:main-gpl; full or gpl so the video fleet starts),
#      XI_CHECK_PORT (default 18480), XI_CHECK_SWARM (default 1; 0 skips the swarm run).
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
image="${XI_IMAGE:-xtandard-image:main-gpl}"
port="${XI_CHECK_PORT:-18480}"
export XI_IMAGE="$image" XI_VIDEO_IMAGE="$image" XI_PUBLISH_PORT="$port"
XI_SIGNING_KEYS="$(openssl rand -hex 32)"
XI_SIGNING_SALTS="$(openssl rand -hex 32)"
XI_VIDEO_TRANSFORM_STREAM_KEYS="$(openssl rand -hex 32)"
export XI_SIGNING_KEYS XI_SIGNING_SALTS XI_VIDEO_TRANSFORM_STREAM_KEYS
base="http://127.0.0.1:$port"
fail=0

ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

wait_up() {
  for _ in $(seq 1 90); do
    curl -fsS -o /dev/null "$base/healthz" 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

# status <url> → HTTP status code
status() { curl -s -o /dev/null -w '%{http_code}' "$1"; }

# Signed imgproxy image path and video path (/video/<sig>/<options>, URL-DIALECTS §3.8).
img_path="$(scripts/sign-url.sh '/rs:fit:64:0/plain/xi://local/jpeg/baseline.jpg@webp')"
vid_signed="$(scripts/sign-url.sh '/video/w:160/vd:1/mu:1/plain/xi://local/video/clip-vp9.webm@webm')"
vid_sig="${vid_signed#/}"; vid_sig="${vid_sig%%/*}"
vid_path="/video/$vid_sig/w:160/vd:1/mu:1/plain/xi://local/video/clip-vp9.webm@webm"

check_common() { # $1 = label
  [[ "$(status "$base/healthz")" == 200 ]] && ok "$1 /healthz 200" || bad "$1 /healthz"
  [[ "$(status "$base/readyz")" == 200 ]] && ok "$1 /readyz 200" || bad "$1 /readyz"
  [[ "$(status "$base$img_path")" == 200 ]] && ok "$1 signed image 200" || bad "$1 signed image ($(status "$base$img_path"))"
  [[ "$(status "$base/insecure/rs:fit:64:0/plain/xi://local/jpeg/baseline.jpg")" == 403 ]] \
    && ok "$1 unsigned image 403" || bad "$1 unsigned image not refused"
}

# no_admin_published <compose args...>: no container of the project publishes 9090.
no_admin_published() {
  if docker compose "$@" ps --format '{{.Publishers}}' | grep -q ':9090\|->9090'; then
    bad "admin port 9090 published"
  else
    ok "admin port 9090 not published"
  fi
}

compose_single() {
  local f=(-f deploy/compose/compose.yml -f deploy/check/local.single.yml -p xi-deploy-check-single)
  echo "== compose single ($image)"
  docker compose "${f[@]}" up -d --quiet-pull
  if wait_up; then check_common "single"; no_admin_published "${f[@]}"; else bad "single did not become healthy"; docker compose "${f[@]}" logs | tail -40; fi
  docker compose "${f[@]}" down -v --remove-orphans
}

compose_split() {
  local f=(-f deploy/compose/compose.split.yml -f deploy/check/local.split.yml -p xi-deploy-check-split)
  echo "== compose split ($image): image x2, video x1, nginx"
  docker compose "${f[@]}" up -d --quiet-pull --scale image=2 --scale video=1 --wait
  if wait_up; then
    check_common "split"
    no_admin_published "${f[@]}"
    local h code
    code="$(status "$base$vid_path")"
    h="$(curl -s -D - -o /dev/null "$base$vid_path" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-xi-fleet"{print $2}')"
    [[ "$code" == 200 && "$h" == video:8080 ]] && ok "split /video/… → video fleet (200, $h)" || bad "split /video/… ($code, $h)"
    h="$(curl -s -D - -o /dev/null "$base$img_path" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-xi-fleet"{print $2}')"
    [[ "$h" == image:8080 ]] && ok "split image path → image fleet" || bad "split image path ($h)"
    for p in "/q/video?url=xi://local/video/clip-vp9.webm" "/_stream/x/master.m3u8"; do
      h="$(curl -s -D - -o /dev/null "$base$p" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-xi-fleet"{print $2}')"
      [[ "$h" == video:8080 ]] && ok "split $p → video fleet" || bad "split $p ($h)"
    done
    # The roles hold: the image fleet refuses video, the video fleet refuses images.
    local img_c vid_c
    img_c="$(docker compose "${f[@]}" ps -q image | head -1)"
    vid_c="$(docker compose "${f[@]}" ps -q video | head -1)"
    code="$(docker exec "$img_c" /usr/local/bin/xtandard-image health --url "http://127.0.0.1:8080$vid_path" >/dev/null 2>&1 && echo 200 || echo not-200)"
    [[ "$code" == not-200 ]] && ok "split image fleet refuses video directly" || bad "image fleet served video"
    code="$(docker exec "$vid_c" /usr/local/bin/xtandard-image health --url "http://127.0.0.1:8080$img_path" >/dev/null 2>&1 && echo 200 || echo not-200)"
    [[ "$code" == not-200 ]] && ok "split video fleet refuses images directly" || bad "video fleet served an image"
    [[ "$(docker compose "${f[@]}" ps -q image | wc -l | tr -d ' ')" == 2 ]] && ok "split image replicas 2" || bad "image replicas"
  else
    bad "split did not become healthy"; docker compose "${f[@]}" logs | tail -60
  fi
  docker compose "${f[@]}" down -v --remove-orphans
}

swarm_run() {
  local state
  state="$(docker info --format '{{.Swarm.LocalNodeState}}')"
  if [[ "$state" != inactive ]]; then
    echo "== swarm: host is already a swarm node ($state); skipped"
    return
  fi
  echo "== swarm (throwaway single-node swarm)"
  docker swarm init --advertise-addr 127.0.0.1 >/dev/null
  trap 'docker stack rm xi-deploy-check >/dev/null 2>&1 || true; docker swarm leave --force >/dev/null 2>&1 || true' RETURN
  # Swarm cannot bind-mount relative paths: the local check uses an http source instead.
  XI_SOURCES_WEB_ALLOW="*" docker stack deploy --detach=false -c deploy/swarm/stack.yml xi-deploy-check >/dev/null
  if wait_up; then
    [[ "$(status "$base/healthz")" == 200 ]] && ok "swarm /healthz 200" || bad "swarm /healthz"
    local n
    n="$(docker service ls --filter name=xi-deploy-check_xtandard-image --format '{{.Replicas}}')"
    [[ "$n" == "2/2"* ]] && ok "swarm replicas $n" || bad "swarm replicas $n"
    docker service inspect xi-deploy-check_xtandard-image \
      --format '{{.Spec.UpdateConfig.Order}} {{.Spec.UpdateConfig.FailureAction}} {{.Spec.RollbackConfig.Order}}' \
      | grep -qx 'start-first rollback stop-first' && ok "swarm update/rollback config" || bad "swarm update config"
  else
    bad "swarm did not become healthy"; docker service ps --no-trunc xi-deploy-check_xtandard-image | tail -5
  fi
}

compose_single
compose_split
[[ "${XI_CHECK_SWARM:-1}" == 1 ]] && swarm_run
exit "$fail"
