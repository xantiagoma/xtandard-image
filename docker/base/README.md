# xtandard-image base image

`docker/base` builds libvips 8.18 and every codec it links into `/opt/xtandard` on
`ubuntu:noble`, in two flavors: `slim` (every still format) and `full` (slim plus FFmpeg 8
under the LGPL, ONNX Runtime and the detection models; WP-70, see [full flavor](#full-flavor-wp-70-revision-3)). It is a fork of
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
| `ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<M>-full`, `:full` | `runtime-full` | slim runtime + FFmpeg libraries and CLIs, ONNX Runtime, models |
| `ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<M>-full-builder`, `:full-builder` | `builder-full` | slim builder + FFmpeg/libvpx headers and pkg-config files, ONNX Runtime headers, models |

`<vips>` is `VIPS_VERSION` and `<N>` is `XI_BASE_REVISION` from `versions.sh`; `<M>` is
`XI_BASE_FULL_REVISION` from `versions-full.sh` (currently `8.18.6-r2-slim` and
`8.18.6-r3-full`). GitHub Actions is disabled for now, so nothing is published yet: build
locally with the `task docker:base:*` tasks (below), which tag `xtandard-base:dev`,
`xtandard-base:dev-builder`, `xtandard-base:dev-full` and `xtandard-base:dev-full-builder`. Both are
multi-arch (linux/amd64 + linux/arm64), each architecture built natively on its own GitHub
runner (`ubuntu-24.04`, `ubuntu-24.04-arm`) by `.github/workflows/base-image.yml` and
joined with `docker buildx imagetools create`. The workflow runs on changes under
`docker/base/**`, weekly (Ubuntu security updates) and on manual dispatch.

## Stages

```
base ──► deps-src        download-deps.sh: fetch every tarball, verify SHA256, unpack
   ├───► deps            install-rust.sh + pinned meson/ninja/cmake, build-deps.sh → /opt/xtandard,
   │                     license bundle, license gate, split-runtime.sh → /runtime
   └───► full-deps-src   download-full-deps.sh: FFmpeg, libvpx, ONNX Runtime, models (SHA256)
deps ──► full-deps       build-full-deps.sh: libvpx + FFmpeg into /opt/xtandard, ONNX Runtime into
                         /usr/local, models into /usr/share/xtandard/models, license gate
                         (+ ffmpeg -buildconf, ldd, models), split-runtime.sh → /runtime
ubuntu:noble ──► builder-base ──► builder        /opt/xtandard from deps (lib, bin, include, lib/pkgconfig)
                              └─► builder-full   /opt/xtandard from full-deps + ONNX Runtime + models
ubuntu:noble ──► runtime-base ──► runtime        /opt/xtandard/{lib,bin/vips,etc/fonts,share} (default target)
                              └─► runtime-full   + FFmpeg libs, bin/ffmpeg, bin/ffprobe, ONNX Runtime, models
```

`builder-base` holds Go, golangci-lint, go-task and the compilers; `runtime-base` holds
`ca-certificates`, `tzdata`, the `xtandard` user and the environment. The full stages start
from the finished slim `deps` stage, so every slim layer is shared and the full-only pins live
in their own `versions-full.sh`: bumping FFmpeg never rebuilds (or changes) slim.

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

## Full flavor (WP-70, revision 3)

Everything in slim, plus:

| Component | Version | Licence | Where | Obtained |
|---|---|---|---|---|
| FFmpeg | 8.1.x (`FFMPEG_VERSION`) | LGPL-2.1-or-later (built with `--disable-gpl --disable-nonfree`, no `--enable-version3`) | `/opt/xtandard/lib/lib{avcodec,avformat,avutil,swscale,avfilter}.so.*`, `bin/ffmpeg`, `bin/ffprobe`; headers + `.pc` in the builder | source tarball from ffmpeg.org, built by `build-full-deps.sh` |
| libvpx | 1.17.x (`LIBVPX_VERSION`) | BSD-3-Clause (+ WebM patent grant) | `/opt/xtandard/lib/libvpx.so.*` | GitHub release tag, built |
| dav1d, libaom | as slim (`DAV1D_VERSION`, `AOM_VERSION`) | BSD-2-Clause (+ AOM patent licence) | already in slim | FFmpeg links the slim builds: dav1d decodes AV1, libaom encodes it |
| ONNX Runtime | 1.30.x (`ONNXRUNTIME_VERSION`) | MIT (bundled third-party notices in `share/licenses/onnxruntime/ThirdPartyNotices.txt`) | `/usr/local/lib/libonnxruntime.so.1*` (+ `ldconfig`); headers in `/usr/local/include/onnxruntime` and `libonnxruntime.pc` in the builder | official prebuilt `onnxruntime-linux-{x64,aarch64}-<v>.tgz` from GitHub releases, one SHA256 per architecture; CPU execution provider only (`libonnxruntime_providers_shared.so` is not shipped) |
| YuNet | `face_detection_yunet_2023mar.onnx` | MIT | `/usr/share/xtandard/models/` + `.LICENSE` | opencv_zoo at a pinned commit (`YUNET_COMMIT`), Git LFS media URL, SHA256-pinned model and licence |
| YOLOX-s | `yolox_s.onnx` (COCO, 80 classes) | Apache-2.0 | `/usr/share/xtandard/models/` + `.LICENSE` | Megvii YOLOX release `0.1.1rc0` asset, SHA256-pinned model and licence |

FFmpeg is configured with `--disable-everything --disable-autodetect` and then only what the
video WPs need (WP-74/75/76), video only:

- demuxers `mov` (mp4/mov/m4v), `matroska` (mkv/webm), `avi`, `gif`, `ivf`, `rawvideo`,
  `image2`, `image2pipe`; protocols `file`, `pipe` (no network);
- decoders `h264`, `hevc`, `vp8`, `vp9`, `libdav1d` (AV1), `gif`, `mpeg4`, `mjpeg`, `png`,
  `rawvideo`; no audio decoder;
- encoders `libvpx` (VP8), `libvpx-vp9`, `libaom-av1`, `png`, `rawvideo`, `wrapped_avframe`;
  no H.264/HEVC encoder of any kind (open questions 1 and 2 in `docs/ROADMAP.md`);
- muxers `mp4`, `mov`, `webm`, `matroska`, `ivf`, `null`, `rawvideo`, `image2`, `image2pipe`;
- filters `scale`, `format`, `fps`, `select`, `thumbnail`, `crop`, `pad`, `transpose`,
  `hflip`, `vflip`, `rotate`, `trim`, `setpts`, `settb`, `tile`, `palettegen`, `paletteuse`,
  `split`, `overlay`, `color`, `null` (and the ones the `ffmpeg` CLI selects);
- `--disable-avdevice --disable-swresample --disable-network --disable-doc`.

`ffmpeg -buildconf` is saved as `share/licenses/ffmpeg/BUILDCONF.txt`; FFmpeg's GPL/LGPLv3
licence texts are removed from the bundle because this build is LGPL-2.1+ only.

The models are laid out as `<name>.onnx` next to `<name>.LICENSE`, with `MODELS.md` (source,
licence) and `SHA256SUMS`; the licences and `MODELS.md` are also copied to
`share/licenses/models/`. Users can mount their own models over the directory (WP-71).

Size: runtime 103.1 MB compressed on linux/arm64 (slim + 48.4 MB; budget ≤ 170 MB), see
[Sizes](#sizes).

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

## Sizes

linux/arm64, `docker save | gzip -6` (compressed) and `docker image inspect` (uncompressed):

| Image | Compressed | Uncompressed | Budget (compressed) |
|---|---|---|---|
| slim runtime (`8.18.6-r2-slim`) | 54.7 MB | 165.1 MB | ≤ 110 MB |
| slim builder | 259.2 MB | 818.6 MB | — |
| full runtime (`8.18.6-r3-full`) | 103.1 MB | 237.3 MB | ≤ 170 MB |
| full builder | 308.2 MB | 893.3 MB | — |

The full additions are ONNX Runtime 24 MiB, the models 35 MiB (YOLOX-s 34 MiB, YuNet
0.2 MiB) and FFmpeg + libvpx 11 MiB (libraries and both CLIs), uncompressed.

## License gate

`check-licenses.sh [--ffmpeg <bin>] [--ldd] [--models <dir>] <lib-dir> [versions.sh ...]` fails when x265, x264, poppler, exiv2,
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

The full flavor adds three checks (WP-70), run by `build-full-deps.sh` and again inside the
`runtime-full` image by `verify-image.sh`:

- `--ffmpeg <bin>`: `ffmpeg -buildconf` must not contain `--enable-gpl`, `--enable-nonfree`
  or a GPL-only library switch (`libx264`, `libx265`, `libxvid`, `frei0r`, `librubberband`,
  `libvidstab`, ...), and `ffmpeg -L` must report the GNU Lesser General Public License.
- `--ldd`: every ELF file under `<lib-dir>` (and the ffmpeg binary) goes through `ldd`; a
  resolved forbidden library (x264, x265, libpostproc, xvid, ...) or a `not found`
  dependency fails. Run on `/opt/xtandard/lib` and `/usr/local/lib` (ONNX Runtime).
- `--models <dir>`: every `<name>.onnx` needs a non-empty `<name>.LICENSE` that is MIT,
  Apache-2.0, BSD or ISC and does not mention GPL/AGPL/SSPL/non-commercial terms, and
  `SHA256SUMS` must verify.

`test-check-licenses.sh` covers them with fake `ffmpeg` and `ldd` stand-ins.

## Bumping versions

1. Edit the `*_VERSION` line(s) in `versions.sh`. For a libvips bump keep 8.18.x until the
   project moves to 8.19 deliberately (ADR); check
   [libvips releases](https://github.com/libvips/libvips/releases).
2. Run `docker/base/update-checksums.sh` (works on macOS and Linux). It downloads every
   pinned tarball and toolchain for both architectures and rewrites the `*_SHA256` lines of
   `versions.sh` and `versions-full.sh`.
   Review the diff: only the bumped entries should change.
3. Bump `XI_BASE_REVISION` when the image changes without a libvips version change; reset it
   to `1` when `VIPS_VERSION` changes. `XI_BASE_FULL_REVISION` (`versions-full.sh`) is the
   full flavor's own counter: bump it whenever slim's revision moves or a full-only component
   changes, including when slim's revision moves (full r3 = slim r2 +
   WP-70).
4. Build and verify locally (host architecture: arm64 on Apple silicon, amd64 on x86 hosts;
   slim ~30 min cold, full adds ~10 min):

   ```sh
   task docker:base:slim docker:base:slim:check   # xtandard-base:dev, xtandard-base:dev-builder
   task docker:base:full docker:base:full:check   # xtandard-base:dev-full, xtandard-base:dev-full-builder
   ```

   The tasks run the `docker buildx build` commands below (full: `--target builder-full` and
   `--target runtime-full`) and `verify-image.sh --flavor slim|full`.

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
| `versions-full.sh` | full flavor pins: FFmpeg, libvpx, ONNX Runtime (per arch), models + licences |
| `download-deps.sh` | fetch + verify + unpack sources; `PRINT_CHECKSUMS=1` mode |
| `download-full-deps.sh` | same for the full flavor |
| `build-deps.sh` | build into `/opt/xtandard`, license bundle, SBOM stubs, license gate |
| `build-full-deps.sh` | full flavor: libvpx + FFmpeg, ONNX Runtime, models, licences, gate |
| `split-runtime.sh` | copy the runtime subset of `/opt/xtandard` to `/runtime` + gate |
| `bashrc.sh` | per-architecture compiler flags (`BASH_ENV`) |
| `install-rust.sh` | pinned Rust + cargo-c (build time only: quantizr, librsvg) |
| `install-tools.sh` | Go, golangci-lint, go-task for the builder stage |
| `magick-policy.xml` | ImageMagick security policy (PSD/PSB read only) |
| `check-licenses.sh`, `test-check-licenses.sh` | license gate and its self-test |
| `update-checksums.sh` | recompute SHA256 lines after a bump |
| `verify-image.sh` | smoke test used by CI and locally (`--flavor full` adds FFmpeg, ONNX Runtime, model checks) |
