//go:build cgo

package codec_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// videoLimits enables video sources the way internal/app does on the full
// image (and asks for them on slim, where they stay unsupported).
func videoLimits() codec.Limits {
	return codec.Limits{Video: video.Settings{Enabled: true, Samples: 5, Span: 10 * time.Second}}
}

// TestProbeVideo: with the ffmpeg build tag video fixtures probe to their
// display size and orientation; without it (slim) every one is
// errs.KindUnsupported with a public message, never a crash.
func TestProbeVideo(t *testing.T) {
	t.Parallel()
	frame := func(f codec.Format, w, h int) codec.ImageMeta {
		return codec.ImageMeta{
			Format: f, Width: w, Height: h, Frames: 1, Pages: 1, PageHeight: h,
			Orientation: 1, BitDepth: 8, Loop: -1, ColorSpace: codec.ColorSpaceSRGB,
		}
	}
	tests := map[string]struct {
		want  codec.ImageMeta
		codec string
	}{
		"video/clip-vp9.webm":    {frame(codec.FormatWebM, 1280, 720), "vp9"},
		"video/clip-av1.mp4":     {frame(codec.FormatMP4, 1280, 720), "av1"},
		"video/bbb-h264-aac.mp4": {frame(codec.FormatMP4, 1280, 720), "h264"},
		"video/clip-vp8.webm":    {frame(codec.FormatWebM, 320, 180), "vp8"},
		"video/clip-vp8.mkv":     {frame(codec.FormatMKV, 320, 180), "vp8"},
		"video/clip-png.mov":     {frame(codec.FormatMOV, 96, 54), "png"},
		"video/clip-raw.avi":     {frame(codec.FormatAVI, 96, 54), "rawvideo"},
		"video/clip-av1-rotate90.mp4": {func() codec.ImageMeta {
			m := frame(codec.FormatMP4, 1280, 720)
			m.Orientation = 6
			return m
		}(), "av1"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := probe(t.Context(), t, readFixture(t, name), videoLimits(), tc.want.Format.MIME())
			if !codec.VideoAvailable() {
				assertVideoUnsupported(t, err)
				return
			}
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if got.Video == nil || got.Video.Codec != tc.codec {
				t.Fatalf("video meta %+v, want codec %s", got.Video, tc.codec)
			}
			if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreFields(codec.ImageMeta{}, "Video")); diff != "" {
				t.Errorf("Probe mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func assertVideoUnsupported(t *testing.T, err error) {
	t.Helper()
	if errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("err = %v, want unsupported", err)
	}
	if e, ok := errors.AsType[*errs.Error](err); !ok || !strings.Contains(e.PublicMessage(), "video") {
		t.Errorf("no specific public message on %v", err)
	}
}

// TestProbeVideoDisabled: XI_VIDEO_ENABLED=false (the zero Limits) refuses
// video in every build; the video limits apply before any decode.
func TestProbeVideoDisabled(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "video/clip-vp9.webm")
	_, err := probe(t.Context(), t, data, codec.Limits{}, "")
	assertVideoUnsupported(t, err)
	if !codec.VideoAvailable() {
		return
	}
	for name, edit := range map[string]func(*codec.Limits){
		"bytes":         func(l *codec.Limits) { l.Video.Limits.MaxBytes = 1024 },
		"duration":      func(l *codec.Limits) { l.Video.Limits.MaxDuration = time.Second },
		"video pixels":  func(l *codec.Limits) { l.Video.Limits.MaxPixels = 1000 },
		"source pixels": func(l *codec.Limits) { l.MaxSrcPixels = 1000 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lim := videoLimits()
			edit(&lim)
			if _, err := probe(t.Context(), t, data, lim, ""); errs.KindOf(err) != errs.KindLimitExceeded {
				t.Errorf("err = %v, want limit exceeded", err)
			}
		})
	}
}

// TestInspectVideo: /info's view of a video (bands, sample format, the
// video metadata).
func TestInspectVideo(t *testing.T) {
	t.Parallel()
	buf := newBuffer(t, readFixture(t, "video/bbb-h264-aac.mp4"))
	defer buf.Release()
	var info codec.Info
	err := vips.Run(t.Context(), func() error {
		var err error
		info, err = codec.Inspect(t.Context(), buf, videoLimits(), "")
		return err
	})
	if !codec.VideoAvailable() {
		assertVideoUnsupported(t, err)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.Bands != 3 || info.SampleFormat != "uchar" || info.Colorspace != "srgb" || info.Video == nil {
		t.Fatalf("info %+v", info)
	}
	if len(info.Video.Streams) != 2 || info.Video.Tags["title"] == "" {
		t.Errorf("video meta %+v", info.Video)
	}
}

// TestSampleVideo: the pixel fields of /info sample the default frame,
// upright (the rotated clip is portrait).
func TestSampleVideo(t *testing.T) {
	t.Parallel()
	for name, want := range map[string][2]int{
		"video/clip-av1.mp4":          {64, 36},
		"video/clip-av1-rotate90.mp4": {36, 64},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, name))
			defer buf.Release()
			var raw codec.Raw
			err := vips.Run(t.Context(), func() error {
				var err error
				raw, err = codec.Sample(t.Context(), buf, codec.FormatMP4, 64, videoLimits())
				return err
			})
			if !codec.VideoAvailable() {
				assertVideoUnsupported(t, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if raw.Width != want[0] || raw.Height != want[1] || raw.Bands != 4 || len(raw.Data) != raw.Width*raw.Height*4 {
				t.Errorf("sample %dx%dx%d (%d bytes), want %dx%d", raw.Width, raw.Height, raw.Bands, len(raw.Data), want[0], want[1])
			}
		})
	}
}

// TestVideoFrameCanceled: a done context stops the decoder with its kind.
func TestVideoFrameCanceled(t *testing.T) {
	t.Parallel()
	buf := newBuffer(t, readFixture(t, "video/clip-vp9.webm"))
	defer buf.Release()
	ctx, cancel := context.WithCancel(t.Context())
	err := vips.Run(ctx, func() error {
		cancel()
		req := video.FrameRequest{Second: new(1.0)}
		img, _, err := codec.VideoFrame(ctx, buf, &req)
		img.Close()
		return err
	})
	if k := errs.KindOf(err); k != errs.KindCanceled && (codec.VideoAvailable() || k != errs.KindUnsupported) {
		t.Errorf("err = %v", err)
	}
}
