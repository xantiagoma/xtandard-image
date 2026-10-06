package media

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// buf is a reference-counted test buffer.
type buf struct {
	data []byte
	refs atomic.Int64
}

func newBuf(s string) *buf {
	b := &buf{data: []byte(s)}
	b.refs.Store(1)
	return b
}

func (b *buf) Bytes() []byte { return b.data }
func (b *buf) Retain()       { b.refs.Add(1) }
func (b *buf) Release()      { b.refs.Add(-1) }

// engine is a fake Engine: Run waits for gate (when set) or its context.
type engine struct {
	mu     sync.Mutex
	opens  int
	gate   chan struct{}
	panics bool
	err    error
	in     video.TranscodeInput
	outs   []*buf
	plans  []*video.TranscodePlan
	pixels [][]Buffer
}

func (e *engine) Open(_ context.Context, src Buffer, _ video.Limits, _ int) (Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.opens++
	if e.panics {
		panic("boom")
	}
	src.Retain()
	return &job{e: e, src: src}, nil
}

type job struct {
	e   *engine
	src Buffer
}

func (j *job) Input() video.TranscodeInput { return j.e.in }

func (j *job) Run(ctx context.Context, p *video.TranscodePlan, pixels []Buffer) (Buffer, error) {
	j.e.mu.Lock()
	j.e.plans = append(j.e.plans, p)
	j.e.pixels = append(j.e.pixels, pixels)
	j.e.mu.Unlock()
	if j.e.gate != nil {
		select {
		case <-j.e.gate:
		case <-ctx.Done():
			return nil, errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
		}
	}
	if j.e.err != nil {
		return nil, j.e.err
	}
	b := newBuf("file:" + p.Output.Key())
	j.e.mu.Lock()
	j.e.outs = append(j.e.outs, b)
	j.e.mu.Unlock()
	return b, nil
}

func (j *job) Close() { j.src.Release() }

type metrics struct {
	mu        sync.Mutex
	results   []string
	coalesced int
	costs     []float64
	rejected  []limiter.Reason
	load      Load
	peak      Load // the largest value of each field
}

func (m *metrics) JobCost(units float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.costs = append(m.costs, units)
}

func (m *metrics) Rejected(r limiter.Reason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, r)
}

func (m *metrics) Load(l Load) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load = l
	m.peak = Load{
		Jobs: max(m.peak.Jobs, l.Jobs), Queued: max(m.peak.Queued, l.Queued), Running: max(m.peak.Running, l.Running),
		CapacityUsed: max(m.peak.CapacityUsed, l.CapacityUsed), QueuedCost: max(m.peak.QueuedCost, l.QueuedCost),
	}
}

func (m *metrics) Transcoded(result string, _ time.Duration, _, _ int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = append(m.results, result)
}

func (m *metrics) Coalesced() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coalesced++
}

func caps() video.TranscodeCaps {
	return video.TranscodeCaps{
		Video: video.EncoderSet{VP9: true, AV1: true}, MuxMP4: true, MuxWebM: true, VideoFilters: true,
		Overlays: true, Timecode: true,
	}
}

func input() video.TranscodeInput {
	return video.TranscodeInput{Duration: 2, Video: true, Width: 1280, Height: 720, FPS: 25}
}

func newService(t *testing.T, e *engine, set func(*Config)) (*Service, *metrics) {
	t.Helper()
	cfg := Config{
		Workers: 2, Queue: 4, QueueTimeout: time.Second, Timeout: time.Minute,
		Transcode: video.TranscodeSettings{Threads: 1, MaxOutputDuration: time.Minute},
	}
	if set != nil {
		set(&cfg)
	}
	e.in = input()
	m := &metrics{}
	s, err := New(&cfg, caps(), e, WithMetrics(m))
	if err != nil {
		t.Fatal(err)
	}
	return s, m
}

func TestTransform(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{}
		s, m := newService(t, e, nil)
		src := newBuf("video")
		res, err := s.Transform(t.Context(), "k", src, &transform.VideoOp{Width: 640, Mute: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(res.Body.Bytes()) != "file:mp4:av1:" || res.Width != 640 || res.Height != 360 || res.Duration != 2 || res.Shared {
			t.Fatalf("result %+v %q", res, res.Body.Bytes())
		}
		res.Body.Release()
		synctest.Wait() // the transcode's goroutine drops its references
		if r := src.refs.Load(); r != 1 {
			t.Errorf("source holds %d references, want the caller's 1", r)
		}
		if r := e.outs[0].refs.Load(); r != 0 {
			t.Errorf("output holds %d references after release", r)
		}
		if len(m.results) != 1 || m.results[0] != "ok" {
			t.Errorf("metrics %v", m.results)
		}
	})
}

func TestTransformUnsupported(t *testing.T) {
	t.Parallel()
	e := &engine{}
	s, _ := newService(t, e, nil)
	_, err := s.Transform(t.Context(), "k", newBuf("v"), &transform.VideoOp{VideoCodec: transform.VideoCodecHEVC}, nil)
	if errs.KindOf(err) != errs.KindUnsupported || e.opens != 0 {
		t.Fatalf("err %v, opens %d", err, e.opens)
	}
}

func TestTransformPanic(t *testing.T) {
	t.Parallel()
	s, m := newService(t, &engine{panics: true}, nil)
	_, err := s.Transform(t.Context(), "k", newBuf("v"), &transform.VideoOp{Mute: true}, nil)
	if errs.KindOf(err) != errs.KindInternal {
		t.Fatalf("err %v", err)
	}
	_ = m
}

