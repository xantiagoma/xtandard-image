//go:build cgo

package vips_test

import (
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestNestedLifecycleKeepsRunning is the vips-level regression test for the
// app crash (g_hash_table_lookup: assertion 'hash_table != NULL' in
// xi_jpegsave, SIGSEGV in vips_sink_disc): Shutdown used to call
// vips_shutdown unconditionally, so one owner's Shutdown tore libvips down
// under another owner's running jobs. Init/Shutdown pairs now nest, and jobs
// keep working while other owners come and go.
func TestNestedLifecycleKeepsRunning(t *testing.T) {
	t.Parallel()

	const (
		workers = 8
		jobs    = 50
		cycles  = 200
	)
	data := readFixture(t, "jpeg/baseline.jpg")
	stop := make(chan struct{})
	var cyclers sync.WaitGroup
	cyclers.Go(func() {
		for range cycles {
			select {
			case <-stop:
				return
			default:
			}
			if err := vips.Init(new(vips.DefaultConfig())); err != nil {
				t.Errorf("nested Init: %v", err)
				return
			}
			vips.Shutdown()
		}
	})

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range jobs {
				buf := newBuffer(t, data)
				_, err := thumbnailJPEG(t.Context(), buf, 64, 64)
				buf.Release()
				if err != nil {
					t.Errorf("job: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	cyclers.Wait()
}

// TestFinalShutdownWaitsForRuns releases the last reference while a Run is
// in progress: Shutdown must refuse new Runs, wait for the running one and
// only then call vips_shutdown; afterwards Init and Run fail instead of
// using a torn-down libvips. vips_shutdown is final for the process, so the
// scenario runs in a child process (finalShutdownScenario).
func TestFinalShutdownWaitsForRuns(t *testing.T) {
	t.Parallel()
	if os.Getenv(finalShutdownEnv) == "1" {
		finalShutdownScenario(t)
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], //nolint:gosec // re-executes this test binary
		"-test.run=^TestFinalShutdownWaitsForRuns$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), finalShutdownEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

const finalShutdownEnv = "XI_TEST_VIPS_FINAL_SHUTDOWN"

func finalShutdownScenario(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	noop := func() error { return nil }

	// A nested pair leaves TestMain's reference in place.
	if err := vips.Init(new(vips.DefaultConfig())); err != nil {
		t.Fatalf("nested Init: %v", err)
	}
	vips.Shutdown()
	if err := vips.Run(ctx, noop); err != nil {
		t.Fatalf("Run after a nested Shutdown: %v", err)
	}

	data := readFixture(t, "jpeg/baseline.jpg")
	entered, release := make(chan struct{}), make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- vips.Run(ctx, func() error {
			close(entered)
			<-release
			buf := newBuffer(t, data)
			defer buf.Release()
			img, err := vips.LoadThumbnail(ctx, buf, 64, 64)
			if err != nil {
				return err
			}
			defer img.Close()
			blob, err := img.Save(ctx, vips.FormatJPEG, new(vips.DefaultSaveOptions()))
			if err != nil {
				return err
			}
			blob.Release()
			return nil
		})
	}()
	<-entered

	// Release TestMain's reference, the last one.
	shutDone := make(chan struct{})
	go func() {
		vips.Shutdown()
		close(shutDone)
	}()
	// New Runs are refused as soon as the final Shutdown has started.
	for vips.Run(ctx, noop) == nil {
		runtime.Gosched()
	}
	select {
	case <-shutDone:
		t.Fatal("Shutdown returned while a Run was in progress")
	default:
	}
	close(release)
	if err := <-runErr; err != nil {
		t.Errorf("in-flight Run: %v", err)
	}
	<-shutDone

	if err := vips.Init(new(vips.DefaultConfig())); err == nil {
		t.Error("Init after the final Shutdown succeeded; libvips cannot be restarted")
	}
	if err := vips.Run(ctx, noop); err == nil {
		t.Error("Run after the final Shutdown succeeded")
	}
}

// TestNestedInitRejectsOtherSettings: only the first Init applies settings
// (re-applying the loader allowlist would briefly block every loader under
// running jobs), so a nested Init asking for different ones fails and takes
// no reference.
func TestNestedInitRejectsOtherSettings(t *testing.T) {
	t.Parallel()
	cfg := vips.DefaultConfig()
	cfg.HEICEncode = true
	if err := vips.Init(&cfg); err == nil {
		vips.Shutdown()
		t.Fatal("nested Init with different settings succeeded")
	}
}
