# ADR 0006: License policy

- Status: accepted
- Date: 2026-09-26

## Context

The project is MIT. It links many C libraries and may ship ML models. GPL/AGPL components would force the combined work's license or create distribution obligations incompatible with the project's goals.

## Decision

- Allowed in shipped artefacts: MIT, BSD-2/3, Apache-2.0, ISC, Zlib, libpng, IJG, OFL (fonts), MPL-2.0 (file-level), LGPL-2.1+/LGPL-3 **dynamically linked only** with source offer and license text shipped. LibRaw used under LGPL-2.1 (or CDDL-1.0).
- Forbidden: GPL, AGPL, SSPL, non-commercial or "research only" licenses, including model weights. Concretely excluded: x265 (use kvazaar), poppler (use PDFium), exiv2, GPL FFmpeg builds (`--enable-gpl`, libx264, libx265), libimagequant ≥ 2.12 (GPL-3; use quantizr), Ultralytics YOLO weights (AGPL), Ghostscript, `dssim` CLI (AGPL; own SSIM implementation instead).
- Go dependencies follow the same list; checked with `go-licenses` in CI.
- `golang.org/x/image` v0.46.0 (BSD-3-Clause, released 2026-09-08), added by WP-36: WebP/BMP/TIFF decoders for the golden harness (`internal/testutil/golden`) and `font/basicfont` for the fixture generator (`testdata/gen`). Decode-only, test support; the service decodes through libvips.
- Source code ported or adapted from other projects keeps its licence obligations. imgproxy is **Apache-2.0** (not MIT; only imgproxy-docker-base and imagor-base are MIT): every file derived from it (for example the BMP/ICO codecs) keeps the upstream copyright header plus a modification notice, and the repository `NOTICE` file credits imgproxy and reproduces any upstream NOTICE text that applies. MIT-licensed build scripts (imgproxy-docker-base) keep their copyright notice in `docker/base/`.
- `build-deps.sh` runs a license gate that fails on any forbidden license; every image contains `/opt/xtandard/share/licenses/` and `SOURCES.md` with exact source URLs and versions.
- Test fixtures must be redistributable; licenses recorded in `testdata/LICENSES.md`.
- Patent-encumbered codecs (HEVC decode/encode, H.264 encode) are a separate question from copyright licenses; tracked in the open questions of `docs/ROADMAP.md`.

## Consequences

- Some capabilities cost more work (own BMP/ICO loaders, own SSIM, PDFium integration) or are unavailable (H.264 encoding without openh264 decision, x265-quality HEIC encoding).
- Users may build their own images with GPL components; such builds are unsupported and must not be published under the project name.
