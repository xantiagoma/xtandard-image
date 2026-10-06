package media

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

func streamOp() *transform.VideoOp {
	return &transform.VideoOp{Mute: true, Stream: &transform.VideoStream{SegmentDuration: time.Second}}
}

// TestStreamPlans: Layout lays the stream out from a probe; Stream plans
// and runs one segment of a rendition, coalesced like Transform.
func TestStreamPlans(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{gate: make(chan struct{})}
		s, m := newService(t, e, nil)
		in, err := s.Probe(t.Context(), newBuf("video"))
		if err != nil {
			t.Fatal(err)
		}
		l, err := s.Layout(&in, streamOp())
		if err != nil {
			t.Fatal(err)
		}
		if l.Segments != 2 || len(l.Video) != 4 || l.Video[0].Height != 720 || l.Audio != nil {
			t.Fatalf("layout %+v", l)
		}
		src := newBuf("video")
		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() {
				res, err := s.Stream(t.Context(), "v1/1", src, streamOp(), StreamPart{Track: "v1", Segment: 1})
				if err != nil {
					t.Error(err)
					return
				}
				if res.Width != 854 || res.Height != 480 || res.Output.Audio != video.AudioNone {
					t.Errorf("result %+v", res)
				}
				res.Body.Release()
			})
		}
		synctest.Wait()
		close(e.gate)
		wg.Wait()
		synctest.Wait()
		if len(e.plans) != 1 || m.coalesced != 2 {
			t.Fatalf("%d transcodes, %d coalesced", len(e.plans), m.coalesced)
		}
		p := e.plans[0]
		if p.Fragment == nil || p.Video == nil || p.Video.Width != 854 || p.Start != 1 || p.End != 2 {
			t.Fatalf("plan %+v", p)
		}
		if r := src.refs.Load(); r != 1 {
			t.Errorf("source holds %d references", r)
		}
	})
}

func TestStreamNotFound(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, &engine{}, nil)
	for _, part := range []StreamPart{{Track: "v9"}, {Track: "v0", Segment: 2}, {}} {
		if _, err := s.Stream(t.Context(), part.Track, newBuf("v"), streamOp(), part); errs.KindOf(err) != errs.KindNotFound {
			t.Errorf("%+v: %v", part, err)
		}
	}
}

// TestPrefetch: a prefetch runs only when a run slot is idle, never for a
// part already in flight, never queues, hands its result to done, and
// Close stops and waits for it.
func TestPrefetch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{gate: make(chan struct{})}
		s, _ := newService(t, e, func(c *Config) { c.Workers, c.MaxJobs = 2, 2 })
		src := newBuf("video")
		got := make(chan *Result, 4)
		done := func(r *Result) { got <- r }

		// A request on one worker: the other one is idle.
		var wg sync.WaitGroup
		wg.Go(func() {
			if res, err := s.Stream(t.Context(), "v0/0", src, streamOp(), StreamPart{Track: "v0"}); err == nil {
				res.Body.Release()
			}
		})
		synctest.Wait()
		if s.Prefetch("v0/0", src, streamOp(), StreamPart{Track: "v0"}, done) {
			t.Fatal("prefetched a part in flight")
		}
		if !s.Prefetch("v0/1", src, streamOp(), StreamPart{Track: "v0", Segment: 1}, done) {
			t.Fatal("no prefetch on an idle worker")
		}
		synctest.Wait()
		// Both workers busy: no more prefetches, and the queue is untouched.
		if s.Prefetch("v1/1", src, streamOp(), StreamPart{Track: "v1", Segment: 1}, done) {
			t.Fatal("prefetched with every worker busy")
		}
		if st := s.Stats(); st.InFlight != 2 || st.Running != 2 || st.Queued != 0 {
			t.Fatalf("pool %+v", st)
		}
		close(e.gate)
		wg.Wait()
		r := <-got
		if r == nil || r.Width != 1280 {
			t.Fatalf("prefetch result %+v", r)
		}
		r.Body.Release()
		synctest.Wait()
		if len(e.plans) != 2 || src.refs.Load() != 1 {
			t.Fatalf("%d transcodes, source references %d", len(e.plans), src.refs.Load())
		}
	})
}

func TestPrefetchClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{gate: make(chan struct{})} // never opens: Close must cancel
		s, _ := newService(t, e, nil)
		src := newBuf("video")
		got := make(chan *Result, 1)
		if !s.Prefetch("v0/0", src, streamOp(), StreamPart{Track: "v0"}, func(r *Result) { got <- r }) {
			t.Fatal("no prefetch")
		}
		synctest.Wait()
		s.Close()
		if r := <-got; r != nil {
			t.Fatalf("a cancelled prefetch gave %+v", r)
		}
		if s.Prefetch("v0/1", src, streamOp(), StreamPart{Track: "v0", Segment: 1}, func(*Result) {}) {
			t.Fatal("prefetch after Close")
		}
		synctest.Wait()
		if r := src.refs.Load(); r != 1 {
			t.Fatalf("source holds %d references", r)
		}
	})
}
