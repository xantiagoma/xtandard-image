package fetch

import (
	"strconv"
	"time"
)

// CacheResult classifies an origin-cache lookup.
type CacheResult uint8

// Cache results.
const (
	CacheMiss        CacheResult = iota // not cached (or stale): a flight was joined
	CacheHit                            // fresh original served from memory
	CacheNegativeHit                    // fresh not-found served from memory
	CacheRevalidated                    // stale original confirmed by a 304
	CacheStoreHit                       // original served from the shared store (WithStore)
	CacheSpoolHit                       // large original shared from its spool file (FetchLarge)
)

// String returns the metric label value.
func (r CacheResult) String() string {
	switch r {
	case CacheMiss:
		return "miss"
	case CacheHit:
		return "hit"
	case CacheNegativeHit:
		return "negative_hit"
	case CacheRevalidated:
		return "revalidated"
	case CacheStoreHit:
		return "store_hit"
	case CacheSpoolHit:
		return "spool_hit"
	default:
		return "CacheResult(" + strconv.Itoa(int(r)) + ")"
	}
}

// Metrics receives fetcher events. Implementations must be safe for
// concurrent use and cheap.
type Metrics interface {
	// ObserveFetch records one backend attempt: outcome is "ok",
	// "not_modified" or an errs.Kind name; bytes is the body size read.
	ObserveFetch(source, outcome string, d time.Duration, bytes int64)
	// OnCache records one lookup per Fetch call (plus CacheRevalidated
	// when a flight's conditional request returns 304, or CacheStoreHit
	// when a flight found the original in the shared store).
	OnCache(r CacheResult)
	// OnCoalesced records a Fetch that joined another caller's flight.
	OnCoalesced()
	// OnRetry records a retry of a transient failure.
	OnRetry(source string)
}

// NopMetrics discards every event. It is the default.
type NopMetrics struct{}

// ObserveFetch implements Metrics.
func (NopMetrics) ObserveFetch(string, string, time.Duration, int64) {}

// OnCache implements Metrics.
func (NopMetrics) OnCache(CacheResult) {}

// OnCoalesced implements Metrics.
func (NopMetrics) OnCoalesced() {}

// OnRetry implements Metrics.
func (NopMetrics) OnRetry(string) {}
