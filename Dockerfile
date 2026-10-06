# syntax=docker/dockerfile:1.7
#
# xtandard-image release app image, one flavor per build (XI_FLAVOR = slim,
# full or gpl, on the base of the same flavor). WP-00 walking skeleton promoted
# by WP-37, flavors by WP-118; see docs/RELEASE.md and docs/ARCHITECTURE.md §14.
#
#   stage build    <base>-<flavor>-builder: Go 1.27.1 + libvips headers → cgo binary
#                  (-trimpath, -s -w, -pgo=auto with cmd/xtandard-image/default.pgo,
#                  GOAMD64=v2 on amd64, GOARM64 left at v8.0)
#   stage debug    unstripped binary for crash symbolication (only built with
#                  --target debug; the release workflow exports it)
#   stage runtime  <base>-<flavor>: ubuntu:noble + ca-certificates/tzdata + the
#                  /opt/xtandard runtime libraries, UID 65532, jemalloc preloaded;
#                  only the binary and the flavor's OCI labels are added
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
#   XI_APP_BASE=xtandard-base:dev-gpl task release:build     # gpl: XI_FLAVOR=gpl GO_TAGS=ffmpeg

ARG BASE_BUILDER=ghcr.io/xantiagoma/xtandard-image-base:8.18.6-r7-slim-builder
ARG BASE_RUNTIME=ghcr.io/xantiagoma/xtandard-image-base:8.18.6-r7-slim
# App flavor: slim | full | gpl, matching the base (its dev.xtandard.base.flavor
# label). It selects the OCI labels (licences, description, dev.xtandard.flavor)
# and names the debug binary; full and gpl need GO_TAGS=ffmpeg (checked below).
ARG XI_FLAVOR=slim

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
ARG XI_FLAVOR

# With PGO=auto, `go version -m` must report -pgo=…/default.pgo: fail the
# build if the profile is not picked up. A full or gpl app without the ffmpeg
# tag would silently lose video: fail that too.
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
  set -eu; \
  case "${XI_FLAVOR}:,${GO_TAGS}," in \
    slim:*|full:*,ffmpeg,*|gpl:*,ffmpeg,*) ;; \
    *) echo "XI_FLAVOR=${XI_FLAVOR} GO_TAGS=${GO_TAGS}: want slim, or full/gpl with GO_TAGS=ffmpeg" >&2; exit 1 ;; \
  esac; \
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
ARG XI_FLAVOR

RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
  set -eu; \
  if [ "${TARGETARCH:-$(go env GOARCH)}" = amd64 ]; then export GOAMD64=v2; fi; \
  date="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)"; \
  CGO_ENABLED=1 go build -trimpath -buildvcs=false -pgo="${PGO}" -tags="${GO_TAGS}" \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${date}" \
    -o "/out/xtandard-image-${VERSION}-${XI_FLAVOR}-linux-${TARGETARCH:-$(go env GOARCH)}.debug" ./cmd/xtandard-image

# docker build --target debug --output type=local,dest=dist .
FROM scratch AS debug
COPY --from=build-debug /out/ /

# ==============================================================================
FROM ${BASE_RUNTIME} AS runtime-common

ARG BASE_RUNTIME
ARG VERSION=dev
ARG COMMIT=unknown
# licenses, description and dev.xtandard.flavor come from the flavor stage
# below (runtime-slim, runtime-full, runtime-gpl).
LABEL org.opencontainers.image.title="xtandard-image" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.url="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.documentation="https://github.com/xantiagoma/xtandard-image/blob/main/docs/CONFIG.md" \
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
COPY LICENSE NOTICE /usr/share/doc/xtandard-image/

USER 65532:65532
WORKDIR /tmp
EXPOSE 8080 9090
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --start-interval=1s --retries=3 \
  CMD ["/usr/local/bin/xtandard-image", "health", "--url", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/usr/local/bin/xtandard-image"]
CMD ["serve"]

# ==============================================================================
# Flavor labels (docs/RELEASE.md, ADR 0006). The labels override the base's.
# slim and full contain only permissive third-party code (MIT, BSD, Apache,
# LGPL libraries used as shared objects: the base's check-licenses.sh gate);
# gpl adds GPL FFmpeg/x264/x265 (GPL-2.0-or-later), the metafile converter
# (GPL-2.0-only), Ghostscript (AGPL), and LibRaw built with the Adobe DNG SDK
# (LicenseRef-Adobe-DNG-SDK) and the GoPro GPR SDK (Apache-2.0 OR MIT), which
# only gpl ships, as its /opt/xtandard/share/licenses/LICENSE-GPL.md lists.
# Every flavour ships this project's LICENSE and NOTICE (third-party notices,
# including what only gpl contains) in /usr/share/doc/xtandard-image/.
FROM runtime-common AS runtime-slim
LABEL org.opencontainers.image.description="Self-hosted image processing proxy (Go + libvips), slim flavor" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.flavor="slim"

FROM runtime-common AS runtime-full
LABEL org.opencontainers.image.description="Self-hosted image processing proxy (Go + libvips), full flavor: video thumbnails (LGPL FFmpeg) and object/face detection (ONNX Runtime)" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.flavor="full"

FROM runtime-common AS runtime-gpl
LABEL org.opencontainers.image.description="Self-hosted image processing proxy (Go + libvips), gpl flavor: full plus GPL FFmpeg (x264, x265), EPS (Ghostscript), WMF/EMF, and JPEG XL DNG and GoPro GPR raw (Adobe DNG SDK, GoPro GPR SDK); contains GPL and AGPL software and the Adobe DNG SDK, see /opt/xtandard/share/licenses/LICENSE-GPL.md" \
      org.opencontainers.image.licenses="GPL-2.0-or-later AND GPL-2.0-only AND AGPL-3.0-or-later AND MIT AND LicenseRef-Adobe-DNG-SDK AND (Apache-2.0 OR MIT)" \
      dev.xtandard.flavor="gpl"

# docker build --target runtime (the default: last stage) --build-arg XI_FLAVOR=…
FROM runtime-${XI_FLAVOR} AS runtime
