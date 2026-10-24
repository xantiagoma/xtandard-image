package server

import (
	"context"
	"io"
	"net"
	"sync/atomic"

	"golang.org/x/sync/semaphore"
)

// LimitListener returns a listener that holds at most n accepted
// connections open at once (XI_SERVER_MAX_CONNECTIONS), like imgproxy's
// IMGPROXY_MAX_CLIENTS (netutil.LimitListener). When n are open, Accept
// waits for one to close: further clients wait in the kernel's accept
// backlog instead of being refused. Each slot is released exactly once,
// when its connection is closed (by net/http, the client going away, or
// a hijacker). Closing the listener, or ctx ending, unblocks a waiting
// Accept with net.ErrClosed, so http.Server.Shutdown still returns at
// once. n ≤ 0 returns ln unchanged.
//
// HTTP/1.1 keep-alive and HTTP/2 connections hold their slot while idle,
// until IdleTimeout closes them; an HTTP/2 connection multiplexes many
// requests in one slot.
func LimitListener(ctx context.Context, ln net.Listener, n int) net.Listener {
	if n <= 0 {
		return ln
	}
	ctx, cancel := context.WithCancel(ctx)
	sem := semaphore.NewWeighted(int64(n))
	return &limitListener{
		Listener: ln,
		sem:      sem,
		// The wait's context lives in this closure (containedctx: no
		// context in a struct); Close cancels it.
		acquire: func() error { return sem.Acquire(ctx, 1) },
		cancel:  cancel,
	}
}

type limitListener struct {
	net.Listener
	sem     *semaphore.Weighted
	acquire func() error
	cancel  context.CancelFunc
}

// Accept waits for a free slot, then for a connection.
func (l *limitListener) Accept() (net.Conn, error) {
	if err := l.acquire(); err != nil {
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		l.sem.Release(1)
		return nil, err
	}
	return &limitConn{Conn: c, sem: l.sem}, nil
}

// Close unblocks a waiting Accept and closes the listener.
func (l *limitListener) Close() error {
	l.cancel()
	return l.Listener.Close()
}

// limitConn releases its listener slot on the first Close.
type limitConn struct {
	net.Conn
	sem    *semaphore.Weighted
	closed atomic.Bool
}

// Close closes the connection and releases its slot once.
func (c *limitConn) Close() error {
	err := c.Conn.Close()
	if c.closed.CompareAndSwap(false, true) {
		c.sem.Release(1)
	}
	return err
}

// ReadFrom keeps net/http's sendfile/splice path (io.Copy into a
// *net.TCPConn) for bodies served from files, which embedding net.Conn
// would hide.
func (c *limitConn) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := c.Conn.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(struct{ io.Writer }{c.Conn}, r)
}

// CloseWrite half-closes the connection when it supports it: net/http
// uses it to finish a response cleanly before closing (no RST).
func (c *limitConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
