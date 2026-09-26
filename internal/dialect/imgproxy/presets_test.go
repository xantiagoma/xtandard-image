package imgproxy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// docsPresets are the presets of imgproxy's "Presets" and "Chained
// pipelines" docs plus a few for ordering and nesting.
var docsPresets = map[string]string{
	// docs.imgproxy.net/usage/presets
	"awesome": "resizing_type:fill/format:jpg",
	// only-presets example: thumbnail:blurry:watermarked
	"thumbnail":   "rs:fill:150:150/g:sm",
	"blurry":      "bl:5",
	"watermarked": "wm:0.5:soea",
	// docs.imgproxy.net/features/chained_pipelines
	"test": "width:300/height:300/-/width:200/height:200/-/width:100/height:200",
	// the WP-22 brief
	"sharp":  "sharpen:0.7",
	"avatar": "resizing_type:fill/width:100/height:100/gravity:sm",
	// ordering, argument-wise merge, request-scoped options, nesting
	"box":     "rs:fill:100:100:1",
	"q50":     "q:50/f:webp",
	"nested":  "pr:avatar:sharp/q:80",
	"deep":    "pr:nested/-/pr:test",
	"cb":      "cb:v2/att:1/fn:photo",
	"chained": "w:10/-/pr:q50/bl:1",
}

func newPresetParser(tb testing.TB, defs map[string]string, only bool, o *imgproxy.Options) *imgproxy.Parser {
	tb.Helper()
	s, err := preset.New(preset.Options{Definitions: defs, Only: only, Separator: o.ArgumentsSeparator})
	if err != nil {
		tb.Fatal(err)
	}
	o.Presets = s
	p, err := imgproxy.New(o)
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

func mustParseRequest(tb testing.TB, p *imgproxy.Parser, path string) *transform.Request {
	tb.Helper()
	res, err := p.ParsePath(path)
	if err != nil {
		tb.Fatalf("%s: %v", path, err)
	}
	return res.Request
}

// TestPresetsMatchInline checks that each preset URL parses to exactly the
// request of the equivalent inline URL (apart from the recorded names).
func TestPresetsMatchInline(t *testing.T) {
	t.Parallel()
	p := newPresetParser(t, docsPresets, false, &imgproxy.Options{})
	tests := []struct {
		name, presetPath, inlinePath string
		used                         []string
	}{
		{"docs awesome", "/pr:awesome", "/resizing_type:fill/format:jpg", []string{"awesome"}},
		{"long name", "/preset:awesome", "/resizing_type:fill/format:jpg", []string{"awesome"}},
		{
			"docs thumbnail blurry watermarked", "/preset:thumbnail:blurry:watermarked",
			"/rs:fill:150:150/g:sm/bl:5/wm:0.5:soea",
			[]string{"thumbnail", "blurry", "watermarked"},
		},
		{"separate references", "/pr:thumbnail/pr:blurry", "/rs:fill:150:150/g:sm/bl:5", []string{"thumbnail", "blurry"}},
		{"brief sharp", "/pr:sharp", "/sharpen:0.7", []string{"sharp"}},
		{"brief avatar", "/pr:avatar", "/resizing_type:fill/width:100/height:100/gravity:sm", []string{"avatar"}},
		{"later option overrides preset", "/pr:sharp/sh:2", "/sh:2", []string{"sharp"}},
		{"preset overrides earlier option", "/sh:2/pr:sharp", "/sh:0.7", []string{"sharp"}},
		{"argument-wise merge after", "/pr:box/rs::200", "/rs:fill:200:100:1", []string{"box"}},
		{"argument-wise merge before", "/rs:fit:1:2:0:1/pr:box", "/rs:fill:100:100:1:1", []string{"box"}},
		{"later preset overrides earlier", "/pr:avatar:box", "/rs:fill:100:100:1/g:sm", []string{"avatar", "box"}},
		{"repeated preset", "/pr:sharp/sh:3/pr:sharp", "/sh:0.7", []string{"sharp", "sharp"}},
		{
			"nested", "/pr:nested", "/resizing_type:fill/width:100/height:100/gravity:sm/sharpen:0.7/q:80",
			[]string{"nested", "avatar", "sharp"},
		},
		{"request-scoped delivery", "/pr:cb", "/cb:v2/att:1/fn:photo", []string{"cb"}},
		{"extension overrides preset format", "/pr:q50", "/q:50/f:webp", []string{"q50"}},
		// docs.imgproxy.net/features/chained_pipelines example.
		{
			"docs chained merge", "/width:400/-/preset:test/width:500/-/width:600",
			"/width:400/-/width:500/height:300/-/width:600/height:200/-/width:100/height:200",
			[]string{"test"},
		},
		{"chained preset first", "/pr:test", "/width:300/height:300/-/width:200/height:200/-/width:100/height:200", []string{"test"}},
		{
			"chained preset then url chain", "/pr:test/-/bl:1/-/q:10/-/q:20",
			"/width:300/height:300/-/width:200/height:200/bl:1/-/width:100/height:200/q:10/-/q:20", nil,
		},
		{"request-scoped from chained pipeline", "/pr:chained", "/w:10/-/bl:1/q:50/f:webp", []string{"chained", "q50"}},
		{
			"nested chained", "/pr:deep",
			"/resizing_type:fill/width:100/height:100/gravity:sm/sharpen:0.7/q:80/-/width:300/height:300/-/width:200/height:200/-/width:100/height:200",
			[]string{"deep", "nested", "avatar", "sharp", "test"},
		},
	}
	for _, tt := range tests {
		for _, source := range []string{src, src + "@png", srcB64 + ".avif"} {
			t.Run(tt.name+source, func(t *testing.T) {
				t.Parallel()
				got := mustParseRequest(t, p, "/unsafe"+tt.presetPath+source)
				want := mustParseRequest(t, p, "/unsafe"+tt.inlinePath+source)
				if tt.used != nil {
					if d := cmp.Diff(tt.used, got.Delivery.Presets); d != "" {
						t.Errorf("Delivery.Presets (-want +got):\n%s", d)
					}
				}
				got.Delivery.Presets = nil
				if d := cmp.Diff(want, got); d != "" {
					t.Errorf("(-inline +preset):\n%s", d)
				}
			})
		}
	}
}

func TestDefaultPreset(t *testing.T) {
	t.Parallel()
	defs := map[string]string{"default": "sm:1/q:80/rs:fit:500:500", "thumb": "rs:fill:100:100", "chain": "w:1/-/w:2"}
	p := newPresetParser(t, defs, false, &imgproxy.Options{})
	plain := newPresetParser(t, map[string]string{"thumb": "rs:fill:100:100", "chain": "w:1/-/w:2"}, false, &imgproxy.Options{})
	tests := []struct{ name, path, inline string }{
		{"applied without options", "/plain/x", "/sm:1/q:80/rs:fit:500:500/plain/x"},
		{"url overrides default", "/q:90/plain/x", "/sm:1/q:80/rs:fit:500:500/q:90/plain/x"},
		{"preset overrides default", "/pr:thumb/plain/x", "/sm:1/q:80/rs:fill:100:100/plain/x"},
		{"explicit default reapplies", "/q:90/pr:default/plain/x", "/sm:1/q:80/rs:fit:500:500/plain/x"},
		{"default only in first pipeline", "/w:5/-/w:6/plain/x", "/sm:1/q:80/rs:fit:500:500/w:5/-/w:6/plain/x"},
		{"chained preset after default", "/pr:chain/plain/x", "/sm:1/q:80/rs:fit:500:500/w:1/-/w:2/plain/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mustParseRequest(t, p, "/unsafe"+tt.path)
			want := mustParseRequest(t, plain, "/unsafe"+tt.inline)
			if got.Delivery.Presets[0] != preset.DefaultName {
				t.Errorf("Delivery.Presets = %q, want default first", got.Delivery.Presets)
			}
			got.Delivery.Presets, want.Delivery.Presets = nil, nil
			if d := cmp.Diff(want, got); d != "" {
				t.Errorf("(-want +got):\n%s", d)
			}
		})
	}
}

