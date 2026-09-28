//go:build cgo

package app

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
)

// manyFrameGIF encodes an animated GIF of frames w×h frames, each a
// different shade so no frame is a duplicate.
func manyFrameGIF(t *testing.T, w, h, frames int) []byte {
	t.Helper()
	pal := make(color.Palette, 0, 256)
	for i := range uint8(255) {
		pal = append(pal, color.RGBA{R: i, G: 255 - i, B: 128, A: 255})
	}
	g := &gif.GIF{LoopCount: 0}
	for i := range frames {
		img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := range h {
			for x := range w {
				img.SetColorIndex(x, y, uint8((x/8+y/8+i)%len(pal))) //nolint:gosec // < len(pal) = 255
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 4)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestAnimationLimitsEndToEnd (WP-98) runs a 400×300 GIF of 60 frames
// (7.2 MP in all, 0.12 MP a frame) through the real service with
// XI_LIMITS_MAX_SRC_PIXELS at 5 MP, the shape of cards.gif against the
// 50 MP default: the per-frame limit replaces the total for an animation
// (imgproxy), 0 turns it off, XI_LIMITS_MAX_ANIMATION_PIXELS bounds the
// total, and every 422 names the limit and the numbers.
func TestAnimationLimitsEndToEnd(t *testing.T) {
	t.Parallel()
	src := manyFrameGIF(t, 400, 300, 60)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(src)
	}))
	t.Cleanup(origin.Close)
	ou, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(ou.Port(), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	ports := []uint16{uint16(port)}
	type answer struct {
		path, status, contentType, body string
		animated                        bool
	}
	webp := func(status int, body string) answer {
		return answer{path: "/insecure/plain/" + origin.URL + "/a.gif@webp", status: strconv.Itoa(status), contentType: "image/webp", body: body, animated: status == http.StatusOK}
	}
	jpg := answer{path: "/insecure/plain/" + origin.URL + "/a.gif@jpg", status: "200", contentType: "image/jpeg"}
	info := answer{path: "/info/insecure/plain/" + origin.URL + "/a.gif", status: "200", contentType: "application/json"}
	for _, tc := range []struct {
		name string
		env  []string
		want []answer
	}{
		{"frame limit replaces the total", nil, []answer{webp(200, ""), jpg, info}},
		{
			"frame limit off: the total applies to the frames loaded",
			[]string{"XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS=0"},
			[]answer{webp(422, "source has 60 frames of 400×300 (7.2 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 5 MP"), jpg, info},
		},
		{
			"animation total cap",
			[]string{"XI_LIMITS_MAX_ANIMATION_PIXELS=6000000"},
			[]answer{webp(422, "source has 60 frames of 400×300 (7.2 MP); limit XI_LIMITS_MAX_ANIMATION_PIXELS is 6 MP"), jpg},
		},
		{
			"frame over the frame limit",
			[]string{"XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS=100000"},
			[]answer{webp(422, "source frame is 400×300 (120000 pixels); limit XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS is 100000 pixels"), jpg},
		},
		{
			"too many frames",
			[]string{"XI_LIMITS_MAX_ANIMATION_FRAMES=50"},
			[]answer{webp(422, "source has 60 frames; limit XI_LIMITS_MAX_ANIMATION_FRAMES is 50")},
		},
		{
			"imgproxy status compat keeps 422 and the message",
			[]string{"XI_COMPAT_IMGPROXY_STATUS=true", "XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS=0"},
			[]answer{webp(422, "source has 60 frames of 400×300 (7.2 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 5 MP")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, _, err := config.Load(config.LoadOptions{Environ: append([]string{
				"XI_DEV_MODE=true",
				"XI_SERVER_ADDR=127.0.0.1:0",
				"XI_ADMIN_ADDR=127.0.0.2:0",
				"XI_SERVER_SHUTDOWN_DELAY=0s",
				"XI_SECURITY_UNSIGNED_POLICY=allow",
				"XI_LIMITS_MAX_SRC_PIXELS=5000000",
			}, tc.env...), ConfigFile: os.DevNull})
			if err != nil {
				t.Fatal(err)
			}
			a := start(t, &cfg, hooks{policy: &ssrf.Policy{
				AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
				Ports:      ports,
			}})
			for _, w := range tc.want {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+a.public.String()+w.path, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if got := strconv.Itoa(resp.StatusCode); got != w.status {
					t.Errorf("%s: status %s, want %s (body %q)", w.path, got, w.status, body)
					continue
				}
				if w.body != "" {
					if got := strings.TrimSpace(string(body)); got != w.body {
						t.Errorf("%s: body %q, want %q", w.path, got, w.body)
					}
					continue
				}
				if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, w.contentType) {
					t.Errorf("%s: Content-Type %q, want %s", w.path, ct, w.contentType)
				}
				if w.animated && !bytes.Contains(body[:min(len(body), 64)], []byte("ANIM")) {
					t.Errorf("%s: the WebP has no ANIM chunk: not animated", w.path)
				}
			}
		})
	}
}
