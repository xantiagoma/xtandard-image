package app

import (
	"context"
	"fmt"
	"os"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// codecProber is the server.Prober: codec.Probe (magic-byte sniff, header
// only, BMP/ICO/PSD in Go, SVG and PDF limits) inside one vips.Run.
type codecProber struct {
	// svg holds the static per-format limits and settings (SVG, PDF, PSD);
	// the pixel and frame limits come per request.
	svg codec.Limits
}

// formatLimits are the codec limits and probe settings that come from the
// configuration rather than the request.
func formatLimits(cfg *config.Config) codec.Limits {
	return codec.Limits{
		MaxSVGBytes:         int64(cfg.Limits.MaxSVGBytes),
		MaxSVGDepth:         cfg.Limits.MaxSVGDepth,
		MaxSVGElements:      cfg.Limits.MaxSVGElements,
		MaxPDFPages:         cfg.Limits.MaxPDFPages,
		PDFDensity:          cfg.Formats.PDF.DPI,
		PSDRequireComposite: cfg.Formats.PSD.RequireComposite,
	}
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
	if lim.MaxFrames == 1 {
		// max_animation_frames=1 disables animation (the planner processes
		// one frame, like imgproxy); it does not reject animations.
		l.MaxAnimationFrames = int(unlimited(0))
	}
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
		Pages:       max(m.Pages, m.Frames, 1),
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
	exec   *exec.Executor
	enc    encode.Config
	pdfDPI float64 // XI_FORMATS_PDF_DPI: the density PDF sizes were probed at
}

// newProcessor builds the executor and encoder; wm is the configured
// watermark (nil when none), owned by the caller. admit charges each plan's
// memory estimate (limiter.Limiter.Reserve; nil admits everything).
func newProcessor(cfg *config.Config, wm *exec.Watermark, admit func(context.Context, int64) (func(), error)) (*processor, error) {
	ec := encoderConfig(cfg)
	enc, err := encode.New(&ec)
	if err != nil {
		return nil, err
	}
	tm, err := exec.ParseToneMap(string(cfg.Formats.HDR.ToneMap))
	if err != nil {
		return nil, err
	}
	if f := cfg.Processing.WatermarkFontFile; f != "" {
		if st, err := os.Stat(f); err != nil || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("processing.watermark_font_file %q: not a readable file", f)
		}
	}
	opacity := cfg.Processing.WatermarkOpacity
	ex, err := exec.New(&exec.Config{
		Limits:    planLimits(cfg),
		FailOn:    vipsFailOn(cfg.Vips.FailOn),
		Watermark: wm,
		Watermarks: exec.WatermarkConfig{
			Opacity:       &opacity,
			Font:          cfg.Processing.WatermarkFont,
			FontFile:      cfg.Processing.WatermarkFontFile,
			MaxTextPixels: cfg.Limits.MaxWatermarkTextPixels,
		},
		ColorProfilesDir: cfg.Processing.ColorProfilesDir,
		SVG: svg.Options{
			MaxBytes:    int64(cfg.Limits.MaxSVGBytes),
			MaxDepth:    cfg.Limits.MaxSVGDepth,
			MaxElements: cfg.Limits.MaxSVGElements,
		},
		ToneMap: tm,
		Admit:   admit,
	}, enc)
	if err != nil {
		return nil, err
	}
	return &processor{exec: ex, enc: ec, pdfDPI: cfg.Formats.PDF.DPI}, nil
}

// planLimits are the planner limits of the executor, shared with the
// thumbor meta response (server.InfoConfig.PlanLimits).
func planLimits(cfg *config.Config) plan.Limits {
	return plan.Limits{
		MaxSrcPixels:            cfg.Limits.MaxSrcPixels,
		MaxResultDimension:      cfg.Limits.MaxResultDimension,
		MaxAnimationFrames:      cfg.Limits.MaxAnimationFrames,
		MaxAnimationFramePixels: cfg.Limits.MaxAnimationFramePixels,
		MemoryBudget:            int64(cfg.Processing.MemoryBudget),
		AllowRaisingLimits:      cfg.Security.AllowRaisingLimits,
		MaxRawDemosaicPixels:    cfg.Limits.MaxRawDemosaicPixels,
		RawPreviewMinRatio:      cfg.Formats.RAW.PreviewMinRatio,
	}
}

