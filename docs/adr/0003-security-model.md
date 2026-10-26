# ADR 0003: Security model

- Status: accepted
- Date: 2026-09-26

## Context

An image proxy fetches attacker-chosen URLs and decodes attacker-supplied bytes with C libraries. Classic failures: SSRF into cloud metadata or internal services, open proxying/abuse, decompression bombs, malicious SVG (XSS, XXE, external fetches), and decoder memory-safety bugs.

## Decision

1. **Allowlist is mandatory.** Configured sources are the allowlist. With no sources and no `XI_DEV_MODE=true`, the server refuses to start.
2. **SSRF defence at connect time**: dialer `Control` hook validates the actual peer IP (defeats DNS rebinding) against deny lists (loopback, RFC 1918, link-local incl. metadata IPs, CGNAT, ULA, multicast, IPv4-mapped/NAT64 variants), port allowlist, per-hop redirect re-validation, no HTTPS→HTTP downgrade, max redirects. Private networks require explicit opt-in.
3. **Signing**: imgproxy-compatible HMAC-SHA256 with key/salt pairs (multiple for rotation) plus each dialect's native scheme (imgix MD5 `s`, thumbor HMAC-SHA1, Cloudinary `s--…--`, ImageKit `ik-s`, Bunny token). Constant-time comparison. Unsigned requests governed by `allow | restricted | deny`; `restricted` limits sizes/qualities for client-generated URLs.
4. **Encrypted sources**: AES-CBC with PKCS#7 and IV prefix, URL-safe base64, imgproxy-compatible.
5. **Resource limits** enforced before decode: source bytes, pixels (header probe), frames, frame pixels, PDF pages, RAW demosaic pixels, result dimensions; request/download timeouts; queue timeouts; libvips work cancelled via kill flag.
6. **Decoder hardening**: `vips_block_untrusted_set(TRUE)` then re-enable only shipped loaders; `fail_on=warning`; ImageMagick restricted to PSD/PSB by `policy.xml` with resource caps; own BMP/ICO decoders fuzzed; SVG sanitised in Go before librsvg or passthrough; SVG responses get a sandboxing CSP.
7. **Container**: non-root UID 65532, read-only root filesystem compatible, no shell tools required, capabilities dropped.
8. Per-request security overrides are off by default and can only lower limits unless explicitly allowed.

## Consequences

- Safe defaults cost first-run convenience; dev mode exists for local use and logs a loud warning.
- Client-side URL generators (unpic) cannot sign; `restricted` mode is the compromise and must be documented prominently.
- Security features need continuous fuzzing and a published security policy (`SECURITY.md`) ~~before 1.0~~. *Amended 2026-10-25 (owner, WP-152):* the project follows ZeroVer, so there is no 1.0 (docs/RELEASE.md "Versioning"). `SECURITY.md` is published before the repository goes public.
