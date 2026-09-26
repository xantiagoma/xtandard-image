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
| `lib/` | libvips (no C++ binding), glib/gobject/gio, libffi, pcre2, zlib-ng, expat, libxml2 (minimal), libjpeg-turbo, libultrahdr, libpng, libspng, libwebp, cgif, quantizr, libtiff, OpenJPEG, libheif + libde265 + dav1d + aom + kvazaar, libjxl + highway + brotli, lcms2, libexif, librsvg + cairo + pixman + pango + harfbuzz + freetype + fontconfig + fribidi, jemalloc; base extras (WP-66): LibRaw (`libraw_r`), PDFium (prebuilt), OpenEXR + Imath, ImageMagick 7 MagickCore (PSD/PSB only) |
| `lib/vips-modules-8.18/vips-magick.so` | the only libvips module: magickload (load only, no magicksave). Every other loader is built into `libvips.so`; deleting this file removes ImageMagick from libvips |
| `lib/pkgconfig/`, `include/` | builder only |
| `bin/vips` | libvips CLI |
| `etc/fonts/` | fontconfig configuration (fonts from `share/fonts`, cache in `/tmp/fontconfig`) |
| `etc/ImageMagick-7/policy.xml` | `magick-policy.xml`: PSD/PSB read only, everything else denied (see below) |
| `share/fonts/dejavu/` | DejaVu Sans (Bitstream Vera license) for SVG text |
| `share/licenses/` | every dependency's license files + `SOURCES.md` (URL, version, SHA256) |
| `share/sbom/` | CycloneDX stubs per self-built library for syft/grype |

Not in the slim image: poppler (GPL; PDF goes through PDFium), x265 (GPL; HEIC/AVIF encode
uses kvazaar/aom), FFmpeg and ONNX Runtime (`full` flavor).

## Base extras (WP-66, revision 2)

