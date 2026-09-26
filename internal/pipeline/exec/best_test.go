//go:build cgo

package exec_test

import (
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// runBest processes a fixture as format=best for a Chrome-like Accept,
// the way the server and internal/app do: negotiate, then Job.Best.
func runBest(t *testing.T, e *exec.Executor, fixture, options string) output {
	t.Helper()
	req := parse(t, options)
	if req.Output.Format != transform.FormatBest {
		t.Fatalf("%q is not a format=best request", options)
	}
	j, buf := job(t, readFixture(t, fixture), req, transform.FormatUnset)
	defer buf.Release()
	cfg := negotiate.DefaultConfig()
	d, err := negotiate.Decide(req, negotiate.ParseAccept("image/avif,image/webp,*/*"), &cfg, negotiate.SourceMeta{
		HasAlpha: j.Meta.HasAlpha, IsAnimated: j.Meta.IsAnimated, FrameCount: j.Meta.Frames,
	})
	if err != nil {
		t.Fatal(err)
	}
	j.Target = encode.NewTarget(&req.Output, &d, j.Meta.IsAnimated)
	j.Best = &d
	res, err := e.Run(t.Context(), j)
	if err != nil {
		t.Fatalf("run %q: %v", options, err)
	}
	defer res.Body.Release()
	data := slices.Clone(res.Body.Bytes())
	img, frames := decode(t, data)
	return output{res: res, img: img, frames: frames, bytes: data}
}

func bestExecutor(t *testing.T, mutate func(*encode.Config)) *exec.Executor {
	t.Helper()
	ec := encode.DefaultConfig()
	if mutate != nil {
		mutate(&ec)
	}
	enc, err := encode.New(&ec)
	if err != nil {
		t.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: testLimits}, enc)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestBest runs format=best through the executor: the processed image is
// classified, the chosen format reaches the Result with its frames, and the
// pixel cap falls back to the negotiated format.
func TestBest(t *testing.T) {
	t.Parallel()
	plain := bestExecutor(t, nil)
	capped := bestExecutor(t, func(c *encode.Config) { c.Best.MaxPixels = 10_000 })
	for _, tc := range []struct {
		name, fixture, options string
		e                      *exec.Executor
		check                  func(t *testing.T, o output)
	}{
		{
			name: "photo", fixture: "jpeg/baseline.jpg", options: "rs:fit:300:300/f:best", e: plain,
			check: func(t *testing.T, o output) {
				t.Helper()
				b := o.res.Best
				if b == nil || b.Chosen < 0 || b.Analysis.Class != negotiate.ContentPhoto || b.Tries[b.Chosen].Candidate.Lossless {
					t.Fatalf("best %+v, want a lossy photo choice", b)
				}
				if o.res.Format != b.Tries[b.Chosen].Candidate.Format || o.res.Saves < len(b.Tries) {
					t.Errorf("format %s in %d saves, tries %+v", o.res.Format, o.res.Saves, b.Tries)
				}
			},
		},
		{
			name: "graphic", fixture: "png/palette.png", options: "f:best", e: plain,
			check: func(t *testing.T, o output) {
				t.Helper()
				b := o.res.Best
				if b == nil || b.Chosen < 0 || b.Analysis.Class != negotiate.ContentGraphic || !b.Tries[b.Chosen].Candidate.Lossless {
					t.Fatalf("best %+v, want a lossless graphic choice", b)
				}
			},
		},
		{
			name: "animation", fixture: "gif/animated.gif", options: "f:best", e: plain,
			check: func(t *testing.T, o output) {
				t.Helper()
				if o.frames != 6 || o.res.Frames != 6 {
					t.Errorf("frames %d/%d, want 6", o.frames, o.res.Frames)
				}
				for _, try := range o.res.Best.Tries {
					if f := try.Candidate.Format; f != transform.FormatWebP && f != transform.FormatGIF {
						t.Errorf("animation tried %s", f)
					}
				}
			},
		},
		{
			name: "pixel cap", fixture: "jpeg/baseline.jpg", options: "rs:fit:300:300/f:best", e: capped,
			check: func(t *testing.T, o output) {
				t.Helper()
				if o.res.Best == nil || o.res.Best.Skipped != encode.BestSkippedPixels ||
					!slices.Contains(o.res.Skipped, encode.BestSkippedPixels) {
					t.Errorf("best %+v, skipped %v", o.res.Best, o.res.Skipped)
				}
				if o.res.Format != transform.FormatAVIF || o.res.Saves != 1 {
					t.Errorf("format %s in %d saves, want the negotiated avif once", o.res.Format, o.res.Saves)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, runBest(t, tc.e, tc.fixture, tc.options))
		})
	}
}
