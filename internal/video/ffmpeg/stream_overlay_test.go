//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"bytes"
	"image"
	"math"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// streamOverlaySegment transcodes segment n of rendition r with its
// image overlay placed as media places it: laid out at refRect on the
// overlay's reference frame (the largest rendition) and scaled to r.
func streamOverlaySegment(t *testing.T, src *vips.Buffer, op *transform.VideoOp, l *video.StreamLayout, r *video.Rendition, n int, refRect image.Rectangle) *ffmpeg.Output {
	t.Helper()
	tx, err := ffmpeg.OpenTranscode(t.Context(), src, video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	in := tx.Input()
	p, err := video.PlanSegment(&in, op, l, r, n, txSettings())
	if err != nil {
		t.Fatal(err)
	}
	v := p.Video
	rw, rh := v.OverlayRef()
	if len(v.Overlays) != 2 || rw != l.Video[0].Width || rh != l.Video[0].Height {
		t.Fatalf("segment %d of %s: overlays %+v laid out on %dx%d", n, r.Name, v.Overlays, rw, rh)
	}
	kx, ky := float64(r.Width)/float64(rw), float64(r.Height)/float64(rh)
	rect := scaleRect(refRect, kx, ky)
	v.Overlays[0].Place(rect.Min.X, rect.Min.Y, rect.Dx(), rect.Dy())
	o, err := tx.Run(t.Context(), p, solid(t, rect.Dx(), rect.Dy(), 255, 0, 0, 255), nil)
	if err != nil {
		t.Fatalf("segment %d of %s: %v", n, r.Name, err)
	}
	return o
}

// frameOf decodes the frame of a file at second s.
func frameOf(tb testing.TB, data []byte, s float64) *ffmpeg.Frame {
	tb.Helper()
	buf, err := vips.NewBuffer(len(data))
	if err != nil {
		tb.Fatal(err)
	}
	defer buf.Release()
	copy(buf.Bytes(), data)
	f, err := ffmpeg.Thumbnail(tb.Context(), buf, &video.FrameRequest{Second: &s})
	if err != nil {
		tb.Fatalf("frame at %g s: %v", s, err)
	}
	tb.Cleanup(f.Release)
	return f
}

func scaleRect(r image.Rectangle, kx, ky float64) image.Rectangle {
	return image.Rect(int(math.Round(float64(r.Min.X)*kx)), int(math.Round(float64(r.Min.Y)*ky)),
		int(math.Round(float64(r.Max.X)*kx)), int(math.Round(float64(r.Max.Y)*ky)))
}

// TestStreamOverlays: overlays on a stream (WP-131) are drawn on each
// segment of each rendition on the stream's timeline: an image overlay
// shown from 1.5 s to 4.5 s appears in the frames of that window only,
// across the segment boundaries at 2 s and 4 s, at the same place relative
// to the frame on both renditions; a timecode is drawn as well, and the
// segments still join into the source's frame count.
func TestStreamOverlays(t *testing.T) {
	t.Parallel()
	caps := ffmpeg.TranscodeCaps()
	if !caps.Overlays || !caps.Timecode {
		t.Skip("this image has no overlay filters")
	}
	src := source(t, streamFixture)
	op := &transform.VideoOp{
		Mute: true, CRF: 10,
		Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second, Renditions: []int{360, 240}},
		Overlays: []transform.VideoOverlay{
			{Opacity: 1, Source: &transform.SourceSpec{Raw: "logo.png"}, Start: 1500 * time.Millisecond, End: 4500 * time.Millisecond},
			{
				Opacity: 1, Position: transform.Gravity{Type: transform.GravitySouthWest, XOffset: 8, YOffset: 8},
				Timecode: transform.VideoTimecodeHMS, TimecodeSize: 32,
			},
		},
	}
	l := layout(t, src, op)
	refRect := image.Rect(480, 20, 600, 100) // on v0, 640×360
	for i := range l.Video {
		r := &l.Video[i]
		t.Run(r.Name, func(t *testing.T) {
			t.Parallel()
			var joined bytes.Buffer
			for n := range l.Segments {
				f := mustParse(t, streamOverlaySegment(t, src, op, l, r, n, refRect))
				if n == 0 {
					joined.Write(f.Init)
				}
				joined.Write(f.Fragments[0].Data)
			}
			if pk := packets(t, joined.Bytes()); len(pk) != 165 {
				t.Fatalf("%d frames, want 165", len(pk))
			}
			rect := scaleRect(refRect, float64(r.Width)/640, float64(r.Height)/360)
			off := float64(l.Offset90k) / 90000
			for _, s := range []float64{1.2, 1.64, 2.0, 3.0, 4.0, 4.36, 4.8} {
				got := mean(frameOf(t, joined.Bytes(), off+s), rect)
				in := s > 1.5 && s < 4.5
				red := got[0] > 200 && got[1] < 60 && got[2] < 60
				if in != red {
					t.Errorf("at %.2f s of the stream the overlay area is %.0f (window 1.5–4.5 s)", s, got)
				}
			}
		})
	}
}
