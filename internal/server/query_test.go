package server

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
)

// keyRecorder is a result cache that records the key of every lookup.
type keyRecorder struct {
	cache.Store

	mu   sync.Mutex
	keys []string
}

func (k *keyRecorder) Get(ctx context.Context, ns cache.Namespace, key string) (cache.Entry, bool, error) {
	k.mu.Lock()
	k.keys = append(k.keys, key)
	k.mu.Unlock()
	return k.Store.Get(ctx, ns, key)
}

func (k *keyRecorder) last() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.keys) == 0 {
		return ""
	}
	return k.keys[len(k.keys)-1]
}

// queryHarness is a harness with the real router: the query dialect at /q/
// and imgproxy at /, a result cache that records its keys and the given
// unsigned policy.
type queryHarness struct {
	*harness
	keys  *keyRecorder
	store *cache.AsyncStore
}

func newQueryHarness(t *testing.T, opts *query.Options, policy router.Policy, mutate func(*harness)) *queryHarness {
	t.Helper()
	mem, err := memory.New(memory.Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewAsync(mem, cache.AsyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	keys := &keyRecorder{Store: store}
	h := newHarness(t, func(h *harness) {
		h.cache = keys
		h.cfg.DebugHeaders = true
		h.cfg.ResultCache = ResultCacheConfig{TTL: time.Hour, Build: "test"}
		if mutate != nil {
			mutate(h)
		}
	})
	qp, err := query.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := router.New(&router.Options{
		Policy:       policy,
		Restrictions: router.Restrictions{Widths: []int{64}},
		Mounts: []router.Mount{
			{Name: "query", Dialect: query.Name, Prefix: "/q/"},
			{Name: "imgproxy", Dialect: imgproxy.Name, Prefix: "/"},
		},
	}, map[string]dialect.Dialect{query.Name: qp, imgproxy.Name: ip})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(&h.cfg, &Deps{
		Router: rt, Resolver: h.res, Fetcher: h.f, Admitter: h.a, Prober: h.pr, Processor: h.p, Sniffer: h.sniffer,
		Allocator:   func(n int) (fetch.Buffer, error) { return fetch.NewGoBuffer(n, nil), nil },
		Metrics:     h.m,
		Logger:      h.logger,
		Readiness:   admin.NewReadiness(),
		ResultCache: keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	return &queryHarness{harness: h, keys: keys, store: store}
}

// TestResultKeysPinned pins the result cache keys of GET requests as
// origin/main computed them before WP-147 (QUERY support): QUERY maps onto
// the GET key, never the other way round, so these must never change.
func TestResultKeysPinned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, target, accept, want string
	}{
		{
			name: "query native", target: "/q/?url=http://origin/a.jpg&w=64&h=48&fit=cover", accept: "image/webp,*/*",
			want: "v1/e03fa37150a5108049ad382ad460081fa8365b6574879d2bd7bc595583e7a87b",
		},
		{
			name: "query fixed format", target: "/q/?url=http://origin/a.jpg&w=64&f=png",
			want: "v1/03d61d63ebe2baef2300d1dc3b84c8bd3adcfe55a757e19d14f180a40c944b4d",
		},
		{
			name: "query o chain", target: "/q/?url=http://origin/a.jpg&o=rs:fill:64:48/bl:2",
			want: "v1/f91122a334ca82913ea0af03a5ede081667d8e1621f6693550dda978f79e86a8",
		},
		{
			name: "query imaginary", target: "/q/resize?url=http://origin/a.jpg&width=64&type=webp",
			want: "v1/2e6bd105e5d3fb654ceda73b3f042ce1cbb2347d4651cf4bb54fe47f7542243a",
		},
		{
			name: "imgproxy", target: "/insecure/rs:fit:64:64/plain/http://origin/a.jpg", accept: "image/avif,image/webp,*/*",
			want: "v1/6f6fbb970b8e2771f3f579e7bfde3acd8c2797a0900bae46ab2a613699f0e7f3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newQueryHarness(t, nil, router.PolicyAllow, nil)
			hdr := http.Header{}
			if tc.accept != "" {
				hdr.Set("Accept", tc.accept)
			}
			rec := h.do(t, http.MethodGet, tc.target, hdr)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if diff := cmp.Diff(tc.want, h.keys.last()); diff != "" {
				t.Errorf("result key (-want +got):\n%s", diff)
			}
		})
	}
}
