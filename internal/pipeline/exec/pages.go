package exec

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// replanPage plans again for a loaded page selection (the `page` option of
// a multi-page still, or `pages` stacked) whose size differs from the
// header's, which describes the first page. Nothing was shrunk on load, so
// the loaded size is the selection's source size and the new plan starts
// from it.
func (r *runner) replanPage(w, h int) error {
	meta := r.meta
	meta.Width, meta.Height, meta.PageHeight, meta.Selection = w, h, 0, true
	if r.stackAlpha {
		meta.HasAlpha = true // transparent gaps beside narrower pages
	}
	p, err := plan.Build(r.req, meta, r.e.cfg.Limits)
	if err != nil {
		return err
	}
	r.meta, r.plan = meta, p
	return nil
}

// loadStack loads ls.NPages pages from ls.Page on one at a time and stacks
// them vertically, left-aligned (imgproxy Pro `pages`). Raster loaders
// refuse to load pages of different sizes as one strip (tiffload: "page 1
// differs from page 0"), so the pages are joined here. When the pages differ
// in width, band count or alpha they are brought to a common shape first:
// sRGB when the band counts differ, and an alpha band on every page when the
// widths differ or any page has alpha, so the gaps beside narrower pages are
// transparent and the plan flattens them like any alpha (stackAlpha).
func (r *runner) loadStack(ctx context.Context, ls *plan.LoadStep) (*vips.Image, error) {
	one := *ls
	one.NPages = 1
	pages := make([]*vips.Image, 0, ls.NPages)
	for i := range ls.NPages {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		one.Page = ls.Page + i
		p, err := r.a.Add(vips.Load(ctx, r.j.Source, r.loadOptions(&one, nil)))
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	w, bands := 0, pages[0].Bands()
	var varying, mixed, alpha bool
	for _, p := range pages {
		varying = varying || (w > 0 && p.Width() != w)
		w = max(w, p.Width())
		mixed = mixed || p.Bands() != bands
		alpha = alpha || p.HasAlpha()
	}
	var out *vips.Image
	for _, p := range pages {
		var err error
		if mixed && p.Interpretation() != vips.InterpretationSRGB {
			if p, err = r.a.Add(p.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
				return nil, err
			}
		}
		if (varying || alpha) && !p.HasAlpha() {
			if p, err = r.a.Add(p.AddAlpha(ctx)); err != nil {
				return nil, err
			}
		}
		if p.Width() < w {
			// Transparent on the right: the alpha band is 0 there.
			if p, err = r.a.Add(p.Embed(ctx, 0, 0, w, p.Height(), vips.ExtendBlack, nil)); err != nil {
				return nil, err
			}
		}
		// vips_arrayjoin would give every page the tallest page's height:
		// join the equally wide pages one below the other instead.
		if out == nil {
			out = p
		} else if out, err = r.a.Add(out.Join(ctx, p, vips.DirectionVertical)); err != nil {
			return nil, err
		}
	}
	r.stackAlpha = (varying || alpha) && !r.meta.HasAlpha
	return out, nil
}

// measureSelection sizes the page selection of a vector source before it is
// rendered: a PDF page other than the first, or several pages stacked, has
// a size the header does not tell, and the render scale has to be chosen
// for that size (render at the target size, never upscale a small render).
// PDFium lays the pages out without rendering anything.
func (r *runner) measureSelection(ctx context.Context) error {
	ls := &r.plan.Load
	// Every page of an EPS/PostScript document renders at the size of its
	// DSC box (the device is fixed to it), which the header gave.
	if !r.meta.IsVector || r.meta.Format == formatEPS || r.meta.Selection || r.plan.Animation != nil ||
		(ls.Page == 0 && ls.NPages <= 1) {
		return nil
	}
	img, err := vips.Load(ctx, r.j.Source, &vips.LoadOptions{
		Density: ls.Density, Page: ls.Page, NPages: max(ls.NPages, 1),
		Access: vips.AccessSequential, FailOn: r.e.cfg.FailOn,
	})
	if err != nil {
		return err
	}
	w, h := img.Width(), img.Height()
	img.Close()
	if w == ls.Width && h == ls.Height && ls.Scale == 0 {
		return nil
	}
	return r.replanPage(w, h)
}

// pdfTransparent reports whether PDF pages render on a transparent
// background: when the request sets a background colour (the plan's
// flatten then paints it, exactly like for any source with alpha), and
// when it names an output format that carries alpha itself and sets no
// background (negotiated formats do not count). Otherwise pages render onto
// opaque white (FORMATS.md, PDF). PDFium's own background option is only
// ever white or transparent: libvips hands it other colours with red and
// blue swapped.
func pdfTransparent(j *Job, req *transform.Request) bool {
	if j.Meta.Format != "pdf" {
		return false
	}
	if len(req.Pipelines) > 0 && req.Pipelines[0].Background != nil {
		return true
	}
	if j.Request.Output.Format == transform.FormatUnset {
		return false
	}
	switch req.Output.Format {
	case transform.FormatPNG, transform.FormatWebP, transform.FormatAVIF, transform.FormatJXL,
		transform.FormatTIFF, transform.FormatGIF, transform.FormatHEIC:
		return true
	default:
		return false
	}
}

// pdfBackground is the PDFium page background of this run: transparent or
// opaque white.
func (r *runner) pdfBackground() []float64 {
	if r.transparent {
		return []float64{0, 0, 0, 0}
	}
	return []float64{255, 255, 255, 255}
}
