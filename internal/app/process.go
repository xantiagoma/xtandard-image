package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/ghostscript"
	"github.com/xantiagoma/xtandard-image/internal/metafile"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// codecProber is the server.Prober: codec.Probe (magic-byte sniff, header
// only, BMP/ICO/PSD in Go, SVG and PDF limits) inside one vips.Run.
type codecProber struct {
	// svg holds the static per-format limits and settings (SVG, PDF, PSD);
	// the pixel and frame limits come per request.
	svg codec.Limits
	// video configures video sources (XI_VIDEO_*).
	video video.Settings
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
		// Startup failed already when it is on without Ghostscript (buildEPS).
		EPSEnabled: cfg.Formats.EPS.Enabled,
	}
}

// buildEPS starts EPS/PostScript rendering when XI_FORMATS_EPS_ENABLED is
// set: Ghostscript must be present and run through the sandbox (only the
// gpl image ships it), otherwise startup fails. It logs once which
// isolation layers hold (ADR 0011). nil when the feature is off.
func buildEPS(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*ghostscript.Runner, error) {
	e := &cfg.Formats.EPS
	if !e.Enabled {
		logger.LogAttrs(ctx, slog.LevelInfo, "eps rendering disabled")
		return nil, nil
	}
	r, err := ghostscript.New(ctx, &ghostscript.Config{
		Path:      e.GSPath,
		MaxProcs:  e.MaxProcs,
		Timeout:   e.Timeout,
		MaxDPI:    e.MaxDPI,
		MaxPixels: cfg.Limits.MaxSrcPixels,
	})
	if err != nil {
		return nil, fmt.Errorf("formats.eps.enabled (XI_FORMATS_EPS_ENABLED) is true but Ghostscript is not usable: %w; "+
			"EPS needs the gpl image (or XI_FORMATS_EPS_GS_PATH), or set it to false", err)
	}
	iso := r.Isolation()
	attrs := append([]slog.Attr{
		slog.String("ghostscript", r.Version()),
		slog.String("gs_path", r.Path()),
		slog.Int("max_procs", e.MaxProcs),
		slog.Duration("timeout", e.Timeout),
		slog.Float64("max_dpi", e.MaxDPI),
	}, iso.LogAttrs()...)
	logger.LogAttrs(ctx, slog.LevelInfo, "eps rendering enabled", attrs...)
	if !iso.NetNamespace {
		logger.LogAttrs(ctx, slog.LevelWarn, "eps rendering without a network namespace",
			slog.String("reason", iso.NetNamespaceError))
	}
	return r, nil
}

