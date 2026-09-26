// Package leakcheck compares libvips' process-wide counters (tracked memory,
// allocations, open files, live objects) before and after a leak test's
// cycles, without flaking on libvips' asynchronous per-thread cleanup.
//
// Why the counters wobble: libvips runs sinks on threads from its global
// threadset. A worker's regions release their pixel buffers onto that
// thread's reserve list (buffer.c, up to two per image), the worker signals
// the pool that it is done, and only then does the threadset call
// vips_thread_shutdown, which frees the reserve (threadset.c
// vips_threadset_work). The calling thread's sink returns as soon as the
// signal arrives, so right after a job the counters may still include the
// last job's reserve buffers: a fixed amount per job shape (measured at
// 81 allocations, 1.6 MB for the heaviest internal/vips TestNoLeak cycle),
// present or not depending on whether the worker was descheduled in that
// gap. On a loaded machine that decides the reading, which is how an
// absolute 1 MiB / 64-allocation slack flaked.
//
// Check therefore waits for the counters to settle: it re-reads them until
// they are within the limits of the baseline. The transient drains as soon
// as the worker threads run (microseconds); a real leak (one allocation,
// object or source buffer per cycle, far above the limits after hundreds
// of cycles) never drains, and Check fails once the settle deadline has
// passed. The wait polls C state that synctest cannot observe (the
// threads are libvips', not goroutines), so it is bounded by a generous
// wall-clock deadline instead of a sleep: a passing run never waits for it.
package leakcheck

import (
	"runtime"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// SettleTimeout bounds how long Check waits for the counters to return
// within limits before it reports a leak. It stays below libvips' 15 s
// idle-thread lifetime (threadset.c max_idle_time), so memory that would
// only be freed when an idle worker thread exits still fails the check.
const SettleTimeout = 5 * time.Second

// Limits are the tolerated growth over the baseline once settled. Files
// must always match exactly.
type Limits struct {
	Bytes   int64
	Allocs  int64 // < 0 skips the allocation count
	Objects int
}

// Default is the tolerance of the libvips leak tests: far below one leaked
// allocation, object or source buffer per cycle over hundreds of cycles.
var Default = Limits{Bytes: 1 << 20, Allocs: 64, Objects: 16}

// Snapshot is a reading of libvips' counters.
type Snapshot struct {
	vips.MemStats
	Objects int
}

// Take reads the counters now.
func Take() Snapshot {
	return Snapshot{MemStats: vips.ReadMemStats(), Objects: vips.LiveObjects()}
}

// within reports whether s is within lim of base.
func (s Snapshot) within(base Snapshot, lim Limits) bool {
	return s.Mem-base.Mem <= lim.Bytes &&
		(lim.Allocs < 0 || s.Allocs-base.Allocs <= lim.Allocs) &&
		s.Files == base.Files
}

// Check waits until the counters are within lim of before and returns the
// settled reading; after SettleTimeout it reports the excess through
// tb.Errorf (prefixed with what) and returns the last reading.
func Check(tb testing.TB, before Snapshot, lim Limits, what string) Snapshot {
	tb.Helper()
	deadline := time.Now().Add(SettleTimeout)
	for {
		// Memory first: LiveObjects walks libvips' object table under its
		// lock, which the draining workers also need.
		if s := (Snapshot{MemStats: vips.ReadMemStats()}); s.within(before, lim) {
			s.Objects = vips.LiveObjects()
			if s.Objects-before.Objects <= lim.Objects {
				return s
			}
		}
		if time.Now().After(deadline) {
			s := Take()
			tb.Errorf("%s: not settled after %v: mem %d → %d B, allocs %d → %d, files %d → %d, "+
				"objects %d → %d (tolerance %d B / %d allocs / %d objects)",
				what, SettleTimeout, before.Mem, s.Mem, before.Allocs, s.Allocs, before.Files, s.Files,
				before.Objects, s.Objects, lim.Bytes, lim.Allocs, lim.Objects)
			return s
		}
		runtime.Gosched()
	}
}
