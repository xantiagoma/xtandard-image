# Comparison with other image servers

How xtandard-image compares with imgproxy (OSS and Pro), thumbor, imaginary and weserv, feature by feature. The xtandard-image column describes the current `main` branch and the latest prerelease ([CHANGELOG](../CHANGELOG.md)). **`full` image** means only the `full` and `gpl` images have the feature ([image flavours](GETTING-STARTED.md#choose-a-flavour)). Features that are planned but not built yet are in the [roadmap](ROADMAP.md), not here.

Legend for the other columns: **yes**, **paid** (paid tier only), **partial**, **no**, and **—** (not compared yet). Their data comes from each project's public docs and source, as of September 2026. Corrections are welcome: open an issue with a link to the source.

| Feature | xtandard-image | imgproxy OSS | imgproxy Pro | thumbor | imaginary | weserv |
|---|---|---|---|---|---|---|
| License | MIT | Apache-2.0 | commercial | MIT | MIT | BSD-3 |
| **URLs and security** | | | | | | |
| imgproxy URL dialect (every option parsed; detection and video options need the `full` image) | yes | yes | yes | no | no | no |
| Query-string dialect (`?url=…&w=…`, imaginary endpoints) | yes | no | no | no | yes | partial (own parameters) |
| HTTP `QUERY` (RFC 10008) on the query dialect: the parameters in the request body (form or JSON), same signature, limits and cache entry as the GET URL ([URL-DIALECTS §4.4](URL-DIALECTS.md#44-http-query)); opt-in in the clients, since many CDNs do not cache or forward QUERY | yes | no | no | no | no | no |
| Presets, only-presets mode | yes | yes | yes | — | — | — |
| Signed URLs (imgproxy HMAC, `xi_sig`, imaginary `sign`) | yes | yes | yes | yes | yes | no |
| Encrypted source URLs (`enc/`) | yes | no | paid | no | no | no |
| Mandatory source allowlist, SSRF protection incl. DNS rebinding | yes | partial (optional allowlist) | partial (optional allowlist) | — | — | — |
| imgix URLs (host or prefix mount, `s`/`expires` signing, `64` parameters, watermarks) | yes | no | no | no | no | no |
| Cloudinary / ImageKit / Cloudflare (incl. Images delivery) / Bunny / Thumbor / wsrv URLs, with their signing schemes | yes | no | no | Thumbor only | no | wsrv only |
| AWS Serverless Image Handler URLs (base64 JSON, thumbor and query forms) | yes | — | — | — | — | — |
| Other unpic providers (Next.js, Netlify, Vercel, Contentful, Shopify, … 21 in all) | yes | no | no | no | no | no |
| **Sources** | | | | | | |
| http(s), S3, GCS, Azure Blob, local filesystem | yes | yes | yes | partial (plugins) | partial (local) | partial (http) |
| FTP / FTPS / SFTP | yes | no | no | no | no | no |
| Multiple named sources | yes | partial | partial | partial | no | no |
| **Formats** | | | | | | |
| JPEG, PNG, GIF, WebP, AVIF, TIFF, BMP, ICO, SVG input | yes | yes | yes | — | — | — |
| HEIC input | yes | yes | yes | partial (plugin) | yes | yes |
| JPEG XL, JPEG 2000, UltraHDR input | yes | partial (JPEG XL) | — | — | — | — |
| AVIF / WebP / JXL output + Accept negotiation | yes | yes | yes | partial | partial | partial |
| Animated GIF / WebP (resize every frame), page and frame selection | yes | yes | yes | — | — | — |
| Camera RAW input (embedded preview or LibRaw demosaic) | yes | no | paid (loading only, v4.0) | no | no | no |
| PDF (PDFium) / PSD thumbnails | yes | no | paid | no | partial (PDF) | partial (PDF) |
| HDR input (OpenEXR, Radiance, PPM/PFM; tone-mapped or `preserve_hdr`) | yes | — | — | — | — | — |
| Best-format and auto-quality (DSSIM) | yes | no | paid | no | no | no |
| Video thumbnails (frame at a time, keyframe, best frame; `/info` video metadata) | yes (`full` image) | no | paid | partial (ffmpeg engine) | no | no |
| Video sprites and animations (`vtt`, `vta`) | yes (`full` image) | no | paid | no | no | no |
| GIF → MP4/WebM | yes (WebM/VP9 and AV1-in-MP4 in `full`, H.264 MP4 and HEVC in `gpl`) | no | paid (MP4) | no | no | no |
| Video transformations (`/video` and `/q/video`: trim, resize/crop/pad with gravity, fps, rotate/flip, fades, CRF or bit-rate cap, keyframe interval, MP4/WebM, codec choice; `Range`; signed only by default) | yes (`full`/`gpl` image, `XI_VIDEO_TRANSFORM_ENABLED`) | no | — | no | no | no |
| Audio (mute; M4A, MP3, Opus/Ogg, FLAC, WAV outputs from video or audio sources; sample rate, channels, volume, EBU R128 loudness normalisation, fades) and waveform pictures (PNG/WebP) | yes (`full`/`gpl` image) | no | — | no | no | no |
| HLS and DASH (VOD): ABR ladders, CMAF segments transcoded on request with prefetch, signed manifests with ticketed child URLs (imgproxy `/video …@m3u8`, Cloudinary `sp_`, ImageKit `ik-master`) | yes (`full`/`gpl` image) | no | — | no | no | no |
| Video presets (`XI_VIDEO_TRANSFORM_PRESETS`: `pr:` on `/video`, `preset=`, Cloudinary `t_`, ImageKit `n-`) and public video without signing every URL (`XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=restricted`: unsigned URLs only as their presets make them; streams and raw only from presets) | yes | no | no | no | no | no |
| **Processing** | | | | | | |
| Resize, crop, gravity, smart crop, focus point (fractions or source pixels) | yes | yes | yes | yes | yes | yes |
| Extend, padding, trim | yes | yes | yes | — | — | — |
| Rotate (any angle), flip, EXIF auto-orientation | yes | yes | yes | yes | yes | yes |
| Blur, sharpen, pixelate | yes | yes | yes | partial (no pixelate) | partial | partial |
| Unsharp masking, blur areas, progressive blur | yes | no | paid | — | — | — |
| Crop aspect ratio, border, rounded corners, shape masks | yes | — | — | — | — | — |
| Watermark (image: position, offsets, scale, opacity, tiling) | yes | yes | yes | yes | yes | no |
| Metadata policy (strip, keep copyright, ICC → sRGB, `color_profile`) | yes | yes | yes | — | — | — |
| Colour adjustments, duotone, gradients | yes | no | paid | yes | no | yes |
| Chained pipelines | yes | no | paid | no | yes (`/pipeline`) | no |
| Face and object detection (face/object gravity, `objects_position`, `crop_objects`, blur/pixelate/draw detections, `/info` objects; every dialect's face options; on video frames and in chained pipelines) | yes (full image; slim answers 422) | no | paid | partial (OpenCV faces) | no | no |
| Image classification (`/info` `classify`: top-k ImageNet-1k classes from MobileNetV3-Large on CPU, run only when asked; class filter) | yes (full and gpl images; slim leaves it out and says so in `X-Xi-Ignored`) | no | paid (`-ml` images) | no | no | no |
| Watermarks (text, per-request URL, size, shadow, rotation, chessboard) | yes | no | paid | partial | partial (text) | no |
| **Serving and operations** | | | | | | |
| `/info` endpoint (dimensions, EXIF, IPTC, XMP) | yes | no | paid | partial (`/meta`) | yes | partial (`output=json`) |
| `/info` Pro fields (palette, dominant colours, blurhash, thumbhash, perceptual hash, hashsums) | yes | no | paid | — | — | — |
| ETag, 304, HEAD, `Cache-Control`, `Vary`, `Content-DPR` | yes | yes | yes | — | — | — |
| Client hints (`Sec-CH-DPR`, `Sec-CH-Width`, `Save-Data`, `Accept-CH`) | yes | — | — | — | — | — |
| `raw` / `skip_processing` passthrough, fallback image | yes | yes | yes | — | — | — |
| Admission control (queue → 429, worker timeout → 503) | yes | yes | yes | — | — | — |
| Prometheus metrics, OpenTelemetry, health and readiness | yes | yes | yes | partial | partial | no |
| Multi-arch Docker image (cosign-signed, SBOM, provenance) | yes (the prereleases so far are unsigned) | yes | yes | — | — | — |
| Built-in result cache (memory, disk, S3, Redis) | yes (optional) | no | paid (v4) | yes | no | partial (nginx) |
| Config file (YAML, JSON, TOML) with a JSON Schema; OpenAPI 3.1 document | yes | — | — | — | — | — |
| Playground web UI | yes | — | — | — | — | — |
| TypeScript URL builder and client (`@xtandard/image`) | yes (not on npm yet) | — | — | — | — | — |

Sources: https://imgproxy.net/features/, https://docs.imgproxy.net/, https://thumbor.readthedocs.io/, https://github.com/h2non/imaginary, https://wsrv.nl/docs/. Corrections are welcome.
