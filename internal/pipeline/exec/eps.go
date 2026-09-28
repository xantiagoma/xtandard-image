package exec

import (
	"context"
	"io"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/ghostscript"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// formatEPS is the Job.Meta.Format of EPS, PostScript and PostScript
// Illustrator sources.
const formatEPS = "eps"

// EPSRenderer renders one page of a PostScript program
// (ghostscript.Runner.Render): read receives the PNG and must consume it.
type EPSRenderer interface {
	Render(ctx context.Context, ps []byte, p *ghostscript.Page, read func(r io.Reader, limit int64) error) (ghostscript.Raster, error)
}

// loadEPS decodes an EPS/PostScript source. With a renderer (Config.EPS)
// Ghostscript draws the requested page at the plan's density and scale into
// a PNG in C memory, which libvips loads; the page box comes from the DSC
// header, the same one the probe measured. Without one, the embedded TIFF
// preview of a DOS EPS file is loaded (codec.EPSPreview refuses a file
// without one). Stacking several pages is not supported: the first page of
// the selection is rendered.
func (r *runner) loadEPS(ctx context.Context, ls *plan.LoadStep) (*vips.Image, error) {
	data := r.j.Source.Bytes()
	if r.e.cfg.EPS == nil {
		pre, err := codec.EPSPreview(data)
		if err != nil {
			return nil, err
		}
		defer pre.Release() // the image keeps its own reference
		return r.a.Add(vips.Load(ctx, pre, &vips.LoadOptions{Access: vips.AccessRandom, FailOn: r.e.cfg.FailOn}))
	}
	h, err := codec.ParseEPSHeader(data)
	if err != nil {
		return nil, err
	}
	if ls.NPages > 1 {
		r.skip("pages")
	}
	dpi := ls.Density
	if !(dpi > 0) {
		dpi = codec.EPSDensity
	}
	if ls.Scale > 0 {
		dpi *= ls.Scale
	}
	page := &ghostscript.Page{
		X: h.Box.LLX, Y: h.Box.LLY, Width: h.Box.Width(), Height: h.Box.Height(),
		Number: ls.Page + 1, DPI: dpi,
	}
	var buf *vips.Buffer
	_, err = r.e.cfg.EPS.Render(ctx, h.PostScript(data), page, func(rd io.Reader, limit int64) error {
		b, err := vips.NewBufferFromReader(rd, limit)
		buf = b
		return err
	})
	if buf != nil {
		defer buf.Release() // the image keeps its own reference
	}
	if err != nil {
		return nil, err
	}
	o := &vips.LoadOptions{Access: vips.AccessSequential, FailOn: r.e.cfg.FailOn}
	if ls.Access == plan.AccessRandom {
		o.Access = vips.AccessRandom
	}
	return r.a.Add(vips.Load(ctx, buf, o))
}
