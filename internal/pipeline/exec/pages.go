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
	p, err := plan.Build(r.req, meta, r.e.cfg.Limits)
	if err != nil {
		return err
	}
	r.meta, r.plan = meta, p
	return nil
}

// measureSelection sizes the page selection of a vector source before it is
// rendered: a PDF page other than the first, or several pages stacked, has
// a size the header does not tell, and the render scale has to be chosen
// for that size (render at the target size, never upscale a small render).
// PDFium lays the pages out without rendering anything.
func (r *runner) measureSelection(ctx context.Context) error {
	ls := &r.plan.Load
	if !r.meta.IsVector || r.meta.Selection || r.plan.Animation != nil || (ls.Page == 0 && ls.NPages <= 1) {
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
