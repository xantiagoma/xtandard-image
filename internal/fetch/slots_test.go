package fetch

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// TestMaxConcurrent covers XI_FETCH_MAX_CONCURRENT: at most N downloads of
// different keys run at once, the next waits for a slot within its attempt
// timeout, and one that gets none in time is 503 without reaching the
// source.
func TestMaxConcurrent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		max  int
		// release the gate after this long; past the timeout the waiting
		// fetch gives up.
		hold     time.Duration
		wantKind errs.Kind // of the third fetch; 0 = success
	}{
		{"waits for a slot", 2, 3 * time.Second, 0},
		{"no slot within the timeout", 2, 20 * time.Second, errs.KindUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				gate := make(chan struct{})
				var running, peak atomic.Int64
				fn := func(_ context.Context, n int, _ source.Key, _ source.FetchOptions) (*source.Object, error) {
					cur := running.Add(1)
					defer running.Add(-1)
					for {
						p := peak.Load()
						if cur <= p || peak.CompareAndSwap(p, cur) {
							break
						}
					}
					if n <= tc.max { // the first ones hold their slot, past their timeout too
						<-gate
					}
					return object(payload(8), ""), nil
				}
				h := newHarness(t, Config{MaxConcurrent: tc.max, Timeout: 10 * time.Second}, fn)
				errsCh := make(chan error, tc.max)
				for i := range tc.max {
					go func() {
						r, err := h.f.Fetch(t.Context(), testKey("held"+strconv.Itoa(i)), source.FetchOptions{})
						if err == nil {
							r.Release()
						}
						errsCh <- err
					}()
				}
				synctest.Wait()
				if got := h.src.calls.Load(); got != int64(tc.max) {
					t.Fatalf("%d calls before the third fetch, want %d", got, tc.max)
				}
				third := make(chan error, 1)
				go func() {
					r, err := h.f.Fetch(t.Context(), testKey("third"), source.FetchOptions{})
					if err == nil {
						r.Release()
					}
					third <- err
				}()
				synctest.Wait()
				if got := h.src.calls.Load(); got != int64(tc.max) {
					t.Fatalf("third fetch reached the source while %d slots were held", tc.max)
				}
				synctest.Sleep(tc.hold)
				close(gate)
				err := <-third
				if tc.wantKind == 0 {
					if err != nil {
						t.Fatalf("third fetch: %v", err)
					}
				} else {
					wantKind(t, err, tc.wantKind)
				}
				for range tc.max {
					if err := <-errsCh; err != nil {
						t.Fatalf("held fetch: %v", err)
					}
				}
				if p := peak.Load(); p > int64(tc.max) {
					t.Errorf("peak concurrent downloads %d, want <= %d", p, tc.max)
				}
				if live := h.alloc.live(); live != 0 {
					t.Errorf("%d buffers leaked", live)
				}
			})
		})
	}
}

func TestMaxConcurrentNegative(t *testing.T) {
	t.Parallel()
	if _, err := New(&fakeSources{src: &fakeSource{}}, (&countingAlloc{}).alloc, Config{MaxConcurrent: -1}); err == nil {
		t.Fatal("New accepted a negative MaxConcurrent")
	}
}
