package fetch

import (
	"sync"

	"github.com/xantiagoma/xtandard-image/internal/source"
)

// call is one in-flight shared fetch. Its fields other than done and
// waiters are written by the leader goroutine before done is closed and are
// read-only afterwards.
type call struct {
	done chan struct{}

	buf       Buffer // the call's own reference; nil when err != nil
	meta      source.Object
	err       error
	fromCache bool

	// Guarded by flightGroup.mu.
	waiters  int  // joined callers that have not yet taken or abandoned the result
	finished bool // done is closed and the call left the map
}

// flightGroup coalesces concurrent fetches of one canonical key, like
// x/sync/singleflight, but accounts for the refcounted result: the call
// holds one Buffer reference until it has finished and every joined caller
// has either retained the buffer or given up. singleflight cannot do that
// because it does not know how many callers abandon their result channel.
type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*call
}

// join registers the caller on the call for key, creating it when none is
// in flight. leader reports that the caller must start the fetch.
func (g *flightGroup) join(key string) (c *call, leader bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.calls[key]; ok {
		c.waiters++
		return c, false
	}
	if g.calls == nil {
		g.calls = make(map[string]*call)
	}
	c = &call{done: make(chan struct{}), waiters: 1}
	g.calls[key] = c
	return c, true
}

// finish publishes c's result: it removes c from the map so later callers
// start a new fetch (or hit the cache), wakes every waiter, and drops the
// call's reference if nobody is left to take it.
func (g *flightGroup) finish(key string, c *call) {
	g.mu.Lock()
	delete(g.calls, key)
	c.finished = true
	close(c.done)
	drop := c.waiters == 0
	g.mu.Unlock()
	if drop && c.buf != nil {
		c.buf.Release()
	}
}

// leave unregisters one waiter, either after it retained c.buf or because
// its context ended first. The last one out after finish drops the call's
// reference.
func (g *flightGroup) leave(c *call) {
	g.mu.Lock()
	c.waiters--
	drop := c.finished && c.waiters == 0
	g.mu.Unlock()
	if drop && c.buf != nil {
		c.buf.Release()
	}
}
