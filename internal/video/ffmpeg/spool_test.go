//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/spool"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// Spool files (WP-121b): sources read from a file descriptor and outputs
// spilled into one must be the same bytes as in memory.

// fileSource writes the fixture after off bytes of junk into a file and
// returns it as a source at off.
func fileSource(tb testing.TB, name string, off int64) *cache.FileBuffer {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		tb.Fatal(err)
	}
	f, err := os.CreateTemp(tb.TempDir(), "src-*")
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := f.Write(append(bytes.Repeat([]byte{0xAB}, int(off)), data...)); err != nil {
		tb.Fatal(err)
	}
	b := cache.NewFileBuffer(f, off, int64(len(data)), nil)
	tb.Cleanup(b.Release)
	return b
}

func newTestSpool(tb testing.TB) *spool.Spool {
	tb.Helper()
	sp, err := spool.New(spool.Config{Dir: filepath.Join(tb.TempDir(), "spool"), Threshold: 1})
	if err != nil {
		tb.Fatal(err)
	}
	return sp
}

// openPlan opens a transcoder with open and plans op on it.
func openPlan(tb testing.TB, open func() (*ffmpeg.Transcoder, error), op *transform.VideoOp) (*ffmpeg.Transcoder, *video.TranscodePlan) {
	tb.Helper()
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.Resolve(op)
	if err != nil {
		tb.Fatal(err)
	}
	t, err := open()
	if err != nil {
		tb.Fatal(err)
	}
	in := t.Input()
	p, err := video.PlanTranscode(&in, op, out, caps, txSettings())
	if err != nil {
		t.Close()
		tb.Fatal(err)
	}
	return t, p
}

// readAll reads a spooled output back.
func readAll(tb testing.TB, b *cache.FileBuffer) []byte {
	tb.Helper()
	data, err := io.ReadAll(b.Reader())
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// TestTranscodeFromFile: a source read through its descriptor (at an
// offset inside the file) probes and transcodes exactly like the same
// bytes in memory. The encoders run single-threaded with bit-exact
// muxing, so the two files are identical: this compares one I/O path with
// the other, not encoder output with a golden file.
func TestTranscodeFromFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file string
		op   transform.VideoOp
	}{
		{"clip-av1.mp4", transform.VideoOp{Container: transform.VideoContainerWebM, Width: 160, Duration: 500 * time.Millisecond}},
		{"clip-vp8-matroska.mkv", transform.VideoOp{Container: transform.VideoContainerWebM, Width: 128, Start: 300 * time.Millisecond, Duration: 400 * time.Millisecond}},
		{"clip-raw.avi", transform.VideoOp{Container: transform.VideoContainerWebM, Width: 96}},
		{"bbb-h264-aac.mp4", transform.VideoOp{Container: transform.VideoContainerM4A}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			mt, mp := openPlan(t, func() (*ffmpeg.Transcoder, error) {
				return ffmpeg.OpenTranscode(ctx, source(t, tc.file), video.Limits{}, 1)
			}, &tc.op)
			defer mt.Close()
			ft, fp := openPlan(t, func() (*ffmpeg.Transcoder, error) {
				return ffmpeg.OpenTranscodeFile(ctx, fileSource(t, tc.file, 4099), video.Limits{}, 1)
			}, &tc.op)
			defer ft.Close()
			if d := cmp.Diff(mt.Input(), ft.Input()); d != "" {
				t.Fatalf("probe of the file differs (-memory +file):\n%s", d)
			}
			mo, err := mt.Run(ctx, mp)
			if err != nil {
				t.Fatal(err)
			}
			defer mo.Release()
			fo, err := ft.Run(ctx, fp)
			if err != nil {
				t.Fatal(err)
			}
			defer fo.Release()
			if !bytes.Equal(mo.Bytes(), fo.Bytes()) {
				t.Errorf("output of the file source differs: %d vs %d bytes", mo.Len(), fo.Len())
			}
		})
	}
}

