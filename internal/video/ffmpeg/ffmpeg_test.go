//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

const fixtures = "../../../testdata/fixtures/video"

func TestMain(m *testing.M) {
	if err := vips.Init(new(vips.DefaultConfig())); err != nil {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

func source(tb testing.TB, name string) *vips.Buffer {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, name))
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

func TestAvailable(t *testing.T) {
	t.Parallel()
	if !ffmpeg.Available() {
		t.Fatal("Available() = false with the ffmpeg build tag")
	}
	if v := ffmpeg.Version(); v == "" {
		t.Fatal("empty FFmpeg version")
	}
}

func TestProbe(t *testing.T) {
	t.Parallel()
	ignore := cmpopts.IgnoreFields(video.Meta{}, "Tags", "Streams", "BitRate")
	tests := []struct {
		file string
		want video.Meta
	}{
		{"clip-vp9.webm", video.Meta{
			Container: "matroska", Duration: 2, Codec: "vp9", Width: 1280, Height: 720,
			DisplayWidth: 1280, DisplayHeight: 720, FPS: 25, Frames: 50,
		}},
		{"clip-av1.mp4", video.Meta{
			Container: "mov", Duration: 2, Codec: "av1", Width: 1280, Height: 720,
			DisplayWidth: 1280, DisplayHeight: 720, FPS: 25, Frames: 50,
		}},
		{"clip-av1-rotate90.mp4", video.Meta{
			Container: "mov", Duration: 2, Codec: "av1", Width: 1280, Height: 720,
			DisplayWidth: 1280, DisplayHeight: 720, FPS: 25, Frames: 50, Rotation: 90,
		}},
		{"clip-vp8.webm", video.Meta{
			Container: "matroska", Duration: 1, Codec: "vp8", Width: 320, Height: 180,
			DisplayWidth: 320, DisplayHeight: 180, FPS: 10, Frames: 10,
		}},
		{"clip-vp8.mkv", video.Meta{
			Container: "matroska", Duration: 1, Codec: "vp8", Width: 320, Height: 180,
			DisplayWidth: 320, DisplayHeight: 180, FPS: 10, Frames: 10,
		}},
		{"clip-png.mov", video.Meta{
			Container: "mov", Duration: 1, Codec: "png", Width: 96, Height: 54,
			DisplayWidth: 96, DisplayHeight: 54, FPS: 10, Frames: 10,
		}},
		{"clip-raw.avi", video.Meta{
			Container: "avi", Duration: 1, Codec: "rawvideo", Width: 96, Height: 54,
			DisplayWidth: 96, DisplayHeight: 54, FPS: 10, Frames: 10,
		}},
		{"bbb-h264-aac.mp4", video.Meta{
			Container: "mov", Duration: 1, Codec: "h264", Width: 1280, Height: 720,
			DisplayWidth: 1280, DisplayHeight: 720, FPS: 30, Frames: 30,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			m, err := ffmpeg.Probe(t.Context(), source(t, tc.file), video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, m, ignore, cmpopts.EquateApprox(0, 0.02)); diff != "" {
				t.Errorf("meta (-want +got):\n%s", diff)
			}
			if len(m.Streams) == 0 || m.Streams[m.VideoStream].Type != "video" {
				t.Errorf("streams %+v: the video stream is missing", m.Streams)
			}
		})
	}
}

func TestProbeStreams(t *testing.T) {
	t.Parallel()
	m, err := ffmpeg.Probe(t.Context(), source(t, "bbb-h264-aac.mp4"), video.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Streams) != 2 {
		t.Fatalf("streams = %+v, want video and audio", m.Streams)
	}
	a := m.Streams[1]
	if a.Type != "audio" || a.Codec != "aac" || a.SampleRate != 16000 || a.Layout != "mono" || a.Language != "eng" {
		t.Errorf("audio stream = %+v", a)
	}
	if m.Tags["major_brand"] != "isom" || m.Tags["artist"] == "" {
		t.Errorf("tags = %v", m.Tags)
	}
	if m.BitRate <= 0 || m.Streams[0].BitRate <= 0 {
		t.Errorf("bit rates %d, %d", m.BitRate, m.Streams[0].BitRate)
	}
}

func TestProbeErrors(t *testing.T) {
	t.Parallel()
	junk, err := vips.NewBuffer(4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(junk.Release)
	tests := []struct {
		name string
		src  *vips.Buffer
		lim  video.Limits
		want errs.Kind
	}{
		{"not a video", junk, video.Limits{}, errs.KindCorrupt},
		{"bytes", source(t, "clip-vp9.webm"), video.Limits{MaxBytes: 1000}, errs.KindLimitExceeded},
		{"duration", source(t, "clip-vp9.webm"), video.Limits{MaxDuration: time.Second}, errs.KindLimitExceeded},
		{"pixels", source(t, "clip-av1.mp4"), video.Limits{MaxPixels: 640 * 360}, errs.KindLimitExceeded},
		{"truncated", truncated(t, "clip-av1.mp4", 2000), video.Limits{}, errs.KindCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ffmpeg.Probe(t.Context(), tc.src, tc.lim)
			if k := errs.KindOf(err); k != tc.want {
				t.Fatalf("kind = %v (%v), want %v", k, err, tc.want)
			}
		})
	}
}

func truncated(tb testing.TB, name string, n int) *vips.Buffer {
	tb.Helper()
	full := source(tb, name)
	buf, err := vips.NewBuffer(n)
	if err != nil {
		tb.Fatal(err)
	}
	copy(buf.Bytes(), full.Bytes()[:n])
	tb.Cleanup(buf.Release)
	return buf
}

func TestThumbnail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file      string
		req       video.FrameRequest
		wantW     int
		wantH     int
		wantTime  float64
		wantKey   bool
		timeSlack float64
	}{
		{"clip-vp9.webm", video.FrameRequest{Second: new(0.0)}, 1280, 720, 0, true, 0.001},
		{"clip-vp9.webm", video.FrameRequest{Second: new(1.0), Width: 320, Height: 180}, 320, 180, 1, false, 0.001},
		{"clip-vp9.webm", video.FrameRequest{Second: new(1.0), Keyframes: true}, 1280, 720, 0.96, true, 0.001},
		{"clip-vp9.webm", video.FrameRequest{Second: new(-0.5)}, 1280, 720, 1.52, false, 0.001},
		{"clip-vp9.webm", video.FrameRequest{Second: new(99.0)}, 1280, 720, 1.96, false, 0.001},
		{"clip-av1.mp4", video.FrameRequest{Second: new(0.5)}, 1280, 720, 0.52, false, 0.001},
		{"bbb-h264-aac.mp4", video.FrameRequest{Second: new(0.5)}, 1280, 720, 0.5, false, 0.001},
		{"clip-vp8.mkv", video.FrameRequest{Second: new(0.3)}, 320, 180, 0.3, false, 0.001},
		{"clip-png.mov", video.FrameRequest{Second: new(0.5)}, 96, 54, 0.5, true, 0.001},
		{"clip-raw.avi", video.FrameRequest{Second: new(0.5)}, 96, 54, 0.5, true, 0.001},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			f, err := ffmpeg.Thumbnail(t.Context(), source(t, tc.file), &tc.req)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Release()
			if f.Width != tc.wantW || f.Height != tc.wantH || f.Bands != 3 {
				t.Errorf("frame %dx%dx%d, want %dx%dx3", f.Width, f.Height, f.Bands, tc.wantW, tc.wantH)
			}
			if d := f.Time - tc.wantTime; d < -tc.timeSlack || d > tc.timeSlack {
				t.Errorf("time %.3f, want %.3f", f.Time, tc.wantTime)
			}
			if f.Keyframe != tc.wantKey {
				t.Errorf("keyframe %v, want %v", f.Keyframe, tc.wantKey)
			}
			if f.Pixels.Len() < f.Width*f.Height*f.Bands {
				t.Errorf("%d bytes for %dx%dx%d", f.Pixels.Len(), f.Width, f.Height, f.Bands)
			}
		})
	}
}

