//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"context"
	"image"
	"math"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// solid is w×h straight RGBA pixels of one colour in C memory.
func solid(tb testing.TB, w, h int, r, g, b, a uint8) *vips.Buffer {
	tb.Helper()
	buf, err := vips.NewBuffer(w * h * 4)
	if err != nil {
		tb.Fatal(err)
	}
	p := buf.Bytes()
	for i := 0; i < len(p); i += 4 {
		p[i], p[i+1], p[i+2], p[i+3] = r, g, b, a
	}
	tb.Cleanup(buf.Release)
	return buf
}

// overlayLayer is an image overlay of the tests: its rectangle and pixels.
type overlayLayer struct {
	rect image.Rectangle
	pix  *vips.Buffer
}

// transcodeOverlays plans op (whose overlays are image overlays, except
// timecodes) and places layers[i] for the i-th planned overlay.
func transcodeOverlays(ctx context.Context, tb testing.TB, src *vips.Buffer, op *transform.VideoOp, layers []overlayLayer) (*ffmpeg.Output, error) {
	tb.Helper()
	r := transform.Request{Kind: transform.KindVideo, Source: transform.SourceSpec{Raw: "a"}, Video: op}
	r.ApplyDefaults(transform.StandardDefaults())
	if err := r.Validate(); err != nil {
		tb.Fatal(err)
	}
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.Resolve(r.Video)
	if err != nil {
		return nil, err
	}
	t, err := ffmpeg.OpenTranscode(ctx, src, video.Limits{}, 1)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	in := t.Input()
	p, err := video.PlanTranscode(&in, r.Video, out, caps, txSettings())
	if err != nil {
		return nil, err
	}
	pixels := make([]*vips.Buffer, len(p.Video.Overlays))
	for i := range p.Video.Overlays {
		if o := &p.Video.Overlays[i]; o.Image() && i < len(layers) {
			l := layers[i]
			o.Place(l.rect.Min.X, l.rect.Min.Y, l.rect.Dx(), l.rect.Dy())
			pixels[i] = l.pix
		}
	}
	return t.Run(ctx, p, pixels...)
}

// frameAt decodes the frame of data at second s.
func frameAt(tb testing.TB, data *ffmpeg.Output, s float64) *ffmpeg.Frame {
	tb.Helper()
	buf, err := vips.NewBuffer(len(data.Bytes()))
	if err != nil {
		tb.Fatal(err)
	}
	defer buf.Release()
	copy(buf.Bytes(), data.Bytes())
	f, err := ffmpeg.Thumbnail(tb.Context(), buf, &video.FrameRequest{Second: &s})
	if err != nil {
		tb.Fatalf("frame at %g s: %v", s, err)
	}
	tb.Cleanup(f.Release)
	return f
}

// mean is the mean R, G, B of r in f (inset by 3 pixels, away from the
// edges chroma subsampling and the codec blur).
func mean(f *ffmpeg.Frame, r image.Rectangle) [3]float64 {
	r = r.Inset(3)
	var sum [3]float64
	p := f.Pixels.Bytes()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := (y*f.Width + x) * f.Bands
			for c := range 3 {
				sum[c] += float64(p[i+c])
			}
		}
	}
	n := float64(r.Dx() * r.Dy())
	return [3]float64{sum[0] / n, sum[1] / n, sum[2] / n}
}

// alphaOf estimates how much of colour over shows in got over base.
func alphaOf(got, base, over [3]float64) float64 {
	var num, den float64
	for c := range 3 {
		num += (got[c] - base[c]) * (over[c] - base[c])
		den += (over[c] - base[c]) * (over[c] - base[c])
	}
	return num / den
}

