# shellcheck shell=bash
# Pinned versions and SHA256 checksums of the components that only the `gpl` flavor
# adds on top of full (WP-70b): the GPL H.264 and HEVC encoders x264 and x265, an
# FFmpeg rebuilt with --enable-gpl so that it can link them, and (WP-99) a minimal
# Ghostscript `gs` program (AGPL-3.0) for EPS/PostScript input, run out of process.
#
# The gpl flavor is opt-in and GPL-licensed as a whole (ADR 0006, amendment
# 2026-09-27). slim and full stay permissive: they never COPY this file, and the licence
# gate refuses these components unless it runs with `--allow-gpl versions-gpl.sh`.
# Bump a version, then run ./update-checksums.sh to refresh the *_SHA256 lines.

# Revision of the gpl flavor. Its tag is <VIPS_VERSION>-r<XI_BASE_GPL_REVISION>-gpl.
# gpl r9 = full r8 (versions-full.sh XI_BASE_FULL_REVISION) + the components below
# (r6: Ghostscript, WP-99; r8: LibRaw with the Adobe DNG SDK and GoPro GPR SDK, WP-103;
# r9: libwmf, libemf2svg and xi-metafile2svg, WP-108).
export XI_BASE_GPL_REVISION=9

# --- GPL allowlist read by check-licenses.sh --allow-gpl ----------------------------
# component=SPDX pairs. Only these GPL components may appear in a gpl image, and only
# under GPL-2.0/GPL-3.0; ghostscript is the one AGPL exception, exactly
# AGPL-3.0-or-later (WP-99, ADR 0011). Anything else AGPL, nonfree or unlisted still
# fails the gate. ffmpeg is the FFmpeg build as a whole: with --enable-gpl it is
# GPL-2.0-or-later. dng-sdk is not GPL but is confined to this flavour the same way
# (owner decision 2026-09-28): the Adobe DNG SDK licence (LicenseRef-Adobe-DNG-SDK,
# permissive with an indemnity clause) is allowed only here, exactly under that id.
# libemf2svg (WP-108, EMF input) is GPL-2.0-only, with the libUEMF copy it vendors.
export XI_GPL_COMPONENTS="x264=GPL-2.0-or-later x265=GPL-2.0-or-later ffmpeg=GPL-2.0-or-later ghostscript=AGPL-3.0-or-later dng-sdk=LicenseRef-Adobe-DNG-SDK libemf2svg=GPL-2.0-only"

# --- H.264 encoder: x264 (GPL-2.0-or-later) ------------------------------------------
# x264 publishes no release tarballs; this is the head of its `stable` branch
# (X264_BUILD 165), fetched as a GitLab archive of that commit.
export X264_VERSION=0.165-b35605ac
export X264_COMMIT=b35605ace3ddf7c1a5d67a2eb553f034aef41d55
export X264_SHA256=6eeb82934e69fd51e043bd8c5b0d152839638d1ce7aa4eea65a3fedcf83ff224

# --- HEVC encoder: x265 (GPL-2.0-or-later), 8-bit Main profile -------------------------
export X265_VERSION=4.2
export X265_SHA256=40b1ea0453e0309f0eba934e0ddf533f8f6295966679e8894e8f1c1c8d5e1210

# --- PostScript interpreter: Ghostscript (AGPL-3.0-or-later), `gs` only ---------------
# A separate program the server runs in a sandboxed subprocess (internal/ghostscript),
# never linked. Built without X11, CUPS, the PDF interpreter (and its JPEG 2000 and
# bi-level decoders), TIFF, OCR and every device but pngalpha, png16m and bbox, with
# its resources and bundled fonts compiled in (build-gpl-deps.sh). Release tarball from ghostpdl-downloads; the SHA256
# was checked against the release SHA512SUMS.
export GHOSTSCRIPT_VERSION=10.08.0
export GHOSTSCRIPT_SHA256=c20492bc8ebb96c87fa2e52a0926e1cda8cde95d66145e018ac713fed5da38cf

# --- Windows metafiles (WP-108): libwmf and libemf2svg ---------------------------------
# Linked by xi-metafile2svg (docker/base/metafile, the app's MIT driver), a separate
# program the server runs as a sandboxed subprocess to turn WMF and EMF sources into SVG
# (build-gpl-metafile.sh). libwmf (WMF) is LGPL-2.0-or-later, with the URW Type 1 fonts
# it installs for text metrics (GPL); libemf2svg (EMF) and the libUEMF it vendors are
# GPL-2.0-only, so the program is a GPL-2.0 work and ships in this flavor only. GitHub
# archives of the release tags.
export LIBWMF_VERSION=0.2.16
export LIBWMF_SHA256=50f36f939b751274b5f0abef9fb38c23c101ebf8bba0934cfbf5444ec37011f0
export LIBEMF2SVG_VERSION=1.8.1
export LIBEMF2SVG_SHA256=5a4b0051a2e8a7f999ecc1bb3451032833dc4c4f1e4bc6d938e4d5b4bec19c10

