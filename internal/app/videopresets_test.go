//go:build cgo

package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// TestVideoPresetsRestricted: XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=restricted
// end to end (WP-132), through the real router, server, media service and
// FFmpeg: unsigned video URLs are served only as their video presets make
// them, streams and raw delivery only from presets, signed URLs as before,
// and refusals are 403 before anything is fetched (404 with the imgproxy
// status compatibility).
func TestVideoPresetsRestricted(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Security.UnsignedPolicy = config.UnsignedRestricted // the image allowlists do not admit video
	tr := &cfg.Video.Transform
	tr.UnsignedPolicy = config.UnsignedRestricted
	tr.Presets = map[string]string{
		"small": "w:160/vd:1/f:webm",
		"hls":   "st:hls:240/vd:2",
		"raw":   "raw:1",
		"cut":   "vd:1",
	}
	tr.UnsignedAllowedOptions = []string{"start"}
	tr.SegmentDuration = time.Second
	a := startVideo(t, cfg)
	base := "http://" + a.public.String()
	s := newVideoSigner(t)
	const src = "/plain/video/clip-vp9.webm"

	for _, tc := range []struct {
		name, url string
		status    int
		ctype     string
	}{
		{"preset", "/video/unsafe/pr:small" + src, http.StatusOK, "video/webm"},
		{"preset and an allowed option", "/video/unsafe/pr:small/vs:0.5" + src, http.StatusOK, "video/webm"},
		{"preset and another option", "/video/unsafe/pr:small/w:320" + src, http.StatusForbidden, ""},
		{"no preset", "/video/unsafe/w:160/vd:1" + src + "@webm", http.StatusForbidden, ""},
		{"stream preset", "/video/unsafe/pr:hls" + src + "@m3u8", http.StatusOK, "application/vnd.apple.mpegurl"},
		{"stream without a stream preset", "/video/unsafe/pr:cut" + src + "@m3u8", http.StatusForbidden, ""},
		{"raw preset", "/video/unsafe/pr:raw" + src, http.StatusOK, "video/webm"},
		{"raw without a raw preset", "/video/unsafe/pr:small/raw:1" + src, http.StatusForbidden, ""},
		{"query preset", "/q/video?url=video/clip-vp9.webm&preset=small", http.StatusOK, "video/webm"},
		{"query no preset", "/q/video?url=video/clip-vp9.webm&width=160", http.StatusForbidden, ""},
		{"signed, any options", s.video(t, "/w:200/vd:1"+src+"@webm"), http.StatusOK, "video/webm"},
		{"image restricted policy unchanged", "/unsafe/rs:fit:641:0/plain/jpeg/baseline.jpg", http.StatusForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := get(t, base+tc.url)
			if r.status != tc.status {
				t.Fatalf("%d %s, want %d", r.status, r.body, tc.status)
			}
			if tc.status == http.StatusForbidden && !strings.Contains(string(r.body), "unsigned request not allowed") {
				t.Errorf("body %s", r.body)
			}
			if tc.ctype != "" && r.ctype != tc.ctype {
				t.Errorf("Content-Type %q, want %q", r.ctype, tc.ctype)
			}
		})
	}

	// The parts of a preset's stream play without a signature.
	master := get(t, base+"/video/unsafe/pr:hls"+src+"@m3u8")
	uris := playlistURIs(master.body)
	if len(uris) == 0 {
		t.Fatalf("master without URIs: %s", master.body)
	}
	media := get(t, base+uris[0])
	parts := playlistURIs(media.body)
	if media.status != http.StatusOK || len(parts) < 2 {
		t.Fatalf("media playlist: %d %s", media.status, media.body)
	}
	for _, p := range parts[:2] {
		u := p
		if !strings.HasPrefix(u, "/") {
			ref, err := url.Parse(uris[0])
			if err != nil {
				t.Fatal(err)
			}
			u = ref.ResolveReference(&url.URL{Path: p}).String()
		}
		if r := get(t, base+u); r.status != http.StatusOK {
			t.Fatalf("part %s: %d %s", u, r.status, r.body)
		}
	}
}

// TestVideoPresetsCompatStatus: the restricted refusal is 404 with
// XI_COMPAT_IMGPROXY_STATUS, like the image unsigned policy's.
func TestVideoPresetsCompatStatus(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Compat.ImgproxyStatus = true
	cfg.Video.Transform.UnsignedPolicy = config.UnsignedRestricted
	cfg.Video.Transform.Presets = map[string]string{"small": "w:160/vd:1"}
	a := startVideo(t, cfg)
	base := "http://" + a.public.String()
	if r := get(t, base+"/video/unsafe/w:160/plain/video/clip-vp9.webm"); r.status != http.StatusNotFound {
		t.Errorf("no preset: %d %s, want 404", r.status, r.body)
	}
	if r := get(t, base+"/video/unsafe/pr:small/plain/video/clip-vp9.webm"); r.status != http.StatusOK {
		t.Errorf("preset: %d %s, want 200", r.status, r.body)
	}
}

// TestVideoPresetsStartup: bad video presets fail startup naming the
// variable and the preset.
func TestVideoPresetsStartup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, def, want string
	}{
		{"unknown option", "w:160/blur:2", "unknown option"},
		{"bad value", "crf:99", "crf"},
		{"over the output pixels", "rs:force:3840:2161", "max_output_pixels"},
		{"over the frame rate", "fps:120", "max_fps"},
		{"over the output duration", "vd:301", "max_output_duration"},
		{"cut over the output duration", "vs:10/ve:400", "max_output_duration"},
		{"delivery option", "cb:x", "delivery option"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Defaults()
			cfg.Video.Transform.Presets = map[string]string{"bad": tc.def}
			_, err := videoPresets(&cfg)
			if err == nil || !strings.Contains(err.Error(), "XI_VIDEO_TRANSFORM_PRESETS") ||
				!strings.Contains(err.Error(), `"bad"`) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want XI_VIDEO_TRANSFORM_PRESETS, preset \"bad\" and %q", err, tc.want)
			}
		})
	}
	cfg := config.Defaults()
	cfg.Video.Transform.Presets = map[string]string{"web": "rs:fit:1280:0/q:60", "default": "ki:2"}
	if st, err := videoPresets(&cfg); err != nil || st.Len() != 2 {
		t.Errorf("valid presets: %v", err)
	}
}
