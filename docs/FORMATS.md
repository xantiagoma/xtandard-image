# Formats

libvips 8.18.x is the engine; library choices and licenses are in `docs/research/stack.md` and ADR 0006. Format detection is **always by magic bytes** (`codec.Sniff`), never by extension or upstream `Content-Type`. Every loader runs behind the probe stage, which reads only headers to enforce pixel, frame and page limits before any pixel decode.

## 1. Input formats

| Format | Loader | Anim / pages | Shrink-on-load | Metadata | Notes | Milestone |
|---|---|---|---|---|---|---|
| JPEG | libjpeg-turbo (`jpegload`) | — | `shrink` 2/4/8 | EXIF, ICC, XMP, IPTC | CMYK → sRGB via embedded or built-in profile; `fail_on=warning` rejects truncated files; UltraHDR gain maps via libultrahdr (`uhdrload`) when `preserve_hdr` | M1 |
| PNG / APNG | libspng (`pngload`) | APNG: first frame only (libvips limitation) | — | eXIf, iCCP, tEXt | 16-bit kept only with `preserve_hdr`; zlib bomb guarded by pixel limit + libspng chunk limits | M1 |
| GIF | libnsgif (`gifload`) | yes | — | — | frame count checked from header; all frames loaded with `n=-1` | M1 |
| WebP | libwebp (`webpload`) | yes | `scale` | EXIF, ICC, XMP | | M1 |
| AVIF | libheif + dav1d (`heifload`) | image sequences: first frame (libheif limitation, revisit) | embedded `thumbnail` when `enforce_thumbnail` | EXIF, ICC, XMP | 10/12-bit → 8-bit unless `preserve_hdr` | M1 |
| HEIC / HEIF | libheif + libde265 | multi-image: `page` | embedded thumbnail | EXIF, ICC, XMP | | M1 |
| JPEG XL | libjxl (`jxlload`) | yes | — | EXIF, ICC, XMP | | M1 |
| TIFF | libtiff (`tiffload`) | pages | pyramid `subifd` level | EXIF, ICC, XMP, IPTC | reject tiles/strips exceeding pixel limit | M1 |
| BMP | own pure-Go loader (`codec/bmp`, reimplemented from imgproxy's C `bmpload.c`, Apache-2.0: upstream header kept, `NOTICE` entry) | — | — | — | 1/4/8/16/24/32-bit, RLE4/8, bitfields; decoded in Go into a vips memory image | M1 |
| ICO / CUR | own loader (`codec/ico`) | picks best entry | picks the smallest entry ≥ target | — | PNG-in-ICO entries go through `pngload` | M1 |
| SVG | librsvg (`svgload`) after `security/svg` sanitiser | — | vector: rendered at target scale (`scale`/`dpi`, 96 DPI base) | — | passthrough when output is SVG and no raster op requested; `style` option injects CSS | M1 |
| PDF | PDFium (`pdfload`) | pages (`page`, `pages`) | `scale`/`dpi` so page renders at target size | — | page count checked at probe; password-protected → 422 | M2 |
| PSD / PSB | ImageMagick 7 (`magickload`, PSD coder only) | layers ignored | — | ICC | strategy: 1) if the file carries a merged composite (PSD "Image Data" section, present when "Maximize compatibility" was on) read that via magick with `-define psd:...` composite only; 2) else render composite via magick; hard 10 s / 256 MiB magick limits | M2 |
| Camera RAW (CR2, CR3, CRW, NEF, NRW, ARW, SRF, SR2, DNG, ORF, RW2, RAF, PEF, SRW, X3F, 3FR, IIQ, …) | LibRaw 0.22.x | — | embedded preview | EXIF from LibRaw | see §2 | M2 |
| JPEG 2000 | OpenJPEG (`jp2kload`; added to the base image by WP-05, not in imgproxy-docker-base) | — | `page` pyramid level | — | | M1 |
| OpenEXR, Radiance HDR, PPM/PFM | OpenEXR (base extras, WP-66) / libvips native | — | — | — | tone-mapped to 8-bit sRGB unless `preserve_hdr` | M2 |
| Video (MP4, MOV, WebM, MKV, animated MP4 GIF-likes) | FFmpeg 8 LGPL via go-astiav (`full` image only) | frames | decoder scaling | duration, streams (`/info` `vm`) | frame at `vts` or best frame by histogram variance among keyframes | M3 |
| Blocked | FITS, Matlab, NIfTI, Analyze, CSV/matrix, raw pixels, magick for anything except PSD/PSB | | | | `vips_block_untrusted_set(TRUE)` and operation block list | — |

