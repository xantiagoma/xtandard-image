# testdata

Shared test inputs and golden outputs for the whole module. Go tooling ignores directories named
`testdata`, so nothing here is built or linted. Licences: `LICENSES.md` (everything is CC0).

## Layout

```
testdata/
  fixtures/<format>/…   one small file per input format and variant (inputs; never rewritten by tests)
  cache/raw/            real camera RAW samples downloaded by internal/testutil/rawfixtures (gitignored)
  golden/<case>.png     expected outputs, compared perceptually by internal/testutil/golden
  info/<case>.json      expected /info responses (internal/app TestInfo; GOLDEN_UPDATE=1 rewrites, review the diff)
  info/pro/<fixture>-<option>.json  expected /info Pro option responses (WP-63) for jpeg/baseline, png/rgba8 and gif/animated
  ssim/                 greyscale reference + distortions validating internal/testutil/ssim
  security/vectors.json signing (imgproxy, xi_sig) and enc/ encryption vectors shared by Go
                        (internal/security/{sign,crypt}) and TypeScript (packages/image-url)
  urls/imgproxy-builder.json  cross-language fixture of the TypeScript URL builder: cases derived
                        from the imgproxy golden tables, URLs built by packages/image-url, parsed
                        back by internal/dialect/imgproxy (task ts:fixtures regenerates it)
  detect/               expected detections on fixtures/detect (expected.json) and the script that made them
  gen/                  generator: main.go (Go), gen.sh (driver), logo.svg; video.go + video.sh (video fixtures, full base builder);
                        designer.go (Procreate, Sketch, Paint.NET, SVGZ) and designer.sh (GIMP/Krita in Docker: XCF, ORA, KRA)
  LICENSES.md           origin and licence of every file
```

### Fixtures

| Path | Format | What it exercises |
|---|---|---|
| `jpeg/exif-orientation-6-icc.jpg` | JPEG 240x320 | Exif Orientation 6 (stored rotated; upright after autorot 320x240), embedded sRGB ICC |
| `jpeg/metadata-p3.jpg` | JPEG 120x160 | Display P3 ICC, Exif (orientation 6, Make/Model, Artist, Copyright, DateTimeOriginal, GPS IFD), XMP with `exif:GPS*`, IPTC copyright: the metadata policy matrix |
| `jpeg/progressive.jpg` | JPEG 320x240 | progressive (interlaced) scans |
| `jpeg/baseline.jpg` | JPEG 320x240 | plain baseline photo, the default "photo" input |
| `png/rgba8.png` | PNG 128x96 | 8-bit RGBA with graded alpha |
| `png/bordered.png` | PNG 80x60 | 56x42 photo on white, margins top 6, right 20, bottom 12, left 4 (trim, equal_hor/equal_ver) |
| `png/rgb16.png` | PNG 96x72 | 16 bits per channel |
| `png/palette.png` | PNG 96x72 | 8-bit palette (PLTE) |
| `watermark/logo.png` | PNG 48x32 | RGBA logo used as the configured watermark (translucent plate, opaque parts, half-transparent rim) |
| `watermark/badge.png` | PNG 40x40 | RGBA badge used as a per-request watermark (`watermark_url`, dialect overlays): teal disc, white "L" |
| `gif/static.gif`, `gif/animated.gif` | GIF 96x72 | single frame; 6 frames, 100 ms, infinite loop |
| `webp/lossy.webp`, `webp/lossless.webp`, `webp/animated.webp` | WebP | VP8, VP8L, animated (6 frames, alpha) |
| `avif/photo.avif` | AVIF 320x240 | 8-bit AV1 still |
| `heic/photo.heic` | HEIC 320x240 | HEVC Main still |
| `heic/thumbnail.heic`, `avif/thumbnail.avif` | HEIC / AVIF 320x240 | the photo with an embedded 80x60 thumbnail of a *different* image (the graphic), so tests can tell which one was decoded (`enforce_thumbnail`) |
| `jxl/photo.jxl` | JPEG XL 320x240 | container, lossy |
| `jp2/photo.jp2` | JPEG 2000 160x120 | JP2 container |
| `tiff/multipage.tif` | TIFF | 2 pages (160x120 photo, 96x72 graphic), deflate |
| `bmp/rgb24.bmp`, `bmp/rle8.bmp` | BMP 96x72 | 24-bit BI_RGB; 8-bit BI_RLE8 |
| `ico/png-entry.ico`, `ico/bmp-entry.ico` | ICO | 64x64 PNG entry; 32x32 32-bit DIB entry with AND mask |
| `svg/logo.svg` | SVG 96x72 | gradient, paths, opacity, text |
| `uhdr/photo-ultrahdr.jpg` | UltraHDR JPEG 320x240 | JPEG with a libultrahdr gain map (MPF secondary image) |
| `dng/landscape.dng` | DNG 192x128 | synthetic camera RAW: 16-bit RGGB mosaic in a SubIFD, 96x64 JPEG preview in IFD0, camera EXIF (Make, Model, exposure, ISO, lens, date) |
| `dng/portrait-orientation-6.dng` | DNG 192x128 stored, shown 128x192 | same, Orientation 6 (mosaic and 48x32 preview stored rotated 90° CCW) |
| `exr/rgba-half.exr`, `exr/rgb-float.exr` | OpenEXR 80x60, 48x32 | scene-linear HDR (sun at 40): RGBA half with an alpha fade in the bottom quarter; RGB float without alpha |
| `hdr/scene.hdr`, `hdr/rgbe.hdr` | Radiance 96x64, 48x32 | the same scene as RGBE, `#?RADIANCE` and `#?RGBE` headers |
| `pfm/scene.pfm` | PFM 64x48 | the same scene as float RGB |
| `ppm/photo.ppm` | PPM 96x64 | 8-bit binary P6 photo (not HDR) |
| `xcf/layers.xcf`, `xcf/gray.xcf` | GIMP XCF 160x120 | GIMP 2.10 (v011, RLE): photo, 70 % logo at (60, 40), multiply logo, a group with one logo, a hidden logo; RGB and greyscale. Flattened by `codec.FlattenXCF`, compared with GIMP's render (`golden/codec/xcf-*-gimp.png`) |
| `xcf/alpha.xcf` | GIMP XCF 64x48 | one 48x32 logo layer at (8, 8) on a transparent canvas |
| `ora/layers.ora`, `kra/layers.kra` | OpenRaster, Krita 96x72 | Krita 5.2 exports of `psd/layered.psd`: `mergedimage.png`, layers, thumbnail |
| `procreate/thumbnail.procreate` | Procreate (ZIP) | stand-in document with a 160x120 `QuickLook/Thumbnail.png` |
| `sketch/preview.sketch` | Sketch (ZIP) | stand-in document with a 192x144 `previews/preview.png` |
| `pdn/thumbnail.pdn` | Paint.NET | `PDN3` XML header with a 128x96 base64 PNG thumbnail, stand-in body |
| `svgz/logo.svgz` | SVGZ | the gzipped `svg/logo.svg` |
| `detect/apollo11-crew.jpg`, `detect/astronaut-dogs.jpg`, `detect/cat-laptop.jpg` | JPEG 640 px wide | real photographs (public domain / CC0, `LICENSES.md` "Detection photos") for face and object detection (WP-71): three faces and three people; a face, a person and two dogs; a cat on a laptop. Expected boxes: `detect/expected.json` |