// buildMetafile starts WMF/EMF conversion (WP-108): with
// XI_FORMATS_METAFILE_ENABLED=auto when the converter is found (only the gpl
// image ships it), with true always, and then the converter must run
// through the sandbox or startup fails. It logs which isolation layers hold
// (ADR 0011). nil when conversion is off.
func buildMetafile(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*metafile.Converter, error) {
	m := &cfg.Formats.Metafile
	if m.Enabled == config.False {
		logger.LogAttrs(ctx, slog.LevelInfo, "metafile conversion disabled")
		return nil, nil
	}
	if m.Enabled == config.Auto && !onPath(m.Path) {
		logger.LogAttrs(ctx, slog.LevelInfo, "metafile conversion unavailable", slog.String("path", m.Path))
		return nil, nil
	}
	c, err := metafile.New(ctx, &metafile.Config{
		Path:     m.Path,
		MaxProcs: m.MaxProcs,
		Timeout:  m.Timeout,
		MaxBytes: int64(cfg.Limits.MaxSrcBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("formats.metafile.enabled (XI_FORMATS_METAFILE_ENABLED) is %s but the converter is not usable: %w; "+
			"WMF/EMF input needs the gpl image (or XI_FORMATS_METAFILE_PATH), or set it to false", m.Enabled, err)
	}
	iso := c.Isolation()
	attrs := append([]slog.Attr{
		slog.String("converter", c.Version()),
		slog.String("path", c.Path()),
		slog.Int("max_procs", m.MaxProcs),
		slog.Duration("timeout", m.Timeout),
	}, iso.LogAttrs()...)
	logger.LogAttrs(ctx, slog.LevelInfo, "metafile conversion enabled", attrs...)
	return c, nil
}

// onPath reports whether the program p resolves (an absolute path or a name
// in PATH).
func onPath(p string) bool {
	_, err := osexec.LookPath(p)
	return err == nil
}

// videoSettings maps XI_VIDEO_* onto the codec's and executor's video
// settings. auto enables video when this build has FFmpeg; startup already
// failed for true without it (checkVideo).
func videoSettings(cfg *config.Config) video.Settings {
	v := &cfg.Video
	s := video.Settings{
		Enabled: v.Enabled != config.False && codec.VideoAvailable(),
		Limits: video.Limits{
			MaxBytes:    int64(v.MaxBytes),
			MaxDuration: v.MaxDuration,
			MaxPixels:   v.MaxPixels,
		},
		Keyframes:          v.Thumbnail.Keyframes,
		Samples:            v.Thumbnail.Samples,
		Span:               v.Thumbnail.Span,
		MaxCompositeFrames: v.MaxAnimationFrames,
	}
	if sec, ok := v.Thumbnail.Second.Seconds(); ok {
		s.Second = &sec
	}
	return s
}

// videoEncodeConfig maps XI_VIDEO_ENCODE_* and the build's encoders onto
// the encoder's video output settings (WP-76).
func videoEncodeConfig(cfg *config.Config) encode.VideoConfig {
	e := &cfg.Video.Encode
	return encode.VideoConfig{
		Enabled:  videoSettings(cfg).Enabled,
		Encoders: codec.VideoEncoders(),
		Settings: video.EncodeSettings{
			CRF: map[video.Codec]int{
				video.CodecVP9: e.VP9CRF, video.CodecAV1: e.AV1CRF,
				video.CodecH264: e.H264CRF, video.CodecHEVC: e.HEVCCRF,
			},
			Speed:       map[video.Codec]int{video.CodecVP9: e.VP9Speed, video.CodecAV1: e.AV1Speed},
			Preset:      map[video.Codec]string{video.CodecH264: e.H264Preset, video.CodecHEVC: e.HEVCPreset},
			MaxFrames:   e.MaxFrames,
			MaxDuration: e.MaxDuration,
			MaxPixels:   e.MaxPixels,
		},
	}
}

// checkVideo fails startup when XI_VIDEO_ENABLED=true asks for video on an
// image without FFmpeg (slim).
func checkVideo(cfg *config.Config) error {
	if cfg.Video.Enabled == config.True && !codec.VideoAvailable() {
		return errors.New("video.enabled (XI_VIDEO_ENABLED) is true but this image has no FFmpeg; use the full image or set it to auto")
	}
	return nil
}

// Probe implements server.Prober.
func (p *codecProber) Probe(ctx context.Context, src fetch.Buffer, lim server.ProbeLimits) (server.SourceMeta, error) {
	vb, err := passwordBuffer(src, lim.Password)
	if err != nil {
		return server.SourceMeta{}, err
	}
	defer vb.Release()
	l := withProbeLimits(p.svg, &lim)
	var m codec.ImageMeta
	err = vips.Run(ctx, func() error {
		var err error
		// A video's FFmpeg handle stays open for the processor (the
		// server closes it): its frames are not probed twice.
		m, err = codec.ProbeSession(ctx, vb, l, &p.video, "")
		return err
	})
	if err != nil {
		return server.SourceMeta{}, err
	}
	f, _ := m.Format.Output()
	out := server.SourceMeta{
		Format:      f,
		Width:       m.Width,
		Height:      m.Height,
		Frames:      max(m.Frames, 1),
		Pages:       max(m.Pages, m.Frames, 1),
		HasAlpha:    m.HasAlpha,
		IsAnimated:  m.IsAnimated,
		Orientation: m.Orientation,
		HDR:         m.HDR,
		GainMap:     m.GainMap,
		Video:       m.Video,
		Raw:         m.Raw,
	}
	if m.Session != nil {
		out.Session = m.Session
	}
	return out, nil
}

// withProbeLimits sets the request's pixel and frame limits on the codec
// limits l. codec.Limits treats zero as its default, the server's zero is
// "unlimited" (a per-frame limit of zero is "off": animation frames count
// against the total, codec's negative); the server re-checks the result
// against lim anyway. A frame limit of 1 disables animation in both.
func withProbeLimits(l codec.Limits, lim *server.ProbeLimits) codec.Limits {
	l.MaxSrcPixels = unlimited(lim.MaxPixels)
	l.MaxAnimationFrames = int(unlimited(int64(lim.MaxFrames)))
	l.MaxAnimationFramePixels = -1
	if lim.MaxFramePixels > 0 {
		l.MaxAnimationFramePixels = lim.MaxFramePixels
	}
	return l
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
	// eps is set when Ghostscript renders EPS/PostScript sources
	// (XI_FORMATS_EPS_ENABLED): they are then vectors measured at 72 DPI;
	// otherwise their TIFF preview is a raster.
	eps bool
	// video selects the frame of a video source detection runs on.
	video video.Settings
	// detect runs face and object detection for the detection gravities
	// and filters (WP-72/73); nil is no detection (tests), like a detector
	// that is off or unavailable (slim).
	detect *detections
}

// newProcessor builds the executor and encoder; wm is the configured
// watermark (nil when none), owned by the caller. admit charges each plan's
// memory estimate (limiter.Limiter.Reserve; nil admits everything). eps
// renders EPS/PostScript sources (buildEPS; nil when XI_FORMATS_EPS_ENABLED
// is off).
func newProcessor(cfg *config.Config, wm *exec.Watermark, admit func(context.Context, int64) (func(), error), eps *ghostscript.Runner) (*processor, error) {
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
	var renderer exec.EPSRenderer // stays a nil interface without a runner
	if eps != nil {
		renderer = eps
	}
	ex, err := exec.New(&exec.Config{
		EPS:       renderer,
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
		Video:   videoSettings(cfg),
	}, enc)
	if err != nil {
		return nil, err
	}
	return &processor{exec: ex, enc: ec, pdfDPI: cfg.Formats.PDF.DPI, eps: eps != nil, video: videoSettings(cfg)}, nil
}

// planLimits are the planner limits of the executor, shared with the
// thumbor meta response (server.InfoConfig.PlanLimits).
func planLimits(cfg *config.Config) plan.Limits {
	return plan.Limits{
		MaxSrcPixels:            cfg.Limits.MaxSrcPixels,
		MaxResultDimension:      cfg.Limits.MaxResultDimension,
		MaxAnimationFrames:      cfg.Limits.MaxAnimationFrames,
		MaxAnimationFramePixels: cfg.Limits.MaxAnimationFramePixels,
		MaxAnimationPixels:      cfg.Limits.MaxAnimationPixels,
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
	ec.Video = videoEncodeConfig(cfg)
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
	src, err := passwordBuffer(in.Source, in.Password)
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
	meta := planMeta(format, &in.Meta, p.pdfDPI, p.eps)
	session, _ := in.Meta.Session.(*codec.VideoSession) // owned by the server, which closes it
	// Detections describe one upright picture of the source; a video
	// sprite or animation (vtt, vta) is assembled from many frames, so it
	// is not detected: the planner falls back to the smart crop and lists
	// the detection options as unplanned. A single-frame video is detected
	// on the frame the request selects, decoded once for detection and
	// processing (WP-91).
	var frame *codec.DecodedFrame
	defer func() { frame.Release() }()
	if !meta.VideoComposite {
		ds := imageSource(src, format)
		if meta.IsVideo {
			ds.frame = func(ctx context.Context) (*codec.DecodedFrame, int, error) {
				if frame == nil {
					f, err := p.decodeFrame(ctx, src, session, in.Request)
					if err != nil {
						return nil, 0, err
					}
					frame = f
				}
				return frame, meta.Orientation, nil
			}
		}
		if meta.Detections, err = p.detect.forRequest(ctx, ds, plan.NeedsDetection(in.Request)); err != nil {
			return nil, err
		}
	}
	job := &exec.Job{
		Request:    in.Request,
		Source:     src,
		Meta:       meta,
		Target:     t,
		Watermarks: marks,
		Video:      session,
	}
	if frame != nil {
		job.Frame = frame
		job.Meta.FrameDecoded = true
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
		VideoFrame:  res.VideoFrame.String(),
		VideoCodec:  res.VideoCodec,
	}, nil
}

// decodeFrame decodes the frame of a video source that req's first
// pipeline selects (video_thumbnail_second, _keyframes, else
// XI_VIDEO_THUMBNAIL_*), at the display size, on the probe's handle.
func (p *processor) decodeFrame(ctx context.Context, src *vips.Buffer, sess *codec.VideoSession, req *transform.Request) (*codec.DecodedFrame, error) {
	var second *float64
	var keyframes *bool
	if len(req.Pipelines) > 0 {
		if vo := req.Pipelines[0].Video; vo != nil {
			second, keyframes = vo.Second, vo.Keyframes
		}
	}
	fr := p.video.Request(second, keyframes, 0, 0)
	return codec.DecodeVideoFrame(ctx, src, sess, &fr)
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
func planMeta(f codec.Format, m *server.SourceMeta, pdfDPI float64, eps bool) plan.SourceMeta {
	name := f.String()
	if f == codec.FormatUHDR {
		name = "jpeg"
	}
	var density float64
	vector := f == codec.FormatSVG || f == codec.FormatPDF
	switch {
	case f == codec.FormatPDF:
		density = pdfDPI
	case f == codec.FormatEPS && eps:
		density, vector = codec.EPSDensity, true
	}
	// The planner's Frames counts pages too: it bounds `page`.
	pm := plan.SourceMeta{
		Width:       m.Width,
		Height:      m.Height,
		Frames:      max(m.Frames, m.Pages),
		HasAlpha:    m.HasAlpha,
		Orientation: m.Orientation,
		IsAnimated:  m.IsAnimated,
		Format:      name,
		IsVector:    vector,
		Density:     density,
		HDR:         f.HDR() || m.HDR,
		GainMap:     m.GainMap,
		IsVideo:     f.IsVideo(),
	}
	if m.Composite && m.Video != nil {
		// A sprite or animation of video frames (the server resized m to
		// it): upright, not scaled on load, decoded from the stream.
		pm.VideoComposite = true
		pm.StreamWidth, pm.StreamHeight = m.Video.DisplayWidth, m.Video.DisplayHeight
	}
	return pm
}
