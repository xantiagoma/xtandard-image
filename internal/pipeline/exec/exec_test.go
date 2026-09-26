//go:build cgo

package exec_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func TestNewNeedsEncoder(t *testing.T) {
	t.Parallel()
	if _, err := exec.New(&exec.Config{}, nil); err == nil {
		t.Error("New(nil encoder) succeeded")
	}
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	photo := readFixture(t, jpegPhoto)
	truncated := photo[:len(photo)/2]

	for _, tc := range []struct {
		name    string
		data    []byte
		options string
		limits  plan.Limits
		ctx     func(context.Context) context.Context
		want    errs.Kind
	}{
		{name: "truncated source", data: truncated, options: "rs:fit:100:100", limits: testLimits, want: errs.KindCorrupt},
		{name: "source over max pixels", data: photo, options: "rs:fit:100:100", limits: plan.Limits{MaxSrcPixels: 1000}, want: errs.KindLimitExceeded},
		{name: "memory budget", data: photo, options: "rs:fit:100:100", limits: plan.Limits{MemoryBudget: 1000}, want: errs.KindLimitExceeded},
		{
			name: "canceled", data: photo, options: "rs:fit:100:100", limits: testLimits, want: errs.KindCanceled,
			ctx: func(ctx context.Context) context.Context {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ec := encode.DefaultConfig()
			enc, err := encode.New(&ec)
			if err != nil {
				t.Fatal(err)
			}
			e, err := exec.New(&exec.Config{Limits: tc.limits}, enc)
			if err != nil {
				t.Fatal(err)
			}
			j, buf := jobMeta(t, photo, tc.data, parse(t, tc.options), transform.FormatJPEG)
			defer buf.Release()
			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}
			res, err := e.Run(ctx, j)
			if err == nil {
				res.Body.Release()
				t.Fatal("Run succeeded")
			}
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("kind = %v, want %v (%v)", got, tc.want, err)
			}
		})
	}
}

func TestSkippedOptions(t *testing.T) {
	t.Parallel()
	// blur, sharpen and crop_aspect_ratio (WP-60) are all applied.
	o := process(t, jpegPhoto, "rs:fit:64:64/bl:2/sh:1/car:1.5", transform.FormatJPEG)
	got := slices.Sorted(slices.Values(o.res.Skipped))
	if diff := cmp.Diff([]string(nil), got); diff != "" {
		t.Errorf("Skipped (-want +got):\n%s", diff)
	}
}

func TestResultMetadata(t *testing.T) {
	t.Parallel()
	o := process(t, jpegPhoto, "rs:fit:80:80/dpr:2", transform.FormatWebP)
	r := o.res
	if r.Format != transform.FormatWebP || r.ContentType != "image/webp" || r.DPR != 2 || r.Quality == 0 {
		t.Errorf("result = %+v", *r)
	}
}

// TestAnimationKept checks that animations keep every frame, the delays and
// the loop count through resize and crops.
func TestAnimationKept(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture, options string
		out              transform.Format
		frames           int
	}{
		{gifAnim, "c:64:48/rs:fill:40:40", transform.FormatGIF, 6},
		{webpAnim, "rs:fit:32:32/pd:2", transform.FormatWebP, 6},
		{gifAnim, "rs:fit:32:32", transform.FormatPNG, 1},               // PNG cannot animate
		{gifAnim, "rs:fit:32:32/da:1", transform.FormatGIF, 1},          // disable_animation
		{webpAnim, "rs:fill:32:32/g:sm", transform.FormatWebP, 6},       // smart crop falls back to centre
		{gifAnim, "rs:fit:32:32/rot:90/fl:1:1", transform.FormatGIF, 6}, // per-frame orientation
	} {
		t.Run(tc.fixture+"/"+tc.options, func(t *testing.T) {
			t.Parallel()
			o := process(t, tc.fixture, tc.options, tc.out)
			if o.frames != tc.frames || o.res.Frames != tc.frames {
				t.Errorf("frames = %d (result %d), want %d", o.frames, o.res.Frames, tc.frames)
			}
		})
	}
}

// TestConcurrentRuns shares one Executor and one source per format between
// goroutines, as the server does with cached originals.
func TestConcurrentRuns(t *testing.T) {
	t.Parallel()
	e := newExecutor(t)
	var wg sync.WaitGroup
	for i, f := range []string{jpegPhoto, "bmp/rgb24.bmp", pngAlpha, gifAnim} {
		out := transform.FormatJPEG
		if i%2 == 1 {
			out = transform.FormatPNG
		}
		j, buf := job(t, readFixture(t, f), parse(t, "rs:fill:40:40"), out)
		t.Cleanup(buf.Release)
		for range 2 {
			wg.Go(func() {
				for range 50 {
					res, err := e.Run(t.Context(), j)
					if err != nil {
						t.Error(err)
						return
					}
					res.Body.Release()
				}
			})
		}
	}
	wg.Wait()
}
