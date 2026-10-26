# Configuration

Every way to configure xtandard-image, and every setting. A setting is renamed, removed or given a new default only in a minor release, and the [changelog](../CHANGELOG.md) says what to change ([versioning](RELEASE.md#versioning)).

## Configuration methods

There are three ways to configure xtandard-image, and they combine field by field:

| Method | How | Good for |
|---|---|---|
| Environment variables | `XI_*`, one per field (`XI_LIMITS_MAX_SRC_BYTES=20MiB`); sources with `XI_SOURCES=a,b` plus `XI_SOURCES_<NAME>_*` | Containers and PaaS (Dokploy, Coolify): no file to mount |
| Config file | YAML, JSON or TOML with the same keys: `--config <path>`, `XI_CONFIG_FILE`, or `/etc/xtandard/config.{yaml,yml,json,toml}` | Many sources and dialect mounts; a file kept in git or a ConfigMap, checked by editors against the [JSON Schema](#config-file-formats-and-schema-adr-0007) |
| Secret files | `XI_…_FILE=/run/secrets/x` for any variable; `{file: /run/secrets/x}` for any secret value in the config file | Docker secrets and Kubernetes Secret volumes: the credential never sits in the environment or the file |

- **Precedence**, lowest to highest: built-in defaults, the config file, `IMGPROXY_*` (only with `XI_COMPAT_IMGPROXY_ENV=true`), `XI_*` (a variable and its `_FILE` form count as one). An environment variable replaces one field of the file, so the file can hold the structure and the environment the per-deployment values. Lists and maps replace rather than merge; the source names of `XI_SOURCES` are the exception, added to the file's `sources`. Details in [§1](#1-sources-of-configuration-and-precedence).
- **Strict**: an unknown key or `XI_*` variable stops startup with a "did you mean" hint. `XI_ALLOW_UNKNOWN_ENV=true` downgrades unknown variables to a startup warning, for an environment shared by several versions (one of them may not know a newer variable); unknown keys in the file stay errors.
- **Check before deploying**: `xtandard-image config validate [--config file] [--no-env]` loads exactly as the server does and lists every violation with its key path and variable (exit 1); `config print` shows the effective configuration with secrets redacted; `config schema` prints the JSON Schema. Examples: [SOURCES.md §7](SOURCES.md#7-troubleshooting).
- **Sources** (the allowlist of origins, several of one type included) have their own guide: [SOURCES.md](SOURCES.md) explains how a URL picks a source, gives provider recipes (AWS S3, R2, B2, Wasabi, Spaces, RustFS/MinIO, GCS, Azure, fs, FTP/SFTP, http) and complete env, YAML, TOML, Compose, Dokploy/Coolify and Kubernetes examples ([`docs/examples/`](examples/)).

Every variable of the §2 tables, each `XI_SOURCES_<NAME>_*` key included, is checked against the code in both directions by a test in `internal/config` (names and defaults), and every example in [`docs/examples/`](examples/) is loaded by one.

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

The file is YAML, JSON or TOML, chosen by extension (`.yaml`/`.yml`, `.json`, `.toml`; anything else is a startup error). All three have the same keys and the same strictness: JSON goes through the same strict YAML decoder, and TOML is converted to the same tree first (TOML dates and times are rejected because no key takes one, and errors after the conversion name the dotted key path rather than a TOML line). A top-level `$schema` key is ignored in every format. Precedence and the env mapping above do not change. A JSON Schema (draft 2020-12) of the file gives editors validation, autocomplete and hover docs; each hover shows the CONFIG.md meaning, the matching `XI_*` variable and, for secrets, its `_FILE` form. The schema is generated from the `Config` struct and the tables below (`go test ./internal/config -run TestJSONSchema -update`), committed as `internal/config/config.schema.json`, and a test fails when it is stale. Its `$id` is the versioned GitHub Pages URL below, which `.github/workflows/pages.yml` publishes (WP-81b; `task pages:build` assembles the same site locally in `dist/pages/`):

```
https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json      # the $id: schema major version 1
https://xantiagoma.github.io/xtandard-image/schema/latest/config.schema.json  # alias of the newest major
```

`v1` is the schema's major version, not the release. It changes only when a key is removed or renamed, so a modeline keeps working across releases; new keys are added to `v1`. The Pages copy follows the newest published schema, so it can list a key that an older binary does not know yet: to check a file against exactly the version you run, use that instance's admin URL (below) or `config validate`. The OpenAPI document sits next to it (`schema/v1/openapi.json`, [OPENAPI.md](OPENAPI.md)).

YAML (VS Code with the Red Hat YAML extension, JetBrains, Neovim/Helix with yaml-language-server). Paste this as the first line:

```
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
```

JSON: a top-level `"$schema"` key, which the loader ignores:

```
{ "$schema": "https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json", "server": { "addr": ":8080" } }
```

TOML (Taplo / Even Better TOML, Tombi). Put this directive first:

```
#:schema https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
```

After the SchemaStore listing ([docs/schemastore](schemastore/README.md), pending until Pages is live), files named `xtandard.{yaml,yml,json,toml}`, `xtandard-image.{yaml,yml,json,toml}` and their dot-prefixed forms are matched without the line. The schema also ships in the binary, and every copy carries the same `$id`; all its `$ref`s are local, so any copy resolves the same way:

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

Every variable the server reads is listed here, with its default.

### Loader

| Variable | Default | Meaning |
|---|---|---|
| `XI_CONFIG_FILE` | `` | Config file, `.yaml`/`.yml`/`.json`/`.toml` (`--config` wins); when both are unset, the one of `/etc/xtandard/config.{yaml,yml,json,toml}` that exists is read |
| `XI_ALLOW_UNKNOWN_ENV` | `false` | Report unknown `XI_*` variables as warnings instead of failing startup |

### Core and server

| Variable | Default | Meaning |
|---|---|---|
| `XI_DEV_MODE` | `false` | Allow any http(s) source (SSRF rules still apply), pretty errors, no signature requirement warnings |
| `XI_ROLE` | `all` | What this instance serves (ADR 0012): `all`; `image` (video transformations answer 404, so a load balancer routes them elsewhere); `video` (only video transformations and the parts of their HLS and DASH streams under `/_stream/`, plus `/healthz`, `/readyz` and the admin listener; every other request answers 404; needs `XI_VIDEO_TRANSFORM_ENABLED`). One binary and image serve every role |
| `XI_SERVER_ADDR` | `:8080` | Listen address |
| `XI_SERVER_PATH_PREFIX` | `` | Global prefix stripped before routing |
| `XI_SERVER_REQUEST_TIMEOUT` | `20s` | Whole-request deadline |
| `XI_SERVER_READ_HEADER_TIMEOUT` | `5s` | Slowloris guard |
| `XI_SERVER_READ_TIMEOUT` | `15s` | |
| `XI_SERVER_WRITE_TIMEOUT` | request timeout + `5s` | |
| `XI_SERVER_IDLE_TIMEOUT` | `90s` | Keep-alive idle |
| `XI_SERVER_MAX_HEADER_BYTES` | `32KiB` | |
| `XI_SERVER_MAX_CONNECTIONS` | `4096` | Open connections on the public listener (the admin listener is not counted). At the cap the server stops accepting: new clients wait in the kernel's accept backlog until a connection closes, they are not refused. Idle keep-alive and HTTP/2 connections hold their slot until `XI_SERVER_IDLE_TIMEOUT`; one HTTP/2 connection carries many requests |
| `XI_SERVER_SHUTDOWN_DELAY` | `5s` | Readiness goes 503 this long before listener closes |
| `XI_SERVER_SHUTDOWN_TIMEOUT` | `30s` | Drain limit |
| `XI_SERVER_READY_QUEUE_THRESHOLD` | `0` (off) | `/readyz` answers 503 `busy` while the image processing queue is at least this % full: running plus waiting jobs over `XI_PROCESSING_WORKERS` + `XI_PROCESSING_QUEUE_SIZE` places (1–100). Checked on every probe with no hysteresis, so the probe's own thresholds (`failureThreshold`, `successThreshold`) are the only damping. Video transformations have their own pool and do not count. [OPERATIONS.md §3.5](OPERATIONS.md#35-readiness-and-drain) |
| `XI_SERVER_CORS_ORIGINS` | `` | Origins browsers may read responses from (`*` for any): a request whose `Origin` is listed gets `Access-Control-Allow-Origin` (`*`, or the origin with `Vary: Origin`) and the range and validator headers exposed, so HLS and DASH players in another origin's page can load playlists and segments; a preflight (`OPTIONS` for `GET`/`HEAD`) is answered 204. Empty: no CORS headers |
| `XI_SERVER_QUERY_METHOD` | `true` | Accept the HTTP `QUERY` method (RFC 10008) on the query dialect's endpoints (`/q/`, `/q/video`, `/q/<imaginary endpoint>`): the parameters travel in the request content (`application/x-www-form-urlencoded` or `application/json`) instead of the URL, with the same signing, unsigned policy, limits and result cache entry as the equivalent `GET` ([URL-DIALECTS §4.4](URL-DIALECTS.md#44-http-query)). `false`: `QUERY` is 405 everywhere, as before |
| `XI_SERVER_QUERY_MAX_BODY_BYTES` | `64KiB` | Largest `QUERY` content; more is 413 |
| `XI_SERVER_QUERY_LOCATION_MAX_LENGTH` | `2048` | Longest equivalent `GET` URL (path and query) a `QUERY` response carries as `Location`; longer, or a query with `pdf_password`, sends none. `0`: never. At most `8192` (the URL limit) |

### Security, signing, encryption

| Variable | Default | Meaning |
|---|---|---|
| `XI_SIGNING_KEYS`, `XI_SIGNING_SALTS` | `` | Hex key/salt pairs (same count); enables imgproxy-scheme and `xi_sig` verification |
| `XI_SIGNING_SIZE` | `32` | Signature bytes before base64 (imgproxy `SIGNATURE_SIZE`) |
| `XI_SIGNING_TRUSTED` | `` | Signatures always accepted |
| `XI_SIGNING_REQUIRED` | `true` when keys set | Reject unsigned imgproxy URLs |
| `XI_ENCRYPTION_KEYS`, `XI_ENCRYPTION_KEY` | `` | Hex AES keys (16/24/32 bytes) for `enc/` sources and `pdf_password` tokens ([URL-DIALECTS §3.7](URL-DIALECTS.md#37-password-protected-pdfs-wp-115)); the first encrypts, all decrypt (rotation). `XI_ENCRYPTION_KEY` is the deprecated single-key alias (YAML `encryption.key`); setting both is an error |
| `XI_SECURITY_UNSIGNED_POLICY` | `restricted` | `allow` \| `restricted` \| `deny` for every request that carries no verified signature, in any dialect (including imgproxy URLs when `XI_SIGNING_REQUIRED` is false) |
| `XI_SECURITY_ALLOWED_WIDTHS` | `16,32,48,64,96,128,256,384,640,750,828,1080,1200,1920,2048,3840` | Restricted-policy widths (Next.js device + image sizes); logged at startup |
| `XI_SECURITY_ALLOWED_HEIGHTS` | same as `XI_SECURITY_ALLOWED_WIDTHS` | Restricted-policy heights |
| `XI_SECURITY_ALLOWED_DPRS` | `1` | Restricted-policy device pixel ratios (0 < dpr ≤ 8); unset and `1` are always allowed |
| `XI_SECURITY_ALLOWED_QUALITIES` | `5-100/5` | Restricted-policy qualities (5, 10, …, 100) |
| `XI_SECURITY_ALLOW_SECURITY_OPTIONS` | `false` | Honour per-request limit options |
| `XI_SECURITY_ALLOW_RAISING_LIMITS` | `false` | Let them exceed config |
| `XI_SECURITY_SECRET` | `` | Require `Authorization: Bearer <secret>` on every public-listener request except `/healthz` and `/readyz` (CORS preflights are answered first); anything else is 403. For a CDN or proxy in front that adds the header. The playground's own renders are exempt |
| `XI_SECURITY_ALLOWED_PROCESSING_OPTIONS` | `` (all) | imgproxy dialect: the only option names a URL may use, as written (`w` does not allow `width`); others are 403. The options a preset expands to are not checked, `preset`/`pr` itself is |

### Sources and fetching

Each source is configured by the `XI_SOURCES_<NAME>_*` variables below (`<NAME>` is the source name upper-cased, `-` → `_`) or by `sources.<name>.*` in the file. How a URL picks one of several sources, which keys each type takes, provider recipes and complete examples: [SOURCES.md](SOURCES.md).

| Variable | Default | Meaning |
|---|---|---|
| `XI_SOURCES` | `` | Names of sources defined via env (`origin,assets`), added to the YAML ones |
| `XI_SOURCES_<NAME>_TYPE` | — | `http` \| `s3` \| `gcs` \| `azure` \| `fs` \| `ftp` \| `sftp` |
| `XI_SOURCES_<NAME>_ALLOW` | — | http: host globs with optional scheme, port and path prefix (`*.example.com`, `cdn.example.com/img/`, `https://*`, `*`); a bare `https://…` source and `xi://<name>/https://…` must match one. An http source needs `_ALLOW`, `_BASE_URL` or both |
| `XI_SOURCES_<NAME>_BASE_URL` | — (optional) | http: base for relative keys (`xi://<name>/a.jpg` → `<base>/a.jpg`), implicitly allowed. Without it, `xi://<name>/<key>` takes only an absolute `http(s)://` key, checked against `_ALLOW`; with it, an absolute key under the base is taken as is and any other key is joined to the base ([URL-DIALECTS §3.1](URL-DIALECTS.md#31-grammar)) |
| `XI_SOURCES_<NAME>_HEADERS` | — | http: extra request headers (`Authorization=…`) |
| `XI_SOURCES_<NAME>_BUCKETS` / `_BUCKET` | — | s3/gcs/azure: allowed buckets/containers; `_BUCKET` binds a default |
| `XI_SOURCES_<NAME>_PREFIX` | `` | object key prefix jail (every type except http) |
| `XI_SOURCES_<NAME>_ENDPOINT`, `_REGION`, `_FORCE_PATH_STYLE`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_ROLE_ARN` | — | s3 (R2, Spaces, B2, RustFS and other S3-compatible stores via endpoint). No keys → AWS default chain (env, shared files, IRSA, ECS/EC2 role); `_ROLE_ARN` assumes a role on top. `_REGION` falls back to the SDK chain, then `us-east-1` when an endpoint is set (R2: `auto`). Checksums are sent/validated only when required, so R2/B2 work |
| `XI_SOURCES_<NAME>_CREDENTIALS_JSON` | ADC | gcs: credentials document (`XI_SOURCES_<NAME>_CREDENTIALS_JSON_FILE` reads it from a mounted file); `XI_SOURCES_<NAME>_ENDPOINT` points at an emulator such as fake-gcs-server |
| `XI_SOURCES_<NAME>_ACCOUNT`, `_ACCOUNT_KEY`, `_CONNECTION_STRING`, `_SAS_TOKEN`, `_ENDPOINT` | `DefaultAzureCredential` (managed identity) | azure; one of `_ACCOUNT`, `_ENDPOINT` or `_CONNECTION_STRING` is required; the connection string stands alone (Validate refuses it next to account, key, SAS or endpoint) and needs `AccountName`+`AccountKey` or `SharedAccessSignature` (no `UseDevelopmentStorage=true`: give Azurite's explicit form); `_ACCOUNT_KEY` (base64) and `_SAS_TOKEN` (the token, not a SAS URL) are exclusive, else DefaultAzureCredential; `_ACCOUNT` is 3-24 lowercase letters and digits and containers (`_BUCKETS`) follow Azure's naming rule (3-63 lowercase letters, digits, single hyphens; `$root`, `$web`); `_ENDPOINT` overrides `https://<account>.blob.core.windows.net/` (Azurite: `http://127.0.0.1:10000/devstoreaccount1`; no query with a SAS token) |
| `XI_SOURCES_<NAME>_ROOT` | — | fs root (jail via `os.Root`); ftp/sftp: server directory keys are relative to (default `/`) |
| `XI_SOURCES_<NAME>_FOLLOW_SYMLINKS` | `false` | fs: follow symlinks whose target stays inside the root (links leaving it are always refused) |
| `XI_SOURCES_<NAME>_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_PRIVATE_KEY`, `_PRIVATE_KEY_PASSPHRASE`, `_HOST_KEY`, `_TLS` | — | ftp/sftp. ftp: empty `_USER` logs in as `anonymous`; `_TLS=true` is explicit TLS (`AUTH TLS`, `PROT P`), passive mode only. sftp: `_USER` plus `_PASSWORD` and/or `_PRIVATE_KEY` (PEM/OpenSSH; encrypted keys need `_PRIVATE_KEY_PASSPHRASE`); `_HOST_KEY` is mandatory (authorized_keys or known_hosts lines, several allowed for rotation, `#` comments; no trust-on-first-use). `_HOST` is a bare host name or address (no scheme or path). `config validate` parses the private key and every host key line |
| `XI_SOURCES_<NAME>_POOL_SIZE` | `4` | ftp/sftp: max open connections to the server |
| `XI_SOURCES_<NAME>_DIAL_TIMEOUT` | `10s` | ftp/sftp: connect + TLS/SSH handshake + login |
| `XI_SOURCES_<NAME>_IDLE_TIMEOUT` | `1m` | ftp/sftp: pooled connections idle longer are closed |
| `XI_SOURCES_<NAME>_PDF_PASSWORD` | — | every type: default password for password-protected PDFs of this source, used when a request carries no `pdf_password` of its own ([URL-DIALECTS §3.7](URL-DIALECTS.md#37-password-protected-pdfs-wp-115)); a secret (`_PDF_PASSWORD_FILE`, `{file: …}`), at most 1024 bytes without control characters. Rotating it does not purge renditions already in the result cache |
| `XI_FETCH_TIMEOUT` | `10s` | Per download attempt |
| `XI_FETCH_RETRY` | `true` | One retry, after a jittered ~100 ms backoff, of a transient failure (origin 5xx, connection error, attempt timeout); never for 4xx |
| `XI_FETCH_MAX_REDIRECTS` | `5` | |
| `XI_FETCH_MAX_CONCURRENT` | `256` | Origin downloads in progress at once, all sources together. Coalesced requests and cache hits take none; a download waits for a slot within its `XI_FETCH_TIMEOUT` (the wait counts against it) and is 503 when none frees up |
| `XI_FETCH_ALLOWED_PORTS` | `80,443` | SSRF port allowlist |
| `XI_FETCH_ALLOW_PRIVATE_NETWORKS` | `false` | Explicit opt-in to every private range |
| `XI_FETCH_ALLOWED_NETWORKS` | `` | CIDRs exempt from the private-address block without opening every range (e.g. `10.20.0.0/16` for an in-cluster object store) |
| `XI_FETCH_USER_AGENT` | `` (`xtandard-image/<version>`) | |

### Dialects and presets

| Variable | Default | Meaning |
|---|---|---|
| `XI_DIALECTS_DEFAULT` | `imgproxy` | Fallback dialect for paths no mount matches (it must be enabled); a dialect other than imgproxy serves them with its primary mount's source and unsigned policy |
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
| `XI_DIALECTS_IMGIX_KEYS` | `` | imgix secure URL tokens, used verbatim (`,`-separated for rotation: any verifies, the first signs in the playground). With tokens, `s` = hex(MD5(token ‖ path ‖ `?` ‖ query without `s`)) is verified and `expires` enforced; without, `s` is not checked and every URL follows the mount's unsigned policy. `XI_SIGNING_REQUIRED=true` rejects unsigned imgix URLs on mounts with tokens. Prefix default `/imgix/`; host-bind (`XI_DIALECTS_IMGIX_HOSTS`) for unpic's imgix provider; `XI_DIALECTS_IMGIX_SOURCE` resolves object keys (URL-DIALECTS §5) |
| `XI_DIALECTS_CLOUDINARY_CLOUDS` | `` | `cloud=source,…` |
| `XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH` | `` | ImageKit URL-endpoint id kept in the path of host-bound mounts (`https://ik.imagekit.io/<id>/…` → `/<id>`), for every ImageKit mount: stripped before parsing, excluded from the source key and the signed message; requests outside it are 400 (URL-DIALECTS §7) |
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
| `XI_PROCESSING_GO_MEMORY_FRACTION` | `0.3` | Sets the Go memory limit (`GOMEMLIMIT`) at startup to this fraction of the cgroup memory limit (v2 `memory.max`, v1 `memory.limit_in_bytes`; the smallest from the process's cgroup up). A `GOMEMLIMIT` you set wins; without a limit nothing changes. Logged once (`go memory limit set`); [OPERATIONS.md §1.3](OPERATIONS.md#13-memory) |
| `XI_PROCESSING_AUTO_ROTATE` | `true` | |
| `XI_PROCESSING_STRIP_METADATA` | `true` | |
| `XI_PROCESSING_KEEP_COPYRIGHT` | `true` | |
| `XI_PROCESSING_STRIP_COLOR_PROFILE` | `true` | |
| `XI_PROCESSING_PRESERVE_HDR` | `false` | |
| `XI_PROCESSING_LINEAR_RESIZE` | `false` | Resize in linear light. **Not implemented yet**: validated and accepted, but resizing is always in the image's own colour space (ROADMAP backlog) |
| `XI_PROCESSING_COLOR_PROFILES_DIR` | `` | Custom ICC files |
| `XI_PROCESSING_WATERMARK_SOURCE` | `` | Default watermark image (source string, resolved against the sources). Fetched and decoded once at startup, never refreshed; a watermark that cannot be loaded stops startup. The playground's `api/meta` reports only whether it is set (`watermark`), for the Examples that need it |
| `XI_PROCESSING_WATERMARK_OPACITY` | `1` | Multiplies every request's watermark opacity (configured image, per-request image and text) |
| `XI_PROCESSING_WATERMARK_FONT` | `sans 16` | Pango font description of text watermarks (imgproxy's default); a request's font takes the family or size it lacks from it. The image ships DejaVu Sans and DejaVu Sans Mono (`sans`, `monospace`) through fontconfig |
| `XI_PROCESSING_WATERMARK_FONT_FILE` | `` | Absolute path of a font file (TTF/OTF) registered with fontconfig for text watermarks, in addition to the fonts under `/opt/xtandard/share/fonts`; requests select it by family name |
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
| `XI_FORMATS_EPS_ENABLED` | `false` | **Security-sensitive; `gpl` image only.** Render EPS, PostScript and PostScript-based Illustrator sources with Ghostscript (AGPL-3.0), a full PostScript interpreter run on untrusted input in a sandboxed subprocess per request (ADR 0011, FORMATS §1.6). `true` without a usable `gs` stops startup. Off, a DOS EPS file's embedded TIFF preview is used when it has one, and any other EPS answers 422 "EPS needs the gpl image with XI_FORMATS_EPS_ENABLED=true". Enabling it makes the running image AGPL-3.0 software you offer over a network (see the security note below the table) |
| `XI_FORMATS_EPS_MAX_PROCS` | `2` | Concurrent Ghostscript processes (1–256), on top of the worker limit; a render beyond it fails fast with 429 |
| `XI_FORMATS_EPS_TIMEOUT` | `10s` | Wall-clock limit of one render (100ms–10m); the process is killed with its process group and the request answers 504. Its CPU-time limit is the same, rounded up, plus one second |
| `XI_FORMATS_EPS_MAX_DPI` | `600` | Highest render resolution (1–2400); pages are also capped at `XI_LIMITS_MAX_SRC_PIXELS` by lowering the resolution |
| `XI_FORMATS_EPS_GS_PATH` | `gs` | Ghostscript executable: absolute path or a name looked up in `PATH` (the gpl image has `/opt/xtandard/bin/gs`) |
| `XI_FORMATS_METAFILE_ENABLED` | `auto` | **`gpl` image only.** Convert Windows metafiles (WMF, EMF and EMF+) to SVG with `xi-metafile2svg` (libwmf, LGPL-2.0-or-later; libemf2svg, GPL-2.0-only), one sandboxed subprocess per source (the Ghostscript sandbox, ADR 0011), then render the SVG like any other (FORMATS §1.8). `auto` converts when the converter is found and passes its startup check (`--version` through the sandbox), and otherwise answers 422 "WMF and EMF input needs the gpl image (XI_FORMATS_METAFILE_ENABLED)"; a converter that is present but fails the check logs the warning `metafile conversion unavailable` with the reason and never stops startup. `true` without a usable converter stops startup; `false` always answers that 422 |
| `XI_FORMATS_METAFILE_MAX_PROCS` | `2` | Concurrent conversions (1–256), on top of the worker limit; a conversion beyond it fails fast with 429 |
| `XI_FORMATS_METAFILE_TIMEOUT` | `10s` | Wall-clock limit of one conversion (100ms–10m); the process is killed with its process group and the request answers 504. Its CPU-time limit is the same, rounded up, plus one second |
| `XI_FORMATS_METAFILE_PATH` | `xi-metafile2svg` | The converter: absolute path or a name looked up in `PATH` (the gpl image has `/opt/xtandard/bin/xi-metafile2svg`) |
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
| `XI_LIMITS_MAX_SRC_PIXELS` | `50000000` | Width × height × frames loaded, as imgproxy's `IMGPROXY_MAX_SRC_RESOLUTION`: a still (a multi-page still counts the pages it loads), the first frame of an animation processed as a still, and an animation processed as one only while `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` is `0`. The 422 body names the limit and the numbers, e.g. `source has 176 frames of 800×600 (84.5 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP`. A camera RAW counts what is decoded: its embedded preview, its half-size demosaic or its full demosaic, whichever the request takes (FORMATS.md §2) |
| `XI_LIMITS_MAX_RESULT_DIMENSION` | `8192` | |
| `XI_LIMITS_MAX_ANIMATION_FRAMES` | `256` | Frames an animation may declare (422 above it, checked on the header); `1` disables animation instead (the first frame is processed, as in imgproxy) |
| `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` | `16000000` | Width × height of one frame of an animation processed as one. When set it **replaces** `XI_LIMITS_MAX_SRC_PIXELS` for those animations, as imgproxy's `IMGPROXY_MAX_ANIMATION_FRAME_RESOLUTION` does, so an 800×600 GIF of 176 frames (84.5 MP in all) is accepted; the total is then bounded by `XI_LIMITS_MAX_ANIMATION_PIXELS`. `0` turns it off: the animation counts all its frames against `XI_LIMITS_MAX_SRC_PIXELS` (imgproxy's default) |
| `XI_LIMITS_MAX_ANIMATION_PIXELS` | `200000000` | Width × height × frames of an animation that `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` checks instead of `XI_LIMITS_MAX_SRC_PIXELS` (not in imgproxy). The frame limit alone would admit 256 frames of 16 MP (4.1 gigapixels, 12–16 GB decoded); this caps one animation at about 0.6–0.8 GB of decoded frames (3–4 bytes a pixel, the planner's memory estimate). `XI_PROCESSING_MEMORY_BUDGET` still applies on top; no URL option changes it |
| `XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS` | `120000000` | Sensor width × height a camera RAW may be demosaiced at, at full or half size (102 MP medium format and 50 MP phones pass); the embedded-preview path is not affected. The decoded image also counts against `XI_LIMITS_MAX_SRC_PIXELS` |
| `XI_LIMITS_MAX_SVG_BYTES` | `5MiB` | |
| `XI_LIMITS_MAX_SVG_DEPTH` | `256` | SVG sanitiser nesting limit |
| `XI_LIMITS_MAX_SVG_ELEMENTS` | `200000` | SVG sanitiser element limit |
| `XI_LIMITS_MAX_PDF_PAGES` | `500` | Pages a PDF may have; checked when the header is read, before any page renders (422) |
| `XI_LIMITS_MAX_WATERMARK_TEXT_PIXELS` | `16777216` | Area a text watermark is rendered on (the frame, or the text width × the frame height, scaled down to this); text that does not fit is shrunk until it does |
| `XI_SANDBOX_MAX_AS` | `1GiB` | Address-space limit (`RLIMIT_AS`) of each sandboxed subprocess (Ghostscript, the metafile converter; ADR 0011), plus twice the output the render may produce (capped at 16 GiB). `0` turns it off; the CPU-time, process, file-size and open-file limits, the timeout and no-new-privileges stay. Otherwise 64MiB–1TiB. Startup logs the value as `rlimit_as` (and `rlimit_as_off` with the reason when there is none) |
| `XI_SANDBOX_DETECT_EMULATION` | `true` | Drop `XI_SANDBOX_MAX_AS` when the server runs under CPU emulation (Rosetta for Linux, e.g. an amd64 image on Apple silicon under OrbStack or Docker Desktop; QEMU user mode): the translator maps its own memory inside the subprocess and fails under the limit (`rosetta error: mmap_anonymous_rw mmap failed`). Startup logs `emulation` and a warning `sandbox without an address-space limit`. `false` keeps the limit everywhere. The trade-off: under emulation a hostile document can make one subprocess allocate up to what the container allows (bounded by its CPU time and the timeout); set a container memory limit |
| `XI_VIDEO_ENABLED` | `auto` | Video sources (MP4, MOV, WebM, MKV, AVI; FORMATS.md §1.5): `auto` decodes them when the image has FFmpeg (the `full` flavor) and answers 422 otherwise; `true` fails startup without FFmpeg; `false` answers 422 in every image |
| `XI_VIDEO_MAX_BYTES` | `100MiB` | Largest video source; `XI_LIMITS_MAX_SRC_BYTES` applies first, at fetch, so raise both for large videos (422) |
| `XI_VIDEO_MAX_DURATION` | `1h` | Longest video, from the container header, before any frame is decoded (422) |
| `XI_VIDEO_MAX_PIXELS` | `33177600` | Width × height of the video stream (8K UHD); the decoder refuses larger frames too (422). `XI_LIMITS_MAX_SRC_PIXELS` applies to the frame as well |
| `XI_VIDEO_THUMBNAIL_SECOND` | `auto` | Frame taken when a request sets no `video_thumbnail_second`: seconds (negative counts from the end; clamped to the video), or `auto` for best-frame selection (imgproxy `IMGPROXY_VIDEO_THUMBNAIL_SECOND`, whose default is 1) |
| `XI_VIDEO_THUMBNAIL_KEYFRAMES` | `false` | Take the keyframe at or before the timestamp instead of the exact frame (faster, less precise; imgproxy `IMGPROXY_VIDEO_THUMBNAIL_KEYFRAMES`); a request's `video_thumbnail_keyframes` overrides it |
| `XI_VIDEO_THUMBNAIL_SAMPLES` | `5` | Keyframes best-frame selection compares (1–32) |
| `XI_VIDEO_THUMBNAIL_SPAN` | `10s` | How much of the start of a video best-frame selection samples |
| `XI_VIDEO_MAX_ANIMATION_FRAMES` | `100` | Frames a `video_thumbnail_tile` sprite or a `video_thumbnail_animation` decodes (1–10000); larger grids keep their columns and lose rows, longer animations are cut. `XI_LIMITS_MAX_ANIMATION_FRAMES` and the pixel limits still apply to the result (422) |
| `XI_VIDEO_ENCODE_VP9_CRF` | `36` | CRF of WebM (VP9, libvpx) output when the request sets no quality (0–63). A request quality maps onto the scale around it: 80 is this CRF, 100 a quarter of it, 1 the worst (FORMATS.md §5.1) |
| `XI_VIDEO_ENCODE_AV1_CRF` | `36` | CRF of AV1-in-MP4 output (libaom, the `full` image) (0–63) |
| `XI_VIDEO_ENCODE_H264_CRF` | `25` | CRF of H.264 MP4 output (libx264, the `gpl` image) (0–51) |
| `XI_VIDEO_ENCODE_HEVC_CRF` | `28` | CRF of HEVC output (libx265, the `gpl` image) (0–51) |
| `XI_VIDEO_ENCODE_VP9_SPEED` | `8` | libvpx `cpu-used` in its realtime mode (0–9; higher is faster and larger) |
| `XI_VIDEO_ENCODE_AV1_SPEED` | `8` | libaom `cpu-used` in its realtime mode (0–10; higher is faster and larger) |
| `XI_VIDEO_ENCODE_H264_PRESET` | `veryfast` | x264 preset (`ultrafast` … `placebo`) |
| `XI_VIDEO_ENCODE_HEVC_PRESET` | `veryfast` | x265 preset (`ultrafast` … `placebo`) |
| `XI_VIDEO_ENCODE_MAX_FRAMES` | `1000` | Most frames a video output may have (422) |
| `XI_VIDEO_ENCODE_MAX_DURATION` | `1m` | Longest video output, the sum of the frame delays (422) |
| `XI_VIDEO_ENCODE_MAX_PIXELS` | `8294400` | Largest frame of a video output, width × height (4K UHD) (422) |
| `XI_VIDEO_TRANSFORM_ENABLED` | `false` | Video transformations (WP-121 to WP-123, ADR 0012): `/video/<signature>/<options>/<source>@<container>` and `/q/video?url=…` (URL-DIALECTS §3.8) trim, resize, crop, rotate and transcode a video into MP4 or WebM, its audio (or an audio file's) into M4A, MP3, Opus, Ogg, FLAC or WAV with volume, loudness normalisation and fades, or draw it as a PNG or WebP waveform. Off: they answer 404. `true` fails startup on an image without FFmpeg (`slim`); with `XI_ROLE=image` the pool is not created. Unsigned requests are refused by default (`XI_VIDEO_TRANSFORM_UNSIGNED_POLICY`). Which codecs exist depends on the image: MP4 is H.264 in `gpl` and AV1 in `full`; audio needs the WP-120 FFmpeg |
| `XI_VIDEO_TRANSFORM_WORKERS` | GOMAXPROCS / 4 (≥ 1) | Size of the video pool, separate from the image pool (`XI_PROCESSING_WORKERS`): the probes run at once, and the defaults of `XI_VIDEO_TRANSFORM_CAPACITY` (`WORKERS × 10000`) and `XI_VIDEO_TRANSFORM_MAX_JOBS` (`2 × WORKERS`). Transcodes are admitted by cost, not one per worker (WP-121c, ADR 0012 §4) |
| `XI_VIDEO_TRANSFORM_CAPACITY` | workers × 10000 | Cost units the running transcodes may hold together. A transcode's cost is estimated from its probe and plan (decode + encode pixels × frames × a measured per-codec factor, audio seconds; HLS/DASH per segment): one unit is about a millisecond of one core's work, so `10000` is ten seconds of work per worker. A transcode costing more than the whole capacity is not refused: it runs alone |
| `XI_VIDEO_TRANSFORM_MAX_JOBS` | 2 × workers | Most transcodes running at once whatever their cost, so cheap ones (audio, small segments) can run beside a heavy one but never more than this (0–4096; 0 is the default) |
| `XI_VIDEO_TRANSFORM_QUEUE` | `8` | Transcodes that may wait beyond `XI_VIDEO_TRANSFORM_MAX_JOBS` (a place is taken from the probe on); beyond it 429 + `Retry-After` |
| `XI_VIDEO_TRANSFORM_QUEUE_COST` | max(queue, 1) × 10000 | Cost units the waiting transcodes may add up to; a transcode that would go over it is refused with 429 + `Retry-After` (one always waits behind an empty queue) |
| `XI_VIDEO_TRANSFORM_QUEUE_TIMEOUT` | `30s` | How long a transcode waits for its probe and for capacity before 503 + `Retry-After`. Waiting is first come, first served: a large transcode at the head of the queue is not overtaken by smaller ones |
| `XI_VIDEO_TRANSFORM_TIMEOUT` | `2m` | Wall time of one transcode from admission (504). A video request's budget is `XI_FETCH_TIMEOUT` + queue timeout + this, instead of `XI_SERVER_REQUEST_TIMEOUT`, and its write deadline is extended to match |
| `XI_VIDEO_TRANSFORM_THREADS` | `1` | Decoder and encoder threads of one transcode (1–64) |
| `XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES` | `512MiB` | Largest video source of a transformation (422); the fetcher accepts the larger of this and `XI_LIMITS_MAX_SRC_BYTES` for video requests. A source over `XI_VIDEO_TRANSFORM_SPOOL_THRESHOLD` is downloaded into a spool file instead of memory |
| `XI_VIDEO_TRANSFORM_MAX_INPUT_DURATION` | `1h` | Longest video source, from its header (422) |
| `XI_VIDEO_TRANSFORM_MAX_INPUT_PIXELS` | `33177600` | Width × height of the source's video stream (8K UHD); the decoder refuses larger frames too (422) |
| `XI_VIDEO_TRANSFORM_MAX_OUTPUT_BYTES` | `256MiB` | Largest output file; the muxer stops past it (422). An output growing past `XI_VIDEO_TRANSFORM_SPOOL_THRESHOLD` moves into a spool file; while it is written it reserves this much of `XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES` |
| `XI_VIDEO_TRANSFORM_MAX_OUTPUT_DURATION` | `5m` | Longest output (the cut, or the whole source) (422) |
| `XI_VIDEO_TRANSFORM_MAX_OUTPUT_PIXELS` | `8294400` | Largest output frame, width × height (4K UHD). A request that names no size is scaled down to fit it (aspect kept); an explicit size over it is 422 |
| `XI_VIDEO_TRANSFORM_MAX_FPS` | `60` | Highest output frame rate: a faster source is reduced to it, a faster request refused (422) (1–240) |
| `XI_VIDEO_TRANSFORM_UNSIGNED_POLICY` | `deny` | Video transformations without a verified signature (ADR 0012 §8), in every dialect: `deny` answers them 403 even on a mount whose `XI_SECURITY_UNSIGNED_POLICY` (or dialect policy) is `allow`; `allow` leaves them to the mount's policy (a `restricted` mount still refuses them, its allowlists describe images); `restricted` (WP-132) admits only what the URL's video presets make of it, on any mount whose policy is not `deny`: the URL must name at least one of `XI_VIDEO_TRANSFORM_PRESETS` (imgproxy and query `pr`/`preset`, Cloudinary `t_`, ImageKit `n-`), and its result must equal what those presets alone give, except for `XI_VIDEO_TRANSFORM_UNSIGNED_ALLOWED_OPTIONS` and a container of `XI_VIDEO_TRANSFORM_UNSIGNED_FORMATS`. So HLS/DASH streams and `raw:1` come only from presets; no cache buster or cache bypass, security options or chained pipelines. Refusals are 403 `unsigned request not allowed` (404 with `XI_COMPAT_IMGPROXY_STATUS`), before anything is fetched. `restricted` needs at least one video preset. Signed requests are never affected |
| `XI_VIDEO_TRANSFORM_PRESETS` | — | Video presets (WP-132): `name=options,name2=options`, the options in `/video` syntax (URL-DIALECTS §3.8: `/`-separated `name:args` with `XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR`, no chained pipelines, no delivery options such as `cb`, `exp` or `fn`); YAML `video.transform.presets: {name: options}`. URLs use them with `pr:name` (imgproxy `/video`, nested references allowed, expanded in place: later options override), `preset=name` (`/q/video`), `t_name` (Cloudinary video) and `n-name` (ImageKit video), where the presets are the starting point and the URL's own parameters override them. `default` applies to every video transformation first. Separate from `XI_PRESETS` (image options). Validated at startup: a bad name or syntax, an unknown option, a bad value or a preset over `XI_VIDEO_TRANSFORM_MAX_OUTPUT_PIXELS`, `_MAX_FPS` or `_MAX_OUTPUT_DURATION` fails it, naming the preset. Names (not contents) are listed in the playground's video meta. With `XI_PRESETS_ONLY=true`, `/video` URLs may hold video preset references only |
| `XI_VIDEO_TRANSFORM_UNSIGNED_ALLOWED_OPTIONS` | — | Options an unsigned request may set besides its presets under the `restricted` video policy: any of `start`, `end`, `duration`, `mute` (by meaning, in every dialect: imgproxy `vs`, Cloudinary `so_`, ImageKit `so-`, …). Empty: none |
| `XI_VIDEO_TRANSFORM_UNSIGNED_FORMATS` | `mp4,webm` | Containers an unsigned `restricted` request may pick (extension, `format`, `f_`, `f-`) instead of its presets' one, of the same kind: a file with video, audio only (`m4a`, `mp3`, `opus`, `ogg`, `flac`, `wav`) or a waveform picture (`png`, `webp`). An automatic choice (Cloudinary `f_auto`, ImageKit's default) needs `mp4` and `webm`. Empty: the presets' container only |
| `XI_VIDEO_TRANSFORM_UNSIGNED_MAX_DURATION` | `0` | When an unsigned `restricted` request changes the cut with `start`, `end` or `duration`, the cut must have a duration or an end and be at most this long (403 otherwise). `0`: only `XI_VIDEO_TRANSFORM_MAX_OUTPUT_DURATION` |
| `XI_VIDEO_TRANSFORM_ABR_LADDER` | `1080:5000k,720:2800k,480:1400k,360:800k,240:400k` | Renditions of HLS and DASH streams (WP-126, ADR 0012 §6), as `<height>[:<bit rate>]` items: the short side of the frame (64–4320; `720` is 1280×720 for a 16:9 landscape source and 720×1280 for a portrait one) and its peak video bit rate in bits per second with an optional `k`/`m` suffix (left out: the standard rate of that height). 1–8 rungs; rungs larger than the source are dropped (a smaller source gets one rendition of its own size). A URL may name its own subset (Cloudinary `sp_`, ImageKit `sr-`, imgproxy `st`) |
| `XI_VIDEO_TRANSFORM_SEGMENT_DURATION` | `6s` | Length of a stream's segments, whole seconds from `1s` to `30s`; the last one is shorter. A video segment has one keyframe (its first) unless the request sets a keyframe interval |
| `XI_VIDEO_TRANSFORM_AUDIO_CHUNK` | `30s` | How much of a stream's audio one transcode encodes (WP-131), rounded down to whole segments (at least one): a segment's request encodes its chunk, every chunk starts with a pre-roll of the source so the chunks join without gaps or clicks, and the next chunk is prefetched. Only sources whose audio timestamps name samples exactly (MP4, MOV, Ogg, FLAC, WAV, MP3; not Matroska/WebM, which count milliseconds) and streams without `loudnorm` are chunked; the others encode their track at once. `0` (or whole seconds up to `1h`): `0` encodes every track at once (the WP-126 behaviour), which needs `XI_VIDEO_TRANSFORM_TIMEOUT` to cover the whole track's encode |
| `XI_VIDEO_TRANSFORM_PREFETCH_SEGMENTS` | `2` | Segments after a requested one that are transcoded ahead (0–16), only when the video pool has a free slot and the segment's cost in free capacity at that moment and nobody waits: a prefetch never queues and never delays a request |
| `XI_VIDEO_TRANSFORM_STREAM_TTL` | `12h` | How long the child URLs of a manifest (`/_stream/<ticket>/…`: playlists and segments) work; a manifest is cacheable for a quarter of it (its tickets move in quarter steps). At least `1m` |
| `XI_VIDEO_TRANSFORM_STREAM_KEYS` | — | Hex keys (at least 16 bytes) that seal the tickets of child URLs (AES-256-GCM: they cannot be forged and do not show the source); the first seals, every one opens (rotation). Empty derives one from the first `XI_SIGNING_KEYS` key, else a random key per process (child URLs then work only on the instance that wrote the manifest, until it restarts; a startup warning says so). Set it on every instance of a fleet |
| `XI_VIDEO_TRANSFORM_STREAM_CACHE_BYTES` | `256MiB` | In-memory cache of stream probes and segments (prefetched ones included), in front of the result cache, which keeps them too (a persistent tier is recommended for streams). `0` is off: then prefetching only helps with a result cache |
| `XI_VIDEO_TRANSFORM_SPOOL_DIR` | temporary directory + `/xtandard-video` (`/tmp/xtandard-video` in the images) | Directory of the spool files (WP-121b): video sources and outputs larger than the threshold live there instead of memory. Created `0700` if missing; it must be writable, so with a read-only root file system mount a `tmpfs` or an `emptyDir` there (DEPLOY.md). Files are unlinked as soon as they are created (an open descriptor is their only name), so they vanish on every path, a crash included; at start, files named `xi-spool-*` that a previous process left are removed. Absolute path |
| `XI_VIDEO_TRANSFORM_SPOOL_THRESHOLD` | `32MiB` | A video source (declared length, or found while reading one of unknown length) or a transcode output larger than this goes to a spool file instead of memory, so a transcode's memory no longer grows with `MAX_INPUT_BYTES + MAX_OUTPUT_BYTES`. The file is read and written by the FFmpeg shim through a duplicate of its descriptor, served with `Range` from the file and streamed into the disk and S3 result-cache tiers (the memory tier skips it; redis keeps only values up to `XI_CACHE_RESULT_REDIS_MAX_VALUE_BYTES`). Stream segments and waveforms stay in memory. `0` keeps everything in memory (the WP-121 behaviour) |
| `XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES` | `0` | Bound on the bytes of all spool files at once: a download reserves its declared length (or grows while it reads), an encode reserves `XI_VIDEO_TRANSFORM_MAX_OUTPUT_BYTES` until it ends; a reservation that does not fit, after dropping the shared sources nobody reads, answers 503 with `Retry-After: 5`. `0` is no bound (the file system's size is). Needs a threshold above 0 |
| `XI_VIDEO_TRANSFORM_SPOOL_TTL` | `1m` | A spooled source is shared by the requests that need it (the segments of an HLS or DASH stream, repeated transformations): reused without asking the origin for this long after its download, kept while it is used at least once per period (then revalidated, with `If-None-Match`/`If-Modified-Since` when the origin sent an ETag or Last-Modified), and removed after a period without use. `0` shares nothing (concurrent requests still share one download) |
| `XI_VIPS_CONCURRENCY` | `1` | libvips threads per image; change only with benchmarks |
| `XI_VIPS_FAIL_ON` | `warning` | |

> [!WARNING]
> **`XI_FORMATS_EPS_ENABLED=true` runs a PostScript interpreter on untrusted input.** PostScript is a programming language, and Ghostscript's `-dSAFER` sandbox has been bypassed many times (file read/write and command execution from a crafted document, for example CVE-2018-16509, CVE-2021-3781, CVE-2023-36664, CVE-2024-29510). xtandard-image runs each render as a separate `gs` process with `-dSAFER`, a fixed page size, no input or output files, its own process group, a wall-clock and a CPU limit, an address-space, file-size, open-file and process limit, no-new-privileges, the parent-death signal, an unprivileged user and, where the kernel and the container's seccomp profile allow it, an empty network namespace (Docker's default profile does not; startup logs `eps rendering enabled` with the layers that hold). The server process is marked non-dumpable, so a process of the same user cannot read its memory or its environment (`XI_KEY`, storage credentials). What that does **not** stop: a `gs` exploit still runs as the server's user inside the server's container, where it can read every file that user can read (a mounted config file or key, for example), signal the server and, without the network namespace, open network connections. Enable it only for sources you trust, or run the gpl image as a separate, network-restricted deployment for EPS. Ghostscript is **AGPL-3.0**: an instance with it enabled is a network service running AGPL software, and you must offer its corresponding source to its users (the gpl image's `LICENSE-GPL.md` and `SOURCES.md` point to it). ADR 0011 has the details.

### Detection

Face and object detection (WP-71, [ARCHITECTURE.md](ARCHITECTURE.md) "Detection") needs the `full` image: ONNX Runtime and the YuNet and YOLOX-s models ship there, under `/usr/share/xtandard/models` with their licences and a `SHA256SUMS` file. On `slim` the defaults find nothing and the service runs without detection.

| Variable | Default | Meaning |
|---|---|---|
| `XI_DETECT_ENABLED` | `auto` | `auto` loads detection when the library and the model files are found and runs without it otherwise (logged at startup); `true` fails startup when they are missing; `false` never loads them. Options that need detection answer 422 while it is off |
| `XI_DETECT_LIBRARY` | `libonnxruntime.so.1` | ONNX Runtime shared library (1.17 or newer): an absolute path or a file name the dynamic linker resolves. Loaded with `dlopen` at startup, never at init (ADR 0010) |
| `XI_DETECT_MODELS_DIR` | `/usr/share/xtandard/models` | Absolute directory of the model files; mount your own there |
| `XI_DETECT_FACE_MODEL` | `face_detection_yunet_2023mar.onnx` | YuNet face model, a file name in `XI_DETECT_MODELS_DIR` or an absolute path; empty disables face detection |
| `XI_DETECT_OBJECT_MODEL` | `yolox_s.onnx` | YOLOX object model (COCO, 80 classes, `[1,N,85]` output); empty disables object detection |
| `XI_DETECT_CLASSIFY_MODEL` | `mobilenetv3_large_100.onnx` | Image classifier of the `/info` option `classify` (WP-110): an ONNX model with one float32 NCHW or NHWC RGB input and one score per class, a file name in `XI_DETECT_MODELS_DIR` or an absolute path; empty disables classification. The shipped one is MobileNetV3-Large (timm, ImageNet-1k, Apache-2.0). Its session is created on the first request that classifies, so a deployment that never classifies pays nothing for it. A missing file disables classification only (detection keeps running; `classify` is then reported in `X-Xi-Ignored`) |
| `XI_DETECT_CLASSIFY_LABELS` | *(empty)* | Absolute path of the class names file of a custom classifier: one name per line, in output order (imgproxy's `IMGPROXY_CLASSIFICATION_CLASSES` format); empty uses the embedded ImageNet-1k names (imagenet-simple-labels, public domain), which need a 1000-class output |
| `XI_DETECT_CLASSIFY_NORMALIZATION` | `imagenet` | How 8-bit input values `v` are scaled: `imagenet` (`(v/255 − mean)/std` with ImageNet statistics: timm and torchvision models), `full` (`v/127.5 − 1`, TensorFlow models such as EfficientNet-Lite), `half` (`v/255`) or `none` (0–255). The image is resized so its short side is the input size / 0.875 and centre-cropped (timm's evaluation transform) |
| `XI_DETECT_CLASSIFY_TOP_K_MAX` | `20` | Most classes one request gets: `cl:<top_k>` above it is capped (1–64) |
| `XI_DETECT_CLASSIFY_THRESHOLD` | `0` | Classes with a probability below it are left out (0–1). imgproxy's default is 0.5 for its multi-label model; a 1000-class softmax spreads probability over similar classes, so 0 returns the `top_k` asked for |
| `XI_DETECT_THREADS` | `1` | ONNX Runtime intra-op threads per inference; like `XI_VIPS_CONCURRENCY`, parallelism normally comes from concurrent requests (1–64) |
| `XI_DETECT_WORKERS` | `0` | Inferences running at once (a semaphore; more wait); `0` is GOMAXPROCS / `XI_DETECT_THREADS` |
| `XI_DETECT_MAX_INPUT` | `640` | Longest side fed to a model whose input size is dynamic (32–4096); the shipped models are fixed at 640×640 |
| `XI_DETECT_VERIFY_CHECKSUMS` | `false` | Check each model against the `SHA256SUMS` of its directory when it lists the model; a mismatch fails startup |
| `XI_DETECT_CACHE_SIZE` | `512` | Detection results kept in memory, least recently used first out: one entry per model and source (keyed by the SHA-256 of the source bytes), shared by processing and `/info` (0–1000000; `0` disables the cache) |
| `XI_DETECT_SMART_FACES` | `true` | Smart gravity (imgproxy `sm`, imgix `crop=edges`, Cloudinary `g_auto`, thumbor `smart`, …) crops on the detected faces when there are some and falls back to libvips attention otherwise; `false` always uses attention. Without the face model (slim) smart gravity is attention |

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

### Playground

The playground (WP-80, v2 in WP-94) is a web UI embedded in the binary: pick a source (sample gallery, upload, fs browser or URL) and a dialect mount, build options from the dialect's option catalogue, get a (signed) URL, see the same transform in every other dialect, and compare the source with the result (slider, zoom, headers, `Server-Timing`, `/info`, detections). It is plain HTML, CSS and JavaScript modules served with `Content-Security-Policy: default-src 'self'` and loads nothing from other origins, so it works air-gapped (the public sample gallery needs network access; the built-in samples do not).

| Variable | Default | Meaning |
|---|---|---|
| `XI_PLAYGROUND_ENABLED` | `false` | Serve the playground at `XI_PLAYGROUND_PATH` on the admin listener (needs `XI_ADMIN_ADDR` unless `XI_PLAYGROUND_PUBLIC`); off, every playground path answers 404 |
| `XI_PLAYGROUND_PUBLIC` | `false` | Also serve it on the public listener (under `XI_SERVER_PATH_PREFIX`). Its sign endpoint then signs any URL for anyone who can reach it, which makes signing pointless: only for trusted networks and demos |
| `XI_PLAYGROUND_PATH` | `/_playground` | Mount path: starts with `/`, no trailing `/`, `[A-Za-z0-9._~/-]` only, and not an admin route |
| `XI_PLAYGROUND_UPLOADS` | `auto` (= `XI_PLAYGROUND_ENABLED`) | `POST api/upload`: files dropped on the page are kept in memory (at most `XI_LIMITS_MAX_SRC_BYTES` each, 64 files or 256 MiB, 10 minutes) as `playground://uploads/<id>` sources that only `api/render` can read. `false` disables uploads; `true` needs `XI_PLAYGROUND_ENABLED` |

Signing per mount: imgproxy mounts get the imgproxy path signature and the query, wsrv and cloudflare mounts `xi_sig` (both from `XI_SIGNING_KEYS`), thumbor mounts the thumbor signature (`XI_DIALECTS_THUMBOR_KEYS` or the mount's `keys`). Without a key the URL is returned unsigned (`insecure/`, `unsafe/`). The other dialects (ImageKit, Cloudinary, Cloudflare Images, bunny, AWS SIH, the simple family) are built free-form and returned unsigned: the playground does not hold their provider keys.

Endpoints under the mount path: the page (`/`), its assets (`/assets/<version>/…`, immutable), `GET /api/meta` (dialect mounts, each listed once with every prefix it answers on, option catalogues, preset names, source names and types, signing mode, output formats, samples), `POST /api/sign` (optionally on one of the mount's other prefixes), `GET /api/render?u=…` (runs the public handler in-process so the page can show the result and its headers from its own origin), `POST /api/translate` (the transform of a URL in every dialect, with what each cannot express or why it cannot write the source; an http(s) source under the `base_url` of a mount's http source is written as a key on it, which ImageKit, Bunny and AWS SIH need), `POST /api/upload` (with `XI_PLAYGROUND_UPLOADS`) `GET /api/sources/<name>/list?prefix=` (one directory of an `fs` source, at most 500 entries, dot files skipped), and with video transformations on (WP-127) `POST /api/probe` (a source's container, duration and streams, read with FFmpeg, audio files included) and `POST /api/probe/result` (the same for a result body; two probes at a time, more answer 429). `api/meta` then has `video_transform` (the codecs and containers this build writes, the output limits) and `api/sign` signs `/video` paths of imgproxy mounts. See [OPENAPI.md](OPENAPI.md).

Samples: the built-in samples are copies of repository fixtures embedded in the binary (`playground://samples/<file>`, readable only through `api/render`). The public gallery (`internal/playground/samples.json`: one or two real-world files per input format, with licence and attribution) is fetched through the http sources like any URL: with no http source allowing its host (or `XI_DEV_MODE`), an entry is shown disabled.

Security:

- **Keys never reach the browser.** `api/meta` reports only the signing mode and each mount's scheme; `api/sign` computes the signature on the server with the configured keys and returns the URL. Source entries list names and types only (no bucket credentials, headers, base URLs or allowlists).
- **Anyone who can reach the playground can sign URLs.** On the admin listener (default `127.0.0.1:9090`) that is whoever can reach the admin port; keep it private. With `XI_PLAYGROUND_PUBLIC=true` and signing keys set, the public playground is a signing oracle for the whole internet, and the server logs a warning at startup.
- `POST api/sign`, `api/translate`, `api/upload` and `api/probe` are protected against cross-site requests (`http.CrossOriginProtection`: `Sec-Fetch-Site` / `Origin` checks); sign and translate only accept a small JSON body.
- `playground://` sources exist only for requests made through `api/render`: a public request naming one gets 404. Upload IDs are 128-bit random values; the rendered result and the original may still be cached (result and origin caches) under that ID for their TTLs.
- `api/sources/<name>/list` lists only `fs` sources, inside the source's root and prefix jail (`os.Root`), never following symlinks out of it.
- On the public listener the playground path (`XI_SERVER_PATH_PREFIX` + `XI_PLAYGROUND_PATH`) is always reserved: it answers 404 unless `XI_PLAYGROUND_PUBLIC=true`, so it never reaches a dialect mounted at `/`.
- `api/render` goes through the public handler with every public-listener check (unsigned policy, source allowlist, SSRF rules, limits); it grants nothing a public request would not get.
- Every playground response carries `Content-Security-Policy` (`default-src 'self'`, `frame-ancestors 'none'`), `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.

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
| `IMGPROXY_ALLOWED_SOURCES` | `XI_SOURCES`, `XI_SOURCES_HTTP_TYPE`, `XI_SOURCES_HTTP_ALLOW`, `XI_SOURCES_LOCAL_PREFIX` | `http(s)://host[:port][/path/]` patterns become the allow list of an http source named `http` (scheme kept, so `https://` never allows `http://`; without a port only the scheme's default port, as imgproxy's prefix match allows; `*` in the host kept as a host glob, the path kept as a directory prefix, `*` in the path rejected). `s3://`, `gs://`, `abs://` patterns restrict the buckets of the matching source (bucket names only). `local:///dir/` becomes the `local` source prefix. Unset: no http source is created (imgproxy would fetch any URL), a startup warning says so |
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
| `IMGPROXY_MAX_ANIMATION_FRAME_RESOLUTION` | `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` | Megapixels (float) → pixels; `0` turns the per-frame limit off as in imgproxy (animations count all frames against `XI_LIMITS_MAX_SRC_PIXELS`). Unset keeps the xtandard-image default of 16 MP, where imgproxy defaults to off |
| `IMGPROXY_MAX_RESULT_DIMENSION` | `XI_LIMITS_MAX_RESULT_DIMENSION` | `0` (unlimited) keeps the default |
| `IMGPROXY_ENABLE_VIDEO_THUMBNAILS` | `XI_VIDEO_ENABLED` | Same; `true` fails startup on an image without FFmpeg (slim) |
| `IMGPROXY_VIDEO_THUMBNAIL_SECOND` | `XI_VIDEO_THUMBNAIL_SECOND` | Same seconds; imgproxy's default is 1, xtandard-image's is best-frame selection (`auto`) |
| `IMGPROXY_VIDEO_THUMBNAIL_KEYFRAMES` | `XI_VIDEO_THUMBNAIL_KEYFRAMES` | Same |
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
| `IMGPROXY_USE_LINEAR_COLORSPACE` | `XI_PROCESSING_LINEAR_RESIZE` | Translated, but not implemented yet (see the variable) |
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
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
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

Sections 4 to 6 are short starting points. Recipes for every platform (Compose, Swarm, Dokploy, Coolify, Fly.io, Cloud Run, ECS, Azure Container Apps and infrastructure as code) are in [DEPLOY.md](DEPLOY.md); sizing, autoscaling, the CDN in front, cache tiers and observability are in [OPERATIONS.md](OPERATIONS.md).

## 4. Dokploy

Create an **Application** → Docker image `ghcr.io/xantiagoma/xtandard-image:0.1.1-slim`, port 8080, domain via Traefik, and set environment:

```
XI_SIGNING_KEYS=<hex>
XI_SIGNING_SALTS=<hex>
XI_SOURCES=origin
XI_SOURCES_ORIGIN_TYPE=http
XI_SOURCES_ORIGIN_ALLOW=cdn.example.com
XI_PROCESSING_MEMORY_BUDGET=1GiB
```

Health check path `/healthz` (the image also declares a Docker `HEALTHCHECK`). Scale replicas freely: the service is stateless; put a CDN in front for caching ([OPERATIONS.md §4](OPERATIONS.md#4-a-cdn-in-front)). An environment with several sources (S3, R2, B2, RustFS, SFTP, http) is [`docs/examples/multi-source.env`](examples/multi-source.env) ([SOURCES.md §5.1](SOURCES.md#51-environment-variables-only-dokploy-coolify-docker-run)).

Replicas, rolling updates and a Swarm stack for Dokploy's "Stack" type: [DEPLOY.md "Dokploy"](DEPLOY.md#dokploy).

## 5. Coolify (docker-compose)

```yaml
services:
  xtandard-image:
    image: ghcr.io/xantiagoma/xtandard-image:0.1.1-slim
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

Several sources with their credentials in Docker secrets (`_FILE` variables or `{file: …}` in a mounted config file): [SOURCES.md §5.2–5.3](SOURCES.md#52-docker-compose-with-secrets-also-dokploy-and-coolify-compose).

Coolify's scaling limits and the other platforms: [DEPLOY.md "Coolify"](DEPLOY.md#coolify).

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
          image: ghcr.io/xantiagoma/xtandard-image:0.1.1-slim
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

Set CPU and memory limits: the worker count follows the CPU limit (GOMAXPROCS), and the server sets `GOMEMLIMIT` to 30 % of the memory limit itself ([OPERATIONS.md §1.3](OPERATIONS.md#13-memory)). A deployment that runs video transformations (`XI_ROLE=video` or `all` with `XI_VIDEO_TRANSFORM_ENABLED`) also needs a disk-backed volume for its spool files (an `emptyDir` without `medium: Memory`, with a `sizeLimit` above `XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES`) at `XI_VIDEO_TRANSFORM_SPOOL_DIR`: [RELEASE.md "Running read-only"](RELEASE.md#running-read-only). A ConfigMap with several sources and a Secret mounted as files (`{file: …}`, `_FILE`, `secretKeyRef`, IRSA): [SOURCES.md §5.4](SOURCES.md#54-kubernetes) and [`docs/examples/k8s/`](examples/k8s/).

The complete manifests are [`deploy/kubernetes/`](../deploy/kubernetes/) (Kustomize: base and `image`, `video`, `keda`, `monitoring` overlays) and the Helm chart [`deploy/helm/xtandard-image/`](../deploy/helm/xtandard-image/) (image and video fleets, HPA or KEDA, ServiceMonitor, PrometheusRule, Grafana dashboard). Autoscaling on the queue instead of CPU, drain timing and monitoring: [OPERATIONS.md §3](OPERATIONS.md#3-autoscaling) and [§6](OPERATIONS.md#6-observability).
