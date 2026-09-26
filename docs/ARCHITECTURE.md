# xtandard-image architecture

Status: design draft, pre-alpha. Owner decisions are settled in `docs/adr/`; tech research in `docs/research/stack.md`. Go coding conventions live in `docs/go-standards.md` (maintained separately). This document is the contract that the work packages in `docs/ROADMAP.md` implement.

## 1. System overview

xtandard-image ("xi") is a stateless HTTP service that turns `GET <url>` into a processed image. It is a single Go binary linked against libvips 8.18.x, shipped as a Docker image. Nothing about a request lives outside that request except optional caches.

```
                 ┌──────────────────────────── xtandard-image process ─────────────────────────────┐
 client / CDN →  │ net/http ─► router ─► dialect parser ─► TransformRequest ─► security gate        │
                 │                                                    │                              │
                 │         result cache ◄─── key ◄────────────────────┤                              │
                 │                                                    ▼                              │
                 │ fetcher (singleflight + origin cache) ─► source (http|s3|gcs|azure|fs|ftp|sftp)   │
                 │                                                    │ bytes                        │
                 │ admission (queue + worker semaphores, fail fast)  ─┤                              │
                 │                                                    ▼                              │
                 │ probe/sniff ─► planner (pure Go) ─► executor (libvips DAG) ─► encoder ─► response │
                 └───────────────────────────────────────────────────────────────────────────────────┘
```

Design principles:

1. **One canonical model.** Every URL dialect parses into the same `transform.Request`. Everything downstream (security, caching, planning, encoding) is dialect-agnostic. Two URLs in different dialects that ask for the same thing share one cache entry.
2. **Pure Go by default, cgo at the edge.** `internal/vips` is the only package that imports `"C"` (CLAUDE.md rule 3); `internal/pipeline/exec`, the `codec` encode/load files and `codec/raw` call its Go API and therefore need a cgo build, but never see a C type. Parsers, planner, config, security, sources, fetcher and caches build and test with `CGO_ENABLED=0`. This lets many agents work in parallel without a libvips toolchain.
3. **Plan, then execute.** A pure planner turns request + source metadata into a declarative `Plan`. The executor maps plan steps onto lazy libvips operations. Planner tests are table tests over data; only executor tests need libvips.
4. **Secure unless told otherwise.** No source allowlist means the server refuses to start unless `XI_DEV_MODE=true`.
5. **Fail closed, fail fast, fail with the right status code.**

## 2. Request lifecycle

Each stage has a timeout budget taken from the request context (`XI_SERVER_REQUEST_TIMEOUT`, default 20 s) and emits one tracing span and one histogram observation (`xi_stage_duration_seconds{stage}`).

| # | Stage | Package | Output | Typical failure → status |
|---|---|---|---|---|
| 1 | Ingress: accept, request ID, panic recovery, max URL length (8 KiB), method check (GET/HEAD) | `server` | `*http.Request` + ctx | 405, 414 |
| 2 | Route: choose dialect by host binding, then longest path prefix, then default | `dialect/router` | `Parser`, stripped path | 404 (no route) |
| 3 | Parse: dialect grammar → `transform.Request` (+ `Ignored` list) | `dialect/<name>` | `*transform.Request` | 400 malformed / unknown option (strict) |
| 4 | Authenticate: dialect signature verifier, expiry | `security/sign` | ok | 403 bad signature, 404 expired (imgproxy-compatible) |
| 5 | Resolve source: decrypt `enc/`, map source string to named source, apply allowlist | `source`, `security/crypt` | `source.Ref` | 403 not allowed, 400 bad source |
| 6 | Normalise and validate: presets expanded, defaults filled, limits clamped, `Validate()` | `transform`, `preset` | frozen request | 400, 422 (result dimension limit) |
| 7 | Negotiate: output format from explicit format, `Accept`, client hints, config | `negotiate` | `OutputFormat`, `Vary` set | 406 (only when `XI_FORMATS_STRICT_ACCEPT`) |
| 8 | Result cache lookup (optional): key = hash(canonical request, output format, source identity) | `cache` | hit → stage 14 | cache errors are logged, never fatal |
| 9 | Fetch: singleflight by source identity, origin cache, size limit, conditional request | `fetch` | `*fetch.Original` (bytes + metadata) | 404, 502, 504, 413→422 |
| 10 | Admission: `queue.TryAcquire` (fail fast), then `workers.Acquire(ctx)` with queue timeout; optional memory budget | `limiter` | permit | 429 queue full, 503 wait timeout (both with `Retry-After`) |
| 11 | Probe: magic-byte sniff, header-only decode, pixel/frame limits, SVG sanitise | `codec` | `ImageMeta` | 415→422 unsupported, 422 limits |
| 12 | Plan + execute: planner builds steps, executor chains libvips ops lazily, pixels computed on save | `pipeline/plan`, `pipeline/exec` | `*vips.Image` graph | 422 corrupt, 500 |
| 13 | Encode: format saver, quality/auto-quality/max-bytes loop | `codec/encode` | vips-owned output buffer + content type | 500 |
| 14 | Respond: headers (Cache-Control, ETag, Last-Modified, Vary, Content-DPR, Content-Disposition), 304 handling, HEAD, write body; store in result cache async | `server/respond` | bytes on the wire | 499 client gone (logged) |

Special paths bypass stages: `raw:1` / `skip_processing` streams source bytes after stage 9 (no admission, no decode, still size-limited and content-type-checked against an allowlist of image MIME types); `/info` stops after stage 11 and returns JSON; `fallback_image` replaces a failed stage 9–12 with a configured image and `XI_FALLBACK_STATUS`.

