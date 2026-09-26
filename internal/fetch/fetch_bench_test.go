package fetch

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/source"
)

// BenchmarkFetch measures the fetcher's own overhead around a 1 MiB
// original: a cache hit, a coalesced-free miss with a declared length, and a
// miss with an unknown length (chunked growth plus the final exact copy).
func BenchmarkFetch(b *testing.B) {
	data := payload(1 << 20)
	body := func(size int64) fetchFunc {
		return func(context.Context, int, source.Key, source.FetchOptions) (*source.Object, error) {
			return &source.Object{Body: io.NopCloser(bytes.NewReader(data)), Size: size}, nil
		}
	}
	for _, bc := range []struct {
		name string
		cfg  Config
		fn   fetchFunc
	}{
		{"hit", cachedCfg(), body(int64(len(data)))},
		{"miss/known_length", Config{}, body(int64(len(data)))},
		{"miss/unknown_length", Config{}, body(-1)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			f, err := New(&fakeSources{src: &fakeSource{fn: bc.fn}}, GoAllocator, bc.cfg)
			if err != nil {
				b.Fatal(err)
			}
			key := testKey("bench.jpg")
			ctx := b.Context()
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				r, err := f.Fetch(ctx, key, source.FetchOptions{})
				if err != nil {
					b.Fatal(err)
				}
				r.Release()
			}
		})
	}
}

// BenchmarkFetchParallelHit measures cache-hit contention on one key.
func BenchmarkFetchParallelHit(b *testing.B) {
	f, err := New(&fakeSources{src: &fakeSource{fn: serve(payload(64 << 10))}}, GoAllocator, cachedCfg())
	if err != nil {
		b.Fatal(err)
	}
	key := testKey("bench.jpg")
	ctx := b.Context()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r, err := f.Fetch(ctx, key, source.FetchOptions{})
			if err != nil {
				b.Error(err)
				return
			}
			r.Release()
		}
	})
}
