# xtandard-image

> **Status: alpha.** Milestones **M1 and M2 are complete on `main`**: every URL dialect below except imgix is served end to end from every source type, with the imgproxy processing set apart from detection, video and the advanced watermarks (chained pipelines, colour adjustments, geometry and filter extras, best format and auto-quality), camera RAW, PDF, PSD and HDR inputs, a memory/disk/S3/Redis result cache, `/info` with the Pro fields, client hints, signing, encryption, a JSON Schema for the config file and an OpenAPI document. **M3 is in progress.** In review in the open `m3` stack (#80–#88), not on `main` yet: the `full` image with FFmpeg and ONNX Runtime, the face/object detection runtime, the imgix mount, advanced and text watermarks, the playground, the TypeScript package, Pages/SchemaStore publishing and a stabilisation pass. Being built: detection-based crops and filters, video thumbnails and the `gpl` image flavour. Planned: video tiles/animation and GIF → video. No release has been tagged yet, so you build the image yourself (below), and configuration and URL options may still change before 1.0.

A self-hosted image processing proxy in Go and libvips. The goal is to offer every imgproxy Pro processing feature for free under the MIT license, support more input formats than anything else in the category, and accept many URL dialects: imgproxy, imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Thumbor, wsrv/weserv, AWS Serverless Image Handler and the rest of the [unpic](https://unpic.pics/providers/) provider list. It ships as a ready-to-run Docker image for Dokploy, Coolify and Kubernetes, with no build step.

## Why

- **imgproxy URLs work unchanged.** Signing, `enc/` sources, presets, chained pipelines, `/info` and the processing options, Pro ones included, work on `main`. Options that need detection or video (`g:obj`, `crop_objects`, `vts`, …) are parsed and reported in `X-Xi-Ignored` until M3 lands them; the advanced watermark options (`wmu`, `wmt`, `wms`, `wmr`, `wmsh`, chessboard) are in review (#85).
- **Point your front end at it and keep your code.** An unpic `<Image>` configured for Cloudinary, ImageKit, Cloudflare, Bunny, wsrv, Netlify, Next.js or any of the other unpic providers can target xtandard-image as if it were that provider (on `main`); imgix URLs are parsed on `main` and served once the imgix mount (#87) is merged. Thumbor and AWS Serverless Image Handler URLs work too.
- **Wide format support.** Inputs: JPEG, PNG, GIF (animated), WebP (animated), AVIF, HEIC, JPEG XL, TIFF, BMP, ICO, SVG, JPEG 2000, UltraHDR, camera RAW (CR3, NEF, ARW, DNG and more, embedded preview first), PDF, PSD/PSB, OpenEXR, Radiance HDR and PPM/PFM. Outputs: JPEG, PNG, GIF, WebP, AVIF, JPEG XL, TIFF, BMP, ICO (HEIC opt-in). Video input and GIF → video output are M3 work for the `full` image.
- **Secure by default.** It refuses to start without a source allowlist. It also protects against SSRF (including DNS rebinding), enforces decompression-bomb and pixel limits, and sanitises SVG.
- **CDN-friendly and stateless.** Responses carry correct `Cache-Control`, `ETag`, `Vary`, `Content-DPR` and `Content-Disposition` headers, with 304 before any processing. Requests for the same original are coalesced. An optional result cache (memory, disk, S3, Redis tiers) is built in.
- **No GPL in the default images.** `slim` and `full` use only LGPL, BSD, MIT and Apache dependencies (see [ADR 0006](docs/adr/0006-license-policy.md)). An opt-in `gpl` flavour with H.264/HEVC encoders is being built as a separate image.

## Status and comparison

The xtandard-image column is the state as of 2026-09-27: **done (M1)** / **done (M2)** means merged on `main` and working in the image you build today; **in review (#N)** means implemented in open pull request N of the `m3` stack, not on `main` yet; **in progress** and **planned** follow the [roadmap](docs/ROADMAP.md), which tracks every work package with its status. Competitor legend: **yes**, **paid** (paid tier only), **partial**, **no**, **—** (not compared yet). Competitor data comes from their public docs and source as of September 2026.

| Feature | xtandard-image | imgproxy OSS | imgproxy Pro | thumbor | imaginary | weserv |
|---|---|---|---|---|---|---|
| License | MIT | Apache-2.0 | commercial | MIT | MIT | BSD-3 |
| **URLs and security** | | | | | | |
| imgproxy URL dialect (every option parsed; detection and video options listed in `X-Xi-Ignored` until M3) | done (M1, M2) | yes | yes | no | no | no |
| Query-string dialect (`?url=…&w=…`, imaginary endpoints) | done (M1) | no | no | no | yes | partial (own parameters) |
| Presets, only-presets mode | done (M1) | yes | yes | — | — | — |
| Signed URLs (imgproxy HMAC, `xi_sig`, imaginary `sign`) | done (M1) | yes | yes | yes | yes | no |
| Encrypted source URLs (`enc/`) | done (M1) | no | paid | no | no | no |
| Mandatory source allowlist, SSRF protection incl. DNS rebinding | done (M1) | partial (optional allowlist) | partial (optional allowlist) | — | — | — |
| imgix URLs (host or prefix mount, `s`/`expires` signing, `64` parameters, watermarks) | parser done (M2); mount in review (#87) | no | no | no | no | no |
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
| Video thumbnails (frame at a time, keyframe, best frame; `/info` video metadata) | in review (#90, `full` image) | no | paid | partial (ffmpeg engine) | no | no |
| GIF → MP4/WebM, video tiles and animation | in progress (WP-75/76, `full`/`gpl` images) | no | paid | partial (ffmpeg engine) | no | no |
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
| Face and object detection (face/object gravity, `objects_position`, `crop_objects`, blur/pixelate/draw detections, `/info` objects; every dialect's face options) | in review (#88 runtime, WP-72/73 PR; `full` image, slim answers 422) | no | paid | partial (OpenCV faces) | no | no |
| Watermarks (text, per-request URL, size, shadow, rotation, chessboard) | in review (#85) | no | paid | partial | partial (text) | no |
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
| Playground web UI | in review (#84) | — | — | — | — | — |
| TypeScript URL builder and client (`@xtandard/image-url`) | in review (#83) | — | — | — | — | — |

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
| `slim` | every still format: JPEG, PNG, GIF, WebP, AVIF, HEIC (decode), JPEG XL, TIFF, JPEG 2000, UltraHDR, SVG, PDF (PDFium), RAW (LibRaw), OpenEXR, PSD | default |
| `full` | slim + FFmpeg 8.1 (LGPL only: video decode, VP8/VP9 and AV1 encode, no H.264/HEVC encode) + ONNX Runtime 1.30 (CPU) + the YuNet (MIT) face and YOLOX-s (Apache-2.0) object models | base image ready (`task docker:base:full`); the detection runtime loads its ONNX Runtime and models at startup (WP-71, `XI_DETECT_*`); the detection options and video land in M3 |
| `gpl` | full + FFmpeg rebuilt under the GPL with x264 and x265: adds H.264 video encoding (MP4 that plays in every browser and phone) and HEVC video encoding. **Contains GPL software; see below** | base image ready (`task docker:base:gpl`); opt-in |

`slim` and `full` contain no GPL code. `gpl` does: its FFmpeg is GPL-2.0-or-later, and H.264 and HEVC are covered by patents. The project does not sell anything and grants no patent licence, so if you run or redistribute the `gpl` image, checking that the GPL and those patents are acceptable for your use is your job; if you are unsure, use `full` (WebM/VP9 and AV1-in-MP4 video). The xtandard-image code itself stays MIT. Every image carries its licences in `/opt/xtandard/share/licenses/`, and the gpl image adds `LICENSE-GPL.md` there ([ADR 0006](docs/adr/0006-license-policy.md)).

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

## Editor support

The config file (YAML, JSON or TOML) has a JSON Schema (on `main`), so editors validate it, complete keys and show each key's meaning and its `XI_*` variable on hover. Point the file at the schema URL below. It is the schema's `$id`; the GitHub Pages site that serves it is in review (#80) and goes live once Pages is enabled, so until then use a running instance's admin URL or a local copy from `xtandard-image config schema`:

```yaml
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
server:
  addr: ":8080"
```

JSON files use a top-level `"$schema"` key with the same URL, and TOML files a first line `#:schema <url>` (Taplo, Tombi). A running instance also serves the schema for its exact version on the admin listener (`GET /config.schema.json`), `xtandard-image config schema` prints it, and `xtandard-image config validate --config <file>` checks a file with the real loader. The HTTP API is described by an OpenAPI 3.1 document (on `main`): admin `GET /openapi.json` and, with `XI_ADMIN_DOCS=true`, a browsable reference at admin `/docs`. The `xtandard-image openapi` subcommand and the [published copy](https://xantiagoma.github.io/xtandard-image/schema/v1/openapi.json) with its [browsable reference](https://xantiagoma.github.io/xtandard-image/api/) come with the Pages work in review (#80). See [docs/CONFIG.md](docs/CONFIG.md#config-file-formats-and-schema-adr-0007) and [docs/OPENAPI.md](docs/OPENAPI.md).

## TypeScript client

In review (#83), not on `main` or npm yet. [`@xtandard/image-url`](packages/image-url) builds typed imgproxy-dialect URLs, signs them, encrypts sources and calls `/info` and the health endpoints. It runs on WebCrypto and has no runtime dependencies. Its option types are generated from the server's OpenAPI document, so an unknown option or a wrong argument does not compile. Package versions will follow the server's release tags; nothing is published until the first release ([RELEASE](docs/RELEASE.md#npm-package)).

```ts
import { imgproxy } from "@xtandard/image-url";

const img = imgproxy({
  baseUrl: "https://img.example.com",
  signing: { key: process.env.XI_SIGNING_KEY!, salt: process.env.XI_SIGNING_SALT! },
});
await img.from("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
  .resize("fill", 1200, 630).gravity("sm").extension("jpg").build();
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

## Playground

In review (#84), not on `main` yet. A small web UI ships inside the binary, **off by default**. Turn it on and open it on the admin listener (with an image built from a branch that contains it):

```sh
docker run --rm -p 127.0.0.1:8080:8080 -p 127.0.0.1:9090:9090 \
  -e XI_DEV_MODE=true -e XI_ADMIN_ADDR=:9090 -e XI_PLAYGROUND_ENABLED=true \
  xtandard-image
# then open http://127.0.0.1:9090/_playground/
```

Pick a source (a URL, one of your named sources, or a sample image), a dialect mount, and add options from the option catalogue (typed inputs per argument, aliases shown). The page shows the generated URL live, with a copy button, and the source next to the result with its response headers (`Content-Type`, size, `X-Xi-*` debug headers, timings).

<!-- Screenshot placeholder: docs/img/playground.png is not committed yet; add one when the UI settles. -->

URLs are signed on the server (`POST /_playground/api/sign`), so keys never reach the browser, but anyone who can open the playground can sign URLs: keep it on the private admin listener. `XI_PLAYGROUND_PUBLIC=true` also serves it on the public listener, for demos and trusted networks only. It is plain HTML, CSS and JavaScript with no build step, loads nothing from other origins (CSP `default-src 'self'`), and works air-gapped. See [docs/CONFIG.md](docs/CONFIG.md#playground).

## Performance

[bench/RESULTS.md](bench/RESULTS.md) compares xtandard-image with imgproxy OSS on the same machine with matched settings (thumbnails to JPEG/WebP/AVIF, PNG with alpha, animated GIF, 1200 px fill, `/info`); the methodology is in [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Documentation

- [Architecture](docs/ARCHITECTURE.md): request lifecycle, packages, the canonical transform model, concurrency and memory, failure modes, and the Docker build.
- [URL dialects](docs/URL-DIALECTS.md): grammar, option mapping and signing for every supported dialect.
- [Formats](docs/FORMATS.md): decoders, encoders, and the best-format decision matrix.
- [Configuration](docs/CONFIG.md): environment variables, the YAML/JSON/TOML config file and its JSON Schema, and Dokploy, Coolify and Kubernetes examples.
- [OpenAPI](docs/OPENAPI.md): the OpenAPI 3.1 document of the HTTP API, where it is published, and the [TypeScript package](packages/image-url/README.md) generated from it.
- [Release](docs/RELEASE.md): how the image and the npm package are built, published and verified.
- [Benchmarks](docs/BENCHMARKS.md): methodology and how to reproduce [bench/RESULTS.md](bench/RESULTS.md).
- [Roadmap](docs/ROADMAP.md): the M1, M2 and M3 work packages.
- [ADRs](docs/adr/): language and engine, URL dialects, security model, caching, Docker images, license policy, config schema and file formats, OpenAPI, the TypeScript workspace, and ONNX Runtime via purego.
- [Go standards](docs/go-standards.md) and [stack research](docs/research/stack.md).

## Contributing

The project is not accepting outside contributions yet. Work is tracked as the work packages in the roadmap. Read `CLAUDE.md` and `docs/go-standards.md` before writing code.

## License

MIT. See [LICENSE](LICENSE). Code derived from imgproxy (Apache-2.0), such as the BMP/ICO codecs, keeps its upstream headers and is credited in `NOTICE`. The Docker images bundle third-party libraries under their own licenses, listed in `/opt/xtandard/share/licenses`.
