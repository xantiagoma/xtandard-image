# Test data licences

Every file under `testdata/` is original to this repository and dedicated to the public domain under
[CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/). No third-party photograph, icon or
artwork is included, so there is no attribution or share-alike obligation (ADR 0006: no GPL or
CC-BY-SA fixtures).

The pixels come from `testdata/gen/main.go` (procedural landscape "photo" with value-noise
terrain, clouds, a sun, lake reflection, Gaussian sensor noise and a caption, plus a flat-colour
"graphic"), or from `testdata/gen/logo.svg` (hand-written). `testdata/gen/gen.sh` converts them
with the tools below. The tools only encode our pixels; no code or data from them is embedded
in the files, and none of them is shipped.

The caption is drawn with `golang.org/x/image/font/basicfont` (BSD-3-Clause; a bitmap face
generated from the public-domain X11 "fixed" font).

| File | Made with | Licence |
|---|---|---|
| `fixtures/jpeg/exif-orientation-6-icc.jpg` | libvips `jpegsave` Q85 of the photo rotated 90° CCW, embedded ICC = libvips built-in `srgb` profile (Clinton Ingram's compact-icc-profiles, CC0), then `gen exif6` adds an Exif APP1 with Orientation = 6 | CC0 |
| `fixtures/jpeg/metadata-p3.jpg` | libvips `icc_transform` of the small photo (rotated 90° CCW) to the built-in `p3` profile (Clinton Ingram's compact-icc-profiles, CC0) and `jpegsave` Q85, then `gen metadata` adds Exif (orientation 6, Artist, Copyright, GPS), XMP (with GPS) and IPTC segments written by hand | CC0 |
| `fixtures/jpeg/progressive.jpg` | libvips `jpegsave` Q80 `interlace` | CC0 |
| `fixtures/jpeg/baseline.jpg` | libvips `jpegsave` Q90 | CC0 |
| `fixtures/png/rgba8.png` | `gen photo-alpha` (Go `image/png`) | CC0 |
| `fixtures/png/bordered.png` | `gen bordered` (Go `image/png`: the photo on white with 6/20/12/4 px margins) | CC0 |
| `fixtures/png/rgb16.png` | `gen photo16` (Go `image/png`, 16-bit) | CC0 |
| `fixtures/png/palette.png` | `gen palette` (Go `image/png`, Plan 9 palette) | CC0 |
| `fixtures/gif/static.gif`, `fixtures/gif/animated.gif` | `gen gif`, `gen gif-anim` (Go `image/gif`, 6 frames) | CC0 |
| `fixtures/webp/lossy.webp`, `lossless.webp`, `animated.webp` | libvips `webpsave` (libwebp, BSD-3) | CC0 |
| `fixtures/avif/photo.avif` | libvips `heifsave` AV1 (libheif + libaom, BSD-2) | CC0 |
| `fixtures/heic/photo.heic` | macOS `sips` (Apple HEVC encoder; no x265/GPL encoder involved) | CC0 |
| `fixtures/jxl/photo.jxl` | libvips `jxlsave` (libjxl, BSD-3) | CC0 |
| `fixtures/jp2/photo.jp2` | libvips `jp2ksave` (OpenJPEG, BSD-2) | CC0 |
| `fixtures/tiff/multipage.tif` | ImageMagick 7 (ImageMagick licence), 2 pages, deflate | CC0 |
| `fixtures/bmp/rgb24.bmp`, `fixtures/bmp/rle8.bmp` | ImageMagick 7 `BMP3:` (24-bit; 8-bit RLE8) | CC0 |
| `fixtures/ico/png-entry.ico`, `bmp-entry.ico` | `gen ico-png`, `gen ico-bmp` (own ICO writer) | CC0 |
| `fixtures/svg/logo.svg` | hand-written (`gen/logo.svg`) | CC0 |
| `fixtures/uhdr/photo-ultrahdr.jpg` | libvips `uhdrsave` of the photo in scRGB ×2.5 (libultrahdr, Apache-2.0) | CC0 |
| `ssim/*.png` | `gen ssim-refs` (Go only: greyscale photo plus mean shift, contrast stretch, impulse noise, Gaussian blur σ 1.5, stdlib JPEG q15) | CC0 |
| `golden/**` | written by tests with `GOLDEN_UPDATE=1` from the fixtures above | CC0 |
