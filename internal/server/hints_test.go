package server

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// hintsOn enables the server-wide client hints (XI_CLIENT_HINTS_ENABLED).
func hintsOn(h *harness) { h.cfg.Negotiate.EnableClientHints = true }

// noURLWidth clears the fake router's URL width so the Width hint applies.
func noURLWidth(r *transform.Request) { r.Pipelines[0].Resize.Width = transform.Dim{} }

// TestClientHintResponses is the WP-65 end-to-end header table: what the
// processor received (width, DPR, quality delta) and the exact Vary,
// Accept-CH, Critical-CH and Content-DPR of the response.
func TestClientHintResponses(t *testing.T) {
	t.Parallel()
	type want struct {
		Width, Delta                           int
		DPR                                    float64
		Vary, AcceptCH, CriticalCH, ContentDPR string
	}
	const allHints = "Sec-CH-DPR, DPR, Sec-CH-Width, Width, Sec-CH-Viewport-Width, Viewport-Width, Save-Data, ECT" //nolint:misspell // ECT header

	tests := []struct {
		name   string
		mutate func(*harness)
		header http.Header
		want   want
	}{
		{
			name:   "disabled: hints ignored, no Accept-CH",
			header: http.Header{"Sec-Ch-Dpr": {"2"}, "Save-Data": {"on"}},
			want:   want{Width: 64, Vary: "Accept"},
		},
		{
			name:   "enabled, no hints: every name that could apply",
			mutate: func(h *harness) { hintsOn(h); h.router.mutate = noURLWidth },
			want:   want{Vary: "Accept, " + allHints, AcceptCH: acceptCH},
		},
		{
			name:   "sec-ch dpr and width",
			mutate: func(h *harness) { hintsOn(h); h.router.mutate = noURLWidth },
			header: http.Header{"Sec-Ch-Dpr": {"2"}, "Sec-Ch-Width": {"800"}, "Dpr": {"3"}, "Sec-Ch-Viewport-Width": {"300"}},
			want:   want{Width: 400, DPR: 2, ContentDPR: "2", Vary: "Accept, Sec-CH-DPR, Sec-CH-Width, Save-Data, ECT", AcceptCH: acceptCH}, //nolint:misspell // ECT header
		},
		{
			name:   "legacy names",
			mutate: func(h *harness) { hintsOn(h); h.router.mutate = noURLWidth },
			header: http.Header{"Dpr": {"1.5"}, "Width": {"600"}},
			want:   want{Width: 400, DPR: 1.5, ContentDPR: "1.5", Vary: "Accept, Sec-CH-DPR, DPR, Sec-CH-Width, Width, Save-Data, ECT", AcceptCH: acceptCH}, //nolint:misspell // ECT header
		},
		{
			name:   "viewport-width fallback",
			mutate: func(h *harness) { hintsOn(h); h.router.mutate = noURLWidth },
			header: http.Header{"Sec-Ch-Viewport-Width": {"375"}},
			want:   want{Width: 375, Vary: "Accept, Sec-CH-DPR, DPR, Sec-CH-Width, Width, Sec-CH-Viewport-Width, Save-Data, ECT", AcceptCH: acceptCH}, //nolint:misspell // ECT header
		},
		{
			name: "url dpr and width win: no hint vary, no content-dpr",
			mutate: func(h *harness) {
				hintsOn(h)
				h.router.mutate = func(r *transform.Request) { r.Pipelines[0].Resize.DPR = 2 }
			},
			header: http.Header{"Sec-Ch-Dpr": {"3"}, "Sec-Ch-Width": {"900"}},
			want:   want{Width: 64, DPR: 2, Vary: "Accept, Save-Data, ECT", AcceptCH: acceptCH}, //nolint:misspell // ECT header
		},
		{
			name: "save-data lowers quality",
			mutate: func(h *harness) {
				hintsOn(h)
				h.router.mutate = func(r *transform.Request) { r.Pipelines[0].Resize.DPR = 1 }
			},
			header: http.Header{"Save-Data": {"on"}},
			want:   want{Width: 64, DPR: 1, Delta: -15, Vary: "Accept, Save-Data", AcceptCH: acceptCH},
		},
		{
			name: "save-data ignored when the url fixes quality",
			mutate: func(h *harness) {
				hintsOn(h)
				h.router.mutate = func(r *transform.Request) { r.Pipelines[0].Resize.DPR = 1; r.Output.Quality = 80 }
			},
			header: http.Header{"Save-Data": {"on"}},
			want:   want{Width: 64, DPR: 1, Vary: "Accept", AcceptCH: acceptCH},
		},
		{
			name: "restricted unsigned: width and dpr hints off, save-data on",
			mutate: func(h *harness) {
				hintsOn(h)
				h.router.policy = router.PolicyRestricted
				h.router.mutate = noURLWidth
			},
			header: http.Header{"Sec-Ch-Dpr": {"2"}, "Sec-Ch-Width": {"1234"}, "Save-Data": {"on"}},
			want:   want{Delta: -15, Vary: "Accept, Save-Data", AcceptCH: acceptCH},
		},
		{
			name: "restricted but signed: hints apply",
			mutate: func(h *harness) {
				hintsOn(h)
				h.router.policy, h.router.sig = router.PolicyRestricted, sign.Verified
				h.router.mutate = noURLWidth
			},
			header: http.Header{"Sec-Ch-Width": {"1234"}},
			want:   want{Width: 1234, Vary: "Accept, Sec-CH-DPR, DPR, Sec-CH-Width, Save-Data, ECT", AcceptCH: acceptCH}, //nolint:misspell // ECT header
		},
		{
			name: "url opt-in without the switch sends Accept-CH",
			mutate: func(h *harness) {
				h.router.mutate = func(r *transform.Request) { noURLWidth(r); r.ClientHints.Width = true }
			},
			header: http.Header{"Sec-Ch-Width": {"500"}},
			want:   want{Width: 500, Vary: "Accept, Sec-CH-Width", AcceptCH: acceptCH},
		},
		{
			name:   "critical-ch",
			mutate: func(h *harness) { hintsOn(h); h.cfg.ClientHints.CriticalCH = true },
			header: http.Header{"Sec-Ch-Dpr": {"2"}},
			want:   want{Width: 64, DPR: 2, ContentDPR: "2", Vary: "Accept, Sec-CH-DPR, Save-Data, ECT", AcceptCH: acceptCH, CriticalCH: criticalCH}, //nolint:misspell // ECT header
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tt.mutate)
			rec := h.do(t, http.MethodGet, "/x", tt.header)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			h.p.mu.Lock()
			rs := h.p.req.Pipelines[0].Resize
			got := want{
				Width: rs.Width.Px, DPR: rs.DPR, Delta: h.p.delta,
				Vary: rec.Header().Get("Vary"), AcceptCH: rec.Header().Get("Accept-CH"),
				CriticalCH: rec.Header().Get("Critical-Ch"), ContentDPR: rec.Header().Get("Content-Dpr"),
			}
			h.p.mu.Unlock()
			if got.DPR == 1 && tt.want.DPR == 0 {
				got.DPR = 0 // ApplyDefaults turns an unset DPR into 1
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// TestClientHintHeadersOn304 checks that a revalidation carries the same
// Vary and Accept-CH as the 200 (RFC 9110 §15.4.5).
func TestClientHintHeadersOn304(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { hintsOn(h); h.router.mutate = noURLWidth })
	hdr := http.Header{"Sec-Ch-Width": {"800"}}
	first := h.do(t, http.MethodGet, "/x", hdr)
	hdr.Set("If-None-Match", first.Header().Get("ETag"))
	second := h.do(t, http.MethodGet, "/x", hdr)
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	for _, k := range []string{"Vary", "Accept-CH"} {
		if a, b := first.Header().Get(k), second.Header().Get(k); a != b || a == "" {
			t.Errorf("%s: 200 %q, 304 %q", k, a, b)
		}
	}
}

// TestClientHintETag checks that hint-derived values are part of the ETag
// (and so of any cache key built from the canonical request plus the quality
// delta): it changes when a hint changed the result and is identical when
// the hint was overridden by the URL or not consulted.
func TestClientHintETag(t *testing.T) {
	t.Parallel()
	type reqCase struct {
		mutate func(*transform.Request)
		header http.Header
	}
	etagOf := func(t *testing.T, c reqCase) string {
		t.Helper()
		h := newHarness(t, func(h *harness) { hintsOn(h); h.router.mutate = c.mutate })
		rec := h.do(t, http.MethodGet, "/x", c.header)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		return rec.Header().Get("ETag")
	}
	urlDPR := func(r *transform.Request) { r.Pipelines[0].Resize.DPR = 2 }
	urlQuality := func(r *transform.Request) { r.Output.Quality = 80 }

	tests := []struct {
		name string
		a, b reqCase
		same bool
	}{
		{"dpr hint changes the etag", reqCase{nil, nil}, reqCase{nil, http.Header{"Sec-Ch-Dpr": {"2"}}}, false},
		{"width hint changes the etag", reqCase{noURLWidth, nil}, reqCase{noURLWidth, http.Header{"Sec-Ch-Width": {"500"}}}, false},
		{"viewport hint changes the etag", reqCase{noURLWidth, nil}, reqCase{noURLWidth, http.Header{"Viewport-Width": {"500"}}}, false},
		{"save-data changes the etag", reqCase{nil, nil}, reqCase{nil, http.Header{"Save-Data": {"on"}}}, false},
		{"dpr 1 hint equals no hint", reqCase{nil, nil}, reqCase{nil, http.Header{"Sec-Ch-Dpr": {"1"}}}, true},
		{"url dpr overrides the hint", reqCase{urlDPR, nil}, reqCase{urlDPR, http.Header{"Sec-Ch-Dpr": {"3"}}}, true},
		{"url width overrides the hint", reqCase{nil, nil}, reqCase{nil, http.Header{"Sec-Ch-Width": {"900"}}}, true},
		{"url quality overrides save-data", reqCase{urlQuality, nil}, reqCase{urlQuality, http.Header{"Save-Data": {"on"}}}, true},
		{"legacy and sec-ch names agree", reqCase{noURLWidth, http.Header{"Width": {"500"}}}, reqCase{noURLWidth, http.Header{"Sec-Ch-Width": {"500"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, b := etagOf(t, tt.a), etagOf(t, tt.b)
			if (a == b) != tt.same {
				t.Errorf("etags %s and %s: same = %v, want %v", a, b, a == b, tt.same)
			}
		})
	}
}