// TestBestFrame: the procedural clip fades in from black, so best-frame
// selection must not take the first keyframe.
func TestBestFrame(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"clip-vp9.webm", "clip-av1.mp4", "bbb-h264-aac.mp4"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			f, err := ffmpeg.Thumbnail(t.Context(), source(t, file), &video.FrameRequest{Width: 160, Height: 90})
			if err != nil {
				t.Fatal(err)
			}
			defer f.Release()
			if f.Sampled == 0 || f.Score <= 0 || !f.Keyframe {
				t.Errorf("sampled %d, score %.3f, keyframe %v", f.Sampled, f.Score, f.Keyframe)
			}
			if file != "bbb-h264-aac.mp4" && f.Time < 0.4 {
				t.Errorf("best frame at %.2f s: inside the fade-in", f.Time)
			}
			again, err := ffmpeg.Thumbnail(t.Context(), source(t, file), &video.FrameRequest{Width: 160, Height: 90})
			if err != nil {
				t.Fatal(err)
			}
			defer again.Release()
			if again.Time != f.Time || again.Score != f.Score {
				t.Errorf("not deterministic: %.3f/%.3f then %.3f/%.3f", f.Time, f.Score, again.Time, again.Score)
			}
		})
	}
}

func TestThumbnailCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ffmpeg.Thumbnail(ctx, source(t, "clip-vp9.webm"), &video.FrameRequest{Second: new(1.0)})
	if k := errs.KindOf(err); k != errs.KindCanceled {
		t.Fatalf("kind = %v (%v), want canceled", k, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v does not wrap context.Canceled", err)
	}
}
