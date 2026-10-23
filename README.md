# xtandard-image

> **Status: alpha.** Milestones **M1, M2 and M3 are on `main`**: every URL dialect below is served end to end from every source type, with the imgproxy processing set apart from detection, video and the advanced watermarks (chained pipelines, colour adjustments, geometry and filter extras, best format and auto-quality), camera RAW, PDF, PSD and HDR inputs, a memory/disk/S3/Redis result cache, `/info` with the Pro fields, client hints, signing, encryption, a JSON Schema for the config file and an OpenAPI document. M3 (merged in #80–#92) added the `full` image with FFmpeg and ONNX Runtime, face/object detection with detection-based crops and filters, video thumbnails, the imgix mount, advanced and text watermarks, the playground, the TypeScript package, the Pages/SchemaStore site build, the `gpl` image flavour and a stabilisation pass. In progress: video tiles/animation and GIF → video. No release has been tagged yet, so you build the image yourself (below), and configuration and URL options may still change before 1.0.

A self-hosted image processing proxy in Go and libvips. The goal is to offer every imgproxy Pro processing feature for free under the MIT license, support more input formats than anything else in the category, and accept many URL dialects: imgproxy, imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Thumbor, wsrv/weserv, AWS Serverless Image Handler and the rest of the [unpic](https://unpic.pics/providers/) provider list. It ships as a ready-to-run Docker image for Dokploy, Coolify and Kubernetes, with no build step.

## Why

- **imgproxy URLs work unchanged.** Signing, `enc/` sources, presets, chained pipelines, `/info` and the processing options, Pro ones included, work on `main`. Options that need detection or video (`g:obj`, `crop_objects`, `vts`, …) take effect in the `full` image (the slim image answers 422 for them); the advanced watermark options (`wmu`, `wmt`, `wms`, `wmr`, `wmsh`, chessboard) work too.
- **Point your front end at it and keep your code.** An unpic `<Image>` configured for Cloudinary, ImageKit, Cloudflare, Bunny, wsrv, Netlify, Next.js or any of the other unpic providers can target xtandard-image as if it were that provider (on `main`); imgix URLs are parsed on `main` and served once the imgix mount (#87) is merged. Thumbor and AWS Serverless Image Handler URLs work too.
- **Wide format support.** Inputs: JPEG, PNG, GIF (animated), WebP (animated), AVIF, HEIC, JPEG XL, TIFF, BMP, ICO, SVG, JPEG 2000, UltraHDR, camera RAW (CR3, NEF, ARW, DNG and more, embedded preview first), PDF, PSD/PSB, OpenEXR, Radiance HDR, PBM/PGM/PPM/PAM/PFM, JPEG XR, TGA, PCX, DDS (BC1–BC7 including BC6H HDR, uncompressed and float), QOI, SGI, Sun raster, XBM, XPM, WBMP and ICNS, designer files (GIMP XCF flattened; OpenRaster and Krita composites, Procreate and Paint.NET thumbnails, Sketch previews and SVGZ, see [FORMATS §1.7](docs/FORMATS.md#17-designer-files-wp-107)), and EPS/PostScript/Illustrator in the opt-in `gpl` image with `XI_FORMATS_EPS_ENABLED=true` (AGPL Ghostscript in a sandboxed subprocess, off by default; elsewhere only a DOS EPS preview, see [FORMATS §1.6](docs/FORMATS.md#16-long-tail-inputs-wp-97) and [ADR 0011](docs/adr/0011-ghostscript-subprocess.md)). Outputs: JPEG, PNG, GIF, WebP, AVIF, JPEG XL, TIFF, BMP, ICO (HEIC opt-in). Video input and GIF → video output are M3 work for the `full` image.
- **Secure by default.** It refuses to start without a source allowlist. It also protects against SSRF (including DNS rebinding), enforces decompression-bomb and pixel limits, and sanitises SVG.
- **CDN-friendly and stateless.** Responses carry correct `Cache-Control`, `ETag`, `Vary`, `Content-DPR` and `Content-Disposition` headers, with 304 before any processing. Requests for the same original are coalesced. An optional result cache (memory, disk, S3, Redis tiers) is built in.
- **No GPL in the default images.** `slim` and `full` use only LGPL, BSD, MIT and Apache dependencies (see [ADR 0006](docs/adr/0006-license-policy.md)). An opt-in `gpl` flavour with H.264/HEVC encoders is being built as a separate image.

## Status and comparison

The xtandard-image column is the state as of 2026-09-28: **done (M1)** / **done (M2)** / **done (M3)** / **done (M5)** means merged on `main` and working in the image you build today (**full image** means only the `full`/`gpl` images have it); **in progress** and **planned** follow the [roadmap](docs/ROADMAP.md), which tracks every work package with its status. Competitor legend: **yes**, **paid** (paid tier only), **partial**, **no**, **—** (not compared yet). Competitor data comes from their public docs and source as of September 2026.

| Feature | xtandard-image | imgproxy OSS | imgproxy Pro | thumbor | imaginary | weserv |
|---|---|---|---|---|---|---|
| License | MIT | Apache-2.0 | commercial | MIT | MIT | BSD-3 |
| **URLs and security** | | | | | | |
| imgproxy URL dialect (every option parsed; detection and video options need the `full` image) | done (M1, M2, M3) | yes | yes | no | no | no |
| Query-string dialect (`?url=…&w=…`, imaginary endpoints) | done (M1) | no | no | no | yes | partial (own parameters) |
| Presets, only-presets mode | done (M1) | yes | yes | — | — | — |
| Signed URLs (imgproxy HMAC, `xi_sig`, imaginary `sign`) | done (M1) | yes | yes | yes | yes | no |
| Encrypted source URLs (`enc/`) | done (M1) | no | paid | no | no | no |
| Mandatory source allowlist, SSRF protection incl. DNS rebinding | done (M1) | partial (optional allowlist) | partial (optional allowlist) | — | — | — |
| imgix URLs (host or prefix mount, `s`/`expires` signing, `64` parameters, watermarks) | done (M2 parser, M3 mount) | no | no | no | no | no |
| Cloudinary / ImageKit / Cloudflare (incl. Images delivery) / Bunny / Thumbor / wsrv URLs, with their signing schemes | done (M2) | no | no | Thumbor only | no | wsrv only |
| AWS Serverless Image Handler URLs (base64 JSON, thumbor and query forms) | done (M2) | — | — | — | — | — |
| Other unpic providers (Next.js, Netlify, Vercel, Contentful, Shopify, … 21 in all) | done (M2) | no | no | no | no | no |
| **Sources** | | | | | | |
| http(s), S3, GCS, Azure Blob, local filesystem | done (M1) | yes | yes | partial (plugins) | partial (local) | partial (http) |
| FTP / FTPS / SFTP | done (M1) | no | no | no | no | no |
| Multiple named sources | done (M1) | partial | partial | partial | no | no |
| **Formats** | | | | | | |
| JPEG, PNG, GIF, WebP, AVIF, TIFF, BMP, ICO, SVG input | done (M1) | yes | yes | — | — | — |
| HEIC input | done (M1) | yes | yes | partial (plugin) | yes | yes |
| JPEG XL, JPEG 2000, UltraHDR input | done (M1) | partial (JPEG XL) | — | — | — | — |
| AVIF / WebP / JXL output + Accept negotiation | done (M1) | yes | yes | partial | partial | partial |
| Animated GIF / WebP (resize every frame), page and frame selection | done (M1, M2) | yes | yes | — | — | — |
| Camera RAW input (embedded preview or LibRaw demosaic) | done (M2) | no | paid (loading only, v4.0) | no | no | no |
| PDF (PDFium) / PSD thumbnails | done (M2) | no | paid | no | partial (PDF) | partial (PDF) |
| HDR input (OpenEXR, Radiance, PPM/PFM; tone-mapped or `preserve_hdr`) | done (M2) | — | — | — | — | — |
| Best-format and auto-quality (DSSIM) | done (M2) | no | paid | no | no | no |
| Video thumbnails (frame at a time, keyframe, best frame; `/info` video metadata) | done (M3, `full` image) | no | paid | partial (ffmpeg engine) | no | no |
| Video sprites and animations (`vtt`, `vta`) | done (M3, `full` image) | no | paid | no | no | no |
| GIF → MP4/WebM | done (M3: WebM/VP9 and AV1-in-MP4 in `full`, H.264 MP4 and HEVC in `gpl`) | no | paid (MP4) | no | no | no |
| Video transformations (`/video` and `/q/video`: trim, resize/crop/pad with gravity, fps, rotate/flip, fades, CRF or bit-rate cap, keyframe interval, MP4/WebM, codec choice; `Range`; signed only by default) | done (M5, `full`/`gpl` image, `XI_VIDEO_TRANSFORM_ENABLED`) | no | — | no | no | no |
| Audio (mute; M4A, MP3, Opus/Ogg, FLAC, WAV outputs from video or audio sources; sample rate, channels, volume, EBU R128 loudness normalisation, fades) and waveform pictures (PNG/WebP) | done (M5, `full`/`gpl` image) | no | — | no | no | no |
| HLS and DASH (VOD): ABR ladders, CMAF segments transcoded on request with prefetch, signed manifests with ticketed child URLs (imgproxy `/video …@m3u8`, Cloudinary `sp_`, ImageKit `ik-master`) | done (M5, `full`/`gpl` image) | no | — | no | no | no |
| Video presets (`XI_VIDEO_TRANSFORM_PRESETS`: `pr:` on `/video`, `preset=`, Cloudinary `t_`, ImageKit `n-`) and public video without signing every URL (`XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=restricted`: unsigned URLs only as their presets make them; streams and raw only from presets) | done (M5, WP-132) | no | no | no | no | no |
| **Processing** | | | | | | |
| Resize, crop, gravity, smart crop, focus point (fractions or source pixels) | done (M1, M2) | yes | yes | yes | yes | yes |
| Extend, padding, trim | done (M1) | yes | yes | — | — | — |
| Rotate (any angle), flip, EXIF auto-orientation | done (M1, M2) | yes | yes | yes | yes | yes |
| Blur, sharpen, pixelate | done (M1) | yes | yes | partial (no pixelate) | partial | partial |
| Unsharp masking, blur areas, progressive blur | done (M2) | no | paid | — | — | — |
| Crop aspect ratio, border, rounded corners, shape masks | done (M2) | — | — | — | — | — |
| Watermark (image: position, offsets, scale, opacity, tiling) | done (M1) | yes | yes | yes | yes | no |
| Metadata policy (strip, keep copyright, ICC → sRGB, `color_profile`) | done (M1, M2) | yes | yes | — | — | — |
| Colour adjustments, duotone, gradients | done (M2) | no | paid | yes | no | yes |
| Chained pipelines | done (M2) | no | paid | no | yes (`/pipeline`) | no |
| Face and object detection (face/object gravity, `objects_position`, `crop_objects`, blur/pixelate/draw detections, `/info` objects; every dialect's face options; on video frames and in chained pipelines) | done (M3, full image; slim answers 422) | no | paid | partial (OpenCV faces) | no | no |
| Image classification (`/info` `classify`: top-k ImageNet-1k classes from MobileNetV3-Large on CPU, run only when asked; class filter) | done (M4, full and gpl images; slim leaves it out and says so in `X-Xi-Ignored`) | no | paid (`-ml` images) | no | no | no |
| Watermarks (text, per-request URL, size, shadow, rotation, chessboard) | done (M3) | no | paid | partial | partial (text) | no |
| **Serving and operations** | | | | | | |
| `/info` endpoint (dimensions, EXIF, IPTC, XMP) | done (M1) | no | paid | partial (`/meta`) | yes | partial (`output=json`) |
| `/info` Pro fields (palette, dominant colours, blurhash, thumbhash, perceptual hash, hashsums) | done (M2) | no | paid | — | — | — |
| ETag, 304, HEAD, `Cache-Control`, `Vary`, `Content-DPR` | done (M1) | yes | yes | — | — | — |
| Client hints (`Sec-CH-DPR`, `Sec-CH-Width`, `Save-Data`, `Accept-CH`) | done (M2) | — | — | — | — | — |
| `raw` / `skip_processing` passthrough, fallback image | done (M1) | yes | yes | — | — | — |
| Admission control (queue → 429, worker timeout → 503) | done (M1) | yes | yes | — | — | — |
| Prometheus metrics, OpenTelemetry, health and readiness | done (M1) | yes | yes | partial | partial | no |
| Multi-arch Docker image (cosign-signed, SBOM, provenance) | done (M1; first tag pending) | yes | yes | — | — | — |
| Built-in result cache (memory, disk, S3, Redis) | done (M2, optional) | no | paid (v4) | yes | no | partial (nginx) |
| Config file (YAML, JSON, TOML) with a JSON Schema; OpenAPI 3.1 document | done (M2) | — | — | — | — | — |
| Playground web UI | done (M3) | — | — | — | — | — |
| TypeScript URL builder and client (`@xtandard/image`) | done (M3; npm publish pending the first release) | — | — | — | — | — |

Sources: https://imgproxy.net/features/, https://docs.imgproxy.net/, https://thumbor.readthedocs.io/, https://github.com/h2non/imaginary, https://wsrv.nl/docs/. Corrections are welcome.

## Quick start

CI runs the commands of this section exactly as written, except the `git clone` (`scripts/quickstart.sh`; locally `task quickstart`). You need Docker, `curl` and `openssl`.

### 1. Get the image

No release is published yet. Build the image from a clone (a few minutes; the Dockerfile pulls the prebuilt libvips base image `ghcr.io/xantiagoma/xtandard-image-base`):

```sh
git clone https://github.com/xantiagoma/xtandard-image.git
cd xtandard-image
```

```sh quickstart-build
docker build -t xtandard-image .
```

Once the first release is out, use `ghcr.io/xantiagoma/xtandard-image:slim` (linux/amd64 and linux/arm64) instead of building, and replace `xtandard-image` with it in the commands below.

Image variants (all on the same `ubuntu:noble` + libvips 8.18 base, built from `docker/base`):

| Variant | Contents | Status |
|---|---|---|
| `slim` | every still format: JPEG, PNG, GIF, WebP, AVIF, HEIC (decode), JPEG XL, TIFF, JPEG 2000, UltraHDR, SVG, PDF (PDFium), RAW (LibRaw), OpenEXR, PSD, JPEG XR (jxrlib) and the legacy formats (TGA, PCX, DDS, QOI, SGI, Sun raster, XBM, XPM, PAM, WBMP, ICNS) and the designer files (XCF, ORA, KRA, Procreate, Sketch, SVGZ, Paint.NET) | default |
| `full` | slim + FFmpeg 8.1 (LGPL only: video decode, VP8/VP9 and AV1 encode, no H.264/HEVC encode) + ONNX Runtime 1.30 (CPU) + the YuNet (MIT) face and YOLOX-s (Apache-2.0) object models and the MobileNetV3-Large (Apache-2.0) ImageNet classifier | on main; built locally, multi-arch publish pending Actions (`task docker:base:full`; detection loads its ONNX Runtime and models at startup, `XI_DETECT_*`) |
| `gpl` | full + FFmpeg rebuilt under the GPL with x264 and x265: adds H.264 video encoding (MP4 that plays in every browser and phone) and HEVC video encoding; and Ghostscript (`gs`, AGPL) for EPS/PostScript input, used only with `XI_FORMATS_EPS_ENABLED=true` (off by default, [ADR 0011](docs/adr/0011-ghostscript-subprocess.md)); and LibRaw with the Adobe DNG SDK (its own licence, with an indemnity clause) and the GoPro GPR SDK, which decode JPEG XL DNG and GoPro GPR (slim and full serve their embedded preview or answer 422). **Contains GPL and AGPL software; see below** | on main; built locally, multi-arch publish pending Actions (`task docker:base:gpl`); opt-in |

`slim` and `full` contain no GPL code. `gpl` does: its FFmpeg is GPL-2.0-or-later, H.264 and HEVC are covered by patents, and its Ghostscript is AGPL-3.0-or-later (a separate program, run only when you enable EPS input; enabling it also runs a PostScript interpreter on untrusted input, read the security note in [CONFIG.md](docs/CONFIG.md) first). The project does not sell anything and grants no patent licence, so if you run or redistribute the `gpl` image, checking that the GPL and those patents are acceptable for your use is your job; if you are unsure, use `full` (WebM/VP9 and AV1-in-MP4 video). The xtandard-image code itself stays MIT. Every image carries its licences in `/opt/xtandard/share/licenses/`, and the gpl image adds `LICENSE-GPL.md` there ([ADR 0006](docs/adr/0006-license-policy.md)).

### 2. Try it in dev mode

`XI_DEV_MODE=true` accepts any public http(s) source. Unsigned URLs are allowed, but only with the widths, heights and qualities of the `restricted` policy (Next.js sizes: 640, 750, 828, 1080, 1200, …; the server logs the list at startup):

```sh quickstart
docker run -d --name xtandard-image -p 8080:8080 -e XI_DEV_MODE=true xtandard-image
until curl -fsS http://localhost:8080/healthz >/dev/null; do sleep 1; done

# 640 px wide WebP (imgproxy URL: /<signature>/<options>/plain/<source>@<format>)
curl -fsS -o thumb.webp \
  'http://localhost:8080/insecure/rs:fit:640:0/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb@webp'

# Smart crop to 640x384; no extension, so the format follows Accept (AVIF here)
curl -fsS -o crop.avif -H 'Accept: image/avif,image/webp,*/*' \
  'http://localhost:8080/insecure/rs:fill:640:384/g:sm/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb'

docker rm -f xtandard-image
```

### 3. Allowlist a source and sign URLs

The production shape: one http source that only allows `images.unsplash.com`, and imgproxy-compatible signing keys. `scripts/sign-url.sh` signs a path with the same key and salt (it works for imgproxy too):

```sh quickstart
export XI_SIGNING_KEYS="$(openssl rand -hex 32)" XI_SIGNING_SALTS="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web \
  -e XI_SOURCES_WEB_TYPE=http \
  -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS -e XI_SIGNING_SALTS \
  xtandard-image
until curl -fsS http://localhost:8080/healthz >/dev/null; do sleep 1; done

# Signed 1200x630 social card, smart crop
path="$(scripts/sign-url.sh '/rs:fill:1200:630/g:sm/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb@jpg')"
echo "$path"
curl -fsS -o og.jpg "http://localhost:8080$path"

# /info: size, format, dimensions and metadata of the source (imgproxy JSON names)
curl -fsS -o info.json "http://localhost:8080/info$(scripts/sign-url.sh '/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb')"
cat info.json; echo

# Unsigned URLs are now refused, and so is any host outside the allowlist: both print 403
curl -s -o /dev/null -w '%{http_code}\n' \
  'http://localhost:8080/insecure/rs:fit:640:0/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb'
curl -s -o /dev/null -w '%{http_code}\n' \
  "http://localhost:8080$(scripts/sign-url.sh '/rs:fit:640:0/plain/https://example.com/a.jpg')"

docker rm -f xtandard-image
```

`info.json` from that run (this source carries no EXIF, IPTC or XMP, hence the empty sections; options such as `cs:1/pn:1/a:1` add colorspace, page count and alpha, [URL-DIALECTS §3.3](docs/URL-DIALECTS.md)):

```json
{"size":4696118,"format":"jpeg","mime_type":"image/jpeg","width":7360,"height":4912,"orientation":1,"exif":{},"iptc":{},"xmp":{}}
```

### 4. Deploy with Dokploy or Coolify

Both accept a compose file. Create a **Docker Compose** service, paste this, set `XI_SIGNING_KEYS` / `XI_SIGNING_SALTS` (`openssl rand -hex 32` each) and your allowlist in the environment tab, and route a domain to port 8080 (Dokploy: *Domains*; Coolify: the service's domain field):

```yaml quickstart-compose
services:
  xtandard-image:
    image: ${XI_IMAGE:-ghcr.io/xantiagoma/xtandard-image:slim}
    restart: unless-stopped
    environment:
      XI_SOURCES: web
      XI_SOURCES_WEB_TYPE: http
      XI_SOURCES_WEB_ALLOW: ${XI_SOURCES_WEB_ALLOW:-images.unsplash.com}
      XI_SIGNING_KEYS: ${XI_SIGNING_KEYS:?set XI_SIGNING_KEYS (openssl rand -hex 32)}
      XI_SIGNING_SALTS: ${XI_SIGNING_SALTS:?set XI_SIGNING_SALTS (openssl rand -hex 32)}
    expose:
      - "8080"
    read_only: true
    tmpfs:
      - /tmp:rw,noexec,nosuid,size=64m
```

The image declares a Docker `HEALTHCHECK` (`/healthz`); Prometheus metrics are on the admin listener (`XI_ADMIN_ADDR`, default `127.0.0.1:9090`, set `:9090` to scrape it from another container). Put a CDN in front for caching. Every variable is documented in [docs/CONFIG.md](docs/CONFIG.md), which also has Kubernetes and larger Dokploy/Coolify examples; `XI_COMPAT_IMGPROXY_ENV=true` accepts your existing `IMGPROXY_*` variables.

## Sources

Sources are the allowlist of origins, and you can configure as many as you need, several of the same type included: AWS S3, Cloudflare R2, Backblaze B2 and a self-hosted RustFS or MinIO side by side, plus GCS, Azure Blob, local directories, FTP/FTPS/SFTP servers and http origins with their own allowlists and headers. For example, in the environment:

```dotenv
XI_SOURCES=aws,r2,rustfs,web
XI_SOURCES_AWS_TYPE=s3
XI_SOURCES_AWS_REGION=eu-west-1
XI_SOURCES_AWS_BUCKETS=acme-media
XI_SOURCES_R2_TYPE=s3
XI_SOURCES_R2_ENDPOINT=https://ACCOUNT_ID.r2.cloudflarestorage.com
XI_SOURCES_R2_REGION=auto
XI_SOURCES_R2_BUCKET=assets
XI_SOURCES_R2_ACCESS_KEY_ID=CHANGE_ME
XI_SOURCES_R2_SECRET_ACCESS_KEY_FILE=/run/secrets/r2_secret_access_key
XI_SOURCES_RUSTFS_TYPE=s3
XI_SOURCES_RUSTFS_ENDPOINT=http://rustfs:9000
XI_SOURCES_RUSTFS_FORCE_PATH_STYLE=true
XI_SOURCES_RUSTFS_BUCKET=assets
XI_SOURCES_RUSTFS_ACCESS_KEY_ID=xi-reader
XI_SOURCES_RUSTFS_SECRET_ACCESS_KEY_FILE=/run/secrets/rustfs_secret_key
XI_SOURCES_WEB_TYPE=http
XI_SOURCES_WEB_ALLOW=https://images.unsplash.com
```

A URL picks its source by name or by scheme: `plain/xi://rustfs/cat.jpg` reads RustFS, `plain/s3://acme-media/cat.jpg` goes to the s3 source that allows that bucket (`aws`), and `plain/https://images.unsplash.com/…` to `web`. When two sources allow the same bucket name (`assets` here), a scheme reference goes to the first one in alphabetical order (the mount's own source first), so address the others with `xi://<name>/…`. Anything no source allows answers 403 with the reason. Credentials come from the environment, `_FILE` variables (Docker and Kubernetes secrets), `{file: …}` references in a config file, or the provider's own identity (IAM roles, Workload Identity, managed identity).

[docs/SOURCES.md](docs/SOURCES.md) has the resolution rules with a worked table, recipes for every provider, and complete YAML, TOML, Docker Compose, Dokploy/Coolify and Kubernetes examples ([docs/examples/](docs/examples/), all loaded by the tests).

## API reference

The HTTP API is described by an OpenAPI 3.1 document (on `main`). A running instance serves it on the admin listener only (`XI_ADMIN_ADDR`, default `127.0.0.1:9090`; set `:9090` in a container and publish it on loopback): `GET /openapi.json`, a browsable Scalar reference at `/docs` with `XI_ADMIN_DOCS=true`, and the config schema at `/config.schema.json`. Without a server, `xtandard-image openapi` and `xtandard-image config schema` print the same files. The [published copy](https://xantiagoma.github.io/xtandard-image/schema/v1/openapi.json) and its [browsable reference](https://xantiagoma.github.io/xtandard-image/api/) go live with the GitHub Pages site, which is built but not enabled yet. See [docs/OPENAPI.md](docs/OPENAPI.md#how-to-view-it).

## Editor support

The config file (YAML, JSON or TOML) has a JSON Schema (on `main`), so editors validate it, complete keys and show each key's meaning and its `XI_*` variable on hover. Point the file at the schema URL below. It is the schema's `$id`; the GitHub Pages site that serves it is built (`task pages:build`) and goes live once the owner enables Pages, so until then use a running instance's admin URL or a local copy from `xtandard-image config schema`:

```yaml
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
server:
  addr: ":8080"
```

JSON files use a top-level `"$schema"` key with the same URL, and TOML files a first line `#:schema <url>` (Taplo, Tombi). A running instance also serves the schema for its exact version on the admin listener (`GET /config.schema.json`), `xtandard-image config schema` prints it, and `xtandard-image config validate --config <file>` checks a file with the real loader. See [docs/CONFIG.md](docs/CONFIG.md#config-file-formats-and-schema-adr-0007).

## TypeScript client

On `main`, not on npm yet. [`@xtandard/image`](clients/typescript) builds typed imgproxy-dialect URLs, signs them, encrypts sources and calls `/info` and the health endpoints. It runs on WebCrypto and has no runtime dependencies. Its option types are generated from the server's OpenAPI document, so an unknown option or a wrong argument does not compile. Package versions will follow the server's release tags; nothing is published until the first release ([RELEASE](docs/RELEASE.md#npm-package)).

```ts
import { imgproxy } from "@xtandard/image";

const img = imgproxy({
  baseUrl: "https://img.example.com",
  signing: { key: process.env.XI_SIGNING_KEY!, salt: process.env.XI_SIGNING_SALT! },
});
await img.from("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
  .resize("fill", 1200, 630).gravity("sm").extension("jpg").build();
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

## Other languages

Official clients for Python, Go, PHP, Ruby, Rust, Java, .NET and Elixir share one spec generated from the server's catalogues, one conformance fixture and one code generator ([`clients/`](clients/README.md), [ADR 0013](docs/adr/0013-official-clients.md)). The first is **Python**, [`clients/python`](clients/python/README.md) (`xtandard-image` on PyPI after the first release). It has typed builders for imgproxy, `/info`, `/video`, HLS/DASH and the query dialect, signing, encryption, and sync and async clients on httpx:

```python
from xtandard_image import SigningKey, imgproxy

img = imgproxy(base_url="https://img.example.com", signing=SigningKey(key=KEY, salt=SALT))
img.url("s3://photos/cat.jpg", {"rs": ("fill", 1200, 630), "g": "sm"}, extension="jpg")
```

**PHP** is [`clients/php`](clients/php/README.md) (`xtandard/image` on Packagist after the first release): the same builders, signing and encryption, PHPStan-typed option shapes and fluent methods, and a client on any PSR-18 HTTP client.

**Go**: [`clients/go`](clients/go/README.md) (module `github.com/xantiagoma/xtandard-image/clients/go`, standard library only) has the same builders as params structs and fluent chains, typed option structs per catalogue, and a `net/http` client with `context.Context`, Range and HEAD.

**Elixir**: [`clients/elixir`](clients/elixir/README.md) (`xtandard_image` on Hex after the first release), with keyword-list and pipe-friendly builders, signing and encryption on `:crypto`, and an HTTP client on Req.

**.NET** is [`clients/dotnet`](clients/dotnet/README.md) (`Xtandard.Image` on NuGet after the first release, .NET 8+, no package dependencies, AOT-compatible): typed option records and fluent builders, signing, encryption, an async `HttpClient` client, and `AddXtandardImage()` for ASP.NET Core in `Xtandard.Image.Extensions`.

**Rust**: [`clients/rust`](clients/rust/README.md) (crate `xtandard-image`): typed builders with one fluent method per option, signing, encryption, async and blocking clients on reqwest (rustls).

**Java** 17+, [`clients/java`](clients/java/README.md) (`io.github.xantiagoma:xtandard-image` on Maven Central after the first release), has the same surface with no runtime dependencies: typed option methods generated from the catalogues, fluent chains, and a `java.net.http` client with `CompletableFuture` async.

**Ruby**: [`clients/ruby`](clients/ruby/README.md) (gem `xtandard-image`), with no runtime dependencies, RBS signatures for every option, a Net::HTTP client and a Rails helper.

**Dart** 3.5+, [`clients/dart`](clients/dart/README.md) (`xtandard_image` on pub.dev after the first release), is pure Dart for Flutter apps (iOS, Android, web, desktop) and Dart servers: typed option methods and sealed option values generated from the catalogues, signing, encryption, and a client on any `package:http` client.

**Swift**: [`clients/swift`](clients/swift/README.md) (SwiftPM product `XtandardImage`, straight from this repository's root `Package.swift`) for iOS, macOS, tvOS, watchOS, visionOS and Linux: typed builders and fluent methods, signing and encryption (CryptoKit/CommonCrypto, swift-crypto on Linux), and an async `URLSession` client.

The other languages are planned (M6 in the [roadmap](docs/ROADMAP.md)).

## Playground

A small web UI ships inside the binary, **off by default**. Turn it on and open it on the admin listener (with an image built from `main`):

```sh
docker run --rm -p 127.0.0.1:8080:8080 -p 127.0.0.1:9090:9090 \
  -e XI_DEV_MODE=true -e XI_ADMIN_ADDR=:9090 -e XI_PLAYGROUND_ENABLED=true \
  xtandard-image
# then open http://127.0.0.1:9090/_playground/
```

What it does (v2, WP-94):

- **Sources:** a sample gallery (built-in fixtures that work offline, plus about 300 curated public files covering every input format and its variants: colour types, bit depths, animation, HDR and gain maps, camera RAW per brand, and the formats not decoded yet; each with its licence, docs/research/format-gap.md), drag-and-drop uploads kept in memory for 10 minutes (`XI_PLAYGROUND_UPLOADS`), a browser for `fs` sources, or any URL / `xi://` reference.
- **Option builder for every dialect:** imgproxy, the query form, imgix, Cloudinary, ImageKit, Cloudflare, wsrv, thumbor, Bunny and AWS SIH, from each dialect's catalogue, grouped by category (resize, gravity incl. face and object classes, format and quality, colour, effects, watermarks, detection, pages/frames/video, metadata, delivery, presets). Typed inputs (bounded numbers with sliders, enums, colour pickers, booleans, tuples and repeatable groups), inline docs, aliases, a search box (`/`) and chained pipelines (imgproxy `/-/`, ImageKit steps, Cloudinary components, so overlays such as `l_…` + `fl_layer_apply` and `l-image`/`l-text` can be built). "Write the path and query by hand" covers what the builder cannot express (imaginary endpoints such as `/watermark`).
- **URL panel:** the live, colour-coded URL (signed on the server), copy as URL, as `curl` and as an `@xtandard/image` TypeScript call, and **the same transform in every other dialect** (`POST api/translate`): each URL is parsed back by its dialect and what it cannot express is listed; a dialect that cannot address the source at all says why (ImageKit, Bunny and AWS SIH take keys on their mount's source: an http(s) URL under that source's `base_url` is written as a key). A mount with several prefixes (a simple provider's native prefixes and `/simple/<name>/`) is listed once, with a prefix picker.
- **Preview (the result):** before/after slider or side by side in a box with the result's aspect ratio (portrait, panoramas and icons are never cropped; a source of another shape is letterboxed), zoom 1×/2×/fit with pan (drag or arrow keys), a checkerboard for alpha, inline animations and video results, a loading placeholder that never shows a stale result (only the latest request paints), a **Source vs result** table (format and MIME type, dimensions, megapixels, bytes, colour space, alpha, pages, orientation; the source's values from its own `/info`, the change in a third column), the **result response headers** named in words (`X-Xi-*` debug headers too, raw name in the tooltip) and the server's per-stage `Server-Timing` as a bar chart; errors show the server's error kind and message. Multi-page sources get a page picker with thumbnails and "all pages stacked", animations a still-frame scrubber, videos a time scrubber, keyframes, best frame, sprite and animation buttons, all written as the current dialect's own options (disabled with a tooltip where the dialect cannot express them).
- **Info (the source file):** the source's `/info` as a collapsible tree, detections drawn over the preview (full image), video metadata.
- **Global tools** (header navigation, `#tools/<name>` routes that keep the session as it is): **Gallery** (every sample as a grid), **History** (the sessions rendered in this browser, with search, thumbnails, open and copy link), **Format matrix** (every sample probed with `/info` and a thumbnail, to check a file of every format works; filters, run all, sortable by format, sample, variant, time and status) and **Examples**.
- **Examples** (WP-116): ready-made sessions (`internal/playground/recipes.json`) as cards with a live thumbnail of each result, filter chips and search, plus a quick "Examples" menu in the Options panel. One click loads the source, dialect and options into the builder, renders them and leaves everything editable (it is an ordinary session: share link, history, Back). Watermarks in every form the server supports, each named in the URL (WP-117): the repository logo as a per-request image (`wmu:` base64 of `xi://local/watermark/logo.png`) in each gravity with offsets, opacity and scale, every corner at once through chained pipelines, a size box (`wms`), rotation (`wmr`), a drop shadow (`wmsh`), tiles (`re`, rotated tiles) and a chessboard (`ch`), other per-request images (`wmu`, a badge fixture or a public SVG), Pango text (`wmt`: a semi-transparent “© 2026 Studio” footer, two lines in two fonts, a tiled “DRAFT” stamp), an animated GIF, and resize → watermark → WebP as chained pipelines; the same ideas in imgix (`txt`, `mark`, `mark-tile`), Cloudinary (`l_text:`, `l_fetch:` with `fl_layer_apply`, `fl_relative`, `fl_tiled`), ImageKit (`l-image`, `l-text`) and imaginary (`/watermark`, `/watermarkimage`). Beyond watermarks: smart and face crops, blurred faces, BlurHash/ThumbHash in the Info tab, GIF → MP4, a video sprite sheet, a PDF page, a camera RAW preview and an UltraHDR gain map as HDR AVIF. Only one recipe, "Default watermark (imgproxy compatibility)", uses the server's default image (`XI_PROCESSING_WATERMARK_SOURCE`, empty by default, reported as `watermark` in `api/meta` without the source): a watermark that appears from nowhere in the URL is confusing, so the demo (`compose.playground.yml`) leaves it unset and that recipe is disabled with the reason, like the recipes that need the detection models or FFmpeg on a server without them. In the builder, `wm` without `wmu`/`wmt` shows a notice (default watermark configured on this server or not) with a one-click "Use an explicit image (wmu)"; `wmu` and imgix `mark` show the image URL in plain text with a thumbnail, and the preview and the Info tab say where the source and each watermark image come from, with a link to the original. A Go test builds every recipe as the page does and parses it with the dialect's strict parser.
- **Video and audio** (WP-127, with `XI_VIDEO_TRANSFORM_ENABLED` on the `full` or `gpl` image): a video or audio source (the gallery has a clip and an MP3; uploads take audio too) gets a **Video and audio** panel that switches the session between an image of the source (thumbnails, frames, sprites) and a **video transform** (`/video` on imgproxy mounts, `/q/video` on query mounts). The panel has a trim timeline with start and end handles over the source's duration, the containers and codecs this server's build writes (MP4 is H.264 on `gpl`, AV1 on `full`; options the image lacks are disabled and the panel says which ones this browser plays), size and frame rate, audio (mute, volume in dB, EBU R128 loudness, fades, sample rate, channels), a one-click logo overlay with a window and fades, a running timecode and waveform pictures; the builder below switches to the `/video` option catalogue for everything else (crop, gravity, rotation, CRF, bit-rate cap, keyframe interval, text overlays). An audio file opens in video mode with its waveform as the source side. The preview plays the result in `<video>` or `<audio>`, counts the seconds of a transcode with a **Cancel** button that aborts the request, and the Source vs result table compares container, codecs, duration, size, frame rate, bit rate, sample rate and channels (`api/probe` and `api/probe/result` read them with FFmpeg, audio files included, which `/info` does not describe). Copy as URL, `curl` or TypeScript (`img.video(…)`), and "Every dialect" writes the transform for Cloudinary `/video/upload/`, ImageKit video files and Cloudflare `/cdn-cgi/media/`, each parsed back to list what it cannot express. Video recipes in the Examples: trim and resize to WebM, MP3 with loudness normalisation, a logo faded in and out on a clip, a timecode, a waveform PNG, `/q/video`, Cloudinary `/video/upload` trim, Cloudflare `mode=audio` and `mode=spritesheet`, an ImageKit video resize. WP-129 adds 55 media examples (filter chips **Video and audio** and **Audio**) on public, permissively licensed sources listed in the gallery with their attribution (the Sintel and Big Buck Bunny trailers and 10 s Big Buck Bunny clips in 1080p60 H.264 and HEVC, CC-BY 3.0 Blender Foundation; NASA vertical, talking-head and timelapse clips, public domain; a LibriVox reading, public domain; a Musopen Chopin waltz, CC0) plus a generated stereo pan sweep: head, middle and tail trims; 9:16 (padded and cropped with gravity), 1:1 and 4:5 social formats; rotating, mirroring and pillarboxing a vertical clip; 60 → 30 and 24 fps; a CRF 20/30/40 ladder; H.264 vs AV1 vs VP9 at the same quality setting; HEVC to WebM; muting a silent track; M4A, MP3, Opus and FLAC extraction; loudness normalisation up (quiet speech to -16 LUFS) and down (a loud trailer to -23 LUFS); volume, fades, mono downmix, 48 → 22.05 kHz resampling; three waveform styles; a corner logo, a lower third in a time window, a seconds counter and a chessboard watermark; a 3 s silent WebM loop; HLS and DASH ladders; `raw:1` sources; `/q/video`; and the same ideas in Cloudinary (`/video/fetch/` with `f_auto`, `ac_none`, `e_volume:mute`, an `l_text` layer with a time window and fade, `sp_sd`), ImageKit (`ik-master.m3u8` with `sr-`, an `l-text` window) and Cloudflare (`mode=video`, `mode=audio` from 5.1, `mode=frame`, `mode=spritesheet`). Each description says what to look or listen for in the comparison. Recipes that need the gpl image's H.264 or HEVC encoder (`needs: h264`/`hevc`) are disabled with the reason on the full image and hidden on images without FFmpeg. The demo raises `XI_FETCH_TIMEOUT` to 30 s for these downloads (up to 25 MB). **Before/after for media** (WP-128): the Source side plays the original file too, not a still: `<video>` or `<audio>` streamed same-origin from `/video/<sig>/raw:1/plain/<source>` (signed by the playground like any video URL, so the server's signing, unsigned policy, allowlists, SSRF rules and input-size limit apply) through `api/render` with byte ranges, with the first frame (or the waveform) as its poster and a loader of its own, separate from the transcode's. One control bar under the stage drives both players: play/pause (`k`), seek, speed, sound and an **A/B** switch (`x`) that moves what you hear between the source and the result at the same position, for loudness, volume and fade comparisons. The result's time t plays against the source's trim start + t (scaled when the durations differ, as after a speed change); the trim window is highlighted on a source timeline in every view. Videos keep the image views: **slider** (a wipe over both videos in one letterboxed box), **side by side** and result only; audio stacks the two players with their waveforms (the source's with the trim window), each with a playhead, click to seek. While both play, the source follows the result: a drift over one frame (100 ms for audio) nudges its playback rate by up to 10 %, over max(0.25 s, four frames) seeks it, and repeated hard re-syncs (a source that seeks slowly here) unlink them with a note; "linked playback" off gives each player its own controls. A source the browser cannot play falls back to its still and the result plays alone. The Source vs result table gives duration, size and bit-rate changes as a percentage and a factor (×). An HLS or DASH result (WP-126; the Examples have an HLS ladder) plays natively where the browser has HLS (Safari) and otherwise lists and links its playlists; the playground serves a stream's parts through its own `api/stream` (no player library is loaded). The demo (`compose.playground.yml`) turns video transformations on with `XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=allow`, for the demo only: keep the default (`deny`, signed only) anywhere public, and run heavy video work on its own instances with `XI_ROLE=video` (the image fleet with `XI_ROLE=image`), routing `/video/*`, `/q/video`, `/_stream/*` (the parts of HLS and DASH streams) and the dialects' video paths to them.
- The whole session lives in the URL hash (share link), keyboard shortcuts, light and dark themes.

<!-- Screenshot placeholder: docs/img/playground.png is not committed yet. -->

URLs are signed on the server (`POST /_playground/api/sign`), so keys never reach the browser, but anyone who can open the playground can sign URLs: keep it on the private admin listener. `XI_PLAYGROUND_PUBLIC=true` also serves it on the public listener, for demos and trusted networks only. It is plain HTML, CSS and JavaScript with no build step, loads nothing from other origins (CSP `default-src 'self'`), and works air-gapped. See [docs/CONFIG.md](docs/CONFIG.md#playground).

## Performance

[bench/RESULTS.md](bench/RESULTS.md) compares xtandard-image with imgproxy OSS on the same machine with matched settings (thumbnails to JPEG/WebP/AVIF, PNG with alpha, animated GIF, 1200 px fill, `/info`); the methodology is in [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Documentation

- [Architecture](docs/ARCHITECTURE.md): request lifecycle, packages, the canonical transform model, concurrency and memory, failure modes, and the Docker build.
- [URL dialects](docs/URL-DIALECTS.md): grammar, option mapping and signing for every supported dialect.
- [Formats](docs/FORMATS.md): decoders, encoders, and the best-format decision matrix.
- [Configuration](docs/CONFIG.md): environment variables, the YAML/JSON/TOML config file and its JSON Schema, and Dokploy, Coolify and Kubernetes examples.
- [Sources](docs/SOURCES.md): several S3-compatible stores, GCS, Azure, directories, FTP/SFTP and http origins at once; how a URL picks one; credentials; provider recipes and deployment examples.
- [OpenAPI](docs/OPENAPI.md): the OpenAPI 3.1 document of the HTTP API, where it is published, and the [TypeScript package](clients/typescript/README.md) generated from it.
- [Release](docs/RELEASE.md): how the image and the npm package are built, published and verified.
- [Benchmarks](docs/BENCHMARKS.md): methodology and how to reproduce [bench/RESULTS.md](bench/RESULTS.md).
- [Roadmap](docs/ROADMAP.md): the M1, M2 and M3 work packages.
- [ADRs](docs/adr/): language and engine, URL dialects, security model, caching, Docker images, license policy, config schema and file formats, OpenAPI, the TypeScript workspace, ONNX Runtime via purego, video, and the official clients.
- [Clients](clients/README.md): the official clients, their shared spec and conformance fixture, and how a language plugs in.
- [Go standards](docs/go-standards.md) and [stack research](docs/research/stack.md).

## Contributing

The project is not accepting outside contributions yet. Work is tracked as the work packages in the roadmap. Read `CLAUDE.md` and `docs/go-standards.md` before writing code.

## License

MIT. See [LICENSE](LICENSE). Code derived from imgproxy (Apache-2.0), such as the BMP/ICO codecs, keeps its upstream headers and is credited in `NOTICE`. The Docker images bundle third-party libraries under their own licenses, listed in `/opt/xtandard/share/licenses`.