**Base image availability (WP-66, base `8.18.6-r2-slim`).** The slim base image now ships the M2 loaders: `dcrawload` (LibRaw 0.22.x, `libraw_r`, headers in the builder for `codec/raw`), `pdfload` (PDFium, never poppler), `openexrload` (OpenEXR 3.4 + Imath) and `magickload` (ImageMagick 7, load only, as the `vips-magick.so` module). ImageMagick's `/opt/xtandard/etc/ImageMagick-7/policy.xml` registers and allows only the PSD module (PSD/PSB read) and caps each magick image at 10 s, 256 MiB memory/map, no disk cache, 64 MP area, 16K width/height (`docker/base/README.md`). Because libvips' generic loader dispatch now considers `magickload`, any file that reaches libvips without a native loader (for example BMP or ICO bytes passed straight to `vips_image_new_from_buffer`) fails in `magickload` with a policy error instead of "not a known format"; the app still routes BMP/ICO to its own loaders and gates M2 formats in `codec.Sniff`. `dcrawload`, `openexrload` and `magickload` are libvips `UNTRUSTED` operations: the WP that enables `vips_block_untrusted_set(TRUE)` must unblock them by name.

### 1.1 Detection and probe (`codec.Sniff`, `codec.Probe`)

- Magic bytes: JPEG `FF D8 FF`; PNG signature; `GIF87a`/`GIF89a`; `RIFF….WEBP`; JPEG XL codestream `FF 0A` or container box `JXL `; JPEG 2000 `jP  ` signature box or raw J2K codestream `FF 4F FF 51`; TIFF `II*\0`/`MM\0*` and BigTIFF `II+\0`/`MM\0+`; BMP `BM` + known info-header length; ICO/CUR (reserved 0, type 1/2, sane first entry); SVG via `security/svg.IsSVG` (first 4 KiB); `%PDF-`; `8BPS` v1/v2 (PSD/PSB); RAW signatures (CR2, CR3 `ftypcrx`, CRW, RAF, ORF, RW2, X3F, MRW). TIFF-based RAWs without their own signature (NEF, ARW, DNG, PEF, SRW) sniff as TIFF.
- ISOBMFF: major brand first, then compatible brands inside the `ftyp` box: `avif`/`avis` → AVIF; `heic`/`heix`/`hevc`/`heim`/`heis`/`hevm`/`hevs` → HEIC; generic `mif1`/`msf1`/`miaf` → HEIF, refined to AVIF/HEIC by the loader's `heif-compression`. libvips' `heifload` only accepts a fixed list of *major* brands (no `avis`), so an AVIF sequence whose major brand is `avis` is sniffed but refused as unsupported by the loader.
- UltraHDR: a JPEG whose header (before SOS) has an APP2 `MPF` segment **and** gain-map metadata (APP2 ISO 21496-1 `urn:iso:std:iso:ts:21496:-1` or APP1 XMP with `hdrgm:Version`). libvips `uhdrload` checks MPF and then libultrahdr's full `is_uhdr_image`; the probe trusts the loader libvips picked (`uhdrload` ⇒ UHDR).
- The format libvips' loader reports must agree with the sniffed family, otherwise the file is refused (polyglots).
- PDF, PSD and RAW are recognised and refused with `unsupported` until M2.
- Limits before decode: frames ≤ `MAX_ANIMATION_FRAMES` (a limit of 1, from config or `max_animation_frames:1`, disables animation instead: the first frame is processed, as in imgproxy), animated frame pixels ≤ `MAX_ANIMATION_FRAME_PIXELS`, width × height × frames ≤ `MAX_SRC_PIXELS` (multi-page stills count one page), SVG bytes ≤ `MAX_SVG_BYTES` and the SVG is sanitised before librsvg reads even its header. Frame counts use the header's delay array as well as `n-pages`, because `vips_image_get_n_pages` reports 1 for 10 000 pages or more.
- The declared upstream `Content-Type` never drives detection; a disagreement only sets `ImageMeta.ContentTypeMismatch`.

## 2. Camera RAW strategy

