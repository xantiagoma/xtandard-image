package vips

/*
#cgo pkg-config: vips
#cgo noescape xi_init
#cgo nocallback xi_init
#cgo noescape xi_probe
#cgo nocallback xi_probe
#cgo noescape xi_load
#cgo nocallback xi_load
#cgo noescape xi_thumbnail
#cgo nocallback xi_thumbnail
#cgo noescape xi_black
#cgo nocallback xi_black
#cgo noescape xi_new_from_memory
#cgo nocallback xi_new_from_memory
#cgo noescape xi_meta_get_int
#cgo nocallback xi_meta_get_int
#cgo noescape xi_meta_get_blob
#cgo nocallback xi_meta_get_blob
#cgo noescape xi_meta_get_array_int
#cgo nocallback xi_meta_get_array_int
#cgo noescape xi_meta_set_int
#cgo nocallback xi_meta_set_int
#cgo noescape xi_meta_set_blob
#cgo nocallback xi_meta_set_blob
#cgo noescape xi_meta_set_array_int
#cgo nocallback xi_meta_set_array_int
#cgo noescape xi_meta_remove
#cgo nocallback xi_meta_remove
#cgo noescape xi_meta_get_string
#cgo nocallback xi_meta_get_string
#cgo noescape xi_meta_fields
#cgo nocallback xi_meta_fields
#cgo noescape xi_copy_memory
#cgo nocallback xi_copy_memory
#cgo noescape xi_share
#cgo nocallback xi_share
#cgo noescape xi_write_to_memory
#cgo nocallback xi_write_to_memory
#cgo noescape xi_resize
#cgo nocallback xi_resize
#cgo noescape xi_extract_area
#cgo nocallback xi_extract_area
#cgo noescape xi_crop
#cgo nocallback xi_crop
#cgo noescape xi_embed
#cgo nocallback xi_embed
#cgo noescape xi_gravity
#cgo nocallback xi_gravity
#cgo noescape xi_flip
#cgo nocallback xi_flip
#cgo noescape xi_rot
#cgo nocallback xi_rot
#cgo noescape xi_autorot
#cgo nocallback xi_autorot
#cgo noescape xi_smartcrop
#cgo nocallback xi_smartcrop
#cgo noescape xi_flatten
#cgo nocallback xi_flatten
#cgo noescape xi_premultiply
#cgo nocallback xi_premultiply
#cgo noescape xi_unpremultiply
#cgo nocallback xi_unpremultiply
#cgo noescape xi_colourspace
#cgo nocallback xi_colourspace
#cgo noescape xi_icc_import
#cgo nocallback xi_icc_import
#cgo noescape xi_icc_export
#cgo nocallback xi_icc_export
#cgo noescape xi_icc_transform
#cgo nocallback xi_icc_transform
#cgo noescape xi_profile_load
#cgo nocallback xi_profile_load
#cgo noescape xi_cast
#cgo nocallback xi_cast
#cgo noescape xi_copy
#cgo nocallback xi_copy
#cgo noescape xi_gaussblur
#cgo nocallback xi_gaussblur
#cgo noescape xi_sharpen
#cgo nocallback xi_sharpen
#cgo noescape xi_shrink
#cgo nocallback xi_shrink
#cgo noescape xi_composite2
#cgo nocallback xi_composite2
#cgo noescape xi_replicate
#cgo nocallback xi_replicate
#cgo noescape xi_join
#cgo nocallback xi_join
#cgo noescape xi_arrayjoin
#cgo nocallback xi_arrayjoin
#cgo noescape xi_linear
#cgo nocallback xi_linear
#cgo noescape xi_invert
#cgo nocallback xi_invert
#cgo noescape xi_bandjoin
#cgo nocallback xi_bandjoin
#cgo noescape xi_bandjoin_const
#cgo nocallback xi_bandjoin_const
#cgo noescape xi_extract_band
#cgo nocallback xi_extract_band
#cgo noescape xi_addalpha
#cgo nocallback xi_addalpha
#cgo noescape xi_find_trim
#cgo nocallback xi_find_trim
#cgo noescape xi_similarity
#cgo nocallback xi_similarity
#cgo noescape xi_affine
#cgo nocallback xi_affine
#cgo noescape xi_jpegsave
#cgo nocallback xi_jpegsave
#cgo noescape xi_pngsave
#cgo nocallback xi_pngsave
#cgo noescape xi_webpsave
#cgo nocallback xi_webpsave
#cgo noescape xi_gifsave
#cgo nocallback xi_gifsave
#cgo noescape xi_heifsave
#cgo nocallback xi_heifsave
#cgo noescape xi_jxlsave
#cgo nocallback xi_jxlsave
#cgo noescape xi_tiffsave
#cgo nocallback xi_tiffsave
#include "shim.h"
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Init starts libvips (once per process) and applies cfg: concurrency,
// operation cache limits, the loader allowlist and the HEIC encode switch.
//
// libvips is process-global, so Init takes a reference that one Shutdown
// releases: several owners (an app.Run per test, a TestMain) may nest their
// Init/Shutdown pairs, concurrently too. The first Init applies cfg; a
// nested Init must ask for the same settings (re-applying the allowlists
// would block every loader for a moment under running jobs) or it fails.
// Once the last reference has been released, libvips is gone for good and
// Init fails.
func Init(cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	loaders, savers := cfg.Loaders, cfg.Savers
	if loaders == nil {
		loaders = DefaultLoaders()
	}
	if savers == nil {
		savers = DefaultSavers()
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer C.xi_thread_cleanup()

	argv0 := C.CString("xtandard-image")
	defer C.free(unsafe.Pointer(argv0))

	cloaders, freeLoaders := cstrings(loaders)
	defer freeLoaders()
	csavers, freeSavers := cstrings(savers)
	defer freeSavers()

	settings := C.CString(fmt.Sprintf("concurrency=%d cache=%d/%d/%d heic_encode=%t loaders=%s savers=%s",
		cfg.Concurrency, cfg.CacheMax, cfg.CacheMaxMem, cfg.CacheMaxFiles, cfg.HEICEncode,
		strings.Join(loaders, ","), strings.Join(savers, ",")))
	defer C.free(unsafe.Pointer(settings))

	var cerr *C.char
	rc := C.xi_init(argv0, C.int(cfg.Concurrency), C.int(cfg.CacheMax), C.size_t(cfg.CacheMaxMem),
		C.int(cfg.CacheMaxFiles), first(cloaders), C.int(len(cloaders)), first(csavers),
		C.int(len(csavers)), cbool(cfg.HEICEncode), settings, &cerr)
	return opError(rc, "init", cerr, nil)
}

// cstrings copies names to C memory (cold data: once per Init). The array
// itself is Go memory holding C pointers only, which cgo may pass.
func cstrings(names []string) ([]*C.char, func()) {
	out := make([]*C.char, len(names))
	for i, n := range names {
		out[i] = C.CString(n)
	}
	return out, func() {
		for _, p := range out {
			C.free(unsafe.Pointer(p))
		}
	}
}

func first(p []*C.char) **C.char {
	if len(p) == 0 {
		return nil
	}
	return &p[0]
}

// Shutdown releases the reference taken by one Init. Releasing the last
// one refuses new Runs, waits for the Runs in progress to return, then calls
// vips_shutdown, after which no vips call may run and Init fails: the
// process is expected to exit. With SetLeakCheck(true), libvips reports
// leaked objects and memory on stderr there. An unbalanced Shutdown does
// nothing. Never call it inside Run (it would wait for itself).
func Shutdown() { C.xi_shutdown() }

// SetLeakCheck turns libvips' leak checking (vips_leak_set) on or off:
// object and memory leaks are reported at Shutdown. For tests.
func SetLeakCheck(on bool) { C.xi_leak_set(cbool(on)) }

// Run executes fn on a locked OS thread and releases libvips' per-thread
// state (vips_error_clear + vips_thread_shutdown) on that thread before
// unlocking it. Every image job — load, transform, save and Close of all its
// images — must happen inside one Run: Image values are not goroutine-safe
// and must not cross Runs. It returns early without calling fn when ctx is
// already done, and fails with a KindInternal error when libvips is not
// initialised (no Init, or the last Shutdown has started): the final
// Shutdown waits for every Run that did start.
func Run(ctx context.Context, fn func() error) error {
	if err := ctxError(ctx, "vips: run"); err != nil {
		return err
	}
	if C.xi_run_begin() != C.XI_OK {
		return errs.New(errs.KindInternal, "vips: run: libvips is not initialised or already shut down")
	}
	defer C.xi_run_end()
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
		Files:        int64(C.xi_tracked_files()),
	}
}

// LiveObjects counts the live VipsObjects (images, operations, regions,
// ...). It walks libvips' object table under its lock: for leak tests and
// debugging, not for per-request metrics.
func LiveObjects() int { return int(C.xi_live_objects()) }

// KillOnCancel arranges for img's libvips kill flag to be set when ctx is
// done. cgo calls cannot be interrupted, but libvips polls the flag for
// every region it computes, so a running sink (Save, WriteToMemory) or a
// computing operation (FindTrim, SmartCrop) over img fails at its next tile.
//
// Call stop before img is closed: it disarms the watcher, waits for a kill
// that is in progress and clears the flag. Save and the other sinks arm
// this themselves; use it for work built from several calls.
func KillOnCancel(ctx context.Context, img *Image) (stop func()) {
	if ctx.Done() == nil || img == nil || img.p == nil {
		return func() {}
	}
	p := img.p
	var mu sync.Mutex
	armed, killed := true, false
	stopWatch := context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		if armed {
			C.xi_image_set_kill(p, 1)
			killed = true
		}
	})
	return func() {
		stopWatch()
		mu.Lock()
		defer mu.Unlock()
		armed = false
		if killed {
			C.xi_image_set_kill(p, 0)
		}
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
// copied error text. The kind depends only on which operation failed, never
// on the text:
//
//   - no enabled loader → Unsupported; a loader failure → Corrupt;
//   - a header over a configured limit → LimitExceeded;
//   - a saver or processing operation → Corrupt when the source's decoder
//     reported a failure while the pipeline ran (src is marked by the shim),
//     Internal otherwise.
func opError(rc C.int, op string, cerr *C.char, src *C.VipsBlob) error {
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
	case C.XI_ERR_LIMIT:
		kind = errs.KindLimitExceeded
	case C.XI_ERR_SAVE, C.XI_ERR_OP:
		if C.xi_blob_failed(src) != 0 {
			kind = errs.KindCorrupt
		}
	}
	return errs.Wrap(kind, &OpError{Op: op, Msg: msg})
}

// stepError is opError for a step that ran under ctx: when ctx ended while
// the call ran (a kill or plain bad timing), the context's error wins.
func stepError(ctx context.Context, rc C.int, op string, cerr *C.char, src *C.VipsBlob) error {
	err := opError(rc, op, cerr, src)
	if err != nil && ctx.Err() != nil {
		return errs.Wrapf(errs.KindOf(ctx.Err()), context.Cause(ctx), "vips: %s: %v", op, err)
	}
	return err
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// cstr returns s as NUL-terminated bytes in Go memory. C may read it during
// one call (libvips copies every string it keeps); nil for "".
func cstr(s string) []byte {
	if s == "" {
		return nil
	}
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// cptr is the C view of cstr's result, NULL for nil.
func cptr(b []byte) *C.char {
	if len(b) == 0 {
		return nil
	}
	return (*C.char)(unsafe.Pointer(&b[0]))
}

// cdoubles is the C view of a float64 slice, NULL when empty.
func cdoubles(v []float64) *C.double {
	if len(v) == 0 {
		return nil
	}
	return (*C.double)(unsafe.Pointer(&v[0]))
}
