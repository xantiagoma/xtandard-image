package media

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// costEngine is a fake Engine for admission tests: a source named
// "<name>:<cost>" probes as a 1280×720 clip whose duration is its cost
// (the test's cost model reads it back), and its transcode runs until
// release(name) or its context ends. Open blocks while openGate is set.
type costEngine struct {
	mu       sync.Mutex
	gates    map[string]chan struct{}
	started  []string      // names, in the order their transcodes started
	openGate chan struct{} // guarded by mu
	opened   int
}

// holdOpens makes Open wait until the returned channel is closed.
func (e *costEngine) holdOpens() chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.openGate = make(chan struct{})
	return e.openGate
}

func (e *costEngine) opens() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.opened
}

func newCostEngine() *costEngine { return &costEngine{gates: map[string]chan struct{}{}} }

func (e *costEngine) gate(name string) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.gates[name]
	if !ok {
		g = make(chan struct{})
		e.gates[name] = g
	}
	return g
}

// release lets name's transcode finish.
func (e *costEngine) release(name string) { close(e.gate(name)) }

func (e *costEngine) order() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.started...)
}

func (e *costEngine) Open(ctx context.Context, src Buffer, _ video.Limits, _ int) (Job, error) {
	e.mu.Lock()
	gate := e.openGate
	e.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
		}
	}
	name, c, _ := strings.Cut(string(src.Bytes()), ":")
	cost, err := strconv.ParseFloat(c, 64)
	if err != nil {
		return nil, errs.New(errs.KindCorrupt, "bad test source")
	}
	e.mu.Lock()
	e.opened++
	e.mu.Unlock()
	src.Retain()
	in := video.TranscodeInput{Duration: cost, Video: true, Width: 1280, Height: 720, FPS: 25}
	return &costJob{e: e, name: name, src: src, in: in}, nil
}

type costJob struct {
	e    *costEngine
	name string
	src  Buffer
	in   video.TranscodeInput
}

func (j *costJob) Input() video.TranscodeInput { return j.in }

func (j *costJob) Run(ctx context.Context, _ *video.TranscodePlan, _ []Buffer) (Buffer, error) {
	j.e.mu.Lock()
	j.e.started = append(j.e.started, j.name)
	j.e.mu.Unlock()
	select {
	case <-j.e.gate(j.name):
		return newBuf("out:" + j.name), nil
	case <-ctx.Done():
		return nil, errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
	}
}

func (j *costJob) Close() { j.src.Release() }

// newCostService is a service over e whose cost model is the probed
// duration (see costEngine).
func newCostService(t *testing.T, e *costEngine, set func(*Config)) (*Service, *metrics) {
	t.Helper()
	cfg := Config{
		Workers: 2, Capacity: 10, MaxJobs: 4, Queue: 8, QueueCost: 100,
		QueueTimeout: time.Minute, Timeout: time.Hour,
		Transcode: video.TranscodeSettings{Threads: 1},
	}
	if set != nil {
		set(&cfg)
	}
	m := &metrics{}
	s, err := New(&cfg, caps(), e, WithMetrics(m))
	if err != nil {
		t.Fatal(err)
	}
	s.estimate = func(in *video.TranscodeInput, _ *video.TranscodePlan) float64 { return in.Duration }
	return s, m
}

// bgJob is one transform of a "<name>:<cost>" source in the background.
type bgJob struct {
	name   string
	cancel context.CancelFunc
	done   chan error
}

func startJob(ctx context.Context, s *Service, name string, cost float64) *bgJob {
	ctx, cancel := context.WithCancel(ctx)
	j := &bgJob{name: name, cancel: cancel, done: make(chan error, 1)}
	src := newBuf(name + ":" + strconv.FormatFloat(cost, 'f', -1, 64))
	go func() {
		res, err := s.Transform(ctx, name, src, &transform.VideoOp{Mute: true}, nil)
		if err == nil {
			res.Body.Release()
		}
		j.done <- err
	}()
	return j
}

func (j *bgJob) wait(t *testing.T) {
	t.Helper()
	if err := <-j.done; err != nil {
		t.Fatalf("%s: %v", j.name, err)
	}
}

func loadOf(s *Service) Load { return s.Stats().Load }

