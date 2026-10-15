package router_test

import (
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// videoPresetDefs are the video presets of the policy tests.
var videoPresetDefs = map[string]string{
	"web":  "rs:fit:640:0/q:60/f:webm",
	"clip": "vd:4/w:320",
	"cut":  "vd:4",
	"long": "w:320",
	"hls":  "st:hls:360:240",
	// WP-131: MPEG-TS segments and overlays on a stream, from presets.
	"hlsts":   "st:hls:360:240/sgf:ts",
	"hlsmark": "st:hls:360:240/wm:1:soea/wmt:aGk/tc:hms",
	"raw":     "raw:1",
	"aud":     "f:m4a",
}

// videoDialects builds the four dialects with video presets: imgproxy
// (trusted signature "trusted"), query, Cloudinary and ImageKit.
func videoDialects(t *testing.T) map[string]dialect.Dialect {
	t.Helper()
	vp, err := preset.New(preset.Options{Definitions: videoPresetDefs})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := sign.ParseHexKeyring([]string{"736563726574"}, []string{"68656C6C6F"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: sign.DefaultSignatureSize, Trusted: []string{"trusted"}})
	if err != nil {
		t.Fatal(err)
	}
	ip, err := imgproxy.New(&imgproxy.Options{Verifier: v, VideoPresets: vp})
	if err != nil {
		t.Fatal(err)
	}
	q, err := query.New(&query.Options{VideoPresets: vp})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := cloudinary.New(&cloudinary.Options{VideoPresets: vp})
	if err != nil {
		t.Fatal(err)
	}
	ik, err := imagekit.New(&imagekit.Options{VideoPresets: vp})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]dialect.Dialect{"imgproxy": ip, "query": q, "cloudinary": cl, "imagekit": ik}
}

func videoRouter(t *testing.T, mount router.Policy, video router.VideoPolicy, ds map[string]dialect.Dialect) *router.Router {
	t.Helper()
	return newRouter(t, &router.Options{
		Policy: mount,
		Mounts: []router.Mount{
			{Name: "imgproxy", Dialect: "imgproxy", Prefix: "/"},
			{Name: "query", Dialect: "query", Prefix: "/q/"},
			{Name: "cloudinary", Dialect: "cloudinary", Prefix: "/cld/", Source: "media"},
			{Name: "imagekit", Dialect: "imagekit", Prefix: "/ik/", Source: "media"},
		},
		Restrictions: restrictions,
		Video:        video,
	}, ds)
}

const vsrc = "/plain/https://example.com/clip.mov"

