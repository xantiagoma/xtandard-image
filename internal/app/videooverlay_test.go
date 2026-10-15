//go:build cgo

package app

import (
	"image"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

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

// joinedRenditions fetches a signed HLS master URL and returns each video
// rendition's init and media segments joined, by playlist name.
func joinedRenditions(t *testing.T, base, master string) map[string][]byte {
	t.Helper()
	m := get(t, base+master)
	if m.status != http.StatusOK {
		t.Fatalf("master: %d %s", m.status, m.body)
	}
	out := map[string][]byte{}
	for _, pl := range playlistURIs(m.body) {
		if strings.HasSuffix(pl, "/a.m3u8") {
			continue
		}
		media := get(t, base+pl)
		if media.status != http.StatusOK {
			t.Fatalf("%s: %d %s", pl, media.status, media.body)
		}
		prefix := pl[:strings.LastIndexByte(pl, '/')+1]
		var joined []byte
		for _, u := range playlistURIs(media.body) {
			r := get(t, base+prefix+u)
			if r.status != http.StatusOK {
				t.Fatalf("%s: %d %s", u, r.status, r.body)
			}
			joined = append(joined, r.body...)
		}
		out[pl[strings.LastIndexByte(pl, '/')+1:]] = joined
	}
	return out
}

// TestStreamOverlaysEndToEnd draws an image overlay and a timecode on an
// HLS stream (WP-131) through signed URLs: in every rendition the badge is
// in the frames of its window (1–4 s of the stream, across the 2 s
// segment boundaries) and nowhere else, at the same place relative to the
// frame (laid out on the 640×360 rendition, scaled on the 426×240 one),
// and the rest of the frame is the plain stream's.
func TestStreamOverlaysEndToEnd(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Video.Transform.SegmentDuration = 2 * time.Second
	a := startVideo(t, cfg)
	s := newVideoSigner(t)
	base := "http://" + a.public.String()
	const src = "/plain/video/stream-vp9-opus.webm@m3u8"
	badge := b64("xi://fixtures/watermark/badge.png")
	plain := joinedRenditions(t, base, s.video(t, "/st:hls:360:240/mu:1/q:90"+src))
	marked := joinedRenditions(t, base, s.video(t, "/st:hls:360:240/mu:1/q:90/wm:0.9:soea:8:8/wmu:"+badge+"/wms:48:0/ow:1:4/tc:hms:nowe:4:4:24"+src))
	// The badge, 48 px wide, 8 px from the bottom-right corner of the
	// 640×360 rendition; scaled by 426/640 on the other.
	for name, mark := range map[string]image.Rectangle{
		"v0.m3u8": image.Rect(584, 304, 632, 352),
		"v1.m3u8": image.Rect(389, 202, 421, 234),
	} {
		got, want := marked[name], plain[name]
		if len(got) == 0 || len(want) == 0 {
			t.Fatalf("%s missing: %d and %d bytes", name, len(got), len(want))
		}
		pk := ffprobePackets(t, got)
		if len(pk) != 165 {
			t.Fatalf("%s: %d frames, want 165", name, len(pk))
		}
		off := pk[0].pts
		inner := mark.Inset(4)
		untouched := image.Rect(mark.Min.X/4, mark.Min.Y/2, mark.Min.X/2, mark.Min.Y*3/4)
		for _, at := range []float64{0.5, 1.5, 2.5, 3.9, 4.6} {
			g, w := videoFrame(t, got, off+at), videoFrame(t, want, off+at)
			d := regionDiff(g, w, inner)
			if in := at > 1 && at < 4; in && d < 8 {
				t.Errorf("%s at %.1f s: the badge is missing (difference %.1f)", name, at, d)
			} else if !in && d > 3 {
				t.Errorf("%s at %.1f s, outside the window: the badge shows (difference %.1f)", name, at, d)
			}
			if d := regionDiff(g, w, untouched); d > 3 {
				t.Errorf("%s at %.1f s: the rest of the frame changed (difference %.1f)", name, at, d)
			}
		}
	}
}

// TestVideoOverlays serves image, text and timecode overlays end to end
// through signed production /video URLs (imgproxy watermark options with
// ow/of/tc, WP-122/124; overlay fetch through the source registry, libvips
// rendering, FFmpeg compositing) and checks extracted frames: the overlay
// is where and when it was asked for, the rest of the frame is the plain
// transcode's, and the frames inside the window match their goldens
// (testdata/golden/app/video-overlay-*.png, compared perceptually).
func TestVideoOverlays(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	a := startVideo(t, videoConfig(t, config.RoleAll, true))
	s := newVideoSigner(t)
	base := "http://" + a.public.String()
	// clip is the signed URL of the 320 px WebM cut with the given
	// options ("" for none).
	clip := func(opts string) string {
		if opts != "" {
			opts = "/" + opts
		}
		return base + s.video(t, "/w:320/vs:0.5/vd:1"+opts+"/plain/video/clip-vp9.webm@webm")
	}
	plain := get(t, clip(""))
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
		{"wmu", "wm:0.8:soea:8:8/wmu:" + badge + "/wms:48:0/ow:0.3:0.7", mark, []float64{0.4, 0.6}, []float64{0.1, 0.9}},
		{"wmu-fade", "wm:1:soea:8:8/wmu:" + badge + "/wms:48:0/of:0.4:0.4", mark, []float64{0.5}, nil},
		{"wmt", "wm:1:nowe:6:6/wmt:" + b64(hostile) + "/ow:0.2", image.Rect(6, 6, 200, 20), []float64{0.4, 0.8}, []float64{0.1}},
		{"timecode", "tc:hms:sowe:0:0:20", image.Rect(0, 150, 140, 180), []float64{0.2, 0.8}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := get(t, clip(tc.query))
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
	r := get(t, clip("wm:1:nowe:6:6/wmt:"+b64(hostile)))
	if r.status != http.StatusOK {
		t.Fatalf("hostile text: %d %s", r.status, r.body)
	}
	box := image.Rect(8, 8, 60, 16)
	if d := regionDiff(videoFrame(t, r.body, 0.2), videoFrame(t, r.body, 0.8), box); d > 4 {
		t.Errorf("the text changed between frames (difference %.1f): it was expanded", d)
	}

	// Markup that is not plain text styling is refused, not passed on.
	for _, bad := range []string{`<span font_file="/etc/passwd">x</span>`, `<img src="x"/>`} {
		r := get(t, clip("wm:1:ce/wmt:"+b64(bad)))
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", bad, r.status, r.body)
		}
	}
	// An overlay source outside the allowlist is 403, a missing one 404.
	for src, status := range map[string]int{
		"s3://bucket/w.png":                   http.StatusForbidden,
		"xi://fixtures/watermark/missing.png": http.StatusNotFound,
	} {
		r := get(t, clip("wm:1:ce/wmu:"+b64(src)))
		if r.status != status {
			t.Errorf("overlay %s: %d, want %d", src, r.status, status)
		}
	}
	// Overlays need a video stream: an audio-only output refuses them.
	if r := get(t, base+s.video(t, "/wm:1:ce/wmu:"+badge+"/plain/video/bbb-h264-aac.mp4@m4a")); r.status != http.StatusBadRequest {
		t.Errorf("overlay on m4a: %d %s, want 400", r.status, r.body)
	}
	// The query dialect carries the same options.
	q, err := s.xi.SignXIQuery("/video", "url=video/clip-vp9.webm&w=320&vs=0.5&vd=1&f=webm&wm=0.8,soea,8,8&wmu="+badge+"&wms=48,0&ow=0.3,0.7")
	if err != nil {
		t.Fatal(err)
	}
	if r := get(t, base+"/q/video?"+q); r.status != http.StatusOK {
		t.Fatalf("query overlay: %d %s", r.status, r.body)
	} else if d := regionDiff(videoFrame(t, r.body, 0.5), videoFrame(t, plain.body, 0.5), mark); d < 8 {
		t.Errorf("query overlay missing (difference %.1f)", d)
	}
}
