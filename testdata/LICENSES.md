# Test data licences

Every file under `testdata/` is dedicated to the public domain under
[CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/). All of it is original to this
repository except the `golden/exec/raw-{cr2,cr3,nef,arw,dng,raf}*.png` goldens, which are
downscaled renders of the CC0 camera samples listed at the end (third-party photographs, public
domain). There is no attribution or share-alike obligation (ADR 0006: no GPL or CC-BY-SA
fixtures).

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
| `fixtures/watermark/logo.png` | `gen watermark` (Go `image/png`, 48x32 RGBA) | CC0 |
| `fixtures/gif/static.gif`, `fixtures/gif/animated.gif` | `gen gif`, `gen gif-anim` (Go `image/gif`, 6 frames) | CC0 |
| `fixtures/webp/lossy.webp`, `lossless.webp`, `animated.webp` | libvips `webpsave` (libwebp, BSD-3) | CC0 |
| `fixtures/avif/photo.avif` | libvips `heifsave` AV1 (libheif + libaom, BSD-2) | CC0 |
| `fixtures/heic/photo.heic` | macOS `sips` (Apple HEVC encoder; no x265/GPL encoder involved) | CC0 |
| `fixtures/heic/thumbnail.heic`, `fixtures/avif/thumbnail.avif` | `gen/heifthumb/heifthumb.c` (ours, CC0) against libheif with kvazaar (HEVC, BSD-3) and libaom (AV1, BSD-2) in the base builder image | CC0 |
| `fixtures/jxl/photo.jxl` | libvips `jxlsave` (libjxl, BSD-3) | CC0 |
| `fixtures/jp2/photo.jp2` | libvips `jp2ksave` (OpenJPEG, BSD-2) | CC0 |
| `fixtures/tiff/multipage.tif` | ImageMagick 7 (ImageMagick licence), 2 pages, deflate | CC0 |
| `fixtures/bmp/rgb24.bmp`, `fixtures/bmp/rle8.bmp` | ImageMagick 7 `BMP3:` (24-bit; 8-bit RLE8) | CC0 |
| `fixtures/ico/png-entry.ico`, `bmp-entry.ico` | `gen ico-png`, `gen ico-bmp` (own ICO writer) | CC0 |
| `fixtures/svg/logo.svg` | hand-written (`gen/logo.svg`) | CC0 |
| `fixtures/uhdr/photo-ultrahdr.jpg` | libvips `uhdrsave` of the photo in scRGB ×2.5 (libultrahdr, Apache-2.0) | CC0 |
| `fixtures/pdf/pages.pdf` | `gen pdf`: a PDF 1.4 written by hand in Go (three pages: vector art with the standard Helvetica font, not embedded; the procedural photo as a Flate RGB image; vector art) | CC0 |
| `fixtures/pdf/password.pdf` | `qpdf --encrypt` (qpdf, Apache-2.0) of `pages.pdf`, AES-256, user password `secret`, owner password `owner` | CC0 |
| `fixtures/psd/layered.psd`, `fixtures/psd/psb-layered.psb` | `gen psd`, `gen psb`: Photoshop files written by hand in Go (RGB 8-bit, raw channels, a photo background layer and a flat-graphic layer at an offset, with the merged composite) | CC0 |
| `fixtures/psd/no-composite.psd` | `gen psd-nocomposite`: the same layers saved like "Maximize compatibility" off (version-info resource with `hasRealMergedData = 0`, blank merged image) | CC0 |
| `fixtures/exr/rgba-half.exr`, `fixtures/exr/rgb-float.exr` | `gen exr-rgba-half`, `gen exr-rgb-float`: OpenEXR files written by hand in Go (single-part scanline, no compression; RGBA half and RGB float) from the procedural HDR scene | CC0 |
| `fixtures/hdr/scene.hdr`, `fixtures/hdr/rgbe.hdr` | `gen radiance`, `gen rgbe`: Radiance RGBE written by hand in Go (flat scanlines; `#?RADIANCE` and `#?RGBE` magic lines) | CC0 |
| `fixtures/pfm/scene.pfm` | `gen pfm`: colour PFM written by hand in Go (little endian) | CC0 |
| `fixtures/ppm/photo.ppm` | `gen ppm`: the procedural photo as a binary 8-bit PPM (P6), written in Go | CC0 |
| `ssim/*.png` | `gen ssim-refs` (Go only: greyscale photo plus mean shift, contrast stretch, impulse noise, Gaussian blur σ 1.5, stdlib JPEG q15) | CC0 |
| `fixtures/dng/landscape.dng`, `fixtures/dng/portrait-orientation-6.dng` | `gen dng` (own DNG writer; the preview is Go `image/jpeg` q85 of the photo, the mosaic is the photo sampled as linear RGGB) | CC0 |
| `golden/**` | written by tests with `GOLDEN_UPDATE=1` from the fixtures above and the RAW samples below | CC0 |

## Downloaded camera RAW samples (not committed)

`internal/testutil/rawfixtures` downloads these at test time into `testdata/cache/raw/` (gitignored).
All come from [raw.pixls.us](https://raw.pixls.us/), whose contributors dedicate each sample to the
public domain under [CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/); every file below
is listed with the CC0 licence there (checked 2026-09-26). The goldens rendered from them are CC0
as well.

| Cache name | raw.pixls.us file | SHA-256 | Size |
|---|---|---|---|
| `canon-eos-350d.cr2` | Canon - EOS Digital Rebel XT - RAW (3:2).CR2 (id 1230; portrait, orientation 8) | `4b47ba27c0090efa091aaf99babc76d9a3e6f6f121b443d6c58c3ce7275cd770` | 7.1 MB |
| `canon-eos-r6.cr3` | Canon - EOS R6 - 3:2.CR3 (id 4659) | `74abb0a113d075ad9887a058082f40dd2a938c4813a08474d82356f11a027778` | 5.0 MB |
| `nikon-d70s.nef` | Nikon - D70s - 12bit 12bit compressed (Lossy (type 1)) (3:2).NEF (id 1117) | `6c46f9854033e7623ebbb5de8c951df6762ca44f05d45035ccc5cb7e9f4e5692` | 5.0 MB |
| `sony-ilce-7s.arw` | Sony - ILCE-7S - 14bit 14bit compressed (3:2).ARW (id 1582) | `a35ebb2fbec929daa5beb20d1ce5c15a8aac7b1a7a231455387f3df8a7442e07` | 5.9 MB |
| `pentax-k-50.dng` | Pentax - K-50 - 12bit (3:2).DNG (id 2235) | `9ea1dc1f92df19699aa8b620670c64a84d4ead0172a49cbbd47dea9257d14e67` | 12.9 MB |
| `fujifilm-x-s10.raf` | Fujifilm - X-S10 - 14bit 14bit compressed (3:2).RAF (id 4190) | `9419d1408ebf850395e5dd563beb96309466546252caf541466034c9cb7e9724` | 17.8 MB |
