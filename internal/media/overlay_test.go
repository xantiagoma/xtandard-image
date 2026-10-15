package media

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// renderer is a fake OverlayRenderer: text "none" draws nothing, text
// "fail" fails; the rest is a 10×10 layer at (2, 4).
type renderer struct {
	mu     sync.Mutex
	sizes  [][2]int
	refs   [][2]int
	imgs   []Buffer
	layers []*buf
}

func (r *renderer) Render(_ context.Context, o *transform.VideoOverlay, img Buffer, f Frame) (Layer, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sizes = append(r.sizes, [2]int{f.Width, f.Height})
	r.refs = append(r.refs, [2]int{f.RefWidth, f.RefHeight})
	r.imgs = append(r.imgs, img)
	switch o.Text {
	case "none":
		return Layer{}, false, nil
	case "fail":
		return Layer{}, false, errs.New(errs.KindInvalidURL, "bad text")
	}
	b := newBuf("rgba")
	r.layers = append(r.layers, b)
	return Layer{Pixels: b, X: 2, Y: 4, Width: 10, Height: 10}, true, nil
}

func overlay(text string) transform.VideoOverlay {
	return transform.VideoOverlay{Opacity: 1, Text: text}
}

func overlayConfig() *Config {
	return &Config{Workers: 1, Timeout: time.Minute, Transcode: video.TranscodeSettings{Threads: 1}}
}

// TestTransformOverlays: image and text overlays are rendered for the
// output size inside the transcode, placed, handed to the job with their
// pixels, and every reference is dropped afterwards.
func TestTransformOverlays(t *testing.T) {
	t.Parallel()
	e, r := &engine{}, &renderer{}
	e.in = input()
	s, err := New(overlayConfig(), caps(), e, WithOverlayRenderer(r))
	if err != nil {
		t.Fatal(err)
	}
	logo := newBuf("png")
	op := &transform.VideoOp{Width: 640, Mute: true, Overlays: []transform.VideoOverlay{
		overlay("a"),
		overlay("none"),
		{Opacity: 1, Timecode: transform.VideoTimecodeHMS},
		{Opacity: 1, Source: &transform.SourceSpec{Raw: "logo.png"}},
	}}
	res, err := s.Transform(t.Context(), "k", newBuf("v"), op, []Buffer{nil, nil, nil, logo})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Release()
	if len(r.sizes) != 3 || r.sizes[0] != [2]int{640, 360} || r.refs[0] != r.sizes[0] || r.imgs[0] != nil || r.imgs[2] != Buffer(logo) {
		t.Fatalf("renders %v (laid out on %v) with %v", r.sizes, r.refs, r.imgs)
	}
	p, px := e.plans[0], e.pixels[0]
	got := p.Video.Overlays
	if len(got) != 3 || got[0].Index != 0 || got[1].Index != 2 || got[1].Timecode == nil || got[2].Index != 3 ||
		got[0].X != 2 || got[0].Y != 4 || got[0].Width != 10 || got[2].Height != 10 {
		t.Fatalf("planned overlays %+v", got)
	}
	if len(px) != 3 || px[0] != Buffer(r.layers[0]) || px[1] != nil || px[2] != Buffer(r.layers[1]) {
		t.Fatalf("pixels %v", px)
	}
	for i, l := range r.layers {
		if n := l.refs.Load(); n != 0 {
			t.Errorf("layer %d has %d references after the transcode", i, n)
		}
	}
	if n := logo.refs.Load(); n != 1 {
		t.Errorf("overlay image has %d references, want the caller's", n)
	}
}

func TestTransformOverlayErrors(t *testing.T) {
	t.Parallel()
	e, r := &engine{}, &renderer{}
	e.in = input()
	s, err := New(overlayConfig(), caps(), e, WithOverlayRenderer(r))
	if err != nil {
		t.Fatal(err)
	}
	op := &transform.VideoOp{Mute: true, Overlays: []transform.VideoOverlay{overlay("a"), overlay("fail")}}
	if _, err := s.Transform(t.Context(), "k", newBuf("v"), op, nil); errs.KindOf(err) != errs.KindInvalidURL {
		t.Fatalf("renderer error: %v", err)
	}
	if len(r.layers) != 1 || r.layers[0].refs.Load() != 0 || len(e.plans) != 0 {
		t.Fatalf("a failed render leaked its first layer or ran: %d layers, %d runs", len(r.layers), len(e.plans))
	}
	if _, err := s.Transform(t.Context(), "k", newBuf("v"), op, make([]Buffer, 3)); errs.KindOf(err) != errs.KindInternal {
		t.Fatalf("more images than overlays: %v", err)
	}

	// Without a renderer, image and text overlays are refused before
	// anything runs; a timecode needs none.
	bare, err := New(overlayConfig(), caps(), e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.Resolve(&transform.VideoOp{Overlays: []transform.VideoOverlay{overlay("a")}}); errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("no renderer: %v", err)
	}
	tc := &transform.VideoOp{Mute: true, Overlays: []transform.VideoOverlay{
		{Opacity: 1, Timecode: transform.VideoTimecodeSeconds},
	}}
	res, err := bare.Transform(t.Context(), "tc", newBuf("v"), tc, nil)
	if err != nil {
		t.Fatalf("timecode without a renderer: %v", err)
	}
	res.Body.Release()
}

// TestStreamOverlays: a stream's video segments (WP-131) draw its
// overlays: rendered for the segment's rendition, laid out on the largest
// one, with the overlay images retained for the transcode only; the
// windows stay on the stream's timeline. Without a renderer, image and
// text overlays on a stream are refused before anything runs.
func TestStreamOverlays(t *testing.T) {
	t.Parallel()
	e, r := &engine{}, &renderer{}
	e.in = input()
	s, err := New(overlayConfig(), caps(), e, WithOverlayRenderer(r))
	if err != nil {
		t.Fatal(err)
	}
	logo := newBuf("png")
	op := streamOp()
	op.Overlays = []transform.VideoOverlay{
		{Opacity: 1, Source: &transform.SourceSpec{Raw: "logo.png"}, Start: 500 * time.Millisecond},
		{Opacity: 1, Timecode: transform.VideoTimecodeHMS, TimecodeSize: 40},
	}
	res, err := s.Stream(t.Context(), "v2/1", newBuf("v"), op, StreamPart{Track: "v2", Segment: 1, Overlays: []Buffer{logo}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Release()
	if len(r.sizes) != 1 || r.sizes[0] != [2]int{640, 360} || r.refs[0] != [2]int{1280, 720} || r.imgs[0] != Buffer(logo) {
		t.Fatalf("renders %v laid out on %v with %v", r.sizes, r.refs, r.imgs)
	}
	ov := e.plans[0].Video.Overlays
	if len(ov) != 2 || ov[0].Enable != "gte(t,0.5)" || ov[0].Width != 10 || ov[1].Timecode == nil || ov[1].Timecode.Size != 20 {
		t.Fatalf("planned overlays %+v", ov)
	}
	if n := logo.refs.Load(); n != 1 {
		t.Errorf("overlay image has %d references, want the caller's", n)
	}
	bare, err := New(overlayConfig(), caps(), e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.ResolveStream(op); errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("no renderer: %v", err)
	}
}
