package plan_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestBuildPagesMatrix is the page/pages matrix of WP-62 for animated
// (GIF, WebP) and multi-page (TIFF) sources and every kind of output: an
// animation that stays one takes `page` as its first frame and `pages` as
// its frame count; one that does not (still output, disable_animation)
// selects frame `page`, and `pages` stacks that many frames vertically
// (imgproxy Pro); a multi-page still stacks its pages the same way.
func TestBuildPagesMatrix(t *testing.T) {
	t.Parallel()
	type want struct {
		Page, NPages int
		Frames       int // Animation.Frames; 0 = processed as a still
		W, H         int // one output frame
	}
	anim := func(format string) plan.SourceMeta {
		return plan.SourceMeta{Width: 96, Height: 72, Frames: 6, IsAnimated: true, Format: format}
	}
	tiff := plan.SourceMeta{Width: 96, Height: 72, Frames: 3, Format: "tiff"}
	pg := func(page, pages int) transform.Pipeline {
		p := transform.Pipeline{}
		if page >= 0 {
			p.Page = &page
		}
		if pages != 0 {
			p.Pages = &pages
		}
		return p
	}
	for _, src := range []string{"gif", "webp"} {
		for _, tc := range []struct {
			name string
			p    transform.Pipeline
			out  transform.Format
			want want
		}{
			{"animated/all", pg(-1, 0), transform.FormatGIF, want{0, 6, 6, 96, 72}},
			{"animated/page 2", pg(2, 0), transform.FormatWebP, want{2, 4, 4, 96, 72}},
			{"animated/pages 3", pg(-1, 3), transform.FormatGIF, want{0, 3, 3, 96, 72}},
			{"animated/page 1 pages 2", pg(1, 2), transform.FormatWebP, want{1, 2, 2, 96, 72}},
			{"animated/pages 1 is a still", pg(4, 1), transform.FormatGIF, want{4, 1, 0, 96, 72}},
			{"still/first frame", pg(-1, 0), transform.FormatPNG, want{0, 1, 0, 96, 72}},
			{"still/page 3", pg(3, 0), transform.FormatJPEG, want{3, 1, 0, 96, 72}},
			{"still/page past the end", pg(9, 0), transform.FormatPNG, want{5, 1, 0, 96, 72}},
			{"still/pages 2 stack", pg(-1, 2), transform.FormatPNG, want{0, 2, 0, 96, 144}},
			{"still/page 4 pages all", pg(4, -1), transform.FormatJPEG, want{4, 2, 0, 96, 144}},
			{"still/pages beyond the end", pg(3, 10), transform.FormatPNG, want{3, 3, 0, 96, 216}},
		} {
			t.Run(src+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				r := req(tc.p)
				r.Output.Format = tc.out
				p, err := plan.Build(r, anim(src), plan.Limits{})
				if err != nil {
					t.Fatal(err)
				}
				got := want{Page: p.Load.Page, NPages: p.Load.NPages, W: p.Output.Width, H: p.Output.Height}
				if p.Animation != nil {
					got.Frames = p.Animation.Frames
				}
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Errorf("(-want +got):\n%s", diff)
				}
			})
		}
	}
	t.Run("gif/disable_animation stacks", func(t *testing.T) {
		t.Parallel()
		p := pg(1, 2)
		p.DisableAnimation = true
		r := req(p)
		r.Output.Format = transform.FormatGIF
		got, err := plan.Build(r, anim("gif"), plan.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Animation != nil || got.Load.Page != 1 || got.Load.NPages != 2 || got.Output.Height != 144 {
			t.Errorf("Load %+v Animation %v Output %+v, want frames 1-2 stacked", got.Load, got.Animation, got.Output)
		}
	})
	for _, tc := range []struct {
		name string
		p    transform.Pipeline
		want want
	}{
		{"tiff/page 1", pg(1, 0), want{1, 1, 0, 96, 72}},
		{"tiff/pages all", pg(-1, -1), want{0, 3, 0, 96, 216}},
		{"tiff/page 1 pages 5", pg(1, 5), want{1, 2, 0, 96, 144}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.Format = transform.FormatPNG
			p, err := plan.Build(r, tiff, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			got := want{Page: p.Load.Page, NPages: p.Load.NPages, W: p.Output.Width, H: p.Output.Height}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

// TestEnforceThumbnail: with enforce_thumbnail a HEIF/AVIF still always
// loads its embedded thumbnail, even when no resize would make it worth it,
// and the plan continues on the thumbnail's size; without it the thumbnail
// is used only when it is large enough for the target.
func TestEnforceThumbnail(t *testing.T) {
	t.Parallel()
	heic := plan.SourceMeta{Width: 4000, Height: 3000, Format: "heic"}
	on, off := true, false
	for _, tc := range []struct {
		name      string
		enforce   *bool
		p         transform.Pipeline
		meta      plan.SourceMeta
		thumb     bool   // first plan asks for the thumbnail
		continued bool   // plan after observing a 320×240 thumbnail keeps it
		out       [2]int // output after the observation
		obs       [2]int // observed thumbnail size
	}{
		{name: "enforced/no resize", enforce: &on, meta: heic, thumb: true, continued: true, out: [2]int{320, 240}, obs: [2]int{320, 240}},
		{name: "enforced/larger target", enforce: &on, meta: heic, p: transform.Pipeline{Resize: transform.Resize{Width: px(1000)}}, thumb: true, continued: true, out: [2]int{320, 240}, obs: [2]int{320, 240}},
		{name: "enforced/avif", enforce: &on, meta: plan.SourceMeta{Width: 4000, Height: 3000, Format: "avif"}, thumb: true, continued: true, out: [2]int{320, 240}, obs: [2]int{320, 240}},
		{name: "not enforced/no resize", enforce: &off, meta: heic, out: [2]int{4000, 3000}},
		{name: "not enforced/thumbnail too small", enforce: &off, meta: heic, p: transform.Pipeline{Resize: transform.Resize{Width: px(1000)}}, thumb: true, out: [2]int{1000, 750}, obs: [2]int{320, 240}},
		{name: "enforced/jpeg left to the executor", enforce: &on, meta: jpeg(4000, 3000), out: [2]int{4000, 3000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := req(tc.p)
			r.Output.EnforceThumbnail = tc.enforce
			p, err := plan.Build(r, tc.meta, plan.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if p.Load.Thumbnail != tc.thumb {
				t.Fatalf("Load.Thumbnail = %v, want %v", p.Load.Thumbnail, tc.thumb)
			}
			if tc.thumb {
				if p, err = plan.Continue(r, tc.meta, plan.Limits{}, plan.Observed{Width: tc.obs[0], Height: tc.obs[1], Thumbnail: true}); err != nil {
					t.Fatal(err)
				}
				if p.Load.Thumbnail != tc.continued {
					t.Fatalf("after the observation Load.Thumbnail = %v, want %v", p.Load.Thumbnail, tc.continued)
				}
			}
			if got := size(p); got != tc.out {
				t.Errorf("output %v, want %v", got, tc.out)
			}
		})
	}
}
