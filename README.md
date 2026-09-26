# xtandard-image

> **Status: pre-alpha, design phase.** Nothing below works yet. The documents in `docs/` are the plan; the feature table describes the target for 1.0, not the current state. Do not deploy.

A self-hosted image processing proxy in Go and libvips. The goal is to offer every imgproxy Pro processing feature for free under the MIT license, support more input formats than anything else in the category, and accept many URL dialects: imgproxy, imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Thumbor, wsrv/weserv and the rest of the [unpic](https://unpic.pics/providers/) provider list. It ships as a ready-to-run Docker image for Dokploy, Coolify and Kubernetes, with no build step.

## Why (design goals, none implemented yet)

- **imgproxy URLs work unchanged.** Signing, `enc/` sources, presets, `/info`, chained pipelines and the Pro options are all part of the plan.
- **Point your front end at it and keep your code.** An unpic `<Image>` configured for imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Netlify, Next.js and others can target xtandard-image as if it were that provider.
- **Wide format support.** Inputs: JPEG, PNG, GIF, WebP, AVIF, HEIC, JPEG XL, TIFF, BMP, ICO, SVG, PDF, PSD, JPEG 2000, UltraHDR, and camera RAW (CR3, NEF, ARW, DNG and more, using the embedded preview first). The `full` image adds video.
- **Secure by default.** It refuses to start without a source allowlist. It also protects against SSRF (including DNS rebinding), enforces decompression-bomb and pixel limits, sanitises SVG, and restricts ImageMagick to PSD.
- **CDN-friendly and stateless.** Responses carry correct `Cache-Control`, `ETag`, `Vary: Accept`, `Content-DPR` and `Content-Disposition` headers. Requests for the same original are coalesced. Result caching (memory, disk, S3, Redis) is optional.
- **No GPL anywhere.** Only LGPL, BSD, MIT and Apache dependencies are used (see [ADR 0006](docs/adr/0006-license-policy.md)).

## Feature matrix (target, nothing shipped yet)

The xtandard-image column is the **planned milestone** from the [roadmap](docs/ROADMAP.md); no feature is implemented today. Competitor legend: **yes**, **paid** (paid tier only), **partial**, **no**. Competitor data comes from their public docs and source as of September 2026.

| Feature | xtandard-image (planned) | imgproxy OSS | imgproxy Pro | thumbor | imaginary | weserv |
|---|---|---|---|---|---|---|
| License | MIT | Apache-2.0 | commercial | MIT | MIT | BSD-3 |
| Resize / crop / gravity / smart crop | M1 | yes | yes | yes | yes | yes |
| Face and object detection (crop, blur) | M3 (full image) | no | paid | partial (OpenCV faces) | no | no |
| AVIF / WebP / JXL output + Accept negotiation | M1 | yes | yes | partial | partial | partial |
| Best-format and auto-quality (DSSIM) | M2 | no | paid | no | no | no |
| HEIC input | M1 | yes | yes | partial (plugin) | yes | yes |
| Camera RAW input | M2 | no | paid (loading only, v4.0) | no | no | no |
| PDF / PSD thumbnails | M2 | no | paid | no | partial (PDF) | partial (PDF) |
| Video thumbnails | M3 (full image) | no | paid | partial (ffmpeg engine) | no | no |
| GIF → MP4/WebM | M3 (full image) | no | paid | partial | no | no |
| Watermarks (image) | M1 | yes | yes | yes | yes | no |
| Watermarks (text, per-request URL, shadow, rotation) | M3 | no | paid | partial | partial (text) | no |
| Colour adjustments, duotone, gradients | M2 | no | paid | yes | no | yes |
| Chained pipelines | M2 | no | paid | no | yes (`/pipeline`) | no |
| Signed URLs | M1 | yes | yes | yes | yes | no |
| Encrypted source URLs | M1 | no | paid | no | no | no |
| S3 / GCS / Azure / local sources | M1 | yes | yes | partial (plugins) | partial (local) | no |
| FTP / SFTP sources | M1 | no | no | no | no | no |
| Multiple named sources | M1 | partial | partial | partial | no | no |
| Built-in result cache | M2 (optional) | no | paid (v4) | yes | no | partial (nginx) |
| imgproxy URL compatibility | M1 | yes | yes | no | no | no |
| imgix / Cloudinary / ImageKit / Cloudflare / Bunny / Thumbor / wsrv URLs | M2 | no | no | Thumbor only | no | wsrv only |
| Prometheus metrics, OpenTelemetry | M1 | yes | yes | partial | partial | no |

Sources: https://imgproxy.net/features/, https://docs.imgproxy.net/, https://thumbor.readthedocs.io/, https://github.com/h2non/imaginary, https://wsrv.nl/docs/. Corrections are welcome.

## Quick start (planned, does not work yet)

```sh
docker run --rm -p 8080:8080 \
  -e XI_SOURCES=web \
  -e XI_SOURCES_WEB_TYPE=http \
  -e XI_SOURCES_WEB_ALLOW='images.unsplash.com' \
  -e XI_DIALECTS_WSRV_ENABLED=true \
  -e XI_SECURITY_UNSIGNED_POLICY=allow \
  ghcr.io/xantiagoma/xtandard-image:slim

# imgproxy dialect (unsigned, because no keys are configured)
open 'http://localhost:8080/unsafe/rs:fill:600:400/g:sm/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb@webp'

# wsrv-style query dialect
open 'http://localhost:8080/?url=images.unsplash.com/photo-1506744038136-46273834b3fb&w=600&h=400&fit=cover&output=webp'
```

`XI_SECURITY_UNSIGNED_POLICY=allow` is for local trials only: the default (`restricted`) accepts only a fixed list of widths and qualities for unsigned URLs. For production, set signing keys (`XI_SIGNING_KEYS`, `XI_SIGNING_SALTS`) and put a CDN in front. Use `:full` instead of `:slim` for video thumbnails and face/object detection. Images are published for linux/amd64 and linux/arm64.

## Documentation

- [Architecture](docs/ARCHITECTURE.md): request lifecycle, packages, the canonical transform model, concurrency and memory, failure modes, and the Docker build.
- [URL dialects](docs/URL-DIALECTS.md): grammar, option mapping and signing for every supported dialect.
- [Formats](docs/FORMATS.md): decoders, encoders, and the best-format decision matrix.
- [Configuration](docs/CONFIG.md): environment variables, YAML, and Dokploy, Coolify and Kubernetes examples.
- [Roadmap](docs/ROADMAP.md): the M1, M2 and M3 work packages.
- [ADRs](docs/adr/): language and engine, URL dialects, security model, caching, Docker images, license policy.
- [Go standards](docs/go-standards.md) and [stack research](docs/research/stack.md).

## Contributing

The project is not accepting outside contributions yet. Work is tracked as the work packages in the roadmap. Read `CLAUDE.md` and `docs/go-standards.md` before writing code.

## License

MIT. See [LICENSE](LICENSE). Code derived from imgproxy (Apache-2.0), such as the BMP/ICO codecs, keeps its upstream headers and is credited in `NOTICE`. The Docker images bundle third-party libraries under their own licenses, listed in `/opt/xtandard/share/licenses`.
