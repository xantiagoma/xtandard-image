# Test data licences

Every file under `testdata/` is dedicated to the public domain under
[CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/). All of it is original to this
repository except the `golden/exec/raw-{cr2,cr3,nef,arw,dng,raf}*.png` goldens, which are
downscaled renders of the CC0 camera samples listed at the end (third-party photographs, public
domain), the three detection photos in `fixtures/detect/` and the seven classification photos
in `fixtures/classify/` (public domain or CC0, listed in "Detection photos" and
"Classification photos") and the YOLOX functions copied into `detect/reference.py` (Apache-2.0).
There is no share-alike obligation anywhere (ADR 0006: no GPL or CC-BY-SA fixtures), and one
attribution exception below. The libavif test images in `fixtures/avif/` and `fixtures/gainmap/`
(named `libavif-*`, WP-104) are BSD-2-Clause, listed under "libavif samples" with their notice.

**Exception (attribution required):** `fixtures/video/bbb-h264-aac.mp4` carries one second of
*Big Buck Bunny* © 2008 Blender Foundation, www.bigbuckbunny.org, under
[CC-BY 3.0](https://creativecommons.org/licenses/by/3.0/), and the goldens rendered from it
(`golden/exec/video-h264*.png`) are adaptations under the same licence. Source, changes and the
other video fixtures: `fixtures/video/README.md`.

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
| `fixtures/watermark/badge.png` | `gen watermark-badge` (Go `image/png`, 40x40 RGBA) | CC0 |
| `fixtures/gif/static.gif`, `fixtures/gif/animated.gif` | `gen gif`, `gen gif-anim` (Go `image/gif`, 6 frames) | CC0 |
| `fixtures/webp/lossy.webp`, `lossless.webp`, `animated.webp` | libvips `webpsave` (libwebp, BSD-3) | CC0 |
| `fixtures/avif/photo.avif` | libvips `heifsave` AV1 (libheif + libaom, BSD-2) | CC0 |
| `fixtures/heic/photo.heic` | macOS `sips` (Apple HEVC encoder; no x265/GPL encoder involved) | CC0 |
| `fixtures/heic/thumbnail.heic`, `fixtures/avif/thumbnail.avif` | `gen/heifthumb/heifthumb.c` (ours, CC0) against libheif with kvazaar (HEVC, BSD-3) and libaom (AV1, BSD-2) in the base builder image | CC0 |
| `fixtures/avif/sequence.avif`, `fixtures/avif/sequence-alpha.avif`, `fixtures/heic/sequence.heic` | `gen/heifseq/heifseq.c` (ours, CC0): procedural frames written as a libheif sequence track with libaom (AV1, BSD-2) and kvazaar (HEVC, BSD-3) in the base builder image | CC0 |
| `fixtures/jxl/photo.jxl` | libvips `jxlsave` (libjxl, BSD-3) | CC0 |
| `fixtures/jp2/photo.jp2` | libvips `jp2ksave` (OpenJPEG, BSD-2) | CC0 |
| `fixtures/tiff/multipage.tif` | ImageMagick 7 (ImageMagick licence), 2 pages, deflate | CC0 |
| `fixtures/bmp/rgb24.bmp`, `fixtures/bmp/rle8.bmp` | ImageMagick 7 `BMP3:` (24-bit; 8-bit RLE8) | CC0 |
| `fixtures/ico/png-entry.ico`, `bmp-entry.ico` | `gen ico-png`, `gen ico-bmp` (own ICO writer) | CC0 |
| `fixtures/svg/logo.svg` | hand-written (`gen/logo.svg`) | CC0 |
| `fixtures/uhdr/photo-ultrahdr.jpg` | libvips `uhdrsave` of the photo in scRGB ×2.5 (libultrahdr, Apache-2.0) | CC0 |
| `fixtures/pdf/pages.pdf` | `gen pdf`: a PDF 1.4 written by hand in Go (three pages: vector art with the standard Helvetica font, not embedded; the procedural photo as a Flate RGB image; vector art) | CC0 |
| `fixtures/pdf/password.pdf` | `qpdf --encrypt` (qpdf, Apache-2.0) of `pages.pdf`, AES-256, user password `secret`, owner password `owner` | CC0 |
| `fixtures/pdf/password-aes128.pdf` | `qpdf --encrypt` (qpdf, Apache-2.0) of `pages.pdf`, AES-128 (R4), user password `open sesame`, owner password `owner128` | CC0 |
| `fixtures/psd/layered.psd`, `fixtures/psd/psb-layered.psb` | `gen psd`, `gen psb`: Photoshop files written by hand in Go (RGB 8-bit, raw channels, a photo background layer and a flat-graphic layer at an offset, with the merged composite) | CC0 |
| `fixtures/psd/no-composite.psd` | `gen psd-nocomposite`: the same layers saved like "Maximize compatibility" off (version-info resource with `hasRealMergedData = 0`, blank merged image) | CC0 |
| `fixtures/exr/rgba-half.exr`, `fixtures/exr/rgb-float.exr` | `gen exr-rgba-half`, `gen exr-rgb-float`: OpenEXR files written by hand in Go (single-part scanline, no compression; RGBA half and RGB float) from the procedural HDR scene | CC0 |
| `fixtures/hdr/scene.hdr`, `fixtures/hdr/rgbe.hdr` | `gen radiance`, `gen rgbe`: Radiance RGBE written by hand in Go (flat scanlines; `#?RADIANCE` and `#?RGBE` magic lines) | CC0 |
| `fixtures/pfm/scene.pfm` | `gen pfm`: colour PFM written by hand in Go (little endian) | CC0 |
| `fixtures/ppm/photo.ppm` | `gen ppm`: the procedural photo as a binary 8-bit PPM (P6), written in Go | CC0 |
| `fixtures/ppm/photo-ascii.ppm`, `photo-ascii.pgm`, `graphic.pbm`, `graphic-ascii.pbm` | ImageMagick 7 (`-compress none` for the ASCII P1-P3 variants; PBM thresholded at 50%) of the 64×48 photo and the 48×36 graphic | CC0 |
| `fixtures/jxr/photo.jxr`, `alpha.jxr`, `rgb16.jxr`, `gray.jxr`, `hdr.jxr` | jxrlib's `JxrEncApp` (BSD-2-Clause; built from the pinned jxrlib in the base builder, not shipped) at quality 0.9 from the 64×48 photo (24-bit BMP), the photo with alpha (32-bit TIFF), the photo at 16 bits (48-bit TIFF), its grey PGM and the 48×32 `gen radiance` scene (32bppRGBE) | CC0 |
| `fixtures/tga/alpha.tga`, `photo-rle.tga`, `pcx/photo.pcx`, `dds/photo-dxt1.dds`, `dds/alpha-dxt5.dds`, `qoi/alpha.qoi`, `sgi/photo.sgi`, `sgi/alpha.sgi`, `sun/photo.ras`, `pam/alpha.pam` | ImageMagick 7 of the 64×48 photo, with alpha where the name says (TGA RLE, DDS `dds:compression=dxt1`/`dxt5` without mipmaps) | CC0 |
| `fixtures/xbm/graphic.xbm`, `xpm/graphic.xpm`, `wbmp/graphic.wbmp` | ImageMagick 7 of the 48×36 graphic (XBM and WBMP thresholded at 50%, XPM with 8 colours) | CC0 |
| `fixtures/icns/icon.icns` | `gen icns` (own ICNS writer: a run-length coded 16×16 `is32` entry with its `s8mk` mask and a 128×128 PNG `ic07` entry of the photo with alpha) | CC0 |
| `fixtures/eps/simple.eps`, `fixtures/eps/pages.ps`, `fixtures/eps/illustrator.ai` | hand-written PostScript (`testdata/gen/eps/`, copied by `gen.sh`): an EPS with a HiRes bounding box, a three-page DSC document and an Illustrator-8-style PostScript file with an offset artboard | CC0 |
| `fixtures/eps/preview.eps` | `gen eps-preview` (own DOS EPS binary writer: a 96×72 pt PostScript drawing and a Deflate TIFF preview of the procedural graphic, `golang.org/x/image/tiff`) | CC0 |
| `fixtures/xcf/layers.xcf`, `gray.xcf`, `alpha.xcf`, `golden/codec/xcf-*-gimp.png` | GIMP 2.10.36 (GPL; run by `gen/designer.sh` in a throwaway container, not shipped) with `gen/designer-gimp.scm`: the photo scaled to 160×120 with the watermark logo as a 70 % layer, a multiply layer, a layer inside a group and a hidden layer; the same in greyscale; the logo alone on a transparent 64×48 canvas; and GIMP's flattened renders of each | CC0 |
| `fixtures/ora/layers.ora`, `fixtures/kra/layers.kra` | Krita 5.2.2 (GPL; run by `gen/designer.sh` in a throwaway container, not shipped): `fixtures/psd/layered.psd` exported to OpenRaster and Krita | CC0 |
| `fixtures/procreate/thumbnail.procreate`, `sketch/preview.sketch`, `pdn/thumbnail.pdn`, `svgz/logo.svgz` | `gen procreate`, `gen sketch`, `gen pdn`, `gen svgz` (`gen/designer.go`, Go `archive/zip`, `compress/gzip`): the applications' layout with stand-in documents around the photo or graphic PNG (Procreate QuickLook thumbnail 160×120, Sketch preview 192×144, Paint.NET header thumbnail 128×96) and the gzipped logo SVG; the public samples (playground gallery: Procreate MIT, Paint.NET Unlicense) are 1–18 MB and no Sketch sample was found | CC0 |
| `../internal/ghostscript/testdata/hostile/*.eps` | hand-written hostile PostScript for the sandbox tests (file read, `%pipe%`, endless loop, unbounded recursion, memory exhaustion, oversized page) | CC0 |
| `ssim/*.png` | `gen ssim-refs` (Go only: greyscale photo plus mean shift, contrast stretch, impulse noise, Gaussian blur σ 1.5, stdlib JPEG q15) | CC0 |
| `fixtures/dng/landscape.dng`, `fixtures/dng/portrait-orientation-6.dng` | `gen dng` (own DNG writer; the preview is Go `image/jpeg` q85 of the photo, the mosaic is the photo sampled as linear RGGB) | CC0 |
| `fixtures/dng/jxl-undecodable.dng`, `fixtures/dng/deflate-corrupt.dng` | `gen dng … 52546` / `… 8`: the landscape with its uncompressed mosaic labelled JPEG XL or deflate, so LibRaw cannot demosaic it (WP-101 preview-only and fallback paths) | CC0 |
| `fixtures/dng/jxl-landscape.dng` | `gen/jxl-dng.sh` (WP-103): `landscape.dng` rewritten by Adobe's `dng_validate -losslessJXL` (DNG SDK 1.7.1, pinned in `docker/base/versions-gpl.sh`; built and run only for this, not shipped): DNG 1.7 with lossless JPEG XL raw data and a 192×128 JPEG preview | CC0 |
| `fixtures/gpr/hero11-crop.gpr` | `gen/gpr.sh` (WP-103): a 256×192 window of the CC0 raw.pixls.us GoPro HERO11 Black sample (id 7319, `gopro-hero11.gpr` below), decoded to raw and VC-5 encoded again by GoPro's `gpr_tools` (GPR SDK, Apache-2.0 OR MIT; built and run only for this) with the sample's parameters and an empty serial number; `../internal/vips/testdata/rawfuzz/*.gpr` are libFuzzer mutations of it that crashed GoPro's VC-5 decoder before `docker/base/patches/gpr/0002` (`FuzzRawLoad` regression seeds) | CC0 |
| `fixtures/video/clip-*.{webm,mkv,mp4,mov,avi}` | `gen video-frames` (Go) encoded by `gen/video.sh` with the LGPL FFmpeg of the full base builder (libvpx VP8/VP9, libaom AV1, PNG) and the host ffmpeg's LGPL rawvideo/AVI muxer; details in `fixtures/video/README.md` | CC0 |
| `fixtures/video/bbb-h264-aac.mp4` | stream copy of Big Buck Bunny (see the exception above) plus an AAC tone (FFmpeg native encoder, LGPL) | CC-BY 3.0 (video), CC0 (audio) |
| `fixtures/video/faces-vp9.webm`, `fixtures/video/faces-av1-rotate90.mp4` | `fixtures/detect/apollo11-crew.jpg` (below) as a one-second still clip, encoded by `gen/video-faces.sh` with the LGPL FFmpeg of the full base builder (libvpx VP9; libaom AV1 stored rotated with a display matrix); details in `fixtures/video/README.md` | Public domain (NASA), like the photo |
| `fixtures/dds/bc1-dx10.dds`, `bc4.dds`, `bc7-alpha.dds`, `bc6h-scene.dds` | `gen dds-bc1`, `dds-bc4`, `dds-bc7`, `dds-bc6h` (own DX10 DDS writer and minimal BC1, BC4, BC7 mode 6 and BC6H mode 11 encoders, `gen/dds.go`) of the 64×48 photo (its luminance for BC4, with the alpha fade for BC7) and of the 48×32 HDR scene | CC0 |
| `fixtures/tiff/lab8.tif`, `lab8-alpha.tif`, `lab8-icc.tif`, `lab16.tif` | `gen lab-tiff8`, `lab-tiff8-alpha`, `lab-tiff8-icc`, `lab-tiff16` (own TIFF writer, `gen/labtiff.go`): the 64×48 photo as CIE L\*a\*b\* (D50), 8-bit, 8-bit with the alpha fade, 8-bit with an identity L\*a\*b\* ICC profile written in Go, and 16-bit | CC0 |
| `fixtures/jxl/cmyk.jxl`, `cmyk-alpha.jxl` | `gen cmyk-raw` (naive CMYK of the 64×48 photo and a CMYK ICC profile written in Go, `gen/cmyk.go`) encoded losslessly by `gen/jxlcmyk/encode.c` (libjxl, BSD-3-Clause; K as a black extra channel, alpha fade in the second) | CC0 |
| `fixtures/jpeg/rgb12.jpg`, `grey12-progressive.jpg`, `rgb12-exif6-icc.jpg`, `lossless16.jpg`, `lossless10-grey.jpg`, `lossless8.jpg`, `lossless6.jpg` | `gen photo16` (the 64×48 photo at 16 bits; greyscale and rotated 90° CCW copies by libvips) encoded by libjpeg-turbo's `cjpeg` (IJG/BSD-3-Clause, built from the base's pinned release by `gen/jpeghp.sh` in a throwaway container, not shipped): 12-bit DCT q95 (colour 4:2:0, grey progressive, the rotated copy q90 with libvips' built-in CC0 `srgb` profile and then `gen exif6`) and lossless at 16, 10, 8 and 6 bits | CC0 |
| `fixtures/jpeg/cmyk12.jpg`, `lossless16-cmyk.jpg`, `lossless12-ycc.jpg` | `gen cmyk-raw` (as for `jxl/cmyk.jxl`) and `gen photo16` encoded by `gen/jpeghp/encode.c` (libjpeg-turbo): 12-bit DCT and 16-bit lossless Adobe-style CMYK with the CMYK profile, 12-bit lossless YCbCr | CC0 |
| `golden/codec/jpeg-*-djpeg.ppm`, `jpeg-*-djpeg.pgm` | libjpeg-turbo's `djpeg` decodes of the high-precision fixtures above at their precision (`gen/jpeghp.sh`; reference decodes, not written by tests) | CC0 |
| `fixtures/ktx2/bc1.ktx2`, `bc4.ktx2`, `bc6h.ktx2`, `bc7.ktx2`, `rgba8.ktx2`, `rgb8.ktx2`, `rgba8-zlib.ktx2` | `gen ktx2-*` (WP-108, own KTX 2.0 writer, `gen/ktx2.go`): the DDS fixtures' BC1/BC4/BC6H/BC7 encoders and the raw pixels of the 64×48 photo (with the alpha fade for BC7 and RGBA8), uncompressed or zlib-supercompressed | CC0 |
| `fixtures/ktx2/rgba8-zstd.ktx2` | `gen ktx2-rgba8-level` compressed by the `zstd` CLI (`-19 --no-check`, BSD-3-Clause; run only for this), then `gen ktx2-rgba8-zstd` | CC0 |
| `fixtures/ktx2/etc1s.ktx2`, `uastc-zstd.ktx2`, `uastc-hdr.ktx2` | the `basisu` 2.50 tool (Basis Universal, Apache-2.0; built from the pinned `BASISU_COMMIT` in a throwaway container, run only for this): `-ktx2` ETC1S of the 64×48 photo, `-ktx2 -uastc` (Zstandard) of the photo with alpha, `-ktx2 -hdr` (UASTC HDR 4×4) of `fixtures/exr/rgb-float.exr` | CC0 |
| `fixtures/fits/u8.fits`, `u16.fits`, `f32-nan.fits`, `f64.fits`, `rgb16.fits`, `mef-i32.fits` | `gen fits 64 48` (WP-108, own FITS writer, `gen/fits.go`) of the 64×48 photo: luminance as 8-bit, unsigned 16-bit (BZERO 32768), 32-bit float with a 4×4 NaN block, 64-bit float, 16-bit 3-plane RGB, and an empty primary + binary table + int32 IMAGE extension with BLANK pixels | CC0 |
| `fixtures/fits/rice-u16.fits.fz`, `rice-f32.fits.fz`, `gzip2-f32.fits.fz`, `dither2-f32.fits.fz`, `gzip1-mef-i32.fits.fz`, `hcompress-u16.fits.fz`, `rice-f32.ref.fits`, `gzip2-f32.ref.fits`, `dither2-f32.ref.fits` | cfitsio 4.03 `fpack` (`-r`, `-g2`, `-r -qz 4`, `-g1`, `-h`) of the fixtures above and `funpack` references, `gen/fits-fpack.sh` (Docker `ubuntu:noble`, `libcfitsio-bin`; run only for this) | CC0 |
| `fixtures/dicom/ct-window.dcm`, `ct-rle.dcm`, `ct-bigendian.dcm` | `gen dicom` (WP-108, own DICOM writer, `gen/dicom.go`): a synthetic CT-like phantom, 16-bit; explicit VR LE 12-of-16 bits with noise in the high bits, intercept −1024, window 40/400; RLE Lossless; explicit VR big endian signed HU without a window; made-up patient elements carrying `XIPRIVACYMARKER` (never real patient data) | CC0 |
| `fixtures/dicom/mono1-implicit.dcm`, `grey-2frames.dcm`, `palette.dcm`, `rgb-planar.dcm`, `rgb-deflated.dcm`, `rgb-rle.dcm` | `gen dicom`: the 64×48 photo as MONOCHROME1 implicit VR, two native frames, PALETTE COLOR with 16-bit heat-map tables, RGB planar, deflated and RLE | CC0 |
| `fixtures/dicom/jpeg-baseline.dcm`, `jpeg-2frames-bot.dcm` | `gen dicom`: Go `image/jpeg` of the photo as YBR_FULL_422, split into two fragments; two frames with a Basic Offset Table | CC0 |
| `fixtures/dicom/ct-j2k.dcm` | `gen dicom-phantom`, then libvips `jp2ksave --lossless` (OpenJPEG, BSD-2-Clause), then `gen dicom` wraps its `jp2c` codestream (JPEG 2000 lossless transfer syntax, 1.2.840.10008.1.2.4.90) | CC0 |
| `fixtures/pict/photo.pct` | ImageMagick 7 `PICT:` (version 2, 72 DPI, 512-byte header) of the 64×48 photo | CC0 |
| `fixtures/mng/png.mng`, `palette.mng`, `frames.mng`, `jng.mng` | `gen mng mng-*` (WP-108, own MNG writer around Go `image/png`/`image/jpeg` of the photo or graphic, `gen/mng.go`): one PNG frame; a global PLTE with an empty embedded PLTE; two frames with FRAM/DEFI; a JNG frame with IDAT alpha | CC0 |
| `fixtures/wmf/shapes.wmf`, `fixtures/emf/shapes.emf` | `gen metafile` (WP-108, own WMF and EMF writers, `gen/metafile.go`): the same 200×150 scene (blue background, orange ellipse, green rectangle, dark outlines) as a placeable WMF and an EMF | CC0 |
| `fixtures/jng/jng.jng`, `jng-idat.jng`, `jng-jdaa.jng` | `gen mng jng*` (own JNG writer: Go `image/jpeg` of the photo in two JDAT chunks; without alpha, with 8-bit PNG-compressed alpha, with JPEG alpha) | CC0 |
| `golden/codec/*.png` | `golden/codec/reference.py`: the CIE L\*a\*b\* fixtures converted by the CIE formulas, the CMYK JPEG XL samples through lcms and the DDS fixtures through Pillow (reference decodes, not written by tests) | CC0 |
| `../internal/codec/dds/testdata/bcdec/*.bin` | pseudo-random blocks and their decodes by bcdec (MIT; not vendored), written by the generator in `bcdec/gen/` | CC0 |
| `golden/**` | written by tests with `GOLDEN_UPDATE=1` from the fixtures above and the RAW samples below | CC0 |
| `detect/expected.json`, `info/detect-*.json` | written by `detect/reference.py` and by tests from the detection photos below | CC0 |
| `detect/reference.py` | ours, plus the YOLOX demo pre/post-processing functions copied from Megvii's YOLOX (Apache-2.0, see NOTICE) | CC0 / Apache-2.0 |
| `classify/expected.json`, `classify/reference.py`, `info/classify-*.json` | ours (the `info` goldens are written by tests from the classification photos): the reference top 5 of the classification photos (timm's evaluation transform reimplemented in Pillow/NumPy, onnxruntime) | CC0 |

## Detection photos (committed, third-party, public domain)

Face and object detection (WP-71) needs real photographs: synthetic scenes do not exercise a
trained detector. These three come from Wikimedia Commons, where each is marked public domain
(US federal government work) or CC0; they were downloaded as the 960 px Commons thumbnails on
2026-09-26 and re-encoded by `vips thumbnail 640` + `jpegsave Q82 --keep none` (no metadata).
No attribution is required; the authors are listed as a courtesy.

| File | Source | Author | Licence | SHA-256 |
|---|---|---|---|---|
| `fixtures/detect/apollo11-crew.jpg` | [File:Apollo 11 Crew.jpg](https://commons.wikimedia.org/wiki/File:Apollo_11_Crew.jpg) (NASA, 1969) | NASA | Public domain (NASA) | `f95f2b8dcbbd79d4635a823a7e4425511036fc34f68902eea2ac068b20b51965` |
| `fixtures/detect/astronaut-dogs.jpg` | [File:NASA astronaut Leland D. Melvin with his dogs Jake and Scout.jpg](https://commons.wikimedia.org/wiki/File:NASA_astronaut_Leland_D._Melvin_with_his_dogs_Jake_and_Scout.jpg) (2009) | Robert Markowitz, NASA | Public domain (NASA) | `5d02c6afa3e401bb8a6e1fa677e47e38645a5c1923717a6212b55fa297000551` |
| `fixtures/detect/cat-laptop.jpg` | [File:Cat on laptop yawning.jpg](https://commons.wikimedia.org/wiki/File:Cat_on_laptop_yawning.jpg) (from Pixabay, 2014) | acebrand | CC0 1.0 | `256422d01e8bbd8e91650cb0015bfec79043a8c9eb3f3ce386a67fe31ce23a71` |

## Classification photos (committed, third-party, public domain)

Image classification (WP-110) is checked on the three detection photos above and on these seven,
each of one ImageNet class. Same origin and processing as the detection photos: Wikimedia
Commons files marked public domain (US federal government work) or CC0, downloaded as the
960 px Commons thumbnails on 2026-09-28 and re-encoded by `vips thumbnail 640` +
`jpegsave Q82 --keep none`. No attribution is required; the authors are listed as a courtesy.

| File | Source | Author | Licence | SHA-256 |
|---|---|---|---|---|
| `fixtures/classify/bald-eagle.jpg` | [File:Haliaeetus leucocephalus-tree-USFWS.jpg](https://commons.wikimedia.org/wiki/File:Haliaeetus_leucocephalus-tree-USFWS.jpg) (2005) | Steve Hillebrand, USFWS | Public domain (USFWS) | `46bfbcca257251d032c8d55be0a418252782f3379dceb73aaca301f072e87dd3` |
| `fixtures/classify/banana.jpg` | [File:Banana-1206002 960 720.jpg](https://commons.wikimedia.org/wiki/File:Banana-1206002_960_720.jpg) (from Pixabay, 2015) | Robfoto | CC0 1.0 | `6fd44259d12317863d00a026ef046b8207fa9403c38bfe1958838096aee53e8c` |
| `fixtures/classify/golden-retriever.jpg` | [File:Golden-retriever-1827903 1920.jpg](https://commons.wikimedia.org/wiki/File:Golden-retriever-1827903_1920.jpg) (from Pixabay, 2016) | RetyiRetyi | CC0 1.0 | `97a7afe7d77e045bd90eb441e5e848721e38f7cc503cb10ceabfca164bf5efc4` |
| `fixtures/classify/monarch.jpg` | [File:Monarch butterfly on butterfly milkweed (53069280718).jpg](https://commons.wikimedia.org/wiki/File:Monarch_butterfly_on_butterfly_milkweed_(53069280718).jpg) (2023) | USFWS Midwest Region | Public domain (USFWS) | `87b7006d881d31934c8c6faba476bcd9fe5040f6cb2567454048eea6e1c76ca4` |
| `fixtures/classify/pizza.jpg` | [File:Pizza-3007395.jpg](https://commons.wikimedia.org/wiki/File:Pizza-3007395.jpg) (from Pixabay, 2013) | igorovsyannykov | CC0 1.0 | `221851816d4794191eb967a5725616b1a40b57b616186f43c7709658c3d276ec` |
| `fixtures/classify/red-fox.jpg` | [File:Red fox (8385104980).jpg](https://commons.wikimedia.org/wiki/File:Red_fox_(8385104980).jpg) (2012) | USFWS Alaska | Public domain (USFWS) | `840d1a226e83be95aa59516ffda61b0e2f132902e71c6db04fd21ae520e57964` |
| `fixtures/classify/space-shuttle.jpg` | [File:Columbia.sts-1.01.jpg](https://commons.wikimedia.org/wiki/File:Columbia.sts-1.01.jpg) (STS-1, 1981) | NASA/KSC | Public domain (NASA) | `ada835659b720439df5871fc70f757063a8943799403c05cf8cb7c9c860e6571` |

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

The WP-101/102/103 samples (`rawfixtures.NetworkFiles`, used only by `-tags network` tests), CC0
on raw.pixls.us as well (checked 2026-09-27):

| Cache name | raw.pixls.us file | SHA-256 | Size |
|---|---|---|---|
| `nikon-z50ii-he.nef` | Nikon - Z50_2 - 8bit compressed (3:2).NEF (id 7764; High Efficiency) | `71c12d398e6f5a01e0e32c2211c40cafdb29e85ba1e17c4f65e7a4e958754c54` | 12.4 MB |
| `nikon-z8-he.nef` | Nikon - Z 8 - 8bit 8bit compressed (3:2).NEF (id 6616; High Efficiency) | `82df041542b8d328738443bb5ff3373b396e406ecc8cbe7dc3b35252790c53e7` | 24.4 MB |
| `nikon-zf-he.nef` | Nikon - Z f - 8bit 8bit lossy compressed (3:2).NEF (id 6887; High Efficiency) | `98d6ca8e6c98048ca7ffed68ccaeda7b2b9f03807f0d320d97d5678db21748c2` | 13.5 MB |
| `sony-ilce-7m5.arw` | Sony - ILCE-7M5 - 14bit (3:2).ARW (id 8849) | `c16cae99d26a2f8ad183d31a23823310a994aa058d55cfe56e4f82ba56865f57` | 11.5 MB |
| `fujifilm-gfx100rf.raf` | Fujifilm - GFX100RF - 16bit compressed (4:3).RAF (id 8091) | `14bed5ac8191d6f2a7c82984e3bf6470ad4740df329293eac42b8018cc6a668a` | 72.8 MB |
| `hasselblad-x1d-ii.fff` | Hasselblad - X1D II 50C - 16bit (4:3).fff (id 3874) | `8c2451b3478fb529b4e9bd51ed3267999ce26eb1fcb5c3b93c3ae7bc56267794` | 78.2 MB |
| `google-pixel-8-pro.dng` | Google - Pixel 8 Pro - 16bit (4:3).dng (id 7751) | `45420c595401547cca950ae58d552bc82b32d31ce1d58c082df33004016197f5` | 45.1 MB |
| `sigma-sd14.x3f` | Sigma - SIGMA SD14 - 3:2.X3F (id 4462) | `c32f247eb372c3e66ad3b433518ec11f898fdc317c8432ebe025b46d4917bcf3` | 12.2 MB |
| `gopro-hero11.gpr` | GoPro - HERO11 Black - 16bit (5:4).GPR (id 7319) | `d7a6be163c847cb8456e6858a99aef3b85dc3d8836bca1da1b8886effe0d43e1` | 13.9 MB |
| `leaf-aptus-22.mos` | Leaf - Aptus 22 - 16bit (4:3).mos (id 3196; WP-108: no Make in IFD0) | `8a1a101cb3940b0648af54a721d92e6fb8612dd29a0909036ea2753196a00ec2` | 23.4 MB |

## libavif samples

Copied unchanged from libavif's `tests/data` (https://github.com/AOMediaCodec/libavif, commit on
`main` as of 2026-09-28), whose README gives each of them the libavif licence (BSD-2-Clause):

| File | libavif file | What it exercises |
|---|---|---|
| `fixtures/avif/libavif-colors-animated-8bpc.avif` | `colors-animated-8bpc.avif` | a real-world `avis` sequence (ffmpeg colours, 5 frames at 30 fps) |
| `fixtures/gainmap/libavif-seine_sdr_gainmap_srgb.avif` | `seine_sdr_gainmap_srgb.avif` | SDR base + ISO 21496-1 `tmap` gain map (the gallery sample) |
| `fixtures/gainmap/libavif-color_grid_gainmap_different_grid.avif` | `color_grid_gainmap_different_grid.avif` | `tmap` with a grid base and a grid gain map |
| `fixtures/gainmap/libavif-color_nogrid_alpha_nogrid_gainmap_grid.avif` | `color_nogrid_alpha_nogrid_gainmap_grid.avif` | `tmap` whose base is the HDR rendition, with alpha |
| `fixtures/gainmap/libavif-unsupported_gainmap_version.avif` | `unsupported_gainmap_version.avif` | `tmap` version 99 (must be ignored) |
| `fixtures/gainmap/libavif-paris_exif_xmp_gainmap_bigendian.jpg` | `paris_exif_xmp_gainmap_bigendian.jpg` | Adobe gain-map JPEG (MPF + hdrgm XMP) that libultrahdr does not read |

The notice those files come with:

```
Copyright 2019 Joe Drago. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
this list of conditions and the following disclaimer in the documentation
and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

The goldens rendered from them (`golden/exec/gainmap-tmap-*`, `gainmap-adobe-*`,
`avif-colors-animated-webp`) and the `/info` answers about them (`info/gainmap-*`,
`info/avif-libavif-*`) are derived from these files under the same licence.

## Cross-language test vectors

`security/vectors.json` repeats the public signing and encryption test vectors already used by the
Go tests: the imgproxy documentation's "Signing the URL" example, `examples/signature*` and
`examples/encrypted_source_url.*`, and `security/signature_test.go` of imgproxy (Apache-2.0,
credited in `NOTICE`), plus vectors computed by this repository. They are keys, paths and
expected outputs, not code. `urls/imgproxy-builder.json` is generated by this repository's tests
(CC0).
