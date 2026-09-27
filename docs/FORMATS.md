# Formats

libvips 8.18.x is the engine; library choices and licenses are in `docs/research/stack.md` and ADR 0006. Format detection is **always by magic bytes** (`codec.Sniff`), never by extension or upstream `Content-Type`. Every loader runs behind the probe stage, which reads only headers to enforce pixel, frame and page limits before any pixel decode.

## 1. Input formats

| Format | Loader | Anim / pages | Shrink-on-load | Metadata | Notes | Milestone |
|---|---|---|---|---|---|---|
| JPEG | libjpeg-turbo (`jpegload`) | — | `shrink` 2/4/8 | EXIF, ICC, XMP, IPTC | CMYK → sRGB via embedded or built-in profile; `fail_on=warning` rejects truncated files; UltraHDR JPEGs load through libultrahdr (`uhdrload`) as their SDR base image, gain map ignored (§1.4) | M1 |
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
| PDF | PDFium (`pdfload`) | pages (`page`, `pages` stacked) | `scale` at `XI_FORMATS_PDF_DPI` so the page renders at the target size | — | see §1.2; page count checked at probe; password-protected → 422 | M2 |
| PSD / PSB | ImageMagick 7 (`magickload`, PSD coder only) | `page` = layer (0 is the composite) | — | — | see §1.3; header probed in Go; hard 10 s / 256 MiB magick limits | M2 |
| Camera RAW (CR2, CR3, CRW, NEF, NRW, ARW, SRF, SR2, DNG, ORF, RW2, RAF, PEF, SRW, X3F, 3FR, IIQ, …) | LibRaw 0.22.x | — | embedded preview | EXIF from LibRaw | see §2 | M2 |
| JPEG 2000 | OpenJPEG (`jp2kload`; added to the base image by WP-05, not in imgproxy-docker-base) | — | `page` pyramid level | — | | M1 |
| OpenEXR, Radiance HDR, PPM/PFM | OpenEXR 3.4 (`openexrload`, the buffer spooled to a memfd) / libvips `radload_buffer`, `ppmload_buffer` (PBM/PGM/PPM ASCII and binary, P1–P6, since WP-97; PF/Pf) | — | — | — | see §1.4: tone-mapped to 8-bit sRGB (`XI_FORMATS_HDR_TONEMAP`) unless `preserve_hdr` and a JPEG XL or AVIF output; `/info` reports `"hdr": true` and `sample_format: "float"` | M2 |
| Video (MP4, MOV, WebM, MKV, AVI, animated MP4 GIF-likes) | FFmpeg 8.1 LGPL through our own shim `internal/video/ffmpeg` (build tag `ffmpeg`, `full` and `gpl` images, ADR 0010; decoders h264, hevc, vp8, vp9, AV1 via dav1d, mpeg4, mjpeg, png, rawvideo) | one frame becomes the image; `vtt` assembles a sprite of frames and `vta` an animation (WP-75, §1.5) | libswscale scales the frame to 2× the target | container tags and streams (`/info` `video_meta`, `video_streams`); display matrix → orientation | frame at `vts` (negative from the end, `vtk` keyframe), else best frame among keyframes (luma entropy and spread, fades penalised); see §1.5 | M3 (WP-74) |
| JPEG XR / HD Photo (`.jxr`, `.wdp`, `.hdp`) | jxrlib (Microsoft, BSD-2-Clause) through the shim's own libvips loader `jxrload_buffer` (`internal/vips/jxr.c`, §1.6) | — | — | ICC | 8/16-bit grey, RGB(A) and CMYK(A) kept; half, float, fixed-point, RGBE and packed formats converted to 8-bit by jxrlib | M4 (WP-97) |
| TGA, PCX, DDS (DXT1/3/5, BC4–BC7), QOI, SGI, Sun raster, XBM, XPM, PAM | ImageMagick 7 (`magickload`, one coder per format in `policy.xml`, §1.6) | first image only (DDS mipmap level 0) | — | — | header probed in Go (`codec.ParseLegacyHeader`) | M4 (WP-97) |
| WBMP, ICNS | own pure-Go loaders (`codec/wbmp`, `codec/icns`, §1.6) | ICNS: largest entry | — | — | ICNS PNG/JPEG 2000 entries go through libvips | M4 (WP-97) |
| Not supported | EPS/PS/AI (needs Ghostscript, AGPL-3.0: refused, §1.6), PICT (libvips' magick sniffer takes its 512-byte zero header for TGA, §1.6) | | | | `unsupported` (422) or unrecognised | — |
| Blocked | FITS, Matlab, NIfTI, Analyze, CSV/matrix, raw pixels, magick for anything except PSD/PSB and the WP-97 legacy coders | | | | `vips_block_untrusted_set(TRUE)` and operation block list | — |

**Per image flavour.** Every input format above is decoded by every base image flavour (`slim`, `full`, `gpl`), video excepted (`full` and `gpl` only, §1.5): jxrlib and the widened ImageMagick policy are in `slim` (jxrlib adds about 0.4 MB) and the other flavours start from it. The `gpl` flavour adds no input format (it would have carried EPS through Ghostscript; §1.6 says why it does not).

**Base image availability (WP-66, base `8.18.6-r2-slim`).** The slim base image now ships the M2 loaders: `dcrawload` (LibRaw 0.22.x, `libraw_r`, headers in the builder for `codec/raw`), `pdfload` (PDFium, never poppler), `openexrload` (OpenEXR 3.4 + Imath) and `magickload` (ImageMagick 7, load only, as the `vips-magick.so` module). ImageMagick's `/opt/xtandard/etc/ImageMagick-7/policy.xml` registers and allows only the PSD module (PSD/PSB read; widened by WP-97 to the legacy coders of §1.6) and caps each magick image at 10 s, 256 MiB memory/map, no disk cache, 64 MP area, 16K width/height (`docker/base/README.md`). Because libvips' generic loader dispatch now considers `magickload`, any file that reaches libvips without a native loader (for example BMP or ICO bytes passed straight to `vips_image_new_from_buffer`) fails in `magickload` with a policy error instead of "not a known format"; the app still routes BMP/ICO to its own loaders and picks every loader from the `codec.Sniff` result. `dcrawload`, `openexrload` and `magickload` are libvips `UNTRUSTED` operations; with `vips_block_untrusted_set(TRUE)` the app unblocks only the ones it uses, by name: `magickload_buffer` (WP-51), `pdfload_buffer` (WP-50), `openexrload`, `radload_buffer` and `ppmload_buffer` (WP-67), and the shim's own `XiForeignLoadJxrBuffer` (WP-97, registered as untrusted as well). Camera RAW goes through the app's own LibRaw shim, so `dcrawload` stays blocked (§2).

### 1.1 Detection and probe (`codec.Sniff`, `codec.Probe`)

- Magic bytes: JPEG `FF D8 FF`; PNG signature; `GIF87a`/`GIF89a`; `RIFF….WEBP`; JPEG XL codestream `FF 0A` or container box `JXL `; JPEG 2000 `jP  ` signature box or raw J2K codestream `FF 4F FF 51`; TIFF `II*\0`/`MM\0*` and BigTIFF `II+\0`/`MM\0+`; BMP `BM` + known info-header length; ICO/CUR (reserved 0, type 1/2, sane first entry); SVG via `security/svg.IsSVG` (first 4 KiB); `%PDF-`; `8BPS` v1/v2 (PSD/PSB); RAW signatures (CR2, CR3 `ftypcrx`, CRW, RAF, ORF, RW2, X3F, MRW). TIFF-based RAWs without their own signature (NEF, ARW, DNG, PEF, SRW, 3FR, ERF, MEF, MOS, DCR) are told apart from TIFF by IFD0 (`codec/raw.Sniff`): a `DNGVersion` tag, or a camera maker's `Make` together with a RAW-shaped IFD0 (reduced-resolution preview, `SubIFDs`, a camera-specific compression or CFA/linear-raw pixels). A plain RGB TIFF written by a camera stays TIFF.
- ISOBMFF: major brand first, then compatible brands inside the `ftyp` box: `avif`/`avis` → AVIF; `heic`/`heix`/`hevc`/`heim`/`heis`/`hevm`/`hevs` → HEIC; generic `mif1`/`msf1`/`miaf` → HEIF, refined to AVIF/HEIC by the loader's `heif-compression`. libvips' `heifload` only accepts a fixed list of *major* brands (no `avis`), so an AVIF sequence whose major brand is `avis` is sniffed but refused as unsupported by the loader.
- HDR inputs (WP-67): OpenEXR `76 2F 31 01`; Radiance `#?RADIANCE` or `#?RGBE` as the first line; PBM/PGM/PPM `P1`–`P6` (ASCII and binary, since WP-97) and PFM `PF`/`Pf` followed by whitespace; the shim refuses `ppmload` for anything else. OpenEXR headers are parsed in Go (`codec.ParseEXRHeader`: data window, channels, tiled/deep/multi-part flags), so the pixel limit holds before OpenEXR sizes its line-offset tables; deep and multi-part files and files without R, G, B or Y channels are `unsupported`. Radiance and PNM headers are read by libvips (text lines, nothing decoded).
- UltraHDR: a JPEG whose header (before SOS) has an APP2 `MPF` segment **and** gain-map metadata (APP2 ISO 21496-1 `urn:iso:std:iso:ts:21496:-1` or APP1 XMP with `hdrgm:Version`). libvips `uhdrload` checks MPF and then libultrahdr's full `is_uhdr_image`; the probe trusts the loader libvips picked (`uhdrload` ⇒ UHDR).
- The format libvips' loader reports must agree with the sniffed family, otherwise the file is refused (polyglots).
- Camera RAW headers are read by LibRaw without unpacking (§2). PDF is probed by PDFium's header read (page tree and first page size, nothing rendered) and PSD/PSB by a Go parser of the Photoshop header (`codec.ParsePSDHeader`: canvas, channels, depth, colour mode, layer count, composite flag), never by ImageMagick, which decodes the whole file even for a header.
- PDF pages: more than `XI_LIMITS_MAX_PDF_PAGES` (500) fail at probe with `limit_exceeded` (422) before any page renders. The raw `n-pages` is read, not `vips_image_get_n_pages`, which reports 1 for 10 000 pages or more.
- Limits before decode: frames ≤ `MAX_ANIMATION_FRAMES` (a limit of 1, from config or `max_animation_frames:1`, disables animation instead: the first frame is processed, as in imgproxy), animated frame pixels ≤ `MAX_ANIMATION_FRAME_PIXELS`, width × height × frames ≤ `MAX_SRC_PIXELS` (multi-page stills count one page), SVG bytes ≤ `MAX_SVG_BYTES` and the SVG is sanitised before librsvg reads even its header. Frame counts use the header's delay array as well as `n-pages`, because `vips_image_get_n_pages` reports 1 for 10 000 pages or more.
- The declared upstream `Content-Type` never drives detection; a disagreement only sets `ImageMeta.ContentTypeMismatch`.
- Long-tail formats (WP-97): JPEG XR `II BC 00|01`; DDS `DDS ` and a 124-byte header; QOI `qoif`; Sun raster `59 A6 6A 95`; SGI `01 DA`, storage 0/1, 1–2 bytes per channel, 1–3 dimensions; PCX `0A`, version 2 or 5 (the ones ImageMagick detects), encoding 0/1, 1/2/4/8 bits per plane; XPM `/* XPM */`; XBM `#define <name>_width`; PAM `P7`; ICNS `icns` and a length. TGA and WBMP have no signature and are checked last: TGA by a self-consistent 18-byte header (colour map only with a colour-mapped type, a known type with a matching depth, non-zero size, no interleave bits, not ISO BMFF), WBMP (type 0, no extension headers) only when the file length is exactly what its header declares, so it needs the whole file (every caller passes it). The shim applies the same TGA and signature checks before it lets `magickload` run (`xi_is_magick_input`), and ImageMagick's policy is the third fence.
- Video containers (WP-74): an ISOBMFF `ftyp` box without an image brand is MP4 (`isom`, `mp41`, `mp42`, `avc1`, `av01`, `dash`, `3gp*`, `M4V `, …) or MOV (`qt  `); a QuickTime file without `ftyp` starts with a `moov`, `mdat`, `wide`, `free`, `skip` or `pnot` atom; EBML (`1A 45 DF A3`) is WebM when its DocType is `webm`, else MKV; `RIFF….AVI ` is AVI. They are input-only formats (`video/mp4`, `video/quicktime`, `video/webm`, `video/x-matroska`, `video/x-msvideo`) and the FFmpeg demuxer that reads them must match the sniff (`mov`, `matroska`, `avi`), like the libvips loader for images.

### 1.6 Long-tail inputs (WP-97)

Input only; none of these formats can be written (§5), and `format=auto` negotiates the output like for any source.

- **JPEG XR** (`jxr`, `image/jxr`): jxrlib at the 4creators commit Debian and Homebrew ship (1.2~git20170615.f752187), BSD-2-Clause, built in the `slim` base (`docker/base/build-jxrlib.sh`, two shared libraries, 0.4 MB) with Debian's fixes and ours (`docker/base/patches/jxrlib`). libvips has no JPEG XR loader and ImageMagick's shells out to `JxrDecApp` (a delegate: refused), so the shim registers its own libvips loader class, `XiForeignLoadJxrBuffer` (`jxrload_buffer`, `internal/vips/jxr.c`), at init: it sniffs `II BC`, reads the container and codestream headers in `header` (no pixel decoded, so probes and pixel limits work as for any libvips format) and decodes in `load` through jxrlib's memory stream over the blob's C memory into a `g_malloc`'d buffer the output image owns. Thumbnails (`vips_thumbnail_buffer`), `/info` and every pipeline path find it like any loader. Pixel formats: 8- and 16-bit grey, RGB (BGR orders swapped) and RGBA, 8/16-bit CMYK(A) as libvips CMYK are kept; half, float, fixed-point, RGBE, 5/6/10-bit packed and 1-bit formats go through jxrlib's converters to 8-bit grey, RGB or RGBA (HDR JPEG XR is clipped, not tone-mapped); N-channel and YCC formats are refused. The container's orientation is applied by jxrlib; an embedded ICC profile is kept. **Hardening:** jxrlib is old C code, so the build defines `NDEBUG` (its asserts would abort the process; the two that validate headers became checks, patch 0005), the library no longer prints to stdout/stderr (0004), and about two hours of libFuzzer with ASan, UBSan and LeakSanitizer over the loader (`jxr.c` + jxrlib, seeded with the fixtures and the imagecodecs samples) found a NULL write, over-reads of a global table, the stack and a heap buffer, and leaks on failed and interleaved-alpha decodes: and an unbounded allocation sized by a corrupt alpha plane header: all fixed (patches 0005 and 0008-0012) or refused up front (the loader rejects a container whose pixel format disagrees with its codestream, which is where jxrlib wrote past its buffers). The crashing inputs are regression seeds of `FuzzJXR`. jxrlib remains old C code decoding untrusted input inside the process; the residual risk is recorded here and in ADR 0006. `FuzzJXR` (Go) keeps fuzzing the loader entry point and `TestNoLeakJXR` checks that decoders and buffers are freed.
- **ImageMagick legacy coders** (`tga`, `pcx`, `dds`, `qoi`, `sgi`, `sun`, `xbm`, `xpm`, `pam`): ImageMagick is already in `slim` for PSD; its coders are compiled in, so widening the policy costs nothing in size. `policy.xml` registers the modules PSD, TGA, PCX, DDS, QOI, SGI, SUN, XBM, XPM and PNM and lets exactly the coders PSD, PSB, TGA, PCX, DDS, QOI, SGI, SUN, XBM, XPM and PAM read (not their aliases ICB, VDA, VST, DCX, DXT1, DXT5, RAS, PICON, PM, PBM, PGM, PPM, PNM, PFM, PHM); every delegate, filter, indirect path and other coder stays denied and the resource limits are unchanged (10 s, 256 MiB, no disk, 64 MP, 16K per side). libvips 8.18 has no QOI loader of its own (`qoiload` arrives in 8.19). ImageMagick decodes a whole file even for its header, so headers are parsed in Go (`codec.ParseLegacyHeader`: size, alpha, grey, depth) and the pixel limits hold before ImageMagick runs. DDS: DXT1/3/5 (BC1–3) and DX10 BC4–BC7 through ImageMagick's own decoders; mipmaps and cube faces are ignored (level 0). XPM transparency is reported when the file names the `None` colour.
- **WBMP and ICNS** (pure Go, `codec/wbmp`, `codec/icns`): ImageMagick has a WBMP coder but no content detection for it, so libvips could never pick it; a 60-line Go decoder is simpler and safer. ICNS picks the largest entry: PNG and JPEG 2000 entries (ic04–ic14, icp4–icp6) are handed to libvips like PNG-in-ICO, "ARGB" and classic `is32`/`il32`/`ih32`/`it32` run-length entries (with their `s8mk`… masks) are decoded in Go. Both are fuzzed (`FuzzDecode`).
- **PBM/PGM/PPM** ASCII and bitmap variants (`P1`–`P4`) now go through `ppmload_buffer` like the binary ones (the shim allowed only `P5`/`P6` before): ASCII parsing is bounded by the same pixel limit as the binary files. PBM decodes to 8-bit grey.
- **EPS / PostScript / Illustrator: not supported, by decision.** Rendering PostScript needs Ghostscript, which is AGPL-3.0. ImageMagick can use it as a library (`--with-gslib`), which links AGPL code into the server process, or as the `gs` delegate program, which the policy forbids everywhere. Either way the whole process would run a full PostScript interpreter on untrusted input: Ghostscript's `-dSAFER` sandbox has been bypassed repeatedly (CVE-2016-7977, CVE-2018-16509, CVE-2019-14811, CVE-2021-3781, CVE-2023-36664, CVE-2024-29510: file access and command execution from a crafted document), and a crash would take the server down with it. On top of that, AGPL's network clause would make the `gpl` image as a whole AGPL for every deployer. WP-97 therefore stops at this record: EPS/PS files are recognised by nothing and answer "unrecognised image format" (415/422). If EPS is ever wanted, the safer shape is a separate, sandboxed converter service (Ghostscript in its own container, no network, seccomp) that the server calls, outside this binary and its licence; ADR 0006 records the decision.
- **PICT: not supported.** ImageMagick reads it, but a PICT file starts with a 512-byte zero header that libvips' magick sniffer classifies as TGA, so ImageMagick is always handed the wrong coder; making it work would need a rewritten buffer (the `PICT` OLE prefix) on every load path. Not worth it for a format last written by classic Mac OS.

### 1.5 Video thumbnails (WP-74)

A frame of a video becomes the image, which the pipeline then processes like any still (imgproxy Pro's video thumbnails). Only the `full` image decodes video; the design is ADR 0010.

- **Availability.** `internal/video/ffmpeg` is built against FFmpeg with the build tag `ffmpeg` (the full image, `-tags ffmpeg`); without it the same API answers `Unsupported`. `XI_VIDEO_ENABLED=auto` (default) decodes video when the build can; `true` fails startup without FFmpeg; `false` refuses video everywhere. A video on the slim image, or with video off, is 422 `unsupported` with the public message "video sources are not supported by this image" (or "… are disabled"), never a crash or "unrecognised format".
- **Reading the source.** The bytes stay in the C memory of the fetched buffer and FFmpeg reads them through a custom `AVIOContext` (no temporary file, no protocol: QuickTime references and playlists cannot open anything). Demuxers are limited to `mov`, `matroska` and `avi`, decoders to `h264`, `hevc`, `vp8`, `vp9`, `libdav1d` (AV1), `mpeg4`, `mjpeg`, `png` and `rawvideo`. Every decoder runs single-threaded (parallelism comes from the worker semaphore) with `max_pixels` = `XI_VIDEO_MAX_PIXELS`.
- **Probe.** Container and stream headers (`avformat_find_stream_info`): the best video stream (`av_find_best_stream`), its codec, coded size, sample aspect ratio (the frame is shown, and decoded, at the display width: anamorphic video comes out undistorted), average frame rate, frame count (from the container, or duration × fps), container duration and bit rate, the display matrix and every stream. The probe reports the display size and turns the display matrix into the EXIF orientation that shows the frame the same way (90° clockwise → 6, 180° → 3, 270° → 8, mirrored → 2, 7, 4, 5), which the planner applies after the resize like a photo's. `XI_VIDEO_MAX_BYTES`, `XI_VIDEO_MAX_DURATION` and `XI_VIDEO_MAX_PIXELS` (also from the container headers, before any decode) are checked here, and `XI_LIMITS_MAX_SRC_PIXELS` applies to the frame like to any image; all are 422 `limit_exceeded`. `XI_LIMITS_MAX_SRC_BYTES` still caps the download itself.
- **Frame selection.** `video_thumbnail_second` (`vts`, seconds, float) picks the first frame whose timestamp reaches it (FFmpeg's accurate seek: the keyframe before it is decoded and the frames in between are dropped); a negative value counts from the end (`vts:-1`: one second before the end) and the time is clamped to the video, the last frame answering a time past the end. `video_thumbnail_keyframes` (`vtk:1`) takes the keyframe at or before the time instead: faster (non-key frames are skipped by the decoder), less precise. Without `vts`, `XI_VIDEO_THUMBNAIL_SECOND` applies; its default `auto` is **best-frame selection**: `XI_VIDEO_THUMBNAIL_SAMPLES` (5) timestamps spread over the interior of the first `XI_VIDEO_THUMBNAIL_SPAN` (10 s) of the video (points (i+1)/(n+1), never the first frame, often black, or the last), the keyframe at or before each, and the best-scoring one wins: the Shannon entropy of a 64 px wide grey copy's luma histogram plus its standard deviation / 64, times (1 − share of pixels darker than 24 or brighter than 232)², quartered when the mean luma is below 32 or above 224 (fades, black or white title cards, flashes). Deterministic; a tie keeps the earlier frame. The debug header `X-Xi-Video-Frame` reports the frame (`0.480s key best 4 7.65`: time, keyframe, best-frame selection over 4 distinct keyframes with that score).
- **Decode and conversion.** The chosen frame is converted by libswscale into 8-bit RGB (RGBA for pixel formats with alpha, e.g. PNG or rawvideo RGBA; VP8/VP9 alpha side data is not decoded) straight into a libvips-owned buffer that becomes the image without a copy. The YUV → RGB matrix and range are the stream's (BT.709, BT.601, BT.2020, full or limited range; unspecified is BT.601 like FFmpeg). High bit depth and HDR (PQ/HLG) video is converted to 8 bits without tone mapping. **Shrink on load:** when the result is at least 2× smaller than the frame, libswscale scales the frame to twice the target (area averaging) and libvips does the last 2× with its kernel, like JPEG shrink-on-load; smaller reductions decode at the display size (`plan.LoadStep.Scale`; `plan.Limits.DisableShrinkOnLoad` turns it off, as for JPEG). `TestVideoPreShrink` checks the pre-shrunk result against a full-size decode (DSSIM ≤ 0.01); the timings are below.
- **Cancellation and memory.** A `context.AfterFunc` watcher sets an atomic abort flag that FFmpeg's I/O interrupt callback and the decode loop poll: a request that times out or whose client leaves stops at the next packet with its context's kind. Nothing in C points to Go memory; every FFmpeg object is freed before the call returns (`TestVideoNoLeak`: 200 thumbnails, no libvips object, tracked byte or FFmpeg handle left behind). The planner adds 8 bytes per stream pixel for the decoder (frame, references, scaler) to the memory-budget estimate.
- **Output.** Any image format, negotiated like any source (a video has no output format of its own: the §6.2 "not encodable" row); `Content-Type` follows the result. A `vta` animation, or any animated result, can also be encoded as MP4/WebM (§5.1, WP-76).
- **One open per request.** The probe keeps its FFmpeg handle open (`codec.ProbeSession`, `ffmpeg.Session`) and hands it to the processor through the server's `SourceMeta.Session` (closed by the server when the request ends, errors included): the frame, sprite or animation is decoded on the probed handle instead of opening and probing the file a second time (the WP-74 double open; `avformat_find_stream_info` costs ~10 ms on H.264). The handle is only reused when it reads the same C memory as the executor's source (`Session.Reads`); otherwise the file is opened again.
- **Detection (WP-91).** Face and object gravities, `crop_objects`, `blur_detections`, `draw_detections` and `/info` `detect_objects` work on a single-frame video: the models see the frame the request uses (the same `vts`/`vtk` or best-frame choice), upright (display matrix applied), so the boxes line up with the frame as shown. For processing that frame is decoded once, at the display size, on the probe's handle, and the same decode is detected on (thumbnailed to 1024 px) and then processed (it is not scaled on load: `plan.SourceMeta.FrameDecoded`); `/info` detects on the frame its default selection picks, like its pixel fields. The detection cache key has the frame's timestamp. Without the detector explicit detection options are 422 and `sm` falls back to attention, as for images.
- **Sprites (`video_thumbnail_tile`, `vtt`, WP-75).** imgproxy Pro's arguments `step:columns:rows:tile_width:tile_height[:extend_tile:trim:fill:focus_x:focus_y]`. `step` > 0 samples a frame every `step` seconds from 0; `step` < 0 spreads `columns × rows` frames evenly over the duration (`duration / cells`), and `step` 0 turns the option off. Every frame is taken upright (display matrix applied) and fitted into `tile_width × tile_height` (one side 0: derived from the frame's aspect ratio; both 0: the frame size); `fill` covers the tile and crops around `focus_x:focus_y` (default 0.5:0.5); `extend_tile` centres a fitted frame on a black tile of exactly the requested size. Cells past the end of a short video stay black unless `trim` drops them (the sprite shrinks to the columns and rows used). The tiles are joined with libvips `arrayjoin` (row-major), and the sprite is then processed like any image (resize, crop, format, …). `video_thumbnail_keyframes` (or `XI_VIDEO_THUMBNAIL_KEYFRAMES`) takes the keyframe at or before each timestamp. The debug header `X-Xi-Video-Frame` says `tile 3x2 6 frames 0.000-1.680s`.
- **Animations (`video_thumbnail_animation`, `vta`, WP-75).** `step:delay:frames:frame_width:frame_height[:extend_frame:trim:fill:focus_x:focus_y]`: `frames` frames every `step` seconds (< 0: `duration / frames`), each sized like a sprite tile, shown for `delay` milliseconds (0: the sampling pace, `step`, at least 20 ms), looping forever. Frames past the end of a short video are black unless `trim` drops them. The result is an animation for negotiation (`format=auto` keeps it animated: WebP or GIF, §6.2), the frame limits and the planner, which resizes each frame like a GIF's; a still output format keeps the first frame. `vtt` wins when both are set. Neither a sprite nor an animation is detected (WP-72/73): face and object gravities fall back to the smart crop and `crop_objects`, `blur_detections` and `draw_detections` do nothing.
- **One handle, forward decoding.** A sprite or animation decodes all its timestamps on one handle (`Session.Frames`): a timestamp after the previous one with no keyframe in between (by the demuxer's index) is reached by decoding forward instead of seeking back to the keyframe, so ten frames of the one-keyframe H.264 clip cost one pass instead of ten seeks (`BenchmarkFrames`, below). The forward and the seeking decode give the same frames (`TestSessionFrames`, DSSIM ≤ 0.001 on H.264, VP9, AV1, VP8 and rawvideo).
- **Limits.** `XI_VIDEO_MAX_ANIMATION_FRAMES` (100) caps the frames decoded (a larger grid keeps its columns and loses rows; a longer animation is cut), each sprite or animation side is at most 16384 px (larger cells are shrunk), and the server checks the assembled result against `XI_LIMITS_MAX_ANIMATION_FRAMES`, `XI_LIMITS_MAX_SRC_PIXELS` and `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` like a decoded animation (422). The memory estimate of the plan counts the composite's pixels plus the decoder's (8 bytes per stream pixel) before anything is decoded.
- **Dialects.** imgproxy `vts`/`video_thumbnail_second`, `vtk`/`video_thumbnail_keyframes` (also in /info URLs); Cloudinary `so_<seconds>` on the `video` asset type with an image format (`/video/upload/so_2,f_jpg/dog.mp4`; `so_auto` keeps the configured selection, percentages are unsupported); ImageKit `<video>/ik-thumbnail.jpg` with `so-<seconds>`; imgix has no equivalent. URL-DIALECTS.md lists them.
- **`/info`.** `video_meta` (`vm`, on by default): the container tags (strings, as imgproxy) plus `container`, `codec`, `width`/`height` (coded), `rotation`, `duration`, `fps`, `bit_rate`, `frames`; `video_streams`: one entry per stream with imgproxy's `type`, `codec`, `bps`, `fps`, `duration`, `language`, `frequency`, `layout`, plus `width`, `height`, `rotation` for video. `width`/`height`/`orientation` describe the frame as shown; the pixel options (`average`, `blurhash`, …) sample the default frame.

Measured on the full builder (arm64 Docker on Apple silicon, one thread, `-count=10`, benchstat medians; raw output in `internal/video/ffmpeg/testdata/bench-thumbnail.txt`), 1280×720 fixtures:

| Codec (720p fixture) | exact frame at 0.9 s, 720p | exact, scaled to 320×180 on load | keyframe (`vtk`), 320×180 | best frame (5 samples), 320×180 | probe (headers) | `ffmpeg` CLI, exact 320×180 (process per request) | full pipeline to a 320×180 JPEG: shrink on load / full-size decode |
|---|---|---|---|---|---|---|---|
| H.264 High (Big Buck Bunny, B-frames, one keyframe) | 74.9 ms | 66.2 ms | 22.4 ms | 19.8 ms | 10.1 ms | 140 ms | 84.0 ms / 87.3 ms |
| VP9 (keyframe every 0.48 s) | 12.3 ms | 7.4 ms | 2.7 ms | 7.3 ms | 0.18 ms | 42 ms | 11.7 ms / 15.8 ms |
| AV1 (dav1d, keyframe every 0.48 s) | 20.3 ms | 17.8 ms | 8.3 ms | 24.2 ms | 0.08 ms | 74 ms | 19.7 ms / 24.5 ms |

Every benchmarked thumbnail call opens the file, so the probe time is included (in the server the probe's handle is reused since WP-75, which saves that time per request); H.264's is high because `avformat_find_stream_info` decodes a frame to learn the pixel format. An exact H.264 frame decodes every frame since the previous keyframe (27 here), which is what `vtk:1` avoids. Shrink on load saves 20–25% end to end for VP9 and AV1 and is within noise for H.264, whose decode dominates; the pre-shrunk result matches the full-size decode (`TestVideoPreShrink`), so it stays on. The in-process decoder is 2–6× faster than spawning the CLI, before counting the temporary file the CLI would need for MP4s with the `moov` box at the end. Go allocations are 2–4.5 KiB and 30–60 allocations per thumbnail (the frame lives in C memory). Raw outputs: `internal/video/ffmpeg/testdata/bench-thumbnail.txt`, `internal/pipeline/exec/testdata/bench-video.txt`.

### 1.2 PDF (WP-50)

- **Loader**: PDFium through `pdfload_buffer`, unblocked by name (`vips.DefaultLoaders`). Probe measures page 0 at `XI_FORMATS_PDF_DPI` (default 72: one pixel per point); that is the page's natural size and the size `/info` reports (`pages_number` is the page count).
- **Rendering at the target size** (like imgproxy Pro): the planner treats PDF as a vector source, so the page is rendered by PDFium directly at the scale the request needs (`scale` on top of the probe density), enlarging included, instead of rendering at the natural size and resampling. The vector guard caps the render at `XI_LIMITS_MAX_SRC_PIXELS`. A test compares a 800 px render with a 576 DPI render downscaled (DSSIM ≈ 0.001, against ≈ 0.19 for the upscaled 72 DPI render).
- **Pages**: `page` (0-based) selects a page; `pages` (`-1` = to the end) stacks that many pages from `page` on into one image, vertically and left-aligned, the gap beside narrower pages filled like the background (imgproxy Pro semantics). A page other than the first, or a stack, has a size the header does not tell: the executor measures the selection with a header-only PDFium load (no rendering) and plans on that size. `pages` applies to multi-page stills generally (TIFF, HEIF: their pages are loaded one at a time and joined, since pages may differ in size, the gap beside narrower pages transparent and flattened like any alpha), to the frames of an animation that is not kept as one (§3), and never to PSD layers.
- **Background**: pages render onto opaque white and the result has no alpha, so negotiation picks JPEG for an automatic format. Pages render transparent (alpha kept) only when the request names an output format with alpha itself (`f:png`, `webp`, `avif`, …; a negotiated format does not count) and no `background`. A `background` colour renders transparent and flattens onto that colour (PDFium's own background option is only used for white or transparent: libvips passes other colours to it with red and blue swapped).
- **Password-protected** PDFs (a user password is needed to open them) fail with `unsupported` (422) and the public message "password-protected documents are not supported"; there is no password option. PDFium reports the case only as `pdfload`'s error text ("password required or incorrect password"), which the shim matches: the one place an error kind comes from libvips text. Owner-password-only PDFs open normally.

### 1.3 PSD / PSB (WP-51)

- **Loader**: ImageMagick 7 through `magickload_buffer` (the `vips-magick.so` module). It is libvips-UNTRUSTED and unblocked by name; three layers keep it to Photoshop files and the legacy formats of §1.6: `codec.Sniff` sends only those signatures to it, the shim refuses it for any buffer that does not start with one of them (even when libvips' own dispatch picks it for an unknown format, as it does for BMP bytes), and the base image's `policy.xml` registers no other coder (regression test: ImageMagick called directly on BMP, PNG, GIF, SVG and PDF bytes fails).
- **Composite strategy**: ImageMagick reads the merged composite (image data section) when the file has one ("Maximize compatibility" on, or a PSB/PSD without the version-info resource saying otherwise). Without it (resource 0x0421 `hasRealMergedData = 0`, the image data is blank) ImageMagick flattens the layers itself: pixel layers with normal blending come out right, adjustment layers, effects and smart objects do not. Such files are processed by default; `XI_FORMATS_PSD_REQUIRE_COMPOSITE=true` refuses them at probe with `unsupported` (422) instead.
- **Pages**: `page` selects an ImageMagick image: 0 is the composite, 1… are the layers at their own size; `/info` `pages_number` is 1 + the layer count. `pages` never stacks PSD layers.
- **Limits**: the canvas counts against `XI_LIMITS_MAX_SRC_PIXELS` at probe; ImageMagick's policy caps every image at 10 s, 256 MiB and 64 MP whatever the configuration says.

### 1.4 HDR inputs: OpenEXR, Radiance, PFM (WP-67)

- **Loaders**: OpenEXR through `openexrload`, which only reads files: the shim spools the source buffer to a `memfd` (Linux; opened as `/proc/self/fd/N`, so a read-only root filesystem is fine and nothing lands in `/tmp`; other platforms use a temporary file) that lives exactly as long as the loaded image. Decode failures inside OpenEXR are mapped back to the source (`corrupt`, 422) through the spool registry. libvips reads every OpenEXR image as 4-band float scRGB (RGBA half → float, A = 1 when the file has none; luminance/chroma files come out grey; multi-part and deep files are refused at probe). Radiance through `radload_buffer` (the shim names it directly for `#?RGBE` headers, which libvips' sniffer ignores); RGBE decodes to float scRGB (XYZE files are converted to scRGB). PFM (and 8/16-bit PGM/PPM, which are not HDR) through `ppmload_buffer`.
- **Probe and limits**: `ImageMeta.HDR` (and `/info` `"hdr": true`) marks OpenEXR, Radiance and PFM; `BitDepth` is 16 for half OpenEXR, 32 otherwise; the colour space is scRGB. `/info` describes the pixels the pipeline sees: `sample_format` `float`, 3 bands (4 with OpenEXR alpha), `colorspace` `scrgb` (`b-w` for a grey PFM). Width × height count against `XI_LIMITS_MAX_SRC_PIXELS` at probe. The planner loads HDR sources with random access (the tone mapper reads them twice) and counts 4 bytes per sample in the memory estimate.
- **Tone mapping** (`pipeline/exec/hdr.go`, the colour-to-working-space step): negative values clip to 0; the white point W is the nearest-rank 99th percentile of the Rec. 709 luminance, measured on a box-filtered copy at most 256 px on its long side, and at least 1. Operators (`XI_FORMATS_HDR_TONEMAP`), per channel on linear light:
  - `reinhard` (default): extended Reinhard, `x·(1 + x/W²)/(1 + x)`. W maps to 1, and a source within [0, 1] (W = 1) is unchanged.
  - `aces`: Narkowicz's fitted ACES filmic curve `x(2.51x + 0.03)/(x(2.43x + 0.59) + 0.14)` on `x = c·e`, with the usual exposure e = 0.6, lowered to 7.2417/W when W would otherwise pass the curve's white (f(7.2417) = 1).
  - `clip`: `min(x, 1)`: no curve, no exposure change.
  The result is clipped to [0, 1], sRGB-encoded to 8 bits (libvips' scRGB → sRGB), and the pipeline continues in 8-bit sRGB (8-bit grey for a grey PFM). Alpha (OpenEXR) is clamped to [0, 1] and kept. The formulas are pinned by a test that maps a linear ramp through each operator.
- **`preserve_hdr`**: with a JPEG XL output the image stays linear float scRGB (values above 1 included) and `jxlsave` writes a float JPEG XL; with AVIF it is clipped at 1 and converted to 16-bit sRGB, which `heifsave` writes as 12-bit AV1. Only crop, resize, rotate and flip keep that precision, so any other step (filters, colour adjustments, trim, backgrounds, watermarks, `color_profile`, chained pipelines), or any other output format, makes the source clip to 8 bits instead (`clip`, not the configured operator) and lists `preserve_hdr` among the skipped options. For sources that are not HDR (16-bit PNG, 10/12-bit AVIF, UltraHDR) `preserve_hdr` is not implemented yet and is reported as skipped.
- **UltraHDR JPEG**: unchanged by WP-67. `uhdrload` decodes the SDR base image (8-bit sRGB) and the gain map is ignored; such files are not `hdr` for `/info`, and keeping the gain map is future work.
- **Fixtures**: `testdata/gen/hdr.go` writes the OpenEXR, Radiance, PFM and PPM fixtures in Go (libvips has no OpenEXR saver) from a procedural scene with a sun at 40 and about 5% of the pixels above 1.0.

## 2. Camera RAW strategy

Implemented in WP-49 (`codec/raw`, `internal/vips/raw.c`, `pipeline/plan` `rawOnLoad`, `pipeline/exec/raw.go`).

1. **Probe, header only.** `codec/raw.Probe` → `vips.ProbeRaw` → LibRaw `libraw_open_buffer`, then `libraw_adjust_sizes_info_only` (with `user_flip = 0`) for the demosaiced size in the stored orientation. Nothing is unpacked; about 0.1 ms per file. It returns size, `flip`, camera metadata and LibRaw's thumbnail list (`thumbs_list`: format, offset, length). The usable preview is the largest thumbnail LibRaw classifies as JPEG whose bytes parse as an 8-bit baseline/extended/progressive JPEG frame (`codec/raw.JPEGSize`, with the SOI marker taken as present the way `libraw_unpack_thumb` patches it); H.265 (CR3), lossless, arithmetic and 12-bit previews are skipped. The RAW's orientation is LibRaw's `flip` mapped to EXIF (inverse of LibRaw's `"50132467"` table): it applies to the demosaic and to the preview alike, both stored as the sensor reads.
2. **Decision, in the planner** (`plan.LoadStep.Preview`/`HalfSize`): the preview is used when, as displayed, it is at least `XI_FORMATS_RAW_PREVIEW_MIN_RATIO` (default 1.0) × the size the image is scaled to on both axes (DPR included), or when `enforce_thumbnail` is set. The preview is then JPEG-shrunk on load (2/4/8) down to that size.
3. **Otherwise the demosaic**: LibRaw `unpack` + `dcraw_process` + `dcraw_make_mem_image` in the shim, with dcrawload's parameters (8 bits, camera white balance, sRGB, no rotation), at `half_size` when the image is scaled to half its size or less (and shrink-on-load is not disabled), full size otherwise. libvips 8.18.6's `dcrawload` has no `half_size` option, so the shim drives LibRaw itself and `dcrawload` stays blocked with every other untrusted loader. A cancelled request stops LibRaw at its next processing stage (progress callback). The demosaic's working memory (2 bytes per sensor pixel + 8 per output pixel) is added to the plan's cost estimate checked against `XI_PROCESSING_MEMORY_BUDGET`.
4. **Metadata**: make, model, software, exposure time, f-number, ISO, focal length, capture time and lens model from LibRaw become `exif-ifdN-*` fields (replacing any EXIF the preview carried), so the encoder keeps or strips them by the metadata policy like any EXIF; stripped by default. `/info` reports them under `exif`. GPS is not exported.
5. **Limits**: RAW files are capped by `XI_LIMITS_MAX_SRC_BYTES` and, like every source, by `XI_LIMITS_MAX_SRC_PIXELS` on their demosaiced size; the demosaic path also by `XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS` (sensor width × height, default 60 MP; checked by the planner and again in the shim before unpacking); the preview load by `XI_LIMITS_MAX_SRC_PIXELS` on the JPEG header.
6. **Observability**: `xi_raw_decodes_total{path}` counts decodes by path (`preview`, `demosaic`, `demosaic-half`); with `XI_OBSERVABILITY_DEBUG_HEADERS` the response carries `X-Xi-Raw-Path`.

Measured on the builder image (arm64 Docker on Apple silicon, 1024 px JPEG output, plan + decode + encode; `BenchmarkRaw -count 10`, benchstat medians): Sony ILCE-7S ARW (12 MP) preview 37 ms, half-size demosaic 76 ms, full demosaic 311 ms; Pentax K-50 DNG (16 MP) preview 46 ms, half-size 382 ms, full 1.06 s. `ProbeRaw` is ~0.1 ms per file.

## 3. Animation handling

- Loaded as a vertical strip with `page-height`. `XI_LIMITS_MAX_ANIMATION_FRAMES` (default 256) and `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` (default 16 MP; total pixel limit applies to frames × frame pixels).
- Output keeps animation when source is animated, output format supports animation (GIF, WebP, AVIF sequences when libvips supports saving them, JXL when supported, MP4/WebM/HEVC video, §5.1) and `disable_animation` is false. Otherwise frame `page` (default 0) is used. A video's `vta` animation (§1.5) is an animated source too.
- Frame selection (WP-62; every dialect maps its page/frame parameters onto `page`/`pages`, URL-DIALECTS): when the animation is kept, `page` is its first frame and `pages` its frame count (imgproxy ignores both then; imgix `frame=` relies on it to pick one frame). When it is not kept (still output, `disable_animation`, a frame limit of 1), `page` selects one frame and `pages` stacks that many frames vertically into one image (imgproxy Pro), exactly like the pages of a multi-page still. `page` past the last frame clamps to it; `pages:-1` runs to the end.
- Frame delays and loop count preserved; per-frame geometric ops use `arrayjoin` over frames.
- `f:mp4|webm|hevc` / `fm=mp4|webm` / `f_mp4` / `f-mp4` / `ik-gif-video.mp4` / `.mp4` extension on an animated source → GIF→video (WP-76, `full` and `gpl` images; codec per [5.1](#51-video-output-wp-76)).

## 4. Metadata policy

Default: strip EXIF/XMP/IPTC, keep copyright fields (`XI_PROCESSING_KEEP_COPYRIGHT=true`), convert to sRGB and drop the ICC profile (`strip_color_profile=true`; with `false` the converted output is tagged with the built-in sRGB profile, sGrey for grey, and an sRGB IEC61966-2.1 source profile is kept as is), always reset orientation after auto-rotate (the output EXIF, when kept, says orientation 1, so a second pass does not rotate again). `color_profile` embeds sRGB/CMYK/custom profiles from `XI_PROCESSING_COLOR_PROFILES_DIR`. GPS is always removed when stripping, even with keep-copyright.

## 5. Output formats

Defaults are config values (`XI_FORMATS_<FMT>_*`) and can be overridden per request by imgproxy `*_options`, `format_quality`, and dialect equivalents.

| Format | Encoder | Quality default | Lossless | Effort / speed | Chroma subsampling | Progressive / interlace | Animation | Alpha |
|---|---|---|---|---|---|---|---|---|
| JPEG | libjpeg-turbo; `jpeg_options` trellis_quant, overshoot_deringing, optimize_scans and quant_table (0–8) are passed to libvips as imgproxy does and only take effect with a mozjpeg build (libjpeg-turbo ignores them with a GLib warning) | 80 | — | — | 4:2:0 default; 4:4:4 when q ≥ 90 or `no_subsample` | progressive default **on** (`XI_FORMATS_JPEG_PROGRESSIVE`) | — | flattened on background (white default) |
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
| MP4 / WebM / HEVC | FFmpeg (`full` and `gpl` images): VP9 (libvpx, BSD) for WebM; MP4 is AV1 (libaom) in `full` and H.264 (libx264) in `gpl`; HEVC-in-MP4 (libx265) only in `gpl`. See [Video output](#51-video-output-wp-76) | CRF mapped from quality (`XI_VIDEO_ENCODE_*_CRF`) | — | realtime `cpu-used` / x26x preset | 4:2:0 (even sides, padded) | MP4 `+faststart` | yes (WP-76) | WebM VP9 alpha (`yuva420p`); MP4 flattened |
| JSON | info endpoint / `fm=json` / `f=json` / `output=json` | | | | | | | |

`*_options` (WP-64 audit): `jpeg_options` (progressive, no_subsample, the mozjpeg arguments above), `png_options` (interlaced, quantize, quantization_colors → palette bit depth, as imgproxy's `vips_pngsave_go`), `webp_options` (compression lossy/near_lossless/lossless, smart_subsample, preset) and `avif_options` (subsample auto/on/off) are applied; tests inspect the output bytes (SOF0/SOF2 and the luma sampling factors, the PNG IHDR colour type and interlace flag and the PLTE size, the WebP VP8/VP8L chunk, the AVIF `av1C` subsampling bits). imgproxy defines no `jxl_options` or `tiff_options`, and `gif_options` was removed upstream (still accepted and ignored for old URLs).

`dpi` (WP-64) replaces the output resolution: PNG pHYs, TIFF resolution tags, JPEG Exif when metadata is kept and otherwise a JFIF APP0 density (libvips built with libexif writes JPEG resolution into Exif only, so the encoder inserts the 18-byte JFIF segment itself: one copy of the output, only for `dpi` requests). WebP, AVIF, HEIC, JPEG XL and GIF have no resolution field. It survives `strip_metadata`.

`enforce_thumbnail` (WP-64): a HEIF/AVIF still is always decoded from its embedded thumbnail when it has one, whatever the target size (imgproxy Pro; without the option the thumbnail is used only when it is large enough, like imgproxy's shrink-on-load). As an extension, a JPEG is decoded from its Exif (IFD1) JPEG thumbnail when the option is on, the thumbnail has the image's aspect ratio (±1%, which refuses letterboxed 160×120 camera thumbnails), and planning on it gives the same result size without enlarging anything; otherwise from the image. The Exif thumbnail carries no ICC profile. The executor reports it in `Result.Thumbnail`.

Not produced: PDF, PSD, RAW, JPEG 2000, EXR (open question if anyone needs JP2 output for Safari legacy; no).

### 5.1 Video output (WP-76)

The contract WP-76 (GIF → video) and the video packages implement. Which encoders exist depends on
the image flavor (`docker/base/README.md`, ADR 0005, ADR 0006 amendment 2026-09-27):

| Requested output | `slim` | `full` | `gpl` |
|---|---|---|---|
| `webm` | `Unsupported` | VP9 (`libvpx-vp9`) | VP9 (`libvpx-vp9`) |
| `mp4` | `Unsupported` | AV1 in MP4 (`libaom-av1`) | H.264 in MP4 (`libx264`, High, yuv420p, `+faststart`) |
| `hevc` / `h265` (MP4, `hvc1` tag) | `Unsupported` | `Unsupported` | HEVC in MP4 (`libx265`, Main) |

- **Detection, not configuration.** At startup the Go side probes the linked FFmpeg with
  `avcodec_find_encoder_by_name` for `libx264`, `libx265`, `libaom-av1` and `libvpx-vp9`, and
  `av_guess_format` for the `mp4` and `webm` muxers (`xi_ff_encoders` in the C shim of
  `internal/video/ffmpeg`, build tag `ffmpeg`, ADR 0010; never by parsing `ffmpeg -encoders`)
  and builds the table above from what it finds. The same binary therefore serves `full` and
  `gpl`; nothing reads the image label. Slim builds (no `ffmpeg` build tag) report every video
  output as `Unsupported`.
- `mp4` means "the best MP4 this image can write": `libx264` when present, otherwise
  `libaom-av1`. No config switch picks AV1 over H.264 in a gpl image in M3; if one is needed later
  it is `XI_FORMATS_MP4_CODEC=auto|h264|av1` (default `auto`).
- `hevc`/`h265` is a separate, explicit output (dialect values that name HEVC); it is never
  chosen by `format=auto` or `best`, and it is `Unsupported` (the usual 4xx for an unavailable
  format) without `libx265`.
- `webm` is VP9 in every video-capable image.
- The startup log line (`"msg":"video"`, `"encoders":"vp9,av1"` or `"vp9,av1,h264,hevc"`) lists
  the detected video encoders, so an operator can see which flavor is running; the debug header
  `X-Xi-Video-Codec` names the codec of each video response. (There is no `/info` capability
  output to list them in.)

**Implemented (WP-76).** The state per flavour, verified with `ffprobe` in each builder
(`TestVideoOutput`):

| | `slim` | `full` | `gpl` |
|---|---|---|---|
| `f:webm` | 422 `unsupported` | VP9 in WebM | VP9 in WebM |
| `f:mp4` | 422 `unsupported` | AV1 in MP4 (`X-Xi-Video-Codec: av1`) | H.264 High in MP4 (`h264`) |
| `f:hevc` (`h265`) | 422 `unsupported` | 422 `unsupported` | HEVC Main in MP4, `hvc1` |

- **Sources.** Any animated result: GIF and animated WebP (every frame), a video's `vta`
  animation, and the frames kept by `page`/`pages`. A still result becomes a one-frame video. APNG
  and AVIF sequences load as their first frame (libvips), so they give one-frame videos.
- **Frames.** libvips renders one frame at a time into C memory (`WritePixels`), libswscale
  converts RGB(A) to BT.709 limited-range `yuv420p` (`yuva420p` for WebM with alpha; MP4 has no
  alpha and is flattened on white, or on the request's background by the planner), and the
  encoder gets it; nothing but the current frame is held. An odd width or height is padded by one
  repeated column or row (4:2:0 needs even sides; `X-Xi-Result-Width` reports the padded size).
- **Timing.** Timestamps in milliseconds from the source's per-frame delays; a delay of 10 ms or
  less (0 included) plays as 100 ms, as browsers show such GIF frames. Each packet carries its
  frame's duration, so the last frame lasts as long as in the GIF and the duration matches the
  source (`TestVideoOutput`: 0.600 s for the 6-frame fixture, 4.000 s for a 100-frame GIF). The
  loop count is ignored (a `<video>` loops with its `loop` attribute).
- **Encoders.** One thread each (parallelism comes from the worker semaphore); VP9 and AV1 in their
  realtime modes (`deadline=realtime` / `usage=realtime`, `cpu-used` = `XI_VIDEO_ENCODE_VP9_SPEED` /
  `_AV1_SPEED`, default 8), x264/x265 with `XI_VIDEO_ENCODE_H264_PRESET` / `_HEVC_PRESET`
  (default `veryfast`, H.264 High profile). Constant quality: the request's quality (or
  `format_quality`) maps onto the codec's CRF around `XI_VIDEO_ENCODE_<CODEC>_CRF` (80 is the
  configured CRF, 100 a quarter of it, 1 the codec's worst: 63 for VP9/AV1, 51 for H.264/HEVC);
  without one the configured CRF applies (VP9 36, AV1 36, H.264 25, HEVC 28). A keyframe every
  10 s at most. `max_bytes` and auto-quality do not search video (reported as skipped).
- **Muxing.** WebM or MP4 written through a custom `AVIOContext` into a growable C buffer (no
  temporary file); MP4 gets `+faststart` (the `moov` box in front), which FFmpeg's second pass
  does by reading the buffer back through the context's `io_open` (no other file or URL can be
  opened), and HEVC the `hvc1` tag Apple players need. The finished file is copied once into a
  libvips buffer that the response sends. `Content-Type` is `video/webm` or `video/mp4`,
  `Content-Disposition` gets `.webm` or `.mp4` (HEVC too), and ETag and the result cache treat it
  like any image.
- **Limits and cancellation.** `XI_VIDEO_ENCODE_MAX_FRAMES` (1000), `_MAX_DURATION` (1 m) and
  `_MAX_PIXELS` (8.3 MP per frame) are 422 `limit_exceeded` before encoding. The request context
  sets the encoder's abort flag (checked per frame, per packet and by the muxer's I/O callbacks).
  `TestVideoOutputNoLeak` / `TestEncoderNoLeak`: 200 encodes leave no encoder, libvips object or
  tracked byte behind, and glibc's heap does not grow.
- **Negotiation.** Only an explicit format picks video; `format=auto` and `best` never do (their
  candidates are image formats, §6.2–6.3). `XI_VIDEO_ENABLED=false` turns video output off too.
- **Dialects.** imgproxy `f:mp4` (Pro; `webm` and `hevc`/`h265` are xtandard-image extensions,
  also as the URL extension), imgix `fm=mp4|webm`, Cloudinary `f_mp4|f_webm` and the
  `.mp4`/`.webm` extension on the `image` asset type (`fl_animated` is the default), ImageKit
  `f-mp4|f-webm` and `<gif>/ik-gif-video.mp4`; wsrv, Cloudflare Images, thumbor, imaginary and the
  other dialects have no video output value (URL-DIALECTS.md).

Measured on the full and gpl builders (arm64 Docker on Apple silicon, one thread, `-count=10`,
benchstat medians; raw output in `internal/pipeline/exec/testdata/bench-video-out-*.txt` and
`internal/video/ffmpeg/testdata/bench-frames.txt`):

| Benchmark (end to end through the executor) | full | gpl | output |
|---|---|---|---|
| 100-frame 320×180 GIF (2.2 MiB, 40 ms frames) → WebM (VP9) | 191 ms (1.9 ms/frame) | 193 ms | 72.7 KiB (3.2% of the GIF) |
| same GIF → MP4 | AV1: 305 ms (3.0 ms/frame) | H.264: 238 ms (2.4 ms/frame) | AV1 38.9 KiB (1.7%), H.264 46.6 KiB (2.0%) |
| 3×3 sprite of a 720p clip, 160×90 tiles → PNG | H.264 59 ms, VP9 39 ms, AV1 85 ms | | |
| 10-frame 320×180 animation of a 720p clip → WebP | H.264 139 ms, VP9 89 ms, AV1 133 ms | | |

| `BenchmarkFrames` (ten frames 0–0.9 s of a 720p clip at 320×180) | one handle, forward decoding (`Session.Frames`) | ten `Thumbnail` calls (open, probe, seek each) |
|---|---|---|
| H.264 (one keyframe, B-frames) | 52.7 ms | 324.5 ms (6.2×) |
| VP9 (keyframe every 0.48 s) | 17.4 ms | 44.5 ms (2.6×) |
| AV1 (keyframe every 0.48 s) | 41.3 ms | 130.9 ms (3.2×) |

The GIF timings include decoding the GIF with libvips and the per-frame pipeline; the encoders run
single-threaded in their realtime modes. With the ffprobe tests: the 6-frame 96×72 fixture GIF
(8038 bytes, 0.6 s) becomes a 2905-byte WebM, a 2776-byte AV1 MP4 (full) or a 3095-byte H.264 MP4
and a 4530-byte HEVC MP4 (gpl), all 6 frames and 0.600 s long.
- The `gpl` flavor exists for these encoders only. Its licensing and patent caveats are in
  `docker/base/README.md` and the image's `/opt/xtandard/share/licenses/LICENSE-GPL.md`.

## 6. Format negotiation and best-format

### 6.1 Inputs to the decision

1. **Explicit format** in URL (option, extension, `fm`, `f_`, `output`) wins, except `auto`/`best`.
2. **Accept header**: parsed with q-values; `image/avif`, `image/webp`, `image/jxl` considered. `*/*` alone does not imply modern formats. Enabled per format via `XI_FORMATS_AUTO_{AVIF,WEBP,JXL}` (default true, true, false) and `XI_FORMATS_ENFORCE_*` (use it even when the source format could be kept).
3. **Content analysis** (probe: alpha, animation, pixel count; for `best` also the processed image's *complexity score*, `negotiate.Analyze`, §6.3): graphic below `XI_FORMATS_BEST_COMPLEXITY_THRESHOLD` − 0.75, photo above it + 0.75, mixed in between.
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
- `format=auto` classes every still image as a photo (the probe knows no content class); only `best` analyses the processed image (§6.3).
- `Vary: Accept` is set whenever Accept was consulted (any enabled, applicable modern format, SVG passthrough check, or strict Accept), including when the fallback won; never when the format is explicit or the source is passed through.

`Save-Data: on` or `ECT: slow-2g|2g` lowers the quality by `XI_FORMATS_SAVE_DATA_QUALITY_DELTA` (default −15) when the URL did not fix quality.

### 6.3 `best` (M2, WP-57)

`format=best` (imgproxy `f:best` / `format:best`, query dialect `f=best` / `format=best`; the other dialects' `auto` values mean §6.2 negotiation only) encodes up to three candidates and returns the smallest. imgproxy Pro's semantics: measure the complexity to decide between lossless and lossy encodings, then try several formats and keep the smallest.

1. **Candidates** (`negotiate.Decide` with `ReasonBest`): the §6.2 rows computed with the request's Accept, enforce/auto flags, `XI_FORMATS_PREFERRED` and cost guards, before processing. A still image gets two lists, photo (the alpha or opaque row, lossy) and graphic (lossless: WebP lossless, JPEG XL lossless when enabled and accepted, AVIF lossless only when enforced, then PNG); GIF is never a still candidate. An animation gets the animated row only (WebP, AVIF sequences when supported, GIF). Each list is capped at 3. `Vary: Accept` is set because Accept shaped the lists; the ETag covers the lists (`Decision.BestKey`), not only the first candidate.
2. **Analysis** (`codec/encode` `EncodeBest`, on the processed image, first frame of an animation): the image is rendered to memory once, then a copy of at most 256 px on the longer side is taken by nearest neighbour (no invented colours), converted to 8-bit sRGB, and scored in pure Go (`negotiate.Analyze`): the Shannon entropy (bits) of the Sobel gradient magnitudes of the luma (premultiplied by alpha, so alpha edges count) plus ¼·log2 of the unique RGBA colour count. It also reports the edge density (share of pixels with gradient ≥ 32/255) and whether any pixel is translucent. `XI_FORMATS_BEST_COMPLEXITY_THRESHOLD` = 0 skips it (every image is a photo, like imgproxy).
3. **Class → candidates** (`Decision.BestCandidates`): graphic → the graphic list (PNG quantised when the sample has ≤ 256 colours); photo → the photo list; mixed → the two interleaved (lossy 1st, lossless 1st, lossy 2nd). Animation: the animated row, WebP/AVIF saved lossless for graphics, both variants for mixed.
4. **Encode and pick**: every candidate is encoded with the request's quality settings: its `quality`/`format_quality`/config quality, or the auto-quality search when one applies (§6.4), and `max_bytes`. A try *meets the bar* when its auto-quality target was met (or no search ran: configured quality and lossless saves count as met) and it fits `max_bytes`. The smallest try that meets the bar wins, else the smallest; ties go to the earlier (preferred) candidate. A candidate this build cannot encode (e.g. HEIC without `XI_FORMATS_HEIC_ENCODE`) is skipped.
5. **Cap**: above `XI_FORMATS_BEST_MAX_PIXELS` output pixels (all frames; default 8 MP) nothing is analysed and the negotiated first candidate (`Decision.Format`) is encoded once; the response lists `best_max_pixels` as skipped.
6. **Reporting**: `X-Xi-Best` (debug headers) summarises the class, score, colour count and every try, e.g. `graphic c=1.83 colors=5; webp~=246*, png~=386` (`~` lossless, `q<n>` quality, `*` chosen, `!` missed the bar); `X-Xi-Quality` and `Content-Type` give the chosen quality and format. Result-cache hits replay both. HEAD responses report the first candidate's type (nothing is encoded).

Calibration (`TestEncodeBestCorpus`, fixtures at 320–1024 px and synthetic graphics): photos score 7.2–7.9 (JPEG, PNG 8/16-bit, WebP, alpha); text 0.96, flat shapes 1.16, a bar chart 1.56, the palette/GIF/lossless-WebP graphic 1.8–2.0 (its animated GIF 2.02), the SVG logo rendered at 384 px 4.55 (graphic) and at 96 px 5.21 (mixed: its gradient sky and anti-aliased edges weigh as much as its flat areas, and lossy WebP won). A greyscale photo has at most 256 colours and tends to land in the mixed band, where both kinds are tried.

Cost against plain negotiation (one encode of the negotiated format), benchstat `-count=10`, `BenchmarkEncodeBest`, base-image builder on arm64, 640 px, configured quality, Chrome-like Accept (output in `internal/codec/encode/testdata/bench-best.txt`):

| Image | negotiate | best | tries | output |
|---|---|---|---|---|
| photo | 291 ms (AVIF) | 386 ms (+33%; +14% in an earlier run, the spread is ±16%) | AVIF, WebP, JPEG | same AVIF, 13.6 KiB |
| bar chart | 197 ms (lossy AVIF) | 28 ms (−86%) | WebP lossless, PNG | WebP lossless, 2.41 KiB vs 2.30 KiB lossy AVIF |

The analysis itself (256 px sample, entropy and colour count) costs under 1 ms and about 0.9 MiB of Go allocations (luma and colour planes, colour hash set); for photos the extra cost is the WebP and JPEG saves on top of AVIF's. For graphics `best` is faster than negotiation because lossless WebP and PNG are much cheaper than AVIF. With auto-quality every candidate runs its own search (§6.4 costs per format, added). Results are cached by the result cache (keyed by the Accept fingerprint), so the cost is paid once per variant.

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
