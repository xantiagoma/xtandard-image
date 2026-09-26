# xtandard-image

A self-hosted image processing proxy written in Go 1.27 with libvips (cgo) that competes with imgproxy and imagor.

## Mandatory reading before writing any Go

- `docs/go-standards.md` is binding. It covers the toolchain, HTTP, concurrency, cgo, observability, errors, config, tests, build and dependencies.
- `docs/research/stack.md` covers the libvips version, format and loader choices, and the licence policy.
- `docs/adr/` holds the decisions. Any new dependency or deviation from the standards needs an ADR.

## Non-negotiables

1. Pin `go 1.27.0` and `toolchain go1.27.1` in go.mod. Never write code for older Go idioms.
2. All code lives under `internal/`. `cmd/xtandard-image/main.go` only wires things together. There is no `pkg/` without an ADR.
3. `internal/vips` is the ONLY package that imports `"C"`. No C type ever appears outside it.
4. Use stdlib `net/http.ServeMux`. Set every `http.Server` timeout. Shut down gracefully via `signal.NotifyContext`.
5. Bound vips work with `x/sync/semaphore` (workers plus queue, fail fast with 429/503). Never use channel worker pools. Keep libvips concurrency at 1 and its operation cache disabled.
6. Never let C keep a pointer to Go memory. Downloaded image bytes go into C-allocated memory that vips owns, and output is sent zero-copy from the vips blob.
7. Lock the OS thread (`LockOSThread`) for each image's processing, and call `vips_thread_shutdown` on that thread before unlocking.
8. `ctx` is the first parameter of anything that blocks. Check `ctx.Err()` between vips steps. Never store a context in a struct.
9. Wrap errors with `%w` and match them with `errors.Is` / `errors.AsType`. Map errors to HTTP status in one place. Never panic in handlers. Never leak internal error text to clients.
10. Log with `log/slog` JSON only, using `LogAttrs`, static messages and snake_case keys. Metrics use client_golang on a private registry. OTel tracing is opt-in. pprof is served only on the admin listener.
11. Config comes from environment variables via `caarlos0/env/v11` into one `Config` struct with a `Validate()` method. No `os.Getenv` outside `internal/config`. No globals and no `init()`.
12. Tests use stdlib `testing` and `go-cmp`, table-driven with `t.Parallel()` and `t.Context()`. Use `synctest` for time-dependent tests and never `time.Sleep`. Fuzz every parser. Golden images are compared perceptually, never byte-for-byte.
13. Every performance claim needs `for b.Loop()` benchmarks and `benchstat` output with `-count=10`.
14. Banned dependencies are listed in §10 of the standards and enforced by depguard. The licence allowlist is MIT/BSD/Apache/ISC for Go modules.

## Commands (CI runs all of them; run them before claiming done)

```sh
golangci-lint fmt --diff && golangci-lint run ./...   # v2.14.0 binary, not via go tool
go vet ./...
go fix -diff ./...                                     # must print nothing
go mod tidy -diff
go test -race -shuffle=on ./...
go tool -modfile=tools/go.mod govulncheck ./...
```

Local libvips on macOS: `brew install vips pkg-config` and `export CGO_CFLAGS_ALLOW=-Xpreprocessor`.

## Reference source code

`.refs/` (gitignored) holds shallow clones of imgproxy, imagor, thumbor, imaginary, weserv, unpic, provider SDKs (imgix, cloudinary, imagekit, cloudflare docs), libvips, sharp, libraw and more. Run `scripts/clone-refs.sh` to (re)populate. Before implementing a dialect, option, or vips call, `rg` the real implementation there instead of guessing.
