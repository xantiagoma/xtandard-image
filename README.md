# xtandard-image

> **Status: alpha.** Milestone M1 is complete on `main`: the imgproxy and query dialects work end to end from every source type, with the core processing set, every still format, signing, encryption, `/info`, metrics, the release pipeline and the M1 acceptance suite that gates releases. No release has been tagged yet, so you build the image yourself (below), and configuration and URL options may still change before 1.0.

A self-hosted image processing proxy in Go and libvips. The goal is to offer every imgproxy Pro processing feature for free under the MIT license, support more input formats than anything else in the category, and accept many URL dialects: imgproxy, imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Thumbor, wsrv/weserv and the rest of the [unpic](https://unpic.pics/providers/) provider list. It ships as a ready-to-run Docker image for Dokploy, Coolify and Kubernetes, with no build step.

## Why

- **imgproxy URLs work unchanged.** Signing, `enc/` sources, presets, `/info` and the OSS processing options work today; chained pipelines and the remaining Pro options arrive in M2 and M3.
- **Point your front end at it and keep your code** (M2, in progress). An unpic `<Image>` configured for imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Netlify, Next.js and others can target xtandard-image as if it were that provider.
- **Wide format support.** Inputs today: JPEG, PNG, GIF (animated), WebP (animated), AVIF, HEIC, JPEG XL, TIFF, BMP, ICO, SVG, JPEG 2000 and UltraHDR. Outputs: JPEG, PNG, GIF, WebP, AVIF, JPEG XL, TIFF, BMP, ICO (HEIC opt-in). Planned: camera RAW (CR3, NEF, ARW, DNG and more, embedded preview first), PDF and PSD in M2; video in the `full` image (M3).
- **Secure by default.** It refuses to start without a source allowlist. It also protects against SSRF (including DNS rebinding), enforces decompression-bomb and pixel limits, and sanitises SVG.
- **CDN-friendly and stateless.** Responses carry correct `Cache-Control`, `ETag`, `Vary: Accept`, `Content-DPR` and `Content-Disposition` headers, with 304 before any processing. Requests for the same original are coalesced. Result caching (memory, disk, S3, Redis) is optional (M2).
- **No GPL anywhere.** Only LGPL, BSD, MIT and Apache dependencies are used (see [ADR 0006](docs/adr/0006-license-policy.md)).

## Status and comparison

The xtandard-image column is the state of `main` as of 2026-09-26: **done (M1)** means merged and working in the image you build today; otherwise the planned milestone from the [roadmap](docs/ROADMAP.md), which tracks every work package with its status. Competitor legend: **yes**, **paid** (paid tier only), **partial**, **no**, **—** (not compared yet). Competitor data comes from their public docs and source as of September 2026.

| Feature | xtandard-image | imgproxy OSS | imgproxy Pro | thumbor | imaginary | weserv |
|---|---|---|---|---|---|---|
| License | MIT | Apache-2.0 | commercial | MIT | MIT | BSD-3 |
| **URLs and security** | | | | | | |
| imgproxy URL dialect (M1 option set; later options parsed and listed in `X-Xi-Ignored`) | done (M1) | yes | yes | no | no | no |
| Query-string dialect (`?url=…&w=…`, imaginary endpoints) | done (M1) | no | no | no | yes | partial (own parameters) |
| Presets, only-presets mode | done (M1) | yes | yes | — | — | — |
| Signed URLs (imgproxy HMAC, `xi_sig`, imaginary `sign`) | done (M1) | yes | yes | yes | yes | no |
| Encrypted source URLs (`enc/`) | done (M1) | no | paid | no | no | no |
| Mandatory source allowlist, SSRF protection incl. DNS rebinding | done (M1) | partial (optional allowlist) | partial (optional allowlist) | — | — | — |
| imgix / Cloudinary / ImageKit / Cloudflare / Bunny / Thumbor / wsrv URLs | M2 (in progress) | no | no | Thumbor only | no | wsrv only |
| Other unpic providers (Next.js, Netlify, Vercel, …) | M2 | no | no | no | no | no |
| **Sources** | | | | | | |
| http(s), S3, GCS, Azure Blob, local filesystem | done (M1) | yes | yes | partial (plugins) | partial (local) | partial (http) |
| FTP / FTPS / SFTP | done (M1) | no | no | no | no | no |
| Multiple named sources | done (M1) | partial | partial | partial | no | no |
| **Formats** | | | | | | |
| JPEG, PNG, GIF, WebP, AVIF, TIFF, BMP, ICO, SVG input | done (M1) | yes | yes | — | — | — |
| HEIC input | done (M1) | yes | yes | partial (plugin) | yes | yes |
| JPEG XL, JPEG 2000, UltraHDR input | done (M1) | partial (JPEG XL) | — | — | — | — |
| AVIF / WebP / JXL output + Accept negotiation | done (M1) | yes | yes | partial | partial | partial |
| Animated GIF / WebP (resize every frame) | done (M1) | yes | yes | — | — | — |
| Camera RAW input | M2 (libraries in the base image; loader planned) | no | paid (loading only, v4.0) | no | no | no |
| PDF / PSD thumbnails | M2 (libraries in the base image; loaders planned) | no | paid | no | partial (PDF) | partial (PDF) |
| Best-format and auto-quality (DSSIM) | M2 | no | paid | no | no | no |
| Video thumbnails, GIF → MP4/WebM | M3 (full image) | no | paid | partial (ffmpeg engine) | no | no |
| **Processing** | | | | | | |
| Resize, crop, gravity, smart crop, focus point | done (M1) | yes | yes | yes | yes | yes |
| Extend, padding, trim | done (M1) | yes | yes | — | — | — |
| Rotate, flip, EXIF auto-orientation | done (M1) | yes | yes | yes | yes | yes |
| Blur, sharpen, pixelate | done (M1) | yes | yes | partial (no pixelate) | partial | partial |
| Watermark (image: position, offsets, scale, opacity, tiling) | done (M1) | yes | yes | yes | yes | no |
| Metadata policy (strip, keep copyright, ICC → sRGB) | done (M1) | yes | yes | — | — | — |
| Colour adjustments, duotone, gradients | M2 | no | paid | yes | no | yes |
| Chained pipelines | M2 | no | paid | no | yes (`/pipeline`) | no |
| Face and object detection (crop, blur) | M3 (full image) | no | paid | partial (OpenCV faces) | no | no |
| Watermarks (text, per-request URL, shadow, rotation) | M3 | no | paid | partial | partial (text) | no |
| **Serving and operations** | | | | | | |
| `/info` endpoint (dimensions, EXIF, IPTC, XMP) | done (M1) | no | paid | partial (`/meta`) | yes | partial (`output=json`) |
| ETag, 304, HEAD, `Cache-Control`, `Vary`, `Content-DPR` | done (M1) | yes | yes | — | — | — |
| `raw` / `skip_processing` passthrough, fallback image | done (M1) | yes | yes | — | — | — |
| Admission control (queue → 429, worker timeout → 503) | done (M1) | yes | yes | — | — | — |
| Prometheus metrics, OpenTelemetry, health and readiness | done (M1) | yes | yes | partial | partial | no |
| Multi-arch Docker image (cosign-signed, SBOM, provenance) | done (M1; first tag pending) | yes | yes | — | — | — |
| Built-in result cache | M2 (optional) | no | paid (v4) | yes | no | partial (nginx) |

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

The config file (YAML, JSON or TOML) has a JSON Schema, so editors validate it, complete keys and show each key's meaning and its `XI_*` variable on hover. Point the file at the published schema:

```yaml
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
server:
  addr: ":8080"
```

JSON files use a top-level `"$schema"` key with the same URL, and TOML files a first line `#:schema <url>` (Taplo, Tombi). A running instance also serves the schema for its exact version on the admin listener (`GET /config.schema.json`), `xtandard-image config schema` prints it, and `xtandard-image config validate --config <file>` checks a file with the real loader. The HTTP API is described by an OpenAPI 3.1 document: `xtandard-image openapi`, admin `GET /openapi.json`, or the [published copy](https://xantiagoma.github.io/xtandard-image/schema/v1/openapi.json) with a [browsable reference](https://xantiagoma.github.io/xtandard-image/api/). See [docs/CONFIG.md](docs/CONFIG.md#config-file-formats-and-schema-adr-0007) and [docs/OPENAPI.md](docs/OPENAPI.md).

## Performance

[bench/RESULTS.md](bench/RESULTS.md) compares xtandard-image with imgproxy OSS on the same machine with matched settings (thumbnails to JPEG/WebP/AVIF, PNG with alpha, animated GIF, 1200 px fill, `/info`); the methodology is in [docs/BENCHMARKS.md](docs/BENCHMARKS.md).

## Documentation

- [Architecture](docs/ARCHITECTURE.md): request lifecycle, packages, the canonical transform model, concurrency and memory, failure modes, and the Docker build.
- [URL dialects](docs/URL-DIALECTS.md): grammar, option mapping and signing for every supported dialect.
- [Formats](docs/FORMATS.md): decoders, encoders, and the best-format decision matrix.
- [Configuration](docs/CONFIG.md): environment variables, the YAML/JSON/TOML config file and its JSON Schema, and Dokploy, Coolify and Kubernetes examples.
- [OpenAPI](docs/OPENAPI.md): the OpenAPI 3.1 document of the HTTP API and where it is published.
- [Release](docs/RELEASE.md): how the image is built, published and verified.
- [Benchmarks](docs/BENCHMARKS.md): methodology and how to reproduce [bench/RESULTS.md](bench/RESULTS.md).
- [Roadmap](docs/ROADMAP.md): the M1, M2 and M3 work packages.
- [ADRs](docs/adr/): language and engine, URL dialects, security model, caching, Docker images, license policy.
- [Go standards](docs/go-standards.md) and [stack research](docs/research/stack.md).

## Contributing

The project is not accepting outside contributions yet. Work is tracked as the work packages in the roadmap. Read `CLAUDE.md` and `docs/go-standards.md` before writing code.

## License

MIT. See [LICENSE](LICENSE). Code derived from imgproxy (Apache-2.0), such as the BMP/ICO codecs, keeps its upstream headers and is credited in `NOTICE`. The Docker images bundle third-party libraries under their own licenses, listed in `/opt/xtandard/share/licenses`.
