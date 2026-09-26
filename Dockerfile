# syntax=docker/dockerfile:1.7
#
# xtandard-image app image (walking skeleton, WP-00; WP-37 promotes it to the
# release image). Builds on the WP-05 base image:
#
#   stage 1  ${BASE}-builder  Go 1.27.1 + libvips headers → cgo binary
#   stage 2  ${BASE}          runtime libraries only, UID 65532, jemalloc
#
# Build natively for the host architecture (no emulation, go-standards §9):
#   docker build -t xtandard-image:dev .                                    # GHCR base
#   docker build -t xtandard-image:dev --build-arg BASE=xtandard-base:dev . # local base

ARG BASE=ghcr.io/xantiagoma/xtandard-image-base:slim

# ==============================================================================
FROM ${BASE}-builder AS build

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod \
  go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=1 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/xtandard-image ./cmd/xtandard-image \
  && /out/xtandard-image version

# ==============================================================================
FROM ${BASE} AS runtime

ARG VERSION=dev
LABEL org.opencontainers.image.title="xtandard-image" \
      org.opencontainers.image.description="Self-hosted image processing proxy (Go + libvips)" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=build /out/xtandard-image /usr/local/bin/xtandard-image

# The base runtime already sets LD_LIBRARY_PATH, LD_PRELOAD (jemalloc),
# MALLOC_ARENA_MAX and FONTCONFIG_PATH. The loader allowlist is enforced in
# internal/vips; VIPS_BLOCK_UNTRUSTED is belt and braces for the vips CLI.
ENV VIPS_BLOCK_UNTRUSTED=1 \
    XI_SERVER_ADDR=:8080

USER 65532:65532
EXPOSE 8080 9090
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/usr/local/bin/xtandard-image", "health", "--url", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/usr/local/bin/xtandard-image"]
CMD ["serve"]
