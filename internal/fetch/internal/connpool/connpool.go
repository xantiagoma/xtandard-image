// Package connpool is the small bounded connection pool shared by the
// stateful fetch backends (ftpsrc, sftpsrc). A connection is leased to one
// fetch at a time; at most Size connections are open (leased plus idle), and
// idle ones older than IdleTimeout are closed instead of reused.
package connpool

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// ErrClosed is returned by Get after Close.
var ErrClosed = errors.New("connpool: pool closed")

// Config configures New.
type Config[C any] struct {
	// Size bounds the open connections (leased plus idle); values < 1 mean 1.
	Size int
	// IdleTimeout discards idle connections older than this; 0 keeps them
	// until the server drops them.
	IdleTimeout time.Duration
	// Dial opens a connection; it must honour ctx.
	Dial func(ctx context.Context) (C, error)
	// Close closes a connection. Only Pool.Close reports its errors; elsewhere
	// the connection is gone either way.
	Close func(C) error
}

type idleConn[C any] struct {
	c     C
	since time.Time
}

// Pool is a bounded LIFO pool. It is safe for concurrent use.
type Pool[C any] struct {
	cfg Config[C]
	sem *semaphore.Weighted

	mu     sync.Mutex
	idle   []idleConn[C] // most recently returned last
	closed bool
}

// New returns an empty pool; connections are dialled lazily.
func New[C any](cfg Config[C]) *Pool[C] {
	cfg.Size = max(cfg.Size, 1)
	return &Pool[C]{cfg: cfg, sem: semaphore.NewWeighted(int64(cfg.Size))}
}

// Lease is one connection checked out of the pool. Exactly one of Release
// or Discard must be called; later calls are no-ops.
type Lease[C any] struct {
	Conn C
	// Reused reports that Conn came from the idle list, so a transport error
	// on its first command may just mean the server dropped it while idle.
	Reused bool

	p    *Pool[C]
	once sync.Once
}

// Get leases a connection, waiting while Size are in use. It returns
// ctx.Err() when ctx ends first and ErrClosed after Close.
func (p *Pool[C]) Get(ctx context.Context) (*Lease[C], error) {
	if err := p.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	c, ok, err := p.popIdle()
	if err != nil {
		p.sem.Release(1)
		return nil, err
	}
	if ok {
		return &Lease[C]{Conn: c, Reused: true, p: p}, nil
	}
	c, err = p.cfg.Dial(ctx)
	if err != nil {
		p.sem.Release(1)
		return nil, err
	}
	return &Lease[C]{Conn: c, p: p}, nil
}

// popIdle returns the freshest idle connection, closing expired ones.
func (p *Pool[C]) popIdle() (C, bool, error) {
	var zero C
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return zero, false, ErrClosed
	}
	var expired []C
	if p.cfg.IdleTimeout > 0 {
		cutoff := time.Now().Add(-p.cfg.IdleTimeout)
		keep := p.idle[:0]
		for _, ic := range p.idle {
			if ic.since.Before(cutoff) {
				expired = append(expired, ic.c)
			} else {
				keep = append(keep, ic)
			}
		}
		clear(p.idle[len(keep):])
		p.idle = keep
	}
	c, ok := zero, false
	if n := len(p.idle); n > 0 {
		c, ok = p.idle[n-1].c, true
		p.idle[n-1] = idleConn[C]{}
		p.idle = p.idle[:n-1]
	}
	p.mu.Unlock()
	for _, e := range expired {
		_ = p.cfg.Close(e)
	}
	return c, ok, nil
}

// Release returns the connection to the idle list (or closes it when the
// pool is closed).
func (l *Lease[C]) Release() {
	l.once.Do(func() {
		p := l.p
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = p.cfg.Close(l.Conn)
		} else {
			p.idle = append(p.idle, idleConn[C]{c: l.Conn, since: time.Now()})
			p.mu.Unlock()
		}
		p.sem.Release(1)
	})
}

// Discard closes the connection instead of returning it.
func (l *Lease[C]) Discard() {
	l.once.Do(func() {
		_ = l.p.cfg.Close(l.Conn)
		l.p.sem.Release(1)
	})
}

// Idle returns the number of idle connections.
func (p *Pool[C]) Idle() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}

// Close closes the idle connections and makes Get fail with ErrClosed;
// leased connections are closed when released.
func (p *Pool[C]) Close() error {
	p.mu.Lock()
	idle := p.idle
	p.idle, p.closed = nil, true
	p.mu.Unlock()
	problems := make([]error, 0, len(idle))
	for _, ic := range idle {
		problems = append(problems, p.cfg.Close(ic.c))
	}
	return errors.Join(problems...)
}
