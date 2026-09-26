package cloudflare_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// unpicRouter mounts both dialects the way the default config does, with
// the Images dialect also bound to the imagedelivery.net host.
func unpicRouter(t *testing.T) *router.Router {
	t.Helper()
	f := getFixture(t)
	rt, err := router.New(&router.Options{
		Policy: router.PolicyAllow,
		Mounts: []router.Mount{
			{Name: "cloudflare", Dialect: cloudflare.Name, Prefix: "/cdn-cgi/image/", Source: "origin"},
			{
				Name: "cloudflare_images", Dialect: cloudflare.ImagesName, Prefix: "/cdn-cgi/imagedelivery/",
				Hosts: []string{"imagedelivery.net"}, Source: "cfimages",
			},
		},
	}, map[string]dialect.Dialect{
		cloudflare.Name:       f.parsers["default"],
		cloudflare.ImagesName: f.parsers["images"],
	})
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

// unpicOps is the subset of unpic's parsed operations the fixtures use,
// expressed as the request fields they map to.
type unpicOps struct {
	Dialect    string
	Source     string
	Mount      string
	Width      int
	Height     int
	Fit        transform.ResizeType
	Enlarge    bool
	Format     transform.Format
	Background *transform.Color
	Rotate     int
	Gravity    transform.GravityType
}

func opsOf(r *transform.Request) unpicOps {
	pl := r.Pipelines[0]
	return unpicOps{
		Dialect: r.Dialect, Source: r.Source.Raw, Mount: r.Source.Mount,
		Width: pl.Resize.Width.Px, Height: pl.Resize.Height.Px,
		Fit: pl.Resize.Type, Enlarge: pl.Resize.Enlarge,
		Format: r.Output.Format, Background: pl.Background, Rotate: pl.Rotate, Gravity: pl.Gravity.Type,
	}
}

// TestUnpicFixtures parses every URL of unpic's cloudflare.test.ts and
// cloudflare_images.test.ts (extract inputs and generate/transform
// outputs) and checks the result against unpic's expectations: the same
// source (or account hash and image id) and the operations unpic reads.
// f=auto is the negotiated format (FormatUnset).
func TestUnpicFixtures(t *testing.T) {
	t.Parallel()
	red := &transform.Color{R: 255, A: 255}
	const avatar = "/uploads/generic/avatar-sample.jpeg"
	const cfImages = "/cdn-cgi/imagedelivery/" + unpicAccount + "/" + unpicImage
	tests := []struct {
		name string
		url  string
		want unpicOps
	}{
		// cloudflare.test.ts
		{
			"cloudflare extract", "https://assets.brevity.io/cdn-cgi/image/background=red,width=128,height=128,f=auto/uploads/generic/avatar-sample.jpeg",
			unpicOps{Dialect: cloudflare.Name, Source: avatar, Mount: "origin", Width: 128, Height: 128, Background: red},
		},
		{
			"cloudflare extract without domain", "/cdn-cgi/image/background=red,width=128,height=128,f=auto/uploads/generic/avatar-sample.jpeg",
			unpicOps{Dialect: cloudflare.Name, Source: avatar, Mount: "origin", Width: 128, Height: 128, Background: red},
		},
		{
			"cloudflare transform", "https://assets.brevity.io/cdn-cgi/image/background=red,width=100,height=200,f=auto,fit=cover/uploads/generic/avatar-sample.jpeg",
			unpicOps{Dialect: cloudflare.Name, Source: avatar, Mount: "origin", Width: 100, Height: 200, Fit: transform.ResizeFill, Enlarge: true, Background: red},
		},
		{
			"cloudflare generate", "https://assets.brevity.io/cdn-cgi/image/width=200,height=100,f=webp,fit=cover/uploads/generic/avatar-sample.jpeg",
			unpicOps{Dialect: cloudflare.Name, Source: avatar, Mount: "origin", Width: 200, Height: 100, Fit: transform.ResizeFill, Enlarge: true, Format: transform.FormatWebP},
		},
		{
			"cloudflare generate without domain", "/cdn-cgi/image/width=200,height=100,f=webp,fit=cover/uploads/generic/avatar-sample.jpeg",
			unpicOps{Dialect: cloudflare.Name, Source: avatar, Mount: "origin", Width: 200, Height: 100, Fit: transform.ResizeFill, Enlarge: true, Format: transform.FormatWebP},
		},
		{
			"cloudflare generate absolute src", "https://assets.brevity.io/cdn-cgi/image/width=200,height=100,f=webp,fit=cover/https://example.com/uploads/generic/avatar-sample.jpeg",
			unpicOps{Dialect: cloudflare.Name, Source: "https://example.com/uploads/generic/avatar-sample.jpeg", Mount: "origin", Width: 200, Height: 100, Fit: transform.ResizeFill, Enlarge: true, Format: transform.FormatWebP},
		},
		// generate() with an absolute src containing "?" (encodeURIComponent).
		{
			"cloudflare generate absolute src with query", "/cdn-cgi/image/width=200,f=auto,fit=cover/" + url.QueryEscape("https://example.com/a.jpg?v=1"),
			unpicOps{Dialect: cloudflare.Name, Source: "https://example.com/a.jpg?v=1", Mount: "origin", Width: 200, Fit: transform.ResizeFill, Enlarge: true},
		},
		// cloudflare_images.test.ts
		{
			"images extract", "https://100francisco.com" + cfImages + "/w=128,h=128,rotate=90,f=auto",
			unpicOps{Dialect: cloudflare.ImagesName, Source: "/" + unpicImage, Mount: "cfimages", Width: 128, Height: 128, Rotate: 90},
		},
		{
			"images extract imagedelivery.net", "https://imagedelivery.net/" + unpicAccount + "/" + unpicImage + "/w=128,h=128,rotate=90,f=auto",
			unpicOps{Dialect: cloudflare.ImagesName, Source: "/" + unpicImage, Mount: "cfimages", Width: 128, Height: 128, Rotate: 90},
		},
		{
			"images generate", "https://100francisco.com" + cfImages + "/w=256,h=256,f=webp,fit=cover",
			unpicOps{Dialect: cloudflare.ImagesName, Source: "/" + unpicImage, Mount: "cfimages", Width: 256, Height: 256, Fit: transform.ResizeFill, Enlarge: true, Format: transform.FormatWebP},
		},
		{
			"images transform", "https://100francisco.com" + cfImages + "/rotate=90,w=256,h=256,f=webp,fit=cover",
			unpicOps{Dialect: cloudflare.ImagesName, Source: "/" + unpicImage, Mount: "cfimages", Width: 256, Height: 256, Fit: transform.ResizeFill, Enlarge: true, Format: transform.FormatWebP, Rotate: 90},
		},
		{
			"images transform imagedelivery.net", "https://imagedelivery.net/" + unpicAccount + "/" + unpicImage + "/rotate=90,w=256,h=256,f=webp,fit=cover",
			unpicOps{Dialect: cloudflare.ImagesName, Source: "/" + unpicImage, Mount: "cfimages", Width: 256, Height: 256, Fit: transform.ResizeFill, Enlarge: true, Format: transform.FormatWebP, Rotate: 90},
		},
		{
			"images transform gravity", "https://100francisco.com" + cfImages + "/rotate=90,gravity=auto,w=256,h=256,f=webp,fit=cover",
			unpicOps{Dialect: cloudflare.ImagesName, Source: "/" + unpicImage, Mount: "cfimages", Width: 256, Height: 256, Fit: transform.ResizeFill, Enlarge: true, Format: transform.FormatWebP, Rotate: 90, Gravity: transform.GravitySmart},
		},
		// extract() src without transformations.
		{
			"images src", "https://100francisco.com" + cfImages,
			unpicOps{Dialect: cloudflare.ImagesName, Source: "/" + unpicImage, Mount: "cfimages"},
		},
	}
	rt := unpicRouter(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, http.NoBody)
			res, _, err := rt.Route(req)
			if err != nil {
				t.Fatalf("Route(%s): %v", tc.url, err)
			}
			if len(res.Request.Ignored) > 0 {
				t.Errorf("ignored options: %+v", res.Request.Ignored)
			}
			if d := cmp.Diff(tc.want, opsOf(res.Request)); d != "" {
				t.Errorf("(-unpic +got):\n%s", d)
			}
		})
	}

	// extract() returns null for URLs of other providers: no mount matches.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/image.jpg", http.NoBody)
	if _, _, err := rt.Route(req); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("non-Cloudflare URL: err = %v, want not found", err)
	}
}
