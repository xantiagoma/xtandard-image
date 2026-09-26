package router_test

import (
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/dialect/wsrv"
)

// TestPagesAcrossDialects: every dialect's page and frame parameters land
// on the same Pipeline.Page (0-based first page or frame) and Pipeline.Pages
// (count, -1 = to the end), which the planner turns into page selection,
// stacking or an animation's frame range (WP-62).
func TestPagesAcrossDialects(t *testing.T) {
	t.Parallel()
	ip, err := imgproxy.New(&imgproxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ix, err := imgix.New(&imgix.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ik, err := imagekit.New(&imagekit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q, err := query.New(&query.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := cloudinary.New(&cloudinary.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := wsrv.New(&wsrv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	type sel struct{ Page, Pages int } // -2 = unset
	const unset = -2
	for _, tc := range []struct {
		name  string
		d     dialect.Dialect
		path  string
		query string
		want  sel
	}{
		{"imgproxy page", ip, "/unsafe/pg:2/plain/http://example.com/a.gif", "", sel{2, unset}},
		{"imgproxy pages", ip, "/unsafe/page:1/pages:3/plain/http://example.com/a.gif", "", sel{1, 3}},
		{"imgproxy pages all", ip, "/unsafe/pgs:-1/plain/http://example.com/a.gif", "", sel{unset, -1}},
		{"query page", q, "/", "url=http://example.com/a.gif&page=2", sel{2, unset}},
		{"query pages", q, "/", "url=http://example.com/a.gif&page=1&pages=3", sel{1, 3}},
		{"query o=", q, "/", "url=http://example.com/a.gif&o=pg:1/pgs:-1", sel{1, -1}},
		{"imgix page is 1-based", ix, "/a.gif", "page=3", sel{2, unset}},
		{"imgix frame selects one", ix, "/a.gif", "frame=3", sel{2, 1}},
		{"imagekit pg", ik, "/tr:pg-3/a.gif", "", sel{2, unset}},
		{"imagekit pg range", ik, "/tr:pg-2_4/a.gif", "", sel{1, 3}},
		{"cloudinary pg is 1-based", cl, "/demo/image/upload/pg_3/a.gif", "", sel{2, unset}},
		{"wsrv page", ws, "/", "url=example.com/a.gif&page=2", sel{2, unset}},
		{"wsrv n", ws, "/", "url=example.com/a.gif&page=1&n=3", sel{1, 3}},
		{"wsrv n all", ws, "/", "url=example.com/a.gif&n=-1", sel{unset, -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := tc.d.Parse(&dialect.Input{Path: tc.path, RawQuery: tc.query, Query: parseQuery(t, tc.query)})
			if err != nil {
				t.Fatal(err)
			}
			pl := res.Request.Pipelines[0]
			got := sel{unset, unset}
			if pl.Page != nil {
				got.Page = *pl.Page
			}
			if pl.Pages != nil {
				got.Pages = *pl.Pages
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
			for _, ig := range res.Request.Ignored {
				t.Errorf("ignored %s (%s)", ig.Option, ig.Reason)
			}
		})
	}
}

func parseQuery(tb testing.TB, raw string) url.Values {
	tb.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		tb.Fatal(err)
	}
	return v
}
