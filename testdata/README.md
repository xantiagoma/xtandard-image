# testdata

Shared test inputs and golden outputs for the whole module. Go tooling ignores directories named
`testdata`, so nothing here is built or linted. Licences: `LICENSES.md` (everything is CC0).

## Layout

```
testdata/
  fixtures/<format>/…   one small file per M1 input format and variant (inputs; never rewritten by tests)
  golden/<case>.png     expected outputs, compared perceptually by internal/testutil/golden
  info/<case>.json      expected /info responses (internal/app TestInfo; GOLDEN_UPDATE=1 rewrites, review the diff)
  ssim/                 greyscale reference + distortions validating internal/testutil/ssim
  gen/                  generator: main.go (Go), gen.sh (driver), logo.svg
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
| `gif/static.gif`, `gif/animated.gif` | GIF 96x72 | single frame; 6 frames, 100 ms, infinite loop |
| `webp/lossy.webp`, `webp/lossless.webp`, `webp/animated.webp` | WebP | VP8, VP8L, animated (6 frames, alpha) |
| `avif/photo.avif` | AVIF 320x240 | 8-bit AV1 still |
| `heic/photo.heic` | HEIC 320x240 | HEVC Main still |
| `jxl/photo.jxl` | JPEG XL 320x240 | container, lossy |
| `jp2/photo.jp2` | JPEG 2000 160x120 | JP2 container |
| `tiff/multipage.tif` | TIFF | 2 pages (160x120 photo, 96x72 graphic), deflate |
| `bmp/rgb24.bmp`, `bmp/rle8.bmp` | BMP 96x72 | 24-bit BI_RGB; 8-bit BI_RLE8 |
| `ico/png-entry.ico`, `ico/bmp-entry.ico` | ICO | 64x64 PNG entry; 32x32 32-bit DIB entry with AND mask |
| `svg/logo.svg` | SVG 96x72 | gradient, paths, opacity, text |
| `uhdr/photo-ultrahdr.jpg` | UltraHDR JPEG 320x240 | JPEG with a libultrahdr gain map (MPF secondary image) |

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
