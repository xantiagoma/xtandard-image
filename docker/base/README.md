# xtandard-image base image

`docker/base` builds libvips 8.18 and every codec it links into `/opt/xtandard` on
`ubuntu:noble`, in three flavors: `slim` (every still format), `full` (slim plus FFmpeg 8
under the LGPL, ONNX Runtime and the detection models; WP-70, see [full flavor](#full-flavor-wp-70-revision-6))
and `gpl` (full with FFmpeg rebuilt under the GPL to add the x264 H.264 and x265 HEVC
encoders, plus the Ghostscript `gs` program for EPS input, AGPL, used only with
`XI_FORMATS_EPS_ENABLED=true`; WP-70b and WP-99, see [gpl flavor](#gpl-flavor-wp-70b-revision-7)). slim and full contain no GPL code; gpl does,
and is labelled and documented as such. It is a fork of
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
| `ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<G>-gpl`, `:gpl` | `runtime-gpl` | full runtime with FFmpeg rebuilt as GPL + libx264 + libx265, `bin/gs` (Ghostscript), and LibRaw with the Adobe DNG SDK and GoPro GPR SDK (**AGPL-3.0-or-later AND GPL-2.0-or-later AND MIT**; the DNG SDK under its own licence) |
| `ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<G>-gpl-builder`, `:gpl-builder` | `builder-gpl` | full builder with the same GPL FFmpeg, x264/x265 headers and pkg-config files |

`<vips>` is `VIPS_VERSION` and `<N>` is `XI_BASE_REVISION` from `versions.sh`; `<M>` is
`XI_BASE_FULL_REVISION` from `versions-full.sh`; `<G>` is `XI_BASE_GPL_REVISION` from
`versions-gpl.sh` (currently `8.18.6-r7-slim`, `8.18.6-r8-full` and `8.18.6-r9-gpl`). GitHub Actions is disabled for now, so nothing is published yet: build
locally with the `task docker:base:*` tasks (below), which tag `xtandard-base:dev`,
`xtandard-base:dev-builder`, `xtandard-base:dev-full`, `xtandard-base:dev-full-builder`,
`xtandard-base:dev-gpl` and `xtandard-base:dev-gpl-builder`. All are
multi-arch (linux/amd64 + linux/arm64), each architecture built natively on its own GitHub
runner (`ubuntu-24.04`, `ubuntu-24.04-arm`) by `.github/workflows/base-image.yml` and
joined with `docker buildx imagetools create`. The workflow runs on changes under
`docker/base/**`, weekly (Ubuntu security updates) and on manual dispatch.

## Stages

```
base ──► deps-src        download-deps.sh: fetch every tarball, verify SHA256, unpack
   ├───► libraw-deps-src download-libraw-deps.sh: Adobe DNG SDK and GoPro GPR SDK (SHA256), trimmed
   │                     (pins in versions-gpl.sh; only gpl-deps copies from it)
   ├───► deps            install-rust.sh + pinned meson/ninja/cmake, build-deps.sh → /opt/xtandard,
   │                     license bundle, license gate; build-jxrlib.sh (own layer, patches/jxrlib);
   │                     build-basisu.sh (own layer: Basis Universal transcoder, patches/basisu);
   │                     build-libraw.sh (own layer: LibRaw again with the X3F tools);
   │                     split-runtime.sh → /runtime
   ├───► full-deps-src   download-full-deps.sh: FFmpeg, libvpx, ONNX Runtime, models (SHA256)
   └───► gpl-deps-src    download-gpl-deps.sh: x264, x265, Ghostscript, libwmf, libemf2svg (SHA256)
deps ──► full-deps       build-full-deps.sh: libvpx + FFmpeg into /opt/xtandard, ONNX Runtime into
                         /usr/local, models into /usr/share/xtandard/models, license gate
                         (+ ffmpeg -buildconf, ldd, models), split-runtime.sh → /runtime
full-deps ──► gpl-deps   build-libraw.sh --with-sdks (LibRaw again with X3F, DNG SDK, GPR SDK;
                         patches/{libraw,dng-sdk,gpr}, gate in gpl mode);
                         build-gpl-metafile.sh: libwmf (patches/libwmf) + libemf2svg +
                         xi-metafile2svg (WMF/EMF to SVG);
                         build-gpl-deps.sh: x264 + x265, FFmpeg rebuilt with full's configuration
                         + --enable-gpl --enable-libx264 --enable-libx265, gs (static, 3 devices),
                         LICENSE-GPL.md, gate in
                         its gpl mode (--allow-gpl versions-gpl.sh), split-runtime.sh → /runtime
ubuntu:noble ──► builder-base ──► builder        /opt/xtandard from deps (lib, bin, include, lib/pkgconfig)
                              ├─► builder-full   /opt/xtandard from full-deps + ONNX Runtime + models
                              └─► builder-gpl    /opt/xtandard from gpl-deps + ONNX Runtime + models
ubuntu:noble ──► runtime-base ──► runtime        /opt/xtandard/{lib,bin/vips,etc/fonts,share} (default target)
                              ├─► runtime-full   + FFmpeg libs, bin/ffmpeg, bin/ffprobe, ONNX Runtime, models
                              └─► runtime-gpl    same as runtime-full with the GPL FFmpeg + libx264 + libx265,
                                                 gs, xi-metafile2svg, and libraw_r with the DNG and GPR SDKs
```

`builder-base` holds Go, golangci-lint, go-task and the compilers; `runtime-base` holds
`ca-certificates`, `tzdata`, the `xtandard` user and the environment. The full stages start
from the finished slim `deps` stage, so every slim layer is shared and the full-only pins live
in their own `versions-full.sh`: bumping FFmpeg never rebuilds (or changes) slim. The gpl
stages start from `full-deps` in the same way, with their pins in `versions-gpl.sh`: a gpl-only
change never rebuilds or changes slim or full. The Adobe DNG SDK and the GoPro GPR SDK are
gpl-only (owner decision 2026-09-28): slim and full never copy the `libraw-deps-src` stage,
never read their pins (`versions-gpl.sh`) and get only `patches/jxrlib` in their build context,
so a change to the SDKs or their patches never rebuilds slim or full either.

**RAW features per flavor** (WP-103):

| | slim | full | gpl |
|---|---|---|---|
| LibRaw snapshot (`LIBRAW_COMMIT`), every camera it lists (Sony A7 V, …) | yes | yes | yes |
| Sigma X3F (Foveon) through the X3F tools (BSD-3-Clause) | yes | yes | yes |
| DNG 1.7 with JPEG XL raw data (Adobe DNG SDK) | embedded preview, else 422 | embedded preview, else 422 | demosaic |
| GoPro GPR, VC-5 (GoPro GPR SDK, through the DNG SDK) | embedded preview, else 422 | embedded preview, else 422 | demosaic |
| `libraw_capabilities()` | `X3FTOOLS` | `X3FTOOLS` | `X3FTOOLS \| DNGSDK \| GPRSDK` |

The 422 reads `camera RAW not supported (<make> <model>: JPEG XL or GoPro VC-5 raw data needs
the gpl image) and the file has no usable embedded preview`; the app reads the build through
`vips.RawLibraryInfo()`, and the shim never demosaics a file LibRaw only has a placeholder
decoder for.

**builder** (runs as root, `WORKDIR /src`): Go `GOLANG_VERSION` at `/usr/local/go`,
golangci-lint `GOLANGCI_LINT_VERSION`, go-task `TASK_VERSION`, gcc/g++, git, pkg-config.
`PKG_CONFIG_PATH=/opt/xtandard/lib/pkgconfig`, `CGO_CFLAGS`, `CGO_LDFLAGS` (with an rpath to
`/opt/xtandard/lib`), `CGO_ENABLED=1`, `GOFLAGS=-buildvcs=false` and `GOTOOLCHAIN=local` are
set, so `go build` of a `#cgo pkg-config: vips` package needs no extra flags:

```sh
docker run --rm -v "$PWD":/src ghcr.io/xantiagoma/xtandard-image-base:slim-builder go test ./...
```

**runtime** (user `xtandard`, UID/GID 65532): `LD_LIBRARY_PATH=/opt/xtandard/lib`,
`LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2`, `MALLOC_ARENA_MAX=2`, `MALLOC_CONF=narenas:2`,
`VIPS_WARNING=0`, `FONTCONFIG_PATH=/opt/xtandard/etc/fonts`. It keeps the `vips` CLI for
diagnostics; headers, pkg-config files and the toolchains are not present. The full and gpl
runtimes inherit the same environment.

jemalloc is not linked into libvips or any other library; it is preloaded, and because it is
built without a symbol prefix it replaces `malloc`/`free` for the whole process (Go's cgo
allocations and every C library). It therefore reads `MALLOC_CONF`, not glibc's
`MALLOC_ARENA_MAX` (kept only for the case where someone clears `LD_PRELOAD`).
`MALLOC_CONF=narenas:2` (since r3, WP-93) caps jemalloc at 2 arenas instead of 4 per CPU (32 on
an 8-CPU host): the default kept the freed pages of every busy thread and more than doubled peak
RSS under concurrent load with no throughput gain (WP-87, `bench/RESULTS.md`). Override it per
deployment, e.g. `MALLOC_CONF=narenas:4,background_thread:true`. `verify-image.sh` checks that
jemalloc actually honours it (`opt.narenas: 2` in its stats).

## `/opt/xtandard`

| Path | Contents |
|---|---|
| `lib/` | libvips (no C++ binding), glib/gobject/gio, libffi, pcre2, zlib-ng, expat, libxml2 (minimal), libjpeg-turbo, libultrahdr, libpng, libspng, libwebp, cgif, quantizr, libtiff + zstd (WP-102), OpenJPEG, libheif + libde265 + dav1d + aom + kvazaar, libjxl + highway + brotli, lcms2, libexif, librsvg + cairo + pixman + pango + harfbuzz + freetype + fontconfig + fribidi, jemalloc; base extras (WP-66): LibRaw (`libraw_r`), PDFium (prebuilt), OpenEXR + Imath, ImageMagick 7 MagickCore (PSD/PSB and the WP-97 legacy coders); jxrlib (`libjpegxr`, `libjxrglue`, WP-97; headers in `include/jxrlib` and `libjxr.pc` in the builder); the Basis Universal transcoder behind the app's wrapper (`libxibasisu`, WP-108; `xibasisu.h` and `xibasisu.pc` in the builder) |
| `lib/vips-modules-8.18/vips-magick.so` | the only libvips module: magickload (load only, no magicksave). Every other loader is built into `libvips.so`; deleting this file removes ImageMagick from libvips |
| `lib/pkgconfig/`, `include/` | builder only |
| `bin/vips` | libvips CLI |
| `etc/fonts/` | fontconfig configuration (fonts from `share/fonts`, cache in `/tmp/fontconfig`) |
| `etc/ImageMagick-7/policy.xml` | `magick-policy.xml`: PSD/PSB, TGA, PCX, DDS, QOI, SGI, SUN, XBM, XPM, PAM and XCF read only, everything else denied (see below) |
| `share/fonts/dejavu/` | DejaVu Sans (Bitstream Vera license) for SVG text |
| `share/licenses/` | every dependency's license files + `SOURCES.md` (URL, version, SHA256) |
| `share/sbom/` | CycloneDX stubs per self-built library for syft/grype |

Not in the slim image: poppler (GPL; PDF goes through PDFium), x265 (GPL; HEIC/AVIF encode
uses kvazaar/aom), FFmpeg and ONNX Runtime (`full` flavor).

## Full flavor (WP-70, revision 8)

Everything in slim, plus:

| Component | Version | Licence | Where | Obtained |
|---|---|---|---|---|
| FFmpeg | 8.1.x (`FFMPEG_VERSION`) | LGPL-2.1-or-later (built with `--disable-gpl --disable-nonfree`, no `--enable-version3`) | `/opt/xtandard/lib/lib{avcodec,avformat,avutil,swscale,avfilter}.so.*`, `bin/ffmpeg`, `bin/ffprobe`; headers + `.pc` in the builder | source tarball from ffmpeg.org, built by `build-full-deps.sh` |
| libvpx | 1.17.x (`LIBVPX_VERSION`) | BSD-3-Clause (+ WebM patent grant) | `/opt/xtandard/lib/libvpx.so.*` | GitHub release tag, built |
| dav1d, libaom | as slim (`DAV1D_VERSION`, `AOM_VERSION`) | BSD-2-Clause (+ AOM patent licence) | already in slim | FFmpeg links the slim builds: dav1d decodes AV1, libaom encodes it |
| ONNX Runtime | 1.30.x (`ONNXRUNTIME_VERSION`) | MIT (bundled third-party notices in `share/licenses/onnxruntime/ThirdPartyNotices.txt`) | `/usr/local/lib/libonnxruntime.so.1*` (+ `ldconfig`); headers in `/usr/local/include/onnxruntime` and `libonnxruntime.pc` in the builder | official prebuilt `onnxruntime-linux-{x64,aarch64}-<v>.tgz` from GitHub releases, one SHA256 per architecture; CPU execution provider only (`libonnxruntime_providers_shared.so` is not shipped) |
| YuNet | `face_detection_yunet_2023mar.onnx` | MIT | `/usr/share/xtandard/models/` + `.LICENSE` | opencv_zoo at a pinned commit (`YUNET_COMMIT`), Git LFS media URL, SHA256-pinned model and licence |
| YOLOX-s | `yolox_s.onnx` (COCO, 80 classes) | Apache-2.0 | `/usr/share/xtandard/models/` + `.LICENSE` | Megvii YOLOX release `0.1.1rc0` asset, SHA256-pinned model and licence |
| MobileNetV3-Large | `mobilenetv3_large_100.onnx` (ImageNet-1k classifier, 1000 classes; timm `mobilenetv3_large_100`, 75.8% top-1; WP-110) | Apache-2.0 | `/usr/share/xtandard/models/` + `.LICENSE` | ONNX Model Zoo (`onnx/models`) at a pinned commit (`CLASSIFIER_COMMIT`), Git LFS media URL, SHA256-pinned model and licence (the zoo's LICENSE) |

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

Size: runtime 124.6 MB compressed on linux/arm64 (r8; slim + 68.6 MB; budget ≤ 170 MB), see
[Sizes](#sizes).

## gpl flavor (WP-70b, revision 9)

> **This image contains GPL and AGPL software.** Its FFmpeg libraries and CLIs are
> GPL-2.0-or-later, H.264 and HEVC are patent-encumbered, and `bin/gs` is Ghostscript,
> AGPL-3.0-or-later (a separate program the server runs only with
> `XI_FORMATS_EPS_ENABLED=true`; [ADR 0011](../../docs/adr/0011-ghostscript-subprocess.md)). If you use or redistribute
> it, reviewing those licences and patents is your responsibility. The xtandard-image code
> stays MIT, and `slim` and `full` stay free of GPL code
> ([ADR 0006](../../docs/adr/0006-license-policy.md), amendment 2026-09-27).

Everything in full, plus:

| Component | Version | Licence | Where | Obtained |
|---|---|---|---|---|
| x264 | `stable` branch head, build 165 (`X264_COMMIT`) | GPL-2.0-or-later | `/opt/xtandard/lib/libx264.so.165`; `x264.h` + `x264.pc` in the builder | GitLab archive of the pinned commit (x264 publishes no release tarballs), SHA256-pinned; library only (`--disable-cli`, no lavf/ffms/gpac/lsmash/swscale/OpenCL), 8- and 10-bit (`--bit-depth=all`) |
| x265 | 4.2 (`X265_VERSION`) | GPL-2.0-or-later | `/opt/xtandard/lib/libx265.so.*`; headers + `x265.pc` in the builder | release tarball from download.videolan.org; library only (`ENABLE_CLI=OFF`), 8-bit Main, no libnuma, no HDR10+ |
| FFmpeg | as full (`FFMPEG_VERSION`) | **GPL-2.0-or-later** as built | same files as full, rebuilt | the full flavor's tarball, configured with **exactly the full configuration** (read back from `share/licenses/ffmpeg/BUILDCONF.txt`) with `--disable-gpl` swapped for `--enable-gpl`, plus `--enable-libx264 --enable-libx265 --enable-encoder=libx264,libx265` |
| Ghostscript (WP-99) | 10.08.0 (`GHOSTSCRIPT_VERSION`) | **AGPL-3.0-or-later** (compiles in FreeType FTL, IJG libjpeg, libpng, zlib, lcms2mt MIT, brotli MIT, Droid Sans Fallback Apache-2.0, Adobe CMaps BSD-3-Clause) | `/opt/xtandard/bin/gs` only (no `libgs`, no headers) | release tarball from ghostpdl-downloads (SHA256; checked against its SHA512SUMS), unmodified; `gs` alone, statically linked against its own libraries (libc/libm only), resources and fonts compiled in (`%rom%`); no X11, CUPS, dbus, GTK, fontconfig, libpaper, IJS, threads, contrib drivers, PDF interpreter, JBIG2/JPX, TIFF, OCR or extract; devices `pngalpha`, `png16m`, `bbox` (+ `nullpage`) |
| libwmf (WP-108) | 0.2.16 (`LIBWMF_VERSION`) | LGPL-2.0-or-later; its 13 URW Type 1 fonts GPL | `lib/libwmf*.so*`, `share/libwmf/fonts` (only `xi-metafile2svg` uses them) | GitHub archive of the release tag (SHA256); `patches/libwmf` (SVG device: SVG/xlink namespaces, no DOCTYPE, viewBox, exact colours, text escaped); no X11, no plotutils, no system or Ghostscript fontmap |
| libemf2svg (WP-108) | 1.8.1 (`LIBEMF2SVG_VERSION`) | **GPL-2.0-only** (with its vendored libUEMF) | `lib/libemf2svg.so*` | GitHub archive of the release tag (SHA256), unmodified; library only (`LONLY`) |
| xi-metafile2svg (WP-108) | the app's (`docker/base/metafile`) | MIT source; GPL-2.0-only as built | `bin/xi-metafile2svg` | `metafile/xi-metafile2svg.c`: `wmf\|emf <max-bytes> <max-side>`, metafile on stdin, SVG on stdout; smoke conversions at build time |

So the gpl image has full's demuxers, decoders, filters and VP8/VP9/AV1 encoders and adds
exactly two encoders: `libx264` (H.264) and `libx265` (HEVC), and one program, `gs`, which
the server runs as a sandboxed subprocess for EPS/PostScript input when
`XI_FORMATS_EPS_ENABLED=true` (off by default; `internal/ghostscript`, ADR 0011), and one more,
`xi-metafile2svg`, which the server runs in the same sandbox to turn WMF and EMF sources into
SVG (`internal/metafile`, `XI_FORMATS_METAFILE_ENABLED`, on when found). Still no audio, network,
nonfree code or `--enable-version3`. HEIC encoding stays on kvazaar (libheif is not
rebuilt against x265), and poppler, exiv2 and GPL `dssim` stay out: they were replaced
(PDFium, own metadata code, own SSIM), not just excluded.

Licences: `share/licenses/x264/COPYING`, `share/licenses/x265/COPYING`,
`share/licenses/ffmpeg/COPYING.GPLv2` (next to the LGPL text), the regenerated
`ffmpeg/BUILDCONF.txt`, `share/licenses/ghostscript/` (its licence statement, the AGPL text
`COPYING.AGPLv3`, the notices of what it compiles in, `BUILD.txt` with `gs -h`), a
`## gpl flavor` section in `SOURCES.md` (with the Ghostscript tarball), and
**`share/licenses/LICENSE-GPL.md`**: the statement that the combined FFmpeg is
GPL-2.0-or-later, that `gs` is AGPL-3.0-or-later and what that means for distributing or
serving it, the patent caveat, where the corresponding source is, and a table of every
component in the bundle with its SPDX licence (from `XI_COMPONENT_LICENSES` in
`versions-gpl.sh`). Image labels: `org.opencontainers.image.licenses="AGPL-3.0-or-later AND GPL-2.0-or-later AND MIT"`,
`dev.xtandard.base.flavor="gpl"` (slim and full keep `MIT`).

`verify-image.sh --flavor gpl` runs every full check with the gate in its gpl mode, requires
`libx264` and `libx265` in `ffmpeg -encoders` and `avcodec_license()` = "GPL version 2 or
later", and encodes a short H.264 MP4 (High) and an HEVC MP4 (Main, `hvc1`) and decodes both
back with ffprobe/ffmpeg, plus GIF → H.264 MP4. For Ghostscript it runs the gate's
`--ghostscript` check, rasterises a test EPS with the server's flags (stdin to a 200×100
RGBA PNG at 144 DPI, pixel-checked with libvips) as the non-root image user, and checks
that `-dSAFER` refuses a file read and `%pipe%`; slim and full must have no `gs`. It converts
the WMF and EMF fixtures with `xi-metafile2svg` (namespaced SVG with the exact colours, rendered
by libvips) and checks the libwmf and libemf2svg licences; slim and full must have neither the
program nor the libraries.

Size: runtime 140.5 MB compressed on linux/arm64 (r9; full + 15.9 MB: x264/x265 2.2 MB, Ghostscript
11.1 MB, LibRaw's DNG and GPR SDKs about 0.4 MB, the WP-108 metafile converter 2.2 MB), see
[Sizes](#sizes).

## Base extras (WP-66, revision 2)

| Component | Version | Licence | Source | libvips |
|---|---|---|---|---|
| LibRaw | 0.22 snapshot (`LIBRAW_COMMIT`, WP-103) | LGPL-2.1 (dual LGPL-2.1/CDDL-1.0; used under LGPL-2.1, dynamically linked) | GitHub archive of the pinned master commit; OpenMP off, JPEG/zlib/lcms2 on; rebuilt with the X3F tools, and in gpl also the DNG and GPR SDKs (see [LibRaw options](#libraw-snapshot-and-options-wp-103)) | `-Draw=enabled` → `dcrawload` |
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

ImageMagick is in the image for PSD/PSB through libvips' `magickload` and, since WP-97
(revision r4), for a short list of legacy formats libvips cannot read: TGA, PCX, DDS, QOI,
SGI, Sun raster, XBM, XPM and PAM (docs/FORMATS.md §1.6), and since WP-107 for GIMP XCF
(one layer per call, flattened by the app; FORMATS.md §1.7), and since WP-108 for Macintosh PICT
and Kodak Photo CD (FORMATS.md §1.8; the app names the coder, `PICT:`/`PCD:` on a memfd path,
because ImageMagick's own sniffing takes PICT's zero header for TGA). Its coders are compiled in anyway,
so this costs no image size. `magick-policy.xml` is installed as
`/opt/xtandard/etc/ImageMagick-7/policy.xml`, the configure path compiled into MagickCore
(the app image also points `MAGICK_CONFIGURE_PATH` there). Rules are evaluated in order,
later ones override earlier ones:

- `module` domain: deny `*`, allow `{PSD,TGA,PCX,DDS,QOI,SGI,SUN,XBM,XPM,PNM,XCF,PICT,PCD}` (read |
  write, which ImageMagick requires to register a module at all). Denied modules are never
  registered, so their coders do not exist in the process: GIF, BMP, PS/EPS, SVG/MVG/MSL,
  TEXT, URL, EPHEMERAL and every other built-in coder are unreachable.
- `coder` domain: deny `*`, allow `{PSD,PSB,TGA,PCX,DDS,QOI,SGI,SUN,XBM,XPM,PAM,XCF,PICT,PCD}` **read**
  only (no writing). The aliases the allowed modules also register (ICB, VDA, VST, DCX,
  DXT1, DXT5, RAS, PICON, PM, PCT, PCDS, and PBM/PGM/PPM/PNM/PFM/PHM of the PNM module, which libvips'
  `ppmload` reads instead) stay denied.
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
policy, not a missing delegate) and still load through their native libvips loaders; every
legacy fixture in `testdata/fixtures` (TGA, PCX, DDS DXT1/DXT5, QOI, SGI, SUN, XBM, XPM, PAM)
decodes through `magickload` with its own coder, `xcf/alpha.xcf` through the XCF coder,
`pict/photo.pct` through the `PICT:` coder, and
the ASCII/bitmap Netpbm fixtures through `ppmload`.

### jxrlib (WP-97, revision r4)

| Component | Version | Licence | Source | Used by |
|---|---|---|---|---|
| jxrlib | `JXRLIB_VERSION` (4creators commit `JXRLIB_COMMIT`, the one Debian and Homebrew package as 1.2~git20170615.f752187) | BSD-2-Clause | GitHub archive of the pinned commit (no release tarballs) | the app's own libvips loader (`internal/vips/jxr.c`); libvips does not link it |

`build-jxrlib.sh` runs right after `build-deps.sh` in its own layer (a patch change rebuilds
only that step and what follows). It applies `patches/jxrlib/*.patch` (Debian's fixes and
ours, listed in `patches/jxrlib/README.md`: header validation, over-reads, leaks and prints
found by fuzzing the loader with libFuzzer + ASan/LSan) and compiles the codec and the glue
API with `-DNDEBUG` (jxrlib's asserts would abort the process on a hostile file) into
`libjpegxr.so.0` and `libjxrglue.so.0` (Debian's SONAMEs, 0.4 MB stripped), with Debian's
`libjxr.pc` layout. The sample apps (`JxrEncApp`, `JxrDecApp`) and the test library are not
built; ImageMagick's JXR coder, which runs `JxrDecApp` as a delegate, stays denied.

### Basis Universal transcoder (WP-108, revision r7)

| Component | Version | Licence | Source | Used by |
|---|---|---|---|---|
| basis_universal (transcoder only) | 2.50 (`BASISU_VERSION`, commit `BASISU_COMMIT`) | Apache-2.0 (its `NOTICE` and `LICENSES/` in `share/licenses/basisu`) | GitHub archive of the pinned commit | the app's KTX2 loader (`internal/vips/ktx2.c` through `libxibasisu`); libvips does not link it |

`build-basisu.sh` runs after `build-jxrlib.sh` in its own layer. It compiles
`transcoder/basisu_transcoder.cpp` alone (no encoder, tools or OpenCL; KTX2 and Zstandard
supercompression on, linked against the base's libzstd rather than the copy in the archive)
with the app's C wrapper `basisu/xibasisu.{h,cpp}` (MIT) into `libxibasisu.so.2` (1.1 MB
stripped), exporting only `xi_basisu_version`, `xi_basisu_ktx2_info` and
`xi_basisu_ktx2_decode` (checked at build time); every exception is caught in the wrapper.
`patches/basisu` makes the containers' `container_abort()` (a failed allocation or an
out-of-range index) throw `std::bad_alloc` instead of printing and calling
`std::terminate()`. The builder gets `xibasisu.h` and `xibasisu.pc`.

### LibRaw snapshot and options (WP-103)

LibRaw is pinned to a **snapshot of its master branch** rather than the last release, so bodies
that arrive between releases decode (WP-103: one snapshot per base release; bump
`LIBRAW_COMMIT`, then diff `src/tables/cameralist.cpp` against the previous pin and record it
here). It is fetched as the GitHub archive of the commit, so `build-deps.sh` runs `autoreconf`.
The snapshot's `libraw_version.h` still says 0.22.0; its SONAME is `libraw_r.so.26`.

| Component | Version | Licence | Source | What it decodes |
|---|---|---|---|---|
| LibRaw | `LIBRAW_VERSION` = 0.22-snapshot-20260925, commit `0694b5ac18` (2026-09-25) | LGPL-2.1 (or CDDL-1.0) | GitHub archive | every camera RAW (1287 cameras with all options) |
| X3F tools | inside LibRaw (`src/x3f`), `-DUSE_X3FTOOLS` | BSD-3-Clause (Roland Karlsson; notice extracted to `share/licenses/libraw/LICENSE.x3f-tools`) | LibRaw's tree | Sigma X3F (Foveon, SD/DP bodies) |
| Adobe DNG SDK (**gpl only**) | `DNGSDK_VERSION` (`versions-gpl.sh`) = 1.7.1 build 2724 (2026-09-08), `-DUSE_DNGSDK` | Adobe DNG SDK License Agreement (permissive, with an indemnity clause for commercial redistribution and no endorsement; `LicenseRef-Adobe-DNG-SDK`, ADR 0006 amendment WP-103) | `download.adobe.com/pub/adobe/dng/<DNGSDK_ARCHIVE>` (only `dng_sdk/source` and the licence are kept) | DNG 1.7 JPEG XL raw data (Lightroom "JPEG XL" DNG, iPhone ProRAW JPEG-XL), through the base's libjxl 0.12 and libjpeg-turbo |
| GoPro GPR SDK (**gpl only**) | `GPR_VERSION` (`versions-gpl.sh`) = v1.1 + master `446c736` (2026-05-05), `-DUSE_GPRSDK` | Apache-2.0 OR MIT | GitHub archive of the commit (only the VC-5 decoder, `common` and the DNG reader glue are compiled) | GoPro `.GPR` (VC-5 compressed DNG; Fusion, HERO5 and later) |

Camera-list diff, LibRaw 0.22.2 (2026-07-16) → snapshot `0694b5ac18`: **+4** (1283 → 1287 with all
options): Canon EOS R50 V, Hasselblad X2D II 100C, Sony ILCE-1M2 (A1 II), Sony ILCE-7M5 (A7 V,
new ARW 6 decoder `sony_arw6_load_raw`). The nine GoPro entries (Fusion, HERO5–HERO12) count
only with `USE_GPRSDK` (gpl). Other changes in the 100 commits: fixes for damaged CR3/CRX, CIFF, Kodak and
Hasselblad files, the X3F data size check, lossy DNG tone curve initialisation.

`build-libraw.sh` runs after `build-jxrlib.sh` in its own layer (the plain LibRaw from
`build-deps.sh` stays what libvips is linked against; same sources and SONAME, so the rebuilt
library replaces it in place) and configures LibRaw with `-DUSE_X3FTOOLS`: that is the LibRaw
of slim and full. The gpl stage runs it again as `build-libraw.sh --with-sdks` over full's
LibRaw, before `build-gpl-deps.sh` (so `LICENSE-GPL.md` lists `dng-sdk` and `gpr`). It compiles the DNG SDK (everything but `dng_validate`,
`dng_xmp_sdk` and `dng_update_meta`) and the GPR SDK's VC-5 decoder into static, PIC,
`-fvisibility=hidden` archives, then configures LibRaw with `-DUSE_X3FTOOLS -DUSE_DNGSDK
-DUSE_GPRSDK` and those archives, so they end up inside `libraw.so`/`libraw_r.so` with no
exported symbol and add only `libjxl`/`libjxl_threads` to its dependencies. The DNG SDK is
configured for Linux, thread-safe, **without the XMP SDK** (`qDNGUseXMP=0`: LibRaw reads its
own metadata; this drops XMP Toolkit, boost and expat copies), with libjpeg, and with
`NDEBUG`. A small C check fails the build unless `libraw_capabilities()` reports exactly
`LIBRAW_CAPS_X3FTOOLS` (slim, full) or `LIBRAW_CAPS_X3FTOOLS | DNGSDK | GPRSDK` (gpl), and the
licence gate runs strict in the first case (it fails on the DNG SDK's RTTI names
`12dng_negative`/`8dng_host` in any library) and in its gpl mode in the second.

Patches (`git apply`, in order; gpl only, since each one only matters with the SDKs):

| Patch | Fix |
|---|---|
| `patches/libraw/0001-c-api-handles-own-a-dng-host.patch` | LibRaw's C API has no `set_dng_host`, so the DNG SDK could never be used through `libraw_init`: with `USE_DNGSDK`, every C API handle owns a default single-threaded `dng_host`, deleted with the handle |
| `patches/dng-sdk/0001-build-without-xmp.patch` | `dng_jxl.cpp` still used `dng_xmp` with `qDNGUseXMP=0` (JXL XMP box on encode, XMP parse on decode) |
| `patches/gpr/0001-read-vc5-through-the-dng-sdk-1.7-reader.patch` | `gpr_read_image::ReadTile` follows DNG SDK 1.7's signature (LibRaw's `GoPro/*.diff`); a failed VC-5 decode throws, the decoded buffer must cover the IFD's image, tiled VC-5 is refused, the tile must lie inside the file |
| `patches/gpr/0002-harden-the-vc5-decoder.patch` | found by libFuzzer + ASan/LSan (below): bounds for the memory stream reader, band runs, channel/subband numbers and the header's counts, failed allocations checked, the decoder freed on errors, no stderr output |

The shim (`internal/vips/raw.c`) sets `rawparams.use_dngsdk = LIBRAW_DNG_NONE`: LibRaw hands the
SDK only what it cannot decode itself (JPEG XL and VC-5 raw data, which it routes there
whatever the flag); every other DNG keeps LibRaw's own decoders, as before. For a linear
(demosaiced) DNG it also sets `LIBRAW_RAWOPTIONS_DNG_STAGE23_IFPRESENT_JPGJXL`, so the SDK
applies the file's opcode lists to lossy JPEG / JPEG XL data (without it Adobe's linear JPEG XL
sample renders magenta and clipped; Bayer ones need no stage 2 and fail with it).

**Not added:** RawSpeed (no gain: it has no CR3/HE/JPEG XL DNG; its VC-5 path duplicates the GPR
SDK); the XMP SDK; the DNG SDK's bundled libjxl/libjpeg copies (the base's are used).

**Fuzzing (WP-103).** libFuzzer + ASan/LSan on LibRaw's C API as the shim drives it
(`open_buffer`, decoder check, `unpack`, half-size `dcraw_process`, `make_mem_image`; 6 MP and
256 MB caps), with LibRaw, the DNG SDK and the GPR SDK instrumented (libjxl and libjpeg-turbo
not), seeded with the DNG fixtures, the JPEG XL DNGs, two small GPRs and the Sigma SD14 X3F.
The first rounds crashed within seconds, all in GoPro's VC-5 reference decoder, whose checks
are `assert()`s: a heap overflow writing a band run past the band, heap over-reads and wild
reads of the memory stream past the buffer (the most frequent), a channel count of up to 255
indexing four-entry arrays, codeblocks dereferencing wavelets that were never allocated, and
leaks on every failed decode; all fixed in `patches/gpr/0002`. The final rounds were clean:
libFuzzer 3 workers × 15 min (892 k executions over the DNG/JPEG XL DNG/GPR corpus), 20 min
on the 12 MB X3F (825 executions: X3F tools, no finding; the slowest input took 12 s under
ASan), and Go `FuzzRawLoad` 10 min (3.7 M executions through the shim). Three of the
crashing inputs are `FuzzRawLoad` regression seeds (`internal/vips/testdata/rawfuzz`). The
DNG SDK's JPEG XL path decodes through libjxl, which was not instrumented (libjxl is fuzzed
upstream).

### zstd for libtiff (WP-102, revision r5)

| Component | Version | Licence | Source | Used by |
|---|---|---|---|---|
| zstd (libzstd) | `ZSTD_VERSION` | BSD-3-Clause (dual-licensed BSD-3-Clause OR GPL-2.0-only; used under BSD-3-Clause, so the bundle ships its `LICENSE` only) | GitHub release tarball | libtiff's ZSTD codec (TIFF compression 50000) |

Built as a shared library only (CMake, no CLI, no legacy formats, no multithreading), then
libtiff with `-Dzstd=ON`; `build-deps.sh` fails when `libtiff.so` does not end up linking
libzstd, and `verify-image.sh` round-trips a ZSTD, a WebP and a deflate TIFF through
`tiffsave`/`tiffload` on every flavour. Every flavour gets it (full and gpl start from slim):
slim r5, full r6, gpl r7. Checked and not added: JPEG XL in TIFF (compression 50002) is a
GDAL extension that upstream libtiff does not implement; WebP in TIFF was already on; LZMA
(liblzma, 0BSD) and LERC stay off until a sample needs them.

Compiler flags (`bashrc.sh`): `-Os` baseline (`-O3` for codecs), `-fstack-protector-strong`,
full RELRO, `-ffunction-sections -fdata-sections -Wl,--gc-sections`, rpath
`/opt/xtandard/lib`; Ubuntu's GCC adds `-D_FORTIFY_SOURCE=3`. Baseline ISA: x86-64 with SSE4,
arm64 `armv8.2-a+fp16` (Graviton2+, Ampere Altra, Apple silicon; not Cortex-A72 boards).
jemalloc uses 64 KiB pages on arm64 so it also runs on 64K-page kernels.

## Sizes

linux/arm64, `docker save | gzip -6` (compressed) and `docker image inspect` (uncompressed):

| Image | Compressed | Uncompressed | Budget (compressed) |
|---|---|---|---|
| slim runtime (`8.18.6-r7-slim`) | 56.0 MB | 167.8 MB | ≤ 110 MB |
| slim builder | 260.6 MB | 821.9 MB | — |
| full runtime (`8.18.6-r8-full`) | 124.6 MB | 261.7 MB | ≤ 170 MB |
| full builder | 329.8 MB | 918.3 MB | — |
| gpl runtime (`8.18.6-r9-gpl`) | 140.5 MB | 285.6 MB | ≤ 170 MB (as full) |
| gpl builder | 346.2 MB | 943.4 MB | — |

r7/r8/r9 over r6/r7/r8 (WP-108): the Basis Universal transcoder (`libxibasisu`) adds 0.6 MB
compressed / 1.1 MB uncompressed to every flavor; the PICT and PCD coders cost nothing. The gpl
flavor adds another 2.2 MB compressed / 3.8 MB uncompressed for libwmf with its URW fonts,
libemf2svg and `xi-metafile2svg`.

r6/r7/r8 over r5/r6/r7: slim +0.2 MB compressed (the LibRaw snapshot with the X3F tools; the
WP-107 XCF policy costs nothing), full +20.3 MB (the WP-110 classifier), gpl +20.7 MB (the
classifier and LibRaw's SDKs). With the Adobe DNG SDK and the VC-5 decoder (gpl only)
`libraw_r.so` grows from 1.3 to 2.2 MB (about 0.4–0.6 MB compressed / 1.0–1.2 MB
uncompressed, measured on the WP-103 builds).

zstd (r5, WP-102) adds 0.3–0.4 MB compressed / 0.8 MB uncompressed to every flavor.

jxrlib (r4, WP-97) adds 0.2 MB compressed / 0.4 MB uncompressed to every flavor; the wider
ImageMagick policy costs nothing (the coders were already compiled in).
The full additions are ONNX Runtime 24 MiB, the models 56 MiB (YOLOX-s 34 MiB, MobileNetV3-Large
21 MiB, YuNet 0.2 MiB) and FFmpeg + libvpx 11 MiB (libraries and both CLIs), uncompressed.
The classifier (WP-110) adds 20.3 MB compressed / 21.9 MB uncompressed to full and gpl.
The gpl flavor adds 2.2 MB compressed / 4.8 MB uncompressed over full for libx264, libx265 and
the two encoders in libavcodec (r5), and Ghostscript another 11.1 MB compressed / 14.5 MB
uncompressed (r6, WP-99: the 14.4 MB static `gs`, whose compiled-in resources and fonts are
already compressed, plus its licence texts).

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

The gpl flavor (WP-70b) runs the same gate in a **gpl mode**; slim and full never use it,
so their results are unchanged:

- `--allow-gpl <versions-gpl.sh>`: the components in its `XI_GPL_COMPONENTS`
  (`component=SPDX`) stop being forbidden, and only those: their file names, symbols
  (`x264_encoder_open`, `libx265.so`), pins and ldd results pass. Only `x264`, `x265` and
  `ffmpeg` can be listed, and only under GPL-2.0-only/-or-later or GPL-3.0-only/-or-later; an
  AGPL, unknown or other licence fails, and so does an unlisted component (x265 present but
  not listed fails). `--ffmpeg` then requires `--enable-gpl` in `-buildconf`, `ffmpeg -L`
  reporting the GNU General Public License, and no `--enable-version3` for a GPL-2.0 entry.
  Everything else stays forbidden: `--enable-nonfree`, xvid, frei0r, libpostproc, poppler,
  exiv2, Ghostscript, libimagequant, ...
- `--licenses <dir>` (with `--allow-gpl`): every entry of the licence bundle must have a
  licence in `XI_COMPONENT_LICENSES` built only from known SPDX identifiers (SSPL and
  unknown identifiers fail, AGPL for anything but an allowed `ghostscript` fails; an entry
  without a licence fails), and `<dir>/LICENSE-GPL.md` must exist and declare
  GPL-2.0-or-later (and AGPL-3.0-or-later when ghostscript is allowed).
- Ghostscript (WP-99): `ghostscript` is the one component allowed under an AGPL, and only as
  exactly `ghostscript=AGPL-3.0-or-later`; `--ghostscript <gs>` then requires `gs --version`
  to run and `gs -h` to list exactly `bbox nullpage png16m pngalpha`. `libgs` in any library
  (file name, `libgs.so`, `gsapi_new_instance`) fails even then, and `--ghostscript` without
  the allowlist (slim, full) fails.

`build-gpl-deps.sh` runs it with `--allow-gpl --licenses --ffmpeg --ghostscript --ldd --models`,
`split-runtime.sh` with `XI_ALLOW_GPL`, and `verify-image.sh --flavor gpl` again inside
`runtime-gpl`. `test-check-licenses.sh` covers the gpl mode (clean gpl tree passes; the same
tree fails the strict gate; AGPL/unknown/unlisted entries, nonfree, version3, a non-GPL
FFmpeg, libpostproc, xvid, unknown bundle entries and a missing notice fail; ghostscript
passes only as AGPL-3.0-or-later with the minimal device list and an AGPL notice, AGPL for
x264 or another bundle entry, extra gs devices, libgs and gs in the strict gate fail) and checks that
the real `versions-gpl.sh` is refused by the strict gate.

## Bumping versions

1. Edit the `*_VERSION` line(s) in `versions.sh`. For a libvips bump keep 8.18.x until the
   project moves to 8.19 deliberately (ADR); check
   [libvips releases](https://github.com/libvips/libvips/releases).
2. Run `docker/base/update-checksums.sh` (works on macOS and Linux). It downloads every
   pinned tarball and toolchain for both architectures and rewrites the `*_SHA256` lines of
   `versions.sh`, `versions-full.sh` and `versions-gpl.sh`.
   Review the diff: only the bumped entries should change.
3. Bump `XI_BASE_REVISION` when the image changes without a libvips version change; reset it
   to `1` when `VIPS_VERSION` changes. `XI_BASE_FULL_REVISION` (`versions-full.sh`) is the
   full flavor's own counter: bump it whenever slim's revision moves or a full-only component
   changes, including when slim's revision moves (full r8 = slim r7 +
   WP-70 + the classifier, WP-110).
   `XI_BASE_GPL_REVISION` (`versions-gpl.sh`) is the gpl flavor's counter in the same way:
   bump it when full's revision moves or x264/x265/Ghostscript/the LibRaw SDKs/the metafile
   converter change (gpl r9 = full r8 + WP-70b + Ghostscript, WP-99 + the Adobe DNG SDK and
   GoPro GPR SDK, WP-103 + libwmf, libemf2svg and `xi-metafile2svg`, WP-108).
4. Build and verify locally (host architecture: arm64 on Apple silicon, amd64 on x86 hosts;
   slim ~30 min cold, full adds ~10 min, gpl another ~5 min):

   ```sh
   task docker:base:slim docker:base:slim:check   # xtandard-base:dev, xtandard-base:dev-builder
   task docker:base:full docker:base:full:check   # xtandard-base:dev-full, xtandard-base:dev-full-builder
   task docker:base:gpl docker:base:gpl:check     # xtandard-base:dev-gpl, xtandard-base:dev-gpl-builder
   ```

   The tasks run the `docker buildx build` commands below (full: `--target builder-full` and
   `--target runtime-full`; gpl: `--target builder-gpl` and `--target runtime-gpl`) and
   `verify-image.sh --flavor slim|full|gpl`.

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
| `Dockerfile` | multi-stage build (`builder`, `runtime`, `builder-full`, `runtime-full`, `builder-gpl`, `runtime-gpl`) |
| `versions.sh` | pinned versions + SHA256 of every tarball and toolchain |
| `versions-full.sh` | full flavor pins: FFmpeg, libvpx, ONNX Runtime (per arch), models + licences |
| `versions-gpl.sh` | gpl flavor pins: x264, x265; the GPL allowlist (`XI_GPL_COMPONENTS`) and the licence of every bundle entry (`XI_COMPONENT_LICENSES`) |
| `download-deps.sh` | fetch + verify + unpack sources; `PRINT_CHECKSUMS=1` mode |
| `download-full-deps.sh` | same for the full flavor |
| `download-gpl-deps.sh` | same for the gpl flavor (x264, x265, Ghostscript, libwmf, libemf2svg) |
| `build-deps.sh` | build into `/opt/xtandard`, license bundle, SBOM stubs, license gate |
| `build-jxrlib.sh`, `patches/jxrlib/` | jxrlib with its patches (own layer), gate |
| `build-basisu.sh`, `basisu/`, `patches/basisu/` | Basis Universal transcoder + the app's C wrapper as `libxibasisu` (own layer, WP-108), export check, gate |
| `build-gpl-metafile.sh`, `metafile/`, `patches/libwmf/` | gpl flavor: libwmf, libemf2svg and the `xi-metafile2svg` driver (WP-108), licences, smoke conversions |
| `download-libraw-deps.sh` | fetch + verify the Adobe DNG SDK zip and the GoPro GPR SDK (pins in `versions-gpl.sh`), keep only what is compiled (own stage, copied by gpl-deps only) |
| `build-libraw.sh`, `patches/{libraw,dng-sdk,gpr}/` | LibRaw rebuilt with the X3F tools (every flavor, own layer), and with `--with-sdks` also the DNG SDK and the GPR SDK (gpl-deps); capability check, licences, gate |
| `build-full-deps.sh` | full flavor: libvpx + FFmpeg, ONNX Runtime, models, licences, gate |
| `build-gpl-deps.sh` | gpl flavor: x264, x265, FFmpeg rebuilt with `--enable-gpl`, licences, `LICENSE-GPL.md`, gate (gpl mode) |
| `split-runtime.sh` | copy the runtime subset of `/opt/xtandard` to `/runtime` + gate (`XI_ALLOW_GPL` for gpl) |
| `bashrc.sh` | per-architecture compiler flags (`BASH_ENV`) |
| `install-rust.sh` | pinned Rust + cargo-c (build time only: quantizr, librsvg) |
| `install-tools.sh` | Go, golangci-lint, go-task for the builder stage |
| `magick-policy.xml` | ImageMagick security policy (PSD/PSB, the WP-97 legacy coders, XCF, PICT and PCD, read only) |
| `check-licenses.sh`, `test-check-licenses.sh` | license gate and its self-test |
| `update-checksums.sh` | recompute SHA256 lines after a bump |
| `verify-image.sh` | smoke test used by CI and locally (`--flavor full` adds FFmpeg, ONNX Runtime, model checks; `--flavor gpl` adds the GPL gate, x264/x265 and H.264/HEVC round trips) |
