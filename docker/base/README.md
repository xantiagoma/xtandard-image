# xtandard-image base image

`docker/base` builds libvips 8.18 and every codec it links into `/opt/xtandard` on
`ubuntu:noble`. It is a fork of
[imgproxy-docker-base](https://github.com/imgproxy/imgproxy-docker-base) (MIT, Copyright (c)
2017 Sergey Alexandrovich; kept in `LICENSE.imgproxy-docker-base`, credited in `/NOTICE`).
Decisions: [ADR 0005](../../docs/adr/0005-docker-image-strategy.md) (image strategy) and
[ADR 0006](../../docs/adr/0006-license-policy.md) (license policy); layout in
[ARCHITECTURE §14](../../docs/ARCHITECTURE.md#14-docker-and-base-image-build).

## Published images

| Tag | Stage | Use |
|---|---|---|
| `ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<N>-slim`, `:slim` | `runtime` | runtime libraries only; the app image copies `/opt/xtandard` from here or runs on it |
| `ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<N>-slim-builder`, `:slim-builder` | `builder` | complete Go + libvips development environment (CI, `task docker:test`, app build stage) |

`<vips>` is `VIPS_VERSION` and `<N>` is `XI_BASE_REVISION` from `versions.sh`. Both are
multi-arch (linux/amd64 + linux/arm64), each architecture built natively on its own GitHub
runner (`ubuntu-24.04`, `ubuntu-24.04-arm`) by `.github/workflows/base-image.yml` and
joined with `docker buildx imagetools create`. The workflow runs on changes under
`docker/base/**`, weekly (Ubuntu security updates) and on manual dispatch.

## Stages

```
base ──► deps-src   download-deps.sh: fetch every tarball, verify SHA256, unpack
   └───► deps       install-rust.sh + pinned meson/ninja/cmake, build-deps.sh → /opt/xtandard,
                    license bundle, license gate, runtime subset in /runtime
ubuntu:noble ──► builder   /opt/xtandard (lib, bin, include, lib/pkgconfig) + Go, golangci-lint, task
ubuntu:noble ──► runtime   /opt/xtandard/{lib,bin/vips,etc/fonts,share} + ca-certificates + tzdata
```

**builder** (runs as root, `WORKDIR /src`): Go `GOLANG_VERSION` at `/usr/local/go`,
golangci-lint `GOLANGCI_LINT_VERSION`, go-task `TASK_VERSION`, gcc/g++, git, pkg-config.
`PKG_CONFIG_PATH=/opt/xtandard/lib/pkgconfig`, `CGO_CFLAGS`, `CGO_LDFLAGS` (with an rpath to
`/opt/xtandard/lib`), `CGO_ENABLED=1`, `GOFLAGS=-buildvcs=false` and `GOTOOLCHAIN=local` are
set, so `go build` of a `#cgo pkg-config: vips` package needs no extra flags:

```sh
docker run --rm -v "$PWD":/src ghcr.io/xantiagoma/xtandard-image-base:slim-builder go test ./...
```

**runtime** (user `xtandard`, UID/GID 65532): `LD_LIBRARY_PATH=/opt/xtandard/lib`,
`LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2`, `MALLOC_ARENA_MAX=2`, `VIPS_WARNING=0`,
`FONTCONFIG_PATH=/opt/xtandard/etc/fonts`. It keeps the `vips` CLI for diagnostics; headers,
pkg-config files and the toolchains are not present.

## `/opt/xtandard`

| Path | Contents |
|---|---|
| `lib/` | libvips (no C++ binding, no modules), glib/gobject/gio, libffi, pcre2, zlib-ng, expat, libxml2 (minimal), libjpeg-turbo, libultrahdr, libpng, libspng, libwebp, cgif, quantizr, libtiff, OpenJPEG, libheif + libde265 + dav1d + aom + kvazaar, libjxl + highway + brotli, lcms2, libexif, librsvg + cairo + pixman + pango + harfbuzz + freetype + fontconfig + fribidi, jemalloc |
| `lib/pkgconfig/`, `include/` | builder only |
| `bin/vips` | libvips CLI |
| `etc/fonts/` | fontconfig configuration (fonts from `share/fonts`, cache in `/tmp/fontconfig`) |
| `share/fonts/dejavu/` | DejaVu Sans (Bitstream Vera license) for SVG text |
| `share/licenses/` | every dependency's license files + `SOURCES.md` (URL, version, SHA256) |
| `share/sbom/` | CycloneDX stubs per self-built library for syft/grype |

Not in the slim M1 image: LibRaw, PDFium, ImageMagick, OpenEXR (WP-66), FFmpeg and ONNX
Runtime (`full` flavor).

Compiler flags (`bashrc.sh`): `-Os` baseline (`-O3` for codecs), `-fstack-protector-strong`,
full RELRO, `-ffunction-sections -fdata-sections -Wl,--gc-sections`, rpath
`/opt/xtandard/lib`; Ubuntu's GCC adds `-D_FORTIFY_SOURCE=3`. Baseline ISA: x86-64 with SSE4,
arm64 `armv8.2-a+fp16` (Graviton2+, Ampere Altra, Apple silicon; not Cortex-A72 boards).
jemalloc uses 64 KiB pages on arm64 so it also runs on 64K-page kernels.

## License gate

`check-licenses.sh <lib-dir> [versions.sh]` fails when x265, x264, poppler, exiv2,
libimagequant, jbig-kit, Ghostscript or a GPL FFmpeg build appears in file names, binary
contents (DT_NEEDED strings, entry points, `--enable-gpl`), pkg-config requirements, or
`versions.sh`. It runs at the end of `build-deps.sh`, again on the runtime subset, in
`verify-image.sh`, and its self-test `test-check-licenses.sh` (clean tree passes, injected
GPL files/symbols/pins fail) runs in CI. `build-deps.sh` also switches every optional GPL
codec off explicitly (libheif `WITH_X265=0`/`WITH_X264=0`, libtiff `-Djbig=OFF`, vips
`-Dimagequant=disabled -Dpoppler=disabled`), so auto-detection can never pull one in.

## Bumping versions

1. Edit the `*_VERSION` line(s) in `versions.sh`. For a libvips bump keep 8.18.x until the
   project moves to 8.19 deliberately (ADR); check
   [libvips releases](https://github.com/libvips/libvips/releases).
2. Run `docker/base/update-checksums.sh` (works on macOS and Linux). It downloads every
   pinned tarball and toolchain for both architectures and rewrites the `*_SHA256` lines.
   Review the diff: only the bumped entries should change.
3. Bump `XI_BASE_REVISION` when the image changes without a libvips version change; reset it
   to `1` when `VIPS_VERSION` changes.
4. Build and verify locally (arm64 on Apple silicon, amd64 on x86 hosts; ~30 min cold):

   ```sh
   docker buildx build --platform linux/arm64 --build-arg VIPS_VERSION=8.18.6 --target builder --load -t xtandard-base:dev-builder docker/base
   docker buildx build --platform linux/arm64 --build-arg VIPS_VERSION=8.18.6 --load -t xtandard-base:dev docker/base
   docker/base/verify-image.sh xtandard-base:dev xtandard-base:dev-builder
   ```

5. Open a PR; the workflow builds and verifies both architectures and publishes on merge.

To see which upstream projects have newer releases, compare against imgproxy-docker-base's
`check-versions.sh` / `CHANGELOG.md` and sharp-libvips `versions.properties`, which track the
same libraries.

## Files

| File | Purpose |
|---|---|
| `Dockerfile` | multi-stage build (`builder`, `runtime`) |
| `versions.sh` | pinned versions + SHA256 of every tarball and toolchain |
| `download-deps.sh` | fetch + verify + unpack sources; `PRINT_CHECKSUMS=1` mode |
| `build-deps.sh` | build into `/opt/xtandard`, license bundle, SBOM stubs, license gate |
| `bashrc.sh` | per-architecture compiler flags (`BASH_ENV`) |
| `install-rust.sh` | pinned Rust + cargo-c (build time only: quantizr, librsvg) |
| `install-tools.sh` | Go, golangci-lint, go-task for the builder stage |
| `check-licenses.sh`, `test-check-licenses.sh` | license gate and its self-test |
| `update-checksums.sh` | recompute SHA256 lines after a bump |
| `verify-image.sh` | smoke test used by CI and locally |
