//go:build cgo

package exec_test

import (
	"bytes"
	"net/url"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/dialect/thumbor"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestDialectWatermarkGolden runs watermarks written in the dialects that
// have no app mount in the app tests (imgix, thumbor, imaginary) through
// the executor, with the badge as every per-request image, and compares
// them with testdata/golden/exec/<name>.png. cloudinary and imagekit are
// covered end to end in internal/app.
func TestDialectWatermarkGolden(t *testing.T) {
	t.Parallel()
	ix, err := imgix.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	th := thumbor.New(nil)
	im, err := query.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	e := newWatermarkExecutor(t, nil)
	for _, tc := range []struct {
		name, path, query string
		d                 dialect.Dialect
	}{
		{name: "wm-imgix-mark", d: ix, path: "/a.jpg", query: "w=200&mark=https://example.com/badge.png&mark-w=40&mark-align=bottom,right&mark-pad=4&mark-alpha=90&mark-rot=20"},
		{name: "wm-imgix-mark-tile", d: ix, path: "/a.jpg", query: "w=200&mark=https://example.com/badge.png&mark-scale=12&mark-tile=grid&mark-alpha=70"},
		{name: "wm-imgix-txt", d: ix, path: "/a.jpg", query: "w=200&txt=Hello%20%3Cworld%3E&txt-size=18&txt-color=ffffff&txt-align=top,left&txt-shad=2"},
		{name: "wm-imgix-txt-width", d: ix, path: "/a.jpg", query: "w=200&txt=wrapped+at+eighty+pixels&txt-size=14&txt-width=80&txt-align=middle,center&txt-font=DejaVu+Sans,Bold"},
		{name: "wm-thumbor", d: th, path: "/unsafe/200x0/filters:watermark(https://example.com/badge.png,-4,-4,10,20)/a.jpg"},
		{name: "wm-imaginary-text", d: im, path: "/watermark", query: "url=https://example.com/a.jpg&width=200&text=imaginary&opacity=0.6&textwidth=100&color=255,255,255"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			res, err := tc.d.Parse(&dialect.Input{Path: tc.path, Query: q, RawQuery: tc.query})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			req := res.Request
			req.ApplyDefaults(transform.StandardDefaults())
			if err := req.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			j, buf := job(t, readFixture(t, jpegPhoto), req, transform.FormatPNG)
			defer buf.Release()
			badge := newBuffer(t, readFixture(t, watermarkBadge))
			defer badge.Release()
			for i := range req.Pipelines[0].Watermarks {
				if req.Pipelines[0].Watermarks[i].Source != nil {
					j.Watermarks = append(j.Watermarks, exec.WatermarkImage{Index: i, Source: badge, Format: "png"})
				}
			}
			out, err := e.Run(t.Context(), j)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			defer out.Body.Release()
			if len(out.Skipped) > 0 {
				t.Errorf("Skipped = %v", out.Skipped)
			}
			img, _ := decode(t, bytes.Clone(out.Body.Bytes()))
			golden.Compare(t, "exec/"+tc.name, img, golden.Options{Tolerance: 0.03})
		})
	}
}
