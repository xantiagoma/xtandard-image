# syntax=docker/dockerfile:1.7
#
# xtandard-image base image (flavors: slim, full).
#
# Forked from imgproxy-docker-base (MIT, Copyright (c) 2017 Sergey Alexandrovich);
# see LICENSE.imgproxy-docker-base and docker/base/README.md.
#
# Targets:
#   builder       complete Go + libvips development environment (published as *-slim-builder)
#   runtime       shared libraries only, non-root, jemalloc preloaded (published as *-slim; default)
#   builder-full  builder + FFmpeg (LGPL) + ONNX Runtime headers/libs + models (*-full-builder)
#   runtime-full  runtime + FFmpeg (LGPL) libs/CLIs + ONNX Runtime + models (*-full)
#
# The full stages reuse the slim ones: full-deps starts from the finished slim `deps`
# stage, so slim and full share every slim layer and the slim image does not change
# when a full-only component (versions-full.sh) moves.
#
# Build natively per architecture (no cross compilation):
#   docker buildx build --platform linux/arm64 --target builder -t xtandard-base:dev-builder docker/base
#   docker buildx build --platform linux/arm64 -t xtandard-base:dev docker/base
#   docker buildx build --platform linux/arm64 --target builder-full -t xtandard-base:dev-full-builder docker/base
#   docker buildx build --platform linux/arm64 --target runtime-full -t xtandard-base:dev-full docker/base

ARG UBUNTU_IMAGE=ubuntu:noble

# ==============================================================================
FROM ${UBUNTU_IMAGE} AS base

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    bash \
    bzip2 \
    ca-certificates \
    curl \
    g++ \
    gcc \
    git \
    libc6-dev \
    make \
    pkg-config \
    xz-utils \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /root

# ==============================================================================
FROM base AS deps-src

COPY versions.sh download-deps.sh ./
RUN ./download-deps.sh

# ==============================================================================
FROM base AS deps

