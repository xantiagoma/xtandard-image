//go:build cgo

package exec_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// timing is the animation metadata of an encoded image as libvips reads it
// back.
type timing struct {
	Frames  int
	Delay   []int // ms per frame; nil when absent
	Loop    int
	HasLoop bool
}

func readTiming(tb testing.TB, data []byte) timing {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var tm timing
	err := vips.Run(tb.Context(), func() error {
		img, err := vips.Load(tb.Context(), buf, &vips.LoadOptions{NPages: -1})
		if err != nil {
			return err
		}
		defer img.Close()
		tm.Frames = img.Pages()
		tm.Delay, _ = img.Ints(vips.FieldDelay)
		tm.Loop, tm.HasLoop = img.Int(vips.FieldLoop)
		return nil
	})
	if err != nil {
		tb.Fatalf("read timing: %v", err)
	}
	return tm
}

// TestAnimationTiming checks that frame count, per-frame delays and the loop
// count survive every per-frame operation and a format change, read back
// from the encoded output.
func TestAnimationTiming(t *testing.T) {
	t.Parallel()
	for _, src := range []string{gifAnim, webpAnim} {
		want := readTiming(t, readFixture(t, src))
		if want.Frames != 6 || len(want.Delay) != 6 || !want.HasLoop {
			t.Fatalf("%s: fixture timing %+v", src, want)
		}
		for _, out := range []transform.Format{transform.FormatGIF, transform.FormatWebP} {
			for _, options := range []string{
				"rs:fill:48:48",
				"c:64:48/rs:fit:40:40/ex:1/bg:255:255:255",
				"rs:fit:40:40/exar:1:so",
				"rs:fit:32:32/pd:2:4",
				"rs:fit:32:32/rot:90/fl:1:0",
			} {
				t.Run(src+"/"+out.String()+"/"+options, func(t *testing.T) {
					t.Parallel()
					o := process(t, src, options, out)
					got := readTiming(t, o.bytes)
					if diff := cmp.Diff(want, got); diff != "" {
						t.Errorf("timing (-source +output):\n%s", diff)
					}
					if o.res.Frames != want.Frames {
						t.Errorf("Result.Frames = %d, want %d", o.res.Frames, want.Frames)
					}
				})
			}
		}
	}
}

// TestDisableAnimation: disable_animation encodes the first frame as a still
// image, without animation metadata, even in a format that can animate.
func TestDisableAnimation(t *testing.T) {
	t.Parallel()
	for _, src := range []string{gifAnim, webpAnim} {
		t.Run(src, func(t *testing.T) {
			t.Parallel()
			first := process(t, src, "rs:fit:48:48", transform.FormatPNG) // PNG cannot animate: frame 0
			o := process(t, src, "rs:fit:48:48/da:1", transform.FormatWebP)
			if tm := readTiming(t, o.bytes); tm.Frames != 1 || tm.Delay != nil || tm.HasLoop {
				t.Errorf("timing = %+v, want one frame without animation metadata", tm)
			}
			r, err := ssim.Compare(first.img, o.img)
			if err != nil {
				t.Fatal(err)
			}
			if r.DSSIM > golden.DefaultTolerance {
				t.Errorf("not the first frame: dssim %.5f", r.DSSIM)
			}
		})
	}
}

// TestMaxAnimationFrames: XI_LIMITS_MAX_ANIMATION_FRAMES and the request's
// max_animation_frames bound the frames processed (imgproxy loads the first
// n); 1 disables animation. The delays of the kept frames are preserved.
func TestMaxAnimationFrames(t *testing.T) {
	t.Parallel()
	src := readFixture(t, gifAnim)
	all := readTiming(t, src)
	for _, tc := range []struct {
		name      string
		cfgFrames int // executor limit (0 = testLimits)
		reqFrames int // request override (lowers only)
		want      int
	}{
		{name: "config 3", cfgFrames: 3, want: 3},
		{name: "config 1", cfgFrames: 1, want: 1},
		{name: "request 2", reqFrames: 2, want: 2},
		{name: "request 1", reqFrames: 1, want: 1},
		{name: "request above config", cfgFrames: 3, reqFrames: 5, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lim := testLimits
			if tc.cfgFrames > 0 {
				lim.MaxAnimationFrames = tc.cfgFrames
			}
			e := executorWithLimits(t, &lim)
			req := parse(t, "rs:fill:40:40")
			req.Limits.MaxAnimationFrames = tc.reqFrames
			o := processRequest(t, e, src, req, transform.FormatGIF)
			got := readTiming(t, o.bytes)
			if got.Frames != tc.want || o.res.Frames != tc.want {
				t.Fatalf("frames = %d (result %d), want %d", got.Frames, o.res.Frames, tc.want)
			}
			if tc.want > 1 {
				if diff := cmp.Diff(all.Delay[:tc.want], got.Delay); diff != "" {
					t.Errorf("delays (-want +got):\n%s", diff)
				}
				if got.Loop != all.Loop {
					t.Errorf("loop = %d, want %d", got.Loop, all.Loop)
				}
			}
			golden.Compare(t, "exec/gif-max-frames-"+strings.ReplaceAll(tc.name, " ", "-"), o.img, golden.Options{})
		})
	}
}

func executorWithLimits(tb testing.TB, lim *plan.Limits) *exec.Executor {
	tb.Helper()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: *lim}, enc)
	if err != nil {
		tb.Fatal(err)
	}
	return e
}
