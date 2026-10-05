package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
)

// TestShutdownDrains proves the shutdown order: readiness flips to
// draining, the listener stops accepting new connections, the in-flight
// request still completes, and only then are sources closed and vips shut
// down.
func TestShutdownDrains(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(e string) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	entered := make(chan struct{})
	unblock := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-unblock
		record("request done")
		_, _ = io.WriteString(w, "drained")
	})

	ready := admin.NewReadiness()
	bound := make(chan net.Addr, 1)
	shutdownStarted := make(chan struct{})
	lc := &lifecycle{
		logger: slog.New(slog.DiscardHandler),
		ready:  ready,
		// A loopback address of its own (Linux routes all of 127/8): the
		// dial below proves the closed listener refuses connections, and
		// on 127.0.0.1 a listener of a parallel test or package that the
		// kernel gave the freed port would accept it instead.
		publicAddr: "127.0.0.119:0",
		public: func(ctx context.Context) *http.Server {
			return &http.Server{
				Handler: handler, ReadHeaderTimeout: time.Minute,
				BaseContext: func(net.Listener) context.Context { return ctx },
			}
		},
		shutdownTimeout: time.Minute,
		listening:       func(p, _ net.Addr) { bound <- p },
		onShutdown:      func() { close(shutdownStarted) },
		afterDrain: []func(context.Context) error{
			func(context.Context) error { record("close sources"); return nil },
			func(context.Context) error { record("vips shutdown"); return nil },
		},
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	done := make(chan error, 1)
	go func() { done <- lc.run(ctx) }()
	addr := (<-bound).String()
	if !ready.IsReady() {
		t.Fatal("not ready after listening")
	}

	// A request in flight when SIGTERM arrives.
	type result struct {
		status int
		body   string
		err    error
	}
	resp := make(chan result, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodGet, "http://"+addr+"/slow", http.NoBody)
		r, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			resp <- result{err: err}
			return
		}
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		resp <- result{status: r.StatusCode, body: string(b), err: err}
	}()
	<-entered

	cancel(errors.New("SIGTERM"))
	<-shutdownStarted // listeners are closed before the shutdown hooks run
	if ready.State() != admin.Draining {
		t.Errorf("readiness = %v, want draining", ready.State())
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	if c, err := d.DialContext(t.Context(), "tcp", addr); err == nil {
		_ = c.Close()
		t.Error("new connection accepted while draining")
	}
	select {
	case err := <-done:
		t.Fatalf("run returned before the in-flight request finished: %v", err)
	default:
	}

	close(unblock)
	got := <-resp
	if got.err != nil || got.status != http.StatusOK || got.body != "drained" {
		t.Errorf("in-flight request: %d %q %v; want 200 drained", got.status, got.body, got.err)
	}
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if diff := cmp.Diff([]string{"request done", "close sources", "vips shutdown"}, events); diff != "" {
		t.Errorf("shutdown order (-want +got):\n%s", diff)
	}
}

// TestListenFailureStillCleansUp: a port that cannot be bound fails Run
// and still runs the cleanup hooks.
func TestListenFailureStillCleansUp(t *testing.T) {
	t.Parallel()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	cleaned := false
	lc := &lifecycle{
		logger:     slog.New(slog.DiscardHandler),
		ready:      admin.NewReadiness(),
		publicAddr: ln.Addr().String(),
		public:     func(context.Context) *http.Server { return &http.Server{ReadHeaderTimeout: time.Second} },
		afterDrain: []func(context.Context) error{func(context.Context) error { cleaned = true; return nil }},
	}
	if err := lc.run(t.Context()); err == nil {
		t.Fatal("want a listen error")
	}
	if !cleaned {
		t.Error("cleanup hooks did not run")
	}
}
