// Package vips is the only package that imports "C" (CLAUDE.md rule 3). It
// wraps libvips 8.18 behind a small Go-typed API; no C type leaves it.
//
// # Binding strategy
//
// The package is a hand-written C shim (shim.c/shim.h) plus Go wrappers, in
// the style of imgproxy's vips/vips.c: each Go call crosses into C once and
// runs a fixed-arity C function, so varargs never reach Go and the
// `#cgo noescape`/`#cgo nocallback` annotations apply. vipsgen (MIT,
// github.com/cshum/vipsgen) is deliberately not used yet:
//
//   - WP-00 needs four operations (init, thumbnail, jpegsave, stats); the
//     pre-generated vipsgen package is ~31k lines with ~300 operations.
//   - vipsgen's package imports "C" itself and carries process-global state
//     (Startup/sync.Once, a global log handler and its own cache and
//     concurrency defaults). Vendored as a sub-package it would break rule 3
//     and scripts/check-cgo.sh; used as a module it would own vips init.
//   - imgproxy, the most battle-tested Go/libvips server, is entirely
//     hand-written for exactly these reasons; imagor uses vipsgen for breadth.
//
// WP-06 pins vipsgen v1.3.11 for the long tail of operations and keeps this
// shim for the hot path (go-standards §4).
//
// # Memory and threads
//
//   - Source bytes live in a [Buffer]: vips_tracked_malloc'd C memory wrapped
//     in a refcounted VipsBlob. Go never hands its own memory to libvips.
//     Images created from a Buffer take their own blob reference, so a
//     Buffer may be released as soon as loading returns.
//   - Encoded output is a [Blob] of g_malloc'd memory, exposed zero-copy by
//     [Blob.Bytes] and freed by [Blob.Release].
//   - Every image job runs inside [Run], which locks the OS thread and calls
//     vips_error_clear + vips_thread_shutdown on it before unlocking.
//   - libvips concurrency is 1 and the operation cache is off by default;
//     parallelism comes from the request semaphore (internal/limiter).
//   - Loaders are an allowlist: vips_block_untrusted_set(TRUE), every
//     VipsForeignLoad blocked, then [DefaultLoaders] re-enabled.
//   - Errors are copied from the global vips error buffer at the failure site
//     in C; the errs kind comes from which operation failed (no loader →
//     Unsupported, load → Corrupt, save → Internal), never from the text.
//
// Without cgo (CGO_ENABLED=0) the package builds with stubs that return
// [ErrUnavailable], so pure-Go builds of the whole module keep working.
package vips