### Detection expectations

`detect/expected.json` holds the detections of the reference implementations on the
`fixtures/detect/` photos: OpenCV's `cv2.FaceDetectorYN` (YuNet, score ≥ 0.6, NMS 0.3) and the
official YOLOX ONNXRuntime demo post-processing (class-aware NMS 0.45; objects with score ≥ 0.5
kept). `internal/detect` must find each of them with IoU ≥ 0.5 (ROADMAP WP-71). Regenerate it
with `detect/reference.py` after changing a photo or a model (it prints the raw JSON; the
committed file keeps the objects with score ≥ 0.5):

```sh
docker run --rm -v "$PWD:/w" -w /w python:3.12-slim sh -c \
  'pip install -q onnxruntime==1.23.2 opencv-python-headless numpy &&
   python testdata/detect/reference.py <yunet.onnx> <yolox_s.onnx> testdata/fixtures/detect/*.jpg'
```

The models are the ones in the `full` base image (`/usr/share/xtandard/models`). The last run
used onnxruntime 1.23.2, OpenCV 5.0.0 and NumPy 2.5.3. The left dog in `astronaut-dogs.jpg` is
reported as a horse by YOLOX-s; the expectation records what the model says.

### Camera RAW samples (downloaded)

Real RAW files are 5–18 MB, so they are not committed. `internal/testutil/rawfixtures` downloads
each once from raw.pixls.us (pinned URL and SHA-256), caches it in `testdata/cache/raw/`
(gitignored) and skips the test when it is not cached and the network is unavailable
(`XI_TEST_OFFLINE=1` skips without trying). The synthetic DNGs above keep every RAW code path
covered offline. The goldens rendered from the samples (`golden/exec/raw-<kind>.png`) are
committed.

Keep each file small (target ≤ 50 KB, hard limit 200 KB). Add new variants (CMYK JPEG, APNG,
10-bit AVIF, malicious SVGs, …) through `gen/gen.sh`, and add a row to `LICENSES.md` in the same
commit. Third-party files are allowed only under CC0/public domain or a permissive licence that
allows redistribution; never GPL or CC-BY-SA.

## Goldens

`golden.Compare(t, "<pkg>/<case>", got, golden.Options{})` decodes `golden/<pkg>/<case>.png`
and fails when the DSSIM between it and `got` exceeds the tolerance (default
`golden.DefaultTolerance` = 0.015: a JPEG q85 re-encode passes, a 1 px shift fails). Encoded
bytes are never compared. Use `golden.Fixture(t, "jpeg/baseline.jpg")` to locate inputs from any
package.

Update flow:

```sh
GOLDEN_UPDATE=1 go test ./internal/pipeline/exec/...   # rewrite goldens for these packages
go test ./internal/pipeline/exec -update               # same, flag form (only for packages importing golden)
git diff --stat testdata/golden                        # review: open the changed PNGs before committing
```

`go test ./... -args -update` fails in packages that do not import `golden` (unknown flag), so use
the environment variable for broad updates. The update run logs the previous DSSIM of every golden
it rewrites, which is the number to sanity-check.

On failure the harness prints the score and writes `<case>.got.png`, `<case>.want.png` and
`<case>.diff.png` (DSSIM heat map: black = same, red → yellow → white = increasingly different)
to `$GOLDEN_OUT` when set, else to `t.ArtifactDir()` (keep them with `go test -artifacts`).

## Regenerating fixtures

```sh
sh testdata/gen/gen.sh
```

Needs Go, libvips ≥ 8.18 with heif/jxl/jp2k/uhdr savers, ImageMagick 7 and macOS `sips` (HEIC).
Pixels are deterministic; encoder upgrades may change bytes, which the perceptual goldens tolerate.
If `ssim/` changes, recompute the scikit-image reference values quoted in
`internal/testutil/ssim/ssim_test.go` with the command in that test's comment.