# --- LibRaw's SDKs (WP-103): Adobe DNG SDK and GoPro GPR SDK --------------------------
# Downloaded by download-libraw-deps.sh (stage libraw-deps-src) and compiled into
# libraw_r by `build-libraw.sh --with-sdks` in the gpl-deps stage only; slim and full
# build LibRaw with the X3F tools alone. Adobe DNG SDK (JPEG XL and VC-5 DNG decoding;
# Adobe DNG SDK License Agreement, ADR 0006 amendment WP-103): the release zip, whose
# date is part of its name. GoPro GPR SDK (Apache-2.0 OR MIT): its VC-5 decoder, from
# the head of master (no release since v1.1 in 2019); LibRaw reaches it through the
# DNG SDK, so it ships only where the DNG SDK does.
export DNGSDK_VERSION=1.7.1-2724
export DNGSDK_ARCHIVE=dng_sdk_1_7_1_2724_20260908.zip
export DNGSDK_SHA256=740fbe95c69e09e9cd17654a5e4fef2d7021254b06fd2b8c5557b79a1496b50c
export GPR_VERSION=1.1-446c736
export GPR_COMMIT=446c736a38fb14f51343605c0780d347dc602f89
export GPR_SHA256=ddb09f4910f07f43e7d93dbda41d20a6af572be5ac7032052f2fa24b33aeeb0c

# --- Licence of every component in /opt/xtandard/share/licenses ----------------------
# One line per entry of the licence bundle: <entry> <SPDX expression>. The gpl build
# writes share/licenses/LICENSE-GPL.md from it, and check-licenses.sh --licenses fails on
# an entry that is missing here or uses a licence outside its allowlist (AGPL, SSPL,
# non-commercial, unknown identifiers).
XI_COMPONENT_LICENSES='
aom BSD-2-Clause
basisu Apache-2.0
brotli MIT
cairo LGPL-2.1-only OR MPL-1.1
cgif MIT
dav1d BSD-2-Clause
dng-sdk LicenseRef-Adobe-DNG-SDK
expat MIT
ffi MIT
ffmpeg GPL-2.0-or-later
fontconfig HPND-sell-variant
fonts-dejavu.copyright Bitstream-Vera
freetype FTL
fribidi LGPL-2.1-or-later
ghostscript AGPL-3.0-or-later AND Apache-2.0 AND FTL AND IJG AND libpng-2.0 AND Zlib AND MIT
glib LGPL-2.1-or-later
gpr Apache-2.0 OR MIT
harfbuzz MIT-Modern-Variant
highway Apache-2.0 OR BSD-3-Clause
imagemagick ImageMagick
imath BSD-3-Clause
jemalloc BSD-2-Clause
jxrlib BSD-2-Clause
kvazaar BSD-3-Clause
lcms2 MIT
libde265 LGPL-3.0-or-later
libemf2svg GPL-2.0-only
libexif LGPL-2.1-or-later
libheif LGPL-3.0-or-later
libjpeg-turbo IJG AND BSD-3-Clause AND Zlib
libjxl BSD-3-Clause
libpng libpng-2.0
libraw (LGPL-2.1-only OR CDDL-1.0) AND BSD-3-Clause
librsvg LGPL-2.1-or-later
libspng BSD-2-Clause
libtiff libtiff
libultrahdr Apache-2.0
libvpx BSD-3-Clause
libwebp BSD-3-Clause
libwmf LGPL-2.0-or-later AND GPL-2.0-or-later
libxml2 MIT
models MIT AND Apache-2.0
onnxruntime MIT
openexr BSD-3-Clause
openjpeg BSD-2-Clause
pango LGPL-2.0-or-later
pcre2 BSD-3-Clause WITH PCRE2-exception
pdfium BSD-3-Clause
pixman MIT
quantizr MIT
vips LGPL-2.1-or-later
x264 GPL-2.0-or-later
x265 GPL-2.0-or-later
zlib Zlib
zstd BSD-3-Clause
'
export XI_COMPONENT_LICENSES