// TestAdmissionInterleave: light jobs run beside a heavy one while their
// costs fit; a heavy job that does not fit waits, and the light ones that
// arrive after it wait behind it (FIFO) even when they would fit, so it
// is never starved; once it fits it starts first, then they do.
func TestAdmissionInterleave(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		s, m := newCostService(t, e, nil) // capacity 10, 4 jobs
		h1 := startJob(t.Context(), s, "h1", 6)
		synctest.Wait()
		l1 := startJob(t.Context(), s, "l1", 2)
		synctest.Wait()
		l2 := startJob(t.Context(), s, "l2", 2)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 3, Running: 3, CapacityUsed: 10}); got != want {
			t.Fatalf("heavy and light together: load %+v, want %+v", got, want)
		}
		h2 := startJob(t.Context(), s, "h2", 6)
		synctest.Wait()
		l3 := startJob(t.Context(), s, "l3", 2)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 5, Queued: 2, Running: 3, CapacityUsed: 10, QueuedCost: 8}); got != want {
			t.Fatalf("full: load %+v, want %+v", got, want)
		}

		// l1 ends: 2 units free, l3 would fit but waits behind h2.
		e.release("l1")
		l1.wait(t)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 4, Queued: 2, Running: 2, CapacityUsed: 8, QueuedCost: 8}); got != want {
			t.Fatalf("head-of-line: load %+v, want %+v", got, want)
		}

		// h1 ends: h2 fits and starts, then l3 beside it.
		e.release("h1")
		h1.wait(t)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 3, Running: 3, CapacityUsed: 10}); got != want {
			t.Fatalf("after h1: load %+v, want %+v", got, want)
		}
		order := e.order()
		slices.Sort(order[3:]) // h2 and l3 start together
		if diff := cmp.Diff([]string{"h1", "l1", "l2", "h2", "l3"}, order); diff != "" {
			t.Errorf("start order (-want +got):\n%s", diff)
		}
		for _, j := range []*bgJob{l2, h2, l3} {
			e.release(j.name)
			j.wait(t)
		}
		synctest.Wait()
		if got := loadOf(s); got != (Load{}) {
			t.Fatalf("idle pool: load %+v", got)
		}
		if diff := cmp.Diff([]float64{6, 2, 2, 6, 2}, m.costs); diff != "" {
			t.Errorf("observed costs (-want +got):\n%s", diff)
		}
	})
}

// TestAdmissionMaxJobs: cheap jobs run together up to MaxJobs, whatever
// capacity is left.
func TestAdmissionMaxJobs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		s, _ := newCostService(t, e, func(c *Config) { c.MaxJobs = 2 })
		var jobs []*bgJob
		for _, n := range []string{"a", "b", "c"} {
			jobs = append(jobs, startJob(t.Context(), s, n, 1))
			synctest.Wait()
		}
		if got, want := loadOf(s), (Load{Jobs: 3, Queued: 1, Running: 2, CapacityUsed: 2, QueuedCost: 1}); got != want {
			t.Fatalf("load %+v, want %+v", got, want)
		}
		for _, j := range jobs {
			e.release(j.name)
			j.wait(t)
		}
	})
}

// TestAdmissionQueueCost: a job whose cost would take the queued cost over
// QueueCost is refused with 429 + Retry-After; one behind an empty queue
// always waits; a queued job is refused with 503 + Retry-After once the
// queue timeout passes.
func TestAdmissionQueueCost(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		s, m := newCostService(t, e, func(c *Config) { c.QueueCost, c.QueueTimeout = 8, 30*time.Second })
		run := startJob(t.Context(), s, "run", 10)
		synctest.Wait()
		a := startJob(t.Context(), s, "a", 9) // over QueueCost, but the queue is empty
		synctest.Wait()
		if got := loadOf(s); got.Queued != 1 || got.QueuedCost != 9 {
			t.Fatalf("a not queued: %+v", got)
		}
		_, err := s.Transform(t.Context(), "b", newBuf("b:1"), &transform.VideoOp{Mute: true}, nil)
		oe, ok := errors.AsType[*limiter.OverloadError](err)
		if !ok || errs.KindOf(err) != errs.KindOverloaded || oe.Reason != limiter.ReasonQueueCost || oe.RetryAfter < time.Second {
			t.Fatalf("queued cost over the bound: %v, want 429 queue_cost", err)
		}
		// a waits out the queue timeout: 503.
		err = <-a.done
		oe, ok = errors.AsType[*limiter.OverloadError](err)
		if !ok || errs.KindOf(err) != errs.KindUnavailable || oe.Reason != limiter.ReasonQueueTimeout {
			t.Fatalf("queue timeout: %v, want 503 queue_timeout", err)
		}
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 1, Running: 1, CapacityUsed: 10}); got != want {
			t.Fatalf("after the refusals: load %+v, want %+v", got, want)
		}
		if diff := cmp.Diff([]limiter.Reason{limiter.ReasonQueueCost, limiter.ReasonQueueTimeout}, m.rejected); diff != "" {
			t.Errorf("rejections (-want +got):\n%s", diff)
		}
		e.release("run")
		run.wait(t)
	})
}