// TestTranscodeOverlays: overlays appear where and when they are placed,
// with their opacity and fades, and nowhere else; the timecode is drawn.
func TestTranscodeOverlays(t *testing.T) {
	t.Parallel()
	caps := ffmpeg.TranscodeCaps()
	if !caps.Overlays {
		t.Skip("this image has no overlay filters")
	}
	src := source(t, "clip-vp9.webm") // 1280×720, 2 s, fades from black until 0.4 s and to white from 1.7 s
	base := transform.VideoOp{Width: 320, Container: transform.VideoContainerWebM, CRF: 8}
	sec := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
	img := func(o transform.VideoOverlay) transform.VideoOverlay {
		o.Opacity, o.Source = 1, &transform.SourceSpec{Raw: "overlay.png"}
		return o
	}
	red, blue, green := image.Rect(10, 10, 50, 40), image.Rect(200, 100, 240, 140), image.Rect(100, 120, 130, 150)
	op := base
	op.Overlays = []transform.VideoOverlay{
		img(transform.VideoOverlay{Start: sec(0.8), End: sec(1.4)}),
		img(transform.VideoOverlay{}),
		img(transform.VideoOverlay{Start: sec(0.6), FadeIn: sec(0.8)}),
	}
	layers := []overlayLayer{
		{red, solid(t, 40, 30, 255, 0, 0, 255)},
		{blue, solid(t, 40, 40, 0, 0, 255, 128)},
		{green, solid(t, 30, 30, 0, 255, 0, 255)},
	}
	tc := caps.Timecode
	timecode := image.Rect(160, 140, 320, 180) // south-east quarter, bottom band
	if tc {
		op.Overlays = append(op.Overlays, transform.VideoOverlay{
			Opacity: 1, Position: transform.Gravity{Type: transform.GravitySouthEast, XOffset: 4, YOffset: 4},
			Timecode: transform.VideoTimecodeHMS, TimecodeSize: 24,
		})
	}

	plain, err := transcodeOverlays(t.Context(), t, src, &base, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Release()
	out, err := transcodeOverlays(t.Context(), t, src, &op, layers)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	p := ffprobe(t, out.Bytes())
	if len(p.Streams) != 1 || p.Streams[0].Width != 320 || p.Streams[0].Height != 180 || p.Streams[0].NbFrames != "50" {
		t.Fatalf("output %+v", p.Streams)
	}

	near := func(what string, got, want [3]float64, tol float64) {
		t.Helper()
		for c := range 3 {
			if math.Abs(got[c]-want[c]) > tol {
				t.Errorf("%s: %.0f, want %.0f ± %.0f", what, got, want, tol)
				return
			}
		}
	}
	for _, s := range []float64{0.6, 1.0, 1.25, 1.6} {
		got, want := frameAt(t, out, s), frameAt(t, plain, s)
		// The red overlay only inside its window [0.8, 1.4].
		if s > 0.8 && s < 1.4 {
			near("red inside its window at "+ftoa(s), mean(got, red), [3]float64{255, 0, 0}, 24)
		} else {
			near("red outside its window at "+ftoa(s), mean(got, red), mean(want, red), 6)
		}
		// Half-transparent blue on every frame.
		bg := mean(want, blue)
		near("blue at half opacity at "+ftoa(s), mean(got, blue), [3]float64{bg[0] / 2, bg[1] / 2, (bg[2] + 255) / 2}, 14)
		// Green fading in from 0.6 s over 0.8 s.
		a := alphaOf(mean(got, green), mean(want, green), [3]float64{0, 255, 0})
		if wantA := min(max((s-0.6)/0.8, 0), 1); math.Abs(a-wantA) > 0.12 {
			t.Errorf("green at %g s: opacity %.2f, want %.2f", s, a, wantA)
		}
		// Elsewhere, the frame is the plain transcode's.
		near("untouched area at "+ftoa(s), mean(got, image.Rect(10, 60, 90, 110)), mean(want, image.Rect(10, 60, 90, 110)), 6)
		if tc {
			d := mean(got, timecode)
			w := mean(want, timecode)
			if math.Abs(d[0]-w[0])+math.Abs(d[1]-w[1])+math.Abs(d[2]-w[2]) < 6 {
				t.Errorf("no timecode drawn at %g s: %.0f vs %.0f", s, d, w)
			}
		}
	}
}

func ftoa(f float64) string { return time.Duration(f * float64(time.Second)).String() }

// TestTranscodeOverlayErrors: what the shim relies on is checked in Go.
func TestTranscodeOverlayErrors(t *testing.T) {
	t.Parallel()
	if !ffmpeg.TranscodeCaps().Overlays {
		t.Skip("this image has no overlay filters")
	}
	src := source(t, "clip-vp8.webm") // 320×180
	op := transform.VideoOp{Container: transform.VideoContainerWebM, Overlays: []transform.VideoOverlay{
		{Opacity: 1, Source: &transform.SourceSpec{Raw: "x.png"}},
	}}
	for _, tc := range []struct {
		name   string
		layers []overlayLayer
	}{
		{"unplaced", nil},
		{"outside the frame", []overlayLayer{{image.Rect(300, 170, 340, 190), solid(t, 40, 20, 1, 2, 3, 4)}}},
		{"short pixels", []overlayLayer{{image.Rect(0, 0, 40, 20), solid(t, 10, 10, 1, 2, 3, 4)}}},
	} {
		out, err := transcodeOverlays(t.Context(), t, src, &op, tc.layers)
		if err == nil {
			out.Release()
		}
		if errs.KindOf(err) != errs.KindInternal {
			t.Errorf("%s: %v, want internal", tc.name, err)
		}
	}
}

// TestTranscodeOverlayNoLeak: transcodes with overlays (fading, timed and
// timecodes) free every handle, frame and graph, failed ones included.
func TestTranscodeOverlayNoLeak(t *testing.T) {
	caps := ffmpeg.TranscodeCaps()
	if !caps.Overlays {
		t.Skip("this image has no overlay filters")
	}
	src := source(t, "clip-vp8.webm")
	pix := solid(t, 64, 32, 255, 255, 255, 200)
	run := func(i int) {
		op := &transform.VideoOp{Width: 160, Container: transform.VideoContainerWebM, Overlays: []transform.VideoOverlay{
			{Opacity: 1, Source: &transform.SourceSpec{Raw: "x"}, FadeIn: 200 * time.Millisecond, End: 800 * time.Millisecond},
			{Opacity: 1, Source: &transform.SourceSpec{Raw: "x"}, Start: 300 * time.Millisecond},
		}}
		if caps.Timecode {
			op.Overlays = append(op.Overlays, transform.VideoOverlay{Opacity: 1, Timecode: transform.VideoTimecodeSeconds})
		}
		layers := []overlayLayer{{image.Rect(0, 0, 64, 32), pix}, {image.Rect(96, 58, 160, 90), pix}}
		ctx := t.Context()
		if i%4 == 3 {
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
		}
		out, err := transcodeOverlays(ctx, t, src, op, layers)
		if err == nil {
			out.Release()
		} else if errs.KindOf(err) != errs.KindCanceled {
			t.Fatal(err)
		}
	}
	for i := range 4 {
		run(i)
	}
	mem, heap := vips.ReadMemStats().Mem, ffmpeg.ReadStats().HeapInUse
	for i := range 40 {
		run(i)
	}
	if s := ffmpeg.ReadStats(); s.Transcoders != 0 || s.Outputs != 0 {
		t.Errorf("%d transcoders and %d outputs alive", s.Transcoders, s.Outputs)
	}
	if m := vips.ReadMemStats().Mem; m != mem {
		t.Errorf("libvips tracked memory %d → %d bytes", mem, m)
	}
	if h := ffmpeg.ReadStats().HeapInUse; heap > 0 && h-heap > 2<<20 {
		t.Errorf("glibc heap in use grew by %d bytes over 40 transcodes", h-heap)
	}
}
