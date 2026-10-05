package translate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// videoMounts are testMounts plus Cloudflare Media Transformations bound to
// the local source, as the demo binds it.
func videoMounts() []playground.Mount {
	return append(testMounts(), playground.Mount{
		ID: "cloudflare_media", Dialect: "cloudflare_media", Prefix: "/cdn-cgi/media/", Source: "local",
		Scheme: playground.SchemeXI, Grammar: playground.GrammarPath,
	})
}

// videoCases are /video option paths with a container; each is translated
// from an http source and from a key on the mounts' source.
var videoCases = []struct {
	name, opts, container string
	// exact lists the dialects that must reproduce the transform with no
	// unsupported or approximated field (from the key; Cloudinary's fetch
	// and ImageKit need what their mounts address).
	exact []string
}{
	{"trim and resize to webm", "vs:1/vd:2/rs:fit:640:0", "webm", []string{"imgproxy", "query", "cloudinary", "imagekit"}},
	{"fill with gravity", "rs:fill:640:360/g:soea", "mp4", []string{"imgproxy", "query", "cloudinary", "imagekit"}},
	{"cover box", "vs:2/vd:5/rs:fill:480:270", "mp4", []string{"imgproxy", "query", "cloudinary", "imagekit", "cloudflare_media"}},
	{"m4a audio", "vd:3", "m4a", []string{"imgproxy", "query", "cloudinary", "imagekit", "cloudflare_media"}},
	{"mp3 with loudnorm", "vs:1/vd:2/ln:1:-16:-1.5:11/sr:44100/ach:1/ab:96k", "mp3", []string{"imgproxy", "query"}},
	{"rotate, flip, fps and quality", "rot:90/fl:1:0/fps:15/q:60", "mp4", []string{"imgproxy", "query", "cloudinary"}},
	{"mute, codec and bit rate", "mu:1/vc:vp9/vb:800k/ki:1", "webm", []string{"imgproxy", "query"}},
	{"crop at a corner", "c:320:240:nowe:10:20", "mp4", []string{"imgproxy", "query", "cloudinary", "imagekit"}},
	{"pad with a background", "rs:fit:640:360:0:1/bg:00ff00", "mp4", []string{"imgproxy", "query", "cloudinary", "imagekit"}},
	{"watermark with a window and fades", "wm:0.8:soea:10:10:0.2/wmu:" + b64("xi://local/watermark/logo.png") + "/ow:1:3/of:0.5:0.5", "mp4", []string{"imgproxy", "query"}},
	{"text overlay and timecode", "wm:1:nowe:12:12/wmt:" + b64("© 2026") + "/tc:hms:soea:8:8:20:ffcc00:0.8", "webm", []string{"imgproxy", "query"}},
	{"fades and volume", "fd:0.5:1/vol:0.5", "webm", []string{"imgproxy", "query"}},
	{"waveform", "wf:1:2266cc:ffffff:1/w:800/h:160", "png", []string{"imgproxy", "query"}},
	{"flac", "vs:0.5/vol:-6dB", "flac", []string{"imgproxy", "query"}},
}