1. `codec/raw` (cgo, `vips/shim`) calls `libraw_open_buffer` → `libraw_unpack_thumb` → `libraw_dcraw_make_mem_thumb`.
2. If the embedded preview is JPEG (or bitmap) and its long edge ≥ target long edge × DPR (or `enforce_thumbnail` is set), decode it with `jpegload_buffer` using normal shrink-on-load and apply the RAW's orientation (LibRaw `flip`), not the preview's.
3. Otherwise fall back to `dcrawload` with half-size demosaic when target ≤ 50% of sensor size, full otherwise. This path counts 4× in the limiter cost model.
4. Metadata: EXIF (make, model, lens, exposure, GPS) exported from LibRaw; stripped by default like any format.
5. Limits: RAW files are capped by `XI_LIMITS_MAX_SRC_BYTES`; demosaic path also by `XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS` (default 60 MP).

## 3. Animation handling

- Loaded as a vertical strip with `page-height`. `XI_LIMITS_MAX_ANIMATION_FRAMES` (default 256) and `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` (default 16 MP; total pixel limit applies to frames × frame pixels).
- Output keeps animation when source is animated, output format supports animation (GIF, WebP, AVIF sequences when libvips supports saving them, JXL when supported, MP4/WebM in M3) and `disable_animation` is false. Otherwise frame `page` (default 0) is used.
- Frame delays and loop count preserved; per-frame geometric ops use `arrayjoin` over frames.
- `fm=mp4|webm` / `f_mp4` / `.mp4` extension on an animated source → GIF→video (M3, `full` image).

## 4. Metadata policy

Default: strip EXIF/XMP/IPTC, keep copyright fields (`XI_PROCESSING_KEEP_COPYRIGHT=true`), convert to sRGB and drop the ICC profile (`strip_color_profile=true`; with `false` the converted output is tagged with the built-in sRGB profile, sGrey for grey, and an sRGB IEC61966-2.1 source profile is kept as is), always reset orientation after auto-rotate (the output EXIF, when kept, says orientation 1, so a second pass does not rotate again). `color_profile` embeds sRGB/CMYK/custom profiles from `XI_PROCESSING_COLOR_PROFILES_DIR`. GPS is always removed when stripping, even with keep-copyright.

## 5. Output formats

Defaults are config values (`XI_FORMATS_<FMT>_*`) and can be overridden per request by imgproxy `*_options`, `format_quality`, and dialect equivalents.

| Format | Encoder | Quality default | Lossless | Effort / speed | Chroma subsampling | Progressive / interlace | Animation | Alpha |
|---|---|---|---|---|---|---|---|---|
| JPEG | libjpeg-turbo; mozjpeg build optional (trellis, overshoot deringing, optimize scans, quant tables 0–8) | 80 | — | — | 4:2:0 default; 4:4:4 when q ≥ 90 or `no_subsample` | progressive default **on** (`XI_FORMATS_JPEG_PROGRESSIVE`) | — | flattened on background (white default) |
| PNG | libspng/libpng; quantizr palette when `quantize` or imgix `png8` | n/a (compression level 6) | yes | `compression` 0–9 | — | Adam7 `interlaced` off | — (APNG not written) | yes |
| WebP | libwebp | 80 | `lossless`, near-lossless | `effort` 0–6 (default 4) | smart subsample option | — | yes | yes |
| AVIF | libheif + aom (SVT-AV1 evaluated later) | 60 (AVIF 60 ≈ JPEG 80) | yes | `effort` 0–9 (default 4; ≤ 2 for images > 4 MP to bound latency) | 4:2:0 default; `avif_options:subsample` auto/on/off, 4:4:4 when q ≥ 90 | — | not in M1 | yes |
| HEIC | libheif + kvazaar (HEVC encoder, BSD) | 60 | — | kvazaar preset | 4:2:0 | — | — | yes |
| JPEG XL | libjxl | 75 (distance mapped) | yes | `effort` 1–10 (libvips default 7; ours 5 for latency) | n/a | progressive option | yes (libvips ≥ 8.16 frames) | yes |
| GIF | cgif + quantizr | n/a (`effort`, dither 0–1, bitdepth 1–8) | — | `effort` 1–10 (default 7) | — | interlace option | yes | 1-bit |
| TIFF | libtiff | 80 (JPEG compression) | deflate/LZW/zstd | — | — | — | pages | yes |
| BMP | own encoder (24/32-bit) | — | yes | — | — | — | — | 32-bit |
| ICO | own encoder (PNG-in-ICO, sizes ≤ 256) | — | yes | — | — | — | — | yes |
| SVG | passthrough of sanitised source only | — | — | — | — | — | — | — |
| MP4 / WebM | FFmpeg: VP9 (libvpx, BSD) for WebM; AV1 (libaom) in MP4; H.264 pending license decision | CRF mapped from quality | — | — | 4:2:0 | — | yes (M3) | WebM VP9 alpha |
| JSON | info endpoint / `fm=json` / `f=json` / `output=json` | | | | | | | |

