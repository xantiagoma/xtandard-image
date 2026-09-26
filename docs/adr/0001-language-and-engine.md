# ADR 0001: Go + libvips (vipsgen + thin cgo shim)

- Status: accepted
- Date: 2026-09-26

## Context

The product must match imgproxy Pro features, support the widest format list in the category (HEIC, AVIF, JXL, RAW, PDF, PSD, SVG, BMP, ICO, TIFF, video thumbnails), and stay fast and memory-lean. Research (`docs/research/stack.md`) compared Go + libvips, Rust + libvips and pure Rust. Pure Rust lacks HEIC, RAW, PDF, PSD and animated AVIF/WebP encoding and a demand-driven streaming pipeline. Rust libvips bindings (rs-vips 0.7) are weeks old with no production users. Go + libvips powers imgproxy and imagor at scale; vipsgen (MIT) generates type-safe bindings for libvips 8.16–8.18.

## Decision

- Go 1.27 (`go 1.27.0`, `toolchain go1.27.1`), `CGO_ENABLED=1`, libvips pinned to 8.18.x.
- Use `github.com/cshum/vipsgen` generated bindings for the long tail, imported **only** by `internal/vips`, which is also the only package that imports `"C"`.
- A small hand-written C shim inside `internal/vips` (imgproxy-style fixed-arity functions, one boundary crossing per step) for hot or missing paths: load-from-buffer with `fail_on`, LibRaw embedded-preview loader, save-to-buffer, progress/kill hook bound to request contexts.
- Parsers, planner, security, sources, fetcher and caches are pure Go and must build with `CGO_ENABLED=0`.
- libvips concurrency 1 with request-level semaphores (imgproxy/imagor pattern), operation cache disabled, jemalloc preloaded, `MALLOC_ARENA_MAX=2`, source bytes held in C memory owned by vips.

## Consequences

- Performance equals imgproxy/imagor class (libvips does the work); Go gives cheap concurrency and simple deployment.
- A C crash kills the process; mitigated by input limits, blocking untrusted loaders, fuzzing, and orchestrator restarts.
- Contributors need libvips only for executor/codec work; parsers, planner, security and sources can be developed on any machine.
- vipsgen is a single-maintainer project; we pin a version and keep the facade narrow so we can regenerate or fork it.