// TestVideoPolicyMatrix is the policy matrix: signed or unsigned ×
// allow, restricted or deny (video) × the mount's policy × with or
// without a preset × file, stream or raw.
func TestVideoPolicyMatrix(t *testing.T) {
	t.Parallel()
	ds := videoDialects(t)
	allow := router.VideoPolicy{Policy: router.PolicyAllow}
	restricted := router.VideoPolicy{Policy: router.PolicyRestricted, Formats: []string{"mp4", "webm"}}
	deny := router.VideoPolicy{}
	tests := []struct {
		name   string
		mount  router.Policy
		video  router.VideoPolicy
		uri    string
		wantOK bool
	}{
		// Signed: no policy applies.
		{"signed deny", router.PolicyDeny, deny, "/video/trusted/w:1920" + vsrc, true},
		{"signed restricted no preset", router.PolicyRestricted, restricted, "/video/trusted/w:1920/vc:hevc" + vsrc, true},
		{"signed stream", router.PolicyDeny, deny, "/video/trusted/st:hls" + vsrc + "@m3u8", true},
		{"signed raw", router.PolicyDeny, deny, "/video/trusted/raw:1" + vsrc, true},

		// deny (the default).
		{"deny no preset", router.PolicyAllow, deny, "/video/unsafe/w:320" + vsrc, false},
		{"deny preset", router.PolicyAllow, deny, "/video/unsafe/pr:web" + vsrc, false},

		// allow: the mount decides.
		{"allow on allow mount", router.PolicyAllow, allow, "/video/unsafe/w:1920" + vsrc, true},
		{"allow on allow mount stream", router.PolicyAllow, allow, "/video/unsafe" + vsrc + "@m3u8", true},
		{"allow on restricted mount", router.PolicyRestricted, allow, "/video/unsafe/pr:web" + vsrc, false},
		{"allow on deny mount", router.PolicyDeny, allow, "/video/unsafe/pr:web" + vsrc, false},

		// restricted.
		{"restricted preset", router.PolicyRestricted, restricted, "/video/unsafe/pr:web" + vsrc, true},
		{"restricted preset on allow mount", router.PolicyAllow, restricted, "/video/unsafe/preset:web" + vsrc, true},
		{"restricted preset on deny mount", router.PolicyDeny, restricted, "/video/unsafe/pr:web" + vsrc, false},
		{"restricted no preset", router.PolicyRestricted, restricted, "/video/unsafe/w:320" + vsrc, false},
		{"restricted no options", router.PolicyRestricted, restricted, "/video/unsafe" + vsrc, false},
		{"restricted unknown preset", router.PolicyRestricted, restricted, "/video/unsafe/pr:nope" + vsrc, false},
		{"restricted extra option", router.PolicyRestricted, restricted, "/video/unsafe/pr:web/w:1920" + vsrc, false},
		{"restricted option the preset overrides", router.PolicyRestricted, restricted, "/video/unsafe/w:1920/pr:web" + vsrc, true},
		{"restricted option equal to the preset's", router.PolicyRestricted, restricted, "/video/unsafe/pr:web/q:60" + vsrc, true},
		{"restricted codec", router.PolicyRestricted, restricted, "/video/unsafe/pr:clip/vc:hevc" + vsrc, false},
		{"restricted free start not allowed", router.PolicyRestricted, restricted, "/video/unsafe/pr:clip/vs:2" + vsrc, false},
		{"restricted cache buster", router.PolicyRestricted, restricted, "/video/unsafe/pr:web/cb:1" + vsrc, false},
		{"restricted filename", router.PolicyRestricted, restricted, "/video/unsafe/pr:web/fn:clip" + vsrc, true},
		{"restricted watermark", router.PolicyRestricted, restricted, "/video/unsafe/pr:web/wm:1:soea/wmt:aGk" + vsrc, false},
		{"restricted container allowlisted", router.PolicyRestricted, restricted, "/video/unsafe/pr:clip" + vsrc + "@webm", true},
		{"restricted container not allowlisted", router.PolicyRestricted, router.VideoPolicy{Policy: router.PolicyRestricted}, "/video/unsafe/pr:clip" + vsrc + "@webm", false},
		{"restricted container other kind", router.PolicyRestricted, restricted, "/video/unsafe/pr:clip/mu:0" + vsrc + "@m4a", false},
		{"restricted audio preset", router.PolicyRestricted, restricted, "/video/unsafe/pr:aud" + vsrc, true},
		{"restricted audio preset to video", router.PolicyRestricted, restricted, "/video/unsafe/pr:aud" + vsrc + "@mp4", false},
		{"restricted stream preset", router.PolicyRestricted, restricted, "/video/unsafe/pr:hls" + vsrc + "@m3u8", true},
		{"restricted stream preset no extension", router.PolicyRestricted, restricted, "/video/unsafe/pr:hls" + vsrc, true},
		{"restricted stream by extension", router.PolicyRestricted, restricted, "/video/unsafe/pr:cut" + vsrc + "@m3u8", false},
		{"restricted stream by option", router.PolicyRestricted, restricted, "/video/unsafe/pr:cut/st:hls" + vsrc, false},
		{"restricted stream other renditions", router.PolicyRestricted, restricted, "/video/unsafe/pr:hls/st:hls:1080" + vsrc, false},
		{"restricted raw preset", router.PolicyRestricted, restricted, "/video/unsafe/pr:raw" + vsrc, true},
		{"restricted raw option", router.PolicyRestricted, restricted, "/video/unsafe/pr:clip/raw:1" + vsrc, false},
		{"restricted ts preset", router.PolicyRestricted, restricted, "/video/unsafe/pr:hlsts" + vsrc + "@m3u8", true},
		{"restricted ts by option", router.PolicyRestricted, restricted, "/video/unsafe/pr:hls/sgf:ts" + vsrc + "@m3u8", false},
		{"restricted stream overlays preset", router.PolicyRestricted, restricted, "/video/unsafe/pr:hlsmark" + vsrc + "@m3u8", true},
		{"restricted stream overlay by option", router.PolicyRestricted, restricted, "/video/unsafe/pr:hls/tc:hms" + vsrc + "@m3u8", false},
		{"restricted stream watermark by option", router.PolicyRestricted, restricted, "/video/unsafe/pr:hls/wm:1:soea/wmt:aGk" + vsrc + "@m3u8", false},

		// Query dialect.
		{"query restricted preset", router.PolicyRestricted, restricted, "/q/video?url=https://example.com/a.mov&preset=web", true},
		{"query restricted extra", router.PolicyRestricted, restricted, "/q/video?url=https://example.com/a.mov&preset=web&width=1920", false},
		{"query restricted format", router.PolicyRestricted, restricted, "/q/video?url=https://example.com/a.mov&preset=clip&format=webm", true},
		{"query restricted no preset", router.PolicyRestricted, restricted, "/q/video?url=https://example.com/a.mov&width=320", false},

		// Cloudinary (t_) and ImageKit (n-).
		{"cloudinary restricted t_", router.PolicyRestricted, restricted, "/cld/demo/video/upload/t_web/clip.mp4", true},
		{"cloudinary restricted extra", router.PolicyRestricted, restricted, "/cld/demo/video/upload/t_web,w_1920/clip.mp4", false},
		{"cloudinary restricted no preset", router.PolicyRestricted, restricted, "/cld/demo/video/upload/w_320/clip.mp4", false},
		{"cloudinary restricted stream extension", router.PolicyRestricted, restricted, "/cld/demo/video/upload/t_clip/clip.m3u8", false},
		{"cloudinary restricted stream preset", router.PolicyRestricted, restricted, "/cld/demo/video/upload/t_hls/clip.m3u8", true},
		{"cloudinary restricted ts preset", router.PolicyRestricted, restricted, "/cld/demo/video/upload/t_hlsts/clip.m3u8", true},
		{"cloudinary restricted fl_hlsv3", router.PolicyRestricted, restricted, "/cld/demo/video/upload/t_hls,fl_hlsv3/clip.m3u8", false},
		{"imagekit restricted n-", router.PolicyRestricted, restricted, "/ik/clip.mp4?tr=n-clip", true},
		{"imagekit restricted extra", router.PolicyRestricted, restricted, "/ik/clip.mp4?tr=n-clip,w-1920", false},
		{"imagekit restricted ik-master without stream preset", router.PolicyRestricted, restricted, "/ik/clip.mp4/ik-master.m3u8?tr=n-clip", false},
		{"imagekit restricted ik-master stream preset", router.PolicyRestricted, restricted, "/ik/clip.mp4/ik-master.m3u8?tr=n-hls", true},
		{"imagekit restricted ik-video.mp4", router.PolicyRestricted, restricted, "/ik/clip.mov/ik-video.mp4?tr=n-clip", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := videoRouter(t, tc.mount, tc.video, ds)
			res, _, err := rt.Route(request(t, "h", tc.uri))
			if strings.HasSuffix(tc.name, "unknown preset") {
				// The dialect refuses it first: 400.
				if errs.KindOf(err) != errs.KindInvalidURL {
					t.Fatalf("Route error = %v, want 400", err)
				}
				return
			}
			checkPolicyResult(t, res != nil, err, tc.wantOK)
		})
	}
}

