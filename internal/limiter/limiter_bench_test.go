package limiter

import (
	"runtime"
	"testing"
	"time"
)

func BenchmarkAcquireRelease(b *testing.B) {
	cases := []struct {
		name string
		cfg  Config
		cost int64
	}{
		{"workers", Config{Workers: 4, QueueSize: 64, QueueTimeout: 5 * time.Second}, 0},
		{"workers_memory", Config{Workers: 4, QueueSize: 64, QueueTimeout: 5 * time.Second, MemoryBudget: 1 << 30}, 1 << 20},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			l, err := New(tc.cfg)
			if err != nil {
				b.Fatal(err)
			}
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				release, err := l.Acquire(ctx, tc.cost)
				if err != nil {
					b.Fatal(err)
				}
				release()
			}
		})
	}
}

// BenchmarkReserve measures Reserve without a budget (the default: it must
// not allocate) and with one.
func BenchmarkReserve(b *testing.B) {
	for _, tc := range []struct {
		name   string
		budget int64
	}{{"no_budget", 0}, {"budget", 1 << 30}} {
		b.Run(tc.name, func(b *testing.B) {
			l, err := New(Config{Workers: 4, QueueSize: 64, QueueTimeout: 5 * time.Second, MemoryBudget: tc.budget})
			if err != nil {
				b.Fatal(err)
			}
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				release, err := l.Reserve(ctx, 1<<20)
				if err != nil {
					b.Fatal(err)
				}
				release()
			}
		})
	}
}

// BenchmarkAcquireReleaseParallel measures contention with GOMAXPROCS
// workers and as many goroutines competing for them.
func BenchmarkAcquireReleaseParallel(b *testing.B) {
	workers := runtime.GOMAXPROCS(0)
	l, err := New(Config{Workers: workers, QueueSize: workers * 16, QueueTimeout: 5 * time.Second})
	if err != nil {
		b.Fatal(err)
	}
	ctx := b.Context()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			release, err := l.Acquire(ctx, 0)
			if err != nil {
				b.Error(err)
				return
			}
			release()
		}
	})
}
