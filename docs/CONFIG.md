# Configuration

Draft reference. Names are stable once M1 ships; until then they may change in the owning work package.

## 1. Sources of configuration and precedence

Lowest to highest:

1. Built-in defaults.
2. YAML file: `--config <path>` or `XI_CONFIG_FILE` (default `/etc/xtandard/config.yaml` if it exists).
3. `IMGPROXY_*` variables, only when `XI_COMPAT_IMGPROXY_ENV=true` (migration aid; mapped to the XI equivalents listed in the reference, e.g. `IMGPROXY_KEY` → `XI_SIGNING_KEYS`).
4. `XI_*` environment variables.
5. Per-request security overrides (`max_src_resolution`, …) only when `XI_SECURITY_ALLOW_SECURITY_OPTIONS=true`, and only downward unless `XI_SECURITY_ALLOW_RAISING_LIMITS=true`.

Rules:

- **Mapping**: an env var is the YAML path upper-cased, dots → `_`, prefixed `XI_`. `limits.max_src_bytes` ↔ `XI_LIMITS_MAX_SRC_BYTES`; map entries use the key: `sources.assets.bucket` ↔ `XI_SOURCES_ASSETS_BUCKET`. Map keys are lower-case `[a-z0-9-]`; `-` becomes `_` in env names.
- **Lists**: comma-separated in env; YAML sequences in the file. An env list **replaces** the YAML list.
- **Secrets**: any variable accepts a `_FILE` suffix (`XI_SIGNING_KEYS_FILE=/run/secrets/xi_keys`) for Docker/Kubernetes secrets.
- **Durations** use Go syntax (`5s`, `1m30s`); sizes accept `KiB/MiB/GiB` suffixes or bytes.
- **Validation** is strict: unknown YAML keys and unknown `XI_` variables are a startup error (typos should not silently disable security). `xtandard-image config print` shows the effective config with secrets redacted.
- **Secure by default**: with no sources configured and `XI_DEV_MODE` unset, the server refuses to start.

## 2. Environment variable reference

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
| `XI_ENCRYPTION_KEY` | `` | Hex AES key (16/24/32 bytes) for `enc/` sources |
| `XI_SECURITY_UNSIGNED_POLICY` | `restricted` | `allow` \| `restricted` \| `deny` for every request that carries no verified signature, in any dialect (including imgproxy URLs when `XI_SIGNING_REQUIRED` is false) |
| `XI_SECURITY_ALLOWED_WIDTHS` | Next.js sizes | Restricted-policy widths |
| `XI_SECURITY_ALLOWED_QUALITIES` | `1-100/5` | Restricted-policy qualities |
| `XI_SECURITY_ALLOW_SECURITY_OPTIONS` | `false` | Honour per-request limit options |
| `XI_SECURITY_ALLOW_RAISING_LIMITS` | `false` | Let them exceed config |
| `XI_SECURITY_SECRET` | `` | Require `Authorization: Bearer <secret>` |
| `XI_SECURITY_ALLOWED_PROCESSING_OPTIONS` | `` (all) | imgproxy option allowlist |

### Sources and fetching

