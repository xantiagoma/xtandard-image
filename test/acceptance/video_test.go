//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestVideoTransform: video transformations, overlays and audio (WP-122,
// WP-123, WP-124)
// against the image under test. scripts/acceptance.sh enables them
// (XI_ACC_VIDEO=true) on an image with FFmpeg (full, gpl); there signed
// /video and /q/video URLs transcode, cut ranges and draw waveforms, and
// unsigned ones are 403 (XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=deny, the
// default). On slim they are off, and every video URL answers 404.
func TestVideoTransform(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	k := loadKeys(t)
	video := func(rest string) string {
		// The signature covers "/video" + the path after it.
		signed := k.sign("/video" + rest)
		sig, _, _ := strings.Cut(strings.TrimPrefix(signed, "/"), "/")
		return app + "/video/" + sig + rest
	}
	const clip = "/plain/local:///video/clip-vp9.webm"
	if os.Getenv("XI_ACC_VIDEO") != "true" {
		get(t, video("/w:320"+clip+"@webm"), nil).wantStatus(t, http.StatusNotFound)
		t.Log("video transformations off (no FFmpeg): /video answers 404")
		return
	}

	for _, c := range []struct {
		name, rest, ctype string
		magic             []byte
		offset            int
	}{
		{"webm cut and fill", "/rs:fill:320:180/g:ea/vs:0.5/vd:1" + clip + "@webm", "video/webm", []byte{0x1a, 0x45, 0xdf, 0xa3}, 0},
		{"mp4", "/w:320/vd:1/fd:0.2:0.2" + clip + "@mp4", "video/mp4", []byte("ftyp"), 4},
		{"m4a from a video", "/sr:22050/ach:1/plain/local:///video/bbb-h264-aac.mp4@m4a", "audio/mp4", []byte("ftyp"), 4},
		{"mp3 loudnorm", "/ln:1/vd:2/plain/local:///audio/tones-stereo.mp3@mp3", "audio/mpeg", nil, 0},
		{"flac", "/vol:-6dB/plain/local:///audio/tones-stereo.mp3@flac", "audio/flac", []byte("fLaC"), 0},
		{"wav", "/sr:8000/vd:1/plain/local:///audio/tones-stereo.mp3@wav", "audio/wav", []byte("RIFF"), 0},
		{"image overlay with a window", "/w:320/vd:1/wm:0.8:soea:8:8/wmu:" + base64.RawURLEncoding.EncodeToString([]byte("local:///watermark/badge.png")) +
			"/wms:48:0/ow:0.2:0.8/of:0.1:0.1/tc:hms" + clip + "@webm", "video/webm", []byte{0x1a, 0x45, 0xdf, 0xa3}, 0},
		{"waveform png", "/wf:1:2266cc:ffffff/w:400/h:100/plain/local:///audio/tones-stereo.mp3@png", "image/png", []byte("\x89PNG"), 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := get(t, video(c.rest), nil)
			r.wantStatus(t, http.StatusOK)
			if ct := r.Header.Get("Content-Type"); ct != c.ctype || r.Header.Get("Accept-Ranges") != "bytes" {
				t.Errorf("Content-Type %q, Accept-Ranges %q; want %s, bytes", ct, r.Header.Get("Accept-Ranges"), c.ctype)
			}
			if c.magic != nil && (len(r.Body) < c.offset+len(c.magic) || !bytes.Equal(r.Body[c.offset:c.offset+len(c.magic)], c.magic)) {
				t.Errorf("body starts % x", r.Body[:min(len(r.Body), 12)])
			}
		})
	}

	t.Run("range", func(t *testing.T) {
		t.Parallel()
		u := video("/w:256/vd:1" + clip + "@webm")
		full := get(t, u, nil)
		full.wantStatus(t, http.StatusOK)
		part := get(t, u, http.Header{"Range": {"bytes=0-99"}})
		part.wantStatus(t, http.StatusPartialContent)
		if !bytes.Equal(part.Body, full.Body[:100]) {
			t.Errorf("range body differs: %d bytes", len(part.Body))
		}
	})

	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		get(t, app+"/video/unsafe/w:320"+clip+"@webm", nil).wantStatus(t, http.StatusForbidden)
		// An image signature does not sign the same path as a video.
		get(t, app+"/video"+k.sign("/w:320"+clip+"@webm"), nil).wantStatus(t, http.StatusForbidden)
		get(t, video("/plain/local:///video/clip-vp9.webm@m4a"), nil).wantStatus(t, http.StatusUnprocessableEntity)
		get(t, video("/crf:99"+clip), nil).wantStatus(t, http.StatusBadRequest)
	})
}
