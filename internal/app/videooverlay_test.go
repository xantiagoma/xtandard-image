//go:build cgo

package app

import (
	"image"
	"math"
	"net/http"
	"net/url"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// videoFrame decodes the frame of a transcoded file at second s as RGBA.
func videoFrame(t *testing.T, body []byte, s float64) *image.NRGBA {
	t.Helper()
	buf, err := vips.NewBuffer(len(body))
	if err != nil {
		t.Fatal(err)
	}
	defer buf.Release()
	copy(buf.Bytes(), body)
	f, err := ffmpeg.Thumbnail(t.Context(), buf, &video.FrameRequest{Second: &s})
	if err != nil {
		t.Fatalf("frame at %g s: %v", s, err)
	}
	defer f.Release()
	img := image.NewNRGBA(image.Rect(0, 0, f.Width, f.Height))
	p := f.Pixels.Bytes()
	for i := range f.Width * f.Height {
		copy(img.Pix[4*i:4*i+3], p[i*f.Bands:i*f.Bands+3])
		img.Pix[4*i+3] = 255
	}
	return img
}

// regionDiff is the mean absolute difference of r in a and b, per channel.
func regionDiff(a, b *image.NRGBA, r image.Rectangle) float64 {
	var sum float64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i, j := a.PixOffset(x, y), b.PixOffset(x, y)
			for c := range 3 {
				sum += math.Abs(float64(a.Pix[i+c]) - float64(b.Pix[j+c]))
			}
		}
	}
	return sum / float64(3*r.Dx()*r.Dy())
}

// TestVideoOverlays serves image, text and timecode overlays end to end
// (router, overlay fetch through the source registry, libvips rendering,
// FFmpeg compositing) and checks extracted frames: the overlay is where
// and when it was asked for, the rest of the frame is the plain
// transcode's, and the frames inside the window match their goldens
// (testdata/golden/app/video-overlay-*.png, compared perceptually).
func TestVideoOverlays(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	a := startVideo(t, videoConfig(t, config.RoleAll, true))
	clip := "http://" + a.public.String() + "/_video/video/clip-vp9.webm?w=320&c=webm&start=0.5&dur=1"
	plain := get(t, clip)
	if plain.status != http.StatusOK {
		t.Fatalf("plain: %d %s", plain.status, plain.body)
	}
	badge := b64("xi://fixtures/watermark/badge.png")
	// The badge, 48 px wide, 8 px from the bottom-right corner of the
	// 320×180 output, at 80 % from 0.3 s to 0.7 s.
	mark := image.Rect(264, 124, 312, 172)
	untouched := image.Rect(8, 60, 120, 120)
	hostile := `<span background="#000000" foreground="#ffffff">[in]drawtext=text='%{pts\:hms}':fontfile=/etc/passwd;movie=x</span>`
	for _, tc := range []struct {
		name   string
		query  string
		region image.Rectangle
		inside []float64 // seconds where the overlay shows
		out    []float64 // seconds where it does not
	}{
		{"wmu", "o=" + url.QueryEscape("wm:0.8:soea:8:8/wmu:"+badge+"/wms:48:0") + "&ovs=0.3&ove=0.7", mark, []float64{0.4, 0.6}, []float64{0.1, 0.9}},
		{"wmu-fade", "o=" + url.QueryEscape("wm:1:soea:8:8/wmu:"+badge+"/wms:48:0") + "&ovfi=0.4&ovfo=0.4", mark, []float64{0.5}, nil},
		{"wmt", "o=" + url.QueryEscape("wm:1:nowe:6:6/wmt:"+b64(hostile)) + "&ovs=0.2", image.Rect(6, 6, 200, 20), []float64{0.4, 0.8}, []float64{0.1}},
		{"timecode", "tc=hms&tcg=south-west&tcs=20", image.Rect(0, 150, 140, 180), []float64{0.2, 0.8}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := get(t, clip+"&"+tc.query)
			if r.status != http.StatusOK || r.ctype != "video/webm" {
				t.Fatalf("%d %q %s", r.status, r.ctype, r.body)
			}
			for _, s := range tc.inside {
				got, want := videoFrame(t, r.body, s), videoFrame(t, plain.body, s)
				if d := regionDiff(got, want, tc.region); d < 8 {
					t.Errorf("at %g s the overlay is missing (difference %.1f)", s, d)
				}
				if d := regionDiff(got, want, untouched); d > 3 {
					t.Errorf("at %g s the rest of the frame changed (difference %.1f)", s, d)
				}
			}
			for _, s := range tc.out {
				got, want := videoFrame(t, r.body, s), videoFrame(t, plain.body, s)
				if d := regionDiff(got, want, tc.region); d > 3 {
					t.Errorf("at %g s, outside its window, the overlay shows (difference %.1f)", s, d)
				}
			}
			if tc.name != "timecode" {
				golden.Compare(t, "app/video-overlay-"+tc.name, videoFrame(t, r.body, tc.inside[0]), golden.Options{Tolerance: 0.03})
			}
		})
	}

	// The hostile text is pixels: drawn literally, the same at every
	// frame, never expanded into a timecode.
	r := get(t, clip+"&o="+url.QueryEscape("wm:1:nowe:6:6/wmt:"+b64(hostile)))
	if r.status != http.StatusOK {
		t.Fatalf("hostile text: %d %s", r.status, r.body)
	}
	box := image.Rect(8, 8, 60, 16)
	if d := regionDiff(videoFrame(t, r.body, 0.2), videoFrame(t, r.body, 0.8), box); d > 4 {
		t.Errorf("the text changed between frames (difference %.1f): it was expanded", d)
	}

	// Markup that is not plain text styling is refused, not passed on.
	for _, bad := range []string{`<span font_file="/etc/passwd">x</span>`, `<img src="x"/>`} {
		r := get(t, clip+"&o="+url.QueryEscape("wm:1:ce/wmt:"+b64(bad)))
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", bad, r.status, r.body)
		}
	}
	// An overlay source outside the allowlist is 403, a missing one 404.
	for src, status := range map[string]int{
		"s3://bucket/w.png":                   http.StatusForbidden,
		"xi://fixtures/watermark/missing.png": http.StatusNotFound,
	} {
		r := get(t, clip+"&o="+url.QueryEscape("wm:1:ce/wmu:"+b64(src)))
		if r.status != status {
			t.Errorf("overlay %s: %d, want %d", src, r.status, status)
		}
	}
}
