# Configuration

Draft reference. Names are stable once M1 ships; until then they may change in the owning work package.

## 1. Sources of configuration and precedence

Lowest to highest:

1. Built-in defaults.
2. Config file (YAML, JSON or TOML, by extension): `--config <path>` or `XI_CONFIG_FILE`. When neither is set, whichever of `/etc/xtandard/config.{yaml,yml,json,toml}` exists is read; finding more than one is an error. `--config /dev/null` reads no file.
3. `IMGPROXY_*` variables, only when `XI_COMPAT_IMGPROXY_ENV=true` (migration aid; translated to the XI equivalents listed in [imgproxy variables](#imgproxy-variables), e.g. `IMGPROXY_KEY` → `XI_SIGNING_KEYS`).
4. `XI_*` environment variables.
5. Per-request security overrides (`max_src_resolution`, …) only when `XI_SECURITY_ALLOW_SECURITY_OPTIONS=true`, and only downward unless `XI_SECURITY_ALLOW_RAISING_LIMITS=true`.

Rules:

- **Mapping**: an env var is the YAML path upper-cased, dots → `_`, prefixed `XI_`. `limits.max_src_bytes` ↔ `XI_LIMITS_MAX_SRC_BYTES`; map entries use the key: `sources.assets.bucket` ↔ `XI_SOURCES_ASSETS_BUCKET`. Map keys are lower-case `[a-z0-9-]`; `-` becomes `_` in env names. Named sources are the only dynamic part: the effective sources are the YAML `sources` keys plus the names listed in `XI_SOURCES`. A test in `internal/config` checks this rule for every field and keeps the tables below in sync with the code (names and defaults).
- **Empty values**: an empty variable (`XI_SERVER_ADDR=`) counts as unset and does not override the file.
- **Lists**: comma-separated in env; YAML sequences in the file. An env list **replaces** the YAML list (maps such as `XI_PRESETS` and `_HEADERS` replace too). Surrounding spaces are trimmed. Integer allowlists also accept ranges: `640-1080/220`, `5-100/5` (start–end/step).
- **Secrets**: any variable accepts a `_FILE` suffix (`XI_SIGNING_KEYS_FILE=/run/secrets/xi_keys`) for Docker/Kubernetes secrets, including dynamic ones (`XI_SOURCES_ASSETS_SECRET_ACCESS_KEY_FILE`). The trailing newline is trimmed; setting both `X` and `X_FILE` is an error. Variables whose own name ends in `_FILE` (`XI_CONFIG_FILE`, `XI_PRESETS_FILE`) are ordinary variables. Inside a config file, the equivalent is a `{file: /path}` reference (see "Secrets in the file" below). A secret file may be at most 64 KiB. Secret fields print as `[REDACTED]` everywhere (`config print`, logs, errors).
- **Types**: durations use Go syntax (`5s`, `1m30s`; negative rejected); sizes are bytes or binary suffixes `KiB/MiB/GiB/TiB` (`10MiB`; decimal `MB` is rejected as ambiguous); networks are CIDRs or single addresses (`10.0.0.0/8`, `fd00::1`; host bits must be zero); keys are hex; enums are case-insensitive.
- **Validation** is strict: unknown YAML keys and unknown `XI_` variables are a startup error (typos should not silently disable security); the error suggests the closest known name. `XI_ALLOW_UNKNOWN_ENV=true` downgrades unknown variables to a startup warning. `Validate` reports every violation at once, each with its YAML path and variable. `xtandard-image config print` shows the effective config with secrets redacted.
- **Secure by default**: with no sources configured and `XI_DEV_MODE` unset, the server refuses to start.

### Config file formats and schema (ADR 0007)

The file is YAML, JSON or TOML, chosen by extension (`.yaml`/`.yml`, `.json`, `.toml`; anything else is a startup error). All three have the same keys and the same strictness: JSON goes through the same strict YAML decoder, and TOML is converted to the same tree first (TOML dates and times are rejected because no key takes one, and errors after the conversion name the dotted key path rather than a TOML line). A top-level `$schema` key is ignored in every format. Precedence and the env mapping above do not change. A JSON Schema (draft 2020-12) of the file gives editors validation, autocomplete and hover docs; each hover shows the CONFIG.md meaning, the matching `XI_*` variable and, for secrets, its `_FILE` form. The schema is generated from the `Config` struct and the tables below (`go test ./internal/config -run TestJSONSchema -update`), committed as `internal/config/config.schema.json`, and a test fails when it is stale. Its `$id` is the URL below. Stable URLs, published per release (planned: WP-81b):

```
https://xantiagoma.github.io/xtandard-image/schema/config.schema.json            # latest release
https://xantiagoma.github.io/xtandard-image/schema/<version>/config.schema.json  # pinned
```

YAML (VS Code with the Red Hat YAML extension, JetBrains, Neovim/Helix with yaml-language-server). Paste this as the first line:

```
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/config.schema.json
```

JSON: a top-level `"$schema"` key, which the loader ignores:

```
{ "$schema": "https://xantiagoma.github.io/xtandard-image/schema/config.schema.json", "server": { "addr": ":8080" } }
```

TOML (Taplo / Even Better TOML, Tombi). Put this directive first:

```
#:schema https://xantiagoma.github.io/xtandard-image/schema/config.schema.json
```

After the SchemaStore listing (planned: WP-81b), files named `xtandard-image.{yaml,yml,json,toml}` are matched without the line. The schema also ships in the binary, and every copy carries the same `$id`; all its `$ref`s are local, so any copy resolves the same way:

- The admin listener serves it at `GET /config.schema.json` (`Content-Type: application/schema+json`, strong `ETag`, `304` on `If-None-Match`), which is always on and never on the public listener. A self-hosted instance can point editors at its own copy, which always matches the running version: `# yaml-language-server: $schema=http://127.0.0.1:9090/config.schema.json` (use `XI_ADMIN_ADDR`'s host and port, or `kubectl port-forward`). The same URL works in `"$schema"` and `#:schema`.
- `xtandard-image config schema > config.schema.json` writes the same bytes to a file, for offline editors.
- `xtandard-image config validate [--config config.yaml] [--no-env]` checks a file with the real loader (file, then `IMGPROXY_*`/`XI_*`, then `Validate`). It prints `<file>: ok`, or `<file>: invalid` followed by every violation with its key path and variable, and exits 1. `--no-env` ignores `XI_*` and `IMGPROXY_*`, so a config repository's CI can check the file on its own.

The loader, not the schema, is the authority. The schema is slightly stricter in places an editor benefits from (enum values are lower case, a source must name its `type` in the file); tests keep both in agreement on a corpus of valid and invalid files.

**Secrets in the file**. `_FILE` variants are env-only, so the file has no `_file` keys. Instead, any secret-typed value (a `Secret` or hex key, one element of a list of them, or one value of a map of them) may be written as a file reference:

```yaml
signing:
  keys:
    - { file: /run/secrets/xi_key_current }
    - { file: /run/secrets/xi_key_previous }
  salts:
    - { file: /run/secrets/xi_salt_current }
    - { file: /run/secrets/xi_salt_previous }
sources:
  assets:
    type: s3
    bucket: my-assets
    access_key_id: AKIAEXAMPLE
    secret_access_key: { file: /run/secrets/s3_secret }
```

A reference means the same as `XI_…_FILE` for that one value: the file is read at startup, trailing newlines are trimmed, and the content is parsed like the literal (the same function reads both). The path must be absolute, and the file must exist and be at most 64 KiB, or startup fails with the key path in the error. `XI_…` and `XI_…_FILE` in the environment still win. JSON (`{"file": "/run/secrets/x"}`) and TOML (`{ file = "/run/secrets/x" }`) use the same shape. Nothing else in the file is interpolated, and there is no `${…}` syntax (ADR 0007 decision 7).

## 2. Environment variable reference

### Loader

| Variable | Default | Meaning |
|---|---|---|
| `XI_CONFIG_FILE` | `` | Config file, `.yaml`/`.yml`/`.json`/`.toml` (`--config` wins); when both are unset, the one of `/etc/xtandard/config.{yaml,yml,json,toml}` that exists is read |
| `XI_ALLOW_UNKNOWN_ENV` | `false` | Report unknown `XI_*` variables as warnings instead of failing startup |

### Core and server

| Variable | Default | Meaning |
|---|---|---|
| `XI_DEV_MODE` | `false` | Allow any http(s) source (SSRF rules still apply), pretty errors, no signature requirement warnings |
| `XI_SERVER_ADDR` | `:8080` | Listen address |
| `XI_SERVER_PATH_PREFIX` | `` | Global prefix stripped before routing |
| `XI_SERVER_REQUEST_TIMEOUT` | `20s` | Whole-request deadline |
| `XI_SERVER_READ_HEADER_TIMEOUT` | `5s` | Slowloris guard |
| `XI_SERVER_READ_TIMEOUT` | `15s` | |
| `XI_SERVER_WRITE_TIMEOUT` | request timeout + `5s` | |
| `XI_SERVER_IDLE_TIMEOUT` | `90s` | Keep-alive idle |
| `XI_SERVER_MAX_HEADER_BYTES` | `32KiB` | |
| `XI_SERVER_MAX_CONNECTIONS` | `4096` | Accepted connections cap |
| `XI_SERVER_SHUTDOWN_DELAY` | `5s` | Readiness goes 503 this long before listener closes |
| `XI_SERVER_SHUTDOWN_TIMEOUT` | `30s` | Drain limit |
| `XI_SERVER_READY_QUEUE_THRESHOLD` | `0` (off) | `/readyz` 503 when queue fill % ≥ value |
| `XI_SERVER_CORS_ORIGINS` | `` | Allowed CORS origins (`*` allowed) |

### Security, signing, encryption

| Variable | Default | Meaning |
|---|---|---|
| `XI_SIGNING_KEYS`, `XI_SIGNING_SALTS` | `` | Hex key/salt pairs (same count); enables imgproxy-scheme and `xi_sig` verification |
| `XI_SIGNING_SIZE` | `32` | Signature bytes before base64 (imgproxy `SIGNATURE_SIZE`) |
| `XI_SIGNING_TRUSTED` | `` | Signatures always accepted |
| `XI_SIGNING_REQUIRED` | `true` when keys set | Reject unsigned imgproxy URLs |
| `XI_ENCRYPTION_KEYS`, `XI_ENCRYPTION_KEY` | `` | Hex AES keys (16/24/32 bytes) for `enc/` sources; the first encrypts, all decrypt (rotation). `XI_ENCRYPTION_KEY` is the deprecated single-key alias (YAML `encryption.key`); setting both is an error |
| `XI_SECURITY_UNSIGNED_POLICY` | `restricted` | `allow` \| `restricted` \| `deny` for every request that carries no verified signature, in any dialect (including imgproxy URLs when `XI_SIGNING_REQUIRED` is false) |
| `XI_SECURITY_ALLOWED_WIDTHS` | `16,32,48,64,96,128,256,384,640,750,828,1080,1200,1920,2048,3840` | Restricted-policy widths (Next.js device + image sizes); logged at startup |
| `XI_SECURITY_ALLOWED_HEIGHTS` | same as `XI_SECURITY_ALLOWED_WIDTHS` | Restricted-policy heights |
| `XI_SECURITY_ALLOWED_DPRS` | `1` | Restricted-policy device pixel ratios (0 < dpr ≤ 8); unset and `1` are always allowed |
| `XI_SECURITY_ALLOWED_QUALITIES` | `5-100/5` | Restricted-policy qualities (5, 10, …, 100) |
| `XI_SECURITY_ALLOW_SECURITY_OPTIONS` | `false` | Honour per-request limit options |
| `XI_SECURITY_ALLOW_RAISING_LIMITS` | `false` | Let them exceed config |
| `XI_SECURITY_SECRET` | `` | Require `Authorization: Bearer <secret>` |
| `XI_SECURITY_ALLOWED_PROCESSING_OPTIONS` | `` (all) | imgproxy option allowlist |

### Sources and fetching

| Variable | Default | Meaning |
|---|---|---|
| `XI_SOURCES` | `` | Names of sources defined via env (`origin,assets`), added to the YAML ones |
| `XI_SOURCES_<NAME>_TYPE` | — | `http` \| `s3` \| `gcs` \| `azure` \| `fs` \| `ftp` \| `sftp` |
| `XI_SOURCES_<NAME>_ALLOW` | — | http: host globs with optional path prefix (`*.example.com`, `cdn.example.com/img/`) |
| `XI_SOURCES_<NAME>_BASE_URL` | — | http: base for relative keys |
| `XI_SOURCES_<NAME>_HEADERS` | — | http: extra request headers (`Authorization=…`) |
| `XI_SOURCES_<NAME>_BUCKETS` / `_BUCKET` | — | s3/gcs/azure: allowed buckets/containers; `_BUCKET` binds a default |
| `XI_SOURCES_<NAME>_PREFIX` | `` | object key prefix jail (every type except http) |
| `XI_SOURCES_<NAME>_ENDPOINT`, `_REGION`, `_FORCE_PATH_STYLE`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_ROLE_ARN` | — | s3 (R2, Spaces, B2, RustFS and other S3-compatible stores via endpoint). No keys → AWS default chain (env, shared files, IRSA, ECS/EC2 role); `_ROLE_ARN` assumes a role on top. `_REGION` falls back to the SDK chain, then `us-east-1` when an endpoint is set (R2: `auto`). Checksums are sent/validated only when required, so R2/B2 work |
| `XI_SOURCES_<NAME>_CREDENTIALS_JSON` | ADC | gcs: credentials document (`XI_SOURCES_<NAME>_CREDENTIALS_JSON_FILE` reads it from a mounted file); `XI_SOURCES_<NAME>_ENDPOINT` points at an emulator such as fake-gcs-server |
| `XI_SOURCES_<NAME>_ACCOUNT`, `_ACCOUNT_KEY`, `_CONNECTION_STRING`, `_SAS_TOKEN`, `_ENDPOINT` | `DefaultAzureCredential` (managed identity) | azure; precedence connection string > account key > SAS > DefaultAzureCredential; `_ENDPOINT` overrides `https://<account>.blob.core.windows.net/` (Azurite: `http://127.0.0.1:10000/devstoreaccount1`) |
| `XI_SOURCES_<NAME>_ROOT` | — | fs root (jail via `os.Root`); ftp/sftp: server directory keys are relative to (default `/`) |
| `XI_SOURCES_<NAME>_FOLLOW_SYMLINKS` | `false` | fs: follow symlinks whose target stays inside the root (links leaving it are always refused) |
| `XI_SOURCES_<NAME>_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_PRIVATE_KEY`, `_PRIVATE_KEY_PASSPHRASE`, `_HOST_KEY`, `_TLS` | — | ftp/sftp. ftp: empty `_USER` logs in as `anonymous`; `_TLS=true` is explicit TLS (`AUTH TLS`, `PROT P`), passive mode only. sftp: `_USER` plus `_PASSWORD` and/or `_PRIVATE_KEY` (PEM/OpenSSH); `_HOST_KEY` is mandatory (authorized_keys or known_hosts lines, several allowed for rotation; no trust-on-first-use) |
| `XI_SOURCES_<NAME>_POOL_SIZE` | `4` | ftp/sftp: max open connections to the server |
| `XI_SOURCES_<NAME>_DIAL_TIMEOUT` | `10s` | ftp/sftp: connect + TLS/SSH handshake + login |
| `XI_SOURCES_<NAME>_IDLE_TIMEOUT` | `1m` | ftp/sftp: pooled connections idle longer are closed |
| `XI_FETCH_TIMEOUT` | `10s` | Per download attempt |
| `XI_FETCH_RETRY` | `true` | One retry, after a jittered ~100 ms backoff, of a transient failure (origin 5xx, connection error, attempt timeout); never for 4xx |
| `XI_FETCH_MAX_REDIRECTS` | `5` | |
| `XI_FETCH_MAX_CONCURRENT` | `256` | Concurrent origin fetches |
| `XI_FETCH_ALLOWED_PORTS` | `80,443` | SSRF port allowlist |
| `XI_FETCH_ALLOW_PRIVATE_NETWORKS` | `false` | Explicit opt-in to every private range |
| `XI_FETCH_ALLOWED_NETWORKS` | `` | CIDRs exempt from the private-address block without opening every range (e.g. `10.20.0.0/16` for an in-cluster object store) |
| `XI_FETCH_USER_AGENT` | `` (`xtandard-image/<version>`) | |

### Dialects and presets

| Variable | Default | Meaning |
|---|---|---|
| `XI_DIALECTS_DEFAULT` | `imgproxy` | Fallback dialect |
| `XI_DIALECTS_STRICT` | `false` | 400 on unknown/unsupported options |
| `XI_DIALECTS_<NAME>_ENABLED` | imgproxy `true`, others `false` | `<NAME>`: `IMGPROXY`, `QUERY`, `WSRV`, `CLOUDFLARE`, `CLOUDFLARE_IMAGES`, `IMGIX`, `CLOUDINARY`, `IMAGEKIT`, `BUNNY`, `THUMBOR`, `AWSSIH`, `SIMPLE` (for `SIMPLE` a list of providers: `nextjs,netlify,ipx`). Extra mounts of one dialect are YAML-only: `dialects.<name>.mounts: [{prefix, hosts, source, keys, unsigned_policy}]` |
| `XI_DIALECTS_<NAME>_PREFIX` | see URL-DIALECTS.md | Mount prefix |
| `XI_DIALECTS_<NAME>_HOSTS` | `` | Host bindings |
| `XI_DIALECTS_<NAME>_SOURCE` | `` | Bound source for relative paths |
| `XI_DIALECTS_<NAME>_KEYS` | `` | Dialect secrets (imgix tokens, thumbor key, cloudinary api secret, imagekit private key, bunny token key, AWS SIH secret used verbatim) |
| `XI_DIALECTS_<NAME>_UNSIGNED_POLICY` | global | Per-dialect override |
| `XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR` | `:` | |
| `XI_DIALECTS_IMGPROXY_BASE_URL` | `` | |
| `XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS` | `` | `regex=replacement;…` (`;`-separated in env) |
| `XI_DIALECTS_CLOUDINARY_CLOUDS` | `` | `cloud=source,…` |
| `XI_DIALECTS_CLOUDFLARE_IMAGES_ACCOUNTS` | `` | `account_hash=source,…`: the source each Cloudflare Images account hash reads image ids from; empty = the mount's source for every hash, otherwise an unknown hash is 403. `XI_DIALECTS_CLOUDFLARE_IMAGES_KEYS` are the raw Images signing keys (`sig`/`exp`); variants are presets |
| `XI_DIALECTS_AWSSIH_BUCKETS` | `` | `bucket=source,…`: SIH `SOURCE_BUCKETS` allowlist; a request without a bucket uses `XI_DIALECTS_AWSSIH_SOURCE` (URL-DIALECTS §13) |
| `XI_DIALECTS_AWSSIH_QUERY_PARAMS` | `true` | SIH query-param mapper (`format`, `fit`, `width`, `height`, `rotate`, `flip`, `flop`, `grayscale`) |
| `XI_DIALECTS_AWSSIH_THUMBOR` | `false` | Forward non-JSON paths to the thumbor dialect (SIH thumbor request type) |
| `XI_DIALECTS_AWSSIH_REWRITE_MATCH_PATTERN` | `` | SIH `REWRITE_MATCH_PATTERN`: `/regex/flags` applied to non-JSON paths before forwarding |
| `XI_DIALECTS_AWSSIH_REWRITE_SUBSTITUTION` | `` | SIH `REWRITE_SUBSTITUTION` (`$1`, `$<name>`, `$&`, `$$`) |
| `XI_DIALECTS_SIMPLE_ENABLED` | `` | unpic providers of the table-driven family, e.g. `nextjs,ipx,contentful` (names in URL-DIALECTS §12; an unknown name fails startup). Each is mounted at its native prefixes and `/simple/<name>/`; no `_PREFIX`/`_KEYS` (xi_sig uses `XI_SIGNING_*`) |
| `XI_DIALECTS_SIMPLE_HOSTS` | `` | Hosts bound to the whole family; the provider is picked per request (native prefix, upstream domain, then the first enabled CDN-style provider) |
| `XI_PRESETS` | `` | `name=opts,name2=opts` (imgproxy syntax); YAML `presets: {name: opts}` |
| `XI_PRESETS_FILE` | `` | One `name=options` per line; blank lines and `#` comments skipped; a name also in `XI_PRESETS` is an error |
| `XI_PRESETS_ONLY` | `false` | Only preset URLs allowed |

### Processing, formats, limits

| Variable | Default | Meaning |
|---|---|---|
| `XI_PROCESSING_WORKERS` | `GOMAXPROCS` | Concurrent processing jobs (`0` = GOMAXPROCS) |
| `XI_PROCESSING_QUEUE_SIZE` | `workers×16` | Waiting jobs; beyond it requests fail fast with 429 (`-1` = workers×16, `0` = no queue) |
| `XI_PROCESSING_QUEUE_TIMEOUT` | `5s` | Max wait for a worker, then 503 |
| `XI_PROCESSING_MEMORY_BUDGET` | `` (off) | Optional weighted admission budget in bytes |
| `XI_PROCESSING_GO_MEMORY_FRACTION` | `0.3` | `GOMEMLIMIT` as fraction of cgroup `memory.max` |
| `XI_PROCESSING_AUTO_ROTATE` | `true` | |
| `XI_PROCESSING_STRIP_METADATA` | `true` | |
| `XI_PROCESSING_KEEP_COPYRIGHT` | `true` | |
| `XI_PROCESSING_STRIP_COLOR_PROFILE` | `true` | |
| `XI_PROCESSING_PRESERVE_HDR` | `false` | |
| `XI_PROCESSING_LINEAR_RESIZE` | `false` | Resize in linear light |
| `XI_PROCESSING_COLOR_PROFILES_DIR` | `` | Custom ICC files |
| `XI_PROCESSING_WATERMARK_SOURCE` | `` | Default watermark image (source string, resolved against the sources). Fetched and decoded once at startup, never refreshed; a watermark that cannot be loaded stops startup |
| `XI_PROCESSING_WATERMARK_OPACITY` | `1` | Multiplies every request's watermark opacity |
| `XI_FALLBACK_SOURCE` | `` | Fallback image (source string) |
| `XI_FALLBACK_STATUS` | `200` | Status used with fallback |
| `XI_FALLBACK_TTL` | `1m` | `Cache-Control: public, max-age` of a fallback response; `0` sends `no-store` |
| `XI_FALLBACK_ALLOW_REQUEST_URL` | `false` | Honour a request's own fallback image (imgproxy `fallback_image_url`); it must still pass the source allowlist, otherwise `XI_FALLBACK_SOURCE` is used |
| `XI_FORMATS_QUALITY` | `80` | Global default |
| `XI_FORMATS_<FMT>_QUALITY` | avif 60, heic 60, jxl 75, webp 80, jpeg 80 | `<FMT>`: `JPEG`, `WEBP`, `AVIF`, `HEIC`, `JXL`; other formats use `XI_FORMATS_QUALITY` |
| `XI_FORMATS_<FMT>_EFFORT` | webp 4, avif 4, jxl 5, gif 7 | `<FMT>`: `WEBP` (0–6), `AVIF` (0–9), `JXL` (1–10), `GIF` (1–10); see FORMATS.md |
| `XI_FORMATS_HEIC_ENCODE` | `false` | HEIC output (HEVC patents; decode is always on) |
| `XI_FORMATS_JPEG_PROGRESSIVE` | `true` | |
| `XI_FORMATS_PNG_QUANTIZE` | `false` | |
| `XI_FORMATS_AUTO_AVIF` / `_WEBP` / `_JXL` | `true` / `true` / `false` | Accept negotiation |
| `XI_FORMATS_ENFORCE_AVIF` / `_WEBP` / `_JXL` | `false` | |
| `XI_FORMATS_STRICT_ACCEPT` | `false` | 406 when nothing acceptable |
| `XI_FORMATS_PREFERRED` | `jpeg,png,gif` | Fallback order for unencodable sources |
| `XI_FORMATS_SVG_PASSTHROUGH` | `true` | |
| `XI_FORMATS_PDF_DPI` | `72` | Resolution of a PDF page's natural size (1–1200; 72 = one pixel per point). Resized pages render directly at the target size |
| `XI_FORMATS_PSD_REQUIRE_COMPOSITE` | `false` | Refuse (422) PSD/PSB files saved without a merged composite ("Maximize compatibility" off) instead of letting ImageMagick flatten the layers |
| `XI_FORMATS_HDR_TONEMAP` | `reinhard` | Operator mapping OpenEXR, Radiance and PFM sources to 8-bit sRGB: `reinhard` (extended Reinhard, white at the 99th-percentile luminance; identity for sources within [0, 1]), `aces` (Narkowicz's fitted ACES filmic curve) or `clip`. Skipped with `preserve_hdr` for JPEG XL (float) and AVIF (12-bit) outputs; FORMATS.md §1.4 |
| `XI_FORMATS_SKIP_PROCESSING` | `` | Source formats streamed unmodified (imgproxy `IMGPROXY_SKIP_PROCESSING_FORMATS`) |
| `XI_CLIENT_HINTS_ENABLED` | `false` | Honour DPR/Width/Viewport-Width/Save-Data hints on every request when the URL leaves the value unset (imgproxy `IMGPROXY_ENABLE_CLIENT_HINTS`); URL opt-ins (`ch=`, `w_auto`, `width=auto`) work regardless. Image responses then carry `Accept-CH: Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width, ECT` and a `Vary` listing only the hint names that decided the result. Unsigned requests on a `restricted` mount get only Save-Data (a hinted width/DPR would bypass the allowlists). Semantics and imgproxy parity: URL-DIALECTS.md §3.5 |
| `XI_CLIENT_HINTS_CRITICAL_CH` | `false` | Also send `Critical-CH: Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width` (browsers honour it on navigations only) |
| `XI_FORMATS_AVIF_MAX_PIXELS` | `16000000` | |
| `XI_FORMATS_BEST_MAX_PIXELS` | `8000000` | `format=best` (imgproxy `f:best`, query `f=best`) is skipped above this many output pixels (all frames): the negotiated format is encoded once instead (FORMATS.md §6.3) |
| `XI_FORMATS_BEST_COMPLEXITY_THRESHOLD` | `5.5` | Content score below which `best` tries lossless formats only, above which lossy only; within ±0.75 of it both. `0` skips the analysis (every image is tried as a photo). Photos score ≈ 7–8, flat graphics and text ≈ 1–4.5 (FORMATS.md §6.3) |
| `XI_FORMATS_RAW_PREVIEW_MIN_RATIO` | `1` | Camera RAW: decode the embedded JPEG preview instead of demosaicing when it is at least this many times the size the image is scaled to, on both axes (FORMATS.md §2); `0 < ratio ≤ 100` |
| `XI_FORMATS_SAVE_DATA_QUALITY_DELTA` | `-15` | |
| `XI_FORMATS_AUTOQUALITY_METHOD` | `none` | `none` \| `size` \| `dssim` (imgproxy `IMGPROXY_AUTOQUALITY_METHOD`; `ml` is not implemented). A request's `autoquality` option overrides it; with the method from config, an explicit request quality wins. See FORMATS.md §6.4 |
| `XI_FORMATS_AUTOQUALITY_TARGET` / `_MIN` / `_MAX` / `_ALLOWED_ERROR` | `0.02` / `60` / `85` / `0.001` | Target: DSSIM (`dssim`) or bytes (`size`, must then be ≥ 1); quality range searched; how far from the DSSIM target a save may land and stop the search (0–1) |
| `XI_FORMATS_AUTOQUALITY_FORMAT_MIN` | `jpeg=40,webp=40,avif=30` | Per-format minimum quality (imgproxy `IMGPROXY_AUTOQUALITY_FORMAT_MIN`); formats not listed use `_MIN`. Setting the variable replaces the whole default map |
| `XI_FORMATS_AUTOQUALITY_FORMAT_MAX` | `jpeg=90,webp=90,avif=80` | Per-format maximum quality (`IMGPROXY_AUTOQUALITY_FORMAT_MAX`); as above |
| `XI_FORMATS_AUTOQUALITY_FORMAT_TARGET` | `` | Per-format target, e.g. `avif=0.015`; formats not listed use `_TARGET` |
| `XI_LIMITS_MAX_SRC_BYTES` | `50MiB` | |
| `XI_LIMITS_MAX_SRC_PIXELS` | `50000000` | |
| `XI_LIMITS_MAX_RESULT_DIMENSION` | `8192` | |
| `XI_LIMITS_MAX_ANIMATION_FRAMES` | `256` | |
| `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` | `16000000` | |
| `XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS` | `60000000` | Sensor width × height a camera RAW may be demosaiced at; the embedded-preview path is not affected |
| `XI_LIMITS_MAX_SVG_BYTES` | `5MiB` | |
| `XI_LIMITS_MAX_SVG_DEPTH` | `256` | SVG sanitiser nesting limit |
| `XI_LIMITS_MAX_SVG_ELEMENTS` | `200000` | SVG sanitiser element limit |
| `XI_LIMITS_MAX_PDF_PAGES` | `500` | Pages a PDF may have; checked when the header is read, before any page renders (422) |
| `XI_VIPS_CONCURRENCY` | `1` | libvips threads per image; change only with benchmarks |
| `XI_VIPS_FAIL_ON` | `warning` | |

### Cache and observability

| Variable | Default | Meaning |
|---|---|---|
| `XI_CACHE_HEADERS_TTL` | `31536000s` | `max-age` for successes |
| `XI_CACHE_HEADERS_ERROR_TTL` | `10s` | For upstream failures |
| `XI_CACHE_HEADERS_PASSTHROUGH` | `false` | Use source Cache-Control/Expires |
| `XI_CACHE_ETAG_BUSTER` | `` | Global ETag salt |
| `XI_CACHE_TAGS_HEADER` | `Cache-Tag` | For `cache_tags` |
| `XI_CACHE_ALLOW_BYPASS` | `false` | Honour `bypass_cache` (the request skips the result cache: no lookup, no store) |
| `XI_CACHE_RESULT_STORES` | `` | Ordered tiers, fastest first, e.g. `memory,disk` or `memory,redis,s3`; empty disables the result cache. A hit in a slower tier is copied into the faster ones (ARCHITECTURE §9.2) |
| `XI_CACHE_RESULT_MEMORY_BYTES` | `256MiB` | In-memory LRU tier bound (bodies, stored headers and keys) |
| `XI_CACHE_RESULT_TTL` | `24h` | How long a result is served from the cache without contacting the source (the key holds the source identity, not its validators); a per-request `ttl`/`expires` shortens it. Use a `cachebuster` or `XI_CACHE_ETAG_BUSTER` to invalidate earlier |
| `XI_CACHE_RESULT_WRITE_QUEUE` | `64` | Asynchronous cache writes in flight; more are dropped and counted in `xi_cache_writes_dropped_total`, never blocking the response |
| `XI_CACHE_RESULT_DISK_PATH` / `_BYTES` | `` / `10GiB` | Absolute directory of the `disk` store and its size bound (entry files; a janitor evicts least recently used entries down to 90 % once a write goes over). The image's root file system is read-only: mount a volume there. The directory must be dedicated to one process; the index is rebuilt from it at start, so entries survive restarts. No fsync: a file torn by a crash fails its checksum and is dropped |
| `XI_CACHE_RESULT_REDIS_URL` | `` | Secret (may carry a password). `redis://[user:pass@]host:port/db`, `rediss://` for TLS, go-redis query options (`dial_timeout`, `pool_size`, …). Any RESP server: Redis, Valkey, KeyDB, managed services |
| `XI_CACHE_RESULT_REDIS_PREFIX` | `xi:` | Prepended to every key, so deployments can share a server |
| `XI_CACHE_RESULT_REDIS_MAX_VALUE_BYTES` | `1MiB` | Larger entries are not stored in Redis (counted in `xi_cache_skipped_total{reason="too_large"}`) |
| `XI_CACHE_RESULT_S3_SOURCE`, `_BUCKET`, `_PREFIX` | `` | `SOURCE` names an s3 source whose endpoint, region, path style and credentials the `s3` store reuses; `BUCKET` (required, must exist) and `PREFIX` (prepended verbatim, e.g. `cache/`) place the objects. Expiry is checked from object metadata on read, so no lifecycle rule is needed for correctness; add one on the prefix to reclaim space |
| `XI_CACHE_ORIGIN_MEMORY_BYTES` | `256MiB` | Original-bytes LRU |
| `XI_CACHE_ORIGIN_TTL` | `60s` | After it, entries with an ETag/Last-Modified are revalidated with a conditional request (304 refreshes the TTL) |
| `XI_CACHE_ORIGIN_NEGATIVE_TTL` | `5s` | How long a source not-found is remembered; `0` disables |
| `XI_CACHE_ORIGIN_STORES` | `` | Shared stores behind the origin LRU (`disk`, `redis`, `s3`, fastest first; configured by the `XI_CACHE_RESULT_*` variables of the same store, which the result cache shares under another namespace): a replica that misses its LRU reads the original from them before downloading, and downloads are written to them. Empty: originals stay in-process |
| `XI_CACHE_ORIGIN_STORE_TTL` | `10m` | How long an original is served from `XI_CACHE_ORIGIN_STORES` without contacting the source (no revalidation) |
| `XI_OBSERVABILITY_LOG_LEVEL` | `info` | |
| `XI_ADMIN_ADDR` | `127.0.0.1:9090` | Admin listener: `/metrics`, `/openapi.json`, pprof; set `:9090` for in-cluster scraping; empty disables |
| `XI_ADMIN_PPROF` | `false` | pprof incl. `goroutineleak` on the admin listener |
| `XI_ADMIN_DOCS` | `false` | Scalar API reference at admin `/docs`, loaded by the browser from a pinned `cdn.jsdelivr.net` URL (SRI, strict CSP); off answers 404 and points at `/openapi.json` ([OPENAPI.md](OPENAPI.md)) |
| `XI_OBSERVABILITY_DEBUG_HEADERS` | `false` | `X-Xi-Ignored`, `X-Xi-Origin-Content-Length`, `X-Xi-Origin-Width/-Height`, `X-Xi-Result-Width/-Height` (ARCHITECTURE §9.1) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` etc. | — | Standard OpenTelemetry vars; tracing on when set |
| `XI_OTEL_ENABLED` | `false` | Force tracing on without an endpoint variable (exporter still chosen by `OTEL_TRACES_EXPORTER`, e.g. `console`) |

### Compatibility

| Variable | Default | Meaning |
|---|---|---|
| `XI_COMPAT_IMGPROXY_STATUS` | `false` | Invalid URL and disallowed source answer 404 like imgproxy (instead of 400 / 403) |
| `XI_COMPAT_IMGPROXY_ENV` | `false` | Translate `IMGPROXY_*` variables (§1, table below); YAML `compat.imgproxy_env` |

### imgproxy variables

With `XI_COMPAT_IMGPROXY_ENV=true` (or YAML `compat.imgproxy_env: true`) every set `IMGPROXY_*` variable below is translated into its `XI_*` equivalent before the `XI_*` variables are read, so precedence is file < `IMGPROXY_*` < `XI_*`: an `XI_*` variable (or its `_FILE`) always wins, except `XI_SOURCES`, whose names are merged with the sources created here. Rules:

- Only variables that are set are translated. imgproxy's own defaults are **not** imported; notably imgproxy allows private source addresses and any http(s) source by default, xtandard-image does neither unless told.
- Named sources created: `http` (from `IMGPROXY_ALLOWED_SOURCES`), `s3`, `gcs`, `abs` (azure) and `local` (fs). Refer to them with `XI_SOURCES_<NAME>_*` to adjust them; `XI_SOURCES_S3_ACCESS_KEY_ID` etc. are the place for credentials that imgproxy read from `AWS_*`.
- Invalid values fail startup and name the `IMGPROXY_*` variable (`XI_PROCESSING_WORKERS (from IMGPROXY_WORKERS): …`). Translations that cannot be expressed faithfully and would loosen security (bucket deny lists, `*` inside an allowed-source path, key prefixes inside a bucket) are errors; ones that only drop a nicety are startup warnings.
- Any other set `IMGPROXY_*` variable is not an error: `Meta.UnmappedImgproxyEnv` lists them for one startup log line. Examples with no equivalent: `IMGPROXY_LOG_FORMAT` (always JSON), `IMGPROXY_DEVELOPMENT_ERRORS_MODE` (`XI_DEV_MODE` also opens every http source, so it is not implied), `IMGPROXY_HEALTH_CHECK_PATH` (`/healthz` is fixed), `IMGPROXY_SET_CANONICAL_HEADER`, `IMGPROXY_USE_ETAG`/`_USE_LAST_MODIFIED` and `IMGPROXY_SANITIZE_SVG` (no switch), `IMGPROXY_WATERMARK_DATA`/`IMGPROXY_FALLBACK_IMAGE_DATA` (use a source string), `IMGPROXY_PROMETHEUS_NAMESPACE`, `IMGPROXY_OPEN_TELEMETRY_*` other than `_ENABLE` (use `OTEL_*`), `IMGPROXY_USE_SWIFT` (use its S3 middleware), `IMGPROXY_MAX_SVG_CHECK_BYTES`, `IMGPROXY_RETURN_ATTACHMENT`, error reporters and `IMGPROXY_ENV_*` secret loaders.

The table is checked against the code by a test in `internal/config`.

| imgproxy variable | xtandard-image variable | Notes |
|---|---|---|
| `IMGPROXY_KEY` | `XI_SIGNING_KEYS` | Same hex list |
| `IMGPROXY_SALT` | `XI_SIGNING_SALTS` | Same hex list |
| `IMGPROXY_SIGNATURE_SIZE` | `XI_SIGNING_SIZE` | Same |
| `IMGPROXY_TRUSTED_SIGNATURES` | `XI_SIGNING_TRUSTED` | Same |
| `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY` | `XI_ENCRYPTION_KEYS` | imgproxy Pro; the hex key becomes the only encryption key |
| `IMGPROXY_SECRET` | `XI_SECURITY_SECRET` | Same `Authorization: Bearer` check |
| `IMGPROXY_ALLOWED_PROCESSING_OPTIONS` | `XI_SECURITY_ALLOWED_PROCESSING_OPTIONS` | Same |
| `IMGPROXY_ALLOW_SECURITY_OPTIONS` | `XI_SECURITY_ALLOW_SECURITY_OPTIONS` | Same |
| `IMGPROXY_ALLOWED_SOURCES` | `XI_SOURCES`, `XI_SOURCES_HTTP_TYPE`, `XI_SOURCES_HTTP_ALLOW`, `XI_SOURCES_LOCAL_PREFIX` | `http(s)://host[:port][/path/]` patterns become the allow list of an http source named `http` (scheme dropped, `*` in the host kept as a host glob, the path kept as a directory prefix, `*` in the path rejected). `s3://`, `gs://`, `abs://` patterns restrict the buckets of the matching source (bucket names only). `local:///dir/` becomes the `local` source prefix. Unset: no http source is created (imgproxy would fetch any URL), a startup warning says so |
| `IMGPROXY_USE_S3` | `XI_SOURCES`, `XI_SOURCES_S3_TYPE`, `XI_SOURCES_S3_BUCKETS` | Creates an s3 source named `s3`. xtandard-image needs a bucket allowlist: `IMGPROXY_S3_ALLOWED_BUCKETS` or `s3://` patterns in `IMGPROXY_ALLOWED_SOURCES`. Credentials come from the standard `AWS_*` chain as in imgproxy |
| `IMGPROXY_S3_ALLOWED_BUCKETS` | `XI_SOURCES_S3_BUCKETS` | Intersected (exact names) with `s3://` patterns of `IMGPROXY_ALLOWED_SOURCES` when both are set |
| `IMGPROXY_S3_DENIED_BUCKETS` | — | Rejected at startup: there is no deny list; list the allowed buckets instead |
| `IMGPROXY_S3_REGION` | `XI_SOURCES_S3_REGION` | Same |
| `IMGPROXY_S3_ENDPOINT_USE_PATH_STYLE` | `XI_SOURCES_S3_FORCE_PATH_STYLE` | Same |
| `IMGPROXY_S3_ENDPOINT` | `XI_SOURCES_S3_ENDPOINT`, `XI_SOURCES_S3_FORCE_PATH_STYLE` | Same; path-style addressing is turned on unless `IMGPROXY_S3_ENDPOINT_USE_PATH_STYLE` says otherwise (imgproxy's default) |
| `IMGPROXY_S3_ASSUME_ROLE_ARN` | `XI_SOURCES_S3_ROLE_ARN` | Same |
| `IMGPROXY_USE_GCS` | `XI_SOURCES`, `XI_SOURCES_GCS_TYPE`, `XI_SOURCES_GCS_BUCKETS` | Creates a gcs source named `gcs`; bucket allowlist as for `IMGPROXY_USE_S3` |
| `IMGPROXY_GCS_ALLOWED_BUCKETS` | `XI_SOURCES_GCS_BUCKETS` | As `IMGPROXY_S3_ALLOWED_BUCKETS` |
| `IMGPROXY_GCS_DENIED_BUCKETS` | — | Rejected at startup, as `IMGPROXY_S3_DENIED_BUCKETS` |
| `IMGPROXY_GCS_KEY` | `XI_SOURCES_GCS_CREDENTIALS_JSON` | Same service-account JSON |
| `IMGPROXY_GCS_ENDPOINT` | `XI_SOURCES_GCS_ENDPOINT` | Same |
| `IMGPROXY_USE_ABS` | `XI_SOURCES`, `XI_SOURCES_ABS_TYPE`, `XI_SOURCES_ABS_BUCKETS` | Creates an azure source named `abs`; container allowlist as for `IMGPROXY_USE_S3` |
| `IMGPROXY_ABS_ALLOWED_BUCKETS` | `XI_SOURCES_ABS_BUCKETS` | As `IMGPROXY_S3_ALLOWED_BUCKETS` |
| `IMGPROXY_ABS_DENIED_BUCKETS` | — | Rejected at startup, as `IMGPROXY_S3_DENIED_BUCKETS` |
| `IMGPROXY_ABS_NAME` | `XI_SOURCES_ABS_ACCOUNT` | Storage account name |
| `IMGPROXY_ABS_KEY` | `XI_SOURCES_ABS_ACCOUNT_KEY` | Same |
| `IMGPROXY_ABS_ENDPOINT` | `XI_SOURCES_ABS_ENDPOINT` | Same service URL |
| `IMGPROXY_LOCAL_FILESYSTEM_ROOT` | `XI_SOURCES`, `XI_SOURCES_LOCAL_TYPE`, `XI_SOURCES_LOCAL_ROOT`, `XI_SOURCES_LOCAL_PREFIX` | Creates an fs source named `local` (jailed with `os.Root`); its prefix comes from `local://` patterns of `IMGPROXY_ALLOWED_SOURCES` |
| `IMGPROXY_ALLOW_LOOPBACK_SOURCE_ADDRESSES` | `XI_FETCH_ALLOWED_NETWORKS` | `true` adds `127.0.0.0/8`, `::1/128` |
| `IMGPROXY_ALLOW_LINK_LOCAL_SOURCE_ADDRESSES` | `XI_FETCH_ALLOWED_NETWORKS` | `true` adds `169.254.0.0/16`, `fe80::/10` |
| `IMGPROXY_ALLOW_PRIVATE_SOURCE_ADDRESSES` | `XI_FETCH_ALLOWED_NETWORKS` | `true` adds RFC 1918, CGNAT `100.64.0.0/10` and `fc00::/7`. imgproxy defaults to `true`; xtandard-image blocks private addresses unless this is set |
| `IMGPROXY_DOWNLOAD_TIMEOUT` | `XI_FETCH_TIMEOUT` | Integer seconds → duration |
| `IMGPROXY_MAX_REDIRECTS` | `XI_FETCH_MAX_REDIRECTS` | Same |
| `IMGPROXY_USER_AGENT` | `XI_FETCH_USER_AGENT` | Same; `%current_version` is not expanded |
| `IMGPROXY_BIND` | `XI_SERVER_ADDR` | Same `host:port` (unix sockets are not supported) |
| `IMGPROXY_PATH_PREFIX` | `XI_SERVER_PATH_PREFIX` | Same |
| `IMGPROXY_TIMEOUT` | `XI_SERVER_REQUEST_TIMEOUT` | Integer seconds → duration |
| `IMGPROXY_READ_REQUEST_TIMEOUT` | `XI_SERVER_READ_TIMEOUT` | Integer seconds → duration |
| `IMGPROXY_WRITE_RESPONSE_TIMEOUT` | `XI_SERVER_WRITE_TIMEOUT` | Integer seconds → duration; must be ≥ the request timeout |
| `IMGPROXY_KEEP_ALIVE_TIMEOUT` | `XI_SERVER_IDLE_TIMEOUT` | Integer seconds → duration; `0` (keep-alive off) is rejected |
| `IMGPROXY_GRACEFUL_STOP_TIMEOUT` | `XI_SERVER_SHUTDOWN_TIMEOUT` | Integer seconds → duration |
| `IMGPROXY_MAX_CLIENTS` | `XI_SERVER_MAX_CONNECTIONS` | `0` (unlimited) keeps the xtandard-image default |
| `IMGPROXY_ALLOW_ORIGIN` | `XI_SERVER_CORS_ORIGINS` | Same single origin |
| `IMGPROXY_WORKERS` | `XI_PROCESSING_WORKERS` | Same |
| `IMGPROXY_REQUESTS_QUEUE_SIZE` | `XI_PROCESSING_QUEUE_SIZE` | `0` (unlimited in imgproxy) keeps the default of workers×16; the queue is always bounded |
| `IMGPROXY_MAX_SRC_RESOLUTION` | `XI_LIMITS_MAX_SRC_PIXELS` | Megapixels (float) → pixels |
| `IMGPROXY_MAX_SRC_FILE_SIZE` | `XI_LIMITS_MAX_SRC_BYTES` | Bytes; `0` (unlimited) keeps the default |
| `IMGPROXY_MAX_ANIMATION_FRAMES` | `XI_LIMITS_MAX_ANIMATION_FRAMES` | Same |
| `IMGPROXY_MAX_ANIMATION_FRAME_RESOLUTION` | `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` | Megapixels (float) → pixels; `0` (off) keeps the default |
| `IMGPROXY_MAX_RESULT_DIMENSION` | `XI_LIMITS_MAX_RESULT_DIMENSION` | `0` (unlimited) keeps the default |
| `IMGPROXY_TTL` | `XI_CACHE_HEADERS_TTL` | Integer seconds → duration |
| `IMGPROXY_CACHE_CONTROL_PASSTHROUGH` | `XI_CACHE_HEADERS_PASSTHROUGH` | Same |
| `IMGPROXY_ETAG_BUSTER` | `XI_CACHE_ETAG_BUSTER` | Same |
| `IMGPROXY_ARGUMENTS_SEPARATOR` | `XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR` | Same |
| `IMGPROXY_BASE_URL` | `XI_DIALECTS_IMGPROXY_BASE_URL` | Same |
| `IMGPROXY_URL_REPLACEMENTS` | `XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS` | Wildcard patterns are converted to the equivalent anchored regexes |
| `IMGPROXY_PRESETS_SEPARATOR` | `XI_PRESETS` | Only used to split `IMGPROXY_PRESETS` |
| `IMGPROXY_PRESETS` | `XI_PRESETS` | Same `name=options` list, split by `IMGPROXY_PRESETS_SEPARATOR` (default `,`) |
| `IMGPROXY_PRESETS_PATH` | `XI_PRESETS_FILE` | Same one-preset-per-line file |
| `IMGPROXY_ONLY_PRESETS` | `XI_PRESETS_ONLY` | Same |
| `IMGPROXY_AUTO_ROTATE` | `XI_PROCESSING_AUTO_ROTATE` | Same |
| `IMGPROXY_STRIP_METADATA` | `XI_PROCESSING_STRIP_METADATA` | Same |
| `IMGPROXY_KEEP_COPYRIGHT` | `XI_PROCESSING_KEEP_COPYRIGHT` | Same |
| `IMGPROXY_STRIP_COLOR_PROFILE` | `XI_PROCESSING_STRIP_COLOR_PROFILE` | Same |
| `IMGPROXY_PRESERVE_HDR` | `XI_PROCESSING_PRESERVE_HDR` | Same |
| `IMGPROXY_USE_LINEAR_COLORSPACE` | `XI_PROCESSING_LINEAR_RESIZE` | Same |
| `IMGPROXY_WATERMARK_OPACITY` | `XI_PROCESSING_WATERMARK_OPACITY` | Same |
| `IMGPROXY_WATERMARK_PATH` | `XI_PROCESSING_WATERMARK_SOURCE` | Translated to `local:///<path>` when the path lies under `IMGPROXY_LOCAL_FILESYSTEM_ROOT`; otherwise a startup warning (set a source string yourself). Wins over `IMGPROXY_WATERMARK_URL` |
| `IMGPROXY_WATERMARK_URL` | `XI_PROCESSING_WATERMARK_SOURCE` | Same URL, resolved against the sources like any source string |
| `IMGPROXY_FALLBACK_IMAGE_PATH` | `XI_FALLBACK_SOURCE` | As `IMGPROXY_WATERMARK_PATH` |
| `IMGPROXY_FALLBACK_IMAGE_URL` | `XI_FALLBACK_SOURCE` | As `IMGPROXY_WATERMARK_URL` |
| `IMGPROXY_FALLBACK_IMAGE_HTTP_CODE` | `XI_FALLBACK_STATUS` | `0` (keep the error status) keeps the default |
| `IMGPROXY_FORMAT_QUALITY` | `XI_FORMATS_JPEG_QUALITY`, `XI_FORMATS_WEBP_QUALITY`, `XI_FORMATS_AVIF_QUALITY`, `XI_FORMATS_HEIC_QUALITY`, `XI_FORMATS_JXL_QUALITY` | `fmt=q` pairs → per-format quality; formats without a per-format setting are ignored with a warning |
| `IMGPROXY_QUALITY` | `XI_FORMATS_QUALITY`, `XI_FORMATS_JPEG_QUALITY`, `XI_FORMATS_HEIC_QUALITY` | Also sets jpeg and heic quality, which imgproxy's default `IMGPROXY_FORMAT_QUALITY` leaves on the global value |
| `IMGPROXY_JPEG_PROGRESSIVE` | `XI_FORMATS_JPEG_PROGRESSIVE` | Same (xtandard-image defaults to `true`, imgproxy to `false`) |
| `IMGPROXY_PNG_QUANTIZE` | `XI_FORMATS_PNG_QUANTIZE` | Same |
| `IMGPROXY_WEBP_EFFORT` | `XI_FORMATS_WEBP_EFFORT` | Same |
| `IMGPROXY_AVIF_SPEED` | `XI_FORMATS_AVIF_EFFORT` | Speed 0–9 → effort `9 − speed` |
| `IMGPROXY_JXL_EFFORT` | `XI_FORMATS_JXL_EFFORT` | Same |
| `IMGPROXY_AUTO_WEBP` | `XI_FORMATS_AUTO_WEBP` | Same |
| `IMGPROXY_AUTO_AVIF` | `XI_FORMATS_AUTO_AVIF` | Same |
| `IMGPROXY_AUTO_JXL` | `XI_FORMATS_AUTO_JXL` | Same |
| `IMGPROXY_ENFORCE_WEBP` | `XI_FORMATS_ENFORCE_WEBP` | Same |
| `IMGPROXY_ENFORCE_AVIF` | `XI_FORMATS_ENFORCE_AVIF` | Same |
| `IMGPROXY_ENFORCE_JXL` | `XI_FORMATS_ENFORCE_JXL` | Same |
| `IMGPROXY_PREFERRED_FORMATS` | `XI_FORMATS_PREFERRED` | Same format names |
| `IMGPROXY_SKIP_PROCESSING_FORMATS` | `XI_FORMATS_SKIP_PROCESSING` | Same format names |
| `IMGPROXY_BEST_FORMAT_MAX_RESOLUTION` | `XI_FORMATS_BEST_MAX_PIXELS` | Megapixels (float) → pixels; `0` (no cap in imgproxy) keeps the default |
| `IMGPROXY_BEST_FORMAT_COMPLEXITY_THRESHOLD` | `XI_FORMATS_BEST_COMPLEXITY_THRESHOLD` | Same meaning (below: lossless; `0` skips the analysis), but the score is our own (FORMATS.md §6.3), so tune it on your images |
| `IMGPROXY_ALWAYS_RASTERIZE_SVG` | `XI_FORMATS_SVG_PASSTHROUGH` | Inverted |
| `IMGPROXY_ENABLE_CLIENT_HINTS` | `XI_CLIENT_HINTS_ENABLED` | Same |
| `IMGPROXY_LOG_LEVEL` | `XI_OBSERVABILITY_LOG_LEVEL` | Same levels |
| `IMGPROXY_ENABLE_DEBUG_HEADERS` | `XI_OBSERVABILITY_DEBUG_HEADERS` | Same switch; the header names differ (`X-Xi-*`) |
| `IMGPROXY_PROMETHEUS_BIND` | `XI_ADMIN_ADDR` | Metrics are served on the admin listener |
| `IMGPROXY_OPEN_TELEMETRY_ENABLE` | `XI_OTEL_ENABLED` | Same; configure the exporter with the standard `OTEL_*` variables |

In the tables, `X` / `_Y` and `X`, `_Y` are shorthand: `_Y` replaces the last `_`-segment of `X` (`XI_FORMATS_AUTO_AVIF` / `_WEBP` → `XI_FORMATS_AUTO_WEBP`).

## 3. YAML example

```yaml
server:
  addr: ":8080"
signing:
  keys: ["943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"]
  salts: ["520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"]
encryption:
  keys: ["1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"]
security:
  unsigned_policy: restricted
sources:
  origin:
    type: http
    allow: ["cdn.example.com", "*.example.org/images/"]
  assets:
    type: s3
    endpoint: https://ACCOUNT_ID.r2.cloudflarestorage.com
    region: auto
    buckets: [assets]
    bucket: assets
    # credentials via XI_SOURCES_ASSETS_ACCESS_KEY_ID_FILE / _SECRET_ACCESS_KEY_FILE
  local:
    type: fs
    root: /data/images
dialects:
  imgix:
    enabled: true
    hosts: [images.example.com]
    source: assets
    keys: ["imgix-token"]
  cloudflare:
    enabled: true
    source: origin
  simple:
    enabled: [nextjs, netlify, ipx]
    source: origin
presets:
  default: "strip_metadata:1/quality:80"
  thumb: "rs:fill:200:200/g:sm"
limits:
  max_src_pixels: 80000000
cache:
  result:
    stores: [memory, s3]
    memory_bytes: 512MiB
    s3: { source: assets, bucket: xi-cache, prefix: results/ }
observability:
  log_level: info
```

The file is decoded with `goccy/go-yaml` in strict mode (unknown fields and duplicate keys rejected, one document only); environment variables are then applied on top with `caarlos0/env/v11`. This example is loaded by a test, so it stays valid. The same example in JSON and TOML is `internal/config/testdata/schema/valid/example.{json,toml}`; a test loads all three to the same `Config` and validates them against the schema. No interpolation syntax: put secrets in env or `_FILE` variables or `{file: …}` references, and keep the file itself non-secret so it can live in a ConfigMap.

## 4. Dokploy

Create an **Application** → Docker image `ghcr.io/xantiagoma/xtandard-image:slim`, port 8080, domain via Traefik, and set environment:

```
XI_SIGNING_KEYS=<hex>
XI_SIGNING_SALTS=<hex>
XI_SOURCES=origin
XI_SOURCES_ORIGIN_TYPE=http
XI_SOURCES_ORIGIN_ALLOW=cdn.example.com
XI_PROCESSING_MEMORY_BUDGET=1GiB
```

Health check path `/healthz` (the image also declares a Docker `HEALTHCHECK`). Scale replicas freely: the service is stateless; put a CDN in front for caching.

## 5. Coolify (docker-compose)

```yaml
services:
  xtandard-image:
    image: ghcr.io/xantiagoma/xtandard-image:slim
    restart: unless-stopped
    environment:
      - SERVICE_FQDN_XI_8080
      - XI_SIGNING_KEYS=${XI_SIGNING_KEYS}
      - XI_SIGNING_SALTS=${XI_SIGNING_SALTS}
      - XI_SOURCES=origin
      - XI_SOURCES_ORIGIN_TYPE=http
      - XI_SOURCES_ORIGIN_ALLOW=cdn.example.com
      - XI_CACHE_RESULT_STORES=memory,disk
      - XI_CACHE_RESULT_DISK_PATH=/cache
    volumes:
      - xi-cache:/cache
    read_only: true
    tmpfs: [/tmp]
    deploy:
      resources:
        limits: { cpus: "2", memory: 2g }
    healthcheck:
      test: ["CMD", "/usr/local/bin/xtandard-image", "health"]
      interval: 15s
volumes:
  xi-cache:
```

## 6. Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: xtandard-image }
spec:
  replicas: 2
  selector: { matchLabels: { app: xtandard-image } }
  template:
    metadata:
      labels: { app: xtandard-image }
      annotations: { prometheus.io/scrape: "true", prometheus.io/port: "9090" }
    spec:
      securityContext: { runAsNonRoot: true, runAsUser: 65532, fsGroup: 65532, seccompProfile: { type: RuntimeDefault } }
      terminationGracePeriodSeconds: 45
      containers:
        - name: xi
          image: ghcr.io/xantiagoma/xtandard-image:0.1.0-slim
          ports: [{ containerPort: 8080, name: http }, { containerPort: 9090, name: metrics }]
          envFrom: [{ secretRef: { name: xi-signing } }]
          env:
            - { name: XI_CONFIG_FILE, value: /etc/xtandard/config.yaml }
            - { name: XI_ADMIN_ADDR, value: ":9090" }
          resources:
            requests: { cpu: "1", memory: 1Gi }
            limits: { cpu: "2", memory: 2Gi }
          readinessProbe: { httpGet: { path: /readyz, port: http }, periodSeconds: 5 }
          livenessProbe: { httpGet: { path: /healthz, port: http }, periodSeconds: 10 }
          securityContext: { readOnlyRootFilesystem: true, allowPrivilegeEscalation: false, capabilities: { drop: [ALL] } }
          volumeMounts:
            - { name: config, mountPath: /etc/xtandard }
            - { name: tmp, mountPath: /tmp }
      volumes:
        - { name: config, configMap: { name: xi-config } }
        - { name: tmp, emptyDir: { medium: Memory, sizeLimit: 64Mi } }
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: { name: xtandard-image }
spec:
  scaleTargetRef: { apiVersion: apps/v1, kind: Deployment, name: xtandard-image }
  minReplicas: 2
  maxReplicas: 20
  metrics:
    - type: Resource
      resource: { name: cpu, target: { type: Utilization, averageUtilization: 65 } }
    # Better signal when a Prometheus adapter is installed:
    # - type: Pods
    #   pods: { metric: { name: xi_queue_depth }, target: { type: AverageValue, averageValue: "4" } }
```

Set CPU and memory limits: the worker count follows the CPU limit (GOMAXPROCS) and `GOMEMLIMIT` is derived from the memory limit.