Stages 1–8 are pure Go and must complete in well under 1 ms for cache hits. Fetch happens **outside** the processing semaphore so slow origins never hold CPU slots.

## 3. Package layout

Go module `github.com/xantiagoma/xtandard-image`. Arrows mean "may import". Anything not listed may not be imported by `internal/transform`.

```
cmd/xtandard-image/         main: flags, subcommands (serve, health, sign, config print, version)
internal/
  app/                      composition root: builds every component from config, wires, owns lifecycle
  config/                   one Config struct: optional YAML (goccy/go-yaml, strict) then env via caarlos0/env/v11 (env wins), *_FILE secrets, Validate(), redaction
  errs/                     error kinds (Kind enum), wrapping helpers, Kind → HTTP status table
  transform/                THE model: Request, Pipeline, enums, defaults, Validate, CanonicalKey. No deps.
  preset/                   named presets store; presets compiled to transform patches
  dialect/                  Parser interface, Registry, Mount
    router/                 host / prefix / default selection
    imgproxy/               canonical dialect (+ /info)
    query/                  native query + imaginary-compatible endpoints
    imgix/ cloudinary/ imagekit/ cloudflare/ bunny/ thumbor/ wsrv/
    simple/                 table-driven "micro-dialects" for the unpic long tail (nextjs, netlify, ipx, ...)
  security/
    sign/                   Verifier interface + imgproxy, imgix, thumbor, cloudinary, imagekit, bunny, xi schemes
    crypt/                  AES-CBC source URL encryption/decryption
    ssrf/                   net.Dialer control, IP classification, redirect policy
    svg/                    SVG sanitiser (pure Go, token-stream based)
  source/                   Source interface, Ref, Registry, resolver (source string → Ref), allowlist
  fetch/                    Fetcher: singleflight, origin cache, byte limits, timeouts, retries
    httpsrc/ s3src/ gcssrc/ azuresrc/ fssrc/ ftpsrc/ sftpsrc/   source backends; the only place storage SDKs may be imported (go-standards §10)
  cache/                    Store interface, key derivation, tiering
    memory/ disk/ s3cache/ redis/
  negotiate/                Accept / client-hint parsing, format decision matrix, best-format analysis hooks
  limiter/                  worker slots, memory-weighted semaphore, queue with timeout
  vips/                     ONLY package that imports "C" and vipsgen; Image handle, Buffer (C-owned bytes), init, errors, thread lifecycle
    (shim .c/.h files)      hand-written C in the same package: owned-buffer load w/ fail_on, libraw thumbnail loader, save-to-blob, progress/kill hook
  codec/                    Format enum, magic sniffing, probe, loader selection, encoder options
    bmp/ ico/               own pure-Go loaders/savers, reimplemented from imgproxy's C loaders (Apache-2.0: keep upstream header, add NOTICE entry)
    raw/                    RAW strategy (calls vips.LoadRawThumbnail / vips.DcrawLoad)
  pipeline/
    plan/                   pure Go: (transform.Request, codec.ImageMeta) → Plan
    exec/                   Plan → libvips operations
  detect/                   (M3) ONNX Runtime face/object detection, loaded via dlopen (purego) when present
  video/                    (M3, build tag `ffmpeg`) go-astiav frame extraction
  obs/                      slog setup, Prometheus registry, OpenTelemetry, admin mux (metrics, pprof)
  server/                   http.Server + http.ServeMux, middleware, handlers (process, info, health), respond, writeError
```

Dependency rules (enforced by a `depguard`/`go-arch-lint` config in CI):

- `transform` and `errs` are leaves. Every other package may import them.
- `dialect/*` → `transform`, `preset`, `security/sign`, `errs`. Dialects never import `source`, `vips`, `pipeline`.
- `security/*` → `errs` only (and stdlib/x/crypto).
- `fetch/<backend>` → `source` (interface types), `security/ssrf`, `errs`. Backends never know about dialects or transforms.
- `fetch` → `source`, `cache`, `errs` (never its own backend subpackages; `app` constructs them).
- `pipeline/plan` → `transform`, `codec` (types only: `Format`, `ImageMeta`; these live in a cgo-free file set), `errs`.
- `pipeline/exec`, `codec` loaders/encoders → `vips`.
- `server` → everything above via interfaces; `app` is the only package that constructs concrete types.
- `config` imports nothing internal except `errs`. Each component declares its own `Options` struct; `app` maps `config.Config` into those options. This keeps component packages testable without the whole config tree.

Routing: `server` registers fixed routes on a stdlib `http.ServeMux` with method patterns (`GET /healthz`, `GET /readyz`, `GET /{path...}` catch-all, which also matches HEAD) and hands the catch-all to `dialect/router`; the admin listener has its own `ServeMux` (`/metrics`, pprof). No third-party router (go-standards §2).

Build tags: `ffmpeg` (video). `server` tests use a fake `exec.Executor`, so they need no tag. ONNX needs no tag (runtime dlopen).

## 4. The TransformRequest model

`internal/transform` is plain data. No methods do I/O. Zero values are meaningful ("not set"); defaults are applied by `ApplyDefaults(cfg Defaults)` so that the canonical key only contains effective values.

