package limiter

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestTryAcquire: a job admitted only when a worker is free and nobody
// waits, never queued, never rejected as an overload.
func TestTryAcquire(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, rec := newLimiter(t, Config{Workers: 2, QueueSize: 2, QueueTimeout: 5 * time.Second})
		first, ok := l.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire on an idle limiter")
		}
		second, err := l.Acquire(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := l.TryAcquire(); ok {
			t.Fatal("TryAcquire with every worker busy")
		}
		waiter := make(chan error, 1)
		go func() {
			r, err := l.Acquire(t.Context(), 0)
			if err == nil {
				r()
			}
			waiter <- err
		}()
		synctest.Wait()
		second()
		if err := <-waiter; err != nil {
			t.Fatal(err)
		}
		third, ok := l.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire with a free worker")
		}
		if st := l.Stats(); st.InFlight != 2 || st.Waiting != 0 {
			t.Fatalf("Stats() = %+v", st)
		}
		first()
		first() // idempotent
		third()
		if r := rec.rejections(); len(r) != 0 {
			t.Fatalf("rejections %v", r)
		}
		assertIdle(t, l, rec)
	})
}
