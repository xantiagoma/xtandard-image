//go:build cgo

package exec_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// HEIF/AVIF image sequences (WP-104, testdata/gen/heifseq): 4 frames of
// 64×48, frame i a palette colour with a white square at x = 4 + 12i,
// lasting (i + 1) × 100 ms.
const (
	seqAVIF   = "avif/sequence.avif"
	seqAlpha  = "avif/sequence-alpha.avif" // half-transparent left half, plays twice
	seqHEIC   = "heic/sequence.heic"
	seqColors = "avif/libavif-colors-animated-8bpc.avif" // libavif: 5 frames of 150×150 at 30 fps
)

// TestSequenceAnimates: a sequence stays an animation for animated outputs,
// with its frames, delays and loop count, through per-frame geometry.
func TestSequenceAnimates(t *testing.T) {
	t.Parallel()
	seq := timing{Frames: 4, Delay: []int{100, 200, 300, 400}, Loop: 0, HasLoop: true}
	for _, tc := range []struct {
		fixture, options string
		out              transform.Format
		want             timing
		w, h             int
		golden           string
	}{
		{seqAVIF, "rs:fit:32:0", transform.FormatWebP, seq, 32, 24, "avif-sequence-webp"},
		{seqAVIF, "rs:fill:24:24/rot:90", transform.FormatGIF, seq, 24, 24, ""},
		{seqHEIC, "rs:fit:32:0", transform.FormatWebP, seq, 32, 24, "heic-sequence-webp"},
		{seqAlpha, "rs:fit:32:0", transform.FormatWebP, timing{Frames: 4, Delay: seq.Delay, Loop: 2, HasLoop: true}, 32, 24, "avif-sequence-alpha-webp"},
		{seqColors, "rs:fit:50:0", transform.FormatWebP, timing{Frames: 5, Delay: []int{33, 33, 33, 33, 33}, HasLoop: true, Loop: 1}, 50, 50, "avif-colors-animated-webp"},
	} {
		t.Run(tc.fixture+"/"+tc.out.String()+"/"+tc.options, func(t *testing.T) {
			t.Parallel()
			o := process(t, tc.fixture, tc.options, tc.out)
			got := readTiming(t, o.bytes)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("timing (-want +got):\n%s", diff)
			}
			checkOutput(t, &goldenCase{out: tc.out, w: tc.w, h: tc.h, frames: tc.want.Frames}, &o)
			if tc.golden != "" {
				golden.Compare(t, "exec/"+tc.golden, o.img, golden.Options{})
			}
		})
	}
}

// TestSequenceStill: a still output is the first frame, or the frame page
// selects; disable_animation and a frame limit of 1 give the first frame.
func TestSequenceStill(t *testing.T) {
	t.Parallel()
	first := process(t, seqAVIF, "", transform.FormatPNG)
	checkOutput(t, &goldenCase{out: transform.FormatPNG, w: 64, h: 48}, &first)
	golden.Compare(t, "exec/avif-sequence-first", first.img, golden.Options{})
	last := process(t, seqAVIF, "pg:3", transform.FormatPNG)
	golden.Compare(t, "exec/avif-sequence-last", last.img, golden.Options{})
	for _, options := range []string{"da:1", "pg:0"} {
		o := process(t, seqAVIF, options, transform.FormatWebP)
		if tm := readTiming(t, o.bytes); tm.Frames != 1 && options == "da:1" {
			t.Errorf("%s: %d frames, want 1", options, tm.Frames)
		}
		frame := o
		if o.frames > 1 {
			continue // pg:0 keeps the animation (page is its first frame)
		}
		r, err := ssim.Compare(first.img, frame.img)
		if err != nil {
			t.Fatal(err)
		}
		if r.DSSIM > 0.01 {
			t.Errorf("%s: not the first frame (dssim %.4f)", options, r.DSSIM)
		}
	}
	// pages stacks frames 1 and 2, loaded as one strip.
	stack := process(t, seqAVIF, "pg:1/pgs:2", transform.FormatPNG)
	checkOutput(t, &goldenCase{out: transform.FormatPNG, w: 64, h: 96}, &stack)
	golden.Compare(t, "exec/avif-sequence-pages", stack.img, golden.Options{})
	// The HEIC sequence (no still item at all) renders its first frame.
	heic := process(t, seqHEIC, "", transform.FormatPNG)
	r, err := ssim.Compare(first.img, heic.img)
	if err != nil {
		t.Fatal(err)
	}
	if r.DSSIM > 0.01 {
		t.Errorf("HEIC first frame differs from the AVIF one: dssim %.4f", r.DSSIM)
	}
}

// TestSequenceMaxFrames: XI_LIMITS_MAX_ANIMATION_FRAMES bounds the frames
// decoded, as for GIF.
func TestSequenceMaxFrames(t *testing.T) {
	t.Parallel()
	lim := testLimits
	lim.MaxAnimationFrames = 2
	o := processRequest(t, executorWithLimits(t, &lim), readFixture(t, seqAVIF), parse(t, "rs:fit:32:0"), transform.FormatWebP)
	got := readTiming(t, o.bytes)
	if got.Frames != 2 || !cmp.Equal(got.Delay, []int{100, 200}) {
		t.Errorf("timing %+v, want 2 frames of 100 and 200 ms", got)
	}
}