// TestTransformCoalesced: identical concurrent requests share one
// transcode, each with its own reference; every reference is returned.
func TestTransformCoalesced(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{gate: make(chan struct{})}
		s, m := newService(t, e, nil)
		src := newBuf("video")
		const n = 8
		var wg sync.WaitGroup
		results := make([]*Result, n)
		for i := range n {
			wg.Go(func() {
				res, err := s.Transform(t.Context(), "same", src, &transform.VideoOp{Mute: true}, nil)
				if err != nil {
					t.Error(err)
					return
				}
				results[i] = res
			})
		}
		synctest.Wait() // every request waits on the one transcode
		close(e.gate)
		wg.Wait()
		synctest.Wait()
		shared := 0
		for _, r := range results {
			if r.Shared {
				shared++
			}
			r.Body.Release()
		}
		if e.opens != 1 || shared != n-1 || m.coalesced != n-1 {
			t.Fatalf("%d transcodes, %d shared, %d coalesced; want 1, %d, %d", e.opens, shared, m.coalesced, n-1, n-1)
		}
		if r := e.outs[0].refs.Load(); r != 0 {
			t.Errorf("output holds %d references", r)
		}
		if r := src.refs.Load(); r != 1 {
			t.Errorf("source holds %d references, want the caller's 1", r)
		}
	})
}

// TestTransformCancel: a transcode survives one of its requests leaving,
// and is cancelled when the last one leaves.
func TestTransformCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{gate: make(chan struct{})}
		s, m := newService(t, e, nil)
		src := newBuf("video")
		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())
		errc := make(chan error, 2)
		for _, ctx := range []context.Context{ctx1, ctx2} {
			go func() {
				_, err := s.Transform(ctx, "k", src, &transform.VideoOp{Mute: true}, nil)
				errc <- err
			}()
		}
		synctest.Wait()
		cancel1()
		if err := <-errc; errs.KindOf(err) != errs.KindCanceled {
			t.Fatalf("first request: %v", err)
		}
		synctest.Wait()
		if len(m.results) != 0 {
			t.Fatal("the transcode ended with one request still waiting")
		}
		cancel2()
		if err := <-errc; errs.KindOf(err) != errs.KindCanceled {
			t.Fatalf("second request: %v", err)
		}
		synctest.Wait()
		if len(m.results) != 1 || m.results[0] != "canceled" {
			t.Fatalf("transcode results %v, want one canceled", m.results)
		}
		if r := src.refs.Load(); r != 1 {
			t.Errorf("source holds %d references", r)
		}
	})
}

// TestTransformTimeout: XI_VIDEO_TRANSFORM_TIMEOUT ends a transcode with
// KindTimeout.
func TestTransformTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{gate: make(chan struct{})}
		s, m := newService(t, e, func(c *Config) { c.Timeout = 30 * time.Second })
		_, err := s.Transform(t.Context(), "k", newBuf("v"), &transform.VideoOp{Mute: true}, nil)
		if errs.KindOf(err) != errs.KindTimeout {
			t.Fatalf("err %v, want timeout", err)
		}
		if len(m.results) != 1 || m.results[0] != "timeout" {
			t.Errorf("metrics %v", m.results)
		}
	})
}

// TestTransformAdmission: the pool is the video one: with one worker and
// no queue, a second transcode is refused (429) while the first runs.
func TestTransformAdmission(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := &engine{gate: make(chan struct{})}
		s, _ := newService(t, e, func(c *Config) { c.Workers, c.MaxJobs, c.Queue = 1, 1, 0 })
		done := make(chan error, 1)
		go func() {
			res, err := s.Transform(t.Context(), "a", newBuf("v"), &transform.VideoOp{Mute: true}, nil)
			if err == nil {
				res.Body.Release()
			}
			done <- err
		}()
		synctest.Wait()
		if st := s.Stats(); st.InFlight != 1 {
			t.Fatalf("in flight %d", st.InFlight)
		}
		_, err := s.Transform(t.Context(), "b", newBuf("v"), &transform.VideoOp{Mute: true}, nil)
		if errs.KindOf(err) != errs.KindOverloaded {
			t.Fatalf("second transcode: %v, want overloaded", err)
		}
		close(e.gate)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	for _, c := range []Config{
		{Workers: 0, Timeout: time.Second},
		{Workers: 1, Queue: -1, Timeout: time.Second},
		{Workers: 1},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v is valid", c)
		}
	}
}

// TestTransformWaveform: a waveform picture reports its size and no codec.
func TestTransformWaveform(t *testing.T) {
	t.Parallel()
	e := &engine{}
	cfg := Config{
		Workers: 1, QueueTimeout: time.Second, Timeout: time.Minute,
		Transcode: video.TranscodeSettings{Threads: 1},
	}
	c := caps()
	c.AudioFilters, c.Waveform = true, true
	s, err := New(&cfg, c, e)
	if err != nil {
		t.Fatal(err)
	}
	e.in = video.TranscodeInput{Duration: 4, Audio: true, AudioDecodable: true, SampleRate: 44100, Channels: 2}
	op := &transform.VideoOp{Container: transform.VideoContainerPNG, Width: 400, Height: 100, Waveform: &transform.VideoWaveform{}}
	res, err := s.Transform(t.Context(), "w", newBuf("audio"), op, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Release()
	if res.Width != 400 || res.Height != 100 || res.Output.Key() != "png::" || res.Output.ContentType != "image/png" {
		t.Fatalf("result %+v", res)
	}
}