COPY versions.sh install-rust.sh ./

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    autoconf \
    automake \
    autopoint \
    file \
    fonts-dejavu-core \
    gettext \
    gperf \
    libcurl4-openssl-dev \
    libssl-dev \
    libtool \
    nasm \
    python3-venv \
  && rm -rf /var/lib/apt/lists/*

RUN ./install-rust.sh

RUN . ./versions.sh \
  && python3 -m venv /root/.python \
  && /root/.python/bin/pip install --no-cache-dir \
    "meson==${MESON_VERSION}" \
    "ninja==${NINJA_VERSION}" \
    "cmake==${CMAKE_VERSION}" \
    "packaging==${PY_PACKAGING_VERSION}"

COPY build-deps.sh check-licenses.sh magick-policy.xml ./
COPY --from=deps-src /root/deps /root/deps

# The flags depend on `uname -m`, so they come from a bash profile instead of ENV.
COPY bashrc.sh /root/.bashrc
ENV BASH_ENV=/root/.bashrc

RUN ./build-deps.sh

# Split the prefix into what the runtime needs and what only the builder needs.
# The runtime keeps the vips and vipsheader CLIs (diagnostics, verify-image.sh) and only
# the thread-safe libraw_r that libvips links (not the plain libraw).
COPY split-runtime.sh ./
RUN ./split-runtime.sh vips vipsheader

# ==============================================================================
# full flavor (WP-70): FFmpeg (LGPL, video only) + libvpx, ONNX Runtime, models.
FROM base AS full-deps-src

COPY versions-full.sh download-full-deps.sh ./
RUN ./download-full-deps.sh

# ==============================================================================
FROM deps AS full-deps

COPY versions-full.sh build-full-deps.sh ./
COPY --from=full-deps-src /root/full-deps /root/full-deps

RUN ./build-full-deps.sh

# Same split as slim plus the ffmpeg/ffprobe CLIs. ONNX Runtime and the models live
# outside /opt/xtandard; build-full-deps.sh stages them (with their symlinks) under
# /full-extra/{runtime,builder} for the final stages.
RUN ./split-runtime.sh vips vipsheader ffmpeg ffprobe

# ==============================================================================
FROM ${UBUNTU_IMAGE} AS builder-base

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    g++ \
    gcc \
    git \
    libc6-dev \
    make \
    pkg-config \
    tzdata \
  && rm -rf /var/lib/apt/lists/* \
  && git config --system --add safe.directory '*'

COPY versions.sh install-tools.sh /usr/local/share/xtandard/
RUN /usr/local/share/xtandard/install-tools.sh

ENV PATH=/usr/local/go/bin:/root/go/bin:/opt/xtandard/bin:$PATH \
    GOTOOLCHAIN=local \
    GOFLAGS=-buildvcs=false \
    CGO_ENABLED=1 \
    PKG_CONFIG_PATH=/opt/xtandard/lib/pkgconfig \
    CGO_CFLAGS="-O2 -g -I/opt/xtandard/include" \
    CGO_LDFLAGS="-L/opt/xtandard/lib -Wl,-rpath,/opt/xtandard/lib" \
    LD_LIBRARY_PATH=/opt/xtandard/lib \
    FONTCONFIG_PATH=/opt/xtandard/etc/fonts \
    VIPS_WARNING=0 \
    XI_IN_BASE_CONTAINER=true

WORKDIR /src
CMD ["bash"]

# ==============================================================================
FROM ${UBUNTU_IMAGE} AS runtime-base

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/log/apt/* /var/log/dpkg.log \
  && groupadd --system --gid 65532 xtandard \
  && useradd --system --uid 65532 --gid 65532 --no-create-home --home-dir /nonexistent \
    --shell /usr/sbin/nologin xtandard

ENV PATH=/opt/xtandard/bin:$PATH \
    LD_LIBRARY_PATH=/opt/xtandard/lib \
    LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 \
    MALLOC_ARENA_MAX=2 \
    FONTCONFIG_PATH=/opt/xtandard/etc/fonts \
    VIPS_WARNING=0

WORKDIR /tmp

# ==============================================================================
FROM builder-base AS builder

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base (builder)" \
      org.opencontainers.image.description="Go 1.27 + libvips 8.18 development image for xtandard-image" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}" \
      dev.xtandard.base.flavor="slim"

COPY --from=deps /opt/xtandard /opt/xtandard

# ==============================================================================
FROM builder-base AS builder-full

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base (full builder)" \
      org.opencontainers.image.description="Go 1.27 + libvips 8.18 + FFmpeg 8 (LGPL) + ONNX Runtime development image for xtandard-image" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}" \
      dev.xtandard.base.flavor="full"

COPY --from=full-deps /opt/xtandard /opt/xtandard
# ONNX Runtime (lib, symlinks, headers, pkg-config) in /usr/local, models in /usr/share.
COPY --from=full-deps /full-extra/builder/ /
RUN ldconfig

# ==============================================================================
FROM runtime-base AS runtime-full

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base" \
      org.opencontainers.image.description="libvips 8.18 + FFmpeg 8 (LGPL) + ONNX Runtime runtime libraries for xtandard-image (full)" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}" \
      dev.xtandard.base.flavor="full"

COPY --from=full-deps /runtime/opt/xtandard /opt/xtandard
# ONNX Runtime (lib + symlinks) in /usr/local/lib, models in /usr/share/xtandard/models.
COPY --from=full-deps /full-extra/runtime/ /
RUN ldconfig

USER 65532:65532

# ==============================================================================
# Last stage = default target: the slim runtime.
FROM runtime-base AS runtime

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base" \
      org.opencontainers.image.description="libvips 8.18 runtime libraries for xtandard-image (slim)" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}" \
      dev.xtandard.base.flavor="slim"

COPY --from=deps /runtime/opt/xtandard /opt/xtandard

USER 65532:65532
