package playground_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/apidoc"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// fixtures is the repository fixture directory, the demo's local source
// (compose.playground.yml).
const fixtures = "../../testdata/fixtures"

// recipeMounts are the demo configuration's mounts the recipes are built
// on: grammar and the named source keys resolve against.
var recipeMounts = map[string]struct{ grammar, source string }{
	"imgproxy":   {playground.GrammarImgproxy, ""},
	"query":      {playground.GrammarQuery, ""},
	"imgix":      {playground.GrammarImgix, "web"},
	"cloudinary": {playground.GrammarCloudinary, "local"},
	"imagekit":   {playground.GrammarImagekit, "local"},
	// /cdn-cgi/media/ next to the Cloudflare mount, bound to the fixtures.
	"cloudflare_media": {playground.GrammarPath, "local"},
}

// defaultWatermarkRecipe is the one recipe that shows the server's default
// watermark (imgproxy compatibility, WP-117).
const defaultWatermarkRecipe = "wm-default"

// TestRecipesParse builds every recipe the way the page does (grammar.js
// build), checks the result against the recipe's url, parses it with the
// dialect's strict parser (unknown or unsupported options are a 400;
// nothing but approximations may be ignored) and checks that the source
// and every watermark image are repository fixtures or gallery URLs.
func TestRecipesParse(t *testing.T) {
	t.Parallel()
	recipes, err := playground.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	if len(recipes) < 30 {
		t.Fatalf("%d recipes, want at least 30", len(recipes))
	}
	gallery, err := playground.Gallery(nil)
	if err != nil {
		t.Fatal(err)
	}
	public := map[string]bool{}
	for _, g := range gallery {
		public[g.URL] = true
	}
	indexes := map[string]map[string]*playground.Option{}
	for _, c := range apidoc.Catalogues() {
		idx := map[string]*playground.Option{}
		for i := range c.Options {
			idx[c.Options[i].Name] = &c.Options[i]
		}
		indexes[c.Grammar] = idx
	}
	info := map[string]bool{}
	for _, o := range apidoc.InfoOptions() {
		info[o.Name] = true
	}
	videoIdx := map[string]*playground.Option{}
	videoOpts := apidoc.VideoOptions()
	for i := range videoOpts {
		videoIdx[videoOpts[i].Name] = &videoOpts[i]
	}
	parsers := recipeParsers(t)
	if !slices.ContainsFunc(recipes, func(r playground.Recipe) bool { return r.ID == defaultWatermarkRecipe }) {
		t.Errorf("no %s recipe", defaultWatermarkRecipe)
	}
	seen := map[string]bool{}
	for i := range recipes {
		r := &recipes[i]
		t.Run(r.ID, func(t *testing.T) {
			t.Parallel()
			if r.ID == "" || r.Title == "" || r.Description == "" || len(r.Tags) == 0 || len(r.Pipelines) == 0 {
				t.Fatalf("incomplete recipe %+v", r)
			}
			if !slices.Contains([]string{"watermark", "video", "showcase"}, r.Category) {
				t.Errorf("category %q", r.Category)
			}
			for _, n := range r.Needs {
				if !slices.Contains([]string{playground.NeedWatermark, playground.NeedDetection, playground.NeedVideo, playground.NeedVideoTransform, playground.NeedH264, playground.NeedHEVC}, n) {
					t.Errorf("unknown need %q", n)
				}
			}
			for _, e := range r.Info {
				if len(e) == 0 || !info[e[0]] {
					t.Errorf("info option %v is not in the info catalogue", e)
				}
			}
			m, ok := recipeMounts[r.Dialect]
			if !ok {
				t.Fatalf("dialect %q has no demo mount", r.Dialect)
			}
			idx := indexes[m.grammar]
			if r.Video {
				if m.grammar != playground.GrammarImgproxy && m.grammar != playground.GrammarQuery {
					t.Fatalf("video recipe on the %s grammar", m.grammar)
				}
				idx = videoIdx
			}
			path, rawQuery, err := buildRecipe(r, m.grammar, m.source, idx)
			if err != nil {
				t.Fatal(err)
			}
			got := path
			if rawQuery != "" {
				got += "?" + rawQuery
			}
			if got != r.URL {
				t.Errorf("url = %q, want the recipe's %q", got, r.URL)
			}
			in := &dialect.Input{Path: path, RawQuery: rawQuery, Mount: dialect.Mount{Name: r.Dialect, Source: m.source}}
			switch {
			case m.grammar == playground.GrammarImgproxy && r.Video:
				in.Path = "/_" + strings.TrimPrefix(path, "/video") // the signature segment, after /video
			case m.grammar == playground.GrammarImgproxy:
				in.Path = "/_" + path // the signature segment
			}
			in.Query, err = url.ParseQuery(rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			var res *dialect.Result
			if r.Video && m.grammar == playground.GrammarImgproxy {
				ip, ok := parsers[r.Dialect].(*imgproxy.Parser)
				if !ok {
					t.Fatalf("%s is not the imgproxy parser", r.Dialect)
				}
				res, err = ip.ParseVideo(in)
			} else {
				res, err = parsers[r.Dialect].Parse(in)
			}
			if err != nil {
				t.Fatalf("%s?%s: %v", in.Path, in.RawQuery, err)
			}
			for _, ig := range res.Request.Ignored {
				if ig.Reason != "approximated" {
					t.Errorf("ignored %+v", ig)
				}
			}
			if m.source != "" && res.Request.Source.Mount == "" {
				res.Request.Source.Mount = m.source // the router binds it
			}
			checkRef(t, res.Request.Source, m.source, public)
			isVideo := res.Request.Kind == transform.KindVideo
			if r.Video && !isVideo {
				t.Errorf("a video recipe parses as %v", res.Request.Kind)
			}
			if isVideo != slices.Contains(r.Needs, playground.NeedVideoTransform) {
				t.Errorf("kind %v, needs %v: video transformations need %s", res.Request.Kind, r.Needs, playground.NeedVideoTransform)
			}
			if isVideo {
				// WP-129: a recipe that asks for a gpl-only encoder says so,
				// so the page disables it on the full image.
				vc := res.Request.Video.VideoCodec
				if (vc == transform.VideoCodecH264) != slices.Contains(r.Needs, playground.NeedH264) ||
					(vc == transform.VideoCodecHEVC) != slices.Contains(r.Needs, playground.NeedHEVC) {
					t.Errorf("video codec %v, needs %v: H.264 and HEVC output need %s / %s", vc, r.Needs, playground.NeedH264, playground.NeedHEVC)
				}
				for _, o := range res.Request.Video.Overlays {
					if o.Source != nil {
						checkRef(t, *o.Source, m.source, public)
					}
				}
			}
			marks, defaults := 0, 0
			for _, p := range res.Request.Pipelines {
				for _, w := range p.Watermarks {
					marks++
					if w.Source != nil {
						checkRef(t, *w.Source, m.source, public)
					} else if w.Text == "" {
						defaults++ // the configured default image: nothing in the URL names it
					}
				}
			}
			if r.Category == "watermark" && marks == 0 {
				t.Error("a watermark recipe without a watermark")
			}
			// WP-117: every watermark is explicit (wmu, wmt or a dialect's
			// overlay), so the URL names what is drawn; only the one
			// compatibility recipe shows the server's default image.
			needsDefault := slices.Contains(r.Needs, playground.NeedWatermark)
			if r.ID == defaultWatermarkRecipe {
				if defaults == 0 || !needsDefault {
					t.Errorf("draws %d default watermarks, needs %v; want the default watermark and the need", defaults, r.Needs)
				}
			} else if defaults > 0 || needsDefault {
				t.Errorf("draws %d default watermarks (needs %v): only %s may depend on XI_PROCESSING_WATERMARK_SOURCE", defaults, r.Needs, defaultWatermarkRecipe)
			}
		})
		if seen[r.ID] {
			t.Errorf("duplicate id %q", r.ID)
		}
		seen[r.ID] = true
	}
}

func recipeParsers(t *testing.T) map[string]dialect.Dialect {
	t.Helper()
	ip, err := imgproxy.New(&imgproxy.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	q, err := query.New(&query.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	ix, err := imgix.New(&imgix.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := cloudinary.New(&cloudinary.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	ik, err := imagekit.New(&imagekit.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	cfm, err := cloudflare.NewMedia(&cloudflare.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]dialect.Dialect{"imgproxy": ip, "query": q, "imgix": ix, "cloudinary": cl, "imagekit": ik, "cloudflare_media": cfm}
}

// checkRef fails unless s names a repository fixture (on the local source)
// or a gallery URL; a relative key resolves against the mount's source.
func checkRef(t *testing.T, s transform.SourceSpec, mountSource string, public map[string]bool) {
	t.Helper()
	raw := s.Raw
	if s.Encoding == transform.SourceRelative && s.Mount == "" {
		s.Mount = mountSource
	}
	var file string
	switch {
	case strings.HasPrefix(raw, "xi://local/"):
		file = strings.TrimPrefix(raw, "xi://local/")
	case strings.HasPrefix(raw, "local:///"):
		file = strings.TrimPrefix(raw, "local:///")
	case strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "http://"):
		if !public[raw] {
			t.Errorf("%s is not a samples.json URL", raw)
		}
		return
	case s.Mount == "local":
		var err error
		if file, err = url.PathUnescape(raw); err != nil {
			t.Errorf("key %q: %v", raw, err)
			return
		}
	default:
		t.Errorf("source %q (mount %q) is neither a fixture nor a gallery URL", raw, s.Mount)
		return
	}
	if _, err := os.Stat(filepath.Join(fixtures, filepath.FromSlash(file))); err != nil {
		t.Errorf("fixture %s: %v", file, err)
	}
}

// ---- grammar.js build, for the grammars the recipes use -----------------

// escapePlain keeps [A-Za-z0-9-._~:/] and percent-encodes every other byte
// (dom.js escapePlain).
func escapePlain(s string) string {
	return escapeKeep(s, "-._~:/")
}

// escapeValue is dom.js escapeValue: path-safe characters, sub-delimiters,
// "@" and "%" (already encoded input) are kept.
func escapeValue(s string) string {
	return escapeKeep(s, "-._~:@!$&'()*+,;=%")
}

// encodeURIComponent is JavaScript's.
func encodeURIComponent(s string) string {
	return escapeKeep(s, "-_.!~*'()")
}

func escapeKeep(s, keep string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// entryValues returns the option and the cleaned values of one recipe
// entry: base64 arguments of the imgproxy grammar are encoded.
func entryValues(e []string, grammar string, idx map[string]*playground.Option) (*playground.Option, []string, error) {
	if len(e) == 0 {
		return nil, nil, errors.New("empty entry")
	}
	o, ok := idx[e[0]]
	if !ok {
		return nil, nil, fmt.Errorf("%s: not in the %s catalogue", e[0], grammar)
	}
	vals := e[1:]
	if len(o.Forms[0].Args) == 0 && len(o.Forms[0].Rest) == 0 {
		if len(vals) != 0 {
			return nil, nil, fmt.Errorf("%s is a flag", e[0])
		}
		return o, nil, nil
	}
	if len(vals) == 0 {
		return nil, nil, fmt.Errorf("%s without values", e[0])
	}
	form := formFor(o, vals)
	if form < 0 {
		return nil, nil, fmt.Errorf("%s: no form takes %v", e[0], vals)
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = strings.TrimSpace(v)
		if grammar == playground.GrammarImgproxy && argType(&o.Forms[form], i) == "base64" {
			out[i] = base64.RawURLEncoding.EncodeToString([]byte(out[i]))
		}
	}
	return o, out, nil
}

// formFor is app.js formFor: the first form whose keyword arguments (a
// single-valued enum) match and that has room for the values.
func formFor(o *playground.Option, vals []string) int {
	for i := range o.Forms {
		f := &o.Forms[i]
		ok := len(f.Rest) > 0 || len(vals) <= len(f.Args)
		for j, a := range f.Args {
			if ok && len(a.Enum) == 1 && (j >= len(vals) || vals[j] != a.Enum[0]) {
				ok = false
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func argType(f *playground.Form, i int) string {
	if i < len(f.Args) {
		return f.Args[i].Type
	}
	if len(f.Rest) == 0 {
		return ""
	}
	return f.Rest[(i-len(f.Args))%len(f.Rest)].Type
}

func isFlag(o *playground.Option) bool {
	return len(o.Forms[0].Args) == 0 && len(o.Forms[0].Rest) == 0
}

// keyOn is grammar.js keyOn: the escaped key of src on source name.
func keyOn(src, name string) (string, bool) {
	p := "xi://" + name + "/"
	if name == "" || !strings.HasPrefix(src, p) {
		return "", false
	}
	segs := strings.Split(strings.TrimPrefix(src, p), "/")
	for i, s := range segs {
		segs[i] = encodeURIComponent(s)
	}
	return strings.Join(segs, "/"), true
}

// buildRecipe is grammar.js build for a recipe on a mount of grammar g
// bound to source.
func buildRecipe(r *playground.Recipe, g, source string, idx map[string]*playground.Option) (path, rawQuery string, err error) {
	src := r.Source
	if r.Raw != nil {
		key, _ := keyOn(src, source)
		fill := func(s string) string {
			return strings.ReplaceAll(strings.ReplaceAll(s, "{src}", escapePlain(src)), "{key}", key)
		}
		path = fill(strings.TrimSpace(r.Raw.Path))
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return path, fill(strings.TrimSpace(r.Raw.Query)), nil
	}
	pipes := make([][]string, len(r.Pipelines))
	var params []string
	for pi, p := range r.Pipelines {
		for _, e := range p {
			o, vals, err := entryValues(e, g, idx)
			if err != nil {
				return "", "", err
			}
			var s string
			switch g {
			case playground.GrammarImgproxy:
				s = strings.Join(append([]string{o.Name}, vals...), ":")
			case playground.GrammarImgix, playground.GrammarQuery:
				if pi > 0 {
					return "", "", fmt.Errorf("%s has no chained pipelines", g)
				}
				if isFlag(o) {
					s = encodeURIComponent(o.Name) + "=true"
				} else {
					s = encodeURIComponent(o.Name) + "=" + encodeURIComponent(strings.Join(vals, ","))
				}
				params = append(params, s)
				continue
			case playground.GrammarCloudinary:
				v := escapeValue(strings.Join(vals, ","))
				switch {
				case isFlag(o):
					s = o.Name
				case strings.HasPrefix(o.Name, "e_"), strings.HasPrefix(o.Name, "fl_"):
					s = o.Name + ":" + v
				default:
					s = o.Name + "_" + v
				}
			case playground.GrammarImagekit:
				v := escapeValue(strings.Join(vals, ","))
				switch {
				case isFlag(o):
					s = o.Name
				case strings.HasPrefix(o.Name, "l-"):
					s = o.Name + "," + v + ",l-end"
				default:
					s = o.Name + "-" + v
				}
			default:
				return "", "", fmt.Errorf("grammar %s not supported by recipes", g)
			}
			pipes[pi] = append(pipes[pi], s)
		}
	}
	for len(pipes) > 1 && len(pipes[len(pipes)-1]) == 0 {
		pipes = pipes[:len(pipes)-1]
	}
	joined := func(sep, between string) string {
		var parts []string
		for _, p := range pipes {
			if len(p) > 0 {
				parts = append(parts, strings.Join(p, sep))
			}
		}
		return strings.Join(parts, between)
	}
	own, isOwn := keyOn(src, source)
	isHTTP := strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://")
	switch g {
	case playground.GrammarImgproxy:
		opts := joined("/", "/-/")
		if opts != "" {
			path = "/" + opts
		}
		path += "/plain/" + escapePlain(src)
		if r.Ext != "" {
			path += "@" + r.Ext
		}
		if r.Video {
			path = "/video" + path
		}
		return path, "", nil
	case playground.GrammarQuery:
		path = "/"
		if r.Video {
			path = "/video"
		}
		return path, strings.Join(append([]string{"url=" + encodeURIComponent(src)}, params...), "&"), nil
	case playground.GrammarImgix:
		switch {
		case isOwn:
			path = "/" + own
		case isHTTP:
			path = "/" + encodeURIComponent(src)
		default:
			return "", "", fmt.Errorf("imgix takes an http(s) URL or a key on %q", source)
		}
		return path, strings.Join(params, "&"), nil
	case playground.GrammarCloudinary:
		kind, id := "upload", own
		if !isOwn {
			if !isHTTP {
				return "", "", fmt.Errorf("cloudinary takes an http(s) URL or a key on %q", source)
			}
			kind, id = "fetch", escapePlain(src)
		}
		path = "/xi/image/" + kind
		if c := joined(",", "/"); c != "" {
			path += "/" + c
		}
		return path + "/" + id, "", nil
	case playground.GrammarImagekit:
		if !isOwn {
			return "", "", fmt.Errorf("imagekit takes a key on %q", source)
		}
		if steps := joined(",", ":"); steps != "" {
			path = "/tr:" + steps
		}
		return path + "/" + own, "", nil
	}
	return "", "", fmt.Errorf("grammar %s not supported by recipes", g)
}
