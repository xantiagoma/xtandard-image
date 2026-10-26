#!/bin/sh
# One xtandard-image container with production settings (docs/DEPLOY.md "docker run").
#
#   export XI_SIGNING_KEYS="$(openssl rand -hex 32)" XI_SIGNING_SALTS="$(openssl rand -hex 32)"
#   XI_SOURCES_WEB_ALLOW=images.example.com deploy/docker/run.sh
#
# Image flavours: <version>-slim (stills), -full (video, detection), -gpl (H.264/HEVC, GPL).
# The admin listener stays on 127.0.0.1:9090 inside the container (the default), so /metrics is
# reachable only from the container's own network namespace; set XI_ADMIN_ADDR=:9090 and join
# a private network to scrape it from another container.
set -eu

: "${XI_SIGNING_KEYS:?export XI_SIGNING_KEYS (openssl rand -hex 32)}"
: "${XI_SIGNING_SALTS:?export XI_SIGNING_SALTS (openssl rand -hex 32)}"

exec docker run -d --name "${XI_NAME:-xtandard-image}" \
  --restart unless-stopped \
  --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --cap-drop ALL --security-opt no-new-privileges \
  --cpus "${XI_CPUS:-2}" --memory "${XI_MEMORY:-2g}" \
  --stop-timeout 40 \
  -p "${PUBLISH_PORT:-8080}:8080" \
  -e XI_SIGNING_KEYS -e XI_SIGNING_SALTS \
  -e XI_SOURCES=web \
  -e XI_SOURCES_WEB_TYPE=http \
  -e XI_SOURCES_WEB_ALLOW="${XI_SOURCES_WEB_ALLOW:-images.example.com}" \
  -e XI_CACHE_RESULT_STORES=memory \
  "${XI_IMAGE:-ghcr.io/xantiagoma/xtandard-image:0.1.1-slim}"
