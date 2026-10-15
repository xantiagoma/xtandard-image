package imgproxy_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func videoPresetParser(t *testing.T, defs map[string]string, only bool) *imgproxy.Parser {
	t.Helper()
	vp, err := preset.New(preset.Options{Definitions: defs})
	if err != nil {
		t.Fatal(err)
	}
	o := &imgproxy.Options{VideoPresets: vp, Strict: true}
	if only {
		if o.Presets, err = preset.New(preset.Options{Definitions: map[string]string{"img": "w:10"}, Only: true}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := imgproxy.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestVideoPresetsExpand checks imgproxy's preset semantics on /video:
// expansion in place (later options win), nesting, the default preset.
func TestVideoPresetsExpand(t *testing.T) {
	t.Parallel()
	p := videoPresetParser(t, map[string]string{
		"web":   "rs:fit:640:0/q:60/f:webm",
		"short": "vd:4/pr:web",
		"hls":   "st:hls:360:240/sgd:2",
	}, false)
	pd := videoPresetParser(t, map[string]string{"default": "ki:2/q:50", "web": "w:640"}, false)
	tests := []struct {
		name    string
		p       *imgproxy.Parser
		path    string
		want    transform.VideoOp
		presets []string
	}{
		{"preset", p, "/unsafe/pr:web" + vsrcPath, transform.VideoOp{Width: 640, Quality: 60, Container: transform.VideoContainerWebM, Fit: transform.VideoFitScaleDown}, []string{"web"}},
		{"long name", p, "/unsafe/preset:web" + vsrcPath, transform.VideoOp{Width: 640, Quality: 60, Container: transform.VideoContainerWebM, Fit: transform.VideoFitScaleDown}, []string{"web"}},
		{"later option wins", p, "/unsafe/pr:web/q:80" + vsrcPath, transform.VideoOp{Width: 640, Quality: 80, Container: transform.VideoContainerWebM, Fit: transform.VideoFitScaleDown}, []string{"web"}},
		{"preset after the option wins", p, "/unsafe/q:80/pr:web" + vsrcPath, transform.VideoOp{Width: 640, Quality: 60, Container: transform.VideoContainerWebM, Fit: transform.VideoFitScaleDown}, []string{"web"}},
		{"extension after the preset wins", p, "/unsafe/pr:web" + vsrcPath + "@mp4", transform.VideoOp{Width: 640, Quality: 60, Container: transform.VideoContainerMP4, Fit: transform.VideoFitScaleDown}, []string{"web"}},
		{"nested", p, "/unsafe/pr:short" + vsrcPath, transform.VideoOp{Duration: 4 * time.Second, Width: 640, Quality: 60, Container: transform.VideoContainerWebM, Fit: transform.VideoFitScaleDown}, []string{"short", "web"}},
		{"stream", p, "/unsafe/pr:hls" + vsrcPath, transform.VideoOp{
			Fit: transform.VideoFitContain, Stream: &transform.VideoStream{Protocol: transform.StreamHLS, Renditions: []int{360, 240}, SegmentDuration: 2 * time.Second},
		}, []string{"hls"}},
		{"default first", pd, "/unsafe/q:70" + vsrcPath, transform.VideoOp{KeyframeInterval: 2 * time.Second, Quality: 70, Fit: transform.VideoFitScaleDown}, []string{"default"}},
		{"default and a preset", pd, "/unsafe/pr:web" + vsrcPath, transform.VideoOp{KeyframeInterval: 2 * time.Second, Quality: 50, Width: 640, Fit: transform.VideoFitScaleDown}, []string{"default", "web"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := tc.p.ParseVideoPath(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(&tc.want, res.Request.Video); diff != "" {
				t.Errorf("op (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.presets, res.Request.Delivery.Presets); diff != "" {
				t.Errorf("presets (-want +got):\n%s", diff)
			}
		})
	}
}

const vsrcPath = "/plain/https://example.com/clip.mov"

// TestVideoPresetsPresetVideo checks the presets-only op of unsigned URLs.
func TestVideoPresetsPresetVideo(t *testing.T) {
	t.Parallel()
	keys, err := sign.ParseHexKeyring([]string{exampleKey}, []string{exampleSalt})
	if err != nil {
		t.Fatal(err)
	}
	v, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: 32, Trusted: []string{trustedSig}})
	if err != nil {
		t.Fatal(err)
	}
	vp, err := preset.New(preset.Options{Definitions: map[string]string{"default": "ki:2", "web": "w:640/q:60"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := imgproxy.New(&imgproxy.Options{Verifier: v, VideoPresets: vp})
	if err != nil {
		t.Fatal(err)
	}
	web := &transform.VideoOp{KeyframeInterval: 2 * time.Second, Width: 640, Quality: 60, Fit: transform.VideoFitScaleDown}
	tests := []struct {
		name string
		path string
		want *transform.VideoOp
	}{
		{"preset alone", "/unsafe/pr:web" + vsrcPath, web},
		{"options and extension left out", "/unsafe/vs:1/pr:web/w:100/wm:1/wmt:aGk" + vsrcPath + "@webm", web},
		{"default preset only", "/unsafe/w:100" + vsrcPath, nil},
		{"no options", "/unsafe" + vsrcPath, nil},
		{"signed", "/" + trustedSig + "/pr:web" + vsrcPath, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := p.ParseVideoPath(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, res.PresetVideo); diff != "" {
				t.Errorf("PresetVideo (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVideoPresetsErrors(t *testing.T) {
	t.Parallel()
	p := videoPresetParser(t, map[string]string{"web": "w:640"}, false)
	for _, path := range []string{"/unsafe/pr:nope" + vsrcPath, "/unsafe/pr:" + vsrcPath, "/unsafe/pr:web:" + vsrcPath} {
		if _, err := p.ParseVideoPath(path); errs.KindOf(err) != errs.KindInvalidURL {
			t.Errorf("%s: error %v, want 400", path, err)
		}
	}
	// Without a store the names are only recorded, as for images.
	bare, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := bare.ParseVideoPath("/unsafe/pr:web" + vsrcPath)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"web"}, res.Request.Delivery.Presets); diff != "" || res.Request.Video.Width != 0 || res.PresetVideo != nil {
		t.Errorf("unexpanded preset: presets %v, op %+v, PresetVideo %v", res.Request.Delivery.Presets, res.Request.Video, res.PresetVideo)
	}
}

// TestVideoPresetsOnly: XI_PRESETS_ONLY admits /video URLs made of video
// preset references only.
func TestVideoPresetsOnly(t *testing.T) {
	t.Parallel()
	p := videoPresetParser(t, map[string]string{"web": "w:640", "mute": "mu:1"}, true)
	tests := []struct {
		path string
		ok   bool
	}{
		{"/unsafe/web" + vsrcPath, true},
		{"/unsafe/web:mute" + vsrcPath, true},
		{"/unsafe/pr:web:mute" + vsrcPath, true},
		{"/unsafe/web/w:10" + vsrcPath, false},
		{"/unsafe/w:10" + vsrcPath, false},
		{"/unsafe/img" + vsrcPath, false}, // an image preset
	}
	for _, tc := range tests {
		res, err := p.ParseVideoPath(tc.path)
		switch {
		case tc.ok && err != nil:
			t.Errorf("%s: %v", tc.path, err)
		case tc.ok && res.Request.Video.Width != 640:
			t.Errorf("%s: width %d", tc.path, res.Request.Video.Width)
		case !tc.ok && errs.KindOf(err) != errs.KindForbidden:
			t.Errorf("%s: error %v, want 403", tc.path, err)
		}
	}
	// No video presets: every /video URL is 403, as before WP-132.
	img, err := preset.New(preset.Options{Definitions: map[string]string{"img": "w:10"}, Only: true})
	if err != nil {
		t.Fatal(err)
	}
	none, err := imgproxy.New(&imgproxy.Options{Presets: img})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := none.ParseVideoPath("/unsafe/img" + vsrcPath); errs.KindOf(err) != errs.KindForbidden {
		t.Errorf("only presets without video presets: %v, want 403", err)
	}
}

// TestVideoPresetsValidate: New refuses video presets that could never
// apply, naming the preset.
func TestVideoPresetsValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, def, want string
	}{
		{"unknown option", "bl:2", "unknown option"},
		{"bad value", "crf:99", "crf"},
		{"chained", "w:10/-/w:20", "chained pipelines"},
		{"delivery option", "cb:1", "delivery option"},
		{"source option", "exp:1", "delivery option"},
		{"bad stream", "st:hls/w:640", "stream"},
		{"undefined reference", "pr:nope", "undefined preset"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vp, err := preset.New(preset.Options{Definitions: map[string]string{"bad": tc.def}})
			if err == nil {
				_, err = imgproxy.New(&imgproxy.Options{VideoPresets: vp})
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `"bad"`) {
				t.Errorf("error %v, want one naming preset \"bad\" and %q", err, tc.want)
			}
		})
	}
	vp, err := preset.New(preset.Options{Definitions: map[string]string{"web": "w,640"}, Separator: ","})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imgproxy.New(&imgproxy.Options{VideoPresets: vp}); err == nil {
		t.Error("New accepted video presets with another separator")
	}
}

func TestVideoPresetOp(t *testing.T) {
	t.Parallel()
	p := videoPresetParser(t, map[string]string{"web": "w:640/f:webm", "hls": "st:dash", "q": "q:70", "default": "ki:3"}, false)
	tests := []struct {
		names         []string
		width         int
		ki            time.Duration
		containerSet  bool
		stream, isNil bool
	}{
		{names: []string{"web"}, width: 640, ki: 3 * time.Second, containerSet: true},
		{names: []string{"q"}, ki: 3 * time.Second},
		{names: []string{"hls"}, ki: 3 * time.Second, containerSet: true, stream: true},
		{names: nil, ki: 3 * time.Second},
	}
	for _, tc := range tests {
		op, set, err := p.VideoPresetOp(tc.names)
		if err != nil {
			t.Fatal(err)
		}
		if op.Width != tc.width || op.KeyframeInterval != tc.ki || set != tc.containerSet || (op.Stream != nil) != tc.stream {
			t.Errorf("%v: op %+v, containerSet %v", tc.names, op, set)
		}
	}
	bare := videoPresetParser(t, map[string]string{"web": "w:640"}, false)
	if op, _, err := bare.VideoPresetOp(nil); op != nil || err != nil {
		t.Errorf("no names, no default: %v, %v", op, err)
	}
	if _, _, err := bare.VideoPresetOp([]string{"nope"}); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("unknown name: %v, want 400", err)
	}
}

// TestVideoPresetsBuilder: the query dialect's builder expands preset and
// builds the presets-only request.
func TestVideoPresetsBuilder(t *testing.T) {
	t.Parallel()
	p := videoPresetParser(t, map[string]string{"web": "w:640/q:60"}, false)
	b := p.NewVideoBuilder("query")
	for _, kv := range [][]string{{"start", "1"}, {"preset", "web"}, {"quality", "70"}} {
		if err := b.Apply(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	if op := b.Request().Video; op.Width != 640 || op.Quality != 70 || op.Start != time.Second || !b.ReferencedVideoPresets() {
		t.Errorf("builder op %+v", op)
	}
	po := p.NewVideoPresetsBuilder("query")
	for _, kv := range [][]string{{"start", "1"}, {"preset", "web"}, {"quality", "70"}} {
		if err := po.Apply(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := po.ApplyPath("o", "w:10/pr:web"); err != nil {
		t.Fatal(err)
	}
	if err := po.Finish(); err != nil {
		t.Fatal(err)
	}
	if op := po.Request().Video; op.Width != 640 || op.Quality != 60 || op.Start != 0 {
		t.Errorf("presets-only op %+v", op)
	}
}

// FuzzVideoPresets fuzzes the video preset definitions (preset.New and
// the dialect's validation must not panic) and the URLs that use them.
func FuzzVideoPresets(f *testing.F) {
	for _, s := range [][2]string{
		{"rs:fit:640:0/q:60/f:webm", "/unsafe/pr:p/plain/https://example.com/a.mov"},
		{"st:hls:360:240/sgd:2", "/unsafe/pr:p/vs:1" + vsrcPath + "@m3u8"},
		{"vd:4/pr:p", "/unsafe/pr:p:p" + vsrcPath},
		{"wm:1:soea/wmt:aGk/ow:1:2", "/unsafe/w:10/pr:p/raw:1" + vsrcPath},
		{"-/w:1", "/unsafe/pr:" + vsrcPath},
		{"raw:1", "/unsafe/pr:p/cb:1/plain/x@webm"},
		{"", ""},
		{":::/::", "/unsafe/pr:p/::/plain/@@"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, def, path string) {
		vp, err := preset.New(preset.Options{Definitions: map[string]string{"p": def}})
		if err != nil {
			return
		}
		p, err := imgproxy.New(&imgproxy.Options{VideoPresets: vp})
		if err != nil {
			return
		}
		if _, _, err := p.VideoPresetOp([]string{"p"}); err != nil {
			t.Fatalf("validated preset %q: %v", def, err)
		}
		res, err := p.ParseVideoPath(path)
		checkFuzz(t, "videopresets", path, res, err, transform.KindVideo)
		if err == nil && res.PresetVideo != nil {
			checkPresetVideo(t, res)
		}
	})
}

// checkPresetVideo: a presets-only op is a valid op of an unsigned URL.
func checkPresetVideo(t *testing.T, res *dialect.Result) {
	t.Helper()
	if res.Signature.Signed() {
		t.Fatal("PresetVideo on a signed URL")
	}
	if err := res.PresetVideo.Validate(); err != nil {
		t.Fatalf("PresetVideo does not validate: %v", err)
	}
}
