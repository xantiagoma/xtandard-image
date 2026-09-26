package app

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// codecProber is the server.Prober: codec.Probe (magic-byte sniff, header
// only, BMP/ICO through the Go loaders, SVG limits) inside one vips.Run.
type codecProber struct {
	svg codec.Limits // SVG limits; the pixel and frame limits come per request
}

// Probe implements server.Prober.
func (p *codecProber) Probe(ctx context.Context, src fetch.Buffer, lim server.ProbeLimits) (server.SourceMeta, error) {
	vb, err := vipsBuffer(src)
	if err != nil {
		return server.SourceMeta{}, err
	}
	defer vb.Release()
	l := p.svg
	// codec.Limits treats zero as its default; the server's zero is
	// "unlimited" (the server re-checks the result against lim anyway).
	l.MaxSrcPixels = unlimited(lim.MaxPixels)
	l.MaxAnimationFrames = int(unlimited(int64(lim.MaxFrames)))
	l.MaxAnimationFramePixels = unlimited(lim.MaxFramePixels)
	var m codec.ImageMeta
	err = vips.Run(ctx, func() error {
		var err error
		m, err = codec.Probe(ctx, vb, l, "")
		return err
	})
	if err != nil {
		return server.SourceMeta{}, err
	}
	f, _ := m.Format.Output()
	return server.SourceMeta{
		Format:      f,
		Width:       m.Width,
		Height:      m.Height,
		Frames:      max(m.Frames, 1),
		HasAlpha:    m.HasAlpha,
		IsAnimated:  m.IsAnimated,
		Orientation: m.Orientation,
	}, nil
}

func unlimited(v int64) int64 {
	if v <= 0 {
		return 1 << 50
	}
	return v
}

// processor is the server.Processor: pipeline/exec (plan, execute) and
// codec/encode (encode) behind the server's interface.
type processor struct {
	exec *exec.Executor
	enc  encode.Config
}

func newProcessor(cfg *config.Config) (*processor, error) {
	ec := encoderConfig(cfg)
	enc, err := encode.New(&ec)
	if err != nil {
		return nil, err
	}
	failOn := map[config.FailOn]vips.FailOn{
		config.FailOnNone: vips.FailOnNone, config.FailOnTruncated: vips.FailOnTruncated,
		config.FailOnError: vips.FailOnError, config.FailOnWarning: vips.FailOnWarning,
	}[cfg.Vips.FailOn]
	ex, err := exec.New(&exec.Config{
		Limits: plan.Limits{
			MaxSrcPixels:            cfg.Limits.MaxSrcPixels,
			MaxResultDimension:      cfg.Limits.MaxResultDimension,
			MaxAnimationFrames:      cfg.Limits.MaxAnimationFrames,
			MaxAnimationFramePixels: cfg.Limits.MaxAnimationFramePixels,
			MemoryBudget:            int64(cfg.Processing.MemoryBudget),
			AllowRaisingLimits:      cfg.Security.AllowRaisingLimits,
		},
		FailOn: failOn,
	}, enc)
	if err != nil {
		return nil, err
	}
	return &processor{exec: ex, enc: ec}, nil
}

// encoderConfig maps config.Formats onto the encoder's options.
func encoderConfig(cfg *config.Config) encode.Config {
	f := &cfg.Formats
	ec := encode.DefaultConfig()
	ec.Quality = f.Quality
	ec.FormatQuality = map[transform.Format]int{
		transform.FormatJPEG: f.JPEG.Quality,
		transform.FormatWebP: f.WebP.Quality,
		transform.FormatAVIF: f.AVIF.Quality,
		transform.FormatHEIC: f.HEIC.Quality,
		transform.FormatJXL:  f.JXL.Quality,
	}
	ec.JPEGProgressive = f.JPEG.Progressive
	ec.PNGQuantize = f.PNG.Quantize
	ec.WebPEffort = f.WebP.Effort
	ec.AVIFEffort = f.AVIF.Effort
	ec.JXLEffort = f.JXL.Effort
	ec.GIFEffort = f.GIF.Effort
	ec.HEICEncode = f.HEIC.Encode
	return ec
}

// Process implements server.Processor.
func (p *processor) Process(ctx context.Context, in *server.ProcessInput) (*server.ProcessOutput, error) {
	src, err := vipsBuffer(in.Source)
	if err != nil {
		return nil, err
	}
	defer src.Release()

	format, ok := codec.Sniff(src.Bytes())
	if !ok {
		return nil, errs.New(errs.KindUnsupported, "app: unrecognised source format")
	}
	t := encode.NewTarget(&in.Request.Output, &in.Decision, in.Meta.IsAnimated)
	if in.QualityDelta != 0 && lossy(t.Format) {
		q := p.enc.ResolveQuality(t.Format, t.Quality, t.FormatQuality)
		t.Quality = min(max(q+in.QualityDelta, 1), 100)
	}
	res, err := p.exec.Run(ctx, &exec.Job{
		Request: in.Request,
		Source:  src,
		Meta:    planMeta(format, &in.Meta),
		Target:  t,
	})
	if err != nil {
		return nil, err
	}
	return &server.ProcessOutput{
		Body:        res.Body,
		ContentType: res.ContentType,
		Format:      res.Format,
		Width:       res.Width,
		Height:      res.Height,
		Skipped:     res.Skipped,
	}, nil
}

func lossy(f transform.Format) bool {
	switch f {
	case transform.FormatJPEG, transform.FormatWebP, transform.FormatAVIF, transform.FormatHEIC, transform.FormatJXL:
		return true
	default:
		return false
	}
}

// planMeta is the planner's view of the probed source. The format name is
// the sniffed codec format (UltraHDR shrinks on load like JPEG).
func planMeta(f codec.Format, m *server.SourceMeta) plan.SourceMeta {
	name := f.String()
	if f == codec.FormatUHDR {
		name = "jpeg"
	}
	return plan.SourceMeta{
		Width:       m.Width,
		Height:      m.Height,
		Frames:      m.Frames,
		HasAlpha:    m.HasAlpha,
		Orientation: m.Orientation,
		IsAnimated:  m.IsAnimated,
		Format:      name,
		IsVector:    f == codec.FormatSVG || f == codec.FormatPDF,
	}
}