```go
package transform

type Request struct {
    Kind      Kind         // Process | Info | Raw
    Dialect   string       // for metrics/logging only; not part of cache key
    Source    SourceSpec
    Pipelines []Pipeline   // len>=1; >1 means chained pipelines (imgproxy "-", cloudinary "/")
    Output    Output
    Delivery  Delivery
    Limits    LimitOverrides // only honoured if XI_SECURITY_ALLOW_SECURITY_OPTIONS
    Info      *InfoOptions   // Kind == Info
    Ignored   []Ignored      // options understood syntactically but not applied (debug header)
}

type SourceSpec struct {
    Raw       string       // as written: "https://…", "s3://b/k", "local:///p", "xi://name/key", or relative key
    Encoding  SourceEncoding // Plain | Base64 | Encrypted | Relative
    Mount     string       // named source bound by dialect mount (imgix source, cloudinary cloud, …)
    Hashsum   *Hashsum     // expected source digest (md5|sha1|sha256|sha512)
}

type Pipeline struct {
    Resize      Resize
    Gravity     Gravity
    Crop        *Crop        // pre-resize crop in source space
    CropObjects *CropObjects // M3
    Trim        *Trim
    Rotate      int          // 0/90/180/270 (arbitrary angles: RotateAny)
    RotateAny   float64      // arbitrary angle with Background fill (imgix rot, wsrv ro, cloudinary a)
    AutoRotate  *bool        // nil = config default
    Flip        Flip         // {H, V bool}
    Padding     *Padding     // after resize, follows DPR
    Extend      Extend       // {Enabled bool; Gravity Gravity}
    ExtendAR    Extend
    Background  *Color       // RGBA; nil = keep alpha
    Border      *Border      // width px + color (cloudflare, cloudinary, imgix, imagekit)
    Radius      *Radius      // rounded corners / "max" circle (cloudinary r, imagekit r, wsrv mask=circle)
    Mask        *Mask        // shape masks (wsrv)
    Adjust      Adjust       // brightness, contrast, saturation, gamma, hue, exposure, vibrance: pointers
    Monochrome  *Monochrome
    Duotone     *Duotone
    Colorize    *Colorize
    Gradient    *Gradient
    Tint        *Color
    Negate, Sepia, Grayscale bool
    Blur        float64      // Gaussian sigma; 0 = off. Dialects convert their radius units to sigma.
    Sharpen     float64      // sigma
    Unsharp     *Unsharp     // mode, weight, divider
    Pixelate    int
    BlurAreas   []Area       // relative source coords
    ProgressiveBlur *ProgressiveBlur
    BlurDetections  *DetectionFilter // M3
    DrawDetections  *DetectionFilter // M3
    Watermarks  []Watermark
    SVGStyle    string       // CSS injected into SVG sources
    Page        *int         // start page/frame
    Pages       *int         // number of pages/frames (-1 = all)
    DisableAnimation bool
    Video       *VideoOptions // M3
}

type Resize struct {
    Type      ResizeType   // Fit | Fill | FillDown | Force | Auto | Outside (wsrv/imagekit at_least)
    Width     Dim          // px or relative (0<r<1 or "%" dialects); zero = auto
    Height    Dim
    MinWidth  int
    MinHeight int
    ZoomX, ZoomY float64   // default 1
    DPR       float64      // default 1; affects offsets and padding
    Enlarge   bool
    Algorithm Kernel       // Lanczos3 default | Nearest | Linear | Cubic | Lanczos2 | Mitchell
}

type Dim struct { Px int; Rel float64 } // exactly one non-zero, or both zero

type Gravity struct {
    Type     GravityType // Center, North, South, East, West, NorthEast, NorthWest, SouthEast, SouthWest,
                         // Smart (libvips attention), Entropy, FocusPoint, Face, Object, ObjectWeighted
    XOffset, YOffset float64 // >=1 absolute px, <1 relative (imgproxy semantics)
    FocusX, FocusY   float64 // FocusPoint, 0..1
    FocusZoom        float64 // imgix fp-z, cloudinary z
    Classes          []string
    Weights          []float64
}

type Output struct {
    Format        Format   // Unset (source/negotiated) | JPEG | PNG | WebP | AVIF | JXL | HEIC | GIF | TIFF | BMP | ICO | SVG | MP4 | WebM | JSON | Best
    Quality       int      // 0 = default for format
    FormatQuality map[Format]int
    AutoQuality   *AutoQuality // method none|size|dssim|ml, target, min, max, allowed error
    MaxBytes      int
    Lossless      *bool
    Progressive   *bool
    ChromaSubsampling Subsampling // Auto | S420 | S422 | S444
    Effort        int          // encoder speed/effort, format-specific scale normalised 0..9
    JPEG *JPEGOptions; PNG *PNGOptions; WebP *WebPOptions; AVIF *AVIFOptions
    StripMetadata    *bool
    KeepCopyright    *bool
    StripColorProfile *bool
    ColorProfile     string  // "srgb" | "cmyk" | file name
    PreserveHDR      *bool
    DPI              int
    EnforceThumbnail *bool
}

type Delivery struct {
    Filename       string
    Attachment     *bool
    CacheBuster    string
    Expires        time.Time
    TTL            time.Duration // per-request max-age override (thumbor max_age, wsrv maxage), clamped by config
    Fallback       *SourceSpec   // per-request fallback image
    OnErrorRedirect bool         // cloudflare onerror=redirect
    SkipProcessing []Format
    Presets        []string      // for logging; already expanded
    Encoding       ResponseEncoding // Binary | DataURL (wsrv encoding=base64)
}
```

Normalisation rules every dialect relies on:

- Units: blur and sharpen are **sigma** internally. Each dialect documents its conversion (for example Cloudflare `blur` radius r → sigma r/2; imgix `blur` 0–2000 → sigma v/20).
- Colour adjustments are **multipliers** with 1 = unchanged (brightness is additive −255..255 to match imgproxy). Dialects with −100..100 scales convert linearly: `1 + v/100`.
- Dimensions are in **CSS pixels before DPR**. The planner multiplies by DPR once.
- Rotation: `Rotate` is always a multiple of 90; anything else goes into `RotateAny`.
- `Request.CanonicalKey()` serialises the effective request (after defaults, without `Dialect`, `Ignored`, `Presets`) to a stable binary form and hashes it with SHA-256. It is the result-cache key input and the ETag input.

