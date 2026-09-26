//go:build cgo

package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/config"
)

// videoRequests is a TestInfo subtest (it needs the running service): a
// video source through the imgproxy, Cloudinary-style option names and
// /info, on the full image; a clean 422 on slim.
func videoRequests(t *testing.T, base string) {
	t.Helper()
	const src = "/plain/video/clip-av1.mp4"
	if !codec.VideoAvailable() {
		for _, u := range []string{"/unsafe/rs:fit:160:90" + src + "@jpg", "/info/unsafe" + src} {
			r := get(t, base+u)
			if r.status != http.StatusUnprocessableEntity || !strings.Contains(string(r.body), "video") {
				t.Errorf("GET %s: %d %s, want 422 naming video", u, r.status, r.body)
			}
		}
		return
	}
	for _, tc := range []struct {
		options, frame string
	}{
		{"vts:1/rs:fit:160:90", "1.000s"},
		{"video_thumbnail_second:1.3/video_thumbnail_keyframes:1/rs:fit:160:90", "0.960s key"},
		{"vts:-0.5/rs:fit:160:90", "1.520s"},
		{"rs:fit:160:90", "0.480s key best"},
	} {
		r := get(t, base+"/unsafe/"+tc.options+src+"@jpg")
		checkJPEG(t, r, 160, 90)
		if got := r.header.Get("X-Xi-Video-Frame"); !strings.HasPrefix(got, tc.frame) {
			t.Errorf("%s: X-Xi-Video-Frame %q, want prefix %q", tc.options, got, tc.frame)
		}
	}
	// A rotated video comes out upright.
	checkJPEG(t, get(t, base+"/unsafe/vts:1/rs:fit:90:160/plain/video/clip-av1-rotate90.mp4@jpg"), 90, 160)

	// vm:0 drops the video fields; images never have them.
	for u, want := range map[string]bool{
		"/info/unsafe" + src:                        true,
		"/info/unsafe/vm:0" + src:                   false,
		"/info/unsafe/vm:1/plain/jpeg/baseline.jpg": false,
	} {
		r := get(t, base+u)
		if r.status != http.StatusOK {
			t.Fatalf("GET %s: %d %s", u, r.status, r.body)
		}
		var body map[string]any
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatal(err)
		}
		_, meta := body["video_meta"]
		_, streams := body["video_streams"]
		if meta != want || streams != want {
			t.Errorf("GET %s: video_meta %v, video_streams %v, want %v", u, meta, streams, want)
		}
	}
}

// TestCheckVideo: XI_VIDEO_ENABLED=true fails startup without FFmpeg; auto
// and false never do, and false turns video off in every build.
func TestCheckVideo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		enabled config.Tristate
		fail    bool
		on      bool
	}{
		{config.Auto, false, codec.VideoAvailable()},
		{config.True, !codec.VideoAvailable(), codec.VideoAvailable()},
		{config.False, false, false},
	} {
		cfg := config.Defaults()
		cfg.Video.Enabled = tc.enabled
		if err := checkVideo(&cfg); (err != nil) != tc.fail {
			t.Errorf("enabled=%v: checkVideo = %v, want failure %v", tc.enabled, err, tc.fail)
		}
		if got := videoSettings(&cfg).Enabled; got != tc.on {
			t.Errorf("enabled=%v: settings enabled = %v, want %v", tc.enabled, got, tc.on)
		}
	}
	cfg := config.Defaults()
	cfg.Video.Thumbnail.Second = "2.5"
	if s := videoSettings(&cfg); s.Second == nil || *s.Second != 2.5 {
		t.Errorf("second = %v, want 2.5", s.Second)
	}
	if s := videoSettings(new(config.Defaults())); s.Second != nil || s.Samples != 5 {
		t.Errorf("defaults: %+v", s)
	}
}