func TestOnlyPresets(t *testing.T) {
	t.Parallel()
	defs := map[string]string{"thumbnail": "rs:fill:150:150/g:sm", "blurry": "bl:5", "watermarked": "wm:0.5:soea", "chain": "w:1/-/w:2"}
	p := newPresetParser(t, defs, true, &imgproxy.Options{})
	inline := newPresetParser(t, defs, false, &imgproxy.Options{})
	comma := newPresetParser(t, map[string]string{"thumbnail": "rs,fill,150,150/g,sm", "blurry": "bl,5"}, true,
		&imgproxy.Options{ArgumentsSeparator: ","})

	ok := []struct {
		name, path, inline string
		p                  *imgproxy.Parser
	}{
		// docs.imgproxy.net/usage/presets only-presets example.
		{
			"docs example", "/thumbnail:blurry:watermarked/plain/http://example.com/images/curiosity.jpg@png",
			"/rs:fill:150:150/g:sm/bl:5/wm:0.5:soea/plain/http://example.com/images/curiosity.jpg@png", p,
		},
		{"single name", "/thumbnail" + srcB64 + ".webp", "/pr:thumbnail" + srcB64 + ".webp", p},
		{"preset option form", "/pr:thumbnail:blurry" + src, "/pr:thumbnail:blurry" + src, p},
		{"several segments", "/thumbnail/preset:blurry/watermarked:blurry" + src, "/pr:thumbnail:blurry:watermarked:blurry" + src, p},
		{"chained preset", "/chain" + src, "/w:1/-/w:2" + src, p},
		{"custom separator", "/thumbnail,blurry" + src, "/pr:thumbnail:blurry" + src, comma},
	}
	for _, tt := range ok {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mustParseRequest(t, tt.p, "/unsafe"+tt.path)
			want := mustParseRequest(t, inline, "/unsafe"+tt.inline)
			got.Delivery.Presets, want.Delivery.Presets = nil, nil
			if d := cmp.Diff(want, got); d != "" {
				t.Errorf("(-inline +only):\n%s", d)
			}
		})
	}

	forbidden := []string{
		"/rs:fit:100:100" + src,
		"/thumbnail/rs:fit:100:100" + src,
		"/pr:thumbnail/q:80" + src,
		"/thumbnail:unknown" + src,
		"/pr:unknown" + src,
		"/thumbnail::blurry" + src,
		"/thumbnail/-/blurry" + src,
		"/-" + src,
		src,
		srcB64,
		"/enc/" + encToken,
	}
	for _, path := range forbidden {
		t.Run("403"+path, func(t *testing.T) {
			t.Parallel()
			_, err := p.ParsePath("/unsafe" + path)
			if errs.KindOf(err) != errs.KindForbidden || !errors.Is(err, imgproxy.ErrOnlyPresets) {
				t.Fatalf("err = %v, want only-presets 403", err)
			}
			pe, _ := errors.AsType[*errs.Error](err)
			if pe.PublicMessage() != "only presets are allowed" || errs.Status(errs.KindOf(err)) != 403 {
				t.Errorf("public %q status %d", pe.PublicMessage(), errs.Status(errs.KindOf(err)))
			}
		})
	}
	if _, err := p.ParsePath("/unsafe/thumbnail"); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("missing source: %v", err)
	}
}

