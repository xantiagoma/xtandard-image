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
# gpl r7 = full r6 (versions-full.sh XI_BASE_FULL_REVISION) + the components below
# (r6: Ghostscript, WP-99).
export XI_BASE_GPL_REVISION=7

# --- GPL allowlist read by check-licenses.sh --allow-gpl ----------------------------
# component=SPDX pairs. Only these GPL components may appear in a gpl image, and only
# under GPL-2.0/GPL-3.0; ghostscript is the one AGPL exception, exactly
# AGPL-3.0-or-later (WP-99, ADR 0011). Anything else AGPL, nonfree or unlisted still
# fails the gate. ffmpeg is the FFmpeg build as a whole: with --enable-gpl it is
# GPL-2.0-or-later.
export XI_GPL_COMPONENTS="x264=GPL-2.0-or-later x265=GPL-2.0-or-later ffmpeg=GPL-2.0-or-later ghostscript=AGPL-3.0-or-later"

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

# --- Licence of every component in /opt/xtandard/share/licenses ----------------------
# One line per entry of the licence bundle: <entry> <SPDX expression>. The gpl build
# writes share/licenses/LICENSE-GPL.md from it, and check-licenses.sh --licenses fails on
# an entry that is missing here or uses a licence outside its allowlist (AGPL, SSPL,
# non-commercial, unknown identifiers).
XI_COMPONENT_LICENSES='
aom BSD-2-Clause
brotli MIT
cairo LGPL-2.1-only OR MPL-1.1
cgif MIT
dav1d BSD-2-Clause
expat MIT
ffi MIT
ffmpeg GPL-2.0-or-later
fontconfig HPND-sell-variant
fonts-dejavu.copyright Bitstream-Vera
freetype FTL
fribidi LGPL-2.1-or-later
ghostscript AGPL-3.0-or-later AND Apache-2.0 AND FTL AND IJG AND libpng-2.0 AND Zlib AND MIT
glib LGPL-2.1-or-later
harfbuzz MIT-Modern-Variant
highway Apache-2.0 OR BSD-3-Clause
imagemagick ImageMagick
imath BSD-3-Clause
jemalloc BSD-2-Clause
jxrlib BSD-2-Clause
kvazaar BSD-3-Clause
lcms2 MIT
libde265 LGPL-3.0-or-later
libexif LGPL-2.1-or-later
libheif LGPL-3.0-or-later
libjpeg-turbo IJG AND BSD-3-Clause AND Zlib
libjxl BSD-3-Clause
libpng libpng-2.0
libraw LGPL-2.1-only OR CDDL-1.0
librsvg LGPL-2.1-or-later
libspng BSD-2-Clause
libtiff libtiff
libultrahdr Apache-2.0
libvpx BSD-3-Clause
libwebp BSD-3-Clause
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
