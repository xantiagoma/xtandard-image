//go:build cgo

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/ghostscript"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestMain holds one libvips reference for the whole package. Every run()
// takes and releases its own (vips.Init/Shutdown are reference-counted), so
// parallel tests and -count=N never drop the last reference: that would
// call vips_shutdown, which is final for the process.
func TestMain(m *testing.M) {
	// The EPS tests re-execute the test binary as the Ghostscript sandbox
	// helper, as the server re-executes itself.
	if ghostscript.IsSandboxHelper(os.Args) {
		os.Exit(ghostscript.RunSandboxHelper(os.Args))
	}
	if err := vips.Init(new(vips.DefaultConfig())); err != nil {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

// TestConcurrentInstancesShareVips is the regression test for the crash in
// xi_jpegsave/xi_pngsave/xi_probe (GLib-CRITICAL g_hash_table_lookup:
// assertion 'hash_table != NULL', or SIGSEGV in vips_sink_disc): one
// instance's shutdown called vips_shutdown while another instance still
// processed images. Here 200 requests go through instance A while
// instances B start, serve one image and shut down over and over.
func TestConcurrentInstancesShareVips(t *testing.T) {
	t.Parallel()

	const (
		requests = 200
		clients  = 16
		cycles   = 10
	)
	cfg := fixturesConfig(t)
	a := start(t, &cfg, hooks{})
	paths := []string{
		"/insecure/rs:fit:64:64/plain/jpeg/baseline.jpg",
		"/insecure/rs:fit:64:64/plain/png/rgba8.png@jpg",
		"/insecure/rs:fit:64:64/plain/webp/lossy.webp@jpg",
	}

	cyclerDone := make(chan struct{})
	go func() {
		defer close(cyclerDone)
		for i := range cycles {
			if err := serveOnce(t.Context(), &cfg, paths[i%len(paths)]); err != nil {
				t.Errorf("instance B cycle %d: %v", i, err)
				return
			}
		}
	}()

	var sent atomic.Int64
	var wg sync.WaitGroup
	for range clients {
		wg.Go(func() {
			for {
				n := sent.Add(1)
				if n > requests {
					select {
					case <-cyclerDone:
						return
					default:
					}
				}
				if err := fetchJPEG(t.Context(), "http://"+a.public.String()+paths[n%int64(len(paths))]); err != nil {
					t.Errorf("request %d: %v", n, err)
					return
				}
			}
		})
	}
	wg.Wait()
	<-cyclerDone
}

// serveOnce runs a whole service lifecycle (vips.Init … vips.Shutdown) and
// serves one image in between.
func serveOnce(ctx context.Context, cfg *config.Config, path string) error {
	bound := make(chan net.Addr, 1)
	h := hooks{listening: func(p, _ net.Addr) { bound <- p }}
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan error, 1)
	go func() { done <- run(runCtx, cfg, slog.New(slog.DiscardHandler), Options{Version: "test"}, h) }()
	var addr net.Addr
	select {
	case addr = <-bound:
	case err := <-done:
		return fmt.Errorf("run exited early: %w", err)
	}
	ferr := fetchJPEG(ctx, "http://"+addr.String()+path)
	cancel(errors.New("cycle done"))
	return errors.Join(ferr, <-done)
}

// fetchJPEG GETs u and checks for a decodable 200 JPEG. It does not call
// t.Fatal, so client goroutines may use it.
func fetchJPEG(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %d %q", u, resp.StatusCode, body)
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(body)); err != nil {
		return fmt.Errorf("GET %s: decode: %w", u, err)
	}
	return nil
}
