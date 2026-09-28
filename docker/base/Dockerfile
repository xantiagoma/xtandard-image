# syntax=docker/dockerfile:1.7
#
# xtandard-image base image (flavors: slim, full, gpl).
#
# Forked from imgproxy-docker-base (MIT, Copyright (c) 2017 Sergey Alexandrovich);
# see LICENSE.imgproxy-docker-base and docker/base/README.md.
#
# Targets:
#   builder       complete Go + libvips development environment (published as *-slim-builder)
#   runtime       shared libraries only, non-root, jemalloc preloaded (published as *-slim; default)
#   builder-full  builder + FFmpeg (LGPL) + ONNX Runtime headers/libs + models (*-full-builder)
#   runtime-full  runtime + FFmpeg (LGPL) libs/CLIs + ONNX Runtime + models (*-full)
#   builder-gpl   builder-full with FFmpeg rebuilt as GPL + x264 + x265 + gs, and LibRaw
#                 rebuilt with the Adobe DNG SDK and GoPro GPR SDK (*-gpl-builder)
#   runtime-gpl   runtime-full with the same gpl additions (*-gpl)
#
# The full stages reuse the slim ones: full-deps starts from the finished slim `deps`
# stage, so slim and full share every slim layer and the slim image does not change
# when a full-only component (versions-full.sh) moves. The gpl stages reuse full the
# same way (gpl-deps starts from full-deps; pins in versions-gpl.sh), so a gpl-only
# change never rebuilds or changes slim or full. slim and full contain no GPL code,
# and no Adobe DNG SDK (owner decision 2026-09-28: its licence ships in gpl only;
# slim and full never copy from libraw-deps-src or read its pins in versions-gpl.sh).
#
# Build natively per architecture (no cross compilation):
#   docker buildx build --platform linux/arm64 --target builder -t xtandard-base:dev-builder docker/base
#   docker buildx build --platform linux/arm64 -t xtandard-base:dev docker/base
#   docker buildx build --platform linux/arm64 --target builder-full -t xtandard-base:dev-full-builder docker/base
#   docker buildx build --platform linux/arm64 --target runtime-full -t xtandard-base:dev-full docker/base
#   docker buildx build --platform linux/arm64 --target builder-gpl -t xtandard-base:dev-gpl-builder docker/base
#   docker buildx build --platform linux/arm64 --target runtime-gpl -t xtandard-base:dev-gpl docker/base

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
# LibRaw's SDKs (WP-103): Adobe DNG SDK and GoPro GPR SDK, trimmed to what
# `build-libraw.sh --with-sdks` compiles. Used by the gpl flavor only (gpl-deps).
FROM base AS libraw-deps-src

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends unzip \
  && rm -rf /var/lib/apt/lists/*
COPY versions-gpl.sh download-libraw-deps.sh ./
RUN ./download-libraw-deps.sh

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

# jxrlib (WP-97) in its own layer: a change to its patches rebuilds only this step.
COPY build-jxrlib.sh ./
COPY patches/jxrlib ./patches/jxrlib
RUN ./build-jxrlib.sh

# The Basis Universal transcoder behind the app's C wrapper (WP-108, KTX2), in its own
# layer like jxrlib.
COPY build-basisu.sh ./
COPY basisu ./basisu
COPY patches/basisu ./patches/basisu
RUN ./build-basisu.sh

# LibRaw rebuilt with the X3F tools (WP-103), in its own layer. The DNG and GPR SDKs
# are added in gpl-deps only.
COPY build-libraw.sh ./
RUN ./build-libraw.sh

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
# gpl flavor (WP-70b): full + x264 and x265 + FFmpeg rebuilt with --enable-gpl.
# The whole FFmpeg in this flavor is GPL-2.0-or-later (ADR 0006, 2026-09-27).
FROM base AS gpl-deps-src

COPY versions-gpl.sh download-gpl-deps.sh ./
RUN ./download-gpl-deps.sh

# ==============================================================================
FROM full-deps AS gpl-deps

# LibRaw rebuilt once more with the X3F tools, the Adobe DNG SDK and the GoPro GPR SDK
# (WP-103; the DNG SDK licence ships in this flavor only, owner decision 2026-09-28),
# over the full flavor's LibRaw (same sources and SONAME). Before build-gpl-deps.sh so
# LICENSE-GPL.md lists dng-sdk and gpr.
COPY versions-gpl.sh ./
COPY patches/libraw ./patches/libraw
COPY patches/dng-sdk ./patches/dng-sdk
COPY patches/gpr ./patches/gpr
COPY --from=libraw-deps-src /root/libraw-deps /root/libraw-deps
RUN ./build-libraw.sh --with-sdks

COPY --from=gpl-deps-src /root/gpl-deps /root/gpl-deps

# WMF and EMF input (WP-108): libwmf, libemf2svg and the xi-metafile2svg program the
# server runs as a sandboxed subprocess. Before build-gpl-deps.sh, which lists them in
# LICENSE-GPL.md and runs the gpl licence gate.
COPY build-gpl-metafile.sh ./
COPY metafile ./metafile
COPY patches/libwmf ./patches/libwmf
RUN ./build-gpl-metafile.sh

COPY build-gpl-deps.sh ./
# A pristine copy of the full flavor's FFmpeg tarball (full-deps built in its own tree).
COPY --from=full-deps-src /root/full-deps/ffmpeg /root/gpl-deps/ffmpeg

RUN ./build-gpl-deps.sh

# Same split as full; the gate on the runtime subset runs in its gpl mode.
RUN XI_ALLOW_GPL=/root/versions-gpl.sh ./split-runtime.sh vips vipsheader ffmpeg ffprobe gs xi-metafile2svg

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

# jemalloc (unprefixed build, so it replaces malloc for every library) reads
# MALLOC_CONF; MALLOC_ARENA_MAX is glibc's knob and only matters if the preload is
# dropped. narenas:2 instead of jemalloc's default 4 arenas per CPU: the default
# more than doubled peak RSS under concurrent load with no throughput gain (WP-87,
# bench/RESULTS.md; WP-93 moved it here from the app image).
ENV PATH=/opt/xtandard/bin:$PATH \
    LD_LIBRARY_PATH=/opt/xtandard/lib \
    LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 \
    MALLOC_ARENA_MAX=2 \
    MALLOC_CONF=narenas:2 \
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
FROM builder-base AS builder-gpl

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base (gpl builder)" \
      org.opencontainers.image.description="Go 1.27 + libvips 8.18 + FFmpeg 8 (GPL, x264, x265) + ONNX Runtime + Ghostscript development image for xtandard-image; contains GPL and AGPL software, see /opt/xtandard/share/licenses/LICENSE-GPL.md" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later AND GPL-2.0-or-later AND MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}" \
      dev.xtandard.base.flavor="gpl"

COPY --from=gpl-deps /opt/xtandard /opt/xtandard
COPY --from=full-deps /full-extra/builder/ /
RUN ldconfig

# ==============================================================================
FROM runtime-base AS runtime-gpl

ARG VIPS_VERSION
LABEL org.opencontainers.image.title="xtandard-image-base" \
      org.opencontainers.image.description="libvips 8.18 + FFmpeg 8 (GPL, x264, x265) + ONNX Runtime runtime libraries and Ghostscript for xtandard-image (gpl); contains GPL and AGPL software, see /opt/xtandard/share/licenses/LICENSE-GPL.md" \
      org.opencontainers.image.source="https://github.com/xantiagoma/xtandard-image" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later AND GPL-2.0-or-later AND MIT" \
      dev.xtandard.vips.version="${VIPS_VERSION}" \
      dev.xtandard.base.flavor="gpl"

COPY --from=gpl-deps /runtime/opt/xtandard /opt/xtandard
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
