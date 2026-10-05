//go:build cgo

package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// TestPlaygroundVideo (WP-127): with video transformations on, api/meta
// describes what this build writes, api/sign signs /video paths the
// public listener serves, api/probe describes a video and an audio source
// (which /info refuses), api/probe/result a transcoded body, and
// api/translate writes a /video URL in the video dialects.
func TestPlaygroundVideo(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Playground.Enabled, cfg.Playground.Uploads = true, config.True
	a := startVideo(t, cfg)
	admin := "http://" + a.admin.String() + "/_playground"
	public := "http://" + a.public.String()

	var m playground.Meta
	if err := json.Unmarshal(get(t, admin+"/api/meta").body, &m); err != nil {
		t.Fatal(err)
	}
	vt := m.VideoTransform
	if vt == nil || !vt.Probe || !slices.Contains(vt.Containers, "webm") || !slices.Contains(vt.Containers, "mp3") ||
		!slices.Contains(vt.Codecs, "vp9") || !slices.Contains(vt.Codecs, "pcm") || (vt.MP4Video != "h264" && vt.MP4Video != "av1") || vt.MaxOutputDuration <= 0 {
		t.Fatalf("video_transform %+v", vt)
	}
	if slices.Contains(vt.Codecs, "h264") != (vt.MP4Video == "h264") {
		t.Errorf("mp4 is %s with codecs %v", vt.MP4Video, vt.Codecs)
	}

	// Sign and render a /video URL.
	signed := postJSON(t, admin+"/api/sign", `{"mount":"imgproxy","path":"/video/w:160/vs:0.5/vd:1/plain/video/clip-vp9.webm@webm"}`)
	var sr playground.SignResponse
	if err := json.Unmarshal(signed.body, &sr); err != nil || !sr.Signed || !strings.HasPrefix(sr.URL, "/video/") {
		t.Fatalf("sign: %d %s", signed.status, signed.body)
	}
	res := get(t, admin+"/api/render?u="+url.QueryEscape(sr.URL))
	if res.status != http.StatusOK || res.ctype != "video/webm" {
		t.Fatalf("render: %d %s %.200s", res.status, res.ctype, res.body)
	}
	if direct := get(t, public+sr.URL); direct.status != http.StatusOK || !bytes.Equal(direct.body, res.body) {
		t.Errorf("public listener: %d", direct.status)
	}

	// Probe the result body.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, admin+"/api/probe/result", bytes.NewReader(res.body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "video/webm")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out playground.MediaInfo
	err = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || out.Video == nil || out.Video.Codec != "vp9" || out.Video.Width != 160 ||
		out.Duration < 0.9 || out.Duration > 1.1 || out.Size != int64(len(res.body)) || out.BitRate == 0 {
		t.Errorf("probe result: %d %+v (%v)", resp.StatusCode, out, err)
	}

	// Probe sources: a video with audio, and an audio file /info refuses.
	for _, tc := range []struct {
		source, audio string
		video         bool
		rate, ch      int
	}{
		{"xi://fixtures/video/bbb-h264-aac.mp4", "aac", true, 16000, 1},
		{"xi://fixtures/audio/tones-stereo.mp3", "mp3", false, 44100, 2},
		{"playground://samples/audio.mp3", "mp3", false, 44100, 2},
	} {
		got := postJSON(t, admin+"/api/probe", `{"source":"`+tc.source+`"}`)
		var mi playground.MediaInfo
		if err := json.Unmarshal(got.body, &mi); err != nil || got.status != http.StatusOK {
			t.Errorf("probe %s: %d %s", tc.source, got.status, got.body)
			continue
		}
		if (mi.Video != nil) != tc.video || mi.Audio == nil || mi.Audio.Codec != tc.audio || mi.Audio.SampleRate != tc.rate ||
			mi.Audio.Channels != tc.ch || mi.Duration <= 0 {
			t.Errorf("probe %s: %+v (video %+v audio %+v)", tc.source, mi, mi.Video, mi.Audio)
		}
	}
	if got := postJSON(t, admin+"/api/probe", `{"source":"xi://fixtures/jpeg/baseline.jpg"}`); got.status < 400 || got.status >= 500 {
		t.Errorf("probe of a JPEG: %d %s, want a 4xx", got.status, got.body)
	}
	if got := postJSON(t, admin+"/api/probe", `{"source":"xi://nope/a.mp4"}`); got.status < 400 || got.status >= 500 {
		t.Errorf("probe of an unknown source: %d %s, want a 4xx", got.status, got.body)
	}

	// Translate a /video URL.
	tr := postJSON(t, admin+"/api/translate", `{"mount":"imgproxy","path":"/video/vs:1/vd:2/rs:fit:640:0/plain/video/clip-vp9.webm@webm"}`)
	var trr playground.TranslateResponse
	if err := json.Unmarshal(tr.body, &trr); err != nil || tr.status != http.StatusOK {
		t.Fatalf("translate: %d %s", tr.status, tr.body)
	}
	for _, d := range []string{"imgproxy", "query"} {
		i := slices.IndexFunc(trr.Targets, func(x playground.TranslateTarget) bool { return x.Dialect == d })
		if i < 0 || trr.Targets[i].URL == "" || len(trr.Targets[i].Unsupported) > 0 {
			t.Errorf("translate %s: %+v", d, trr.Targets)
			continue
		}
		if got := get(t, public+trr.Targets[i].URL); got.status != http.StatusOK || got.ctype != "video/webm" {
			t.Errorf("translate %s: %s answered %d %s", d, trr.Targets[i].URL, got.status, got.ctype)
		}
	}
}
