//go:build cgo

package app

import (
	"bytes"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/image/webp"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// videoSigner signs with videoConfig's key: imgproxy paths and xi_sig
// queries.
type videoSigner struct{ ip, xi *sign.Signer }

func newVideoSigner(t *testing.T) videoSigner {
	t.Helper()
	keys, err := sign.ParseHexKeyring([]string{videoKey}, []string{videoSalt})
	if err != nil {
		t.Fatal(err)
	}
	ipScheme, err := sign.NewImgproxyScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := sign.NewSigner(ipScheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	xiScheme, err := sign.NewXIScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	xi, err := sign.NewSigner(xiScheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	return videoSigner{ip: ip, xi: xi}
}

// video returns the signed /video URL path of rest ("/w:320/plain/…").
func (s videoSigner) video(t *testing.T, rest string) string {
	t.Helper()
	sig, err := s.ip.Sign("/video" + rest)
	if err != nil {
		t.Fatal(err)
	}
	return "/video/" + sig + rest
}

// TestVideoURLs: the production /video and /q/video routes, signed (the
// default unsigned policy denies everything else), through the real
// router, server, media service and FFmpeg: containers, audio options,
// waveform pictures, ranges, and signatures that do not cross between
// image and video URLs.
func TestVideoURLs(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Video.Transform.UnsignedPolicy = config.Defaults().Video.Transform.UnsignedPolicy
	a := startVideo(t, cfg)
	base := "http://" + a.public.String()
	s := newVideoSigner(t)

	for _, tc := range []struct {
		name, rest  string
		ctype, xvc  string
		magic       []byte
		magicOffset int
	}{
		{
			"webm resize cut", "/rs:fill:160:160/g:ea/vs:0.5/vd:1/ki:0.5/plain/video/clip-vp9.webm@webm",
			"video/webm", "vp9",
			[]byte{0x1a, 0x45, 0xdf, 0xa3},
			0,
		},
		{
			"m4a from a video", "/sr:22050/ach:2/ab:96k/plain/video/bbb-h264-aac.mp4@m4a",
			"audio/mp4", "aac", []byte("ftyp"), 4,
		},
		{
			"mp3 loudnorm and fades", "/ln:1/fd:0.5:0.5/vd:2/plain/audio/tones-stereo.mp3@mp3",
			"audio/mpeg", "mp3", nil, 0,
		},
		{"flac", "/vol:-6dB/plain/audio/tones-stereo.mp3@flac", "audio/flac", "flac", []byte("fLaC"), 0},
		{"wav", "/sr:8000/ach:1/vd:1/plain/audio/tones-stereo.mp3@wav", "audio/wav", "pcm_s16le", []byte("RIFF"), 0},
		{"ogg", "/f:ogg/plain/audio/tones-stereo.mp3", "audio/ogg", "opus", []byte("OggS"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := get(t, base+s.video(t, tc.rest))
			if r.status != http.StatusOK {
				t.Fatalf("%d %s", r.status, r.body)
			}
			if r.ctype != tc.ctype || r.header.Get("X-Xi-Video-Codec") != tc.xvc || r.header.Get("Accept-Ranges") != "bytes" {
				t.Errorf("Content-Type %q, X-Xi-Video-Codec %q, Accept-Ranges %q", r.ctype, r.header.Get("X-Xi-Video-Codec"), r.header.Get("Accept-Ranges"))
			}
			if tc.magic != nil && !bytes.HasPrefix(r.body[tc.magicOffset:], tc.magic) {
				t.Errorf("body starts % x", r.body[:min(12, len(r.body))])
			}
		})
	}

	t.Run("waveform png and webp", func(t *testing.T) {
		r := get(t, base+s.video(t, "/wf:1:2266cc:ffffff/w:400/h:100/plain/audio/tones-stereo.mp3@png"))
		if r.status != http.StatusOK || r.ctype != "image/png" {
			t.Fatalf("png: %d %s %s", r.status, r.ctype, r.body)
		}
		img, err := png.Decode(bytes.NewReader(r.body))
		if err != nil || img.Bounds().Dx() != 400 || img.Bounds().Dy() != 100 {
			t.Fatalf("png %v %v", img.Bounds(), err)
		}
		if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0xffff {
			t.Errorf("background alpha %d, want opaque", alpha)
		}
		r = get(t, base+s.video(t, "/w:300/h:60/plain/video/bbb-h264-aac.mp4@webp"))
		if r.status != http.StatusOK || r.ctype != "image/webp" {
			t.Fatalf("webp: %d %s %s", r.status, r.ctype, r.body)
		}
		cfg, err := webp.DecodeConfig(bytes.NewReader(r.body))
		if err != nil || cfg.Width != 300 || cfg.Height != 60 {
			t.Fatalf("webp %+v %v", cfg, err)
		}
	})

	t.Run("range", func(t *testing.T) {
		u := base + s.video(t, "/w:320/vd:1/plain/video/clip-av1.mp4@mp4")
		full := get(t, u)
		if full.status != http.StatusOK || len(full.body) < 1000 {
			t.Fatalf("GET: %d, %d bytes", full.status, len(full.body))
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Range", "bytes=500-")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var part bytes.Buffer
		_, _ = part.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(part.Bytes(), full.body[500:]) {
			t.Fatalf("range: %d, %d bytes", resp.StatusCode, part.Len())
		}
	})

	t.Run("query dialect", func(t *testing.T) {
		q, err := s.xi.SignXIQuery("/video", "url=video/clip-vp8.webm&start=0.2&duration=0.5&width=160&format=webm&mute=1")
		if err != nil {
			t.Fatal(err)
		}
		r := get(t, base+"/q/video?"+q)
		if r.status != http.StatusOK || r.ctype != "video/webm" || r.header.Get("X-Xi-Video-Codec") != "vp9" {
			t.Fatalf("%d %s %s", r.status, r.ctype, r.body)
		}
	})

	t.Run("signatures", func(t *testing.T) {
		const rest = "/w:320/plain/video/clip-vp9.webm@webm"
		imgSigned, err := s.ip.SignImgproxyPath(rest)
		if err != nil {
			t.Fatal(err)
		}
		sig, _, _ := strings.Cut(strings.TrimPrefix(imgSigned, "/"), "/")
		for _, c := range []struct {
			name, path string
			want       int
		}{
			{"image signature on /video", "/video/" + sig + rest, http.StatusForbidden},
			{"unsigned", "/video/unsafe" + rest, http.StatusForbidden},
			{"tampered", strings.Replace(s.video(t, rest), "w:320", "w:640", 1), http.StatusForbidden},
			{"audio-only output of a silent video", s.video(t, "/plain/video/clip-vp9.webm@m4a"), http.StatusUnprocessableEntity},
			{"bad option", s.video(t, "/crf:99/plain/video/clip-vp9.webm"), http.StatusBadRequest},
		} {
			if r := get(t, base+c.path); r.status != c.want {
				t.Errorf("%s: %d %s, want %d", c.name, r.status, r.body, c.want)
			}
		}
	})
}
