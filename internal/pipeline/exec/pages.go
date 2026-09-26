package exec

import "github.com/xantiagoma/xtandard-image/internal/pipeline/plan"

// replanPage plans again for a loaded page (the `page` option of a
// multi-page still) whose size differs from the header's, which describes
// the first page. Nothing was shrunk on load, so the loaded size is the
// page's source size and the new plan starts from it.
func (r *runner) replanPage(w, h int) error {
	meta := r.meta
	meta.Width, meta.Height, meta.PageHeight = w, h, 0
	p, err := plan.Build(r.req, meta, r.e.cfg.Limits)
	if err != nil {
		return err
	}
	r.meta, r.plan = meta, p
	return nil
}
