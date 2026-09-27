package translate

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/playground"
)

// TestCanonicalInput translates the canonical request of a URL and checks
// it gives the same URLs as the URL itself.
func TestCanonicalInput(t *testing.T) {
	t.Parallel()
	tr := newTranslator(t)
	fromURL, err := tr.Translate(&playground.TranslateRequest{Mount: "imgproxy", Path: "/rs:fill:300:200:1/g:sm/bl:2/plain/xi://local/a.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(fromURL.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	fromReq, err := tr.Translate(&playground.TranslateRequest{Request: m})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(fromURL.Targets, fromReq.Targets); diff != "" {
		t.Errorf("targets differ (-url +request):\n%s", diff)
	}
}

func TestTranslateFromOtherDialects(t *testing.T) {
	t.Parallel()
	tr := newTranslator(t)
	tests := []struct {
		name, mount, path, query string
		want                     string // the imgproxy target URL
	}{
		{"imgix", "imgix", "/photos/a.jpg", "w=300&h=200&fit=crop&crop=faces&fm=webp", "/rs:fill:300:200:1/g:obj:face/f:webp/plain/xi://local/photos/a.jpg"},
		{"query", "query", "/", "url=https%3A%2F%2Fexample.com%2Fa.jpg&w=300&blur=2", "/rs::300/bl:2/plain/https://example.com/a.jpg"},
		{"thumbor", "thumbor", "/300x200/smart/https%3A%2F%2Fexample.com%2Fa.jpg", "", "/rs:fill:300:200:1/g:sm/plain/https://example.com/a.jpg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := tr.Translate(&playground.TranslateRequest{Mount: tc.mount, Path: tc.path, Query: tc.query})
			if err != nil {
				t.Fatal(err)
			}
			if got := res.Targets[0]; got.Dialect != "imgproxy" || got.Path != tc.want {
				t.Errorf("imgproxy target = %s %q, want %q", got.Dialect, got.Path, tc.want)
			}
		})
	}
}

func TestTranslateErrors(t *testing.T) {
	t.Parallel()
	tr := newTranslator(t)
	for _, in := range []*playground.TranslateRequest{
		{Mount: "nope", Path: "/"},
		{Request: map[string]any{"Nope": 1}},
		{Request: map[string]any{"Kind": "process"}},
		{Mount: "imgproxy", Path: "/raw:1/plain/https://example.com/a.jpg"},
	} {
		if _, err := tr.Translate(in); !errors.As(err, new(*badRequestError)) {
			t.Errorf("Translate(%+v) error = %v, want a bad request", in, err)
		}
	}
	// A malformed URL is the dialect's own client-safe error.
	if _, err := tr.Translate(&playground.TranslateRequest{Mount: "imgproxy", Path: "/rs:nope/plain/https://e.com/a.jpg"}); publicText(err) == "invalid url" {
		t.Errorf("parse error %v has no public text", err)
	}
}

func FuzzTranslate(f *testing.F) {
	f.Add("imgproxy", "/rs:fill:300:200/g:sm/plain/https://example.com/a.jpg", "")
	f.Add("imgix", "/a.jpg", "w=300&fit=crop&crop=faces,entropy&txt=hi&mark=https://e.com/m.png")
	f.Add("query", "/", "url=xi%3A%2F%2Flocal%2Fa.jpg&o=rs:fit:10:10/-/bl:1")
	f.Add("thumbor", "/trim/10x20:30x40/fit-in/-300x0/left/top/filters:blur(3):quality(80)/a.jpg", "")
	f.Add("cloudinary", "/xi/image/upload/w_300,c_fill,g_face,e_blur:100/a.jpg", "")
	f.Add("imagekit", "/tr:w-300,h-200,fo-auto:bl-10/a.jpg", "")
	f.Add("bunny", "/a.jpg", "width=300&aspect_ratio=16:9&blur=10")
	f.Add("awssih", "/eyJrZXkiOiJhLmpwZyIsImVkaXRzIjp7InJlc2l6ZSI6eyJ3aWR0aCI6MjAwfX19", "")
	tr, err := New(Options{Mounts: testMounts()})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, mount, path, query string) {
		res, err := tr.Translate(&playground.TranslateRequest{Mount: mount, Path: path, Query: query})
		if err != nil {
			return
		}
		for i := range res.Targets {
			tg := &res.Targets[i]
			if tg.Error == "" && tg.Path == "" {
				t.Errorf("%s: no path and no error", tg.Dialect)
			}
		}
	})
}
