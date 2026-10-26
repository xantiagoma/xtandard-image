# Getting started

This walkthrough takes you from nothing to a signed, allowlisted server and a Compose service for Dokploy or Coolify. The [README](../README.md#quick-start) has a 30-second version. CI runs every command on this page and in the README quick start exactly as written, except the `git clone` (`scripts/quickstart.sh`; locally `task quickstart`). You need Docker, `curl` and `openssl`.

## Choose a flavour

Every flavour is a multi-arch image (linux/amd64 and linux/arm64) on the same `ubuntu:noble` + libvips 8.18 base, built from `docker/base`:

| Flavour | Contents | Status |
|---|---|---|
| `slim` | every still format: JPEG, PNG, GIF, WebP, AVIF, HEIC (decode), JPEG XL, TIFF, JPEG 2000, UltraHDR, SVG, PDF (PDFium), RAW (LibRaw), OpenEXR, PSD, JPEG XR (jxrlib) and the legacy formats (TGA, PCX, DDS, QOI, SGI, Sun raster, XBM, XPM, PAM, WBMP, ICNS) and the designer files (XCF, ORA, KRA, Procreate, Sketch, SVGZ, Paint.NET) | default |
| `full` | slim + FFmpeg 8.1 (LGPL only: video decode, VP8/VP9 and AV1 encode, no H.264/HEVC encode) + ONNX Runtime 1.30 (CPU) + the YuNet (MIT) face and YOLOX-s (Apache-2.0) object models and the MobileNetV3-Large (Apache-2.0) ImageNet classifier | published as prereleases (`:0.1.0-rc.2-full`); signed builds wait for Actions (`task docker:base:full`; detection loads its ONNX Runtime and models at startup, `XI_DETECT_*`) |
| `gpl` | full + FFmpeg rebuilt under the GPL with x264 and x265: adds H.264 video encoding (MP4 that plays in every browser and phone) and HEVC video encoding; and Ghostscript (`gs`, AGPL) for EPS/PostScript input, used only with `XI_FORMATS_EPS_ENABLED=true` (off by default, [ADR 0011](adr/0011-ghostscript-subprocess.md)); and LibRaw with the Adobe DNG SDK (its own licence, with an indemnity clause) and the GoPro GPR SDK, which decode JPEG XL DNG and GoPro GPR (slim and full serve their embedded preview or answer 422). **Contains GPL and AGPL software; see below** | published as prereleases (`:0.1.0-rc.2-gpl`); signed builds wait for Actions (`task docker:base:gpl`); opt-in |

`slim` and `full` contain no GPL code. `gpl` does: its FFmpeg is GPL-2.0-or-later, H.264 and HEVC are covered by patents, and its Ghostscript is AGPL-3.0-or-later (a separate program, run only when you enable EPS input; enabling it also runs a PostScript interpreter on untrusted input, read the security note in [CONFIG.md](CONFIG.md) first). The project does not sell anything and grants no patent licence, so if you run or redistribute the `gpl` image, checking that the GPL and those patents are acceptable for your use is your job; if you are unsure, use `full` (WebM/VP9 and AV1-in-MP4 video). The xtandard-image code itself stays MIT. Every image carries its licences in `/opt/xtandard/share/licenses/`, and the gpl image adds `LICENSE-GPL.md` there ([ADR 0006](adr/0006-license-policy.md)).

## 1. Get the image

The commands on this page use the published image, `ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2` (slim; add `-full` or `-gpl` to the tag for the other flavours). It is multi-arch (linux/amd64 and linux/arm64), public on GHCR ([release notes](https://github.com/xantiagoma/xtandard-image/releases/tag/v0.1.0-rc.2)). The moving `latest`, `slim`, `full` and `gpl` tags arrive with the first stable release, `v0.1.0` ([how tags map](RELEASE.md#versioning)).

```sh
docker pull ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
```

To build your own image from a clone instead (a few minutes; the Dockerfile pulls the prebuilt libvips base image `ghcr.io/xantiagoma/xtandard-image-base`), and then use `xtandard-image` in place of the GHCR reference in the commands below:

```sh
git clone https://github.com/xantiagoma/xtandard-image.git
cd xtandard-image
```

```sh quickstart-build
docker build -t xtandard-image .
```

## 2. Try it in dev mode

`XI_DEV_MODE=true` accepts any public http(s) source. Unsigned URLs are allowed, but only with the widths, heights and qualities of the `restricted` policy (Next.js sizes: 640, 750, 828, 1080, 1200, …; the server logs the list at startup):

```sh quickstart
docker run -d --name xtandard-image -p 8080:8080 -e XI_DEV_MODE=true \
  ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
until curl -fsS http://localhost:8080/healthz >/dev/null; do sleep 1; done

# 640 px wide WebP (imgproxy URL: /<signature>/<options>/plain/<source>@<format>)
curl -fsS -o thumb.webp \
  'http://localhost:8080/insecure/rs:fit:640:0/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb@webp'

# Smart crop to 640x384; no extension, so the format follows Accept (AVIF here)
curl -fsS -o crop.avif -H 'Accept: image/avif,image/webp,*/*' \
  'http://localhost:8080/insecure/rs:fill:640:384/g:sm/plain/https://images.unsplash.com/photo-1506744038136-46273834b3fb'

docker rm -f xtandard-image
```

## 3. Allowlist a source and sign URLs

The production shape: one http source that only allows `images.unsplash.com`, and imgproxy-compatible signing keys. `scripts/sign-url.sh` signs a path with the same key and salt (it works for imgproxy too):

```sh quickstart
export XI_SIGNING_KEYS="$(openssl rand -hex 32)" XI_SIGNING_SALTS="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web \
  -e XI_SOURCES_WEB_TYPE=http \
  -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS -e XI_SIGNING_SALTS \
  ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
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

`info.json` from that run (this source carries no EXIF, IPTC or XMP, hence the empty sections; options such as `cs:1/pn:1/a:1` add colorspace, page count and alpha, [URL-DIALECTS §3.3](URL-DIALECTS.md)):

```json
{"size":4696118,"format":"jpeg","mime_type":"image/jpeg","width":7360,"height":4912,"orientation":1,"exif":{},"iptc":{},"xmp":{}}
```

## 4. Deploy with Dokploy or Coolify

Both accept a compose file. Create a **Docker Compose** service, paste this, set `XI_SIGNING_KEYS` / `XI_SIGNING_SALTS` (`openssl rand -hex 32` each) and your allowlist in the environment tab, and route a domain to port 8080 (Dokploy: *Domains*; Coolify: the service's domain field):

```yaml quickstart-compose
services:
  xtandard-image:
    image: ${XI_IMAGE:-ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2}
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

The image declares a Docker `HEALTHCHECK` (`/healthz`); Prometheus metrics are on the admin listener (`XI_ADMIN_ADDR`, default `127.0.0.1:9090`, set `:9090` to scrape it from another container). Put a CDN in front for caching. Every variable is documented in [docs/CONFIG.md](CONFIG.md), which also has Kubernetes and larger Dokploy/Coolify examples; `XI_COMPAT_IMGPROXY_ENV=true` accepts your existing `IMGPROXY_*` variables.

Other platforms (Docker Swarm, Nomad, Fly.io, Cloud Run, ECS/Fargate, Azure Container Apps, Railway, Render, DigitalOcean, Modal) and Terraform/OpenTofu, Pulumi, SST and AWS CDK, each with autoscaling and separate image and video fleets: [docs/DEPLOY.md](DEPLOY.md).
