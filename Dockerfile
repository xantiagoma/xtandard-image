# syntax=docker/dockerfile:1.7
#
# xtandard-image release app image (slim). WP-00 walking skeleton promoted by
# WP-37; see docs/RELEASE.md and docs/ARCHITECTURE.md §14.
#
#   stage build    <base>-slim-builder: Go 1.27.1 + libvips headers → cgo binary
#                  (-trimpath, -s -w, -pgo=auto with cmd/xtandard-image/default.pgo,
#                  GOAMD64=v2 on amd64, GOARM64 left at v8.0)
#   stage debug    unstripped binary for crash symbolication (only built with
#                  --target debug; the release workflow exports it)
#   stage runtime  <base>-slim: ubuntu:noble + ca-certificates/tzdata + the
#                  /opt/xtandard runtime libraries, UID 65532, jemalloc preloaded;
#                  only the binary is added
#
# The base images are pinned in docker/base/versions.sh (single source of
# truth). CI and the release workflow resolve the tags below to digests with
# scripts/base-image-ref.sh and pass them as BASE_BUILDER / BASE_RUNTIME
# (image@sha256:…); scripts/base-image-ref.sh --check keeps these defaults in
# sync with versions.sh.
#
# Build natively for the host architecture (no emulation, go-standards §9):
#   task release:build                                        # GHCR base, pinned by digest
#   docker build -t xtandard-image:dev \
#     --build-arg BASE_BUILDER=xtandard-base:dev-builder \
#     --build-arg BASE_RUNTIME=xtandard-base:dev .            # locally built base

ARG BASE_BUILDER=ghcr.io/xantiagoma/xtandard-image-base:8.18.6-r7-slim-builder
ARG BASE_RUNTIME=ghcr.io/xantiagoma/xtandard-image-base:8.18.6-r7-slim

# ==============================================================================
FROM ${BASE_BUILDER} AS source

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod \
  go mod download

COPY cmd ./cmd
COPY internal ./internal

# ==============================================================================
FROM source AS build

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
# Commit time (git log -1 --pretty=%ct); buildx forwards it from the
# environment. Used for main.date so the binary stays reproducible.
ARG SOURCE_DATE_EPOCH=0
# auto uses cmd/xtandard-image/default.pgo; `off` only for bootstrapping a new
# profile from scratch (docs/RELEASE.md, "Refreshing the PGO profile").
ARG PGO=auto
# Build tags: `ffmpeg` on a full-flavor base (video thumbnails, ADR 0010;
# task release:build sets it from the base image), empty for slim.
ARG GO_TAGS=

# With PGO=auto, `go version -m` must report -pgo=…/default.pgo: fail the
# build if the profile is not picked up.
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
  set -eu; \
  if [ "${TARGETARCH:-$(go env GOARCH)}" = amd64 ]; then export GOAMD64=v2; fi; \
  date="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)"; \
  CGO_ENABLED=1 go build -trimpath -buildvcs=false -pgo="${PGO}" -tags="${GO_TAGS}" \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${date}" \
    -o /out/xtandard-image ./cmd/xtandard-image; \
  go version -m /out/xtandard-image | grep -E '(-pgo=|GOAMD64|GOARM64|CGO_ENABLED)' || true; \
  if [ "${PGO}" = auto ]; then go version -m /out/xtandard-image | grep -q -- '-pgo=.*default.pgo'; fi; \
  /out/xtandard-image version

# ==============================================================================
FROM source AS build-debug

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE_DATE_EPOCH=0
ARG PGO=auto
# Build tags: `ffmpeg` on a full-flavor base (video thumbnails, ADR 0010;
# task release:build sets it from the base image), empty for slim.
ARG GO_TAGS=

RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
  set -eu; \
  if [ "${TARGETARCH:-$(go env GOARCH)}" = amd64 ]; then export GOAMD64=v2; fi; \
  date="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)"; \
  CGO_ENABLED=1 go build -trimpath -buildvcs=false -pgo="${PGO}" -tags="${GO_TAGS}" \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${date}" \
    -o "/out/xtandard-image-${VERSION}-linux-${TARGETARCH:-$(go env GOARCH)}.debug" ./cmd/xtandard-image

# docker build --target debug --output type=local,dest=dist .
FROM scratch AS debug
COPY --from=build-debug /out/ /

# ==============================================================================
FROM ${BASE_RUNTIME} AS runtime

ARG BASE_RUNTIME
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="xtandard-image" \
      org.opencontainers.image.description="Self-hosted image processing proxy (Go + libvips), slim flavor" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.url="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.documentation="https://github.com/xantiagoma/xtandard-image/blob/main/docs/CONFIG.md" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.base.name="${BASE_RUNTIME}"

# ARCHITECTURE §14 step 3. The base runtime already sets most of these; they
# are repeated so the app image contract does not depend on the base's ENV.
# /tmp is the only path the process may write (fontconfig cache, optional
# disk cache): run read-only with a tmpfs there (docs/RELEASE.md).
# MALLOC_CONF=narenas:2 is jemalloc's equivalent of MALLOC_ARENA_MAX=2 (which
# only glibc reads): jemalloc's default of 4 arenas per CPU kept freed pages
# of every busy thread and more than doubled peak RSS under load, with no
# throughput gain (WP-87, bench/RESULTS.md). The base runtime sets it too
# since r3 (WP-93); it stays here so an older or third-party BASE_RUNTIME
# cannot silently drop it (scripts/verify-app-image.sh checks it).
ENV LD_LIBRARY_PATH=/opt/xtandard/lib \
    LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 \
    MALLOC_ARENA_MAX=2 \
    MALLOC_CONF=narenas:2 \
    MAGICK_CONFIGURE_PATH=/opt/xtandard/etc/ImageMagick-7 \
    FONTCONFIG_PATH=/opt/xtandard/etc/fonts \
    VIPS_BLOCK_UNTRUSTED=1 \
    VIPS_WARNING=0 \
    TMPDIR=/tmp \
    XDG_CACHE_HOME=/tmp/.cache \
    XI_SERVER_ADDR=:8080

COPY --from=build --chmod=0555 /out/xtandard-image /usr/local/bin/xtandard-image

USER 65532:65532
WORKDIR /tmp
EXPOSE 8080 9090
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --start-interval=1s --retries=3 \
  CMD ["/usr/local/bin/xtandard-image", "health", "--url", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/usr/local/bin/xtandard-image"]
CMD ["serve"]
