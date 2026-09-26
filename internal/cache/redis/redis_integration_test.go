//go:build integration

// Integration tests against the Valkey service of compose.test.yml
// (`task test:integration`); Valkey is the BSD-3 fork of Redis 7.2 and
// speaks the same protocol. They skip when the address is unset
// (go-standards §8):
//
//	XI_TEST_REDIS_ADDR   e.g. valkey:6379 (in compose) or 127.0.0.1:<port>
package redis

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/cachetest"
)

func redisURL(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("XI_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("XI_TEST_REDIS_ADDR not set; run `task test:integration`")
	}
	return "redis://" + addr + "/0"
}

// newStore returns a store with a prefix of its own, so parallel tests
// sharing the server never see each other's keys.
func newStore(t *testing.T, opts *Options) *Store {
	t.Helper()
	opts.URL = redisURL(t)
	opts.Prefix = "xi-test:" + rand.Text() + ":"
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() }) // idempotent
	return s
}

func TestIntegrationRedisConformance(t *testing.T) {
	t.Parallel()
	redisURL(t)
	cachetest.Run(t, func(t *testing.T, now func() time.Time) cache.Store {
		t.Helper()
		return newStore(t, &Options{Now: now})
	})
}

// TestIntegrationRedisServerTTL checks that the server itself expires
// entries: SET … PX leaves a PTTL of at most the TTL, and no TTL for 0.
func TestIntegrationRedisServerTTL(t *testing.T) {
	t.Parallel()
	s := newStore(t, &Options{})
	cachetest.Set(t, s, cache.NSResult, "v1/px", []byte("px"), nil, 90*time.Second)
	cachetest.Set(t, s, cache.NSResult, "v1/sub", []byte("sub"), nil, 1500*time.Millisecond)
	cachetest.Set(t, s, cache.NSResult, "v1/none", []byte("none"), nil, 0)

	raw := goredis.NewClient(&goredis.Options{Addr: os.Getenv("XI_TEST_REDIS_ADDR")})
	defer raw.Close()
	for _, tc := range []struct {
		key      string
		min, max time.Duration
	}{
		{"v1/px", 80 * time.Second, 90 * time.Second},
		{"v1/sub", time.Millisecond, 1500 * time.Millisecond},
		{"v1/none", -1, -1}, // PTTL -1: no expiry
	} {
		d, err := raw.PTTL(t.Context(), s.redisKey(string(cache.NSResult)+tc.key)).Result()
		if err != nil {
			t.Fatalf("PTTL %s: %v", tc.key, err)
		}
		if d < tc.min || d > tc.max {
			t.Errorf("PTTL %s = %s, want in [%s, %s]", tc.key, d, tc.min, tc.max)
		}
	}
}

func TestIntegrationRedisOversize(t *testing.T) {
	t.Parallel()
	m := &skipRec{}
	s := newStore(t, &Options{Metrics: m}) // default 1 MiB limit
	big := bytes.Repeat([]byte{1}, DefaultMaxValueBytes+1)
	cachetest.Set(t, s, cache.NSResult, "v1/big", big, nil, time.Hour)
	if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/big"); ok {
		t.Error("value over 1 MiB stored")
	}
	if got := m.skipped(cache.SkipTooLarge); got != 1 {
		t.Errorf("too_large skips %d, want 1", got)
	}
	fits := bytes.Repeat([]byte{2}, DefaultMaxValueBytes-4096)
	cachetest.Set(t, s, cache.NSResult, "v1/fits", fits, http.Header{"Content-Type": {"image/png"}}, time.Hour)
	if _, got, ok := cachetest.Get(t, s, cache.NSResult, "v1/fits"); !ok || !bytes.Equal(got, fits) {
		t.Errorf("value under the limit: ok=%v, %d bytes", ok, len(got))
	}
}

func TestIntegrationRedisCorrupt(t *testing.T) {
	t.Parallel()
	m := &skipRec{}
	s := newStore(t, &Options{Metrics: m})
	raw := goredis.NewClient(&goredis.Options{Addr: os.Getenv("XI_TEST_REDIS_ADDR")})
	defer raw.Close()
	if err := raw.Set(t.Context(), s.redisKey(string(cache.NSResult)+"v1/junk"), "not a frame", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cachetest.Get(t, s, cache.NSResult, "v1/junk"); ok {
		t.Error("foreign value served")
	}
	if got := m.skipped(cache.SkipCorrupt); got != 1 {
		t.Errorf("corrupt skips %d, want 1", got)
	}
}

func TestIntegrationRedisUnreachableIsError(t *testing.T) {
	t.Parallel()
	redisURL(t)
	s, err := New(&Options{URL: "redis://127.0.0.1:1/0?dial_timeout=100ms&max_retries=-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok, err := s.Get(t.Context(), cache.NSResult, "v1/x"); ok || err == nil {
		t.Errorf("Get on a dead server: ok=%v err=%v, want a miss with an error", ok, err)
	}
}