| Variable | Default | Meaning |
|---|---|---|
| `XI_SOURCES` | `` | Names of sources defined via env (`origin,assets`) |
| `XI_SOURCES_<NAME>_TYPE` | — | `http` \| `s3` \| `gcs` \| `azure` \| `fs` \| `ftp` \| `sftp` |
| `XI_SOURCES_<NAME>_ALLOW` | — | http: host globs with optional path prefix (`*.example.com`, `cdn.example.com/img/`) |
| `XI_SOURCES_<NAME>_BASE_URL` | — | http: base for relative keys |
| `XI_SOURCES_<NAME>_HEADERS` | — | http: extra request headers (`Authorization=…`) |
| `XI_SOURCES_<NAME>_BUCKETS` / `_BUCKET` | — | s3/gcs/azure: allowed buckets/containers; `_BUCKET` binds a default |
| `XI_SOURCES_<NAME>_PREFIX` | `` | object key prefix jail |
| `XI_SOURCES_<NAME>_ENDPOINT`, `_REGION`, `_FORCE_PATH_STYLE`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_ROLE_ARN` | — | s3 (R2, MinIO, Spaces, B2 via endpoint) |
| `XI_SOURCES_<NAME>_CREDENTIALS_JSON` | ADC | gcs |
| `XI_SOURCES_<NAME>_ACCOUNT`, `_ACCOUNT_KEY`, `_CONNECTION_STRING` | managed identity | azure |
| `XI_SOURCES_<NAME>_ROOT` | — | fs root (jail via `os.Root`) |
| `XI_SOURCES_<NAME>_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_PRIVATE_KEY`, `_HOST_KEY`, `_TLS` | — | ftp/sftp (sftp host key mandatory) |
| `XI_FETCH_TIMEOUT` | `10s` | Per download attempt |
| `XI_FETCH_MAX_REDIRECTS` | `5` | |
| `XI_FETCH_MAX_CONCURRENT` | `256` | Concurrent origin fetches |
| `XI_FETCH_ALLOWED_PORTS` | `80,443` | SSRF port allowlist |
| `XI_FETCH_ALLOW_PRIVATE_NETWORKS` | `false` | Explicit opt-in (e.g. in-cluster MinIO over http source) |
| `XI_FETCH_USER_AGENT` | `xtandard-image/<ver>` | |

### Dialects and presets

| Variable | Default | Meaning |
|---|---|---|
| `XI_DIALECTS_DEFAULT` | `imgproxy` | Fallback dialect |
| `XI_DIALECTS_STRICT` | `false` | 400 on unknown/unsupported options |
| `XI_DIALECTS_<NAME>_ENABLED` | imgproxy `true`, others `false` | |
| `XI_DIALECTS_<NAME>_PREFIX` | see URL-DIALECTS.md | Mount prefix |
| `XI_DIALECTS_<NAME>_HOSTS` | `` | Host bindings |
| `XI_DIALECTS_<NAME>_SOURCE` | `` | Bound source for relative paths |
| `XI_DIALECTS_<NAME>_KEYS` | `` | Dialect secrets (imgix tokens, thumbor key, cloudinary api secret, imagekit private key, bunny token key) |
| `XI_DIALECTS_<NAME>_UNSIGNED_POLICY` | global | Per-dialect override |
| `XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR` | `:` | |
| `XI_DIALECTS_IMGPROXY_BASE_URL` | `` | |
| `XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS` | `` | `regex=replacement;…` |
| `XI_DIALECTS_CLOUDINARY_CLOUDS` | `` | `cloud=source,…` |
| `XI_PRESETS` | `` | `name=opts,name2=opts` (imgproxy syntax) |
| `XI_PRESETS_FILE` | `` | One preset per line |
| `XI_PRESETS_ONLY` | `false` | Only preset URLs allowed |

### Processing, formats, limits

| Variable | Default | Meaning |
|---|---|---|
| `XI_PROCESSING_WORKERS` | `GOMAXPROCS` | Concurrent processing jobs |
| `XI_PROCESSING_QUEUE_SIZE` | `workers×16` | Waiting jobs; beyond it requests fail fast with 429 |
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
| `XI_PROCESSING_WATERMARK_SOURCE` | `` | Default watermark image (source string) |
| `XI_PROCESSING_WATERMARK_OPACITY` | `1` | |
| `XI_FALLBACK_SOURCE` | `` | Fallback image (source string) |
| `XI_FALLBACK_STATUS` | `200` | Status used with fallback |
| `XI_FORMATS_QUALITY` | `80` | Global default |
| `XI_FORMATS_<FMT>_QUALITY` | avif 60, heic 60, jxl 75, webp 80, jpeg 80 | |
| `XI_FORMATS_<FMT>_EFFORT` | per FORMATS.md | |
| `XI_FORMATS_JPEG_PROGRESSIVE` | `true` | |
| `XI_FORMATS_PNG_QUANTIZE` | `false` | |
| `XI_FORMATS_AUTO_AVIF` / `_WEBP` / `_JXL` | `true` / `true` / `false` | Accept negotiation |
| `XI_FORMATS_ENFORCE_AVIF` / `_WEBP` / `_JXL` | `false` | |
| `XI_FORMATS_STRICT_ACCEPT` | `false` | 406 when nothing acceptable |
| `XI_FORMATS_PREFERRED` | `jpeg,png,gif` | Fallback order for unencodable sources |
| `XI_FORMATS_SVG_PASSTHROUGH` | `true` | |
| `XI_FORMATS_AVIF_MAX_PIXELS` | `16000000` | |
| `XI_FORMATS_BEST_MAX_PIXELS` | `8000000` | |
| `XI_FORMATS_BEST_COMPLEXITY_THRESHOLD` | `5.5` | |
| `XI_FORMATS_SAVE_DATA_QUALITY_DELTA` | `-15` | |
| `XI_FORMATS_AUTOQUALITY_METHOD` | `none` | `none` \| `size` \| `dssim` |
| `XI_FORMATS_AUTOQUALITY_TARGET` / `_MIN` / `_MAX` / `_ALLOWED_ERROR` | `0.02` / `60` / `85` / `0.001` | |
| `XI_LIMITS_MAX_SRC_BYTES` | `50MiB` | |
| `XI_LIMITS_MAX_SRC_PIXELS` | `50000000` | |
| `XI_LIMITS_MAX_RESULT_DIMENSION` | `8192` | |
| `XI_LIMITS_MAX_ANIMATION_FRAMES` | `256` | |
| `XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS` | `16000000` | |
| `XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS` | `60000000` | |
| `XI_LIMITS_MAX_SVG_BYTES` | `5MiB` | |
| `XI_LIMITS_MAX_PDF_PAGES` | `500` | |
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
| `XI_CACHE_ALLOW_BYPASS` | `false` | Honour `bypass_cache` |
| `XI_CACHE_RESULT_STORES` | `` | Ordered tiers: `memory,disk,redis,s3` |
| `XI_CACHE_RESULT_MEMORY_BYTES` | `256MiB` | |
| `XI_CACHE_RESULT_DISK_PATH` / `_DISK_BYTES` | `` / `10GiB` | |
| `XI_CACHE_RESULT_REDIS_URL` | `` | |
| `XI_CACHE_RESULT_S3_SOURCE` | `` | Name of an s3 source definition to reuse credentials, plus `_S3_BUCKET`, `_S3_PREFIX` |
| `XI_CACHE_ORIGIN_MEMORY_BYTES` | `256MiB` | Original-bytes LRU |
| `XI_CACHE_ORIGIN_TTL` | `60s` | |
| `XI_OBSERVABILITY_LOG_LEVEL` | `info` | |
| `XI_ADMIN_ADDR` | `127.0.0.1:9090` | Admin listener: `/metrics`, pprof; set `:9090` for in-cluster scraping; empty disables |
| `XI_ADMIN_PPROF` | `false` | pprof incl. `goroutineleak` on the admin listener |
| `XI_OBSERVABILITY_DEBUG_HEADERS` | `false` | `X-Xi-Ignored`, stage timings |
| `OTEL_EXPORTER_OTLP_ENDPOINT` etc. | — | Standard OpenTelemetry vars; tracing on when set |
| `XI_OTEL_ENABLED` | `false` | Force tracing on without an endpoint variable (exporter still chosen by `OTEL_TRACES_EXPORTER`, e.g. `console`) |

## 3. YAML example

```yaml
server:
  addr: ":8080"
signing:
  keys: ["943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"]
  salts: ["520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"]
encryption:
  key: "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"
security:
  unsigned_policy: restricted
sources:
  origin:
    type: http
    allow: ["cdn.example.com", "*.example.org/images/"]
  assets:
    type: s3
    endpoint: https://<account>.r2.cloudflarestorage.com
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

The file is decoded with `goccy/go-yaml` in strict mode (unknown fields rejected); environment variables are then applied on top with `caarlos0/env/v11`. No interpolation syntax: put secrets in env or `_FILE` variables, keep the YAML file non-secret so it can live in a ConfigMap.

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
