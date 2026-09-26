package humaspike

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/server"
)

func humaMux(ready *admin.Readiness) *http.ServeMux {
	mux := http.NewServeMux()
	RegisterHealth(NewAPI(mux), ready)
	return mux
}

func handMux(ready *admin.Readiness) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(admin.PatternHealthz, admin.Healthz)
	mux.Handle(admin.PatternReadyz, ready)
	return mux
}

type result struct {
	Status  int
	Headers map[string]string
	Body    string
}

func get(t *testing.T, h http.Handler, target string, header ...string) result {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	hs := map[string]string{}
	for _, k := range []string{"Content-Type", "Cache-Control", "X-Content-Type-Options"} {
		hs[k] = rec.Header().Get(k)
	}
	return result{rec.Code, hs, rec.Body.String()}
}

// TestHealthParity: the huma handlers answer exactly like the hand-written
// ones (status, body, headers) in every readiness state.
func TestHealthParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path string
		state      func(*admin.Readiness)
	}{
		{"healthz", "/healthz", func(*admin.Readiness) {}},
		{"readyz starting", "/readyz", func(*admin.Readiness) {}},
		{"readyz ready", "/readyz", func(r *admin.Readiness) { r.SetReady() }},
		{"readyz draining", "/readyz", func(r *admin.Readiness) { r.SetDraining() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ready := admin.NewReadiness()
			tc.state(ready)
			want := get(t, handMux(ready), tc.path)
			got := get(t, humaMux(ready), tc.path)
			if d := cmp.Diff(want, got); d != "" {
				t.Errorf("huma differs from hand-written (-hand +huma):\n%s", d)
			}
		})
	}
}

type echoInput struct {
	N int `query:"n" minimum:"1" maximum:"9"`
}

type echoOutput struct {
	Body struct {
		N int `json:"n"`
	}
}

// TestErrorEnvelope: every error huma generates on a JSON route with input
// (malformed, invalid, a handler errs error, an unexpected error) comes out
// as server.ErrorResponse with the errs status, using only the per-API
// hooks of NewAPI.
func TestErrorEnvelope(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	api := NewAPI(mux)
	huma.Register(api, huma.Operation{OperationID: "echo", Method: http.MethodGet, Path: "/echo"},
		func(_ context.Context, in *echoInput) (*echoOutput, error) {
			switch in.N {
			case 2:
				return nil, StatusError{errs.New(errs.KindForbidden, "not this one")}
			case 3:
				return nil, errs.New(errs.KindForbidden, "internal detail") // not a huma.StatusError
			}
			out := &echoOutput{}
			out.Body.N = in.N
			return out, nil
		})

	envelope := func(k errs.Kind) string {
		b, err := json.Marshal(server.NewErrorResponse(k, errs.PublicMessage(k), ""))
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	jsonHeaders := map[string]string{"Content-Type": "application/json", "Cache-Control": "", "X-Content-Type-Options": ""}
	for _, tc := range []struct {
		name, target string
		header       []string
		want         result
	}{
		{"ok", "/echo?n=5", nil, result{200, jsonHeaders, `{"n":5}` + "\n"}},
		{"ok with an unknown Accept falls back to JSON", "/echo?n=5", []string{"Accept", "application/xml"}, result{200, jsonHeaders, `{"n":5}` + "\n"}},
		{"malformed", "/echo?n=abc", nil, result{400, jsonHeaders, envelope(errs.KindInvalidURL)}},
		{"below minimum", "/echo?n=0", nil, result{400, jsonHeaders, envelope(errs.KindInvalidURL)}},
		{"above maximum", "/echo?n=10", nil, result{400, jsonHeaders, envelope(errs.KindInvalidURL)}},
		{"errs error via StatusError", "/echo?n=2", nil, result{403, jsonHeaders, envelope(errs.KindForbidden)}},
		{"plain error is internal, message not leaked", "/echo?n=3", nil, result{500, jsonHeaders, envelope(errs.KindInternal)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := get(t, mux, tc.target, tc.header...)
			if d := cmp.Diff(tc.want, got); d != "" {
				t.Errorf("(-want +got):\n%s", d)
			}
		})
	}
}

// TestHealthAllocs reports allocations per /healthz request, hand-written
// vs huma, on the same recorder-based path (WP-82b measurement). Not
// parallel: testing.AllocsPerRun refuses to run in a parallel test.
func TestHealthAllocs(t *testing.T) {
	ready := admin.NewReadiness()
	ready.SetReady()
	for _, tc := range []struct {
		name string
		h    http.Handler
	}{{"hand-written", handMux(ready)}, {"huma", humaMux(ready)}} {
		for _, path := range []string{"/healthz", "/readyz"} {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			allocs := testing.AllocsPerRun(1000, func() {
				tc.h.ServeHTTP(httptest.NewRecorder(), req)
			})
			t.Logf("%s %s: %.0f allocs/request (including the recorder)", tc.name, path, allocs)
		}
	}
}

func BenchmarkHealthz(b *testing.B) {
	ready := admin.NewReadiness()
	ready.SetReady()
	for _, bc := range []struct {
		name string
		h    http.Handler
	}{{"impl=hand", handMux(ready)}, {"impl=huma", humaMux(ready)}} {
		b.Run(bc.name, func(b *testing.B) {
			req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/healthz", http.NoBody)
			b.ReportAllocs()
			for b.Loop() {
				bc.h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
