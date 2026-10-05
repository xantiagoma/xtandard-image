//go:build cgo

package app

import (
	"encoding/json"
	"image"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// Signing material of TestVideoDialects.
const (
	vdXIKey     = "736563726574" // "secret"
	vdXISalt    = "68656c6c6f"   // "hello"
	vdCloudKey  = "cloudinary-api-secret"
	vdImageKitK = "private_imagekit_key"
)

// ffprobeResult is ffprobe's view of a response body.
type ffprobeResult struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
}

func ffprobeBody(t *testing.T, data []byte) ffprobeResult {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	f := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-show_entries",
		"stream=codec_type,codec_name,width,height:format=format_name,duration", "-of", "json", f).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var p ffprobeResult
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestVideoDialects runs the video URL syntaxes of Cloudflare
// (/cdn-cgi/media/), Cloudinary (/video/upload/) and ImageKit (WP-125)
// through the whole service with signed URLs, video transformations
// enabled and unsigned ones denied (the default), and checks each output
// with ffprobe: container, codecs, size and duration, and that Cloudinary
// and ImageKit layers are drawn as video overlays (WP-124) where and when
// they ask.
func TestVideoDialects(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	fixtures := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	yml := `server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: "127.0.0.2:0"
signing:
  keys: ["` + vdXIKey + `"]
  salts: ["` + vdXISalt + `"]
  required: "false"
security:
  unsigned_policy: allow
observability:
  debug_headers: true
sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(fixtures) + `
dialects:
  cloudflare:
    enabled: true
    source: fixtures
  cloudinary:
    enabled: true
    source: fixtures
    keys: ["` + vdCloudKey + `"]
  imagekit:
    enabled: true
    source: fixtures
    keys: ["` + vdImageKitK + `"]
video:
  transform:
    enabled: true
    workers: 2
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{ConfigFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a := start(t, &cfg, hooks{keepVips: true})
	base := "http://" + a.public.String()

	xiKeys, err := sign.ParseHexKeyring([]string{vdXIKey}, []string{vdXISalt})
	if err != nil {
		t.Fatal(err)
	}
	xiScheme, err := sign.NewXIScheme(cfg.Signing.Size)
	if err != nil {
		t.Fatal(err)
	}
	xi, err := sign.NewSigner(xiScheme, xiKeys)
	if err != nil {
		t.Fatal(err)
	}
	cld, err := sign.NewSigner(sign.NewCloudinaryScheme(false, false), sign.NewKeyring(sign.NewKey([]byte(vdCloudKey), nil)))
	if err != nil {
		t.Fatal(err)
	}
	ik, err := sign.NewSigner(sign.NewImageKitScheme(), sign.NewKeyring(sign.NewKey([]byte(vdImageKitK), nil)))
	if err != nil {
		t.Fatal(err)
	}
	cloudflare := func(p string) string { // p after /cdn-cgi/media
		q, err := xi.SignXIQuery(p, "")
		if err != nil {
			t.Fatal(err)
		}
		return "/cdn-cgi/media" + p + "?" + q
	}
	cloudinary := func(tx, id string) string {
		s, err := cld.SignCloudinary(tx + "/" + id)
		if err != nil {
			t.Fatal(err)
		}
		return "/cloudinary/demo/video/upload/" + s + "/" + tx + "/" + id
	}
	imagekit := func(p, query string) string { // p after /imagekit
		q, err := ik.SignImageKitQuery(p, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		return "/imagekit" + p + "?" + q
	}

	mp4Codec := "av1"
	if ffmpeg.TranscodeCaps().Video.H264 {
		mp4Codec = "h264"
	}
	type want struct {
		ctype, format, video, audio string
		w, h                        int
		duration                    float64 // ±0.15 s
	}
	for _, tc := range []struct {
		name, url string
		accept    string
		want      want
	}{
		{
			name: "cloudflare video", url: cloudflare("/mode=video,time=0.5s,duration=1s,width=320,height=320,fit=cover,audio=false/video/clip-vp9.webm"),
			want: want{ctype: "video/mp4", format: "mp4", video: mp4Codec, w: 320, h: 320, duration: 1},
		},
		{
			name: "cloudflare audio", url: cloudflare("/mode=audio,duration=1s/video/bbb-h264-aac.mp4"),
			want: want{ctype: "audio/mp4", format: "mp4", audio: "aac", duration: 1},
		},
		{
			name: "cloudinary trim, fill and webm", url: cloudinary("so_0.5,du_1/c_fill,g_north,w_240,h_240/vc_vp9,f_webm", "video/clip-av1.mp4"),
			want: want{ctype: "video/webm", format: "webm", video: "vp9", w: 240, h: 240, duration: 1},
		},
		{
			name: "cloudinary f_auto picks webm", url: cloudinary("w_320,du_1,f_auto,ac_none", "video/clip-av1.mp4"), accept: "video/webm,*/*",
			want: want{ctype: "video/webm", format: "webm", video: "vp9", w: 320, h: 180, duration: 1},
		},
		{
			name: "cloudinary audio only", url: cloudinary("so_0.2,eo_0.8,vc_none", "video/bbb-h264-aac.mp4"),
			want: want{ctype: "audio/mp4", format: "mp4", audio: "aac", duration: 0.6},
		},
		{
			name: "imagekit trim and crop", url: imagekit("/tr:so-0.2,du-1,w-200,h-100,cm-extract,fo-bottom_right:w-100/video/clip-vp9.webm", ""),
			want: want{ctype: "video/mp4", format: "mp4", video: mp4Codec, w: 100, h: 50, duration: 1},
		},
		{
			name: "imagekit ik-video.mp4", url: imagekit("/video/clip-vp9.webm/ik-video.mp4", "tr=du-1,w-160,ac-none"),
			want: want{ctype: "video/mp4", format: "mp4", video: mp4Codec, w: 160, h: 90, duration: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+tc.url, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: %d %s", tc.url, resp.StatusCode, body)
			}
			if tc.accept != "" && resp.Header.Get("Vary") != "Accept" {
				t.Errorf("Vary %q, want Accept", resp.Header.Get("Vary"))
			}
			if got := resp.Header.Get("Content-Type"); got != tc.want.ctype {
				t.Errorf("Content-Type %q, want %q", got, tc.want.ctype)
			}
			p := ffprobeBody(t, body)
			if !strings.Contains(p.Format.FormatName, tc.want.format) {
				t.Errorf("format %q, want %q", p.Format.FormatName, tc.want.format)
			}
			var video, audio string
			var w, h int
			for _, s := range p.Streams {
				switch s.CodecType {
				case "video":
					video, w, h = s.CodecName, s.Width, s.Height
				case "audio":
					audio = s.CodecName
				}
			}
			if video != tc.want.video || audio != tc.want.audio || w != tc.want.w || h != tc.want.h {
				t.Errorf("streams video %q %dx%d audio %q, want %q %dx%d %q", video, w, h, audio,
					tc.want.video, tc.want.w, tc.want.h, tc.want.audio)
			}
			d, _ := strconv.ParseFloat(p.Format.Duration, 64)
			if d < tc.want.duration-0.15 || d > tc.want.duration+0.15 {
				t.Errorf("duration %v, want %v", d, tc.want.duration)
			}
		})
	}

	// Unsigned video transformations are refused before any fetch; frame
	// and sprite modes are images and follow the image unsigned policy.
	for _, tc := range []struct {
		name, url string
		status    int
		ctype     string
	}{
		{"cloudflare unsigned video", "/cdn-cgi/media/mode=video,duration=1s/video/clip-vp9.webm", http.StatusForbidden, ""},
		{"cloudinary unsigned video", "/cloudinary/demo/video/upload/du_1/video/clip-vp9.mp4", http.StatusForbidden, ""},
		{"imagekit unsigned video", "/imagekit/video/clip-vp9.webm?tr=du-1", http.StatusForbidden, ""},
		{"cloudflare frame", "/cdn-cgi/media/mode=frame,time=1s,width=160,format=png/video/clip-vp9.webm", http.StatusOK, "image/png"},
		{"cloudflare spritesheet", "/cdn-cgi/media/mode=spritesheet,imageCount=4,width=80/video/clip-vp9.webm", http.StatusOK, "image/jpeg"},
		{"cloudinary thumbnail", "/cloudinary/demo/video/upload/so_1,w_160,f_jpg/video/clip-vp9.webm", http.StatusOK, "image/jpeg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := get(t, base+tc.url)
			if r.status != tc.status {
				t.Fatalf("%d %s, want %d", r.status, r.body, tc.status)
			}
			if tc.ctype != "" && r.ctype != tc.ctype {
				t.Errorf("Content-Type %q, want %q", r.ctype, tc.ctype)
			}
		})
	}

	// Overlays (WP-124) through each dialect's layer syntax: the badge,
	// 48 px wide, 8 px from the bottom-right corner of the 320×180 output,
	// from 0.3 s to 0.7 s, drawn over the plain transcode of the same URL
	// without the layer. Cloudflare Media Transformations has no overlays.
	mark := image.Rect(264, 124, 312, 172)
	untouched := image.Rect(8, 60, 120, 120)
	for _, tc := range []struct {
		name, plain, overlay string
	}{
		{
			name:    "cloudinary layer",
			plain:   cloudinary("du_1,w_320,f_webm", "video/clip-vp9.webm"),
			overlay: cloudinary("du_1,w_320,f_webm/l_watermark:badge.png,w_48/fl_layer_apply,g_south_east,x_8,y_8,so_0.3,eo_0.7", "video/clip-vp9.webm"),
		},
		{
			name:    "imagekit layer",
			plain:   imagekit("/video/clip-vp9.webm", "tr=du-1,w-320,f-webm"),
			overlay: imagekit("/video/clip-vp9.webm", "tr=du-1,w-320,f-webm,l-image,i-watermark@@badge.png,w-48,lx-N8,ly-N8,lso-0.3,leo-0.7,l-end"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plain, over := get(t, base+tc.plain), get(t, base+tc.overlay)
			if plain.status != http.StatusOK || over.status != http.StatusOK {
				t.Fatalf("plain %d %s, overlay %d %s", plain.status, plain.body, over.status, over.body)
			}
			for _, sec := range []float64{0.4, 0.6} {
				got, want := videoFrame(t, over.body, sec), videoFrame(t, plain.body, sec)
				if d := regionDiff(got, want, mark); d < 8 {
					t.Errorf("at %g s the overlay is missing (difference %.1f)", sec, d)
				}
				if d := regionDiff(got, want, untouched); d > 3 {
					t.Errorf("at %g s the rest of the frame changed (difference %.1f)", sec, d)
				}
			}
			for _, sec := range []float64{0.1, 0.9} {
				if d := regionDiff(videoFrame(t, over.body, sec), videoFrame(t, plain.body, sec), mark); d > 3 {
					t.Errorf("at %g s, outside its window, the overlay shows (difference %.1f)", sec, d)
				}
			}
		})
	}
	// Overlays on an audio-only output are refused.
	if r := get(t, base+cloudinary("vc_none/l_watermark:badge.png/fl_layer_apply", "video/bbb-h264-aac.mp4")); r.status != http.StatusBadRequest {
		t.Errorf("overlay on audio: %d %s, want 400", r.status, r.body)
	}
}
