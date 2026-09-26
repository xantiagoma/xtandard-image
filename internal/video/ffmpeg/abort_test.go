//go:build ffmpeg && cgo

package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// These white-box tests share TestMain (vips.Init) with ffmpeg_test.go:
// both files are compiled into one test binary.

func internalSource(tb testing.TB, name string) *vips.Buffer {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "video", name))
	if err != nil {
		tb.Fatal(err)
	}
	buf, err := vips.NewBuffer(len(data))
	if err != nil {
		tb.Fatal(err)
	}
	copy(buf.Bytes(), data)
	tb.Cleanup(buf.Release)
	return buf
}

// TestAbortFlag: with the abort flag set, the C decode loop and the AVIO
// interrupt callback stop the call (errs.KindCanceled when the context is
// not done itself); clearing it makes the handle usable again.
func TestAbortFlag(t *testing.T) {
	t.Parallel()
	d, err := open(t.Context(), internalSource(t, "clip-vp9.webm"), video.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	d.setAbort(true)
	if _, _, err := d.decodeAt(t.Context(), 1, false, nil); errs.KindOf(err) != errs.KindCanceled {
		t.Fatalf("aborted decode: %v, want canceled", err)
	}
	d.setAbort(false)
	at, _, err := d.decodeAt(t.Context(), 1, false, nil)
	if err != nil || at != 1 {
		t.Fatalf("decode after clearing the flag: %v at %v", err, at)
	}
}

// TestAbortOnDeadline: when the request deadline passes, the watcher sets
// the abort flag (so a decode in C stops at its next packet), and the call
// reports a timeout. synctest's clock makes the deadline deterministic.
func TestAbortOnDeadline(t *testing.T) {
	t.Parallel()
	src := internalSource(t, "clip-vp9.webm")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		d, err := open(ctx, src, video.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		defer d.close()
		if d.aborted() {
			t.Fatal("aborted before the deadline")
		}
		synctest.Sleep(2 * time.Second)
		synctest.Wait()
		if !d.aborted() {
			t.Fatal("the deadline did not set the abort flag")
		}
		if _, _, err := d.decodeAt(ctx, 1, false, nil); errs.KindOf(err) != errs.KindTimeout {
			t.Fatalf("decode after the deadline: %v, want timeout", err)
		}
	})
}

// TestWatcherStop: closing the decoder disarms the watcher, so a context
// that ends later never touches the freed handle.
func TestWatcherStop(t *testing.T) {
	t.Parallel()
	src := internalSource(t, "clip-vp8.webm")
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		d, err := open(ctx, src, video.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		d.close()
		cancel()
		synctest.Wait()
		if n := ReadStats().Handles; n < 0 {
			t.Fatalf("handles %d", n)
		}
	})
}

// TestKeyframeIndex: best-frame selection skips samples that land on a
// keyframe it already scored, from the demuxer's index, without decoding.
func TestKeyframeIndex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file   string
		a, b   float64 // two timestamps
		sameKF bool    // on the same keyframe
	}{
		{"clip-av1.mp4", 1.0, 1.2, true},
		{"clip-av1.mp4", 0.3, 0.6, false},
		{"bbb-h264-aac.mp4", 0.2, 0.8, true}, // one keyframe; B-frames: negative DTS
	} {
		d, err := open(t.Context(), internalSource(t, tc.file), video.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		ka, okA := d.keyframeBefore(tc.a)
		kb, okB := d.keyframeBefore(tc.b)
		d.close()
		if !okA || !okB || (ka == kb) != tc.sameKF {
			t.Errorf("%s: keyframes before %.2f and %.2f s: %.3f (%v), %.3f (%v); same keyframe want %v",
				tc.file, tc.a, tc.b, ka, okA, kb, okB, tc.sameKF)
		}
	}
}
