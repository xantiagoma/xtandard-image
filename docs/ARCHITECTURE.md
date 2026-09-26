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
| 2 | Route: choose dialect by host binding, then longest path prefix, then default | `dialect/router` | `Dialect`, stripped path | 404 (no route) |
| 3 | Parse: dialect grammar → `transform.Request` (+ `Ignored` list) | `dialect/<name>` | `*transform.Request` | 400 malformed / unknown option (strict) |
| 4 | Authenticate: dialect signature verifier, expiry | `security/sign` | ok | 403 bad signature, 404 expired (imgproxy-compatible) |
| 5 | Resolve source: decrypt `enc/`, map source string to named source, apply allowlist | `source`, `security/crypt` | `source.Ref` | 403 not allowed, 400 bad source |
| 6 | Normalise and validate: defaults filled, limits clamped, `Validate()` (presets were expanded in place at stage 3, since URL order decides overrides) | `transform` | frozen request | 400, 422 (result dimension limit) |
| 7 | Negotiate: output format from explicit format, `Accept`, client hints, config | `negotiate` | `OutputFormat`, `Vary` set | 406 (only when `XI_FORMATS_STRICT_ACCEPT`) |
| 8 | Result cache lookup (optional, §9.2): key = hash(canonical request, source identity, format or Accept weights, hints, build); runs right after stage 5 | `cache` | hit → stage 14 | cache errors are logged, never fatal |
| 9 | Fetch: singleflight by source identity, origin cache, size limit, conditional request | `fetch` | `*fetch.Original` (bytes + metadata) | 404, 502, 504, 413→422 |
| 10 | Admission: `queue.TryAcquire` (fail fast), then `workers.Acquire(ctx)` with queue timeout; optional memory budget | `limiter` | permit | 429 queue full, 503 wait timeout (both with `Retry-After`) |
| 11 | Probe: magic-byte sniff, header-only decode, pixel/frame limits, SVG sanitise | `codec` | `ImageMeta` | 415→422 unsupported, 422 limits |
| 12 | Plan + execute: planner builds steps, executor chains libvips ops lazily, pixels computed on save | `pipeline/plan`, `pipeline/exec` | `*vips.Image` graph | 422 corrupt, 500 |
| 13 | Encode: format saver, quality/auto-quality/max-bytes loop | `codec/encode` | vips-owned output buffer + content type | 500 |
| 14 | Respond: headers (Cache-Control, ETag, Last-Modified, Vary, Content-DPR, Content-Disposition), write body; 304 and HEAD are answered earlier without processing (§9.1); store in result cache async | `server/response.go` | bytes on the wire | 499 client gone (logged) |

Special paths bypass stages: `raw:1` / `skip_processing` streams source bytes after stage 9 (no admission, no decode, still allowlisted, SSRF-checked, size-limited and content-type-checked against an allowlist of image MIME types); `/info` stops after stage 11 and returns JSON; `fallback_image` replaces a failed stage 9–12 with a configured image and `XI_FALLBACK_STATUS`.

Special paths as implemented (WP-34c, `internal/server/special.go`):

