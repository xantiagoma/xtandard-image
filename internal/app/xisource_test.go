package app_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

type webSource struct{}

func (webSource) Name() string { return "web" }
func (webSource) Type() string { return source.TypeHTTP }
func (webSource) Fetch(context.Context, source.Key, source.FetchOptions) (*source.Object, error) {
	return nil, errs.New(errs.KindNotFound, "fake")
}

// TestXISourceAbsoluteKey: an absolute http(s) URL inside
// xi://<http source>/ survives the plain/ (literal, percent-encoded,
// proxy-collapsed) and base64 source forms and resolves through the named
// source (WP-95).
func TestXISourceAbsoluteKey(t *testing.T) {
	t.Parallel()
	p, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := source.NewRegistry(source.Options{Sources: []source.Binding{{
		Spec:   source.Spec{Name: "web", Type: source.TypeHTTP, Allow: []string{"*"}},
		Source: webSource{},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	const (
		origin = "https://raw.githubusercontent.com/link-u/avif-sample-images/refs/heads/master/fox.profile0.10bpc.yuv420.avif"
		xi     = "xi://web/" + origin
	)
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	tests := []struct {
		name string
		path string
		want string // resolved Key.Path
	}{
		{name: "plain literal", path: "/unsafe/rs:fit:64/plain/" + xi + "@jpg", want: origin},
		{name: "plain without options", path: "/unsafe/plain/" + xi, want: origin},
		{name: "plain inner url collapsed", path: "/unsafe/plain/xi://web/https:/raw.githubusercontent.com/a/b.png", want: "https://raw.githubusercontent.com/a/b.png"},
		{name: "plain xi collapsed", path: "/unsafe/plain/xi:/web/https://raw.githubusercontent.com/a/b.png", want: "https://raw.githubusercontent.com/a/b.png"},
		{name: "plain fully percent-encoded", path: "/unsafe/plain/xi%3A%2F%2Fweb%2Fhttps%3A%2F%2Fraw.githubusercontent.com%2Fa%2Fb%20c.png@webp", want: "https://raw.githubusercontent.com/a/b%20c.png"},
		{name: "plain inner query encoded", path: "/unsafe/plain/xi://web/https://h.example.org/a.png%3Fv%3D2", want: "https://h.example.org/a.png?v=2"},
		{name: "base64", path: "/unsafe/rs:fit:64/" + b64(xi) + ".jpg", want: origin},
		{name: "base64 split", path: "/unsafe/" + b64(xi)[:20] + "/" + b64(xi)[20:], want: origin},
		{name: "base64 with query", path: "/unsafe/" + b64("xi://web/https://h.example.org/a.png?v=2"), want: "https://h.example.org/a.png?v=2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := p.ParsePath(tc.path)
			if err != nil {
				t.Fatalf("ParsePath(%q): %v", tc.path, err)
			}
			key, err := reg.Resolve(res.Request.Source)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", res.Request.Source.Raw, err)
			}
			if diff := cmp.Diff(source.Key{Source: "web", Path: tc.want}, key); diff != "" {
				t.Errorf("Resolve(%q) mismatch (-want +got):\n%s", res.Request.Source.Raw, diff)
			}
		})
	}
}