// TestVideoPolicyFreeOptions covers the allowed free options and the
// unsigned duration bound.
func TestVideoPolicyFreeOptions(t *testing.T) {
	t.Parallel()
	ds := videoDialects(t)
	vp := router.VideoPolicy{
		Policy: router.PolicyRestricted, AllowedOptions: []string{"start", "end", "duration", "mute"},
		Formats: []string{"mp4"}, MaxDuration: 10 * time.Second,
	}
	tests := []struct {
		name, uri string
		wantOK    bool
	}{
		{"start within the preset's duration", "/video/unsafe/pr:clip/vs:30" + vsrc, true},
		{"duration within the bound", "/video/unsafe/pr:long/vs:1/vd:10" + vsrc, true},
		{"duration over the bound", "/video/unsafe/pr:long/vd:11" + vsrc, false},
		{"end within the bound", "/video/unsafe/pr:long/vs:5/ve:15" + vsrc, true},
		{"end over the bound", "/video/unsafe/pr:long/vs:5/ve:16" + vsrc, false},
		{"start alone on an unbounded preset", "/video/unsafe/pr:long/vs:5" + vsrc, false},
		{"mute", "/video/unsafe/pr:long/mu:1" + vsrc, true},
		{"cloudinary so_ and du_", "/cld/demo/video/upload/t_long,so_2,du_5/clip.mp4", true},
		{"imagekit so- and du-", "/ik/clip.mp4?tr=n-long,so-2,du-5,f-mp4", true},
		{"imagekit automatic container, as without options", "/ik/clip.mp4?tr=n-long,so-2,du-5", true},
		{"width is not free", "/video/unsafe/pr:long/w:640" + vsrc, false},
		{"webm not in the formats", "/video/unsafe/pr:long" + vsrc + "@webm", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := videoRouter(t, router.PolicyRestricted, vp, ds)
			res, _, err := rt.Route(request(t, "h", tc.uri))
			checkPolicyResult(t, res != nil, err, tc.wantOK)
		})
	}
}

func TestVideoPolicyOptions(t *testing.T) {
	t.Parallel()
	_, ds := fakes("a")
	for _, vp := range []router.VideoPolicy{
		{Policy: "open"},
		{Policy: router.PolicyRestricted, AllowedOptions: []string{"width"}},
		{Policy: router.PolicyRestricted, Formats: []string{"mkv"}},
		{Policy: router.PolicyRestricted, MaxDuration: -time.Second},
	} {
		if _, err := router.New(&router.Options{Video: vp, Mounts: []router.Mount{{Dialect: "a", Prefix: "/"}}}, ds); err == nil {
			t.Errorf("router.New accepted video policy %+v", vp)
		}
	}
}