`InfoOptions` mirrors imgproxy info options (size, format, dimensions, exif, iptc, xmp, video_meta, colorspace, bands, sample_format, pages_number, alpha, crop, palette, average, dominant_colors, blurhash, thumbhash, phash, calc_hashsums, detect_objects, classify).

## 5. Dialect abstraction

```go
package dialect

type Parser interface {
    Name() string
    // Parse turns the part of the URL after the mount into a request. It must not do I/O.
    Parse(r *Input) (*transform.Request, error)
    // Verifier returns the signature scheme(s) for this dialect, or nil if unsigned.
    Verifier() sign.Verifier
}

type Input struct {
    Path   string      // escaped path after mount prefix
    Query  url.Values
    RawQuery string
    Host   string
    Header http.Header // Accept, client hints; never used for cache-key-relevant decisions except negotiation
    Mount  Mount       // bound source name, dialect options
}
```

Mounts are configured per dialect (`dialects.<name>.prefix`, `dialects.<name>.hosts`, `dialects.<name>.source`). Router precedence: exact host binding → longest path prefix → `XI_DIALECTS_DEFAULT` (imgproxy). Details in `URL-DIALECTS.md`.

## 6. Source abstraction

```go
package source

type Source interface {
    Name() string
    Type() string // http, s3, gcs, azure, fs, ftp, sftp
    // Fetch returns a streaming body; the fetcher enforces limits, the source only honours ctx.
    Fetch(ctx context.Context, key Key, opt FetchOptions) (*Object, error)
}

type Key struct {
    Source string   // named source
    Path   string   // object key / path / absolute URL for http sources
}

type FetchOptions struct {
    MaxBytes        int64
    IfNoneMatch     string
    IfModifiedSince time.Time
    Header          http.Header // passthrough headers (allowlisted)
}

type Object struct {
    Body         io.ReadCloser
    Size         int64          // -1 unknown
    ContentType  string
    ETag         string
    LastModified time.Time
    CacheControl string
    Expires      time.Time
    NotModified  bool
}

// Resolver maps a SourceSpec to a Key using the configured sources; it IS the allowlist.
type Resolver interface {
    Resolve(spec transform.SourceSpec) (Key, error) // errs.KindForbidden when nothing matches
}
```

Source string resolution order: `xi://<name>/<key>` explicit; `s3://bucket/key`, `gs://`, `abs://container/blob`, `local:///path`, `ftp://`, `sftp://` (imgproxy-compatible schemes) matched to a source of that type whose bucket/host list contains the target; absolute `http(s)://` matched against each http source's `allow` patterns (host globs + optional path prefix); relative keys resolved against the dialect mount's bound source. No match → 403 unless dev mode, in which case an implicit unrestricted `http` source is used (SSRF protections still apply).

Backends (under `internal/fetch/`): `httpsrc` (SSRF-safe transport, see §10), `s3src` (aws-sdk-go-v2 with custom endpoint and path-style for R2/MinIO/Spaces/B2), `gcssrc`, `azuresrc`, `fssrc` (uses Go `os.Root` so `..` and symlinks cannot escape the root), `ftpsrc` (jlaffaye/ftp, explicit TLS option), `sftpsrc` (pkg/sftp with pinned host keys; no TOFU).

## 7. Fetcher and original cache

`fetch.Fetcher` wraps sources:

- **Coalescing**: `singleflight.Group` keyed by `sourceName + "\x00" + path + "\x00" + versionHint`. Ten concurrent requests for ten sizes of one original download it once.
- **Original cache**: optional `cache.Store` namespace `orig/`. Default is an in-memory byte-bounded LRU (`XI_CACHE_ORIGIN_MEMORY_BYTES`, default 256 MiB, TTL 60 s) because most size bursts are close in time. Can be pointed at disk/redis/S3 for multi-replica setups.
- **Limits**: `Content-Length` checked before reading; body wrapped in a counting reader that aborts at `XI_LIMITS_MAX_SRC_BYTES` (default 50 MiB). Download timeout per attempt (`XI_FETCH_TIMEOUT`, 10 s). One retry for idempotent network errors on sources that allow it.
- **Buffering** (go-standards §4): originals are read straight into C-allocated memory that vips will own (`vips.NewBuffer(n)` + `io.ReadFull`), never into pooled Go slices. The fetcher only sees the small interface below, so it stays pure Go and tests inject a Go-memory implementation. Buffers are reference-counted so coalesced requests and the origin cache share one allocation; the last `Release` frees it (production: `vips.Buffer`, which implements `fetch.Buffer` and unrefs its `vips_blob`; `app` injects `vips.NewBuffer` as the `Allocator`).

```go
type Buffer interface {
    Bytes() []byte   // read-only view; valid until the final Release
    Retain()
    Release()
}
type Allocator func(n int) (Buffer, error)
```
- **Request dedup**: in addition to fetch coalescing, identical in-flight *requests* (same result-cache key) are collapsed with a second `singleflight.Group`, as imagor does.
- **Conditional requests**: if the client sent `If-None-Match` and the result ETag can be derived from source ETag alone, the fetcher issues a conditional request and a 304 short-circuits the pipeline.

## 8. Processing pipeline

### 8.1 Planner (pure Go)

