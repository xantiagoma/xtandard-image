# Sources

A **source** is a named place xtandard-image may read originals from: an HTTP origin, an S3-compatible bucket, Google Cloud Storage, Azure Blob Storage, a local directory, or an FTP/FTPS/SFTP server. The configured sources are the allowlist. A URL that no source allows answers 403, and with no source at all the server refuses to start (unless `XI_DEV_MODE=true`).

You can configure any number of sources, several of the same type included: AWS S3, Cloudflare R2, Backblaze B2 and a RustFS bucket next to each other, two local directories, a partner's SFTP server, and http sources with different allowlists and headers. This page explains how a URL picks one of them, gives a recipe per provider and complete examples for Docker Compose, Dokploy, Coolify and Kubernetes.

Every example on this page and in [`docs/examples/`](examples/) is loaded by a test (`internal/config/examples_test.go`) with the real configuration loader, and the resolution table in [§2.1](#21-worked-example) is checked against the resolver (`internal/app/sources_doc_test.go`).

Related: every variable is listed in [CONFIG.md](CONFIG.md#sources-and-fetching), the URL grammar is in [URL-DIALECTS.md](URL-DIALECTS.md#31-grammar), and the design is in [ARCHITECTURE.md §6](ARCHITECTURE.md#6-source-abstraction).

## 1. Concepts

- **Name.** Each source has a name: 1–63 characters of `a-z`, `0-9` and `-` (`r2`, `partner-sftp`). In environment variables the name is upper-cased and `-` becomes `_`: source `partner-sftp` is configured with `XI_SOURCES_PARTNER_SFTP_*`.
- **Type.** `http`, `s3` (AWS and every S3-compatible store), `gcs`, `azure`, `fs`, `ftp` or `sftp`.
- **What it allows.** An `http` source allows the URLs matched by its `allow` patterns and everything under its `base_url`. An `s3`, `gcs` or `azure` source allows the buckets (containers) in `buckets` plus its default `bucket`. An `ftp` or `sftp` source allows its own `host` and `port`. An `fs` source allows everything under its `root`.
- **Prefix jail.** `prefix` (every type except `http`) restricts a source to the keys under it. Relative keys get it prepended; absolute references (`s3://bucket/key`) must already start with it.
- **Default bucket.** An `s3`, `gcs` or `azure` source with a `bucket` can take relative keys (`xi://r2/cat.jpg`, or a key on a mount bound to it). Without one, only `s3://bucket/key`-style references reach it.
- **Mount source.** A URL dialect mount (the imgproxy mount at `/`, an imgix host, a Cloudinary cloud, …) can be bound to a source: `dialects.<dialect>.source`. Relative keys on that mount are read from it, and scheme references try it first.

A request names its source with one of these **source strings** (in the imgproxy dialect, after `plain/`, base64-encoded or `enc/`; other dialects use their own field, [URL-DIALECTS.md](URL-DIALECTS.md)):

| Source string | Goes to |
|---|---|
| `xi://<name>/<key>` | The named source, whatever its type. The key is relative to it (`prefix` and default `bucket` applied; an `http` source joins it to `base_url`, or takes an absolute `https://…` key, [URL-DIALECTS §3.1](URL-DIALECTS.md#31-grammar)) |
| `https://…`, `http://…` | An `http` source whose `allow` or `base_url` matches |
| `s3://<bucket>/<key>[?versionId=…]` | An `s3` source allowing the bucket |
| `gs://<bucket>/<key>[?generation=…]` | A `gcs` source allowing the bucket |
| `abs://<container>/<blob>`, `az://…` | An `azure` source allowing the container |
| `local:///<path>`, `local:/<path>`, `file:///<path>` | An `fs` source (the path is relative to its `root`) |
| `ftp://<host>[:port]/<path>`, `sftp://<host>[:port]/<path>` | The `ftp` / `sftp` source with that host and port |
| `<key>` (no scheme) | The mount's source |

## 2. How a URL picks a source

The resolver (`internal/source`, `Registry.Resolve`) applies these rules in order; the first that applies decides:

1. **`xi://<name>/<key>`** goes to the source called `<name>`, and only to it. An unknown name is 403 `unknown source "<name>"`.
2. **A scheme reference** (`s3://`, `gs://`, `abs://`/`az://`, `local://`/`file://`, `ftp://`, `sftp://`) goes to the first source of the matching type that allows it: its buckets match the bucket (or its host and port match the server), and the key lies inside its prefix jail. The candidates are tried **the mount's source first, then every source of that type in alphabetical order of name**. Nothing matching is 403 `no source allows this s3:// reference` (with the scheme used).
3. **An `http(s)://` URL** goes to the first `http` source, in the same order, whose `allow` patterns or `base_url` match. Nothing matching is 403 `no source allows this url`, except in dev mode, where an implicit unrestricted http source takes it (the SSRF rules still apply).
4. **A relative key** (no scheme) goes to the mount's source. A mount without a source answers 403 `forbidden`.
5. **`playground://…`** is the playground's in-memory store, readable only from the playground itself ([CONFIG.md](CONFIG.md#playground)).

The order in rule 2 and 3 is alphabetical by name because neither a YAML map nor `XI_SOURCES` keeps an order: `aws`, `b2`, `r2`, `rustfs` are tried in that order whatever order you wrote them in. Only the mount's own source jumps the queue.

### 2.1 Worked example

With [`docs/examples/config.multi-source.yaml`](examples/config.multi-source.yaml) (the sources `aws`, `r2`, `b2`, `rustfs`, `gcs`, `azure`, `photos`, `uploads`, `partner-sftp`, `legacy-ftp`, `web` and `cms`; see [§5](#5-several-providers-at-once)), these source strings resolve as follows. "Mount's source" is the source of the dialect mount the request arrived on (`r2` for the imgproxy mount of that file, `—` for a mount without one). The result is written as the server logs it: `<source>:<bucket>/<key>` or `<source>:<url>`.

<!-- resolution-table: docs/examples/config.multi-source.yaml -->
| Source string | Mount's source | Resolves to |
|---|---|---|
| `cat.jpg` | `r2` | `r2:assets/cat.jpg` |
| `cat.jpg` | — | 403 `forbidden` |
| `s3://assets/cat.jpg` | `r2` | `r2:assets/cat.jpg` |
| `s3://assets/public/cat.jpg` | — | `r2:assets/public/cat.jpg` |
| `s3://assets/public/cat.jpg` | `rustfs` | `rustfs:assets/public/cat.jpg` |
| `s3://assets/cat.jpg` | `rustfs` | `r2:assets/cat.jpg` |
| `xi://rustfs/cat.jpg` | `r2` | `rustfs:assets/public/cat.jpg` |
| `s3://acme-tenant-42/logo.png` | `r2` | `aws:acme-tenant-42/logo.png` |
| `s3://acme-archive/2019/a.jpg` | — | `b2:acme-archive/2019/a.jpg` |
| `xi://b2/2019/a.jpg` | — | `b2:acme-archive/2019/a.jpg` |
| `s3://other-bucket/a.jpg` | `r2` | 403 `no source allows this s3:// reference` |
| `s3:///a.jpg` | `r2` | 400 `invalid url` |
| `gs://acme-gcs-images/a.jpg` | — | `gcs:acme-gcs-images/a.jpg` |
| `abs://media/a.jpg` | — | `azure:media/a.jpg` |
| `az://media/a.jpg` | — | `azure:media/a.jpg` |
| `local:///a.jpg` | — | `photos:a.jpg` |
| `local:///public/a.jpg` | — | `photos:public/a.jpg` |
| `xi://uploads/a.jpg` | — | `uploads:public/a.jpg` |
| `xi://photos/../secret.jpg` | — | 403 `forbidden` |
| `sftp://sftp.partner.example:2222/x.jpg` | — | `partner-sftp:x.jpg` |
| `sftp://sftp.partner.example/x.jpg` | — | 403 `no source allows this sftp:// reference` |
| `ftp://ftp.example.com/a.jpg` | — | `legacy-ftp:a.jpg` |
| `https://images.unsplash.com/photo-1.jpg` | `r2` | `web:https://images.unsplash.com/photo-1.jpg` |
| `http://images.unsplash.com/photo-1.jpg` | `r2` | 403 `no source allows this url` |
| `https://example.com/a.jpg` | `r2` | 403 `no source allows this url` |
| `https://cms.example.com/media/a.jpg` | — | `cms:https://cms.example.com/media/a.jpg` |
| `xi://cms/2024/a.jpg` | — | `cms:https://cms.example.com/media/2024/a.jpg` |
| `xi://web/https://images.unsplash.com/photo-1.jpg` | — | `web:https://images.unsplash.com/photo-1.jpg` |
| `xi://web/photo-1.jpg` | — | 403 `source "web" has no base_url: the key must be an absolute http(s) url` |
| `xi://web/https://example.com/a.jpg` | — | 403 `source "web": host not in the source allowlist` |
| `xi://web/http://images.unsplash.com/a.jpg` | — | 403 `source "web": url scheme, port or path not in the source allowlist` |
| `xi://nope/a.jpg` | — | 403 `unknown source "nope"` |

Things to notice:

- `s3://assets/…` exists in both `r2` and `rustfs`. Without a mount preference it goes to `r2` (alphabetically first). A mount bound to `rustfs` gets `rustfs`, but only for keys inside its `public/` jail; `s3://assets/cat.jpg` falls through to `r2`.
- `local:///public/a.jpg` goes to `photos`, not `uploads`: `photos` has no prefix jail, so it allows every path and sorts first. `uploads` is reached with `xi://uploads/…`.
- `web` allows only `https://`, so the `http://` form of the same URL is refused.

### 2.2 Same bucket name in several providers

Bucket names are only unique inside one provider: `assets` can exist in R2, in B2 and in your RustFS. When two sources of one type allow the same bucket name, a scheme reference (`s3://assets/…`) always goes to the first one in the order above, so the other one is never reached that way. To address each one explicitly:

- Give every such source a default `bucket` and use **`xi://<name>/<key>`** (`xi://r2/cat.jpg`, `xi://rustfs/cat.jpg`). This is the only form that cannot be ambiguous; prefer it in generated URLs.
- Bind a dialect mount (a host or a prefix) to each source and use relative keys on it (the `imgix` mounts of the example: `images.example.com` → `r2`, `archive-images.example.com` → `b2`).
- Or keep the bucket lists disjoint, so each `s3://bucket/…` has exactly one candidate.

The same applies to two `fs` sources (a `local:///…` path goes to the first one whose jail contains it), and to two `http` sources whose patterns overlap: the bare URL uses the first one's headers, `xi://<name>/https://…` picks the one whose headers and allowlist you want.

`config validate` does not warn about overlaps; the resolution table above is the way to reason about them.

## 3. Configuring sources

### 3.1 Environment variables, file, or both

The same source can be written in the config file (YAML, JSON or TOML, [CONFIG.md §1](CONFIG.md#1-sources-of-configuration-and-precedence)) or in environment variables:

- `XI_SOURCES` lists the names defined in the environment, comma-separated (`XI_SOURCES=r2,b2,partner-sftp`). They are added to the names in the file's `sources` map.
- `XI_SOURCES_<NAME>_<FIELD>` sets one field: `sources.r2.endpoint` ↔ `XI_SOURCES_R2_ENDPOINT`. A variable wins over the file for that field, so a file can define the sources and the environment can adjust one of them.
- Lists are comma-separated (`XI_SOURCES_AWS_BUCKETS=acme-media,acme-tenant-*`) and replace the file's list. `headers` is a map: `XI_SOURCES_CMS_HEADERS=Authorization=Bearer abc,X-Tenant=acme` (a value cannot contain a comma in this form; use the file for such headers).
- A variable for a source that is neither in `XI_SOURCES` nor in the file is an error that says so (`source "r3" is not listed in XI_SOURCES or the config file`), like any unknown `XI_` variable.

### 3.2 Credentials

Pick whichever fits the platform; they combine freely:

| Method | Form | Use it for |
|---|---|---|
| Plain variable | `XI_SOURCES_R2_SECRET_ACCESS_KEY=…` | Platforms that store environment variables encrypted (Dokploy, Coolify, a CI secret store) |
| `_FILE` variable | `XI_SOURCES_R2_SECRET_ACCESS_KEY_FILE=/run/secrets/r2_secret_access_key` | Docker secrets and Kubernetes Secret volumes. **Every** variable has a `_FILE` form, including dynamic ones and non-secret fields (`XI_SOURCES_R2_ACCESS_KEY_ID_FILE`) |
| `{file: …}` in the config file | `secret_access_key: { file: /run/secrets/r2_secret_access_key }` | Keeping the whole configuration in a file that holds no secret (a ConfigMap, a git repository) |
| Provider identity | no key at all | AWS (IRSA, EKS Pod Identity, ECS task role, instance profile, `AWS_*` variables), GCS (Application Default Credentials, GKE Workload Identity), Azure (`DefaultAzureCredential`: managed identity, workload identity, `AZURE_*` variables) |

Rules: a file is read once at startup, must be an absolute path and at most 64 KiB, and its trailing newline is trimmed. Setting both `X` and `X_FILE` is an error. `{file: …}` is accepted only by secret-typed fields: `headers` values, `secret_access_key`, `credentials_json`, `account_key`, `connection_string`, `sas_token`, `password`, `private_key`, `private_key_passphrase` and `pdf_password`. The other fields (`access_key_id`, `host_key`, …) are not secret; write them in the file, or use the `_FILE` variable if you keep them next to the secret.

Secret-typed fields print as `[REDACTED]` in `config print`, in logs and in errors, and never reach clients or the playground (it lists source names and types only). Logged source references drop http query strings (`?REDACTED`), where origins often carry tokens.

### 3.3 Reference per type

The environment variable of each key is `XI_SOURCES_<NAME>_<KEY>` in upper case (and `_FILE` for a file). "—" means no default. Which keys apply depends on the type: a key set for another type is a startup error (`sources.web.bucket: does not apply to type http`).

**http**

| Key | Required | Default | Meaning |
|---|---|---|---|
| `allow` | `allow` or `base_url` | — | Patterns `[http://\|https://]host-glob[:port][/path/prefix/]`. No scheme allows both; a port pins it (it must also be in `XI_FETCH_ALLOWED_PORTS`, default `80,443`); a path prefix is a directory (`/img/` does not match `/img-private/`). `*.example.com` matches any depth of subdomain but not `example.com`; `img-*.example.com` matches within one label; `*` alone matches every host |
| `base_url` | `allow` or `base_url` | — | Absolute http(s) URL without a query. Relative keys are joined to it and everything under it is allowed |
| `headers` | no | — | Extra request headers (`Authorization`, API keys); secret values |

**s3** (AWS S3, Cloudflare R2, Backblaze B2, Wasabi, DigitalOcean Spaces, RustFS, MinIO, Ceph RGW, Garage, …)

| Key | Required | Default | Meaning |
|---|---|---|---|
| `buckets` | `buckets` or `bucket` | — | Allowed bucket names; `*` is a wildcard (`tenant-*`). Case-sensitive |
| `bucket` | no | — | Default bucket for relative keys; allowed implicitly; must be in `buckets` when both are set |
| `prefix` | no | — | Key prefix jail |
| `endpoint` | no | AWS | Service URL of an S3-compatible store |
| `region` | no | SDK chain, else `us-east-1` with an endpoint | Signing region (`auto` for R2) |
| `force_path_style` | no | `false` | `https://endpoint/bucket/key` instead of `https://bucket.endpoint/key`; needed by RustFS and MinIO |
| `access_key_id`, `secret_access_key` | no | AWS default chain | Static keys |
| `role_arn` | no | — | Role assumed with STS on top of the credentials |

**gcs**

| Key | Required | Default | Meaning |
|---|---|---|---|
| `buckets`, `bucket`, `prefix` | as s3 | — | As s3 |
| `credentials_json` | no | Application Default Credentials | Service-account key JSON (secret) |
| `endpoint` | no | Google | Emulator URL (fake-gcs-server) |

**azure**

| Key | Required | Default | Meaning |
|---|---|---|---|
| `buckets`, `bucket`, `prefix` | as s3 | — | Containers, as s3 buckets |
| `account` | unless `connection_string` or `endpoint` | — | Storage account name |
| `connection_string` | no | — | Wins over every other credential; carries account and endpoint |
| `account_key` | no | — | Shared key (needs `account`) |
| `sas_token` | no | — | Account or container SAS, with or without the leading `?` |
| `endpoint` | no | `https://<account>.blob.core.windows.net/` | Service URL (Azurite: `http://127.0.0.1:10000/devstoreaccount1`) |
| (none of the credentials) | | `DefaultAzureCredential` | Managed identity, workload identity, `AZURE_*` variables |

**fs**

| Key | Required | Default | Meaning |
|---|---|---|---|
| `root` | yes | — | Absolute directory. Reads are jailed in it with `os.Root`: `..` and symlinks cannot leave it |
| `prefix` | no | — | Sub-directory jail inside the root |
| `follow_symlinks` | no | `false` | Follow symlinks whose target stays inside the root |

**ftp** and **sftp**

| Key | Required | Default | Meaning |
|---|---|---|---|
| `host` | yes | — | Server host name or IP (no port) |
| `port` | no | `21` / `22` | Server port; `ftp://host:port/…` must name the same port |
| `user` | sftp: yes | ftp: `anonymous` | Login |
| `password` | no | — | Password (secret) |
| `root` | no | `/` | Server directory keys are relative to |
| `prefix` | no | — | Key prefix jail |
| `tls` | no | `false` | ftp only: explicit TLS (`AUTH TLS`, `PROT P`); passive mode |
| `private_key`, `private_key_passphrase` | sftp: `password` or `private_key` | — | PEM or OpenSSH private key (secret) and its passphrase |
| `host_key` | sftp: yes | — | The server's public key: `authorized_keys` or `known_hosts` lines, one per line, several for rotation. There is no trust on first use |
| `pool_size` | no | `4` | Open connections to the server |
| `dial_timeout` | no | `10s` | Connect, handshake and login |
| `idle_timeout` | no | `1m` | Idle pooled connections are closed after it |

**Every type**

| Key | Required | Default | Meaning |
|---|---|---|---|
| `pdf_password` | no | — | Default password for password-protected PDFs of this source (secret; at most 1024 bytes, no control characters), used when a request carries no `pdf_password` of its own. A request's encrypted password wins ([URL-DIALECTS §3.7](URL-DIALECTS.md#37-password-protected-pdfs-wp-115)). Every request for the source opens its PDFs with it, signed or not, so put documents that need different passwords (or none) in different sources. Changing it does not purge renditions already in the result cache |

## 4. Provider recipes

Each block is a complete, valid configuration on its own; combine the `sources` entries you need. Secrets are `{file: …}` references (see [§3.2](#32-credentials) for the alternatives).

### AWS S3

Least privilege is `s3:GetObject` on the allowed buckets (`s3:GetObjectVersion` too if URLs carry `?versionId=`). No keys: the AWS default chain signs the requests (IRSA or EKS Pod Identity on Kubernetes, the task role on ECS, the instance profile on EC2, or `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`).

```yaml
sources:
  aws:
    type: s3
    region: eu-west-1
    buckets: [acme-media, "acme-tenant-*"]
    bucket: acme-media
    # Optional: read through another account's role.
    role_arn: arn:aws:iam::111122223333:role/xtandard-image-read
```

With static keys instead:

```yaml
sources:
  aws:
    type: s3
    region: eu-west-1
    buckets: [acme-media]
    bucket: acme-media
    access_key_id: AKIAIOSFODNN7EXAMPLE
    secret_access_key: { file: /run/secrets/aws_secret_access_key }
```

### Cloudflare R2

Create an R2 API token with **Object Read only** on the bucket; it gives an access key id and a secret. `region` is `auto`. For a bucket in the EU jurisdiction the endpoint is `https://ACCOUNT_ID.eu.r2.cloudflarestorage.com`.

```yaml
sources:
  r2:
    type: s3
    endpoint: https://ACCOUNT_ID.r2.cloudflarestorage.com
    region: auto
    buckets: [assets]
    bucket: assets
    access_key_id: R2_ACCESS_KEY_ID
    secret_access_key: { file: /run/secrets/r2_secret_access_key }
```

### Backblaze B2

Use an application key restricted to the bucket, read-only. The endpoint and region are shown on the bucket's page (`s3.<region>.backblazeb2.com`).

```yaml
sources:
  b2:
    type: s3
    endpoint: https://s3.us-west-004.backblazeb2.com
    region: us-west-004
    buckets: [acme-archive]
    bucket: acme-archive
    access_key_id: B2_KEY_ID
    secret_access_key: { file: /run/secrets/b2_application_key }
```

### Wasabi

```yaml
sources:
  wasabi:
    type: s3
    endpoint: https://s3.eu-central-1.wasabisys.com
    region: eu-central-1
    buckets: [acme-wasabi]
    bucket: acme-wasabi
    access_key_id: WASABI_ACCESS_KEY
    secret_access_key: { file: /run/secrets/wasabi_secret_key }
```

### DigitalOcean Spaces

The endpoint is the Space's region (`fra1`, `nyc3`, …). Spaces ignores the signing region; the SDK needs one, so keep `us-east-1`.

```yaml
sources:
  spaces:
    type: s3
    endpoint: https://fra1.digitaloceanspaces.com
    region: us-east-1
    buckets: [acme-space]
    bucket: acme-space
    access_key_id: DO_SPACES_KEY
    secret_access_key: { file: /run/secrets/do_spaces_secret }
```

### RustFS, MinIO and other self-hosted S3

Self-hosted stores usually need path-style addressing (`force_path_style`). An endpoint on a private network is fine: the SSRF policy (`XI_FETCH_*`) applies to `http` sources, not to the endpoints you configure for object stores.

```yaml
sources:
  rustfs:
    type: s3
    endpoint: http://rustfs:9000
    region: us-east-1
    force_path_style: true
    buckets: [assets]
    bucket: assets
    prefix: public/
    access_key_id: xi-reader
    secret_access_key: { file: /run/secrets/rustfs_secret_key }
```

### Google Cloud Storage

Grant the service account `roles/storage.objectViewer` on the bucket. On GKE with Workload Identity, or anywhere Application Default Credentials work, drop `credentials_json`.

```yaml
sources:
  gcs:
    type: gcs
    buckets: [acme-gcs-images]
    bucket: acme-gcs-images
    credentials_json: { file: /run/secrets/gcs_service_account.json }
```

### Azure Blob Storage

Four ways to authenticate, in precedence order: connection string, account key, SAS token, and `DefaultAzureCredential` (managed identity; give it **Storage Blob Data Reader**). A container SAS with read permission is the narrowest.

```yaml
sources:
  azure-sas:
    type: azure
    account: acmeimages
    buckets: [media]
    bucket: media
    sas_token: { file: /run/secrets/azure_sas_token }
  azure-key:
    type: azure
    account: acmeimages
    buckets: [archive]
    bucket: archive
    account_key: { file: /run/secrets/azure_account_key }
  azure-identity:
    type: azure
    account: acmeprivate
    buckets: [originals]
    bucket: originals
  azurite:
    type: azure
    connection_string: { file: /run/secrets/azurite_connection_string }
    buckets: [test]
```

### Local directories

Each `fs` source is jailed in its `root` (`..` and escaping symlinks are refused), and `prefix` narrows it further. Mount the directories read-only into the container.

```yaml
sources:
  photos:
    type: fs
    root: /data/photos
  uploads:
    type: fs
    root: /data/uploads
    prefix: public/
    follow_symlinks: true
```

### FTP, FTPS and SFTP

For SFTP the host key is mandatory; get it from a trusted network with `ssh-keyscan -p 2222 sftp.partner.example` and paste the lines (several lines allow a key rotation). FTP supports explicit TLS (`tls: true`) in passive mode; plain FTP sends the password in clear text.

```yaml
sources:
  partner-sftp:
    type: sftp
    host: sftp.partner.example
    port: 2222
    user: xi
    private_key: { file: /run/secrets/partner_sftp_key }
    host_key: |
      sftp.partner.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPleaseReplaceWithTheOutputOfSshKeyscanKey01
      sftp.partner.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPleaseReplaceWithTheOutputOfSshKeyscanKey02
    root: /outgoing/images
    pool_size: 8
  legacy-ftp:
    type: ftp
    host: ftp.example.com
    tls: true
    user: images
    password: { file: /run/secrets/legacy_ftp_password }
    dial_timeout: 5s
```

### HTTP origins

Allowlist the hosts, optionally pinned to `https://` and a path prefix. `base_url` turns relative keys into URLs and allows everything under it. Headers are sent on every request of that source, so two sources can point at the same host with different credentials and be picked with `xi://<name>/…`.

```yaml
sources:
  web:
    type: http
    allow:
      - https://images.unsplash.com
      - https://*.cloudfront.net/public/
      - "*.example.org"
  cms:
    type: http
    base_url: https://cms.example.com/media/
    headers:
      Authorization: { file: /run/secrets/cms_authorization }
      X-Api-Version: "2"
```

An origin on a private address (another container, a Kubernetes service) is blocked by the SSRF policy until you allow its network: `XI_FETCH_ALLOWED_NETWORKS=10.20.0.0/16` (only that range) or, for a trusted network, `XI_FETCH_ALLOW_PRIVATE_NETWORKS=true`. A port other than 80 or 443 must be added to `XI_FETCH_ALLOWED_PORTS`.

```yaml
sources:
  internal:
    type: http
    base_url: http://thumbnails.internal.svc.cluster.local:8081/
fetch:
  allowed_networks: [10.96.0.0/12]
  allowed_ports: [80, 443, 8081]
```

## 5. Several providers at once

### 5.1 Environment variables only (Dokploy, Coolify, `docker run`)

[`docs/examples/multi-source.env`](examples/multi-source.env): paste it into the environment tab of a Dokploy or Coolify application, or pass it with `docker run --env-file`. Both platforms store the values encrypted; replace every `<hex>` with `openssl rand -hex 32` and every `CHANGE_ME` with the credential.

```dotenv docs/examples/multi-source.env
# Environment-only configuration of several sources (docs/SOURCES.md), for
# the environment tab of a Dokploy or Coolify application, `docker run
# --env-file`, or a .env file. A test in internal/config loads it.
#
# Replace every <hex> with the output of `openssl rand -hex 32` and every
# CHANGE_ME with the real credential. Prefer the _FILE variants (see
# compose.multi-source.yml) when the platform can mount secret files.

XI_SIGNING_KEYS=<hex>
XI_SIGNING_SALTS=<hex>

# Source names, comma-separated. A "-" in a name becomes "_" in variables.
XI_SOURCES=aws,r2,b2,rustfs,photos,partner-sftp,web,cms

# AWS S3: no keys, so the AWS default chain (task role, instance profile,
# AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY) is used.
XI_SOURCES_AWS_TYPE=s3
XI_SOURCES_AWS_REGION=eu-west-1
XI_SOURCES_AWS_BUCKETS=acme-media,acme-tenant-*
XI_SOURCES_AWS_BUCKET=acme-media

# Cloudflare R2
XI_SOURCES_R2_TYPE=s3
XI_SOURCES_R2_ENDPOINT=https://ACCOUNT_ID.r2.cloudflarestorage.com
XI_SOURCES_R2_REGION=auto
XI_SOURCES_R2_BUCKETS=assets
XI_SOURCES_R2_BUCKET=assets
XI_SOURCES_R2_ACCESS_KEY_ID=CHANGE_ME
XI_SOURCES_R2_SECRET_ACCESS_KEY=CHANGE_ME

# Backblaze B2
XI_SOURCES_B2_TYPE=s3
XI_SOURCES_B2_ENDPOINT=https://s3.us-west-004.backblazeb2.com
XI_SOURCES_B2_REGION=us-west-004
XI_SOURCES_B2_BUCKETS=acme-archive
XI_SOURCES_B2_BUCKET=acme-archive
XI_SOURCES_B2_ACCESS_KEY_ID=CHANGE_ME
XI_SOURCES_B2_SECRET_ACCESS_KEY=CHANGE_ME

# RustFS / MinIO (path-style); same bucket name as r2
XI_SOURCES_RUSTFS_TYPE=s3
XI_SOURCES_RUSTFS_ENDPOINT=http://rustfs:9000
XI_SOURCES_RUSTFS_REGION=us-east-1
XI_SOURCES_RUSTFS_FORCE_PATH_STYLE=true
XI_SOURCES_RUSTFS_BUCKETS=assets
XI_SOURCES_RUSTFS_BUCKET=assets
XI_SOURCES_RUSTFS_PREFIX=public/
XI_SOURCES_RUSTFS_ACCESS_KEY_ID=xi-reader
XI_SOURCES_RUSTFS_SECRET_ACCESS_KEY=CHANGE_ME

# A mounted directory
XI_SOURCES_PHOTOS_TYPE=fs
XI_SOURCES_PHOTOS_ROOT=/data/photos

# SFTP with a pinned host key (ssh-keyscan -p 2222 sftp.partner.example)
XI_SOURCES_PARTNER_SFTP_TYPE=sftp
XI_SOURCES_PARTNER_SFTP_HOST=sftp.partner.example
XI_SOURCES_PARTNER_SFTP_PORT=2222
XI_SOURCES_PARTNER_SFTP_USER=xi
XI_SOURCES_PARTNER_SFTP_PASSWORD=CHANGE_ME
XI_SOURCES_PARTNER_SFTP_HOST_KEY=sftp.partner.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPleaseReplaceWithTheOutputOfSshKeyscanKey01
XI_SOURCES_PARTNER_SFTP_ROOT=/outgoing/images

# Public web images, https only
XI_SOURCES_WEB_TYPE=http
XI_SOURCES_WEB_ALLOW=https://images.unsplash.com,https://upload.wikimedia.org/wikipedia/commons/

# A private CMS with an auth header (map syntax: Name=value, comma-separated)
XI_SOURCES_CMS_TYPE=http
XI_SOURCES_CMS_BASE_URL=https://cms.example.com/media/
XI_SOURCES_CMS_HEADERS=Authorization=Bearer CHANGE_ME

# Relative imgproxy sources read r2
XI_DIALECTS_IMGPROXY_SOURCE=r2
```

To keep secrets out of the environment on these platforms, mount each one as a file (Dokploy: the service's *Advanced → Mounts → File Mount*; Coolify: *Persistent Storage → File Mount*) and point the `_FILE` variant at it, for example `XI_SOURCES_R2_SECRET_ACCESS_KEY_FILE=/run/secrets/r2_secret_access_key`.

### 5.2 Docker Compose with secrets (also Dokploy and Coolify compose)

[`docs/examples/compose.multi-source.yml`](examples/compose.multi-source.yml) configures the sources in `environment` and reads every credential from a Docker secret through its `_FILE` variable. Dokploy ("Docker Compose" service) and Coolify (compose resource) accept it as is: they fill the `${…}` variables from their environment tab. The container runs as uid 65532, so make the secret files readable (`chmod 0444 secrets/*`); outside Swarm, Compose bind-mounts them as they are.

```yaml docs/examples/compose.multi-source.yml
# Several sources configured with environment variables, credentials from
# Docker secrets through the _FILE variants (docs/SOURCES.md). Works with
# `docker compose up`, a Dokploy "Docker Compose" service and a Coolify
# compose resource. A test in internal/config loads this environment.
#
# Create the secret files next to this file first, one value each:
#   mkdir -p secrets && chmod 700 secrets
#   openssl rand -hex 32 > secrets/xi_signing_key
#   openssl rand -hex 32 > secrets/xi_signing_salt
#   printf %s "$R2_SECRET" > secrets/r2_secret_access_key   # etc.
services:
  xtandard-image:
    image: ${XI_IMAGE:-ghcr.io/xantiagoma/xtandard-image:slim}
    restart: unless-stopped
    ports:
      - "8080:8080"
      # Admin listener (metrics, /openapi.json, /docs): loopback only.
      - "127.0.0.1:9090:9090"
    environment:
      XI_ADMIN_ADDR: ":9090"
      XI_ADMIN_DOCS: "true"
      XI_SIGNING_KEYS_FILE: /run/secrets/xi_signing_key
      XI_SIGNING_SALTS_FILE: /run/secrets/xi_signing_salt

      XI_SOURCES: aws,r2,b2,photos,web

      # AWS S3 with static keys (on EC2/ECS/EKS drop them and use the role)
      XI_SOURCES_AWS_TYPE: s3
      XI_SOURCES_AWS_REGION: eu-west-1
      XI_SOURCES_AWS_BUCKETS: acme-media
      XI_SOURCES_AWS_BUCKET: acme-media
      XI_SOURCES_AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID:?set AWS_ACCESS_KEY_ID}
      XI_SOURCES_AWS_SECRET_ACCESS_KEY_FILE: /run/secrets/aws_secret_access_key

      # Cloudflare R2
      XI_SOURCES_R2_TYPE: s3
      XI_SOURCES_R2_ENDPOINT: https://${R2_ACCOUNT_ID:?set R2_ACCOUNT_ID}.r2.cloudflarestorage.com
      XI_SOURCES_R2_REGION: auto
      XI_SOURCES_R2_BUCKETS: assets
      XI_SOURCES_R2_BUCKET: assets
      XI_SOURCES_R2_ACCESS_KEY_ID: ${R2_ACCESS_KEY_ID:?set R2_ACCESS_KEY_ID}
      XI_SOURCES_R2_SECRET_ACCESS_KEY_FILE: /run/secrets/r2_secret_access_key

      # Backblaze B2
      XI_SOURCES_B2_TYPE: s3
      XI_SOURCES_B2_ENDPOINT: https://s3.us-west-004.backblazeb2.com
      XI_SOURCES_B2_REGION: us-west-004
      XI_SOURCES_B2_BUCKETS: acme-archive
      XI_SOURCES_B2_BUCKET: acme-archive
      XI_SOURCES_B2_ACCESS_KEY_ID: ${B2_KEY_ID:?set B2_KEY_ID}
      XI_SOURCES_B2_SECRET_ACCESS_KEY_FILE: /run/secrets/b2_application_key

      # Files on a volume
      XI_SOURCES_PHOTOS_TYPE: fs
      XI_SOURCES_PHOTOS_ROOT: /data/photos

      # Public web images, https only
      XI_SOURCES_WEB_TYPE: http
      XI_SOURCES_WEB_ALLOW: https://images.unsplash.com,https://upload.wikimedia.org/wikipedia/commons/

      # Relative imgproxy sources (/…/plain/cat.jpg) read r2
      XI_DIALECTS_IMGPROXY_SOURCE: r2
    secrets:
      - xi_signing_key
      - xi_signing_salt
      - aws_secret_access_key
      - r2_secret_access_key
      - b2_application_key
    volumes:
      - photos:/data/photos:ro
    read_only: true
    tmpfs:
      - /tmp:rw,noexec,nosuid,size=64m

secrets:
  xi_signing_key:
    file: ./secrets/xi_signing_key
  xi_signing_salt:
    file: ./secrets/xi_signing_salt
  aws_secret_access_key:
    file: ./secrets/aws_secret_access_key
  r2_secret_access_key:
    file: ./secrets/r2_secret_access_key
  b2_application_key:
    file: ./secrets/b2_application_key

volumes:
  photos:
```

### 5.3 A config file with `{file: …}` secrets

The same kind of setup kept in one file: [`docs/examples/config.multi-source.yaml`](examples/config.multi-source.yaml) defines every source type, with each credential a `{file: …}` reference, and [`docs/examples/compose.config-file.yml`](examples/compose.config-file.yml) mounts it with the matching Docker secrets. The file holds nothing secret, so it can live in git.

```yaml docs/examples/config.multi-source.yaml
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
#
# Several sources of the same type side by side (docs/SOURCES.md). Nothing
# secret is written here: every credential is a {file: …} reference to a
# Docker or Kubernetes secret, so this file can live in git or a ConfigMap.
# config.multi-source.toml is the same configuration in TOML; a test in
# internal/config loads both and checks they are equal.

signing:
  keys: [{ file: /run/secrets/xi_signing_key }]
  salts: [{ file: /run/secrets/xi_signing_salt }]

sources:
  # AWS S3. No keys: the AWS default chain (IRSA, ECS task role, EC2
  # instance profile, AWS_* variables) signs the requests.
  aws:
    type: s3
    region: eu-west-1
    buckets: [acme-media, "acme-tenant-*"]
    bucket: acme-media

  # Cloudflare R2 (S3 API). Replace ACCOUNT_ID with your account id.
  r2:
    type: s3
    endpoint: https://ACCOUNT_ID.r2.cloudflarestorage.com
    region: auto
    buckets: [assets]
    bucket: assets
    access_key_id: R2_ACCESS_KEY_ID
    secret_access_key: { file: /run/secrets/r2_secret_access_key }

  # Backblaze B2 (S3-compatible API): the endpoint names the bucket's region.
  b2:
    type: s3
    endpoint: https://s3.us-west-004.backblazeb2.com
    region: us-west-004
    buckets: [acme-archive]
    bucket: acme-archive
    access_key_id: B2_KEY_ID
    secret_access_key: { file: /run/secrets/b2_application_key }

  # RustFS or MinIO next to the service: path-style addressing. Its bucket
  # has the same name as R2's, so it is reached with xi://rustfs/<key>.
  rustfs:
    type: s3
    endpoint: http://rustfs:9000
    region: us-east-1
    force_path_style: true
    buckets: [assets]
    bucket: assets
    prefix: public/
    access_key_id: xi-reader
    secret_access_key: { file: /run/secrets/rustfs_secret_key }

  # Google Cloud Storage with a service-account key. Without
  # credentials_json, Application Default Credentials are used (GKE
  # Workload Identity, GOOGLE_APPLICATION_CREDENTIALS).
  gcs:
    type: gcs
    buckets: [acme-gcs-images]
    bucket: acme-gcs-images
    credentials_json: { file: /run/secrets/gcs_service_account.json }

  # Azure Blob Storage with a container SAS token (read + list is enough).
  azure:
    type: azure
    account: acmeimages
    buckets: [media]
    bucket: media
    sas_token: { file: /run/secrets/azure_sas_token }

  # Two local directories. Every fs source is jailed in its root.
  photos:
    type: fs
    root: /data/photos
  uploads:
    type: fs
    root: /data/uploads
    prefix: public/

  # A partner's SFTP server. host_key is the server's public key
  # (ssh-keyscan -p 2222 sftp.partner.example); it is mandatory.
  partner-sftp:
    type: sftp
    host: sftp.partner.example
    port: 2222
    user: xi
    private_key: { file: /run/secrets/partner_sftp_key }
    host_key: "sftp.partner.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPleaseReplaceWithTheOutputOfSshKeyscanKey01"
    root: /outgoing/images

  # A legacy FTP server over explicit TLS.
  legacy-ftp:
    type: ftp
    host: ftp.example.com
    tls: true
    user: images
    password: { file: /run/secrets/legacy_ftp_password }

  # Public web images: https only, two hosts.
  web:
    type: http
    allow:
      - https://images.unsplash.com
      - https://upload.wikimedia.org/wikipedia/commons/

  # A private CMS: relative keys are joined to base_url, and every request
  # carries the Authorization header.
  cms:
    type: http
    base_url: https://cms.example.com/media/
    headers:
      Authorization: { file: /run/secrets/cms_authorization }

dialects:
  # Relative imgproxy sources (/<sig>/rs:fit:300:0/plain/cat.jpg) read the
  # r2 bucket "assets".
  imgproxy:
    source: r2
  # Host-bound imgix mounts, one source each.
  imgix:
    enabled: true
    hosts: [images.example.com]
    source: r2
    mounts:
      - hosts: [archive-images.example.com]
        source: b2
```

<details>
<summary>The same configuration in TOML (<code>docs/examples/config.multi-source.toml</code>)</summary>

```toml docs/examples/config.multi-source.toml
#:schema https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
#
# config.multi-source.yaml in TOML (docs/SOURCES.md). A test in
# internal/config loads both files and checks they give the same config.

[signing]
keys = [{ file = "/run/secrets/xi_signing_key" }]
salts = [{ file = "/run/secrets/xi_signing_salt" }]

# AWS S3 with the default credential chain (IRSA, task role, instance profile).
[sources.aws]
type = "s3"
region = "eu-west-1"
buckets = ["acme-media", "acme-tenant-*"]
bucket = "acme-media"

# Cloudflare R2
[sources.r2]
type = "s3"
endpoint = "https://ACCOUNT_ID.r2.cloudflarestorage.com"
region = "auto"
buckets = ["assets"]
bucket = "assets"
access_key_id = "R2_ACCESS_KEY_ID"
secret_access_key = { file = "/run/secrets/r2_secret_access_key" }

# Backblaze B2
[sources.b2]
type = "s3"
endpoint = "https://s3.us-west-004.backblazeb2.com"
region = "us-west-004"
buckets = ["acme-archive"]
bucket = "acme-archive"
access_key_id = "B2_KEY_ID"
secret_access_key = { file = "/run/secrets/b2_application_key" }

# RustFS or MinIO, path-style; same bucket name as r2 (use xi://rustfs/<key>)
[sources.rustfs]
type = "s3"
endpoint = "http://rustfs:9000"
region = "us-east-1"
force_path_style = true
buckets = ["assets"]
bucket = "assets"
prefix = "public/"
access_key_id = "xi-reader"
secret_access_key = { file = "/run/secrets/rustfs_secret_key" }

[sources.gcs]
type = "gcs"
buckets = ["acme-gcs-images"]
bucket = "acme-gcs-images"
credentials_json = { file = "/run/secrets/gcs_service_account.json" }

[sources.azure]
type = "azure"
account = "acmeimages"
buckets = ["media"]
bucket = "media"
sas_token = { file = "/run/secrets/azure_sas_token" }

[sources.photos]
type = "fs"
root = "/data/photos"

[sources.uploads]
type = "fs"
root = "/data/uploads"
prefix = "public/"

[sources.partner-sftp]
type = "sftp"
host = "sftp.partner.example"
port = 2222
user = "xi"
private_key = { file = "/run/secrets/partner_sftp_key" }
host_key = "sftp.partner.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPleaseReplaceWithTheOutputOfSshKeyscanKey01"
root = "/outgoing/images"

[sources.legacy-ftp]
type = "ftp"
host = "ftp.example.com"
tls = true
user = "images"
password = { file = "/run/secrets/legacy_ftp_password" }

[sources.web]
type = "http"
allow = ["https://images.unsplash.com", "https://upload.wikimedia.org/wikipedia/commons/"]

[sources.cms]
type = "http"
base_url = "https://cms.example.com/media/"
headers = { Authorization = { file = "/run/secrets/cms_authorization" } }

[dialects.imgproxy]
source = "r2"

[dialects.imgix]
enabled = true
hosts = ["images.example.com"]
source = "r2"
mounts = [{ hosts = ["archive-images.example.com"], source = "b2" }]
```

</details>

```yaml docs/examples/compose.config-file.yml
# The sources of config.multi-source.yaml, mounted as the config file, with
# every credential a Docker secret the file points at with {file: …}
# (docs/SOURCES.md). A test in internal/config loads this service's
# environment, config file and secrets.
#
# Put the secret files in ./secrets/ (one value each, no quotes) and make
# them readable by the container user (uid 65532): chmod 0444 secrets/*.
services:
  xtandard-image:
    image: ${XI_IMAGE:-ghcr.io/xantiagoma/xtandard-image:slim}
    restart: unless-stopped
    ports:
      - "8080:8080"
      - "127.0.0.1:9090:9090"
    environment:
      XI_CONFIG_FILE: /etc/xtandard/config.yaml
      XI_ADMIN_ADDR: ":9090"
      # An environment variable still wins over the file, per field:
      XI_SOURCES_AWS_REGION: eu-west-1
    volumes:
      - ./config.multi-source.yaml:/etc/xtandard/config.yaml:ro
      - photos:/data/photos:ro
      - uploads:/data/uploads:ro
    secrets:
      - xi_signing_key
      - xi_signing_salt
      - r2_secret_access_key
      - b2_application_key
      - rustfs_secret_key
      - source: gcs_service_account
        target: gcs_service_account.json
      - azure_sas_token
      - partner_sftp_key
      - legacy_ftp_password
      - cms_authorization
    read_only: true
    tmpfs:
      - /tmp:rw,noexec,nosuid,size=64m

secrets:
  xi_signing_key: { file: ./secrets/xi_signing_key }
  xi_signing_salt: { file: ./secrets/xi_signing_salt }
  r2_secret_access_key: { file: ./secrets/r2_secret_access_key }
  b2_application_key: { file: ./secrets/b2_application_key }
  rustfs_secret_key: { file: ./secrets/rustfs_secret_key }
  gcs_service_account: { file: ./secrets/gcs_service_account.json }
  azure_sas_token: { file: ./secrets/azure_sas_token }
  partner_sftp_key: { file: ./secrets/partner_sftp_key }
  legacy_ftp_password: { file: ./secrets/legacy_ftp_password }
  cms_authorization: { file: ./secrets/cms_authorization }

volumes:
  photos:
  uploads:
```

### 5.4 Kubernetes

The configuration goes in a ConfigMap, the credentials in a Secret mounted as files (`{file: …}` in the config, `_FILE` variables in the environment) or injected with `secretKeyRef`; AWS uses IRSA, so its source has no key at all. Create the Secret with `kubectl create secret generic` (the command is at the top of `deployment.yaml`), or from your secret manager (External Secrets, Sealed Secrets, the CSI driver) with the same keys.

```yaml docs/examples/k8s/configmap.yaml
# The non-secret configuration (docs/SOURCES.md). Credentials are {file: …}
# references to the xi-secrets Secret, which deployment.yaml mounts at
# /run/secrets/xi. A test in internal/config loads this ConfigMap together
# with deployment.yaml.
apiVersion: v1
kind: ConfigMap
metadata:
  name: xi-config
data:
  config.yaml: |
    # yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json
    sources:
      # AWS S3 through IRSA / EKS Pod Identity: no keys in the config.
      aws:
        type: s3
        region: eu-west-1
        buckets: [acme-media]
        bucket: acme-media
      # Cloudflare R2: the access key id comes from the environment
      # (XI_SOURCES_R2_ACCESS_KEY_ID, deployment.yaml), the secret from a file.
      r2:
        type: s3
        endpoint: https://ACCOUNT_ID.r2.cloudflarestorage.com
        region: auto
        buckets: [assets]
        bucket: assets
        secret_access_key: { file: /run/secrets/xi/r2-secret-access-key }
      # In-cluster RustFS or MinIO, path-style. Its address is private, which
      # only http sources care about (the SSRF policy); s3 endpoints are trusted.
      rustfs:
        type: s3
        endpoint: http://rustfs.storage.svc.cluster.local:9000
        region: us-east-1
        force_path_style: true
        buckets: [assets, "tenant-*"]
        access_key_id: xi-reader
        secret_access_key: { file: /run/secrets/xi/rustfs-secret-key }
      # GCS with a service-account key (on GKE prefer Workload Identity and
      # drop credentials_json).
      gcs:
        type: gcs
        buckets: [acme-gcs-images]
        bucket: acme-gcs-images
        credentials_json: { file: /run/secrets/xi/gcs-service-account.json }
      web:
        type: http
        allow: [https://images.unsplash.com]
    dialects:
      imgproxy:
        source: r2
```

```yaml docs/examples/k8s/deployment.yaml
# Deployment for configmap.yaml (docs/SOURCES.md). Create the Secret first;
# its keys are the file names under /run/secrets/xi:
#
#   kubectl create secret generic xi-secrets \
#     --from-literal=signing-key="$(openssl rand -hex 32)" \
#     --from-literal=signing-salt="$(openssl rand -hex 32)" \
#     --from-literal=r2-access-key-id=<R2 access key id> \
#     --from-literal=r2-secret-access-key=<R2 secret access key> \
#     --from-literal=rustfs-secret-key=<RustFS secret key> \
#     --from-file=gcs-service-account.json=./sa.json
#
# A test in internal/config loads this Deployment's environment, the
# ConfigMap and the mounted Secret keys, so every path below must match.
apiVersion: v1
kind: ServiceAccount
metadata:
  name: xtandard-image
  annotations:
    # EKS IRSA: the role the aws source uses (no keys in the config).
    eks.amazonaws.com/role-arn: arn:aws:iam::111122223333:role/xtandard-image-read
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: xtandard-image
spec:
  replicas: 2
  selector:
    matchLabels: { app: xtandard-image }
  template:
    metadata:
      labels: { app: xtandard-image }
    spec:
      serviceAccountName: xtandard-image
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        fsGroup: 65532
        seccompProfile: { type: RuntimeDefault }
      containers:
        - name: xtandard-image
          image: ghcr.io/xantiagoma/xtandard-image:slim
          ports:
            - { name: http, containerPort: 8080 }
            - { name: admin, containerPort: 9090 }
          env:
            - { name: XI_CONFIG_FILE, value: /etc/xtandard/config.yaml }
            # Admin listener: metrics, /openapi.json, /config.schema.json and,
            # with XI_ADMIN_DOCS, /docs. Reach it with kubectl port-forward.
            - { name: XI_ADMIN_ADDR, value: ":9090" }
            - { name: XI_ADMIN_DOCS, value: "true" }
            # _FILE variants read a mounted Secret key.
            - { name: XI_SIGNING_KEYS_FILE, value: /run/secrets/xi/signing-key }
            - { name: XI_SIGNING_SALTS_FILE, value: /run/secrets/xi/signing-salt }
            # Or inject the value itself from the Secret.
            - name: XI_SOURCES_R2_ACCESS_KEY_ID
              valueFrom:
                secretKeyRef: { name: xi-secrets, key: r2-access-key-id }
          volumeMounts:
            - { name: config, mountPath: /etc/xtandard, readOnly: true }
            - { name: secrets, mountPath: /run/secrets/xi, readOnly: true }
            - { name: tmp, mountPath: /tmp }
          readinessProbe: { httpGet: { path: /readyz, port: http }, periodSeconds: 5 }
          livenessProbe: { httpGet: { path: /healthz, port: http }, periodSeconds: 10 }
          resources:
            requests: { cpu: "1", memory: 1Gi }
            limits: { cpu: "2", memory: 2Gi }
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
      volumes:
        - name: config
          configMap: { name: xi-config }
        - name: secrets
          secret:
            secretName: xi-secrets
            defaultMode: 0440
            items:
              - { key: signing-key, path: signing-key }
              - { key: signing-salt, path: signing-salt }
              - { key: r2-secret-access-key, path: r2-secret-access-key }
              - { key: rustfs-secret-key, path: rustfs-secret-key }
              - { key: gcs-service-account.json, path: gcs-service-account.json }
        - name: tmp
          emptyDir: { medium: Memory, sizeLimit: 64Mi }
---
apiVersion: v1
kind: Service
metadata:
  name: xtandard-image
spec:
  selector: { app: xtandard-image }
  ports:
    - { name: http, port: 80, targetPort: http }
    # No admin port here: keep /metrics, /docs and pprof off the public path.
```

Kubernetes updates a mounted Secret in place, but xtandard-image reads secret files once, at startup: roll the Deployment (`kubectl rollout restart deployment/xtandard-image`) after rotating a credential. For a signing key rotation, list the new key first and keep the old one until old URLs expire (`XI_SIGNING_KEYS` takes several keys).

## 6. Security notes

- **The allowlist is required.** An `http` source needs `allow` or `base_url`; `s3`, `gcs` and `azure` sources need at least one bucket; `sftp` needs `host_key`. There is no "any bucket" or "any host" default. `allow: ["*"]` is possible but opens every public host; pin a scheme and a path prefix where you can.
- **SSRF.** `http` sources (and every redirect hop, re-checked against the same source's allowlist) connect only to public addresses on `XI_FETCH_ALLOWED_PORTS`, checked on the connected IP, so DNS rebinding does not help. Private, loopback and link-local networks (cloud metadata at 169.254.169.254 included) need `XI_FETCH_ALLOWED_NETWORKS` or `XI_FETCH_ALLOW_PRIVATE_NETWORKS`. An https URL may not redirect to http. The object-store endpoints and FTP/SFTP hosts you configure are trusted and not subject to this policy.
- **Jails.** `fs` reads go through `os.Root`: `..` and symlinks leaving the root fail, and without `follow_symlinks` any symlink in the path is refused. `..` segments are refused in every source string (403) before any source is consulted. `prefix` jails keys on every non-http type.
- **Error bodies name the failed check, not the configuration.** A refusal says which rule failed (`source "web": host not in the source allowlist`) but never echoes allow patterns, base URLs, buckets, prefixes, endpoints or credentials; those stay in the server log, with the source reference redacted. `XI_COMPAT_IMGPROXY_STATUS=true` turns 400 and 403 into imgproxy's 404.
- **Secrets never leave the process.** Secret fields are redacted in `config print`, logs and errors; the playground lists only source names and types. Prefer `_FILE` / `{file: …}` over plain variables where the platform can mount files, since environment variables are visible to anything that can inspect the container.
- **Least privilege.** Give every source read-only credentials on exactly the buckets it lists. Only the S3 result or origin cache store (`XI_CACHE_RESULT_S3_SOURCE`) writes, and it should use its own source with write access to the cache bucket only.
- **Signed URLs.** A source allowlist limits *where* images come from; signing (`XI_SIGNING_KEYS`) limits *who* can ask for them. Use both in production.

## 7. Troubleshooting

Public error bodies (`text/plain`, or JSON with `Accept: application/json`) and what they mean:

| Status and body | Cause | Fix |
|---|---|---|
| 403 `unknown source "x"` | `xi://x/…` names a source that is not configured | Check the name in `XI_SOURCES` or the file (`config print`); names are lower case |
| 403 `no source allows this url` | No `http` source's `allow` or `base_url` matches the URL (host, scheme, port or path prefix) | Add a pattern; a pattern with a scheme or path is stricter than the bare host |
| 403 `no source allows this s3:// reference` (or `gs://`, `abs://`, `local://`, `ftp://`, `sftp://`) | No source of that type allows the bucket, the host and port, or the key is outside every prefix jail | Add the bucket to `buckets`, check the port, or include the `prefix` in the key |
| 403 `source "x" has no default bucket: name the bucket in the url` | A relative key or `xi://x/key` on an `s3`/`gcs`/`azure` source without `bucket` | Set `bucket`, or use `s3://bucket/key` |
| 403 `source "x" has no base_url: the key must be an absolute http(s) url` | `xi://x/key` with a relative key on an `http` source without `base_url` | Set `base_url`, or write `xi://x/https://host/key` |
| 403 `source "x": host not in the source allowlist` | `xi://x/https://…` whose host none of `x`'s patterns allow | Add the host to that source's `allow` |
| 403 `source "x": url scheme, port or path not in the source allowlist` | The host is allowed but the scheme, port or path prefix is not | Compare with the pattern (`https://` only? a path prefix?) |
| 403 `forbidden` | A relative key on a mount without a source; `..` in the path; at fetch time an SSRF block (private address or port) or a refused redirect; the storage answered AccessDenied | Bind the mount to a source; check `XI_FETCH_*` and the credentials; the server log has the reason |
| 400 `invalid url` | The source string does not parse: unknown scheme, `s3:///key` without a bucket, userinfo (`user:pass@`), control characters, bad percent-encoding | Fix the URL; in imgproxy `plain/` sources, `%`, `?` and `@` must be percent-encoded |
| 404 `source image not found` | The source is allowed but the object does not exist (NoSuchKey, ENOENT, HTTP 404) | Check the key, including the `prefix` that relative keys get |
| 502 `upstream error`, 504 `upstream timeout` | The origin failed or was slower than `XI_FETCH_TIMEOUT` | Check the endpoint and network; one transient failure is retried (`XI_FETCH_RETRY`) |
| 502 `upstream busy, retry later` | The object store throttled the request (S3 SlowDown) | Put a CDN or the result cache in front |

Startup errors name the key and its variable, for example `sources.web.allow (XI_SOURCES_WEB_ALLOW): an http source needs allow patterns or a base_url` or `sources.r2.buckets: must list at least one bucket`. To check a configuration without starting the server:

```sh
xtandard-image config validate --config config.yaml           # file + XI_* environment, as serve does
xtandard-image config validate --config config.yaml --no-env  # the file alone (CI)
xtandard-image config print --config config.yaml              # effective config, secrets redacted
docker run --rm -v "$PWD/config.yaml:/etc/xtandard/config.yaml:ro" \
  ghcr.io/xantiagoma/xtandard-image:slim config validate
```

`config validate` catches syntax, unknown keys, missing allowlists, bad patterns and unreadable secret files. It does not contact the sources: a wrong credential shows up as a 403 or 502 on the first request, with the cause in the server log.
