package media

import (
	"context"
	"errors"
	"sync"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// group coalesces identical transcodes, like x/sync/singleflight, with two
// differences a transcode needs: the work runs on a context of its own
// (the first caller's values, not its cancellation), cancelled only when
// every caller has gone, so one client disconnecting does not fail the
// others; and the result is reference counted, each caller getting its own
// reference.
type group struct {
	mu sync.Mutex
	m  map[string]*flight
}

// flight is one transcode in progress. Its fields are guarded by the
// group's mutex; res and err are set before done is closed.
type flight struct {
	done    chan struct{}
	res     *Result
	err     error
	waiters int
	cancel  context.CancelCauseFunc
}

// errAbandoned is the cause of a transcode every caller left.
var errAbandoned = errors.New("media: every request for the transcode is gone")

// do runs fn once per key at a time. joined reports that the call waited
// for a transcode another call started. The caller owns one reference to
// the result's body.
func (g *group) do(ctx context.Context, key string, fn func(context.Context) (*Result, error)) (res *Result, joined bool, err error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*flight)
	}
	f, joined := g.m[key]
	if !joined {
		work, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
		f = &flight{done: make(chan struct{}), cancel: cancel}
		g.m[key] = f
		go g.run(work, key, f, fn)
	}
	f.waiters++
	g.mu.Unlock()

	select {
	case <-f.done:
	case <-ctx.Done():
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	f.waiters--
	select {
	case <-f.done:
		if f.err != nil {
			return nil, joined, f.err
		}
		r := *f.res
		r.Body.Retain()
		if f.waiters == 0 {
			f.res.Body.Release() // the flight's own reference
		}
		return &r, joined, nil
	default:
		if f.waiters == 0 {
			f.cancel(errAbandoned)
		}
		return nil, joined, errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
	}
}

// run executes fn for f and publishes its result. A flight no caller
// waits for any more releases its result itself.
func (g *group) run(ctx context.Context, key string, f *flight, fn func(context.Context) (*Result, error)) {
	defer f.cancel(nil)
	res, err := call(ctx, fn)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m[key] == f {
		delete(g.m, key)
	}
	f.res, f.err = res, err
	close(f.done)
	if f.waiters == 0 && res != nil {
		res.Body.Release()
	}
}

// call runs fn, turning a panic into an error: the transcode runs on a
// goroutine of its own, outside the server's recovery.
func call(ctx context.Context, fn func(context.Context) (*Result, error)) (res *Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, err = nil, errs.Newf(errs.KindInternal, "media: transcode panicked: %v", p)
		}
	}()
	res, err = fn(ctx)
	if err == nil && (res == nil || res.Body == nil) {
		return nil, errs.New(errs.KindInternal, "media: transcode returned no body")
	}
	return res, err
}