```go
package plan

type Plan struct {
    Load     LoadSpec   // loader, shrink-on-load factor, page, n, access (Sequential|Random), fail_on
    Steps    []Step     // ordered, typed descriptors
    Output   EncodeSpec
    Chained  []*Plan    // subsequent pipelines operate on the materialised result
    Estimate Cost       // peak pixel bytes, used by limiter
}

type Step interface{ step() }      // sealed sum type
type ResizeStep  struct{ ScaleX, ScaleY float64; Kernel Kernel; Premultiply bool }
type CropStep    struct{ Rect image.Rectangle }
type SmartCropStep struct{ W, H int; Interesting Interesting }
type RotateStep  struct{ Angle int }
// … one struct per operation

func Build(req *transform.Request, meta codec.ImageMeta, d Defaults) (*Plan, error)
```

Fixed step order within one pipeline (imgproxy-compatible, chosen for sequential access):

1. **Load** with shrink-on-load: JPEG `shrink` 2/4/8, WebP/HEIF/AVIF `scale`/thumbnail, SVG/PDF `scale`/`dpi`, RAW embedded preview (see FORMATS.md). The planner computes the final scale from header dimensions, then picks the largest integer shrink that keeps the image ≥ target.
2. **Trim** (forces random access; planner flips `Load.Access`).
3. **Pre-crop** (`crop`, thumbor `AxB:CxD`, imgix `rect`) in source coordinates, EXIF-orientation-aware.
4. **Resize** remainder with Lanczos3 (premultiplied alpha, linear light optional via `XI_PROCESSING_LINEAR_RESIZE`).
5. **Colour to working space**: import ICC, convert to sRGB (or keep 16-bit when preserving HDR).
6. **Orientation**: EXIF auto-rotate, then `rotate`, then `flip`.
7. **Target crop** by gravity (compass + offsets, focus point, smart = `vips_smartcrop` attention/entropy, face/object in M3).
8. **Geometry fill**: extend / extend-aspect-ratio / padding / arbitrary rotate / border / radius / mask; background flatten when output lacks alpha or background set.
9. **Filters**: adjust (brightness, contrast, saturation, gamma, hue), monochrome, duotone, colorize, tint, sepia/negate/grayscale, blur, sharpen/unsharp, pixelate, blur areas, progressive blur, gradient, detection blur/draw.
10. **Watermarks** in order.
11. **Output colour**: ICC transform/embedding, strip metadata (keep copyright if asked), DPI, orientation tag reset.
12. **Encode** (see §8.3).

Animated images carry all frames as one tall "toilet roll" image with `page-height`; every geometric step is applied per page by the executor (libvips handles most ops natively with `page-height`; crop/extend/rotate are done with `vips_arrayjoin` per frame). Frame limits are checked at probe time.

### 8.2 Executor (cgo)

libvips is demand-driven: each call (`resize`, `crop`, `embed`, …) returns a new image node that references its input; no pixels are computed until a sink (save to buffer) pulls regions through the graph in parallel tiles. The executor:

