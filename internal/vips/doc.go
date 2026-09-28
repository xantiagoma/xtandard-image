// Package vips is the only package that imports "C" (CLAUDE.md rule 3). It
// wraps libvips 8.18 behind a small Go-typed API; no C type leaves it.
//
// # Binding strategy
//
// The package is a hand-written C shim (shim.c, load.c, image.c, ops.c,
// save.c behind shim.h) plus Go wrappers, in the style of imgproxy's
// vips/vips.c: each Go call crosses into C once and runs one fixed-arity C
// function per operation, so varargs never reach Go, options travel as
// plain C structs and the `#cgo noescape`/`#cgo nocallback` annotations
// apply. vipsgen (MIT, github.com/cshum/vipsgen) is not used, a deliberate
// deviation from the WP-06 roadmap row (ARCHITECTURE §8.4):
//
//   - vipsgen's package imports "C" itself and carries process-global state
//     (Startup/sync.Once, a global log handler and its own cache and
//     concurrency defaults). Vendored as a sub-package it would break rule 3
//     and scripts/check-cgo.sh; used as a module it would own vips init.
//   - The M1 surface (loaders, ~40 operations, 7 savers) is small enough to
//     write by hand, and every function needs the same error capture and
//     source attribution that generated wrappers do not do.
//   - imgproxy, the most battle-tested Go/libvips server, is entirely
//     hand-written for exactly these reasons; imagor uses vipsgen for breadth.
//
// vipsgen and imagor remain the reference for operation coverage and option
// names when the surface grows.
//
// # API
//
//   - Lifecycle: [Init] (reference-counted; cache off, concurrency 1, loader
//     and saver allowlists, HEIC encode switch), [Shutdown], [Run] (locked OS thread,
//     vips_thread_shutdown), [ReadMemStats], [LiveObjects], [SetLeakCheck],
//     [KillOnCancel].
//   - Bytes: [Buffer] (source bytes, implements fetch.Buffer; [NewBuffer] is
//     the fetch.Allocator) and [Blob] (encoded output, same interface).
//   - Decode: [Probe] and [ProbeDensity] (header only), [Load], [Thumbnail], [Black],
//     [NewFromMemory]; camera RAW through LibRaw (raw.c, pkg-config
//     libraw_r): [ProbeRaw] (header only) and [RawLoad] (demosaic).
//   - Process: methods on [Image] that return new lazy images; [Arena]
//     closes all of a request's images in one place.
//   - Encode: [Image.Save] with [SaveOptions].
//
// # Memory and threads
//
//   - Source bytes live in a [Buffer]: vips_tracked_malloc'd C memory wrapped
//     in a refcounted VipsBlob. Go never hands its own memory to libvips
//     beyond one call (options, metadata and profile names are copied).
//     Images created from a Buffer take their own blob reference, so a
//     Buffer may be released as soon as loading returns.
//   - Encoded output is a [Blob] of g_malloc'd memory, exposed zero-copy by
//     [Blob.Bytes] and freed by the last [Blob.Release].
//   - Every image job runs inside [Run], which locks the OS thread and calls
//     vips_error_clear + vips_thread_shutdown on it before unlocking. [Image]
//     values are not goroutine-safe and never cross Runs.
//
// # Lifecycle invariant
//
// libvips is process-global and vips_shutdown is final: it frees the
// operation cache table (created by a GOnce, so every later operation hits
// g_hash_table_lookup(NULL)), stops the thread pool, and a later vips_init
// does nothing. [Init] and [Shutdown] are therefore reference-counted in the
// C shim, next to that state:
//
//   - each Init takes a reference (Inits are serialised) and each Shutdown
//     releases one; nested and concurrent pairs, such as one app.Run per
//     parallel test, never tear libvips down under each other;
//   - only the first Init applies its settings; a nested Init must ask for
//     the same ones or it fails (re-applying the loader allowlist blocks
//     every loader for a moment, failing concurrent loads);
//   - [Run] registers itself for its whole duration and fails when no
//     reference is held;
//   - the Shutdown that releases the last reference refuses new Runs, waits
//     for the running ones, and only then calls vips_shutdown, once. After
//     that Init and Run fail: libvips cannot be restarted in the process.
//
// Test packages that start the service more than once (or with -count=N)
// hold a reference in TestMain so the last one is only released at exit.
// Every vips call that touches images or operations must happen inside
// Run; only Buffer/Blob reference counting and the statistics readers may
// run outside it. Never call Shutdown inside Run.
//   - libvips concurrency is 1 and the operation cache is off by default;
//     parallelism comes from the request semaphore (internal/limiter).
//   - Loaders and savers are allowlists: vips_block_untrusted_set(TRUE),
//     every VipsForeignLoad/VipsForeignSave blocked, then [DefaultLoaders]
//     and [DefaultSavers] re-enabled. ImageMagick (UNTRUSTED, PSD only) is
//     re-enabled by name and additionally refused for any buffer that does
//     not start with the PSD signature; the base image's policy.xml is the
//     third line of defence.
//   - Every call checks ctx first; sinks and computing operations (Save,
//     WriteToMemory, SmartCrop, FindTrim) also arm [KillOnCancel], so a
//     cancelled request stops at the next tile.
//
// # Errors
//
// libvips' error buffer is process-global and shared by every thread, so its
// text is never used to classify anything. The shim writes its own failures
// straight into a per-call message (xi_error_new) and copies libvips' text,
// clearing it, at the failure site (xi_error_copy); that text is kept in
// [OpError] for logs only. The errs kind comes from which operation failed:
// no enabled loader → Unsupported; a loader failure → Corrupt; a header over
// a configured limit → LimitExceeded; a saver or operation failure →
// Internal, unless the source's decoder signalled "invalidate" while the
// pipeline ran (lazy decode of a truncated file), which the shim records per
// source buffer → Corrupt. A loader failure on a PDF whose trailer carries
// /Encrypt is a password-protected document: Unsupported with a public
// message (the trailer is parsed in Go; PDFium's text is not matched),
// "wrong document password" when the Buffer carries one
// ([Buffer.WithPassword], WP-115: the password reaches PDFium through the
// loader's "password" property and libvips' copy is wiped once the loader
// is built). A context that ended wins over all of them (Canceled/Timeout).
//
// Without cgo (CGO_ENABLED=0) the package builds with stubs that return
// [ErrUnavailable], so pure-Go builds of the whole module keep working.
package vips