Not produced: PDF, PSD, RAW, JPEG 2000, EXR (open question if anyone needs JP2 output for Safari legacy; no).

## 6. Format negotiation and best-format

### 6.1 Inputs to the decision

1. **Explicit format** in URL (option, extension, `fm`, `f_`, `output`) wins, except `auto`/`best`.
2. **Accept header**: parsed with q-values; `image/avif`, `image/webp`, `image/jxl` considered. `*/*` alone does not imply modern formats. Enabled per format via `XI_FORMATS_AUTO_{AVIF,WEBP,JXL}` (default true, true, false) and `XI_FORMATS_ENFORCE_*` (use it even when the source format could be kept).
3. **Content analysis** (from probe + cheap stats on the shrunk image): has alpha (and whether any pixel is actually non-opaque), animated, pixel count, and a *complexity score*: mean Sobel gradient magnitude on a 256 px luma thumbnail combined with unique-colour count. Score < `XI_FORMATS_BEST_COMPLEXITY_THRESHOLD` (default 5.5, same scale as imgproxy) → "graphic", else "photo".
4. **Cost guard**: AVIF skipped above `XI_FORMATS_AVIF_MAX_PIXELS` (default 16 MP) and JXL above 32 MP.

When the chosen format depends on `Accept`, the response carries `Vary: Accept`.

### 6.2 Decision matrix (`format=auto`, the default when no format is given and auto flags are on)

| Content | Accept has AVIF | else WebP | else JXL (if enabled) | else |
|---|---|---|---|---|
| Still photo, opaque | AVIF lossy | WebP lossy | JXL lossy | JPEG |
| Still photo, alpha | AVIF lossy + alpha | WebP lossy + alpha | JXL | PNG |
| Graphic / flat colours, opaque or alpha | WebP lossless (AVIF lossless is larger) | WebP lossless | JXL lossless | PNG (quantised when ≤ 256 colours) |
| Animated | WebP animated (AVIF sequences when supported) | WebP animated | GIF | GIF |
| SVG source, no raster ops, SVG acceptable (`XI_FORMATS_SVG_PASSTHROUGH`) | SVG | SVG | SVG | SVG |
| Source format not encodable (PDF, PSD, RAW, HEIC to non-Safari, BMP, TIFF) | apply rows above | | | JPEG or PNG by alpha |

Resolution rules (`internal/negotiate`, WP-31):

