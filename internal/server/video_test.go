package server

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// fakeSession counts Close calls.
type fakeSession struct{ closed atomic.Int32 }

func (s *fakeSession) Close() { s.closed.Add(1) }

// videoMeta is the probe of a 1280×720, 2 s video with a kept session.
func videoMeta(s *fakeSession) SourceMeta {
	return SourceMeta{
		Width: 1280, Height: 720, Frames: 1, Pages: 1, Orientation: 1,
		Video:   &video.Meta{DisplayWidth: 1280, DisplayHeight: 720, Duration: 2},
		Session: s,
	}
}

// TestVideoCompositeMeta: a video_thumbnail_animation turns the probed
// video into an animation for the negotiation, the limits and the
// processor; a sprite into one large still; the probe's session reaches
// the processor and is closed once the request is done, also when the
// limits refuse the composite.
func TestVideoCompositeMeta(t *testing.T) {
	t.Parallel()
	anim := func(step float64, frames int) *transform.VideoOptions {
		return &transform.VideoOptions{Animation: &transform.VideoAnimation{Step: step, Delay: 0.1, Frames: frames, FrameWidth: 160}}
	}
	for _, tc := range []struct {
		name         string
		vo           *transform.VideoOptions
		out          transform.Format
		w, h, frames int
		animated     bool
		status       int
	}{
		{"animation", anim(0.5, 4), transform.FormatWebP, 160, 90, 4, true, http.StatusOK},
		{
			"sprite", &transform.VideoOptions{Tile: &transform.VideoTile{Columns: 3, Rows: 2, TileWidth: 160, TileHeight: 90}},
			transform.FormatPNG, 480, 180, 1, false, http.StatusOK,
		},
		{"thumbnail", &transform.VideoOptions{Second: new(1.0)}, transform.FormatPNG, 1280, 720, 1, false, http.StatusOK},
		{"over the frame limit", anim(0.001, 300), transform.FormatWebP, 0, 0, 0, false, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &fakeSession{}
			h := newHarness(t, func(h *harness) {
				h.cfg.Limits.MaxAnimationFrames = 256
				h.cfg.Limits.MaxVideoCompositeFrames = 1000
				h.pr.meta = videoMeta(s)
				h.router.mutate = func(r *transform.Request) {
					r.Pipelines[0].Resize = transform.Resize{}
					r.Pipelines[0].Video = tc.vo
					r.Output.Format = tc.out
				}
			})
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if n := s.closed.Load(); n == 0 {
				t.Error("the probe's session was not closed")
			}
			h.checkReleased(t)
			if tc.status != http.StatusOK {
				return
			}
			h.p.mu.Lock()
			m := h.p.meta
			h.p.mu.Unlock()
			if m.Width != tc.w || m.Height != tc.h || m.Frames != tc.frames || m.IsAnimated != tc.animated {
				t.Errorf("processor meta %dx%d, %d frames, animated %v; want %dx%d, %d, %v",
					m.Width, m.Height, m.Frames, m.IsAnimated, tc.w, tc.h, tc.frames, tc.animated)
			}
			if m.Session != s {
				t.Error("the session did not reach the processor")
			}
		})
	}
}

// TestVideoOutputHeaders: an MP4 result is served as video/mp4 with the
// .mp4 extension, and X-Xi-Video-Codec says which codec wrote it.
func TestVideoOutputHeaders(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.cfg.DebugHeaders = true
		h.p.outFormat, h.p.outType, h.p.outCodec = transform.FormatMP4, "video/mp4", "av1"
	})
	rec := h.do(t, http.MethodGet, "/insecure/f:mp4/plain/http://origin/a.gif", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	hd := rec.Header()
	if hd.Get("Content-Type") != "video/mp4" || hd.Get("X-Xi-Video-Codec") != "av1" {
		t.Errorf("headers %v", hd)
	}
	if cd := hd.Get("Content-Disposition"); cd != `inline; filename="a.mp4"` {
		t.Errorf("Content-Disposition %q", cd)
	}
	if got := formatExt(transform.FormatHEVC); got != ".mp4" {
		t.Errorf("hevc extension %q", got)
	}
}
