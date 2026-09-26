# Go engineering standards: xtandard-image

Mandatory for every contributor and coding agent. Verified against live sources on 2026-09-26. When a rule and your memory disagree, the rule wins. If you believe a rule is wrong, stop and raise it; do not silently deviate.

## 1. Toolchain

**Go version.** Go 1.27.0 shipped 2026-08-19; current stable is **go1.27.1** (https://go.dev/doc/devel/release, https://go.dev/dl/?mode=json). Pin both directives:

```
go 1.27.0
toolchain go1.27.1
```

This is an application, not a library, so pin the newest language version on purpose. Do not accept the `go mod init` default: since 1.26 it writes N-1 (https://go.dev/doc/go1.26#go-command). We need 1.27 for: generic methods, `encoding/json` backed by v2, the GA `goroutineleak` profile, `synctest.Sleep`, `httptest.NewTestServer`, and `http.Server.MaxHeaderValueCount` (https://go.dev/doc/go1.27). Bump `toolchain` on each point release. CI uses `actions/setup-go` with `go-version-file: go.mod`, which uses the `toolchain` directive when it is present (https://github.com/actions/setup-go). The local machine runs 1.25.5, and `GOTOOLCHAIN=auto` downloads 1.27.1 automatically.

**Formatting.** Use `gofumpt` v0.12.0 (a stricter gofmt) plus `goimports`, both run through golangci-lint formatters. CI runs `golangci-lint fmt --diff` and fails on any diff.

**Lint.** Use golangci-lint **v2.14.0**, which is built with go1.27.0 (https://github.com/golangci/golangci-lint/releases). Install the release binary. Never add it through `go get` or the `tool` directive, because its dependency graph would pollute ours. Commit `.golangci.yml`. The baseline below was checked with `golangci-lint config verify`:

```yaml
version: "2"
linters:
  default: standard   # errcheck, govet, ineffassign, staticcheck, unused
  enable: [bodyclose, canonicalheader, containedctx, depguard, durationcheck,
    errname, errorlint, exhaustive, fatcontext, forcetypeassert,
    gocheckcompilerdirectives, gochecknoinits, gocritic, gosec, iface, intrange,
    makezero, misspell, modernize, nilerr, nilnesserr, noctx, nolintlint,
    perfsprint, prealloc, promlinter, recvcheck, revive, sloglint, spancheck,
    thelper, tparallel, unconvert, unparam, usestdlibvars, usetesting, wastedassign]
  settings:
    govet: { enable-all: true, disable: [fieldalignment, shadow] }
    gocritic: { enabled-tags: [diagnostic, performance] }
    sloglint: { attr-only: true, context: scope, static-msg: true, key-naming-case: snake }
    nolintlint: { require-explanation: true, require-specific: true }
    exhaustive: { default-signifies-exhaustive: true }
    depguard: { rules: { main: { deny: [ ... see §10 ... ] } } }
  exclusions:
    generated: strict
    presets: [std-error-handling, common-false-positives]
formatters:
  enable: [gofumpt, goimports]
  settings: { goimports: { local-prefixes: [github.com/xantiagoma/xtandard-image] } }
```

Why these linters: `modernize` enforces current idioms such as `min`/`max`, `slices`, range-over-int and `wg.Go`. `errorlint` catches `==` comparisons against wrapped errors. `bodyclose` and `noctx` catch leaks on the upstream fetcher. `sloglint` and `promlinter` keep logs and metrics consistent. `spancheck` catches unended OTel spans. `gosec` covers the SSRF-adjacent fetch code. `nolintlint` forces a written reason on every suppression. We skip style-only linters (wsl, lll, varnamelen, exhaustruct), which cost review time and prevent no bugs.

**Other gates, all in CI on every PR:**
- `go vet ./...`, run separately from golangci. It is cheap and uses the toolchain's exact analyzer set. Since 1.27, `go test` also runs the `stdversion` check (https://go.dev/doc/go1.27).
- staticcheck runs inside golangci. Do not run it a second time.
- `go fix -diff ./...` must print nothing. Since 1.26, `go fix` is the home of the modernizers and supports `//go:fix inline` (https://go.dev/doc/go1.26#go-command).
- govulncheck **v1.8.0** (`golang.org/x/vuln`) also runs nightly. Fail on any reachable vulnerability.
- `go test -race -shuffle=on ./...`.
- `go mod tidy -diff` must be clean.

Dev tools other than golangci go in a separate `tools/go.mod` with `tool` directives (https://go.dev/doc/go1.24#tools). Run them with `go tool -modfile=tools/go.mod govulncheck ./...`. This keeps benchstat, govulncheck and similar tools out of the application's module graph.

**Layout (single binary):**

```
cmd/xtandard-image/main.go    # wiring only: config, signals, run(); default.pgo lives here
internal/vips/                # the ONLY package that imports "C" (plus *.c/*.h shim)
internal/transform/           # normalised Request model (pure Go)
internal/dialect/<name>/      # URL parsers per dialect: imgproxy, query, imgix, ... (fuzzed)
internal/security/            # sign (HMAC per dialect), enc (AES source URLs), ssrf, svg
internal/fetch/<backend>/     # sources: http, s3, gcs, azure, fs, ftp; singleflight fetcher
internal/codec/               # sniff/probe, BMP/ICO loaders, format decision
internal/pipeline/            # operation graph over internal/vips; pure Go
internal/server/              # ServeMux handlers, middleware, error→status mapping
internal/config/  internal/errs/  internal/obs/ (slog, metrics, tracing)
testdata/                     # golden images, fuzz corpora
```

`docs/ARCHITECTURE.md` §3 is authoritative for package names and dependency direction; this list is a summary.

Everything lives under `internal/`. Add `pkg/` only when an external consumer really exists, and write an ADR first. Keep one package per concept. Never create `util`, `common` or `helpers` packages. `main` stays under 100 lines and calls `run(ctx, args, getenv, stdout) error`.

## 2. HTTP server

Use stdlib `net/http` and `ServeMux` with method and wildcard patterns (Go 1.22+). Never add chi, gin, echo or gorilla. Note that since 1.26, ServeMux redirects use 307 instead of 301 (https://go.dev/doc/go1.26#nethttppkgnethttp).

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /healthz", s.health)
mux.HandleFunc("GET /readyz", s.ready)
mux.HandleFunc("GET /{sig}/{path...}", s.image) // path parsed by internal/dialect, not the mux
```

Always set every timeout on `http.Server`. Never call `http.ListenAndServe`.

```go
srv := &http.Server{
    Handler:           h,
    ReadHeaderTimeout: 5 * time.Second,  // Slowloris
    ReadTimeout:       15 * time.Second,
    WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
    IdleTimeout:       90 * time.Second,
    MaxHeaderBytes:    32 << 10,
    BaseContext:       func(net.Listener) context.Context { return ctx },
    ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
}
```

- **Per-request budget:** use `context.WithTimeoutCause(r.Context(), cfg.RequestTimeout, errRequestTimeout)`. When one route legitimately needs longer, extend it with `http.NewResponseController(w).SetWriteDeadline(...)`, not by raising the global limit.
- **Graceful shutdown:** create `ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`. Since 1.26, `context.Cause(ctx)` names the signal that fired (https://go.dev/doc/go1.26). On shutdown, in order: (1) flip readiness to 503, (2) wait `cfg.DrainDelay` so the load balancer deregisters us, (3) call `srv.Shutdown(ctxWithTimeout)`, (4) wait for in-flight vips work to finish, (5) call `vips.Shutdown()`. Treat `http.ErrServerClosed` as a clean exit.
- **Bodies:** wrap every request body (upload or POST) in `http.MaxBytesReader`, and use `errors.AsType[*http.MaxBytesError]` to map the error to 413. Limit upstream response bodies with `io.LimitReader(resp.Body, max+1)` and fail when the result exceeds `max`.
- **Streaming:** write the encoded image straight from the vips output buffer with one `w.Write`. Set `Content-Length` when the size is known. Use `ResponseController.Flush` only for real streaming. Never buffer through `bytes.Buffer`.
- **HTTP/2:** configure it with `srv.Protocols` (Go 1.24+). Behind a TLS-terminating proxy that speaks h2c, enable `p.SetUnencryptedHTTP2(true)`, gated by config and off by default (https://pkg.go.dev/net/http#Protocols). Never import `golang.org/x/net/http2/h2c`. Set `srv.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: ...}` to match the worker queue.
- **Allocation-light handlers:** use `r.PathValue`. Pre-build static header values. Never call `fmt.Sprintf` on the hot path (use `strconv.Append*`). Never create `regexp` objects per request. Never read the request body when you don't need it. Every allocation per request must be justified in a benchmark (§8).
- **Panics:** add a recover middleware as the outermost layer. It logs the stack, increments a metric and returns 500, but re-panics `http.ErrAbortHandler`. Remember it cannot catch a SIGSEGV in C.

## 3. Concurrency

- **Context:** `ctx context.Context` is the first parameter of anything that does I/O, blocks or calls into vips. Never store a context in a struct (the `containedctx` linter enforces this). Never pass `context.Background()` below `main`. Use `context.WithoutCancel` only for work that must outlive the request, such as async cache writes, and give that work its own timeout.
- **cgo work cannot be cancelled mid-call.** Check `ctx.Err()` between pipeline steps, as imgproxy (`processing/pipeline.go`) and imagor (`vipsprocessor/process.go`) both do.
- **Bound the vips work with a weighted semaphore and never with a worker pool of channels.** Use per-request goroutines (net/http already does), gated by `golang.org/x/sync/semaphore` (v0.23.0). Copy imgproxy's two-semaphore design (`workers/workers.go`):

```go
// queue = workers + queueSize; TryAcquire fails fast → 429/503 with Retry-After
if !s.queue.TryAcquire(1) { return nil, ErrOverloaded }
if err := s.workers.Acquire(ctx, 1); err != nil { s.queue.Release(1); return nil, err }
```

  The worker count defaults to `runtime.GOMAXPROCS(0)` (imgproxy uses 2×). Benchmark before you change it.
- **Deduplicate identical in-flight requests** with `golang.org/x/sync/singleflight` on the canonical cache key, as imagor does (`imagor.go`).
- **Fan-out:** use `errgroup.WithContext` plus `g.SetLimit(n)`, for example to fetch the source and the watermark in parallel. For fire-and-wait without errors use `sync.WaitGroup.Go` (1.25, https://pkg.go.dev/sync#WaitGroup.Go). Never write the `wg.Add(1); go func(){ defer wg.Done() }` pattern.
- **Goroutine hygiene:** every `go` statement has a documented owner and exit path, and is tied to a context or a close. Use `time.NewTimer` together with `Stop`, and remember that `time` channels are always unbuffered in 1.27 (https://go.dev/doc/go1.27). In staging, scrape `/debug/pprof/goroutineleak`, which is GA in 1.27 and verified in `pprof.Profiles()`.
- **`sync.Pool`:** use it only for small, uniformly sized scratch objects such as header builders and parse buffers. Store pointers (`*[]byte`). Drop anything above a cap (for example 64 KiB) instead of returning it to the pool. Never pool image-sized buffers, because image bytes live in C memory (§4).
- **GOMAXPROCS:** since 1.25 the runtime respects cgroup CPU limits and updates when they change (https://go.dev/doc/go1.25#runtime). Never import `go.uber.org/automaxprocs`.
- **Iterators:** return `iter.Seq`/`iter.Seq2` for lazy sequences such as options tokens or animation frames. Use `strings.SplitSeq`/`FieldsSeq` (1.24) instead of `strings.Split` on hot paths. Use `slices`, `maps`, `min`, `max`, `clear`, `for i := range n` and `strings.CutLast` (1.27). Never import `golang.org/x/exp/slices`.
- **Generics:** use them for algorithmic reuse, such as typed caches, `errors.AsType`, and the new generic methods (1.27) where they remove duplication (https://go.dev/doc/go1.27#language). Do not use them for "flexibility" or to hide interface dispatch in hot paths. Never write a generic abstraction over vips operations. Write concrete functions instead.
- **Testing concurrency:** use `testing/synctest`, which is GA since 1.25 and gained `synctest.Sleep` in 1.27. Combine it with `httptest.NewTestServer` (1.27, an in-memory network) to test timeouts, retries and shutdown deterministically. Never write `time.Sleep` in tests.

## 4. cgo and libvips

**Structure (copying the good parts of imgproxy and imagor/vipsgen):**
- `internal/vips` is the only package that imports `"C"`. Nothing else sees a `*C.VipsImage`. The exported API is small and Go-typed: `Load`, `Thumbnail`, `Resize`, `Save` and similar.
- imgproxy hand-writes a C shim (`vips/vips.c`, about 33 KB) whose functions do several vips calls per Go call and each take one fixed-arity C function. That crosses the boundary once per step, avoids varargs from Go, and lets `#cgo noescape/nocallback` apply (https://github.com/imgproxy/imgproxy/tree/master/vips). **Copy this for hot paths.**
- vipsgen v1.3.11 generates about 300 type-safe operations from GObject introspection, pre-generated for libvips 8.16, 8.17 and 8.18 (https://github.com/cshum/vipsgen). **Use it for the long tail and as a reference for signatures.** Wrap it inside `internal/vips` and never leak its types.
- Both projects disable the vips operation cache (`vips_cache_set_max(0)`, `vips_cache_set_max_mem(0)`), because it can SIGSEGV on musl and doesn't help a tuned pipeline. Both default `vips_concurrency_set(1)` and get parallelism from the request semaphore instead (imgproxy `vips.go` `Init`; imagor `config/vipsconfig`). **We do the same.** Benchmark the alternative (fewer workers, `VIPS_CONCURRENCY`>1) for the latency tier before changing anything.
- Streaming I/O goes through a custom `VipsSource`/`VipsTarget` that calls back into Go. It is identified by `runtime/cgo.Handle` and never by a Go pointer (imgproxy `vips/source.go`, vipsgen `connection.go`). Delete the handle exactly once, guarded by an `atomic`.
- Use `runtime.LockOSThread()` for the whole processing of one image, then call `vips_error_clear(); vips_thread_shutdown()` on that thread before unlocking (imgproxy `processing.go` + `vips_cleanup`). libvips keeps thread-private data that leaks unless `vips_thread_shutdown` runs on the thread that used it (https://github.com/libvips/libvips/blob/master/libvips/iofuncs/init.c).
- **The vips error buffer is process-global**, behind `vips__global_lock` (https://github.com/libvips/libvips/blob/master/libvips/iofuncs/error.c). Under concurrency, messages from different requests can interleave. In the shim, capture failures with `vips_error_buffer_copy()` right at the failure site. Choose the HTTP status from *which operation failed* (load → 422, save → 500), never by parsing the message text. Use the message only as a log attribute.

**Memory rules:**
- Never pass Go memory to a C function that keeps it after returning. `vips_image_new_from_buffer` and `vips_source_new_from_memory` keep it, because decoding is lazy. For bytes you download yourself, allocate in C and let vips own the memory. `unsafe.Slice` gives zero-copy writes:

```go
p := C.malloc(C.size_t(n))                       // freed by vips via blob free-fn
buf := unsafe.Slice((*byte)(p), n)
if _, err := io.ReadFull(body, buf); err != nil { C.free(p); return err }
img := C.xi_load_owned(p, C.size_t(n))           // shim wraps vips_blob_new(free, p, n)
```

- For output, wrap the vips blob with `unsafe.Slice((*byte)(ptr), size)`, write it to the response, then unref it. Don't copy it with `C.GoBytes` (imgproxy `Save`).
- `C.CBytes` and `C.GoBytes` copy the data. Use them only for small, cold data such as ICC profiles and EXIF blobs. Use `unsafe.String`/`unsafe.StringData` only for read-only strings, and never store the result.
- Transient Go pointers, where C only reads during the call, are legal. Mark those functions `#cgo noescape f` and `#cgo nocallback f` (1.24+) to skip heap escape and callback preparation (https://pkg.go.dev/cmd/cgo). Use `runtime.Pinner` only when C must hold a Go pointer inside a struct during a call. Keep the Pinner alive and `Unpin` it in a `defer` (https://pkg.go.dev/runtime#Pinner).
- Cache constant C strings, such as operation and field names, in a `sync.Map` (imgproxy `cached_c_strings.go`). Never `C.CString` per request.
- Free resources deterministically with `defer img.Close()`. Treat `runtime.AddCleanup` (1.24) as a leak backstop that logs and counts, never as the release mechanism. Never use `runtime.SetFinalizer`.
- Leave `GODEBUG=cgocheck=1` (the default) on in all environments. The `-race` CI job also builds with `GOEXPERIMENT=cgocheck2` (https://pkg.go.dev/cmd/cgo).

**Build flags:** use `#cgo pkg-config: vips` only. Never hard-code `-I`/`-L`. Set `PKG_CONFIG_PATH` for `/opt/app/lib` builds, as imagor's Dockerfile does. On macOS, vipsgen needs `CGO_CFLAGS_ALLOW=-Xpreprocessor`.

**Process memory tuning (runtime image):**
- Preload jemalloc with `LD_PRELOAD=libjemalloc.so.2` and set `MALLOC_ARENA_MAX=2` as a glibc fallback. Both imgproxy (`docker/Dockerfile`, `entrypoint.sh`) and imagor (`Dockerfile`) ship this. glibc arena fragmentation under many threads is the main cause of apparent memory leaks. When running on glibc without jemalloc, periodically call `debug.FreeOSMemory()` followed by `malloc_trim(0)` (imgproxy `memory/free_linux.go`).
- `GOMEMLIMIT` / `debug.SetMemoryLimit` covers **only Go-runtime memory, not C allocations** (https://pkg.go.dev/runtime/debug#SetMemoryLimit). Set it to the Go share, computed at startup as a fraction of cgroup `memory.max` (default 30%, overridable). Never set it to the container limit, because vips memory dominates. Keep `GOGC=100` and never set `GOGC=off`.
- Bound C memory by input limits (max pixels, max bytes, max frames) checked from the header *before* decoding, and by the worker semaphore. Export `vips_tracked_get_mem()` / `vips_tracked_get_mem_highwater()` as gauges (imgproxy `vips/stats`).
- Green Tea GC is the default since 1.26 and needs no action (https://go.dev/doc/go1.26#runtime). cgo call overhead dropped about 30% in 1.26, but still batch work per crossing.

## 5. Logging and observability

- Use `log/slog` with `slog.NewJSONHandler(os.Stdout, ...)`. Never use logrus, zap or zerolog.
- Write a small handler wrapper that adds `request_id`, `trace_id` and `span_id` from `ctx`, and always log with `logger.LogAttrs(ctx, level, "static msg", slog.String(...))`. `LogAttrs` avoids `any` boxing, and the linter enforces static messages and snake_case keys. Log one access line per request, plus errors. Never log per-operation at Info. Never log signed URLs or secrets. Use `slog.NewMultiHandler` (1.26) only when a second sink is really needed.
- **Metrics:** use `prometheus/client_golang` **v1.24.1** on a dedicated registry (`prometheus.NewRegistry()`), never the global default. Serve `promhttp.HandlerFor(reg, ...)` on the admin listener. Track RED metrics per route and format, a queue-depth gauge, semaphore wait time, vips memory, and input and output bytes. Never use a label for an unbounded value such as URL or source host.
- **Tracing:** use OpenTelemetry Go **v1.46.0** (SDK) and contrib **v0.71.0** (`otelhttp`) (https://github.com/open-telemetry/opentelemetry-go/releases). Import semconv as `go.opentelemetry.io/otel/semconv/v1.43.0`, the newest in v1.46.0. Tracing is off unless `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Configure exporters with `go.opentelemetry.io/contrib/exporters/autoexport`. Set the composite propagator to TraceContext plus Baggage. Wrap the mux with `otelhttp.NewHandler`, and add child spans only around fetch, decode, pipeline and encode. On shutdown, `errors.Join` all provider `Shutdown` errors (https://opentelemetry.io/docs/languages/go/getting-started/). Don't adopt the OTel logs SDK: `sdk/log` is still v0.22.0.
- **pprof:** serve it on a separate admin listener (`cfg.AdminAddr`, localhost by default) and only when `cfg.Pprof=true`. Register the handlers explicitly on the admin mux. Never serve `http.DefaultServeMux` on the public port. The admin port also exposes `goroutineleak` (1.27).

## 6. Errors

- Wrap with context using `fmt.Errorf("fetch %s: %w", host, err)`. Messages are lowercase, have no trailing punctuation and never say "failed to".
- Match wrapped errors with `errors.Is` or `errors.AsType[*T](err)` (1.26, generic and faster than `errors.As`, https://pkg.go.dev/errors#AsType). Never use `==` or a type switch on wrapped errors.
- Use sentinels (`var ErrNotFound = errors.New(...)`) for conditions callers branch on. Use typed errors (`type UpstreamError struct{ Status int; Err error }`) when you need to carry data. Name them `ErrXxx` and `XxxError` (the errname linter enforces this).
- Give every error that reaches a handler a single mapping point, `server.writeError(w, r, err)`, that resolves the status through an interface:

```go
type statusError interface { error; HTTPStatus() int; PublicMessage() string }
if se, ok := errors.AsType[statusError](err); ok { status, msg = se.HTTPStatus(), se.PublicMessage() }
```

  Anything else becomes 500 with a generic body. Never leak internal messages to clients. `context.DeadlineExceeded` whose cause is our timeout maps to 504. A client disconnect (`context.Canceled`) maps to 499, which is logged but never written.
- Combine errors from cleanup, validation and shutdown with `errors.Join`.
- Never panic in library or handler code. `Must*` helpers are allowed only for package-level constants at init.

## 7. Configuration

- Use `github.com/caarlos0/env/v11` **v11.4.1** (`env.ParseAs[Config]()`). It has zero dependencies, generics, `required`/`notEmpty`/`envDefault`/`envPrefix`/`file` tags and `AggregateError`, and its maintainers consider it feature-complete, which means a stable API (https://github.com/caarlos0/env).
  - Rejected: `sethvargo/go-envconfig`, which shipped a breaking v2.0.0 on 2026-09-23 (decoder interface removed, module path changed) and has too much churn right now (https://github.com/sethvargo/go-envconfig/releases/tag/v2.0.0).
  - Rejected: `kelseyhightower/envconfig`, whose last release was v1.4.0 in 2019.
- Put all settings in one `Config` struct built once in `main` and passed down explicitly. Prefix variables with `XI_`. Never read `os.Getenv` outside `internal/config`. Never use globals or `init()`.
- Validate with an explicit `func (c Config) Validate() error` that returns `errors.Join` of every violation. Fail fast at startup. Don't use `go-playground/validator`: it is reflection-heavy, uses stringly-typed tags, and our rules are cross-field.
- If a YAML file is ever added (presets, for example), use `github.com/goccy/go-yaml` v1.19.2 with `yaml.UnmarshalWithOptions(b, &c, yaml.Strict(), yaml.DisallowUnknownField())`. `gopkg.in/yaml.v3` is archived (repo archived April 2025). Its successor `go.yaml.in/yaml/v3` receives security fixes only, and `v4` is still `v4.0.0-rc.6` (https://github.com/yaml/go-yaml). Environment variables stay the source of truth, and they override the file.

## 8. Testing

- Use stdlib `testing` plus `github.com/google/go-cmp/cmp` for diffs. Don't use testify, gomock or ginkgo. Write fakes by hand against small interfaces that the consumer defines.
- Write tests table-driven with `t.Run(tc.name, ...)`, and call `t.Parallel()` in every test and subtest. The exceptions are tests that use `t.Chdir`/`t.Setenv`, since `Chdir` cannot run in parallel (https://pkg.go.dev/testing#T.Chdir). Use `t.Context()` (1.24) instead of `context.Background()`. Use `t.ArtifactDir()` (1.26) for debug output such as diff images.
- Initialise vips once per package in `TestMain` and shut it down after `m.Run()`.
- **Golden images:** store them under `testdata/golden/` and regenerate with `-update`. **Never compare encoded bytes**, which change with encoder versions. Decode both images and assert that dimensions, band count and format match and that the perceptual distance is below a tolerance (DSSIM/SSIMULACRA2, the same metric used for auto-quality).
- **Fuzzing:** every parser that takes attacker input (URL options, signatures, base64 source, range and accept headers) has a `FuzzXxx` with a seed corpus in `testdata/fuzz/`. Fuzz tests run for 60 s per target nightly, and any crash they find is committed to the corpus.
- **Race:** `go test -race` runs in CI. It does not see races inside C, which is why we lock threads and use `cgocheck2`.
- **Integration:** mark integration tests with `//go:build integration` and run them against `docker compose` services (RustFS, a slow HTTP origin). Tests read their endpoints from the environment and call `t.Skip` when an endpoint is unset. We choose compose over testcontainers-go (v0.44.0) to keep Docker client dependencies out of `go.mod`.
- **Benchmarks:** write every hot-path change with `for b.Loop()` (1.24; since 1.26 it no longer blocks inlining, https://go.dev/doc/go1.26), call `b.ReportAllocs()`, and use real images from `testdata/`. Compare with `go test -run='^$' -bench=. -count=10 -benchmem > new.txt` against main, then run `benchstat old.txt new.txt` (`golang.org/x/perf/cmd/benchstat`). A PR claiming a speed-up must paste the benchstat output. Anything under p<0.05 counts as noise.

## 9. Build and release

- Build command: `CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=$VERSION -X main.commit=$SHA" -o /out/xtandard-image ./cmd/xtandard-image`. The build runs inside Docker without `.git`, so version info comes from `-X`. Keep an unstripped build artifact for crash symbolication.
- **PGO:** commit `cmd/xtandard-image/default.pgo`. It is picked up automatically because `-pgo=auto` has been the default since 1.21, and the expected gain is 2–14% (https://go.dev/doc/pgo). To collect a profile, take a 30 s CPU profile from the admin listener of several production (or load-test) instances, merge them with `go tool pprof -proto a.pprof b.pprof > default.pgo`, and refresh the profile each release. The Go side is a minority of our CPU, so benchmark to confirm it helps.
- **GOAMD64:** v3 requires AVX2, BMI and FMA (https://go.dev/wiki/MinimumRequirements), and binaries crash on older or emulated CPUs such as QEMU `qemu64`. Our hot loops are C (libvips and highway use runtime dispatch), so the gain on the Go side is small. **Ship v2.** Publish a v3 variant only if benchstat shows at least 3%.
- **GOARM64:** keep the default `v8.0`, so the image still runs on Raspberry Pi 4 and older ARM hardware. Do not raise it without a benchmark.
- **Multi-arch:** build natively on each architecture. Use GitHub `ubuntu-24.04` plus `ubuntu-24.04-arm`: arm64 runners are GA for public repos and available in private repos since 2026-01-29 (https://github.blog/changelog/2026-01-29-arm64-standard-runners-are-now-available-in-private-repositories/). Push per-arch images by digest and merge them with `docker buildx imagetools create`.
  - No QEMU emulation: it is too slow for compiling libvips dependencies.
  - No `zig cc`: every one of roughly 20 C dependencies would also need cross-building, and zig-built cgo binaries have known pprof gaps for C code (https://github.com/golang/go/issues/59666).
- **Reproducible builds:** use `-trimpath`, a pinned toolchain, pinned base-image digests, `go.sum`, and `SOURCE_DATE_EPOCH=$(git log -1 --pretty=%ct)` for buildx (https://docs.docker.com/build/ci/github-actions/reproducible-builds/).
- **goreleaser: no.** Our artefact is a container with dynamically linked LGPL libraries, and goreleaser's cgo path needs goreleaser-cross or zig, which gives us nothing extra. A Makefile/justfile plus buildx is enough.

## 10. Dependencies

Use the stdlib first. Every new module needs an ADR line with the reason, its licence (MIT/BSD/Apache/ISC only) and the date of its last release. Keep `go.mod` small enough to review by eye.

**Blessed:**

| Module | Version | Why |
|---|---|---|
| `github.com/cshum/vipsgen` | v1.3.11 | generated libvips 8.18 bindings (MIT) |
| `golang.org/x/sync` (semaphore, errgroup, singleflight) | v0.23.0 | concurrency primitives the stdlib lacks |
| `golang.org/x/net` | v0.59.0 | only if needed (for example `idna`, `publicsuffix`) |
| `github.com/prometheus/client_golang` | v1.24.1 | metrics |
| `go.opentelemetry.io/otel`, `.../sdk`, contrib `otelhttp`/`autoexport` | v1.46.0 / v0.71.0 | tracing, opt-in |
| `github.com/caarlos0/env/v11` | v11.4.1 | env config |
| `github.com/goccy/go-yaml` | v1.19.2 | only if a YAML file is added |
| `github.com/google/go-cmp` | v0.7.0 | test diffs |
| `golang.org/x/image` | v0.46.0 | test-only decoders (WebP, BMP, TIFF) for the golden harness (ADR 0006) |
| `github.com/klauspost/compress` | v1.20.1 | only if we gzip or zstd non-image responses |

Storage SDKs such as AWS SDK v2 and GCS are allowed only inside `internal/fetch/<provider>` and require an ADR.

**Banned (enforced with depguard):**
- `github.com/pkg/errors`
- `gopkg.in/yaml.v2`/`v3`
- `github.com/stretchr/testify`
- logrus, zap and zerolog
- `io/ioutil`
- `math/rand` (use `math/rand/v2`)
- `golang.org/x/exp/*`
- gorilla/mux, chi, gin and echo
- `kelseyhightower/envconfig`
- `go.uber.org/automaxprocs`
- `github.com/google/uuid` (the stdlib `uuid` package exists since 1.27, with `uuid.NewV7()`)
- `encoding/json` alternatives such as jsoniter and sonic: `encoding/json` is backed by v2 in 1.27 (https://go.dev/doc/go1.27#encodingjsonv2)
- bimg and govips
- any GPL, AGPL or LGPL *Go* module