// TestVideoRoundTrip emits every video case in every dialect, parses each
// URL back with that dialect's parser and checks the exact dialects; the
// matrix is the golden file testdata/video_roundtrip.json.
func TestVideoRoundTrip(t *testing.T) {
	t.Parallel()
	tr, err := New(Options{Mounts: videoMounts()})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]roundTripResult, 0, 2*len(videoCases))
	for _, tc := range videoCases {
		for _, src := range []string{"https://example.com/clips/a.mp4", "xi://local/clips/a.mp4"} {
			path := "/video/" + tc.opts + "/plain/" + src + "@" + tc.container
			res, err := tr.Translate(&playground.TranslateRequest{Mount: "imgproxy", Path: path})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if canon, ok := res.Canonical.(map[string]any); !ok || canon["Kind"] != transform.KindVideo.String() {
				t.Fatalf("%s: canonical %v", tc.name, res.Canonical)
			}
			row := roundTripResult{Case: tc.name, Source: src, Targets: map[string]roundTripTarget{}}
			for _, tg := range res.Targets {
				row.Targets[tg.Dialect] = roundTripTarget{
					URL: withQueryTest(tg.Path, tg.Query), Unsupported: tg.Unsupported, Approximated: tg.Approximated, Error: tg.Error,
				}
				if strings.Contains(tg.Error, "does not parse") {
					t.Errorf("%s (%s): %s: %s", tc.name, src, tg.Dialect, tg.Error)
				}
				if tg.Error == "" && tg.Path == "" {
					t.Errorf("%s (%s): %s: neither a URL nor an error", tc.name, src, tg.Dialect)
				}
				keyed := strings.HasPrefix(src, "xi://local/")
				if !keyed || !slices.Contains(tc.exact, tg.Dialect) {
					continue
				}
				if tg.Error != "" || len(tg.Unsupported)+len(tg.Approximated) > 0 {
					t.Errorf("%s (%s): %s %s?%s: error %q, unsupported %v, approximated %v",
						tc.name, src, tg.Dialect, tg.Path, tg.Query, tg.Error, tg.Unsupported, tg.Approximated)
				}
			}
			if len(res.Targets) != len(videoTargets()) {
				t.Errorf("%s: %d targets", tc.name, len(res.Targets))
			}
			rows = append(rows, row)
		}
	}
	got, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", "video_roundtrip.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("video round-trip matrix changed (-want +got, run with -update if intended):\n%s", cmp.Diff(string(want), string(got)))
	}
}

// TestVideoInputs translates video URLs of every video dialect (the input
// side): each parses into the same canonical VideoOp as its imgproxy
// equivalent, and the imgproxy card writes that /video URL.
func TestVideoInputs(t *testing.T) {
	t.Parallel()
	tr, err := New(Options{Mounts: videoMounts()})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mount, path, query string
		imgproxy                 string
	}{
		{
			"imgproxy", "imgproxy", "/video/vs:1/vd:2/rs:fit:640:0/plain/xi://local/v/a.mp4@webm", "",
			"/video/vs:1/vd:2/rs:fit:640/plain/xi://local/v/a.mp4@webm",
		},
		{
			"query", "query", "/video", "url=xi%3A%2F%2Flocal%2Fv%2Fa.mp4&start=1&duration=2&width=640&format=webm",
			"/video/vs:1/vd:2/rs:fit:640/plain/xi://local/v/a.mp4@webm",
		},
		{
			"cloudinary upload", "cloudinary", "/xi/video/upload/so_1,du_2/v/a.mp4", "",
			"/video/vs:1/vd:2/plain/xi://local/v/a.mp4",
		},
		{
			"cloudinary audio", "cloudinary", "/xi/video/upload/vc_none,so_2,du_5/v/a.m4a", "",
			"/video/vs:2/vd:5/plain/xi://local/v/a.m4a@m4a",
		},
		{
			"imagekit resize", "imagekit", "/tr:w-320,h-180,f-webm/v/a.mp4", "",
			"/video/rs:fill:320:180/plain/xi://local/v/a.mp4@webm",
		},
		{
			"cloudflare audio", "cloudflare_media", "/mode=audio,time=1s,duration=2s/v/a.mp4", "",
			"/video/vs:1/vd:2/ac:aac/plain/xi://local/v/a.mp4@m4a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := tr.Translate(&playground.TranslateRequest{Mount: tc.mount, Path: tc.path, Query: tc.query})
			if err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(res.Targets, func(x playground.TranslateTarget) bool { return x.Dialect == "imgproxy" })
			if i < 0 {
				t.Fatal("no imgproxy target")
			}
			if got := res.Targets[i].Path; got != tc.imgproxy {
				t.Errorf("imgproxy = %q, want %q", got, tc.imgproxy)
			}
		})
	}
}

// TestVideoInfoRefused: /info and raw URLs still do not translate.
func TestVideoInfoRefused(t *testing.T) {
	t.Parallel()
	tr := newTranslator(t)
	_, err := tr.Translate(&playground.TranslateRequest{Mount: "imgproxy", Path: "/raw:1/plain/https://example.com/a.jpg"})
	if err == nil || !playground.IsClientError(err) {
		t.Fatalf("raw: %v, want a client error", err)
	}
}
