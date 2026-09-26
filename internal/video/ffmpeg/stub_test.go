//go:build !ffmpeg || !cgo

package ffmpeg_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// TestStub: without the ffmpeg build tag every call reports
// errs.KindUnsupported with a public message, and nothing crashes.
func TestStub(t *testing.T) {
	t.Parallel()
	if ffmpeg.Available() || ffmpeg.Version() != "" {
		t.Fatal("the stub claims FFmpeg support")
	}
	if _, err := ffmpeg.Probe(t.Context(), nil, video.Limits{}); errs.KindOf(err) != errs.KindUnsupported {
		t.Errorf("Probe: %v, want unsupported", err)
	}
	f, err := ffmpeg.Thumbnail(t.Context(), nil, &video.FrameRequest{})
	if errs.KindOf(err) != errs.KindUnsupported || f != nil {
		t.Errorf("Thumbnail: %v, %v, want unsupported", f, err)
	}
	se, ok := errors.AsType[*errs.Error](err)
	if !ok || se.PublicMessage() == "" {
		t.Errorf("no public message on %v", err)
	}
	if s := ffmpeg.ReadStats(); s != (ffmpeg.Stats{}) {
		t.Errorf("stats %+v", s)
	}
	f.Release() // nil-safe
}
