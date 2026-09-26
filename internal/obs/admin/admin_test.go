package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type route struct{ method, path string }

// pprofPaths are the pprof routes that answer immediately.
func pprofPaths() []route {
	return []route{
		{http.MethodGet, "/debug/pprof/"},
		{http.MethodGet, "/debug/pprof/cmdline"},
		{http.MethodGet, "/debug/pprof/symbol"},
		{http.MethodPost, "/debug/pprof/symbol"},
		{http.MethodGet, "/debug/pprof/goroutine?debug=1"},
		{http.MethodGet, "/debug/pprof/heap"},
		{http.MethodGet, "/debug/pprof/goroutineleak"},
		// profile and trace would block for their sampling window; the
		// route checks cover them without invoking the handler.
	}
}

func TestPprofDisabledIs404(t *testing.T) {
	t.Parallel()

	mux := NewMux(Options{Metrics: http.NotFoundHandler(), Readiness: NewReadiness()})
	all := append(pprofPaths(),
		route{http.MethodGet, "/debug/pprof/profile?seconds=1"},
		route{http.MethodGet, "/debug/pprof/trace?seconds=1"},
	)
	for _, p := range all {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			t.Parallel()
			if rec := serve(t, mux, p.method, p.path); rec.Code != http.StatusNotFound {
				t.Errorf("status %d, want 404 with pprof disabled", rec.Code)
			}
		})
	}
}

func TestPprofEnabled(t *testing.T) {
	t.Parallel()

	mux := NewMux(Options{Pprof: true})
	for _, p := range pprofPaths() {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			t.Parallel()
			if rec := serve(t, mux, p.method, p.path); rec.Code != http.StatusOK {
				t.Errorf("status %d, want 200 with pprof enabled: %s", rec.Code, rec.Body.String())
			}
		})
	}
	for _, path := range []string{"/debug/pprof/profile", "/debug/pprof/trace"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		if _, pattern := mux.Handler(req); pattern != "GET "+path {
			t.Errorf("%s routed to pattern %q", path, pattern)
		}
	}
}

func TestMuxRoutes(t *testing.T) {
	t.Parallel()

	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("xi_build_info 1\n"))
	})
	ready := NewReadiness()
	mux := NewMux(Options{Metrics: metrics, Readiness: ready})

	tests := []struct {
		method, path string
		status       int
		body         string
	}{
		{http.MethodGet, "/healthz", http.StatusOK, "ok\n"},
		{http.MethodGet, "/readyz", http.StatusServiceUnavailable, "starting\n"},
		{http.MethodGet, "/metrics", http.StatusOK, "xi_build_info 1\n"},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, ""},
		{http.MethodGet, "/", http.StatusNotFound, ""},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()
			rec := serve(t, mux, tc.method, tc.path)
			if rec.Code != tc.status {
				t.Errorf("status %d, want %d", rec.Code, tc.status)
			}
			if tc.body != "" && rec.Body.String() != tc.body {
				t.Errorf("body %q, want %q", rec.Body.String(), tc.body)
			}
		})
	}
}

func TestMetricsOptional(t *testing.T) {
	t.Parallel()

	if rec := serve(t, NewMux(Options{}), http.MethodGet, "/metrics"); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 without a metrics handler", rec.Code)
	}
}

func TestReadinessTransitions(t *testing.T) {
	t.Parallel()

	type step struct {
		action    string // "ready", "drain"
		wantOK    bool   // SetReady result
		wantState State
		wantCode  int
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{"starting to ready", []step{
			{"ready", true, Ready, http.StatusOK},
		}},
		{"ready is idempotent", []step{
			{"ready", true, Ready, http.StatusOK},
			{"ready", true, Ready, http.StatusOK},
		}},
		{"ready to draining", []step{
			{"ready", true, Ready, http.StatusOK},
			{"drain", false, Draining, http.StatusServiceUnavailable},
		}},
		{"draining is terminal", []step{
			{"ready", true, Ready, http.StatusOK},
			{"drain", false, Draining, http.StatusServiceUnavailable},
			{"ready", false, Draining, http.StatusServiceUnavailable},
		}},
		{"drain before ready", []step{
			{"drain", false, Draining, http.StatusServiceUnavailable},
			{"ready", false, Draining, http.StatusServiceUnavailable},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := NewReadiness()
			if r.State() != Starting || r.IsReady() {
				t.Fatalf("new readiness in state %v", r.State())
			}
			if rec := serve(t, r, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("starting /readyz = %d, want 503", rec.Code)
			}
			for i, s := range tc.steps {
				switch s.action {
				case "ready":
					if ok := r.SetReady(); ok != s.wantOK {
						t.Errorf("step %d: SetReady = %v, want %v", i, ok, s.wantOK)
					}
				case "drain":
					r.SetDraining()
				}
				if got := r.State(); got != s.wantState {
					t.Errorf("step %d: state %v, want %v", i, got, s.wantState)
				}
				rec := serve(t, r, http.MethodGet, "/readyz")
				if rec.Code != s.wantCode {
					t.Errorf("step %d: /readyz = %d, want %d", i, rec.Code, s.wantCode)
				}
				if body := strings.TrimSpace(rec.Body.String()); body != s.wantState.String() {
					t.Errorf("step %d: body %q, want %q", i, body, s.wantState)
				}
				if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
					t.Errorf("step %d: Cache-Control %q", i, cc)
				}
			}
		})
	}
}

// TestReadinessConcurrent races SetReady against SetDraining; draining must
// always win once it has happened.
func TestReadinessConcurrent(t *testing.T) {
	t.Parallel()

	r := NewReadiness()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { r.SetReady() })
	}
	wg.Go(r.SetDraining)
	wg.Wait()
	r.SetReady()
	if r.State() != Draining {
		t.Fatalf("state %v after drain, want draining", r.State())
	}
}

func TestStateString(t *testing.T) {
	t.Parallel()

	for s, want := range map[State]string{Starting: "starting", Ready: "ready", Draining: "draining", State(42): "unknown"} {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", s, got, want)
		}
	}
}
