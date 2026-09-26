package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil/promlint"
	dto "github.com/prometheus/client_model/go"
)

// value returns the single sample value of c (counter, gauge or untyped) or
// the sample count of a histogram. It stands in for prometheus/testutil,
// which would pull an extra module into go.mod.
func value(t *testing.T, c prometheus.Collector) float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	c.Collect(ch)
	close(ch)
	var (
		total float64
		n     int
	)
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		n++
		switch {
		case pb.Counter != nil:
			total += pb.GetCounter().GetValue()
		case pb.Gauge != nil:
			total += pb.GetGauge().GetValue()
		case pb.Histogram != nil:
			total += float64(pb.GetHistogram().GetSampleCount())
		}
	}
	if n != 1 {
		t.Fatalf("collector produced %d series, want 1", n)
	}
	return total
}

type fakeVips struct{ s VipsStats }

func (f fakeVips) Stats() VipsStats { return f.s }

func mustNew(t *testing.T, opts Options) *Metrics {
	t.Helper()
	m, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// TestNewTwice proves each New owns its registry: a second construction in
// the same process neither fails nor shares state with the first.
func TestNewTwice(t *testing.T) {
	t.Parallel()

	a := mustNew(t, Options{Version: "v1", Vips: fakeVips{}})
	b := mustNew(t, Options{Version: "v1", Vips: fakeVips{}})
	if a.Registry() == b.Registry() {
		t.Fatal("two Metrics share a registry")
	}
	a.ObserveRequest("imgproxy", 200, "webp", time.Millisecond)
	if got := value(t, b.requests.WithLabelValues("imgproxy", "200", "webp")); got != 0 {
		t.Fatalf("second registry saw first's observation: %v", got)
	}
}

func TestMetricNamesAndLint(t *testing.T) {
	t.Parallel()

	m := mustNew(t, Options{Version: "v1.2.3", Commit: "abc", Vips: fakeVips{VipsStats{MemBytes: 10, MemHighwaterBytes: 20, Allocs: 3}}})
	// Touch every vec so it appears in Gather.
	m.ObserveRequest("imgproxy", 200, "webp", 5*time.Millisecond)
	m.ObserveStage("fetch", time.Millisecond)
	m.ObserveFetch("default", "http", time.Millisecond, 1024)
	m.ObserveCache("memory", true)
	m.Limiter().Rejected(OverloadQueueFull)

	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var names []string
	for _, mf := range mfs {
		if n := mf.GetName(); strings.HasPrefix(n, Namespace+"_") {
			names = append(names, n)
		}
	}
	want := []string{
		"xi_build_info",
		"xi_cache_requests_total",
		"xi_fetch_coalesced_total",
		"xi_overload_total",
		"xi_panics_total",
		"xi_queue_depth",
		"xi_request_duration_seconds",
		"xi_requests_in_flight",
		"xi_requests_total",
		"xi_semaphore_wait_seconds",
		"xi_source_bytes_total",
		"xi_source_fetch_duration_seconds",
		"xi_stage_duration_seconds",
		"xi_vips_allocs",
		"xi_vips_mem_bytes",
		"xi_vips_mem_highwater_bytes",
		"xi_workers_busy",
	}
	slices.Sort(names)
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("metric names (-want +got):\n%s", diff)
	}

	problems, err := promlint.NewWithMetricFamilies(mfs).Lint()
	if err != nil {
		t.Fatalf("promlint: %v", err)
	}
	for _, p := range problems {
		if strings.HasPrefix(p.Metric, Namespace+"_") {
			t.Errorf("promlint %s: %s", p.Metric, p.Text)
		}
	}
}

func TestVipsGaugesOptional(t *testing.T) {
	t.Parallel()

	m := mustNew(t, Options{})
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "xi_vips_") {
			t.Errorf("vips gauge %s registered without a VipsCollector", mf.GetName())
		}
	}
}

func TestObservations(t *testing.T) {
	t.Parallel()

	m := mustNew(t, Options{Vips: fakeVips{VipsStats{MemBytes: 4096, MemHighwaterBytes: 8192, Allocs: 7}}})

	m.ObserveRequest("imgix", 404, "jpeg", time.Millisecond)
	m.ObserveRequest("imgix", 404, "jpeg", time.Millisecond)
	done := m.RequestStarted()
	inFlight := value(t, m.inFlight)
	done()
	m.ObserveFetch("assets", "s3", time.Millisecond, 100)
	m.ObserveFetch("assets", "s3", time.Millisecond, 0)
	m.IncFetchCoalesced()
	m.IncPanic()
	m.ObserveCache("disk", false)
	m.ObserveCache("disk", true)
	m.ObserveCache("disk", true)
	lim := m.Limiter()
	lim.QueueDepth(5)
	lim.WorkersBusy(3)
	lim.Waited(time.Second)
	lim.Rejected(OverloadWaitTimeout)
	lim.Rejected("https://attacker.example/unbounded")

	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"requests_total", value(t, m.requests.WithLabelValues("imgix", "404", "jpeg")), 2},
		{"in_flight during request", inFlight, 1},
		{"in_flight after done", value(t, m.inFlight), 0},
		{"source_bytes_total", value(t, m.sourceBytes.WithLabelValues("s3")), 100},
		{"fetch_coalesced_total", value(t, m.fetchCoalesced), 1},
		{"panics_total", value(t, m.panics), 1},
		{"cache miss", value(t, m.cacheRequests.WithLabelValues("disk", CacheMiss)), 1},
		{"cache hit", value(t, m.cacheRequests.WithLabelValues("disk", CacheHit)), 2},
		{"queue_depth", value(t, m.queueDepth), 5},
		{"workers_busy", value(t, m.workersBusy), 3},
		{"overload wait_timeout", value(t, m.overload.WithLabelValues(OverloadWaitTimeout)), 1},
		{"overload other", value(t, m.overload.WithLabelValues("other")), 1},
		{"semaphore_wait count", value(t, m.semaphoreWait), 1},
		{"fetch observations", value(t, m.fetchDuration), 2},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}

	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	gotVips := map[string]float64{}
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "xi_vips_") {
			gotVips[mf.GetName()] = mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	wantVips := map[string]float64{"xi_vips_mem_bytes": 4096, "xi_vips_mem_highwater_bytes": 8192, "xi_vips_allocs": 7}
	if diff := cmp.Diff(wantVips, gotVips); diff != "" {
		t.Errorf("vips gauges (-want +got):\n%s", diff)
	}
}

func TestHandler(t *testing.T) {
	t.Parallel()

	m := mustNew(t, Options{Version: "v9", Commit: "deadbeef"})
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{`xi_build_info{commit="deadbeef"`, `version="v9"`, "go_goroutines"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
}