// TestTranscodeSpill: an output past the spill threshold moves into the
// spool file, faststart and cues_to_front rewrites included, and is the
// same file as in memory; one under it stays in memory.
func TestTranscodeSpill(t *testing.T) {
	t.Parallel()
	mp4 := transform.VideoOp{Width: 320, Duration: time.Second}
	tests := []struct {
		name string
		op   transform.VideoOp
	}{
		{"mp4 faststart", mp4},
		{"webm cues to front", transform.VideoOp{Container: transform.VideoContainerWebM, Width: 320, Duration: time.Second}},
		{"m4a", transform.VideoOp{Container: transform.VideoContainerM4A}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			src := source(t, "bbb-h264-aac.mp4")
			want := func() []byte {
				tr, p := openPlan(t, func() (*ffmpeg.Transcoder, error) { return ffmpeg.OpenTranscode(ctx, src, video.Limits{}, 1) }, &tc.op)
				defer tr.Close()
				o, err := tr.Run(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				defer o.Release()
				return bytes.Clone(o.Bytes())
			}()
			sp := newTestSpool(t)
			for _, threshold := range []int64{1024, int64(len(want) - 1), int64(len(want))} {
				tr, p := openPlan(t, func() (*ffmpeg.Transcoder, error) { return ffmpeg.OpenTranscode(ctx, src, video.Limits{}, 1) }, &tc.op)
				w, err := sp.Create(spool.KindOutput, 0)
				if err != nil {
					t.Fatal(err)
				}
				out, n, err := tr.RunSpill(ctx, p, w.File(), threshold)
				tr.Close()
				if err != nil {
					w.Abort()
					t.Fatalf("threshold %d: %v", threshold, err)
				}
				if threshold >= int64(len(want)) {
					w.Abort()
					if out == nil || n != 0 || !bytes.Equal(out.Bytes(), want) {
						t.Fatalf("threshold %d: an output of %d bytes left memory", threshold, len(want))
					}
					out.Release()
					continue
				}
				if out != nil || n != int64(len(want)) {
					t.Fatalf("threshold %d: output %v, spilled %d bytes, want %d", threshold, out != nil, n, len(want))
				}
				fb, err := w.Finish(n)
				if err != nil {
					t.Fatal(err)
				}
				if got := readAll(t, fb); !bytes.Equal(got, want) {
					t.Errorf("threshold %d: spilled file differs (%d bytes)", threshold, len(got))
				}
				fb.Release()
			}
			if b, f := sp.Usage(); b != 0 || f != 0 {
				t.Errorf("spool holds %d bytes in %d files", b, f)
			}
		})
	}
}

// TestTranscodeSpillLimit: the output limit still holds in the file.
func TestTranscodeSpillLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := txSettings()
	s.MaxOutputBytes = 20_000
	op := transform.VideoOp{Container: transform.VideoContainerWebM, Width: 640}
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.Resolve(&op)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := ffmpeg.OpenTranscode(ctx, source(t, "bbb-h264-aac.mp4"), video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	in := tr.Input()
	p, err := video.PlanTranscode(&in, &op, out, caps, s)
	if err != nil {
		t.Fatal(err)
	}
	w, err := newTestSpool(t).Create(spool.KindOutput, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	if _, _, err := tr.RunSpill(ctx, p, w.File(), 1024); errs.KindOf(err) != errs.KindLimitExceeded {
		t.Fatalf("over the output limit: %v", err)
	}
}

// openFDs counts this process's open descriptors (Linux).
func openFDs(tb testing.TB) int {
	tb.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		tb.Skip("no /proc/self/fd")
	}
	return len(entries)
}

// TestSpoolNoLeak: file sources, spilled outputs, failures and aborts
// close every descriptor the shim duplicated and free every handle.
func TestSpoolNoLeak(t *testing.T) {
	ctx := t.Context()
	sp := newTestSpool(t)
	src := fileSource(t, "bbb-h264-aac.mp4", 0)
	op := transform.VideoOp{Container: transform.VideoContainerWebM, Width: 160, Duration: 500 * time.Millisecond}
	run := func(ctx context.Context, threshold int64) {
		tr, err := ffmpeg.OpenTranscodeFile(ctx, src, video.Limits{}, 1)
		if err != nil {
			if ctx.Err() == nil {
				t.Fatal(err)
			}
			return
		}
		defer tr.Close()
		in := tr.Input()
		caps := ffmpeg.TranscodeCaps()
		out, _ := caps.Resolve(&op)
		p, err := video.PlanTranscode(&in, &op, out, caps, txSettings())
		if err != nil {
			t.Fatal(err)
		}
		w, err := sp.Create(spool.KindOutput, 0)
		if err != nil {
			t.Fatal(err)
		}
		o, n, err := tr.RunSpill(ctx, p, w.File(), threshold)
		switch {
		case err != nil:
			w.Abort()
		case o != nil:
			w.Abort()
			o.Release()
		default:
			fb, err := w.Finish(n)
			if err != nil {
				t.Fatal(err)
			}
			fb.Release()
		}
	}
	for range 5 { // warm up
		run(ctx, 1)
	}
	fds, stats := openFDs(t), ffmpeg.ReadStats()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for i := range 60 {
		switch i % 3 {
		case 0:
			run(ctx, 1) // spilled
		case 1:
			run(ctx, 1<<30) // in memory
		default:
			run(cancelled, 1) // aborted
		}
	}
	after := ffmpeg.ReadStats()
	if n := openFDs(t); n != fds {
		t.Errorf("open descriptors %d → %d", fds, n)
	}
	if after.Transcoders != stats.Transcoders || after.Outputs != stats.Outputs {
		t.Errorf("transcoders %d → %d, outputs %d → %d", stats.Transcoders, after.Transcoders, stats.Outputs, after.Outputs)
	}
	if b, f := sp.Usage(); b != 0 || f != 0 {
		t.Errorf("spool holds %d bytes in %d files", b, f)
	}
}