- `raw`: route → resolve → fetch → respond. Processing options are ignored (imgproxy parity), request security options still adjust the size limit, and raw never falls back. Neither raw nor skip-processing takes an admission permit: they do no libvips work and their bytes are already bounded by the size limit, so a permit would only let cheap passthroughs starve processing.
- `skip_processing` (`XI_FORMATS_SKIP_PROCESSING` ∪ the request's list) is matched on the magic-byte sniff right after the fetch, before admission. A format only the prober recognises (or SVG passthrough decided by negotiation) is matched after the probe, while the permit is already held.
- The `Content-Type` of every unprocessed response comes from the sniff (`server.Sniffer`, adapted from `codec.Sniff` in `app`), never from the origin header; anything whose sniffed type is not `image/*` is 422 `Unsupported`. SVG is sanitised before it is streamed.
- Fallback: after the source resolved, an eligible failure (§12) re-runs stages 9–13 with the same transform request on the fallback source: the request's `fallback_image_url` when `XI_FALLBACK_ALLOW_REQUEST_URL` is on and the allowlist accepts it, else `XI_FALLBACK_SOURCE` (resolved at startup; a source the allowlist refuses fails startup). The response has status `XI_FALLBACK_STATUS`, `Cache-Control: public, max-age=XI_FALLBACK_TTL` (`no-store` when 0), no `Expires`/`Last-Modified`, and `X-Xi-Fallback: 1` with debug headers; ETag, Vary and `Content-Disposition` are the §9.1 ones of the fallback result (a `fallbackWriter` rewrites only the 200, so `respond` stays unaware of fallbacks). A fallback is always a full response: conditional request headers are ignored while producing it, because a 304 would refresh a cached fallback with the success `Cache-Control` and outlive `XI_FALLBACK_TTL`. If the fallback fails too, the original error is returned; the fallback never falls back.
- Unprocessed responses get the §9.1 headers unchanged: the ETag uses the sniffed format and no quality delta, 304 and HEAD are answered without streaming, and `Content-Disposition` (§9.1) takes the sniffed extension (e.g. `.jp2` for a format no encoder writes). Client-hint `Vary`/`Content-DPR` are not sent (the bytes do not depend on them; `Accept-CH` still is); SVG passthrough decided by negotiation keeps its `Vary: Accept`.

Stages 1–8 are pure Go and must complete in well under 1 ms for cache hits. Fetch happens **outside** the processing semaphore so slow origins never hold CPU slots.

Implementation order (WP-34a, `server.process`): route+parse+authenticate (2–4) → normalise (6: `ApplyClientHints`, `ApplyDefaults`, `Validate`, result-dimension limit) → resolve (5) → result cache lookup (8, WP-52, §9.2; a hit goes straight to respond) → fetch (9) → admission (10) → probe (11: SVG sanitised by magic-byte sniff *before* the header probe, then limits re-checked on the probed meta) → negotiate (7, after the probe because `negotiate.Decide` needs source meta) → process (12–13) → respond (14). Conditional requests short-circuit to 304 right after the fetch when the URL fixes the format, else after negotiation; HEAD stops after negotiation; the processor never runs for either (§9.1). Every stage records `xi_stage_duration_seconds{stage}` and an access-log timing; fetch, probe and process get spans. The server sees stages 11–13 only through `server.Prober` and `server.Processor` (`internal/server/server.go`); `app` adapts codec (WP-07: `codec.Probe`) and pipeline/encode (WP-25/26a/30: `exec.Executor` with an `encode.Encoder`) to them; `ProcessOutput. Stages 9–13 live in `server.produce`, so the fallback can re-run them on another source. Info requests (WP-35, `server/info.go`; the router sends `<mount>/info/...` to dialects implementing `router.InfoParser`) run normalise → resolve → fetch → admission → `server.Inspector` (stage 11: SVG sanitised first, limits re-checked; `app` adapts `codec.Inspect`, a probe plus a header-only load that reads EXIF fields, libexif tag titles, `iptc-data` and `xmp-data`, parsed by the pure-Go `codec/imagemeta`) → `respondInfo` (JSON with imgproxy field names, the processing ETag scheme, `Cache-Control: max-age=XI_CACHE_HEADERS_TTL, public`, 304 on `If-None-Match`, `X-Xi-Ignored` with debug headers). Info options not computed yet are parsed, answered without their field and listed in `X-Xi-Ignored`; they never cause a 501.

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
  dialect/                  Dialect interface, Input/Result, error helpers, Registry, Mount
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
  cache/                    Store interface, key derivation, tiering, persisted entry framing (meta.go)
    memory/ disk/ s3cache/ redis/   backends; s3cache and redis are the only other places storage SDKs may be imported
    cachetest/              conformance suite every backend runs
  imageinfo/                pure Go: /info pixel fields (palette, average, dominant colours, BlurHash, ThumbHash, pHash) and hashsums over an RGBA copy (WP-63)
  negotiate/                Accept / client-hint parsing, format decision matrix, best-format candidates and content analysis (`Analyze`)
  limiter/                  worker slots, memory-weighted semaphore, queue with timeout
  vips/                     ONLY package that imports "C" (hand-written shim, §8.4); Image handle, Buffer (C-owned bytes), init, errors, thread lifecycle
    (shim .c/.h files)      hand-written C in the same package: owned-buffer load w/ fail_on, libraw thumbnail loader, save-to-blob, progress/kill hook
  codec/                    Format enum, magic sniffing, probe, loader selection, encoder options
    raster/                 cgo-free pixel types (Raw, Decoded) shared by the own loaders; a leaf so codec can import bmp/ico
    bmp/ ico/               own pure-Go loaders/savers, reimplemented from imgproxy's C loaders (Apache-2.0: keep upstream header, add NOTICE entry)
    raw/                    camera RAW: magic/IFD0 sniffing, header probe (vips.ProbeRaw), preview pick, flip → EXIF orientation, camera EXIF; decode via vips.Load (preview) / vips.RawLoad (demosaic)
  pipeline/
    plan/                   pure Go: (transform.Request, plan.SourceMeta) → Plan
    exec/                   Plan → libvips operations
  detect/                   (M3) ONNX Runtime face/object detection, loaded via dlopen (purego) when present
  video/                    (M3, build tag `ffmpeg`) go-astiav frame extraction
  obs/                      slog setup, Prometheus registry, OpenTelemetry, admin mux (metrics, pprof)
  server/                   http.Server + http.ServeMux, middleware, handlers (process, info, health), respond, writeError
```

Dependency rules (enforced by a `depguard`/`go-arch-lint` config in CI):

- `transform` and `errs` are leaves. Every other package may import them.
- `dialect/*` → `transform`, `preset`, `security/sign`, `security/crypt`, `errs`. Dialects never import `source`, `vips`, `pipeline`.
- `security/*` → `errs` only (and stdlib/x/crypto).
- `fetch/<backend>` → `source` (interface types), `security/ssrf`, `errs`. Backends never know about dialects or transforms.
- `fetch` → `source`, `cache`, `errs` (never its own backend subpackages; `app` constructs them).
- `pipeline/plan` → `transform`, `errs` (it defines its own `SourceMeta`; the executor maps codec/vips headers onto it).
- `pipeline/exec`, `codec` loaders/encoders → `vips`.
- `server` → everything above via interfaces; `app` is the only package that constructs concrete types. Exception: `server` calls the pure-Go `pipeline/plan.Build` directly for the thumbor `meta` response (WP-86b), with the executor's limits passed in `InfoConfig.PlanLimits`, and `plan.InfoCrop` for the imgproxy /info `crop` coordinates (WP-63). `imageinfo` is a pure-Go leaf (stdlib only); `server` and `app` use it.
- `config` imports nothing internal except `errs`. Each component declares its own `Options` struct; `app` maps `config.Config` into those options. This keeps component packages testable without the whole config tree.

Routing: `server` registers fixed routes on a stdlib `http.ServeMux` with method patterns (`GET /healthz`, `GET /readyz`, `GET /{path...}` catch-all, which also matches HEAD) and hands the catch-all to `dialect/router`; the admin listener has its own `ServeMux` (`/metrics`, `/config.schema.json` (ADR 0007), pprof). No third-party router (go-standards §2).

Build tags: `ffmpeg` (video). `server` tests use fakes of its consumer-side interfaces (`server.Processor`, `server.Prober`, fetcher, resolver, router), so they need no tag. ONNX needs no tag (runtime dlopen).

## 4. The TransformRequest model

`internal/transform` is plain data. No methods do I/O. Zero values are meaningful ("not set"); defaults are applied by `ApplyDefaults(cfg Defaults)` so that the canonical key only contains effective values.

```go
package transform

type Request struct {
    Kind        Kind           // Process (zero) | Info | Raw
    Dialect     string         `key:"-"` // for metrics/logging only; not part of cache key
    Source      SourceSpec
    Pipelines   []Pipeline     // len>=1 after ApplyDefaults; >1 means chained pipelines (imgproxy "-", cloudinary "/"); max 16
    Output      Output
    Delivery    Delivery
    ClientHints ClientHints    // hints this URL opts into (imgix ch=, w_auto, dpr_auto, width=auto); server resolves them before CanonicalKey
    Limits      LimitOverrides // only honoured if XI_SECURITY_ALLOW_SECURITY_OPTIONS
    Info        *InfoOptions   // Kind == Info
    Ignored     []Ignored      `key:"-"` // {Option, Reason}: understood syntactically but not applied (debug header)
}

type SourceSpec struct {
    Raw      string         // as written: "https://…", "s3://b/k", "local:///p", "xi://name/key", or relative key
    Encoding SourceEncoding // Plain (zero) | Base64 | Encrypted | Relative
    Mount    string         // named source bound by dialect mount (imgix source, cloudinary cloud, …)
    Hashsum  *Hashsum       // {Algorithm md5|sha1|sha256|sha512, Digest lowercase hex}
}

type ClientHints struct { Width, DPR, SaveData bool; WidthStep int }
type LimitOverrides struct {
    MaxSrcResolution float64; MaxSrcFileSize int64; MaxAnimationFrames int
    MaxAnimationFrameResolution float64; MaxResultDimension int // zero = config value
}

type Pipeline struct {
    Resize      Resize
    Gravity     Gravity
    Crop        *Crop        // pre-resize crop in source space; unset gravity inherits Pipeline.Gravity
    CropObjects *CropObjects // M3 {Scale, Classes}
    Trim        *Trim        // {Threshold, Color *Color (nil = auto), ColorFromBottomRight, EqualHor, EqualVer}
    Rotate      int          // 0/90/180/270 after ApplyDefaults
    RotateAny   float64      // (−180, 180] degrees with Background fill (imgix rot, wsrv ro, cloudinary a)
    AutoRotate  *bool        // nil = config default
    Flip        Flip         // {H, V bool}
    Padding     *Padding     // {Top, Right, Bottom, Left}; after resize, follows DPR
    Extend      Extend       // {Enabled bool; Gravity Gravity; Mode Background|Copy (imgix clamp)|Blur (M3)}
    ExtendAR    Extend
    Background  *Color       // {R,G,B,A uint8}; nil = keep alpha
    Border      *Border      // per-side widths + Color (cloudflare, cloudinary, imgix, imagekit)
    Radius      *Radius      // per-corner px, Max = circle (cloudinary r, imagekit r, thumbor round_corner)
    Mask        *Mask        // {Shape, Trim, Background} (wsrv mask, mtrim, mbg)
    Adjust      Adjust       // pointers: Brightness, Contrast, Saturation, Gamma, Hue, Exposure, Vibrance, Red, Green, Blue, Opacity
    Monochrome  *Monochrome  // {Intensity, Color}
    Duotone     *Duotone     // {Intensity, Shadow, Highlight}
    Colorize    *Colorize    // {Opacity, Color, KeepAlpha}
    Gradient    *Gradient    // {Opacity, Color, Direction down|up|left|right|angle, Angle, Start, Stop}
    Tint        *Color
    Negate, Sepia, Grayscale, Equalize bool
    Blur        float64      // Gaussian sigma; 0 = off. Dialects convert their radius units to sigma.
    Sharpen     float64      // sigma
    Unsharp     *Unsharp     // imgproxy {Mode, Weight, Divider} + classic USM {Sigma, Amount}
    Pixelate    int
    BlurAreas   *BlurAreas   // {Sigma, Areas []Area} relative source coords
    ProgressiveBlur *ProgressiveBlur // {Sigma, Direction, Angle, Start, Stop}
    BlurDetections  *DetectionFilter // M3 {Sigma, Pixelate, Classes}
    DrawDetections  *DetectionFilter // M3
    Watermarks  []Watermark  // {Opacity, Position Gravity, Tile none|repeat|chessboard, Scale, Source *SourceSpec, Text, Font, Color, Width, Height, Rotate, Shadow}
    SVGStyle    string       // CSS injected into SVG sources
    Page        *int         // start page/frame
    Pages       *int         // number of pages/frames (-1 = all)
    DisableAnimation bool
    Video       *VideoOptions // M3 {Second, Keyframes, Tile *VideoTile, Animation *VideoAnimation}
}

type Resize struct {
    Type        ResizeType // Unset → Fit | Fill | FillDown | Force | Auto | Outside (wsrv/imagekit at_least)
    Width       Dim        // px or relative; zero = auto
    Height      Dim
    MinWidth, MinHeight int
    MaxWidth, MaxHeight int // clamp (imgix max-w/max-h)
    AspectRatio float64    // derive a missing dimension (imgix ar, cloudinary ar_, imagekit ar, bunny aspect_ratio)
    ZoomX, ZoomY float64   // default 1
    DPR         float64    // default 1; affects offsets and padding
    Enlarge     bool
    Adaptive    bool       // swap W/H when orientation differs (thumbor adaptive-*)
    Algorithm   Kernel     // Unset → Lanczos3 | Nearest | Linear | Cubic | Mitchell | Lanczos2
}

type Dim struct { Px int; Rel float64 } // at most one non-zero; both zero = auto

type Gravity struct {
    Type     GravityType // Unset → Center, North, South, East, West, NorthEast, NorthWest, SouthEast, SouthWest,
                         // Smart (libvips attention), Entropy, FocusPoint, Face, Object, ObjectWeighted
    XOffset, YOffset float64 // >=1 absolute px, <1 relative (imgproxy semantics)
    FocusX, FocusY   float64 // FocusPoint, 0..1
    FocusPx          *FocusRect // FocusPoint in source pixels (thumbor focal, cloudinary x_/y_); planner converts (WP-86a)
    FocusZoom        float64 // imgix fp-z, cloudinary z
    Classes          []string
    Weights          []float64
    FaceIndex        *int    // imgix faceindex (M3)
    FacePad          float64 // imgix facepad (M3)
    ObjectsPosition  *ObjectsPosition // imgproxy objects_position {Type compass|FocusPoint, X, Y, Proportional} (M3)
}

type Crop struct { Width, Height Dim; Gravity Gravity; AspectRatio float64; AspectEnlarge bool }

type Output struct {
    Format        Format   // Unset (source/negotiated) | JPEG | PNG | WebP | AVIF | JXL | HEIC | GIF | TIFF | BMP | ICO | SVG | MP4 | WebM | JSON | Best
    Quality       int      // 0 = default for format
    FormatQuality map[Format]int
    SlowConnectionQuality int // cloudflare scq (M2); 0 = config delta
    AutoQuality   *AutoQuality // method none|size|dssim|ml, target, min, max, allowed error
    MaxBytes      int
    Lossless      *bool
    Progressive   *bool
    PreferJPEGIfOpaque bool    // thumbor autojpg()
    ChromaSubsampling Subsampling // Auto (zero) | 4:2:0 | 4:2:2 | 4:4:4
    Effort        int          // 1..9 normalised per encoder; 0 = format default
    JPEG *JPEGOptions          // Progressive, NoSubsample, TrellisQuant, OvershootDeringing, OptimizeScans, QuantTable
    PNG  *PNGOptions           // Interlaced, Quantize, QuantizationColors, Compression (zlib 0–9), AdaptiveFilter
    WebP *WebPOptions          // Compression lossy|near_lossless|lossless, SmartSubsample, Preset
    AVIF *AVIFOptions          // Subsample auto|4:2:0|4:4:4
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
    Immutable      bool          // cloudinary fl_immutable_cache
    Fallback       *SourceSpec   // per-request fallback image
    OnErrorRedirect bool         // cloudflare onerror=redirect
    SkipProcessing []Format
    BypassCache    bool          `key:"-"` // imgproxy bypass_cache (M2)
    CacheTags      []string      // imgproxy cache_tags (M2)
    Presets        []string      `key:"-"` // for logging; already expanded
    Encoding       ResponseEncoding // Binary (zero) | DataURL (wsrv encoding=base64)
}
```

Fields added in WP-03 beyond the original sketch, each so that an option in `URL-DIALECTS.md` has a home:
`Request.ClientHints` (imgix `ch`, cloudinary `w_auto`/`dpr_auto`, cloudflare `width=auto`); `Resize.MaxWidth/MaxHeight` (imgix `max-w/max-h`), `Resize.AspectRatio` (`ar`), `Resize.Adaptive` (thumbor `adaptive-*`); `Gravity.FaceIndex/FacePad` (imgix `facearea`), `Gravity.ObjectsPosition` (imgproxy `objects_position`); `Crop.AspectEnlarge` (second arg of `crop_aspect_ratio`); `Trim.ColorFromBottomRight` (thumbor `trim:bottom-right`); `Extend.Mode` (imgix `fit=clamp`, `fill=blur`); `Mask.Trim/Background` (wsrv `mtrim`, `mbg`); `Adjust.Red/Green/Blue` (thumbor `rgb`) and `Adjust.Opacity` (cloudinary `o_`); `Pipeline.Equalize` (thumbor `equalize`); `Unsharp.Sigma/Amount` (imgix `usm`, thumbor `sharpen`); `DetectionFilter.Pixelate` (cloudinary `e_pixelate_faces`); `Watermark.Tile/Font/Color` (imgproxy `re`/`ch`, imgix `txt-font`/`txt-color`); `Output.SlowConnectionQuality` (cloudflare `scq`), `Output.PreferJPEGIfOpaque` (thumbor `autojpg`); `PNGOptions.Compression/AdaptiveFilter` (wsrv `l`, `af`, imaginary `compression`); `Delivery.Immutable` (cloudinary `fl_immutable_cache`), `Delivery.BypassCache`, `Delivery.CacheTags` (imgproxy `bypass_cache`, `cache_tags`); `InfoOptions.Schema` (thumbor `meta` JSON shape, which must be part of the cache key). Changed shapes: `BlurAreas` is `*BlurAreas{Sigma, Areas}` (imgproxy `ba` carries a sigma); `Border` has per-side widths; `Radius` has per-corner values; `Effort` 0 means "format default" and 1..9 are explicit.

Normalisation rules every dialect relies on:

- Units: blur and sharpen are **sigma** internally. Each dialect documents its conversion (for example Cloudflare `blur` radius r → sigma r/2; imgix `blur` 0–2000 → sigma v/20).
- Colour adjustments are **multipliers** with 1 = unchanged (brightness is additive −255..255 to match imgproxy). Dialects with −100..100 scales convert linearly: `1 + v/100`.
- Dimensions are in **CSS pixels before DPR**. The planner multiplies by DPR once.
- Rotation: `Rotate` is always a multiple of 90; anything else goes into `RotateAny`.
- Hue is additive degrees; `Adjust` identity values (brightness 0, hue 0, multipliers 1) are dropped to nil by `ApplyDefaults`.
- `Request.Validate()` runs after `ApplyDefaults` and returns an `errors.Join` of `*transform.ValidationError{Field, Reason}`; each implements `HTTPStatus() == 400` and a client-safe `PublicMessage()` (go-standards §6).
- `Request.CanonicalKey()` serialises the effective request (after defaults, and after the server resolves `ClientHints` and the negotiated format) to a stable binary form and hashes it with SHA-256 (64 hex chars). It is the result-cache key input and the ETag input. Fields tagged `key:"-"` are excluded: `Dialect`, `Ignored`, `Presets`, and `BypassCache` (it never changes the response). The encoding walks the struct by reflection with fields sorted by name, omits zero values (so adding a field never changes existing keys), writes enums by canonical name, floats as IEEE-754 bits (−0 folded to +0, one NaN pattern) and map entries sorted by encoded key; the full algorithm is the doc comment on `CanonicalKey`, and `internal/transform/testdata/canonical_keys.json` pins 40 golden keys.

`InfoOptions` mirrors imgproxy info options (size, format, dimensions, exif, iptc, xmp, video_meta, colorspace, bands, sample_format, pages_number, alpha, crop, palette, average, dominant_colors, blurhash, thumbhash, phash, calc_hashsums, detect_objects, classify).

## 5. Dialect abstraction

```go
package dialect

type Dialect interface {
    Name() string
    // Parse turns the part of the URL after the mount into a request and
    // verifies its signature. It must not do I/O.
    Parse(in *Input) (*Result, error)
    // Verifier returns the signature scheme Parse applies, or nil if unsigned.
    Verifier() sign.Verifier
}

type Input struct {
    Path     string      // escaped path after mount prefix
    Query    url.Values
    RawQuery string
    Host     string
    Header   http.Header // Accept, client hints; never used for cache-key-relevant decisions except negotiation
    Mount    Mount       // {Name, Prefix, Source}: bound source name
}

type Result struct {
    Request      *transform.Request // what the URL says, presets expanded in place (URL order matters); defaults/Validate come later
    SourceString string             // decoded source (plain/base64/decrypted, base URL applied) == Request.Source.Raw
    Signature    sign.Result        // Unsigned | Verified | Trusted
}
```

Parse authenticates as well as parses because only the dialect knows which bytes are signed (imgproxy: the redenormalised path after the signature segment; imgix: path plus query). Errors are `*errs.Error`: `KindInvalidURL` (400; argument errors wrap `*dialect.ArgumentError{Field}` and the public message names the field), `KindUnauthorized` (bad signature, bad `enc/` token), `KindForbidden` (option disallowed by config, e.g. security options), `KindExpired`. Shared constructors (`dialect.InvalidArgument`, `InvalidURL`, `UnknownOption`, `OptionNotAllowed`, `Expired`) live in `internal/dialect`. `enc/` sources are decrypted by the dialect with `security/crypt`, so stage 5 receives a plain source string.

Mounts are configured per dialect (`dialects.<name>.prefix`, `dialects.<name>.hosts`, `dialects.<name>.source`). Router precedence: exact host binding → longest path prefix → `XI_DIALECTS_DEFAULT` (imgproxy). Details in `URL-DIALECTS.md`.

`dialect/router` API: `router.New(*router.Options, map[string]dialect.Dialect) (*Router, error)` and `(*Router).Route(*http.Request) (*dialect.Result, *router.Mount, error)`. `router.Options` is the router's own options struct (mounts with dialect/prefix/hosts/source/policy, default mount, root `?url=` mount, global unsigned policy, restricted allowlists); `app` maps `config.Config` into it, so the router does not import `config`. Full precedence: exact host → wildcard host (`*.example.com`, longest suffix) → `/?url=` root rule (wsrv, else query) → longest path prefix (segment boundary, trailing slash ignored) → root prefix `/` → default mount. `Route` passes the mount source in `Input.Mount.Source`, copies it into an empty `Request.Source.Mount`, and applies the mount's unsigned policy after parsing (403 `KindForbidden`, public "unsigned request not allowed"). The precedence table and the restricted option set are in the package doc.

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
    Source  string // named source, or DevSourceName ("_dev") in dev mode
    Bucket  string // s3/gcs bucket or azure container; empty otherwise
    Path    string // canonical absolute URL for http; decoded key without leading "/" (prefix jail included) otherwise
    Version string // s3 versionId / gcs generation; empty = latest
    Dev     bool   // matched no allowlist; implicit dev-mode http source (SSRF rules only)
}

