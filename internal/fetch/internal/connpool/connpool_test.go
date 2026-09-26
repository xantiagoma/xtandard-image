package connpool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

type fakeConn struct{ id int }

type recorder struct {
	mu     sync.Mutex
	dialed int
	closed []int
}

func (r *recorder) config(size int, idle time.Duration) Config[*fakeConn] {
	return Config[*fakeConn]{
		Size:        size,
		IdleTimeout: idle,
		Dial: func(ctx context.Context) (*fakeConn, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			r.dialed++
			return &fakeConn{id: r.dialed}, nil
		},
		Close: func(c *fakeConn) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.closed = append(r.closed, c.id)
			return nil
		},
	}
}

func (r *recorder) counts() (int, []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dialed, append([]int(nil), r.closed...)
}

func TestReuseIsLIFO(t *testing.T) {
	t.Parallel()
	var r recorder
	p := New(r.config(2, 0))
	a, err := p.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if a.Reused || b.Reused {
		t.Fatal("fresh connections reported as reused")
	}
	a.Release()
	b.Release()
	b.Release() // idempotent
	c, err := p.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Reused || c.Conn.id != b.Conn.id {
		t.Fatalf("got conn %d reused=%v, want most recently released %d", c.Conn.id, c.Reused, b.Conn.id)
	}
	c.Discard()
	if dialed, closed := r.counts(); dialed != 2 || !cmp.Equal(closed, []int{2}) {
		t.Fatalf("dialed %d closed %v, want 2 and [2]", dialed, closed)
	}
	if got := p.Idle(); got != 1 {
		t.Fatalf("Idle() = %d, want 1", got)
	}
}

func TestGetBlocksAtSize(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var r recorder
		p := New(r.config(1, 0))
		first, err := p.Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		// A waiter with a deadline gives up without dialling.
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := p.Get(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Get at capacity = %v, want deadline exceeded", err)
		}

		// A patient waiter gets the released connection.
		got := make(chan *Lease[*fakeConn])
		go func() {
			l, err := p.Get(t.Context())
			if err != nil {
				t.Error(err)
			}
			got <- l
		}()
		synctest.Wait()
		select {
		case <-got:
			t.Fatal("second Get returned while the pool was full")
		default:
		}
		first.Release()
		second := <-got
		if !second.Reused || second.Conn.id != first.Conn.id {
			t.Fatalf("waiter got conn %d reused=%v, want reused %d", second.Conn.id, second.Reused, first.Conn.id)
		}
		second.Release()
		if dialed, _ := r.counts(); dialed != 1 {
			t.Fatalf("dialed %d connections, want 1", dialed)
		}
	})
}

func TestIdleTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var r recorder
		p := New(r.config(2, time.Minute))
		l, err := p.Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		l.Release()
		synctest.Sleep(2 * time.Minute)
		l, err = p.Get(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if l.Reused {
			t.Fatal("expired idle connection was reused")
		}
		l.Release()
		if dialed, closed := r.counts(); dialed != 2 || !cmp.Equal(closed, []int{1}) {
			t.Fatalf("dialed %d closed %v, want 2 and [1]", dialed, closed)
		}
	})
}

func TestDialErrorFreesSlot(t *testing.T) {
	t.Parallel()
	var r recorder
	p := New(r.config(1, 0))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Get(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get with canceled ctx = %v", err)
	}
	l, err := p.Get(t.Context())
	if err != nil {
		t.Fatalf("slot leaked after dial error: %v", err)
	}
	l.Release()
}

func TestClose(t *testing.T) {
	t.Parallel()
	var r recorder
	p := New(r.config(2, 0))
	a, err := p.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a.Release()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	b.Release() // closed on release after Close
	if _, err := p.Get(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v, want ErrClosed", err)
	}
	if _, closed := r.counts(); !cmp.Equal(closed, []int{1, 2}) {
		t.Fatalf("closed %v, want [1 2]", closed)
	}
}
