//go:build cgo

package vips_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestConcurrentJobs runs 8 goroutines × 200 full jobs over shared,
// refcounted source Buffers (as coalesced requests do). CI runs it with
// -race and GOEXPERIMENT=cgocheck2.
func TestConcurrentJobs(t *testing.T) {
	t.Parallel()

	const (
		workers = 8
		jobs    = 200
	)
	fixtures := []string{"jpeg/baseline.jpg", "png/rgba8.png", "webp/lossy.webp", "gif/animated.gif"}
	shared := make([]*vips.Buffer, len(fixtures))
	for i, f := range fixtures {
		shared[i] = newBuffer(t, readFixture(t, f))
	}
	formats := []vips.Format{vips.FormatJPEG, vips.FormatPNG, vips.FormatWebP}

	var wg sync.WaitGroup
	errc := make(chan error, workers)
	for w := range workers {
		wg.Go(func() {
			for j := range jobs {
				buf := shared[(w+j)%len(shared)]
				buf.Retain()
				err := tryJob(t.Context(), func(ctx context.Context, a *vips.Arena) error {
					// Release as soon as the image owns its reference.
					img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Access: vips.AccessSequential}))
					buf.Release()
					if err != nil {
						return err
					}
					if _, err := vips.Probe(ctx, buf); err != nil {
						return err
					}
					small, err := a.Add(img.Resize(ctx, 0.25+0.05*float64(j%5), 0, vips.KernelLanczos3))
					if err != nil {
						return err
					}
					_, err = saveBytes(ctx, small, formats[j%len(formats)], nil)
					return err
				})
				if err != nil {
					errc <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
	for _, b := range shared {
		b.Release()
	}
}

// TestBufferConcurrentRefcount retains and releases one Buffer from many
// goroutines; the memory must survive until the last Release.
func TestBufferConcurrentRefcount(t *testing.T) {
	t.Parallel()

	buf, err := vips.NewBuffer(4)
	if err != nil {
		t.Fatal(err)
	}
	copy(buf.Bytes(), "abcd")
	var wg sync.WaitGroup
	for range 16 {
		buf.Retain()
		wg.Go(func() {
			defer buf.Release()
			for range 1000 {
				buf.Retain()
				if string(buf.Bytes()) != "abcd" {
					t.Error("bytes changed while referenced")
				}
				buf.Release()
			}
		})
	}
	wg.Wait()
	buf.Release()
	if buf.Bytes() != nil {
		t.Error("Bytes after the final Release is not nil")
	}
}

// TestKillOnCancel cancels a sink that would run for minutes (a gaussian
// blur of a 12000×12000 image with a wide kernel on one libvips thread)
// and checks that it stops within the tile granularity instead. Not
// parallel: it would slow the other tests down while it runs.
func TestKillOnCancel(t *testing.T) {
	const timeout = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	start := time.Now()
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		black, err := a.Add(vips.Black(ctx, 12000, 12000, 3))
		if err != nil {
			return err
		}
		blurred, err := a.Add(black.Gaussblur(ctx, 40))
		if err != nil {
			return err
		}
		_, err = saveBytes(ctx, blurred, vips.FormatJPEG, nil)
		return err
	})
	elapsed := time.Since(start)
	if errs.KindOf(err) != errs.KindTimeout {
		t.Fatalf("error = %v (kind %v), want KindTimeout", err, errs.KindOf(err))
	}
	// A tile row of this pipeline takes milliseconds; the whole image
	// minutes. Allow generous scheduling slack for -race.
	if elapsed > timeout+5*time.Second {
		t.Errorf("save stopped %v after start, want shortly after the %v deadline", elapsed, timeout)
	}
	t.Logf("killed after %v (deadline %v)", elapsed, timeout)
}

// TestKillOnCancelStop disarms the watcher: an image whose kill was never
// triggered (or was cleared) keeps working.
func TestKillOnCancelStop(t *testing.T) {
	t.Parallel()

	job(t, func(ctx context.Context, a *vips.Arena) error {
		img, err := a.Add(vips.Black(ctx, 64, 64, 3))
		if err != nil {
			return err
		}
		cctx, cancel := context.WithCancel(ctx)
		stop := vips.KillOnCancel(cctx, img)
		cancel() // fires the kill (asynchronously)
		stop()   // waits for it and clears the flag
		// img is usable again under a live context.
		_, err = img.WriteToMemory(ctx)
		if err != nil {
			return err
		}
		vips.KillOnCancel(context.WithoutCancel(ctx), img)() // no Done channel: no-op
		return nil
	})
}