- Wraps every node in `vips.Image` (a Go handle with explicit `Close()`; nodes are tracked in a per-request arena and all unref'd in one `defer arena.Close()` so error paths never leak).
- Never calls `copy_memory` unless a step needs random access on a sequential source (trim, smartcrop, arbitrary rotate of a non-random loader, chained pipeline boundary). The planner decides this, not the executor.
- Runs the whole job on one locked OS thread (`runtime.LockOSThread`), and calls `vips_error_clear(); vips_thread_shutdown()` before unlocking (go-standards §4).
- Checks `ctx.Err()` between steps. cgo calls cannot be interrupted, so for the long sink (save) a watcher tied to `ctx` sets the image's kill flag (`vips_image_set_kill`) through a `vips.Kill` helper; libvips aborts at the next tile.
- Captures errors with `vips_error_buffer_copy()` at the failure site in the shim; the HTTP status comes from *which* operation failed (load → 422 `Corrupt`, save → 500), never from message text.
- Uses `fail_on=warning` for untrusted sources (configurable `none|truncated|error|warning`).

```go
package exec

type Executor interface {
    Run(ctx context.Context, p *plan.Plan, src vips.Buffer) (*Result, error)
}
type Result struct {
    Body        vips.Buffer   // encoded bytes, written zero-copy then Released
    Format      codec.Format
    Width, Height int
    DPR         float64
    ContentType string
}
```

### 8.3 Encoding and quality loops

`codec/encode` owns per-format savers (options in FORMATS.md). Quality selection precedence: `Output.Quality` → `FormatQuality[f]` → config per-format default → global default (80). `max_bytes` and auto-quality run a bounded binary search (≤ 6 saves) over quality; auto-quality by DSSIM uses an in-house SSIM implementation on a downscaled luma copy (the `dssim` CLI is AGPL and is not used). Best-format (M2) encodes candidate formats from the decision matrix and picks the smallest that meets the quality target, skipped above `XI_FORMATS_BEST_MAX_PIXELS`.

## 9. Caching

### 9.1 HTTP semantics (always on)

| Header | Rule |
|---|---|
| `Cache-Control` | Success: `public, max-age=<TTL>, stale-while-revalidate=<TTL/10>`; add `immutable` when the URL is signed and carries a cache buster or content hash. Source `Cache-Control` passthrough optional (`XI_CACHE_HEADERS_PASSTHROUGH`). Errors: `no-store` (4xx signature/validation) or `public, max-age=XI_CACHE_HEADERS_ERROR_TTL` (default 10 s) for upstream failures. |
| `ETag` | Strong: `"xi1-" + base64url(sha256(canonicalKey ‖ outputFormat ‖ sourceValidator ‖ XI_CACHE_ETAG_BUSTER))[:22]`. `sourceValidator` is source ETag, else Last-Modified, else SHA-256 of source bytes. |
| `Last-Modified` | Source Last-Modified when known. |
| `Vary` | `Accept` only when format was negotiated (format not fixed by URL). Add `Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width` when client hints influenced the result. Never `Vary: *`. |
| `Content-DPR` | Set when DPR ≠ 1 was applied. |
| `Content-Disposition` | `inline` (or `attachment`) with RFC 6266 `filename*` from `filename` option or source basename + new extension. |
| `Content-Type`, `Content-Length`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox` for SVG output. |
| `X-Xi-Ignored` | Debug only (`XI_OBSERVABILITY_DEBUG_HEADERS`): options parsed but not applied. |

`If-None-Match` / `If-Modified-Since` → 304 before processing whenever the ETag is computable without processing (source validator known).

### 9.2 Pluggable stores (M2)

```go
package cache

type Store interface {
    Get(ctx context.Context, key string) (*Entry, error) // returns ErrMiss on miss
    Set(ctx context.Context, key string, e *Entry, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    Close() error
}

type Entry struct {
    Body        []byte
    ContentType string
    ETag        string
    LastModified time.Time
    Headers     map[string]string // Content-Disposition, Content-DPR, Vary
    CreatedAt   time.Time
}
```

Backends: `memory` (W-TinyLFU via otter, byte-bounded), `disk` (sharded dirs, atomic rename, size-bounded LRU janitor), `s3cache` (any S3-compatible, lifecycle rules for expiry), `redis` (`SET PX`, values > 1 MiB skipped by default). A `Tiered` store composes them (memory → disk/redis → s3). Writes are async with a bounded queue; a full queue drops writes (metric `xi_cache_write_dropped_total`). Result key: `res/v1/` + hex(sha256(canonicalKey ‖ negotiated format ‖ sourceValidator)).

## 10. Security architecture (summary; decisions in ADR 0003)

- **Signing**: `sign.Verifier` per dialect; multiple keys accepted for rotation; constant-time compare. Unsigned dialect traffic is governed by `XI_SECURITY_UNSIGNED_POLICY` = `allow | restricted | deny` (restricted = only allowlisted widths, qualities and formats, like Next.js `deviceSizes`).
- **Encrypted sources**: AES-CBC, PKCS#7, 16-byte IV prefix, URL-safe base64; key 16/24/32 bytes hex (imgproxy-compatible).
- **Allowlist** = the configured sources. No sources and no dev mode → startup error.
- **SSRF**: custom `net.Dialer.Control` checks the *connected* IP (defeats DNS rebinding), denies loopback, RFC 1918, link-local (incl. 169.254.169.254), CGNAT 100.64/10, ULA fc00::/7, multicast, unspecified, IPv4-mapped/NAT64 forms of those, and non-allowlisted ports (default 80/443). Redirects: max `XI_FETCH_MAX_REDIRECTS` (5), each hop re-resolved against the allowlist and SSRF rules, HTTPS→HTTP downgrade refused. Proxy env vars ignored unless configured.
- **Resource limits**: max source bytes, max source pixels (default 50 MP, checked from header before decode), max frames (default 256) and max frame pixels, max result dimension (default 8192), max SVG size, request and download timeouts, `vips_block_untrusted_set(TRUE)` plus explicit re-enable of the loaders we ship.
- **SVG**: sanitised (scripts, event handlers, `foreignObject`, external `href`/`xlink:href`, external CSS `@import`/`url()`, entities/DOCTYPE removed) before librsvg or passthrough; librsvg never fetches external resources.
- **ImageMagick** (PSD only): `policy.xml` denies all coders except PSD/PSB, all delegates, `@` file reads, URL/HTTP, MVG/MSL/TEXT; resource limits (memory 256 MiB, disk 0, time 10 s, width/height 16k).

## 11. Concurrency, backpressure and memory

Follows the imgproxy/imagor pattern mandated by `CLAUDE.md` and go-standards §3–4: libvips concurrency fixed at 1, parallelism comes from request-level semaphores.

```
requests ─► [http.Server, all timeouts set; HTTP/2 MaxConcurrentStreams sized to the queue]
          ─► fetch   (outside the semaphores; bounded by XI_FETCH_MAX_CONCURRENT, default 256)
          ─► limiter.Acquire(ctx)
                ├─ queue.TryAcquire(1)      capacity = workers + XI_PROCESSING_QUEUE_SIZE → fail fast: 429 + Retry-After
                ├─ workers.Acquire(ctx, 1)  capacity = XI_PROCESSING_WORKERS (default GOMAXPROCS);
                │                           waits at most XI_PROCESSING_QUEUE_TIMEOUT (5 s) → 503 + Retry-After
                └─ memory.Acquire(ctx, est) optional weighted semaphore, off unless XI_PROCESSING_MEMORY_BUDGET is set
          ─► one locked OS thread per job; vips_concurrency_set(1)
```

- Both semaphores are `golang.org/x/sync/semaphore`; no channel worker pools. GOMAXPROCS follows the container CPU limit natively (Go ≥ 1.25), so workers scale with `resources.limits.cpu`. imgproxy defaults to 2× cores; we start at 1× and change it only with benchmark evidence.
- `XI_VIPS_CONCURRENCY` exists (default 1) for a latency-tier experiment (fewer workers, more threads per image); changing it requires benchmarks per go-standards §8.
- Optional memory budget: when set, each job acquires its planner estimate of peak decoded bytes (shrunk-on-load width × height × bands × bytes-per-sample × frames, ×2 for random-access steps). A single job above the whole budget is rejected with 422 instead of deadlocking.
- Metrics: `xi_queue_depth`, `xi_semaphore_wait_seconds`, `xi_workers_busy`, `xi_overload_total{reason}`.
- libvips operation cache disabled (`vips_cache_set_max(0)`, `vips_cache_set_max_mem(0)`): we cache results, not operations.
- Memory: C memory is bounded by header-checked input limits × worker count. jemalloc via `LD_PRELOAD` (`MALLOC_ARENA_MAX=2` as glibc fallback; without jemalloc, periodic `debug.FreeOSMemory()` + `malloc_trim(0)`). `GOMEMLIMIT` set at startup to 30% of cgroup `memory.max` (`XI_PROCESSING_GO_MEMORY_FRACTION`), never to the full limit. `vips_tracked_get_mem`/`_highwater` exported as gauges.
- `/readyz` turns 503 when draining; optional `XI_SERVER_READY_QUEUE_THRESHOLD` makes it 503 when the queue is more than N% full (off by default to avoid flapping).
- Graceful shutdown via `signal.NotifyContext`: SIGTERM → readyz 503 → wait `XI_SERVER_SHUTDOWN_DELAY` (5 s) → `srv.Shutdown` drains for up to `XI_SERVER_SHUTDOWN_TIMEOUT` (30 s) → `vips_shutdown()`.
- A C crash (SIGSEGV) kills the process and the orchestrator restarts it; the recover middleware only catches Go panics (→ 500, counted).

## 12. Failure modes and status codes

Errors carry an `errs.Kind`; the table below is the single mapping (`errs.Status`). `errs` errors implement `HTTPStatus() int` and `PublicMessage() string`, and `server.writeError(w, r, err)` resolves them with `errors.AsType` (go-standards §6); anything else is a 500 with a generic body.

| Kind | Examples | Status | Cache-Control |
|---|---|---|---|
| `InvalidURL` | cannot parse path, bad base64, unknown option in strict mode, bad arg type | 400 | no-store |
| `Unauthorized` | missing/bad signature, bad `enc/` payload | 403 | no-store |
| `Forbidden` | source not allowlisted, SSRF block, bucket not allowed, security option not allowed, only-presets violation | 403 | no-store |
| `Expired` | `expires`, imgix `expires`, imagekit `ik-t` in the past | 404 (imgproxy parity) | no-store |
| `NotFound` | source 404/NoSuchKey/ENOENT | 404 | error TTL |
| `SourceTooLarge` / `LimitExceeded` | bytes, pixels, frames, result dimension, cost > budget | 422 | error TTL |
| `Unsupported` | undetectable/unsupported format, format not enabled in this image (video on slim) | 422 (imgproxy parity) | error TTL |
| `Corrupt` | decoder failure | 422 | error TTL |
| `HashMismatch` | `hashsum` check failed | 422 | no-store |
| `NotAcceptable` | strict Accept with no acceptable format | 406 | no-store |
| `UpstreamError` | origin 5xx, connection refused, TLS failure | 502 | error TTL |
| `UpstreamTimeout` | download timeout | 504 | error TTL |
| `Overloaded` | queue full (imgproxy parity) | 429 + Retry-After | no-store |
| `Unavailable` | worker wait timeout, draining | 503 + Retry-After | no-store |
| `Timeout` | processing exceeded our request deadline (`context.Cause` is our timeout) | 504 (go-standards §6; imgproxy uses 503) | no-store |
| `Canceled` | client disconnected | 499 (log only) | — |
| `Internal` | bug, panic, vips error not attributable to input | 500 | no-store |

When a fallback image is configured (global or per request), `NotFound`, `UpstreamError`, `UpstreamTimeout`, `Unsupported` and `Corrupt` serve it with `XI_FALLBACK_STATUS` (default 200) and a short TTL. Cloudflare `onerror=redirect` returns 302 to the source URL for the same kinds. Error bodies are `text/plain` by default, JSON (`{"error":{"kind":…,"message":…,"request_id":…}}`) when `Accept: application/json`; development mode adds a stack trace.

## 13. Observability

- Logs: `log/slog` JSON to stdout; one access line per request (`request_id`, dialect, source name, status, bytes, stage timings, cache status, output format, dimensions). Source URLs are logged with query strings stripped; signatures never logged.
- Metrics (`/metrics` on the admin listener `XI_ADMIN_ADDR`, default `127.0.0.1:9090`; set `:9090` to scrape from outside the pod), client_golang on a private registry: `xi_requests_total{dialect,status,format}`, `xi_request_duration_seconds`, `xi_stage_duration_seconds{stage}`, `xi_source_fetch_duration_seconds{source,type}`, `xi_source_bytes_total`, `xi_fetch_coalesced_total`, `xi_cache_requests_total{store,result}`, `xi_queue_depth`, `xi_workers_busy`, `xi_vips_mem_bytes`, `xi_vips_mem_highwater_bytes`, `xi_vips_allocs`, Go runtime collectors.
- Tracing: OpenTelemetry OTLP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; one span per stage, W3C propagation, outbound propagation to sources off by default.
- `/healthz` (liveness, no vips calls), `/readyz` (vips initialised, sources' static config valid, not draining). pprof (incl. `goroutineleak`) on the admin listener only when `XI_ADMIN_PPROF=true`.

## 14. Docker and base image build

Two repositories of artefacts, one Git repo:

```
docker/
  base/                        forked from imgproxy-docker-base (MIT; imgproxy itself is Apache-2.0), attribution kept
    versions.sh                pinned versions + SHA256 of every tarball
    build-deps.sh              builds into /opt/xtandard (prefix)
    Dockerfile                 ubuntu:noble builder → FROM scratch export stage
  Dockerfile                   app: FROM base → go build → runtime
  imagemagick-policy.xml
```

**Base image** `ghcr.io/xantiagoma/xtandard-image-base:<vips-version>-r<N>-<flavor>` (flavor `slim` | `full`), built weekly and on `versions.sh` change, amd64 + arm64 on native runners, cosign-signed, SBOM (syft) attached.

Contents of `/opt/xtandard`:

| Path | Contents |
|---|---|
| `lib/` | (slim, M1) libvips 8.18.x, glib/gobject/gio (minimal), expat, libjpeg-turbo (+ optional mozjpeg), libspng, libpng, zlib-ng, cgif, quantizr, libwebp, libheif + libde265 + dav1d + aom + kvazaar, libjxl + highway + brotli, libtiff, lcms2, libexif, librsvg + cairo + pango + harfbuzz + freetype + fontconfig + fribidi, OpenJPEG, libultrahdr, jemalloc; added in M2 (WP-66): OpenEXR/Imath, LibRaw 0.22.x, PDFium (prebuilt, BSD/Apache), ImageMagick 7 (PSD coder only, no delegates). `full` adds FFmpeg 8 LGPL (`libavformat`, `libavcodec`, `libavutil`, `libswscale`; decoders h264/hevc/vp8/vp9/av1 via dav1d, demuxers mp4/mov/matroska/webm/gif; encoders only libvpx-vp9 and libaom-av1 for GIF→video, see open questions) and ONNX Runtime 1.30 CPU. |
| `lib/pkgconfig`, `include/` | build stage only; stripped from runtime |
| `share/fonts/` | Noto Sans + DejaVu (OFL / Bitstream-Vera licenses) for text watermarks |
| `share/models/` | `full`: YuNet face model (MIT), YOLOX-s object model (Apache-2.0), with LICENSE files |
| `etc/ImageMagick-7/policy.xml` | restrictive policy (§10) |
| `share/licenses/` | every dependency's license + `SOURCES.md` with source URLs (LGPL compliance) |

All libraries built with `-O3 -fstack-protector-strong -D_FORTIFY_SOURCE=2`, RPATH `$ORIGIN`, stripped. No GPL component: `build-deps.sh` runs a license check that fails the build on GPL/AGPL (see ADR 0006).

**App image** (`docker/Dockerfile`, multi-stage):

1. `FROM ghcr.io/xantiagoma/xtandard-image-base:<pin>-<flavor> AS build` with Go 1.27.1 copied in; `CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=… -X main.commit=…" [-tags ffmpeg]` (unstripped artefact kept for symbolication; `default.pgo` picked up automatically).
2. `FROM ubuntu:noble` runtime (glibc required; `ca-certificates`, `tzdata` only), `COPY --from=build /opt/xtandard/lib /opt/xtandard/lib` (runtime subset), binary to `/usr/local/bin/xtandard-image`.
3. `ENV LD_LIBRARY_PATH=/opt/xtandard/lib LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 MALLOC_ARENA_MAX=2 MAGICK_CONFIGURE_PATH=/opt/xtandard/etc/ImageMagick-7 FONTCONFIG_PATH=/opt/xtandard/etc/fonts VIPS_BLOCK_UNTRUSTED=1`.
4. `USER 65532:65532` (user `xtandard`, no shell needed), read-only root filesystem compatible (writes only to `/tmp` if disk cache is enabled at a configured path).
5. `EXPOSE 8080 9090`; `HEALTHCHECK CMD ["/usr/local/bin/xtandard-image","health","--url","http://127.0.0.1:8080/healthz"]` (no curl in the image).
6. `ENTRYPOINT ["/usr/local/bin/xtandard-image"]`, `CMD ["serve"]`.

Tags: `ghcr.io/xantiagoma/xtandard-image:{latest,slim,<ver>,<ver>-slim}` and `:{full,<ver>-full}`. Size targets: slim ≤ 110 MB compressed, full ≤ 170 MB. CI integration test boots each final image with `docker compose` (plus MinIO and a static origin; go-standards §8, no testcontainers) and runs the golden corpus over HTTP for both architectures (arm64 on native runners).

## 15. Testing architecture

- Unit: every dialect parser is table-tested URL → expected `transform.Request` (JSON fixtures in `testdata/`), including round-trips generated by upstream SDKs (imgproxy URL builder, `@imgix/js-core`, `cloudinary` SDK, `unpic` transformers) to prove compatibility.
- Planner: request + meta → expected plan, pure Go.
- Golden images: `testdata/golden/<pkg>/<case>.png` via `internal/testutil/golden.Compare`; compared with the in-house SSIM/DSSIM (`internal/testutil/ssim`, Wang et al. 2004 on luma) ≤ tolerance per case (default `golden.DefaultTolerance` = 0.015, calibrated so a JPEG q85 re-encode passes and a 1 px shift fails), regenerated with `GOLDEN_UPDATE=1 go test ./...` or `-update` on packages importing the harness. See `testdata/README.md`.
- Fixtures per format (JPEG CMYK/progressive/EXIF-rotated, PNG 16-bit/palette/APNG, GIF animated, WebP animated, AVIF 10-bit, HEIC, JXL, TIFF multipage, BMP variants, ICO, SVG malicious set, PDF, PSD, RAW: CR3, NEF, ARW, DNG) in `testdata/fixtures/<format>/` (the M1 set landed with WP-36; M2 formats are added by their WPs), all with redistributable licenses recorded in `testdata/LICENSES.md`.
- Security: SSRF matrix (rebinding test with custom resolver), signature vectors from upstream docs, fuzzing (`go test -fuzz`) for every parser, the SVG sanitiser and the BMP/ICO decoders.
- Integration: `//go:build integration` tests against the Docker image + MinIO + a local HTTP origin started with `docker compose` in CI (go-standards §8).