// TestAdmissionSlots: the pool holds MaxJobs + Queue jobs, probing ones
// included; beyond that, 429 queue_full at once.
func TestAdmissionSlots(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		gate := e.holdOpens()
		s, _ := newCostService(t, e, func(c *Config) { c.MaxJobs, c.Queue = 1, 1 })
		a := startJob(t.Context(), s, "a", 1)
		b := startJob(t.Context(), s, "b", 1)
		synctest.Wait() // both probing (one waits for the single probe slot)
		_, err := s.Transform(t.Context(), "c", newBuf("c:1"), &transform.VideoOp{Mute: true}, nil)
		if oe, ok := errors.AsType[*limiter.OverloadError](err); !ok || oe.Reason != limiter.ReasonQueueFull {
			t.Fatalf("pool full: %v, want 429 queue_full", err)
		}
		close(gate)
		e.release("a")
		e.release("b")
		a.wait(t)
		b.wait(t)
	})
}

// TestAdmissionClamp: a job costing more than the whole capacity is not
// refused: it is charged the capacity and runs alone; a job under one
// unit is charged one.
func TestAdmissionClamp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		s, m := newCostService(t, e, nil)
		huge := startJob(t.Context(), s, "huge", 1e9)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 1, Running: 1, CapacityUsed: 10}); got != want {
			t.Fatalf("huge job: load %+v, want %+v", got, want)
		}
		tiny := startJob(t.Context(), s, "tiny", 0.01)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 2, Queued: 1, Running: 1, CapacityUsed: 10, QueuedCost: 1}); got != want {
			t.Fatalf("tiny job beside the huge one: load %+v, want %+v", got, want)
		}
		e.release("huge")
		huge.wait(t)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 1, Running: 1, CapacityUsed: 1}); got != want {
			t.Fatalf("tiny job: load %+v, want %+v", got, want)
		}
		e.release("tiny")
		tiny.wait(t)
		if diff := cmp.Diff([]float64{1e9, 0.01}, m.costs); diff != "" {
			t.Errorf("observed costs are the estimates (-want +got):\n%s", diff)
		}
		if m.peak.CapacityUsed != 10 {
			t.Errorf("peak capacity used %d, want the capacity", m.peak.CapacityUsed)
		}
	})
}

// TestAdmissionCancel: a job cancelled while it waits (for capacity, or
// for the probe) gives back its place and nothing else; the pool is whole
// afterwards.
func TestAdmissionCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		s, m := newCostService(t, e, nil)
		run := startJob(t.Context(), s, "run", 8)
		synctest.Wait()
		queued := startJob(t.Context(), s, "queued", 5)
		synctest.Wait()
		if got := loadOf(s); got.Queued != 1 || got.QueuedCost != 5 {
			t.Fatalf("not queued: %+v", got)
		}
		queued.cancel()
		if err := <-queued.done; errs.KindOf(err) != errs.KindCanceled {
			t.Fatalf("cancelled while queued: %v", err)
		}
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 1, Running: 1, CapacityUsed: 8}); got != want {
			t.Fatalf("after cancel: load %+v, want %+v", got, want)
		}
		e.release("run")
		run.wait(t)
		synctest.Wait()
		// Every unit and slot is back: a job of the whole capacity and
		// MaxJobs cheap ones start at once.
		full := startJob(t.Context(), s, "full", 10)
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 1, Running: 1, CapacityUsed: 10}); got != want {
			t.Fatalf("whole capacity: load %+v, want %+v", got, want)
		}
		e.release("full")
		full.wait(t)

		// Cancelled while waiting for the probe slot (Workers probes).
		gate := e.holdOpens()
		var probing []*bgJob
		for _, n := range []string{"p1", "p2", "p3"} {
			probing = append(probing, startJob(t.Context(), s, n, 1))
		}
		synctest.Wait()
		probing[2].cancel()
		if err := <-probing[2].done; errs.KindOf(err) != errs.KindCanceled {
			t.Fatalf("cancelled while probing: %v", err)
		}
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 2}); got != want {
			t.Fatalf("two probing: load %+v, want %+v", got, want)
		}
		close(gate)
		for _, j := range probing[:2] {
			e.release(j.name)
			j.wait(t)
		}
		synctest.Wait()
		if got := loadOf(s); got != (Load{}) || m.load != (Load{}) {
			t.Fatalf("idle pool: load %+v, metrics %+v", got, m.load)
		}
	})
}

