package vips

/*
#cgo pkg-config: vips
#cgo noescape xi_init
#cgo nocallback xi_init
#cgo noescape xi_thumbnail
#cgo nocallback xi_thumbnail
#cgo noescape xi_jpegsave
#cgo nocallback xi_jpegsave
#include "shim.h"
*/
import "C"

import (
	"context"
	"runtime"
	"strings"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Init starts libvips and applies cfg: concurrency, operation cache limits
// and the loader allowlist. It must be called once before any other
// function and paired with Shutdown at process exit. Calling it again only
// re-applies the settings (vips_init itself runs once per process).
func Init(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	loaders := cfg.Loaders
	if loaders == nil {
		loaders = DefaultLoaders()
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer C.xi_thread_cleanup()

	argv0 := C.CString("xtandard-image")
	defer C.free(unsafe.Pointer(argv0))

	// The loader names are cold data: one C copy per Init.
	cnames := make([]*C.char, len(loaders))
	for i, l := range loaders {
		cnames[i] = C.CString(l)
	}
	defer func() {
		for _, p := range cnames {
			C.free(unsafe.Pointer(p))
		}
	}()
	var list **C.char
	if len(cnames) > 0 {
		list = &cnames[0]
	}

	var cerr *C.char
	rc := C.xi_init(argv0, C.int(cfg.Concurrency), C.int(cfg.CacheMax), C.size_t(cfg.CacheMaxMem),
		C.int(cfg.CacheMaxFiles), list, C.int(len(cnames)), &cerr)
	return opError(rc, "init", cerr)
}

// Shutdown stops libvips. No vips call may run afterwards; the process is
// expected to exit.
func Shutdown() { C.xi_shutdown() }

// Run executes fn on a locked OS thread and releases libvips' per-thread
// state (vips_error_clear + vips_thread_shutdown) on that thread before
// unlocking it. Every image job — load, transform, save and Close of all its
// images — must happen inside one Run. It returns early without calling fn
// when ctx is already done.
func Run(ctx context.Context, fn func() error) error {
	if err := ctxError(ctx, "vips: run"); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer C.xi_thread_cleanup()
	return fn()
}

// ReadMemStats returns libvips' tracked allocation counters. It is cheap and
// safe for concurrent use (metrics scrape it).
func ReadMemStats() MemStats {
	return MemStats{
		Mem:          int64(C.xi_tracked_mem()),
		MemHighwater: int64(C.xi_tracked_mem_highwater()),
		Allocs:       int64(C.xi_tracked_allocs()),
	}
}

// ctxError classifies a done context (client gone or deadline) and returns
// nil otherwise. cgo calls cannot be interrupted, so it runs between steps.
func ctxError(ctx context.Context, msg string) error {
	if ctx.Err() == nil {
		return nil
	}
	return errs.Wrapf(errs.KindOf(ctx.Err()), context.Cause(ctx), "%s", msg)
}

// opError turns a shim result code into a classified error and frees the
// copied error text. The kind depends only on rc (which operation failed).
func opError(rc C.int, op string, cerr *C.char) error {
	if rc == C.XI_OK {
		return nil
	}
	var msg string
	if cerr != nil {
		msg = strings.TrimSpace(C.GoString(cerr))
		C.xi_free(unsafe.Pointer(cerr))
	}
	kind := errs.KindInternal
	switch rc {
	case C.XI_ERR_UNSUPPORTED:
		kind = errs.KindUnsupported
	case C.XI_ERR_LOAD:
		kind = errs.KindCorrupt
	}
	return errs.Wrap(kind, &OpError{Op: op, Msg: msg})
}