// vipsFailOn maps XI_VIPS_FAIL_ON onto the decoder strictness.
func vipsFailOn(f config.FailOn) vips.FailOn {
	return map[config.FailOn]vips.FailOn{
		config.FailOnNone: vips.FailOnNone, config.FailOnTruncated: vips.FailOnTruncated,
		config.FailOnError: vips.FailOnError, config.FailOnWarning: vips.FailOnWarning,
	}[f]
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
	ec.AutoQuality = autoQualityConfig(&f.AutoQuality)
	ec.Best = encode.BestConfig{MaxPixels: f.Best.MaxPixels, ComplexityThreshold: f.Best.ComplexityThreshold}
	return ec
}

// autoQualityConfig maps XI_FORMATS_AUTOQUALITY_* onto the encoder's.
func autoQualityConfig(aq *config.AutoQuality) encode.AutoQualityConfig {
	method := map[config.AutoQualityMethod]transform.AutoQualityMethod{
		config.AutoQualityNone: transform.AutoQualityNone, config.AutoQualitySize: transform.AutoQualitySize,
		config.AutoQualityDSSIM: transform.AutoQualityDSSIM,
	}[aq.Method]
	return encode.AutoQualityConfig{
		Method:       method,
		Target:       aq.Target,
		Min:          aq.Min,
		Max:          aq.Max,
		AllowedError: aq.AllowedError,
		FormatTarget: formatMap(aq.FormatTarget),
		FormatMin:    formatMap(aq.FormatMin),
		FormatMax:    formatMap(aq.FormatMax),
	}
}

// formatMap re-keys a per-format config map by transform.Format; config
// validation already rejected formats without a quality.
func formatMap[V any](in map[config.Format]V) map[transform.Format]V {
	if in == nil {
		return nil
	}
	out := make(map[transform.Format]V, len(in))
	for f, v := range in {
		if tf, err := transform.ParseFormat(string(f)); err == nil {
			out[tf] = v
		}
	}
	return out
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
	marks, err := watermarkImages(in.Watermarks)
	defer func() {
		for _, m := range marks {
			m.Source.Release()
		}
	}()
	if err != nil {
		return nil, err
	}
	job := &exec.Job{
		Request:    in.Request,
		Source:     src,
		Meta:       planMeta(format, &in.Meta, p.pdfDPI),
		Target:     t,
		Watermarks: marks,
	}
	if in.Decision.Reason == negotiate.ReasonBest {
		job.Best = &in.Decision
	}
	res, err := p.exec.Run(ctx, job)
	if err != nil {
		return nil, err
	}
	return &server.ProcessOutput{
		Body:        res.Body,
		ContentType: res.ContentType,
		Format:      res.Format,
		Width:       res.Width,
		Height:      res.Height,
		Quality:     res.Quality,
		Skipped:     res.Skipped,
		Best:        res.Best.String(),
		RawPath:     res.RawPath,
	}, nil
}

// watermarkImages hands the request's watermark images to the executor:
// C memory and the sniffed format. The caller releases every returned
// Source, also on error.
func watermarkImages(in []server.WatermarkInput) ([]exec.WatermarkImage, error) {
	out := make([]exec.WatermarkImage, 0, len(in))
	for _, w := range in {
		f, ok := codec.Sniff(w.Source.Bytes())
		if !ok {
			return out, errs.WithPublic(errs.New(errs.KindUnsupported, "app: unrecognised watermark format"),
				"unsupported watermark image format")
		}
		vb, err := vipsBuffer(w.Source)
		if err != nil {
			return out, err
		}
		name := f.String()
		if f == codec.FormatUHDR {
			name = "jpeg"
		}
		out = append(out, exec.WatermarkImage{Pipeline: w.Pipeline, Index: w.Index, Source: vb, Format: name})
	}
	return out, nil
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
func planMeta(f codec.Format, m *server.SourceMeta, pdfDPI float64) plan.SourceMeta {
	name := f.String()
	if f == codec.FormatUHDR {
		name = "jpeg"
	}
	var density float64
	if f == codec.FormatPDF {
		density = pdfDPI
	}
	// The planner's Frames counts pages too: it bounds `page`.
	return plan.SourceMeta{
		Width:       m.Width,
		Height:      m.Height,
		Frames:      max(m.Frames, m.Pages),
		HasAlpha:    m.HasAlpha,
		Orientation: m.Orientation,
		IsAnimated:  m.IsAnimated,
		Format:      name,
		IsVector:    f == codec.FormatSVG || f == codec.FormatPDF,
		Density:     density,
		HDR:         f.HDR(),
	}
}
