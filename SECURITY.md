# Security policy

xtandard-image fetches and decodes untrusted files from the network for a living, so we take reports seriously. Thank you for taking the time to report one responsibly.

## Reporting a vulnerability

**Do not open a public issue, discussion or pull request for a vulnerability.**

Report it privately through GitHub's private vulnerability reporting: open the repository's **Security** tab, then choose **Report a vulnerability**, or go to [github.com/xantiagoma/xtandard-image/security/advisories/new](https://github.com/xantiagoma/xtandard-image/security/advisories/new). Only the maintainer can see the report.

Please include:

- the image flavour (`slim`, `full` or `gpl`) and version (`xtandard-image version`, or the image tag or digest), or the client and its version;
- the relevant configuration, with any secrets removed;
- a request or file that reproduces the problem, and what happens;
- the impact as you understand it.

What to expect: an acknowledgement within 7 days, and an assessment within 14 days. We agree on a disclosure date with you, normally once a fix is released and no later than 90 days after the report. A fix ships as a patch release of the latest `0.x` minor, with a GitHub security advisory (and a CVE where one applies). We credit you in the advisory unless you ask us not to. This is a one-maintainer project: these are goals, not contractual deadlines.

## Supported versions

Versions follow [ZeroVer](https://0ver.org) (`0.MINOR.PATCH`, [docs/RELEASE.md](docs/RELEASE.md#versioning)). Only the **latest minor release line** gets security fixes, as a new patch release. Before the first stable release, that means the latest prerelease. Older minors and older prereleases are not patched. Upgrade to the latest patch of the latest minor.

| Version | Supported |
|---|---|
| latest `0.MINOR.x` (today: `0.1.0` prereleases) | yes |
| any older line | no |

The Docker images also contain the base image's third-party libraries (libvips, the codecs, FFmpeg, ONNX Runtime, Ghostscript in `gpl`, Ubuntu packages). A vulnerability in one of them that affects xtandard-image is in scope: report it here as well as upstream. The `base-image` workflow rebuilds the base images weekly with Ubuntu security updates, once GitHub Actions is enabled on the repository again. A patch release picks up the rebuild.

## Scope

In scope:

- **Server-side request forgery (SSRF).** This includes reaching private, loopback, link-local or metadata addresses through a source, a redirect, DNS rebinding or a watermark or overlay URL; a source allowlist bypass; and path traversal out of an `fs` source.
- **Signing and encryption.** This includes accepting a forged, tampered or expired URL in any dialect (imgproxy HMAC, `xi_sig`, imgix, Cloudinary, ImageKit, Cloudflare, Bunny, Thumbor, AWS SIH); signature timing leaks; weaknesses in `enc/` source or PDF password encryption; and keys or secrets leaking into responses, logs or metrics.
- **Resource exhaustion that gets past the limits.** For example: decompression bombs, pixel, frame and page limits, the input size limit, animation and video limits, cost-based video admission, and the request queue (429/503). A file or URL that uses far more memory or CPU than the configured limits allow is a vulnerability. Slowness within those limits is not.
- **Memory-safety bugs** in the C code this project owns (`internal/vips`, `internal/video/ffmpeg` and their shims) or in its use of libvips, FFmpeg and the other libraries; crashes caused by crafted inputs.
- **The `gpl` image's Ghostscript sandbox.** EPS, PostScript and Illustrator input is **off by default** (`XI_FORMATS_EPS_ENABLED`). When it is on, Ghostscript runs as a sandboxed subprocess with `-dSAFER` and resource limits ([ADR 0011](docs/adr/0011-ghostscript-subprocess.md), [CONFIG](docs/CONFIG.md)). An escape from that sandbox, such as file access, command execution or network access, is in scope. So is an escape of the metafile converter's sandbox. Ghostscript bugs that stay inside the sandbox belong upstream.
- **SVG sanitisation**, response header injection, and cache poisoning through the result cache or the `Vary` and `ETag` headers.
- **The playground and the admin listener**, if they expose more than documented. The playground signs URLs for anyone who can reach it, by design. Keep it on the private admin listener; `XI_PLAYGROUND_PUBLIC=true` is for trusted networks only.
- **The official clients** (`clients/*`), if they build URLs that sign or encrypt incorrectly.

Out of scope:

- Problems that need a configuration the docs warn against: `XI_DEV_MODE=true` in production, `XI_SECURITY_UNSIGNED_POLICY=allow` on a public listener, EPS input enabled for untrusted users, the admin listener or a public playground on the internet, or private networks allowed through `XI_FETCH_ALLOWED_NETWORKS`.
- Volumetric denial of service, and the cost of requests that the limits you configured allow.
- Findings from automated scanners, without a working proof of concept.
- The deployment examples in `deploy/`, except where their defaults are insecure as shipped.

## Hardening

The secure defaults and the operational guidance are in [docs/CONFIG.md](docs/CONFIG.md) (security, sources and limits), [docs/SOURCES.md](docs/SOURCES.md) and [docs/OPERATIONS.md](docs/OPERATIONS.md). In short:

- sign every URL;
- keep the source allowlist narrow;
- run the image read-only with all capabilities dropped;
- keep the admin listener private;
- put a CDN in front.
