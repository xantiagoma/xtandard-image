package cache

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Tiered composes stores fastest first (memory → disk/redis → s3,
// XI_CACHE_RESULT_STORES order):
//
//   - Get asks each tier in order and returns the first hit. A hit in a
//     slower tier is copied into every faster tier (read-fill) with the
//     entry's remaining lifetime, so the next lookup stops at the front.
//     A failing tier counts as a miss; its error is returned only when no
//     tier hits.
//   - Set writes through to every tier (callers wrap it in an AsyncStore
//     to keep writes off the request path).
//   - Delete and Close apply to every tier, joining errors.
type Tiered struct {
	tiers []Store
}

// NewTiered returns the composition of tiers, fastest first. It needs at
// least one tier; with exactly one, callers may use that store directly.
func NewTiered(tiers ...Store) (*Tiered, error) {
	if len(tiers) == 0 {
		return nil, errors.New("cache: tiered store needs at least one tier")
	}
	for i, t := range tiers {
		if t == nil {
			return nil, fmt.Errorf("cache: tier %d is nil", i)
		}
	}
	return &Tiered{tiers: tiers}, nil
}

// Get implements Store.
func (t *Tiered) Get(ctx context.Context, ns Namespace, key string) (Entry, bool, error) {
	var errs []error
	for i, tier := range t.tiers {
		e, ok, err := tier.Get(ctx, ns, key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		if i > 0 {
			t.fill(ctx, ns, key, &e, i)
		}
		return e, true, nil
	}
	return Entry{}, false, errors.Join(errs...)
}

// fill copies a hit from tier i into the faster tiers. Failures are
// ignored: the entry is still served, and the next lookup retries.
func (t *Tiered) fill(ctx context.Context, ns Namespace, key string, e *Entry, i int) {
	ttl := time.Duration(0)
	if !e.ExpiresAt.IsZero() {
		if ttl = time.Until(e.ExpiresAt); ttl <= 0 {
			return
		}
	}
	for _, faster := range t.tiers[:i] {
		_ = faster.Set(ctx, ns, key, e, ttl) // best effort: a failed fill is a later miss
	}
}

// Set implements Store.
func (t *Tiered) Set(ctx context.Context, ns Namespace, key string, e *Entry, ttl time.Duration) error {
	var errs []error
	for _, tier := range t.tiers {
		if err := tier.Set(ctx, ns, key, e, ttl); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Delete implements Store.
func (t *Tiered) Delete(ctx context.Context, ns Namespace, key string) error {
	var errs []error
	for _, tier := range t.tiers {
		if err := tier.Delete(ctx, ns, key); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Close implements Store.
func (t *Tiered) Close() error {
	var errs []error
	for _, tier := range t.tiers {
		if err := tier.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