- Order: info → JSON; raw request or source format in `XI_FORMATS_SKIP_PROCESSING` / `skip_processing` → passthrough (before the explicit format, like imgproxy); explicit URL format; SVG row; content row.
- A modern format is a candidate only when `Accept` names it exactly with q > 0 (`image/*` and `*/*` never imply it), its auto or enforce flag is on, the row allows it and the cost guard passes. Candidates are ordered by enforce flag, then Accept q-value, then the column order above (AVIF, WebP, JXL). Enforce never overrides an explicit URL format (imgproxy's `ENFORCE_*` does; we follow §6.1.1).
- The "else" column is the first entry of `XI_FORMATS_PREFERRED` compatible with the row (alpha, animation, lossless for graphics); if none is, JPEG / PNG / GIF. The source format is never kept implicitly.
- Until M2 content analysis exists every still image is classed as a photo.
- `Vary: Accept` is set whenever Accept was consulted (any enabled, applicable modern format, SVG passthrough check, or strict Accept), including when the fallback won; never when the format is explicit or the source is passed through.

`Save-Data: on` or `ECT: slow-2g|2g` lowers the quality by `XI_FORMATS_SAVE_DATA_QUALITY_DELTA` (default −15) when the URL did not fix quality.

### 6.3 `best` (M2)

`format=best` / `f_auto` with `q_auto` / imgix `auto=format,compress`: encode the candidate set from the matrix row (max 3 formats), each at the quality chosen by auto-quality (DSSIM target, default 0.02) when enabled, and return the smallest. Skipped (falls back to the matrix's first choice) above `XI_FORMATS_BEST_MAX_PIXELS` (default 8 MP). Results are cached by the result cache so the cost is paid once per variant.

### 6.4 Auto-quality (M2, WP-56, `codec/encode`)

imgproxy Pro's `autoquality` (`aq:method:target:min_quality:max_quality:allowed_error`) and `XI_FORMATS_AUTOQUALITY_*` (CONFIG.md) pick the quality of a lossy save by search. It applies only to quality-driven saves (JPEG, lossy WebP, AVIF, HEIC, JPEG XL, TIFF-JPEG); lossless and quality-less formats save once.

Resolution, per field: the request's value, else the per-format config (`_FORMAT_TARGET`, `_FORMAT_MIN`, `_FORMAT_MAX`; defaults JPEG and WebP 40–90, AVIF 30–80), else the global config (target 0.02, 60–85, allowed error 0.001). A method that comes only from the config yields to an explicit request `quality`/`format_quality` (including the Save-Data delta); a request method always runs. When one request bound crosses a configured one, the configured one moves onto it.

| Method | Search | Result |
|---|---|---|
| `none` | — | the quality of §5 |
| `size` | the `max_bytes` search (`fitBytes`) over min..max: max first, then binary search, ≤ 6 saves. The target is bytes, capped by `max_bytes`; a target < 1 falls back to `max_bytes`, and without either the option is skipped (`autoquality_size_target`). An animation is searched with all its frames. | the largest quality tried whose output fits, else the smallest output with `OverBudget` |
| `dssim` | ≤ 6 saves starting at the §5 quality clamped into min..max. Each candidate is decoded with libvips and compared with the pre-encode image (after flatten/metadata) by the in-house SSIM (`internal/testutil/ssim`, luma, alpha composited on white and black). From one side of the target the next quality extrapolates ln(DSSIM) (slope of the last two saves, 0.05/quality before that); once the target is bracketed it interpolates between the closest failing and passing qualities, kept away from the bracket ends. A save within `allowed_error` of the target stops the search. | the lowest passing quality found; unmet (`Met=false`) at max when even max fails |
| `ml` | not implemented | parsed and run as `none`; reported in the skipped list (`autoquality_ml`) |

- **Comparison size**: both images are reduced to at most 512 px on the longer side (`CompareMaxDimension`, linear kernel, premultiplied) before SSIM, so one comparison costs the same for a 1 MP and a 16 MP image. Trade-off: downscaling averages away part of the artefacts, so above 512 px the measured DSSIM is lower than at full size and the search settles on a lower quality. On the 1024 px corpus of `TestAutoQualityDSSIMCorpus` the full-size DSSIM of the chosen output was 0.9–2.3× the compared one (median ≈ 1.4×; the largest gaps are on flat graphics and alpha edges, where the absolute values are small); at or below 512 px the two are the same measurement.
- **Animation**: `dssim` searches on the first frame only (cheap, and frames of one animation compress alike), then saves every frame once at the chosen quality: up to 7 saves in all.
- **With `max_bytes`**: the `dssim` result must still fit; when it does not, `max_bytes`' search runs below the chosen quality (extra saves) and `Met` is false.
- **Metric scale**: our DSSIM is (1 − SSIM)/2 on luma (Wang et al. 2004); imgproxy's comes from a different, multi-scale implementation, so the same numeric target is not the same visual quality. On the corpus the default 0.02 is lenient: most photos land at the per-format minimum. Tighten `_TARGET` (≈ 0.005–0.01) for visibly cleaner output.
- **Reporting**: `Result.Quality` is the chosen quality (`X-Xi-Quality` with debug headers), `Result.Attempts` counts every save, `Result.AutoQuality` holds the method, target, measured metric and `Met`.
- **Cost** (benchstat `-count=10`, `BenchmarkAutoQuality`, base-image builder on arm64; benchstat output in `internal/codec/encode/testdata/bench-autoquality.txt`), photo, default target):

  | Size / format | plain | dssim | saves | overhead |
  |---|---|---|---|---|
  | 320 px JPEG | 1.6 ms | 18.2 ms | 3 | 11× |
  | 320 px WebP | 7.5 ms | 27.1 ms | 2 | 3.6× |
  | 320 px AVIF | 109 ms | 524 ms | 4 | 4.8× |
  | 1024 px JPEG | 8.4 ms | 44.7 ms | 2 | 5.3× |
  | 1024 px WebP | 69 ms | 309 ms | 3 | 4.5× |
  | 1024 px AVIF | 845 ms | 1.88 s | 2 | 2.2× |

  Each comparison (decode, 512 px copy, SSIM) costs about 5 ms at 320 px and 15 ms at 1024 px, so JPEG pays mostly for the metric and WebP/AVIF mostly for the extra saves; allocations grow from 1 KiB to 3–9 MiB per request (luma planes and pixel copies). Enable it where the result cache absorbs the cost.
