# syntax=docker/dockerfile:1.7
#
# xtandard-image base image (flavor: slim).
#
# Forked from imgproxy-docker-base (MIT, Copyright (c) 2017 Sergey Alexandrovich);
# see LICENSE.imgproxy-docker-base and docker/base/README.md.
#
# Targets:
#   builder  complete Go + libvips development environment (published as *-slim-builder)
#   runtime  shared libraries only, non-root, jemalloc preloaded (published as *-slim; default)
#
# Build natively per architecture (no cross compilation):
#   docker buildx build --platform linux/arm64 --target builder -t xtandard-base:dev-builder docker/base
#   docker buildx build --platform linux/arm64 -t xtandard-base:dev docker/base

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
RUN mkdir -p /runtime/opt/xtandard \
  && cp -a /opt/xtandard/lib /opt/xtandard/bin /opt/xtandard/etc /opt/xtandard/share /runtime/opt/xtandard/ \
  && rm -rf /runtime/opt/xtandard/lib/pkgconfig /runtime/opt/xtandard/lib/cmake \
    /runtime/opt/xtandard/lib/glib-2.0 /runtime/opt/xtandard/share/pkgconfig \
    /runtime/opt/xtandard/share/aclocal /runtime/opt/xtandard/share/gettext \
    /runtime/opt/xtandard/share/glib-2.0 \
  && rm -f /runtime/opt/xtandard/lib/libraw.so* \
  && find /runtime/opt/xtandard/bin -type f ! -name vips ! -name vipsheader -delete \
  && ./check-licenses.sh /runtime/opt/xtandard/lib

# ==============================================================================
FROM ${UBUNTU_IMAGE} AS builder

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base (builder)" \
      org.opencontainers.image.description="Go 1.27 + libvips 8.18 development image for xtandard-image" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}"

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

COPY --from=deps /opt/xtandard /opt/xtandard

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
FROM ${UBUNTU_IMAGE} AS runtime

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base" \
      org.opencontainers.image.description="libvips 8.18 runtime libraries for xtandard-image (slim)" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}"

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
  && rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/log/apt/* /var/log/dpkg.log \
  && groupadd --system --gid 65532 xtandard \
  && useradd --system --uid 65532 --gid 65532 --no-create-home --home-dir /nonexistent \
    --shell /usr/sbin/nologin xtandard

COPY --from=deps /runtime/opt/xtandard /opt/xtandard

ENV PATH=/opt/xtandard/bin:$PATH \
    LD_LIBRARY_PATH=/opt/xtandard/lib \
    LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 \
    MALLOC_ARENA_MAX=2 \
    FONTCONFIG_PATH=/opt/xtandard/etc/fonts \
    VIPS_WARNING=0

USER 65532:65532
WORKDIR /tmp