func TestPresetErrors(t *testing.T) {
	t.Parallel()
	p := newPresetParser(t, docsPresets, false, &imgproxy.Options{})
	tests := []struct {
		path  string
		kind  errs.Kind
		field string
	}{
		{"/pr:nope" + src, errs.KindInvalidURL, "preset"},
		{"/pr:sharp:nope" + src, errs.KindInvalidURL, "preset"},
		{"/pr:" + src, errs.KindInvalidURL, "preset"},
		{"/pr:sharp:" + src, errs.KindInvalidURL, "preset"},
		// test spans 3 pipelines; referenced from the 15th it needs 17.
		{"/" + strings.Repeat("q:1/-/", 14) + "pr:test" + src, errs.KindInvalidURL, ""},
		// the preset created the next pipelines; "-" still stops at 16.
		{"/" + strings.Repeat("q:1/-/", 13) + "pr:test/-/q:1/-/q:1/-/q:1" + src, errs.KindInvalidURL, ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			_, err := p.ParsePath("/unsafe" + tt.path)
			if errs.KindOf(err) != tt.kind {
				t.Fatalf("err = %v, want %v", err, tt.kind)
			}
			if ae, ok := errors.AsType[*dialect.ArgumentError](err); tt.field != "" && (!ok || ae.Field != tt.field) {
				t.Errorf("field: %v", err)
			}
		})
	}
	// Exactly 16 pipelines is fine.
	r := mustParseRequest(t, p, "/unsafe/"+strings.Repeat("q:1/-/", 13)+"pr:test"+src)
	if len(r.Pipelines) != transform.MaxPipelines {
		t.Errorf("pipelines = %d", len(r.Pipelines))
	}
}