| Component | Version | Licence | Source | libvips |
|---|---|---|---|---|
| LibRaw | 0.22.x | LGPL-2.1 (dual LGPL-2.1/CDDL-1.0; used under LGPL-2.1, dynamically linked) | [libraw.org](https://www.libraw.org/download) tarball; OpenMP off, JPEG/zlib/lcms2 on | `-Draw=enabled` → `dcrawload` |
| PDFium | chromium/`PDFIUM_VERSION` | BSD-3-Clause (bundled third-party notices in `share/licenses/pdfium/licenses/`); build scripts MIT | [bblanchon/pdfium-binaries](https://github.com/bblanchon/pdfium-binaries) `pdfium-linux-{x64,arm64}.tgz` (no V8, no XFA), one SHA256 per architecture; `build-deps.sh` writes `pdfium.pc` | `-Dpdfium=enabled` → `pdfload` (poppler stays disabled) |
| OpenEXR + Imath | 3.4.x / 3.2.x | BSD-3-Clause; vendored libdeflate (MIT) and OpenJPH (BSD-2-Clause) compiled in | GitHub release tags; no tools, examples or tests | `-Dopenexr=enabled` → `openexrload` |
| ImageMagick | 7.1.x | ImageMagick License (Apache-2.0 style) | GitHub release tag; MagickCore only, Q16 HDRI, coders built in (`--without-modules`), no utilities, no C++/Perl, no OpenMP/OpenCL, every delegate library off except zlib | `-Dmagick=enabled -Dmagick-package=MagickCore -Dmagick-module=enabled -Dmagick-features=load` → `magickload` |

Size (linux/arm64, `docker save | gzip -6`): runtime 54.8 MB compressed (r1: 48.3 MB,
+6.5 MB; budget ≤ 110 MB), builder 259.3 MB (r1: 251.9 MB). Uncompressed the extras are
PDFium 7.6 MiB, MagickCore 2.8 MiB, OpenEXR + Imath 3.1 MiB, LibRaw 1.2 MiB, so a
minimal-coder ImageMagick build was not needed.

`dcrawload`, `openexrload` and `magickload` carry libvips' `VIPS_OPERATION_UNTRUSTED` flag
(`pdfload` does not): an application that calls `vips_block_untrusted_set(TRUE)` must
unblock the loaders it wants by name (`vips_operation_block_set`).

### ImageMagick policy

ImageMagick is in the image for one purpose: PSD/PSB through libvips' `magickload`
(docs/FORMATS.md). `magick-policy.xml` is installed as
`/opt/xtandard/etc/ImageMagick-7/policy.xml`, the configure path compiled into MagickCore
(the app image also points `MAGICK_CONFIGURE_PATH` there). Rules are evaluated in order,
later ones override earlier ones:

- `module` domain: deny `*`, allow `PSD` (read | write, which ImageMagick requires to
  register a module at all). Denied modules are never registered, so their coders do not
  exist in the process: GIF, PNM, BMP, SVG/MVG/MSL, TEXT, URL, EPHEMERAL and every other
  built-in coder are unreachable.
- `coder` domain: deny `*`, allow `{PSD,PSB}` **read** only (no writing).
- `delegate`, `filter`: deny `*`; `path`: deny `@*` (indirect reads), `-`, `fd:`, `|` (pipes).
- Resources: thread 1, time 10 s (per image), memory 256 MiB, map 256 MiB, disk 0 (never
  spill the pixel cache to disk; fail instead), area 64 MP, width/height 16 KP,
  list-length 128, files 64, `max-memory-request` 256 MiB, anonymous memory maps.

The limits are ceilings: `MAGICK_*` environment variables and the API can only lower them.
ImageMagick also reads `policy.xml` from `MAGICK_CONFIGURE_PATH`, `$XDG_CONFIG_HOME` and
`$HOME/.config/ImageMagick` and merges them; the runtime user has no home directory
(`/nonexistent`) and the app runs with a read-only root filesystem, so nothing can add
rules. `verify-image.sh` checks the policy: a generated PSD loads through `magickload`
(and the generic loader), while JPEG, PNG, GIF, PPM, TIFF and WebP are refused by
`magickload` (GIF and PNM are compiled-in ImageMagick coders, so their refusal proves the
policy, not a missing delegate) and still load through their native libvips loaders.

Compiler flags (`bashrc.sh`): `-Os` baseline (`-O3` for codecs), `-fstack-protector-strong`,
full RELRO, `-ffunction-sections -fdata-sections -Wl,--gc-sections`, rpath
`/opt/xtandard/lib`; Ubuntu's GCC adds `-D_FORTIFY_SOURCE=3`. Baseline ISA: x86-64 with SSE4,
arm64 `armv8.2-a+fp16` (Graviton2+, Ampere Altra, Apple silicon; not Cortex-A72 boards).
jemalloc uses 64 KiB pages on arm64 so it also runs on 64K-page kernels.

## License gate

`check-licenses.sh <lib-dir> [versions.sh]` fails when x265, x264, poppler, exiv2,
libimagequant, jbig-kit, Ghostscript, a GPL FFmpeg build, or a GPL delegate ImageMagick or
LibRaw could pick up (FFTW, DjVuLibre, AutoTrace, LibRaw's GPL demosaic packs) appears in
file names, binary
contents (DT_NEEDED strings, entry points, `--enable-gpl`), pkg-config requirements, or
`versions.sh`. It runs at the end of `build-deps.sh`, again on the runtime subset, in
`verify-image.sh`, and its self-test `test-check-licenses.sh` (clean tree passes, injected
GPL files/symbols/pins fail) runs in CI. `build-deps.sh` also switches every optional GPL
codec off explicitly (libheif `WITH_X265=0`/`WITH_X264=0`, libtiff `-Djbig=OFF`, vips
`-Dimagequant=disabled -Dpoppler=disabled`, ImageMagick `--without-fftw --without-djvu
--without-autotrace --without-gslib`), so auto-detection can never pull one in.

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

   PDFium is a binary per architecture: `PDFIUM_VERSION` is the Chromium build number of a
   [bblanchon/pdfium-binaries release](https://github.com/bblanchon/pdfium-binaries/releases)
   and `update-checksums.sh` refreshes both `PDFIUM_SHA256_AMD64` and `PDFIUM_SHA256_ARM64`.

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
| `magick-policy.xml` | ImageMagick security policy (PSD/PSB read only) |
| `check-licenses.sh`, `test-check-licenses.sh` | license gate and its self-test |
| `update-checksums.sh` | recompute SHA256 lines after a bump |
| `verify-image.sh` | smoke test used by CI and locally |
