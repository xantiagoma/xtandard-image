# Changelog

Each entry covers the server, its Docker images (`slim`, `full`, `gpl` on GHCR) and the official clients in `clients/`. They all share one version, taken from the git tag `vX.Y.Z`. Versions follow [ZeroVer](https://0ver.org) (`0.x`, see [docs/RELEASE.md](docs/RELEASE.md#versioning)): a minor bump may break configuration or URL options and a patch does not. The release notes on [GitHub Releases](https://github.com/xantiagoma/xtandard-image/releases) list the image digests, SBOMs and verification results of each version.

## Unreleased

## v0.1.2 (2026-11-21)

A security and maintenance release: fixes for three bugs found by the nightly fuzzer, the Go 1.27.4 toolchain and dependency updates for every advisory `govulncheck` reports, and base images that no longer depend on the removed ONNX Runtime GitHub repository. No configuration, URL option or client API changes.

### Security

- **Go 1.27.4.** The server is built with Go 1.27.4 (was 1.27.1), which fixes the standard-library advisories GO-2026-6859, -6809, -6613, -6609, -6608, -6607, -6605, -6604, -6600 and -6599, and the http2 copies of the `golang.org/x/net` ones. The base image builders ship Go 1.27.4 (`8.18.6-r8-slim`, `-r11-full`, `-r12-gpl`).
- **Dependencies:** `github.com/klauspost/compress` v1.20.5 (GO-2026-6865) and `golang.org/x/net` v0.63.0 (GO-2026-6617, -6612, -6611, -6610, -6603), which also raises `x/crypto`, `x/sync`, `x/sys` and `x/text`. `govulncheck ./...` reports no vulnerabilities.
- **Bunny tokens are decoded strictly.** Base64 decoding used to ignore the spare low bits of the last character and skip CR and LF, so several spellings of one token (`…kzkA` and `…kzkB`, or a token with `%0D%0A` inside) verified for the same MAC. Only the spelling the Bunny SDKs emit (with or without trailing `=`) is accepted now. Found by `FuzzBunnyVerify`.

### Fixed

- **DDS (BC6H signed):** a signed 16-bit endpoint of `-0x8000` decoded to `-Inf`; signed endpoints are clamped to the symmetric half-float range. Found by `FuzzBlocks`.
- **imgix `crop`:** a token shaped like an object class but unknown to the detector (`crop=a`) parsed as object gravity and then failed validation; it is now a 400 on `crop`. Found by `FuzzParse`.
- Each fuzzer input is committed as a regression seed. A false positive in `FuzzLoadEnv` (a value that was part of the variable name counted as a leaked secret) is fixed too.

### Changed

- **Base images** take ONNX Runtime 1.30.0 (the `full` and `gpl` detection runtime) from Microsoft's official NuGet package `Microsoft.ML.OnnxRuntime`, because the `microsoft/onnxruntime` GitHub repository, and its release downloads, were removed in November 2026. Same version and files; `verify-image.sh` runs inference on both architectures.
- **Brand:** pages show only the SVG logos (lockups and symbol, light and dark) and never the app-icon tile; the PNG logo rasters are gone. The horizontal lockups centre the symbol on the wordmark ([docs/BRAND.md](docs/BRAND.md)).
- **Documentation site:** the home hero is the stacked lockup, and `og:url`, `og:image` and the sitemap use `https`. The README and the site list the xtandard ecosystem (lib, flags, webhooks, audit, links, entitlements, xyte).
- **CI:** the .NET SDK is pinned exactly, Maven is installed with retries and a pinned checksum, and flaky or architecture-dependent tests (FITS dequantisation, the AVIF gain-map golden, the Rust stand-in server) were fixed at the root.
- Every example and deploy file pins `0.1.2`.

### Migration

Nothing to change for clients that use the official SDKs, the documented URL formats or the Bunny SDKs:

- A **Bunny token** spelled differently from what the SDKs produce (non-zero spare bits in its last base64 character, or a CR/LF inside) is now rejected as a bad signature. Regenerate such URLs with an SDK or the playground.
- **`crop=<name>`** with a name that is neither a crop mode nor an object class the detector knows now answers 400 instead of failing later in validation. Check the class list in [URL dialects](docs/URL-DIALECTS.md).
- If you build your own image **`FROM ghcr.io/xantiagoma/xtandard-image-base`**, the new revisions are `8.18.6-r8-slim`, `8.18.6-r11-full` and `8.18.6-r12-gpl` (and their `-builder` tags); the floating `slim`, `full` and `gpl` tags point at them.

## v0.1.1 (2026-10-26)

The first release whose npm package, `@xtandard/image`, is published by the release workflow through npm trusted publishing (OIDC), with a provenance attestation.

### Added

- **Documentation website** at [me.xantiagoma.com/xtandard-image](https://me.xantiagoma.com/xtandard-image/): every doc, client README and ADR with search, navigation and light and dark themes, built with Astro Starlight from the Markdown in the repository. The schema URLs and the `/api/` reference are unchanged ([ADR 0015](docs/adr/0015-documentation-site.md)). (WP-156)

### Changed

- The release workflow publishes `@xtandard/image` to npm from CI (OIDC, provenance) and skips a version that is already published; a non-dry re-run of a tag also updates its GitHub release.

## v0.1.0 (2026-10-26)

The first stable release, and the first release built, signed and attested by GitHub Actions in the now public repository. It contains everything in rc.1 and rc.2 (below), plus the changes listed here. `latest`, `slim`, `full` and `gpl` on GHCR now point at it.

### Added

- **Edge images from `main`:** `main`, `main-full`, `main-gpl` and an immutable `sha-<commit>[-flavour]` per commit, verified, signed and attested like releases ([docs/RELEASE.md](docs/RELEASE.md)). (WP-155)
- **Brand:** logo, palette, favicons and social card ([docs/BRAND.md](docs/BRAND.md)), in the playground, on the site and in every README. (WP-153)
- **Modal** ([modal.com](https://modal.com)) as a deployment target: `deploy/modal/app.py`, with image and video fleets as separate web endpoints ([docs/DEPLOY.md](docs/DEPLOY.md)). (WP-151)
- Community files for the move to a public repository: `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `SUPPORT.md`, issue and pull request templates, `CODEOWNERS` and this changelog. (WP-152)

### Changed

- The repository and the GHCR packages (app and base images) are **public**. Images are signed with keyless cosign and carry SLSA provenance and SBOM attestations; the rc.2 images were re-published that way.
- The client READMEs are written for their package registries (absolute links, install, quick start), and the README states the project's goal and a disclaimer. (WP-154)
- Every example and deploy file pins `0.1.0`.
- The versioning policy is now written down: ZeroVer, with one version for the images and every client ([docs/RELEASE.md](docs/RELEASE.md#versioning)). (WP-152)
- The README is now a short guide for users. The walkthrough, the feature comparison and the playground tour moved to [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md), [docs/COMPARISON.md](docs/COMPARISON.md) and [docs/PLAYGROUND.md](docs/PLAYGROUND.md). (WP-152)

## v0.1.0-rc.2 (2026-10-24)

The second prerelease, built from commit `a9a493a4`. Like rc.1 it was first built and verified locally, while GitHub Actions was unavailable; on 2026-10-26 the release workflow re-published its images signed and attested, as public GHCR packages.

### Added

- **Video and audio transformations (M5):** `/video` (imgproxy) and `/q/video` (query), plus the Cloudinary, ImageKit and Cloudflare Media paths.
  - Video: trim; resize, crop and pad with gravity; fps; codecs (WebM/VP9, AV1-in-MP4 on `full`, H.264 and HEVC on `gpl`); CRF and a bit-rate cap; overlays, a timecode and waveform pictures.
  - Audio-only output (MP3, M4A, Opus, FLAC, WAV), with volume, EBU R128 loudness and fades.
  - Just-in-time HLS and DASH (fMP4 or MPEG-TS segments).
  - Cost-based admission, a disk spool, video presets and an unsigned video policy (`allow`, `restricted` or `deny`; the default is `deny`).
  - `raw:1` passthrough and `XI_ROLE=image|video` fleets.
- **Official clients (M6):** TypeScript (`@xtandard/image`), Python, Go, PHP, Ruby, Rust, Java, .NET, Elixir, Swift, Kotlin Multiplatform and Dart. All twelve are built from one spec and pass the same conformance cases. None is on a package registry yet.
- **HTTP `QUERY` (RFC 10008)** on the query dialect: the options go in the request body (form or JSON), with the same signature, limits and cache entry as the GET URL. The clients send it only when asked to.
- **Deployment and operations (M7):**
  - [docs/DEPLOY.md](docs/DEPLOY.md): Compose, Swarm, Nomad, Dokploy, Coolify, Fly.io, Cloud Run, ECS/Fargate, Azure Container Apps, Railway, Render, DigitalOcean, Terraform/OpenTofu, Pulumi, SST and AWS CDK.
  - [docs/OPERATIONS.md](docs/OPERATIONS.md): sizing, autoscaling, CDN recipes and observability.
  - A Helm chart and Kustomize manifests with HPA and KEDA, a Grafana dashboard, and Prometheus alert and SLO rules.
- **Playground:**
  - global tools;
  - before/after for video and audio, with linked playback and A/B listening;
  - 55 media recipes;
  - code snippets for every official client.
- **Formats and M4 follow-ups:**
  - long-tail formats;
  - EPS through sandboxed Ghostscript (`gpl` image only, off by default);
  - the Adobe DNG SDK and GoPro GPR (`gpl` image only);
  - ImageNet classification;
  - encrypted per-request PDF passwords;
  - every `/info` option.

### Changed

- Six settings now take effect. rc.1 accepted them but ignored them: `XI_SECURITY_SECRET`, `XI_SECURITY_ALLOWED_PROCESSING_OPTIONS`, `XI_FETCH_MAX_CONCURRENT`, `XI_PROCESSING_GO_MEMORY_FRACTION`, `XI_SERVER_READY_QUEUE_THRESHOLD` and `XI_SERVER_MAX_CONNECTIONS`. **If you set `XI_SECURITY_SECRET` on rc.1, requests now need `Authorization: Bearer <secret>`.**
- The amd64 `gpl` sandbox works under CPU emulation (Rosetta or QEMU). There, it drops only the address-space limit. `XI_FORMATS_METAFILE_ENABLED=auto` no longer stops startup when the converter's self-check fails. (WP-118)

### Verification

Acceptance passed on all three flavours, on arm64 (native) and on amd64 (Rosetta emulation): slim 143/143, full 155/155 and gpl 155/155.

## v0.1.0-rc.1 (2026-09-29)

The first prerelease, built from commit `8c9d16b`. It was built and pushed locally because GitHub Actions was unavailable. The images are not signed, and they are private GHCR packages.

### Added

- **M1, core:**
  - the imgproxy dialect end to end, plus the query dialect;
  - http, S3, GCS, Azure Blob, filesystem, FTP/FTPS and SFTP sources;
  - core geometry, filters, watermarks, and metadata and ICC handling;
  - every still-format encoder and `Accept` negotiation;
  - signing and `enc/` source encryption;
  - a mandatory source allowlist and SSRF protection, DNS rebinding included;
  - admission control (429 and 503);
  - Prometheus metrics, health and readiness;
  - an image that runs read-only as UID 65532.
- **M2, breadth:**
  - every URL dialect: imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Thumbor, wsrv, AWS Serverless Image Handler and the unpic providers;
  - camera RAW (LibRaw), PDF (PDFium), PSD and HDR inputs;
  - a memory, disk, S3 and Redis result cache;
  - best format and auto-quality, chained pipelines, colour adjustments and the imgproxy Pro processing options;
  - `/info` with the Pro fields, and client hints;
  - a config JSON Schema and an OpenAPI 3.1 document.
- **M3, intelligence, video and polish:**
  - the `full` image, with FFmpeg (LGPL) and ONNX Runtime;
  - face and object detection, with crops and filters based on what it finds;
  - video thumbnails, sprites and animations, and GIF to video;
  - the imgix mount, and advanced and text watermarks;
  - the playground (off by default);
  - the TypeScript package;
  - the opt-in `gpl` flavour.
- **M4, playground and long tail:**
  - playground v2: an option builder for every dialect, translation between dialects, an Info panel and watermark recipes;
  - detection on video frames and through chained pipelines;
  - long-tail inputs: JPEG XR, PNM, TGA, PCX, DDS, QOI and more;
  - designer containers: XCF, ORA, KRA, Procreate and Sketch;
  - Lab TIFF, CMYK JPEG XL, BC6H DDS, AVIF sequences and gain maps, and 12/16-bit JPEG;
  - FITS, DICOM, KTX2, WMF and EMF;
  - password-protected PDFs and the `classify` info option.

### Known limitations

- On amd64 under Rosetta emulation, the `gpl` EPS case failed: 132 of 133 acceptance cases passed. WP-118 fixed it in rc.2.