type FetchOptions struct {
    MaxBytes        int64
    IfNoneMatch     string
    IfModifiedSince time.Time
    Header          http.Header // passthrough headers (allowlisted)
    CheckRedirect   func(location string) error // fetcher binds Registry.ResolveRedirect; nil = refuse redirects
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

// Registry (built by app from []Binding{Spec, Source} + dev mode) implements Resolver,
// Source(name) lookup and ResolveRedirect(from Key, location) for http redirect hops.
// ParseRef(raw) (Ref, error) is the pure syntax step (fuzzed); Ref.String() is canonical.
// RedactKey(Key) renders keys for logs with http query strings redacted.
```

Source string resolution order: `xi://<name>/<key>` explicit; `s3://bucket/key`, `gs://`, `abs://container/blob` (alias `az://`), `local:///path` (aliases `local:/path`, `local:path`, `file:///path`), `ftp://`, `sftp://` (imgproxy-compatible schemes; the mount's own source is tried first, then configuration order) matched to a source of that type whose bucket/host list contains the target; absolute `http(s)://` matched against each http source's `allow` patterns (host globs + optional path prefix); relative keys resolved against the dialect mount's bound source. No match → 403 unless dev mode, in which case an implicit unrestricted `http` source is used (SSRF protections still apply).

Backends (under `internal/fetch/`): `httpsrc` (SSRF-safe transport, see §10), `s3src` (aws-sdk-go-v2 with custom endpoint and path-style for R2/Spaces/B2/RustFS), `gcssrc`, `azuresrc`, `fssrc` (uses Go `os.Root` so `..` and symlinks cannot escape the root), `ftpsrc` (jlaffaye/ftp, explicit TLS option), `sftpsrc` (pkg/sftp with pinned host keys; no TOFU).

## 7. Fetcher and original cache

`fetch.Fetcher` wraps sources:

- **Coalescing**: a singleflight-style group keyed by `source + "\x00" + bucket + "\x00" + path + "\x00" + version`. Ten concurrent requests for ten sizes of one original download it once and share one `Buffer` (one reference per caller). It is a small in-package group rather than `x/sync/singleflight` because the call must hold its own buffer reference until every joined caller has either retained it or given up, which singleflight cannot count. The shared fetch runs on `context.WithoutCancel(leaderCtx)` bounded by the per-attempt timeout, so a caller whose context ends stops waiting without aborting the download for the others (or for the cache).
- **Original cache**: an in-memory byte-bounded LRU (`XI_CACHE_ORIGIN_MEMORY_BYTES`, default 256 MiB, TTL 60 s) because most size bursts are close in time, optionally backed by shared stores for multi-replica setups and restarts (`XI_CACHE_ORIGIN_STORES=disk|redis|s3`, `fetch.WithStore`, namespace `orig/`, key `v1/` + hex SHA-256 of the coalescing key): a flight that misses the LRU asks the store before downloading (a hit is copied into an allocator buffer, i.e. C memory, and enters the LRU; `CacheStoreHit`), and each download is written through the app's `AsyncStore` for `XI_CACHE_ORIGIN_STORE_TTL` (10 min) with Content-Type, ETag, Last-Modified, Cache-Control and Expires. Store entries are not revalidated and store failures are misses. The in-memory LRU holds one buffer reference per entry, caches not-found for `XI_CACHE_ORIGIN_NEGATIVE_TTL` (5 s), and after the TTL revalidates entries that carry an ETag/Last-Modified with `If-None-Match`/`If-Modified-Since` (304 → TTL refreshed, same buffer served).
- **Limits**: `Content-Length` checked before reading; body wrapped in a counting reader that aborts at `XI_LIMITS_MAX_SRC_BYTES` (default 50 MiB). Download timeout per attempt (`XI_FETCH_TIMEOUT`, 10 s, via `context.WithTimeoutCause` → `UpstreamTimeout`). Exactly one retry (`XI_FETCH_RETRY`) of `UpstreamError`/`UpstreamTimeout` after a backoff jittered in [50, 150) ms; 4xx kinds are never retried. With `Content-Length` the buffer is allocated once; without it, it grows in doubling chunks from 64 KiB via the allocator and is copied into an exact-size buffer at the end, so `Bytes()` is always exactly the original.
- **Buffering** (go-standards §4): originals are read straight into C-allocated memory that vips will own (`vips.NewBuffer(n)` + `io.ReadFull`), never into pooled Go slices. The fetcher only sees the small interface below, so it stays pure Go and tests inject a Go-memory implementation. Buffers are reference-counted so coalesced requests and the origin cache share one allocation; the last `Release` frees it (production: `*vips.Buffer`, which implements `fetch.Buffer` and unrefs its `vips_blob`; `app` injects `func(n int) (fetch.Buffer, error) { return vips.NewBuffer(n) }` as the `Allocator`, adapting the concrete return type and never returning a typed nil; `Retain`/`Release` are atomic and extra `Release`s are no-ops).

```go
type Buffer interface {
    Bytes() []byte   // exactly n bytes; written once by the fetcher before sharing, then read-only until the final Release
    Retain()
    Release()
}
type Allocator func(n int) (Buffer, error) // new Buffer of exactly n bytes holding one reference
```

`fetch.Fetcher.Fetch(ctx, source.Key, source.FetchOptions) (*fetch.Result, error)`; `Result{Buffer, Object (Body nil), FromCache}` and the caller calls `Result.Release()`. Metrics hooks (`fetch.Metrics`: fetch duration/bytes/outcome per attempt, cache hit/miss/negative-hit/revalidated, coalesced, retry) default to a no-op. `fetch.GoAllocator` is the pure-Go allocator for tests.
- **Request dedup**: in addition to fetch coalescing, identical in-flight *requests* (same result-cache key) are collapsed with a second `singleflight.Group`, as imagor does.
- **Conditional requests**: if the client sent `If-None-Match` and the result ETag can be derived from source ETag alone, the fetcher issues a conditional request and a 304 short-circuits the pipeline.

## 8. Processing pipeline

### 8.1 Planner (pure Go)

Implemented in WP-25; the full type list is in `internal/pipeline/plan/plan.go`.

```go
package plan

func Build(req *transform.Request, meta SourceMeta, limits Limits) (*Plan, error)
func Continue(req *transform.Request, meta SourceMeta, limits Limits, obs Observed) (*Plan, error)
func Chain(req *transform.Request, meta SourceMeta, limits Limits, i int, in Input, obs *Observed) (*Plan, error)

type Plan struct {
    Load      LoadStep       // shrink-on-load (JPEG 2/4/8), scale (WebP/SVG/PDF), page, n, access, HEIF thumbnail, predicted size
    Animation *AnimationStep // {Frames, PageHeight}: Steps run per frame, After on the joined strip
    Steps     []Step         // ordered, typed descriptors (sealed sum type)
    After     []Step         // watermarks; output colour on the last plan
    Chained   []*Plan        // subsequent pipelines: Build's prediction; the executor replans each with Chain
    Materialize bool         // chained plans: copy the previous result to memory first (a step reads out of order)
    Output    Output         // {Width, Height, Frames, Format hint, Alpha, DPRScale}
    Cost      Cost           // {Pixels, EstimatedBytes}: peak, used by the memory-weighted limiter
    Unplanned []string       // options later WPs turn into steps
}
```

`SourceMeta` is the planner's own header struct (the executor maps `codec`/`vips` header data onto it), so `pipeline/plan` imports only `transform` and `errs`. The geometry reproduces imgproxy's exactly (`calcScale`, `calcSizes`, `limitScale`, `scaleOnLoad`, `calcPosition`, `RotateAndFlip`, `extend`, `padding`, `fixSize`); `testdata/imgproxy_corpus.json` pins 127 cases recorded by running imgproxy's own pipeline. What a header cannot predict (loader rounding, the embedded HEIF thumbnail, the size after trim) the executor reports through `Continue`, which returns the complete plan with the geometry recomputed. Crop and extend steps carry their gravity and a `Rect(imgW, imgH)` method, so they are placed on the real size. Chained pipelines (WP-58) are predicted by `Build` from the previous prediction, for admission; the executor plans each again with `Chain` on the observed result of the previous pipeline (size, frames, alpha; EXIF orientation already applied, no shrink-on-load or page selection) and, after a trim inside a chained pipeline, with the trimmed size. `Chain` sets `Materialize` when a step of the chained plan needs random access (trim, smart crop, rotate, flip, filters); otherwise the chained steps extend the previous pipeline's lazy graph (a three-stage chain of sequential steps is 34% faster than copying at every boundary, `BenchmarkChained`). The root `Cost` covers the worst pipeline plus the materialised results the arena still holds while it runs. A planning error in a chained pipeline names its index (`pipeline N`).

Fixed step order within one pipeline (imgproxy's `mainPipeline`, chosen for sequential access):

1. **Load** with shrink-on-load: JPEG `shrink` 2/4/8, WebP `scale`, HEIF/AVIF embedded thumbnail, SVG/PDF `scale` (vector guard at `max_src_resolution`). The shrink comes from the size the computed scales give, not the scales, so one-sided targets do not overshrink; the remaining scale is rebased on the loaded size. Animated sources and trimmed images never shrink on load. RAW embedded preview: see FORMATS.md.
2. **Colour to working space**: import ICC, convert to sRGB (or keep 16-bit when preserving HDR).
3. **Trim** (forces random access; disables shrink-on-load; not on animations).
4. **Pre-crop** (`crop`, thumbor `AxB:CxD`, imgix `rect`) in stored (un-rotated) coordinates: the gravity is mapped back through the user rotate/flip and the EXIF orientation. `crop_aspect_ratio` (imgproxy Pro, WP-60) corrects the crop area (the whole image without `crop`) to the ratio before it is placed: it shrinks the area to match, or with `enlarge` grows it and shrinks it back, keeping the ratio, when it no longer fits; the gravity is not corrected (imgproxy docs).
5. **Resize** remainder (premultiplied when the image has alpha, linear light optional via `XI_PROCESSING_LINEAR_RESIZE`).
6. **Orientation**: EXIF rotate, EXIF flip, then `rotate`, then `flip` (the loader does not autorotate).
7. **Target crop** by gravity (compass + offsets scaled by DPR, focus point, smart = `vips_smartcrop` attention/entropy, face/object fall back to attention until M3; animations fall back to centre).
8. **Filters** (`FilterStep`, per frame, in sRGB, premultiplied when there is alpha): blur, sharpen, pixelate (one pass, as imgproxy), with the Pro filters of WP-61 in between and after, in this order: blur, sharpen, **unsharp masking**, pixelate, **blur areas**, **progressive blur**. Unsharp masking (`Unsharp`): imgproxy's `unsharp_masking` is the 3×3 kernel −w around a centre of d + 8w, divided by d (w = weight, default 1; d = divider, default 24), i.e. v + 8w/d × (v − mean of the 8 neighbours), so a step edge's delta grows by 1 + 6w/d; mode `auto` (the default, also when only weight/divider are given) applies it only when the pipeline downscaled (shrink-on-load, a scaled load or a resize below 1) and `sharpen` is not set, `always` always, `none` never. The classic parameters (imgix `usm`/`usmrad`, ImageKit `e-usm`: `Sigma`, `Amount`) are a true unsharp mask v + amount × (v − gaussblur(v, σ)) and default to `always` (σ 1 when unset; amount 0 is off). Blur areas (`BlurAreas`): rectangles relative to the source after EXIF orientation and before `rotate`/`flip` (imgproxy docs), mapped by the planner through pre-crop, resize, orientation, rotate, flip and the result crop onto the frame (clipped; areas left outside are dropped; a smart crop's runtime position is not known to the planner), each blurred from the unfiltered frame with a 3σ+1 margin and inserted back, so pixels outside the areas are unchanged. Progressive blur (`ProgressiveBlur`): the frame blended with a copy blurred at σ through the gradient ramp of `gradient` (same directions and angles, 0 at `start`, full blur at `stop`). imgproxy's Pro filters are closed source; this follows its documented semantics. Detection blur/draw come with M3. Then **colour adjustments** (`ColourAdjustStep`, WP-59, per frame, before the canvas grows so backgrounds keep their exact colour), in this order on 8-bit sRGB: tone curves (brightness `v+b`, contrast `(v−128)·k+128`, gamma `255·(v/255)^(1/g)`, exposure ×e in linear light, red/green/blue ×m) as one lookup table; saturation and hue as CSS `saturate()`/`hue-rotate()` matrices (Rec. 709 luma), grayscale (luma matrix) and sepia (weserv matrix) merged into one `vips_recomb`; tint (sharp: CIELAB a\*b\* of the colour, weighted to the midtones), monochrome (the colour's a\*b\* at full strength at its own lightness, fading to neutral at black and white; b3b3b3 is plain greyscale) and duotone (CIELAB ramp shadow→highlight) as palettes indexed by the luma and mixed by intensity; negate; colorize (a table, or composited "over" when the image has alpha and `keep_alpha` is off); gradient (a `vips_xyz` ramp, CSS angles: 0 up, 90 right, 180 down, composited over); then `Adjust.Opacity` on the alpha band. The alpha band is never touched by the colour maths. imgproxy's colour options are Pro-only and closed source, so their maths follows the documented semantics and the other dialects' public definitions; vibrance and equalize remain unplanned. Then **colour adjustments** (`ColourAdjustStep`, WP-59, per frame, before the canvas grows so backgrounds keep their exact colour), in this order on 8-bit sRGB: tone curves (brightness `v+b`, contrast `(v−128)·k+128`, gamma `255·(v/255)^(1/g)`, exposure ×e in linear light, red/green/blue ×m) as one lookup table; saturation and hue as CSS `saturate()`/`hue-rotate()` matrices (Rec. 709 luma), grayscale (luma matrix) and sepia (weserv matrix) merged into one `vips_recomb`; tint (sharp: CIELAB a\*b\* of the colour, weighted to the midtones), monochrome (the colour's a\*b\* at full strength at its own lightness, fading to neutral at black and white; b3b3b3 is plain greyscale) and duotone (CIELAB ramp shadow→highlight) as palettes indexed by the luma and mixed by intensity; negate; colorize (a table, or composited "over" when the image has alpha and `keep_alpha` is off); gradient (a `vips_xyz` ramp, CSS angles: 0 up, 90 right, 180 down, composited over); then `Adjust.Opacity` on the alpha band. The alpha band is never touched by the colour maths. imgproxy's colour options are Pro-only and closed source, so their maths follows the documented semantics and the other dialects' public definitions; vibrance and equalize remain unplanned.
9. **Geometry fill** (WP-60 extras in `plan/geometry_extras.go`, `exec/geometry_extras.go`, per frame): **arbitrary rotate** (`RotateAnyStep`, imgix `rot`, ImageKit `rt`, Cloudinary `a_`, wsrv `ro`) with `vips_similarity` (bilinear, premultiplied when the image has alpha): the canvas grows to the rotated bounding box (`plan.RotatedSize` reproduces libvips' rounding, 100×50 at 45° is 106×106) and the corners are filled with the background, or transparent without one; it runs after the colour adjustments, so the fill keeps its exact colour, and before extend and padding, which then grow the rotated canvas. Then extend, extend-aspect-ratio, padding (scaled by DPR). Then the canvas is shaped: **mask** (`MaskStep`, wsrv `mask`/`mtrim`/`mbg`, imgix `mask=ellipse`; wsrv's shapes and fitting, the alpha multiplied by the shape, the background laid over the outside, `mtrim` crops to the shape's box), **border** (`BorderStep`, per-side widths × DPR, `vips_embed` with the colour, alpha included), **radius** (`RadiusStep`, per-corner radii × DPR scaled down together like CSS when they do not fit, `max` = an ellipse, a circle when square, like Cloudinary `r_max`); border before radius, so the corners round the border too. Mask and radius outlines are flattened into polygons (curve segments of about one pixel) and rasterised in Go (`exec/raster.go`) into an 8-bit alpha mask by area coverage (64 sub-scanlines per row, exact horizontal span coverage: edges anti-aliased at any DPR, interior exactly opaque; libvips' `vips_draw_*` do not anti-alias), handed to libvips with `vips_image_new_from_memory` and applied with `bandjoin` (no alpha yet) or a `dest-in` composite; the mask is cached per frame size, so an animation rasterises it once. The result gains alpha; step 10 flattens it onto the background (white by default) for formats without alpha or when `background` is set.
10. **Encoder size limits** (WebP 16383, AVIF/HEIC 16384, GIF, ICO 256), then **flatten** when the output lacks alpha or a background is set. A translucent background (`background_alpha` < 1) is composited under the image and keeps the alpha band, in formats that have one.
11. **Watermarks** in order (`After`: once on the joined strip for animations).
12. **Output colour** (`After` of the last plan): ICC transform/embedding, strip metadata (keep copyright if asked), DPI, orientation tag reset. `color_profile` converts to `srgb`, `cmyk` (JPEG and TIFF only; skipped elsewhere) or an ICC file in `XI_PROCESSING_COLOR_PROFILES_DIR` (skipped when unset; an unknown file is 400) and embeds the profile regardless of `strip_color_profile`.
13. **Encode** (see §8.3).

Animated images carry all frames as one tall "toilet roll" image with `page-height`; every geometric step is applied per page by the executor (libvips handles most ops natively with `page-height`; crop/extend/rotate are done with `vips_arrayjoin` per frame). Frame limits are checked at probe time.

### 8.2 Executor (cgo)

libvips is demand-driven: each call (`resize`, `crop`, `embed`, …) returns a new image node that references its input; no pixels are computed until a sink (save to buffer) pulls regions through the graph in parallel tiles. The executor:

- Wraps every node in `vips.Image` (a Go handle with explicit `Close()`; nodes are tracked in a per-request arena and all unref'd in one `defer arena.Close()` so error paths never leak).
- Never calls `copy_memory` unless a step needs random access on a sequential source (trim, smartcrop, arbitrary rotate of a non-random loader, chained pipeline boundary). The planner decides this, not the executor.
- Runs the whole job on one locked OS thread (`runtime.LockOSThread`), and calls `vips_error_clear(); vips_thread_shutdown()` before unlocking (go-standards §4).
- Checks `ctx.Err()` between steps. cgo calls cannot be interrupted, so for the long sink (save) a watcher tied to `ctx` sets the image's kill flag (`vips_image_set_kill`) through `vips.KillOnCancel` (armed by every sink); libvips aborts at the next tile.
- Captures errors with `vips_error_buffer_copy()` at the failure site in the shim; the HTTP status comes from *which* operation failed (load → 422 `Corrupt`, save → 500), never from message text.
- Uses `fail_on=warning` for untrusted sources (configurable `none|truncated|error|warning`).

Implemented in WP-26a (`internal/pipeline/exec`). The executor plans and encodes too, so one call is stages 12–13; `app` adapts it to `server.Processor` (`exec` does not import `server`, §3):

```go
package exec

func New(cfg *Config, enc *encode.Encoder) (*Executor, error) // Config{Limits plan.Limits, FailOn vips.FailOn}
func (e *Executor) Run(ctx context.Context, j *Job) (*Result, error)

type Job struct {
    Request *transform.Request // after ApplyDefaults + Validate
    Source  *vips.Buffer       // caller keeps its reference
    Meta    plan.SourceMeta    // Format is the sniffed codec name ("bmp"/"ico" decode in Go)
    Target  encode.Target      // encode.NewTarget(&req.Output, &decision, animated)
}
type Result struct {
    Body          encode.Body // vips blob (or Go bytes for BMP/ICO), written zero-copy then Released
    Format        transform.Format
    ContentType   string
    Width, Height int         // one frame
    Frames        int
    DPR           float64
    Quality       int
    Skipped       []string    // options without an implementation yet (M3 watermark sources/text, chessboard, plan.Unplanned)
}
```

Run plans for `Target.Format` (not the source format: alpha, flatten, animation and encoder size limits depend on it), then, in one `vips.Run` with one `Arena`: load (shrink/scale/density/page/frames/HEIF thumbnail; EXIF orientation tag dropped, page-height dropped from single images), `plan.Continue` on a loaded size that differs from the prediction, on the HEIF thumbnail (reloading the full image when the planner declines it) and after trim; steps with imgproxy's semantics (ICC import unless the profile is sRGB IEC61966-2.1, then 8-bit sRGB or grey; premultiplied resize; crops and extends placed with `Rect`; smart crop on a memory copy; extend and padding embed on a transparent canvas that `FlattenStep` fills). Animations: every frame is extracted by page-height, runs `Steps` alone, and the frames are joined with `vips.Arrayjoin`; page-height, `n-pages`, delays (40 ms when absent) and loop count are set on the strip before `After`. Chained pipelines are replanned with `plan.Chain` on the observed previous result and run on it lazily, or on its `CopyMemory` when the plan sets `Materialize`; the source ICC profile is imported once (a chained pipeline sees sRGB pixels); errors name the pipeline index; `TestChainedGolden`, `TestChainedEqualsSequential` (a chain equals its pipelines run as separate requests), `TestChainedObservedSize`, `TestChainedAnimationTiming`, `TestChainedICCOnce` and `TestChainedNoLeak` cover WP-58. The ICC import goes through the profile's own PCS (XYZ or Lab, as imgproxy). The output colour step drops the source ICC profile after an import (the pixels are sRGB; re-exporting to the source profile is not done) and, with `strip_color_profile=false`, tags the result with libvips' built-in sRGB (sGrey) profile (`vips.ProfileLoad`); a profile that was not imported (sRGB IEC61966-2.1) is kept or dropped by the encoder per the flag. A `page` of a multi-page still whose size differs from the header's (first page) is planned again from its loaded size (`server.SourceMeta.Pages` carries the page count to the planner). `pages` on a multi-page still loads the pages stacked vertically (left-aligned) as one image; the loaded strip replans the same way (`SourceMeta.Selection`). For a vector source (PDF) the page or stack is measured first with a header-only load, so the render scale is chosen for its real size; PDF pages render at the probe density (`SourceMeta.Density`, `XI_FORMATS_PDF_DPI`) times the plan's scale, onto opaque white unless the request asks for transparency (FORMATS.md §1.2). WP-50/51 goldens: `TestGoldenPDFPSD`, `TestPDFCrisp`, `TestPDFTransparency`, `TestPSDCompositeStrategies`. WP-26b/29 (trim incl. bottom-right colour and equal cuts, extend, extend_aspect_ratio, per-frame padding, `disable_animation`, `max_animation_frames`, delays and loop read back, ICC, orientation reset, the metadata matrix through `app` `Process`) are covered by `TestGoldenFill`, `TestTrimBottomRight`, `TestAnimationTiming`, `TestMaxAnimationFrames`, `TestICCToSRGB`, `TestOrientationReset` and `internal/app` `TestProcessMetadata`. `testdata/golden/exec` holds the goldens; `TestAgainstImgproxy` and `TestAgainstImgproxyFill` (`-tags integration`, `XI_IMGPROXY_URL`, imgproxy with `IMGPROXY_MAX_ANIMATION_FRAMES` > 1) compare every case with darthsim/imgproxy.

Watermarks (WP-28, `exec/watermark.go`): `app` fetches `XI_PROCESSING_WATERMARK_SOURCE` once at startup through the source registry and fetcher (SVG sanitised), and `exec.NewWatermark` decodes it (first frame, EXIF orientation applied, sRGB/grey working space) into a `vips.Shared` memory image kept for the process lifetime; any failure stops startup. Per request, `plan.WatermarkStep` (After, on the whole strip) reproduces imgproxy's `prepareWatermark`/`applyWatermark`: fit into `Scale` × one frame (enlarging allowed, premultiplied resize), place by gravity with DPR-scaled or fractional offsets and overflow allowed (`WatermarkGeometry.Place`), or tile (`re`) with the offsets as spacing and a centred odd grid; alpha × request opacity × `XI_PROCESSING_WATERMARK_OPACITY`; composite over, back to the base band format, alpha dropped when the base had none. Animations get one frame-sized overlay replicated down the strip (clipped per frame) and a single composite. Watermarks with their own source or text and chessboard tiling (M3) are skipped and reported in `Skipped`.

### 8.3 Encoding and quality loops

`codec/encode` owns per-format savers (options in FORMATS.md). Quality selection precedence: `Output.Quality` → `FormatQuality[f]` → config per-format default → global default (80). `max_bytes` and auto-quality run a bounded binary search (≤ 6 saves) over quality; auto-quality by DSSIM uses an in-house SSIM implementation on a downscaled luma copy (the `dssim` CLI is AGPL and is not used). Best-format (`EncodeBest`, WP-57) classifies the processed image (`negotiate.Analyze`), encodes the candidates `negotiate.Decision.BestCandidates` gives for its class and picks the smallest that meets the quality target, skipped above `XI_FORMATS_BEST_MAX_PIXELS` (FORMATS.md §6.3).

API (WP-30): `encode.New(Config)` → `(*Encoder).Encode(ctx, img, Target) (*Result, error)`, with `encode.NewTarget(&req.Output, &decision, animated)`. The max_bytes search renders the image once with `vips.(*Image).CopyMemory`, saves at the resolved quality, then binary-searches below it (≤ `encode.MaxSaves` = 6 saves) and returns the largest fitting quality or the smallest attempt with `Result.OverBudget`. Metadata: libvips' `keep` has a single EXIF bit and savers rebuild EXIF from the `exif-ifdN-*` fields, so `keep_copyright` (IFD0 Copyright + Artist, as imgproxy) and the always-on GPS strip (IFD 3 and XMP `exif:GPS*`) remove fields by name via `Fields`/`RemoveMetadata`.

### 8.4 The `vips` package (WP-06)

`internal/vips` is a hand-written C shim (`shim.h` + `shim.c`, `load.c`, `image.c`, `ops.c`, `save.c`) with thin Go wrappers: one fixed-arity C function per operation, options passed as plain C structs, `#cgo noescape/nocallback` on every entry point, no varargs from Go. vipsgen is **not** pinned (deviation from the WP-06 row, reasons in `internal/vips/doc.go`: it imports `"C"` itself and owns process-global vips state); it and imagor stay the reference for option names.

| Group | API |
|---|---|
| Lifecycle | `Init(*Config)` (reference-counted, see the lifecycle invariant below; concurrency 1, cache 0, loader + saver allowlists after `vips_block_untrusted_set`, `HEICEncode`), `Shutdown`, `Run(ctx, fn)`, `ReadMemStats` (mem, highwater, allocs, files), `LiveObjects`, `SetLeakCheck`, `KillOnCancel(ctx, img) (stop)` |
| Bytes | `Buffer` (`NewBuffer(n)`, `NewBufferFromReader`, refcounted C memory, implements `fetch.Buffer`), `Blob` (encoded output, zero-copy, refcounted) |
| Decode | `Probe(ctx, buf) Header` (header only, sequential, never decodes), `Load(ctx, buf, *LoadOptions)` (shrink, scale, density, page, n, access, fail_on, autorotate, heif thumbnail, max pixels/frames → `LimitExceeded`), `Thumbnail(ctx, buf, *ThumbnailOptions)`, `Black`, `NewFromMemory` (Go decoders → vips) |
| Process | `(*Image)` methods returning new lazy images: `Resize`, `ExtractArea`, `Crop`, `Embed`, `Gravity`, `Flip`, `Rot`, `Autorot`, `SmartCrop`, `Flatten`, `Premultiply`, `Unpremultiply`, `Colourspace`, `ICCImport/Export/Transform`, package func `ProfileLoad` (built-in or file ICC profile), `Cast`, `Copy`, `Gaussblur`, `Sharpen`, `Shrink`, `Composite2`, `Replicate`, `Join`, `Linear`, `Invert`, `BandjoinConst`, `ExtractBand`, `AddAlpha`, `FindTrim`/`Trim`, `Similarity`, `Affine`; package funcs `Arrayjoin`, `Bandjoin`; metadata `Int/Ints/Blob/Text` getters, `Fields` (every field name; EXIF tags are `exif-ifdN-Tag` fields the savers rebuild EXIF from) and copy-on-write `SetInt/SetInts/SetBlob/RemoveMetadata/SetOrientation/RemoveOrientation/SetPageHeight`; `Arena` closes a request's images |
| Shared | `Share(ctx, img)` renders a metadata-free root memory image many Runs read at once (the watermark); `(*Shared).Image()` hands a Run its own handle; `Close` after the last Run |
| Encode | `(*Image).CopyMemory(ctx)` renders once before repeated saves (max_bytes); `(*Image).Save(ctx, Format, *SaveOptions)` for JPEG, PNG, WebP, GIF, AVIF, HEIF (HEVC only with `HEICEncode`), JXL, TIFF; `DefaultSaveOptions()` mirrors the libvips defaults; `Keep` maps to `VipsForeignKeep` |

Rules the executor relies on:

- Every call takes `ctx` first and returns `Canceled`/`Timeout` without crossing into C when it is done. Sinks (`Save`, `WriteToMemory`) and computing ops (`SmartCrop`, `FindTrim`) arm `KillOnCancel`; a killed call reports the context's kind.
- Animated images are strips with `page-height`. `Crop`, `Embed`, `Gravity`, vertical `Flip`, `Rot`, `Autorot`, `Similarity`, `Affine` work per frame (extract + `arrayjoin`) and fix `page-height`; `Resize` rounds the frame height to whole rows and keeps the strip an exact multiple; `SmartCrop` and `Trim` refuse multi-frame input (the planner falls back to gravity).
- Error kinds come from the failing operation (§8.2). Lazy decode failures are attributed through a libvips `invalidate` emission hook that marks the source buffer, so a truncated JPEG found during `Save` is `Corrupt`, not `Internal`.
- `Image` is not goroutine-safe and lives inside one `Run`. `Buffer` and `Blob` reference counts are safe for concurrent use.
- **Lifecycle invariant.** libvips is process-global and `vips_shutdown` is final (it frees the operation cache table, which is created once by a `GOnce`, and stops the thread pool; a later `vips_init` is a no-op). `Init`/`Shutdown` are therefore reference-counted in the C shim: every `Init` takes a reference (serialised; only the first applies its settings, and a nested `Init` with different settings fails, because re-applying the loader allowlist briefly blocks every loader under running jobs), every `Shutdown` releases one, `Run` registers itself for its whole duration and fails without a reference, and the `Shutdown` that releases the last reference refuses new `Run`s, waits for the running ones and then calls `vips_shutdown` exactly once; `Init` and `Run` fail afterwards. Several `app.Run` instances in one process (parallel tests) nest safely; test packages that start the service repeatedly hold a reference in `TestMain`. Every image or operation call happens inside `Run`; never call `Shutdown` inside `Run`. (Before this rule, one instance's shutdown tore libvips down under another's jobs: `g_hash_table_lookup: assertion 'hash_table != NULL'` in `xi_jpegsave`/`xi_probe`, SIGSEGV in `vips_sink_disc`.)

## 9. Caching

### 9.1 HTTP semantics (always on)

Implemented in `internal/server/response.go` (WP-34b).

| Header | Rule |
|---|---|
| `Cache-Control` | Success: `public, max-age=<TTL>, stale-while-revalidate=<TTL/10>` (`XI_CACHE_HEADERS_TTL`; `0` → `no-cache`); add `immutable` for the `immutable` option (cloudinary `fl_immutable_cache`) or when the URL is signed (verified or trusted) and carries a cache buster or a `hashsum`. A per-request TTL is clamped by the config TTL. `expires` shortens max-age to the time left and drops `stale-while-revalidate` and `immutable`. Source passthrough (`XI_CACHE_HEADERS_PASSTHROUGH`): source `Cache-Control` verbatim, else source `Expires` as `public, max-age=N`; a per-request TTL or `expires` beats passthrough. Errors: the Cache-Control column of §12 — `public, max-age=XI_CACHE_HEADERS_ERROR_TTL` (default 10 s; `0` → `no-store`) for source/image failures, `no-store` for everything else (501 included); `Retry-After` stays on 429/503. |
| `ETag` | Strong: `"xi1-" + base64url(sha256(F(canonicalKey) F(outputFormat) F(qualityDelta) F(sourceValidator) F(XI_CACHE_ETAG_BUSTER)))[:22]` with `F(x) = decimal byte length of x ‖ ":" ‖ x`. `outputFormat` is the negotiated format name (`jpeg`, `webp`, …), `qualityDelta` the decimal Save-Data delta (not part of the canonical key). `sourceValidator` is `"e" ‖ source ETag`, else `"m" ‖ source Last-Modified as an HTTP date`, else `"h" ‖ raw SHA-256 of the source bytes`. Pinned by `TestETagGolden`; changing the encoding means bumping `xi1`. The cache buster is part of the canonical key, so it changes the ETag but never the pixels. |
| `Last-Modified` | Source Last-Modified when known. |
| `Vary` | `Accept` only when format was negotiated (format not fixed by URL). Add only the client-hint names that decided the result (`negotiate.Decision.Vary | HintResult.Vary`, WP-65): a hint overridden by the URL is not listed, a valid `Sec-CH-*` value drops its legacy name, a Width hint drops Viewport-Width, `Save-Data: on` drops `ECT`; a hint that could apply but was absent lists both its names (its presence would have changed the result). Rules: URL-DIALECTS §3.5. Never `Vary: *`. Error responses add `Vary: Accept` (JSON vs text body). |
| `Accept-CH` | `Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width, ECT` when `XI_CLIENT_HINTS_ENABLED` or the URL opted into a hint, on 200, HEAD, 304 and unprocessed responses; `Critical-CH: Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width` with `XI_CLIENT_HINTS_CRITICAL_CH` (`server/hints.go`). |
| `Content-DPR` | Set when a DPR ≠ 1 taken from a client hint was applied (`HintResult.ContentDPR`); a DPR fixed by the URL does not set it. |
| `Content-Disposition` | `inline`, or `attachment` with the `return_attachment` option. `filename="<stem><ext>"`: stem from the `filename` option (an extension equal to the output's is not doubled), else the source path's base name (query and fragment dropped, percent-decoded) without its extension, else `image`; ext from the output format (`.jpg`, `.png`, `.webp`, …). Sanitised: last path segment only (`/` and `\`), control characters and invalid UTF-8 dropped, leading dots trimmed, capped at 200 bytes. The quoted value is printable ASCII with `"`, `\`, `%` and non-ASCII replaced by `_`; whenever that differs from the real name, RFC 8187 `filename*=UTF-8''…` follows. |
| `Content-Type`, `Content-Length`, `X-Content-Type-Options: nosniff`; `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox` for SVG output. |
| `Link` | `<source URL>; rel="canonical"` for http sources when `server.Config.CanonicalHeader` is on (imgproxy `IMGPROXY_SET_CANONICAL_HEADER`; no `XI_` variable yet, off). |
| `Cache-Tag` | `cache_tags` values joined by `,` under the header named by `XI_CACHE_TAGS_HEADER`. |
| `X-Xi-*` | Debug only (`XI_OBSERVABILITY_DEBUG_HEADERS`): `X-Xi-Origin-Content-Length`, `X-Xi-Origin-Width/-Height` (probe), `X-Xi-Result-Width/-Height` (encoder), `X-Xi-Quality` (the encoder quality; with auto-quality the chosen one; absent for formats without quality), `X-Xi-Ignored` (options parsed but not applied). |

**Conditional requests.** `If-None-Match` (weak comparison, lists, `*`) and, only without it, `If-Modified-Since` against the source Last-Modified, answer 304 with the headers a 200 would carry except the representation headers (Content-Type/-Length/-Disposition). The processor never runs for a 304. The ETag needs only the canonical key, the negotiated format and the source validator, so it is computed right after the fetch; the fetcher owns origin revalidation (it sends the cached original's validators to the origin, §7), because the client's `If-None-Match` is our hash and cannot be translated to an origin validator. When the URL fixes the output format (and no skip-processing list could turn the request into a passthrough) the 304 is answered before admission and probe; otherwise after probe and negotiation, with the admission permit and buffers released before the response is written.

**HEAD** runs the lifecycle up to negotiation and answers the GET's headers without processing; `Content-Length` is omitted because it is only known after encoding.

**`expires`**: a URL whose `expires` timestamp is not in the future answers 404 (`KindExpired`, `no-store`) before the fetch; dialects check it at authentication too.

### 9.2 Pluggable stores (M2)

Implemented in `internal/cache` (WP-52): the interface, `Buffer`, key derivation, `Tiered`, `AsyncStore`, and the `memory` backend; `disk`, `s3cache` and `redis` (WP-53/54/55) implement the same `Store`, share the persisted form `cache.Meta` (full key, status, header subset, stored/expiry times, body size and CRC-32C; `meta.go`) and pass the `cachetest` conformance suite.

```go
package cache

type Namespace string // "res/" (NSResult), "orig/" (NSOrigin, reserved)

// Buffer is refcounted, read-only bytes (the shape of fetch.Buffer and
// server.Blob): a vips output blob is stored without copying.
type Buffer interface {
    Bytes() []byte
    Retain()
    Release()
}

type Entry struct {
    Body      Buffer      // Get: one reference owned by the caller (Release it); Set: the store retains or copies
    Status    int         // 200 for results
    Header    http.Header // Content-Type, ETag, Last-Modified, Vary, Content-DPR, X-Xi-* (read-only, shared)
    StoredAt  time.Time
    ExpiresAt time.Time   // zero = only eviction removes it; derived from the Set ttl
}

type Store interface {
    Get(ctx context.Context, ns Namespace, key string) (e Entry, ok bool, err error) // err = backend failure, callers treat it as a miss
    Set(ctx context.Context, ns Namespace, key string, e *Entry, ttl time.Duration) error // ttl <= 0: no expiry; *e is copied, never kept
    Delete(ctx context.Context, ns Namespace, key string) error
    Close() error // releases every held entry; later calls return ErrClosed
}
```

Backends: `memory` (byte-bounded LRU over `container/list`, TTL per entry checked lazily, no goroutine; a hit hands out the stored buffer itself; `XI_CACHE_RESULT_MEMORY_BYTES`), `disk` (one file per entry at `<dir>/h[0:2]/h[2:4]/h` with h the hex SHA-256 of ns+key, holding a `cache.AppendFrame` frame then the body; written to a temp file and renamed; no fsync, torn files fail the checksum; in-memory index rebuilt by a scan at start ordered by mtime; a janitor goroutine evicts LRU entries down to 90 % once a write exceeds `XI_CACHE_RESULT_DISK_BYTES` and sweeps expired entries every minute; corrupt entries are removed and counted), `s3cache` (the client of the s3 source named by `XI_CACHE_RESULT_S3_SOURCE`, `s3src.NewClient`; one object per entry, the `cache.Meta` JSON base64url in `x-amz-meta-xi-meta` plus `Content-Type`/`Expires`; expiry checked from the metadata before the body is read, so no lifecycle rule is needed; metadata over S3's 2 KB limit is skipped), `redis` (go-redis v9, any RESP server; value = frame + body at `XI_CACHE_RESULT_REDIS_PREFIX` + ns + key, `SET … PX`, values over `XI_CACHE_RESULT_REDIS_MAX_VALUE_BYTES` (1 MiB) skipped; TLS and auth from the URL). Skipped entries are counted in `xi_cache_skipped_total{store,reason}` (`too_large`, `corrupt`). `app` builds each backend listed in `XI_CACHE_RESULT_STORES` or `XI_CACHE_ORIGIN_STORES` once and composes the result tiers and the origin tiers from them, each behind its own `AsyncStore`. `cache.Tiered` composes them in `XI_CACHE_RESULT_STORES` order (memory → disk/redis → s3): Get returns the first hit and fills the faster tiers with the remaining lifetime (read-fill), Set writes through, a failing tier counts as a miss. `cache.AsyncStore` wraps the composition: Set retains the body and writes on a goroutine bounded by an `x/sync` semaphore of `XI_CACHE_RESULT_WRITE_QUEUE` slots (TryAcquire, never a channel pool), detached from the request (`context.WithoutCancel` + 30 s timeout); a full queue drops the write (`ErrDropped`, `xi_cache_writes_dropped_total`) and never blocks the response. Metrics: `xi_cache_requests_total{store,result}`, `xi_cache_evictions_total{store}`, `xi_cache_bytes{store}`, `xi_cache_entries{store}`, `xi_cache_writes_dropped_total`, `xi_cache_evictions_total{store}`, `xi_cache_bytes{store}`, `xi_cache_entries{store}`.

**Result key** (`cache.ResultKey`, pinned by `TestResultKeyGolden`): `res/` + `v1/` + hex(sha256(F("v1") F(canonicalKey) F(sourceIdentity) F(formatSelector) F(qualityDelta) F(hints) F(XI_CACHE_ETAG_BUSTER) F(buildVersion))), `F` as in the ETag (§9.1). `sourceIdentity` is the resolved `source.Key` (name, bucket, path, version joined by NUL); `formatSelector` is `f:<format>` when the URL fixes the format, else `a:` + `negotiate.AcceptList.Key()` (the Quality/Explicit weights of every output media type, the only Accept inputs of `negotiate.Decide`, so equivalent Accept headers share entries); `hints` is the client-hint Vary set and Content-DPR; `buildVersion` keeps a new release from serving an older encoder's bytes. Every input is known before the fetch, so the lookup is stage 8 (§2) and a hit skips fetch, admission, probe and processing. Source validators are deliberately **not** in the key (imagor's result storage works the same way): a result is served for `XI_CACHE_RESULT_TTL` (default 24 h; a per-request `ttl`/`expires` shortens it) without contacting the origin; `cachebuster` or `XI_CACHE_ETAG_BUSTER` invalidate earlier. The stored ETag still embeds the source validator of the response that was cached.

**Server integration** (`internal/server/cache.go`): the hit is answered through `respond` like a miss. The entry stores the headers derived from source and output (ETag, Content-Type, Last-Modified, Vary, Content-DPR, `X-Xi-Origin-*`/`X-Xi-Result-*`, plus internal `X-Xi-Format`, `X-Xi-Ext`, `X-Xi-Source-Cache-Control`/`-Expires` for passthrough), and the request-dependent ones (Cache-Control, Content-Disposition, Cache-Tag, Link) are recomputed, so a hit's headers equal the miss's; `If-None-Match`/`If-Modified-Since` get a 304 and HEAD the GET's headers from the entry. `X-Xi-Cache: HIT|MISS|BYPASS` is a debug header (`XI_OBSERVABILITY_DEBUG_HEADERS`) and the access log's `cache` attribute. Stored: 200 GET responses of process requests from their own source (never a fallback, raw, info, 304 or HEAD) whose Cache-Control has neither `no-store` nor `private`; the body adopts the encoder's blob (refcount, no copy). `bypass_cache` with `XI_CACHE_ALLOW_BYPASS` skips lookup and store. Errors are never stored (no negative result caching; the fetcher's origin cache remembers not-found for `XI_CACHE_ORIGIN_NEGATIVE_TTL`), and cache failures are logged and served as misses. The original cache is fetch's in-process LRU, optionally backed by `XI_CACHE_ORIGIN_STORES` (§7).

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

When a fallback image is configured (global or per request), a failure after the source resolved (stages 9–12) of kind `NotFound`, `Forbidden` (origin 403 or SSRF block at fetch; an allowlist refusal at stage 5 never falls back), `UpstreamError`, `UpstreamTimeout`, `Unsupported`, `Corrupt`, `SourceTooLarge` or `LimitExceeded` serves it with `XI_FALLBACK_STATUS` (default 200) and `XI_FALLBACK_TTL` (default 1 m). Client errors, `NotAcceptable`, overload, our own `Timeout`, `Canceled` and `Internal` are never masked; `raw` requests never fall back. A request with `OnErrorRedirect` (Cloudflare `onerror=redirect`, wsrv `default=`) is answered for the same kinds, ahead of the fallback image, with a 302 (`internal/server/redirect.go`, one code path): to its fallback URL when it has one (wsrv `default=<url>`) and that URL is absolute http(s) and passes the allowlist, else to its own http(s) source; the redirect carries the error Cache-Control (`XI_CACHE_HEADERS_ERROR_TTL`). Error bodies are `text/plain` by default, JSON (`{"error":{"kind":…,"message":…,"request_id":…}}`) when `Accept: application/json`; development mode adds a stack trace.

## 13. Observability

- Logs: `log/slog` JSON to stdout; one access line per request (`request_id`, dialect, source name, status, bytes, stage timings, cache status, output format, dimensions). Source URLs are logged with query strings stripped; signatures never logged.
- Metrics (`/metrics` on the admin listener `XI_ADMIN_ADDR`, default `127.0.0.1:9090`; set `:9090` to scrape from outside the pod), client_golang on a private registry: `xi_requests_total{dialect,status,format}`, `xi_request_duration_seconds{dialect,status,format}`, `xi_requests_in_flight`, `xi_stage_duration_seconds{stage}`, `xi_panics_total`, `xi_source_fetch_duration_seconds{source,type}`, `xi_source_bytes_total{type}`, `xi_fetch_coalesced_total`, `xi_cache_requests_total{store,result}`, `xi_cache_evictions_total{store}`, `xi_cache_bytes{store}`, `xi_cache_entries{store}`, `xi_cache_writes_dropped_total`, `xi_queue_depth`, `xi_workers_busy`, `xi_semaphore_wait_seconds`, `xi_overload_total{reason}`, `xi_raw_decodes_total{path}`, `xi_vips_mem_bytes`, `xi_vips_mem_highwater_bytes`, `xi_vips_allocs`, `xi_build_info{version,commit,goversion}`, Go runtime and process collectors. Implemented in `internal/obs/metrics`; the limiter reports through `metrics.LimiterObserver` and vips through `metrics.VipsCollector`.
- Tracing: OpenTelemetry OTLP when `OTEL_EXPORTER_OTLP_ENDPOINT` (or `XI_OTEL_ENABLED=true`) is set, otherwise a no-op provider and no middleware; one span per stage, W3C propagation, outbound propagation to sources off by default.
- `/healthz` (liveness, no vips calls), `/readyz` (vips initialised, sources' static config valid, not draining). pprof (incl. `goroutineleak`) on the admin listener only when `XI_ADMIN_PPROF=true`.

## 14. Docker and base image build

Two repositories of artefacts, one Git repo:

```
docker/
  base/                        forked from imgproxy-docker-base (MIT; imgproxy itself is Apache-2.0), attribution kept
    versions.sh                pinned versions + SHA256 of every tarball and toolchain
    build-deps.sh              builds into /opt/xtandard (prefix), license bundle, license gate
    check-licenses.sh          GPL/AGPL gate (ADR 0006)
    Dockerfile                 ubuntu:noble deps → `builder` (Go + libvips dev) and `runtime` (libs only) targets
    magick-policy.xml          ImageMagick policy (PSD/PSB read only), installed as etc/ImageMagick-7/policy.xml
Dockerfile                     app (repo root): FROM <base>-builder → go build → FROM <base> runtime
compose.smoke.yml              app + static origin, driven by scripts/smoke.sh (WP-00) and scripts/verify-app-image.sh (WP-37)
compose.acceptance.yml         app (env-only config) + app-tiny + nginx origin + RustFS, driven by scripts/acceptance.sh (WP-39, M1 release gate; suite in test/acceptance)
```

**Base image** `ghcr.io/xantiagoma/xtandard-image-base:<vips-version>-r<N>-<flavor>` (flavor `slim` | `full`; the `runtime` target) plus `…-<flavor>-builder` (the `builder` target: Go 1.27.1, golangci-lint, go-task, headers and pkg-config files; used for CI, `task docker:test` and the app build stage), built weekly and on `docker/base/**` change, amd64 + arm64 on native runners, cosign-signed, SBOM (syft) attached. See `docker/base/README.md`.

Contents of `/opt/xtandard`:

| Path | Contents |
|---|---|
| `lib/` | (slim, M1) libvips 8.18.x, glib/gobject/gio (minimal), expat, libjpeg-turbo (+ optional mozjpeg), libspng, libpng, zlib-ng, cgif, quantizr, libwebp, libheif + libde265 + dav1d + aom + kvazaar, libjxl + highway + brotli, libtiff, lcms2, libexif, librsvg + cairo + pango + harfbuzz + freetype + fontconfig + fribidi, OpenJPEG, libultrahdr, jemalloc; added in M2 (WP-66): OpenEXR/Imath, LibRaw 0.22.x, PDFium (prebuilt, BSD-3-Clause), ImageMagick 7 (PSD coder only, no delegates; loaded through the `vips-magick.so` module). `full` adds FFmpeg 8 LGPL (`libavformat`, `libavcodec`, `libavutil`, `libswscale`; decoders h264/hevc/vp8/vp9/av1 via dav1d, demuxers mp4/mov/matroska/webm/gif; encoders only libvpx-vp9 and libaom-av1 for GIF→video, see open questions) and ONNX Runtime 1.30 CPU. |
| `lib/pkgconfig`, `include/` | build stage only; stripped from runtime |
| `share/fonts/` | Noto Sans + DejaVu (OFL / Bitstream-Vera licenses) for text watermarks |
| `share/models/` | `full`: YuNet face model (MIT), YOLOX-s object model (Apache-2.0), with LICENSE files |
| `etc/ImageMagick-7/policy.xml` | restrictive policy (§10) |
| `share/licenses/` | every dependency's license + `SOURCES.md` with source URLs (LGPL compliance) |

All libraries built with `-O3 -fstack-protector-strong -D_FORTIFY_SOURCE=2`, RPATH `$ORIGIN`, stripped. No GPL component: `build-deps.sh` runs a license check that fails the build on GPL/AGPL (see ADR 0006).

**App image** (`Dockerfile` at the repo root, so the build context is the module; multi-stage; release process in `docs/RELEASE.md`). WP-37 keeps the WP-00 layout: the runtime stage is the base `runtime` target itself (`FROM ${BASE_RUNTIME}`), which already is `ubuntu:noble` + `ca-certificates`/`tzdata` + the runtime subset of `/opt/xtandard` + UID 65532, so copying the libraries into a bare `ubuntu:noble` would rebuild the same layers and lose the base image's verified contents (`verify-image.sh`, license gate) for no size gain (slim measured 63.9 MB compressed on arm64). Both base references are Dockerfile `ARG`s (`BASE_BUILDER`, `BASE_RUNTIME`) whose defaults are the tags derived from `docker/base/versions.sh`; CI and the release workflow resolve them to `@sha256:` digests with `scripts/base-image-ref.sh`, and `--check` fails when the defaults drift from `versions.sh`.

1. `FROM ${BASE_BUILDER} AS build` (`ghcr.io/xantiagoma/xtandard-image-base:<vips>-r<N>-<flavor>-builder`, Go 1.27.1 included); `CGO_ENABLED=1 go build -trimpath -buildvcs=false -pgo=auto -ldflags "-s -w -X main.version=… -X main.commit=… -X main.date=…" [-tags ffmpeg]` with `GOAMD64=v2` on amd64 (GOARM64 default v8.0) and `main.date` from `SOURCE_DATE_EPOCH`; the build fails unless `go version -m` reports `-pgo=default.pgo`. The unstripped artefact comes from the `debug` target (`docker build --target debug --output type=local,dest=dist .`) and is attached to the GitHub release.
2. `FROM ${BASE_RUNTIME} AS runtime` (`…-base:<vips>-r<N>-<flavor>`), binary to `/usr/local/bin/xtandard-image`; OCI labels `source`, `version`, `revision`, `licenses=MIT`, `description`, `base.name` (`created` set by the release workflow).
3. `ENV LD_LIBRARY_PATH=/opt/xtandard/lib LD_PRELOAD=/opt/xtandard/lib/libjemalloc.so.2 MALLOC_ARENA_MAX=2 MAGICK_CONFIGURE_PATH=/opt/xtandard/etc/ImageMagick-7 FONTCONFIG_PATH=/opt/xtandard/etc/fonts VIPS_BLOCK_UNTRUSTED=1` (repeated in the app image even where the base sets them), plus `VIPS_WARNING=0 TMPDIR=/tmp XDG_CACHE_HOME=/tmp/.cache XI_SERVER_ADDR=:8080`.
4. `USER 65532:65532` (user `xtandard`, no shell needed), read-only root filesystem compatible: run with `--read-only --tmpfs /tmp` (compose `read_only: true` + `tmpfs: [/tmp]`; Kubernetes `readOnlyRootFilesystem: true` + an `emptyDir` at `/tmp`). `/tmp` receives the fontconfig cache and, if enabled at a path there, the disk cache; `scripts/verify-app-image.sh` checks that the rootfs rejects writes.
5. `EXPOSE 8080 9090` (the admin listener defaults to `127.0.0.1:9090`; set `XI_ADMIN_ADDR=:9090` to scrape it); `HEALTHCHECK --interval=10s --start-interval=1s CMD ["/usr/local/bin/xtandard-image","health","--url","http://127.0.0.1:8080/healthz"]` (no curl in the image).
6. `ENTRYPOINT ["/usr/local/bin/xtandard-image"]`, `CMD ["serve"]`.

Tags: `ghcr.io/xantiagoma/xtandard-image:{latest,slim,<ver>,<ver>-slim}` and `:{full,<ver>-full}` (`<ver>` without the leading `v`; `latest` and `<ver>` point at slim until `full` exists; prereleases get only `<ver>` and `<ver>-slim`). Published by `.github/workflows/release.yml` on `v*` tags: per-arch native builds pushed by digest, `imagetools create`, cosign keyless signature, syft SBOM attestation and SLSA provenance (`docs/RELEASE.md`). Size targets: slim ≤ 110 MB compressed, full ≤ 170 MB. The M1 acceptance suite (`scripts/acceptance.sh`, `test/acceptance` with `-tags acceptance`, WP-39) boots each final image with `docker compose` (plus RustFS and a static origin; go-standards §8, no testcontainers) and checks it over HTTP for both architectures (arm64 on native runners): CI job `acceptance` and a gate in `release.yml` before anything is tagged (`docs/RELEASE.md`).

## 15. Testing architecture

- Unit: every dialect parser is table-tested URL → expected `transform.Request` (JSON fixtures in `testdata/`), including round-trips generated by upstream SDKs (imgproxy URL builder, `@imgix/js-core`, `cloudinary` SDK, `unpic` transformers) to prove compatibility.
- Planner: request + meta → expected plan, pure Go.
- Golden images: `testdata/golden/<pkg>/<case>.png` via `internal/testutil/golden.Compare`; compared with the in-house SSIM/DSSIM (`internal/testutil/ssim`, Wang et al. 2004 on luma) ≤ tolerance per case (default `golden.DefaultTolerance` = 0.015, calibrated so a JPEG q85 re-encode passes and a 1 px shift fails), regenerated with `GOLDEN_UPDATE=1 go test ./...` or `-update` on packages importing the harness. See `testdata/README.md`.
- Fixtures per format (JPEG CMYK/progressive/EXIF-rotated, PNG 16-bit/palette/APNG, GIF animated, WebP animated, AVIF 10-bit, HEIC, JXL, TIFF multipage, BMP variants, ICO, SVG malicious set, PDF, PSD, RAW: CR3, NEF, ARW, DNG) in `testdata/fixtures/<format>/` (the M1 set landed with WP-36; M2 formats are added by their WPs), all with redistributable licenses recorded in `testdata/LICENSES.md`.
- Security: SSRF matrix (rebinding test with custom resolver), signature vectors from upstream docs, fuzzing (`go test -fuzz`) for every parser, the SVG sanitiser and the BMP/ICO decoders.
- Integration: `//go:build integration` tests against RustFS and the other compose.test.yml backends (go-standards §8). Acceptance: `//go:build acceptance` black-box suite in `test/acceptance` against the app image + RustFS + a local HTTP origin (compose.acceptance.yml, `task acceptance`).