// TestNilPresetStore keeps the pre-WP-22 behaviour without a store: names
// are recorded, not expanded.
func TestNilPresetStore(t *testing.T) {
	t.Parallel()
	p, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := mustParseRequest(t, p, "/unsafe/pr:a:b/pr:c"+src)
	if d := cmp.Diff([]string{"a", "b", "c"}, r.Delivery.Presets); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

func TestPresetStartupValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		defs map[string]string
		o    imgproxy.Options
		sep  string
		want string
	}{
		{"bad argument", map[string]string{"bad": "rs:fit:-1:-2"}, imgproxy.Options{}, "", `preset "bad"`},
		{"bad argument nested", map[string]string{"a": "pr:b", "b": "q:101"}, imgproxy.Options{}, "", `preset "a"`},
		{"unknown option", map[string]string{"typo": "sharpening:0.7"}, imgproxy.Options{}, "", `unknown option "sharpening"`},
		{"security option not allowed", map[string]string{"s": "mrd:100"}, imgproxy.Options{}, "", `preset "s"`},
		{"too many args", map[string]string{"s": "q:1:2"}, imgproxy.Options{}, "", `preset "s"`},
		{"separator mismatch", map[string]string{"s": "q:1"}, imgproxy.Options{ArgumentsSeparator: ","}, ":", "separator"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := preset.New(preset.Options{Definitions: tt.defs, Separator: tt.sep})
			if err != nil {
				t.Fatal(err)
			}
			tt.o.Presets = s
			if _, err := imgproxy.New(&tt.o); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}

	// Allowed with the matching configuration; later-milestone options are
	// accepted like in URLs.
	s, err := preset.New(preset.Options{Definitions: map[string]string{"s": "mrd:100/pr:l", "l": "aq:dssim/br:10"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := imgproxy.New(&imgproxy.Options{Presets: s, AllowSecurityOptions: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := mustParseRequest(t, p, "/unsafe/pr:s"+src); r.Limits.MaxResultDimension != 100 {
		t.Errorf("limits = %+v", r.Limits)
	}
}

// FuzzParsePresets parses arbitrary paths with presets and only-presets
// mode on. Parse must not panic and errors must be client kinds.
func FuzzParsePresets(f *testing.F) {
	for _, s := range []string{
		"/unsafe/pr:awesome" + src,
		"/unsafe/width:400/-/preset:test/width:500/-/width:600" + src,
		"/unsafe/thumbnail:blurry:watermarked/plain/http://example.com/images/curiosity.jpg@png",
		"/unsafe/pr:deep/-/-/-/-" + srcB64,
		"/unsafe/" + strings.Repeat("pr:test/-/", 8) + src,
		"/unsafe/pr:chained:nope" + src,
		"/unsafe/thumbnail",
		"/unsafe/-/thumbnail" + src,
	} {
		f.Add(s)
	}
	parsers := []*imgproxy.Parser{
		newPresetParser(f, docsPresets, false, &imgproxy.Options{}),
		newPresetParser(f, docsPresets, true, &imgproxy.Options{}),
		newPresetParser(f, map[string]string{"default": "pr:test/q:1", "test": "w:1/-/w:2/-/w:3"}, false, &imgproxy.Options{}),
	}
	f.Fuzz(func(t *testing.T, path string) {
		for i, p := range parsers {
			res, err := p.ParsePath(path)
			if err != nil {
				switch k := errs.KindOf(err); k {
				case errs.KindInvalidURL, errs.KindForbidden, errs.KindUnauthorized, errs.KindExpired:
				default:
					t.Fatalf("parser %d: %q: unexpected kind %v: %v", i, path, k, err)
				}
				continue
			}
			if n := len(res.Request.Pipelines); n == 0 || n > transform.MaxPipelines {
				t.Fatalf("parser %d: %q: %d pipelines", i, path, n)
			}
		}
	})
}

// TestDocsChainedMergeValues pins the chained-pipelines docs example to its
// documented result, independently of the inline parse.
func TestDocsChainedMergeValues(t *testing.T) {
	t.Parallel()
	p := newPresetParser(t, docsPresets, false, &imgproxy.Options{})
	r := mustParseRequest(t, p, "/unsafe/width:400/-/preset:test/width:500/-/width:600"+src)
	type wh struct{ W, H int }
	got := make([]wh, 0, len(r.Pipelines))
	for _, pl := range r.Pipelines {
		got = append(got, wh{pl.Resize.Width.Px, pl.Resize.Height.Px})
	}
	if d := cmp.Diff([]wh{{400, 0}, {500, 300}, {600, 200}, {100, 200}}, got); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
}

// TestBuilderPresetChainMerge: the Builder shares the path parser's chain
// rules, so a "-" after a chained preset moves into the pipeline the preset
// created (URL options there win) instead of appending an empty one.
func TestBuilderPresetChainMerge(t *testing.T) {
	t.Parallel()
	p := newPresetParser(t, docsPresets, false, &imgproxy.Options{})
	type wh struct{ W, H int }
	for _, tt := range []struct {
		name  string
		build func(b *imgproxy.Builder) error
	}{
		{"apply path", func(b *imgproxy.Builder) error {
			return b.ApplyPath("o", "width:400/-/preset:test/width:500/-/width:600")
		}},
		{"new pipeline", func(b *imgproxy.Builder) error {
			return errors.Join(b.Apply("w", "400"), b.NewPipeline(), b.Apply("pr", "test"),
				b.Apply("w", "500"), b.NewPipeline(), b.Apply("w", "600"))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := p.NewBuilder("query")
			if err := tt.build(b); err != nil {
				t.Fatal(err)
			}
			if err := b.Finish(); err != nil {
				t.Fatal(err)
			}
			pls := b.Request().Pipelines
			got := make([]wh, 0, len(pls))
			for _, pl := range pls {
				got = append(got, wh{pl.Resize.Width.Px, pl.Resize.Height.Px})
			}
			if d := cmp.Diff([]wh{{400, 0}, {500, 300}, {600, 200}, {100, 200}}, got); d != "" {
				t.Errorf("(-want +got):\n%s", d)
			}
		})
	}
}