// TestPrefetchBacksOff: a prefetch starts only when nobody is queued and
// its segment's cost fits in the free capacity at once; otherwise it
// gives up after its probe (done gets nil) without queueing, transcoding
// or keeping anything.
func TestPrefetchBacksOff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		s, _ := newCostService(t, e, nil)
		busy := startJob(t.Context(), s, "busy", 8)
		synctest.Wait()
		got := make(chan *Result, 1)
		// The stream's cost is its duration (5 units): only 2 are free.
		src := newBuf("seg:5")
		if !s.Prefetch("seg/0", src, streamOp(), StreamPart{Track: "v0"}, func(r *Result) { got <- r }) {
			t.Fatal("no prefetch with a run slot and capacity free")
		}
		if r := <-got; r != nil {
			t.Fatalf("prefetch ran without the capacity for it: %+v", r)
		}
		synctest.Wait()
		if got, want := loadOf(s), (Load{Jobs: 1, Running: 1, CapacityUsed: 8}); got != want {
			t.Fatalf("after the prefetch: load %+v, want %+v", got, want)
		}

		// Someone queued: no prefetch at all, not even its probe.
		queued := startJob(t.Context(), s, "queued", 5)
		synctest.Wait()
		opened := e.opens()
		if s.Prefetch("seg/1", src, streamOp(), StreamPart{Track: "v0", Segment: 1}, func(*Result) {}) {
			t.Fatal("prefetch with a job queued")
		}
		if e.opens() != opened {
			t.Fatal("a refused prefetch probed")
		}

		// Idle with room: the prefetch runs.
		e.release("busy")
		busy.wait(t)
		e.release("queued")
		queued.wait(t)
		synctest.Wait()
		e.release("seg")
		if !s.Prefetch("seg/2", src, streamOp(), StreamPart{Track: "v0", Segment: 2}, func(r *Result) { got <- r }) {
			t.Fatal("no prefetch on an idle pool")
		}
		r := <-got
		if r == nil {
			t.Fatal("idle prefetch failed")
		}
		r.Body.Release()
		s.Close()
		synctest.Wait()
		if got := loadOf(s); got != (Load{}) {
			t.Fatalf("idle pool: load %+v", got)
		}
		if n := src.refs.Load(); n != 1 {
			t.Fatalf("source holds %d references", n)
		}
		if diff := cmp.Diff([]string{"busy", "queued", "seg"}, e.order()); diff != "" {
			t.Errorf("transcodes (-want +got): the refused prefetch ran\n%s", diff)
		}
	})
}

// TestStreamJoinsBusyPrefetch: a request that joins a prefetch which then
// finds the pool busy runs the transcode itself instead of failing.
func TestStreamJoinsBusyPrefetch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newCostEngine()
		s, _ := newCostService(t, e, func(c *Config) { c.Capacity = 6 })
		busy := startJob(t.Context(), s, "busy", 4)
		synctest.Wait()
		gate := e.holdOpens()
		got := make(chan *Result, 1)
		src := newBuf("seg:5") // 5 units, 2 free
		if !s.Prefetch("seg/0", src, streamOp(), StreamPart{Track: "v0"}, func(r *Result) { got <- r }) {
			t.Fatal("no prefetch")
		}
		synctest.Wait() // the prefetch is probing
		errc := make(chan error, 1)
		go func() {
			res, err := s.Stream(t.Context(), "seg/0", src, streamOp(), StreamPart{Track: "v0"})
			if err == nil {
				res.Body.Release()
			}
			errc <- err
		}()
		synctest.Wait() // the request joined the prefetch
		close(gate)
		if r := <-got; r != nil {
			t.Fatalf("the prefetch ran without the capacity: %+v", r)
		}
		synctest.Wait()
		if got := loadOf(s); got.Queued != 1 || got.QueuedCost != 5 {
			t.Fatalf("the request does not wait for its own transcode: %+v", got)
		}
		e.release("busy")
		busy.wait(t)
		e.release("seg")
		if err := <-errc; err != nil {
			t.Fatalf("request: %v", err)
		}
		s.Close()
		synctest.Wait()
		if n := src.refs.Load(); n != 1 {
			t.Fatalf("source holds %d references", n)
		}
		if got := loadOf(s); got != (Load{}) {
			t.Fatalf("idle pool: load %+v", got)
		}
	})
}
