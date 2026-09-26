# ADR 0005: Docker image strategy

- Status: accepted
- Date: 2026-09-26

## Context

Users deploy on Dokploy, Coolify and Kubernetes and expect `docker run` with no build step. No existing base image fits: imgproxy's base lacks LibRaw, PDF, magick, OpenJPEG, libspng, libultrahdr and jemalloc; imagor's includes GPL x265 and poppler.

## Decision

- Own base image built from a fork of imgproxy-docker-base scripts (MIT, attributed), adding libspng, OpenJPEG, libultrahdr and jemalloc in M1, and LibRaw 0.22.x, PDFium prebuilt binaries, OpenEXR and restricted ImageMagick 7 (PSD only) in M2, all under `/opt/xtandard`. Pinned versions with checksums, weekly rebuilds, published as `ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<N>-<flavor>`.
- Two flavors and tags: `slim` (all still formats) and `full` (adds FFmpeg 8 LGPL and ONNX Runtime with permissive models).
- App image is multi-stage: build with the base image, run on `ubuntu:noble` with only the runtime libraries copied; `LD_PRELOAD` jemalloc; UID 65532; `HEALTHCHECK` via the binary's `health` subcommand; ports 8080 (HTTP) and 9090 (metrics).
- Multi-arch amd64 + arm64 on native runners, cosign signatures, SBOM, license bundle in `/opt/xtandard/share/licenses`.
- CI integration tests run against the exact published image, started with `docker compose` (go-standards §8).

## Consequences

- We own a C dependency build: security updates are our responsibility (Renovate on `versions.sh`, weekly rebuilds).
- glibc runtime (not distroless-static/musl) because PDFium and ONNX Runtime ship glibc binaries.
- `full` is materially larger (target ≤ 170 MB compressed vs ≤ 110 MB slim).
- WP-66 (base revision `r2`) added the M2 extras to slim: LibRaw 0.22.x (LGPL-2.1, https://www.libraw.org/), PDFium prebuilt from https://github.com/bblanchon/pdfium-binaries (BSD-3-Clause; poppler stays out), OpenEXR 3.4.x + Imath 3.2.x (BSD-3-Clause, https://openexr.com/), and ImageMagick 7 (ImageMagick License, https://imagemagick.org/license/) as MagickCore only, loaded by libvips through the single `vips-magick.so` module and restricted to reading PSD/PSB by `docker/base/magick-policy.xml`. Licence details in ADR 0006; sizes in `docker/base/README.md`.
