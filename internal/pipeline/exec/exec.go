package exec

import (
	"context"
	"errors"
	"image"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Config configures an Executor. internal/app maps config.Config onto it.
type Config struct {
	// Limits are the processing limits the planner enforces (XI_LIMITS_*,
	// XI_PROCESSING_MEMORY_BUDGET).
	Limits plan.Limits
	// FailOn is the decoder strictness for untrusted sources (XI_VIPS_FAIL_ON;
	// the zero value is fail_on=warning).
	FailOn vips.FailOn
	// Watermark is the configured watermark image (NewWatermark); nil when
	// XI_PROCESSING_WATERMARK_SOURCE is unset, and watermarks without a
	// source or text of their own are then skipped. The Executor does not
	// close it.
	Watermark *Watermark
	// Watermarks configures per-request and text watermarks.
	Watermarks WatermarkConfig
	// ColorProfilesDir holds the ICC files color_profile may name
	// (XI_PROCESSING_COLOR_PROFILES_DIR); empty allows only the built-in
	// srgb and cmyk.
	ColorProfilesDir string
	// SVG is the sanitiser policy (XI_LIMITS_MAX_SVG_*) the document is
	// sanitised with again after `style` injected its CSS.
	SVG svg.Options
	// ToneMap maps OpenEXR, Radiance and PFM sources to 8-bit sRGB
	// (XI_FORMATS_HDR_TONEMAP; the zero value is Reinhard).
	ToneMap ToneMap
}

// Executor plans, executes and encodes images. It holds no per-request state
// and is safe for concurrent use; each Run owns one vips.Run.
type Executor struct {
	cfg Config
	enc *encode.Encoder
}

// New returns an Executor that encodes with enc.
func New(cfg *Config, enc *encode.Encoder) (*Executor, error) {
	if enc == nil {
		return nil, errors.New("exec: nil encoder")
	}
	return &Executor{cfg: *cfg, enc: enc}, nil
}

// Job is one image to process. Every field is read-only for the executor.
type Job struct {
	// Request has passed transform.Request.ApplyDefaults and Validate.
	Request *transform.Request
	// Source holds the original bytes; the caller keeps its reference and
	// may release it once Run returns.
	Source *vips.Buffer
	// Meta is the probed header. Meta.Format is the sniffed codec format
	// name ("jpeg", "png", "bmp", "ico", "svg", ...): BMP and ICO are
	// decoded in Go.
	Meta plan.SourceMeta
	// Target is the encoding (encode.NewTarget). Target.Animated is decided
	// by the plan: an animation stays one only when the plan processed it
	// frame by frame.
	Target encode.Target
	// Watermarks are the per-request watermark images of the request
	// (Watermark.Source), fetched by the caller.
	Watermarks []WatermarkImage
	// Best is the negotiated decision of a format=best request
	// (negotiate.ReasonBest): the processed image is encoded with
	// encode.Encoder.EncodeBest, which picks among its candidates. Nil
	// encodes Target.Format.
	Best *negotiate.Decision
}

// Result is an encoded image. The caller writes Body and releases it.
type Result struct {
	Body        encode.Body
	Format      transform.Format
	ContentType string
	// Width and Height are the size of one frame of the result.
	Width, Height int
	// Frames is the number of frames encoded (1 unless animated).
	Frames int
	// DPR is the effective device-pixel ratio of the result (Content-DPR).
	DPR float64
	// Quality is the encoder quality used (0 for formats without one); with
	// auto-quality, the quality the search chose.
	Quality int
	// Saves counts the encoder saves (1 unless max_bytes or auto-quality
	// searched).
	Saves int
	// RawPath is how a camera RAW source was decoded: RawPathPreview
	// (its embedded JPEG), RawPathDemosaic or RawPathDemosaicHalf; empty
	// for other formats. It is exposed as a debug header.
	RawPath string
	// RawDecoded is the size of the camera RAW source as decoded (the
	// embedded preview after shrink-on-load, or the demosaic at full or half
	// size): the pixels the chosen path had to produce. Zero for other
	// formats.
	RawDecoded image.Point
	// Skipped lists the request options that were not applied because a
	// later work package implements them (filters, watermarks, the plan's
	// Unplanned options), for debugging.
	Skipped []string
	// Best reports the best-format selection (Job.Best); nil otherwise.
	Best *encode.BestResult
	// Thumbnail reports that the image was decoded from the thumbnail
	// embedded in the source (HEIF/AVIF, or a JPEG's Exif thumbnail with
	// enforce_thumbnail) rather than from the full image.
	Thumbnail bool
}

// Run plans j.Request for j.Meta, executes the plan on j.Source and encodes
// the result.
//
// Errors carry an errs kind: KindLimitExceeded (source, frames, result
// dimensions or memory budget over the limits), KindCorrupt (the decoder
// failed), KindUnsupported (no loader or encoder), KindCanceled/KindTimeout
// (ctx ended) and KindInternal.
func (e *Executor) Run(ctx context.Context, j *Job) (*Result, error) {
	if j == nil || j.Request == nil || j.Source == nil {
		return nil, errs.New(errs.KindInternal, "exec: incomplete job")
	}
	// The planner decides alpha, flattening and animation from the output
	// format: plan for the negotiated one, not for the source's.
	req := j.Request
	if j.Target.Format != transform.FormatUnset && req.Output.Format != j.Target.Format {
		cp := *req
		cp.Output.Format = j.Target.Format
		req = &cp
	}
	meta := j.Meta
	transparent := pdfTransparent(j, req)
	if transparent {
		meta.HasAlpha = true
	}
	var skipped []string
	if css := firstStyle(req); css != "" {
		styled, err := e.styledSource(j, css)
		if err != nil {
			return nil, err
		}
		if styled == nil {
			skipped = append(skipped, skippedStyle) // not an SVG source
		} else {
			defer styled.Release()
			sj := *j
			sj.Source = styled
			j = &sj
		}
	}
	// A camera RAW is planned inside the Run: the preview size comes from
	// its LibRaw header (planRaw), which also picks its embedded preview.
	isRaw := j.Meta.Format == formatRAW
	var p *plan.Plan
	var err error
	if !isRaw {
		if p, err = plan.Build(req, meta, e.cfg.Limits); err != nil {
			return nil, err
		}
	}
	fromThumb := false
	if !isRaw {
		if buf, tm, tp, ok := e.exifThumbnail(j, req, &meta, p); ok {
			defer buf.Release()
			tj := *j
			tj.Source, tj.Meta = buf, tm
			j, meta, p, fromThumb = &tj, tm, tp, true
		}
	}
	var res *Result
	err = vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		r := &runner{e: e, j: j, req: req, a: &a, plan: p, meta: meta, transparent: transparent, skipped: skipped}
		if isRaw {
			if err := r.planRaw(ctx); err != nil {
				return err
			}
		}
		img, err := r.execute(ctx)
		if err != nil {
			return err
		}
		if err := ctxErr(ctx); err != nil {
			return err
		}
		t := j.Target
		t.Animated = img.Pages() > 1
		if r.profileEmbedded {
			t.Metadata.StripColorProfile = false // color_profile embeds it
		}
		var out *encode.Result
		if j.Best != nil && j.Best.Reason == negotiate.ReasonBest {
			out, err = e.enc.EncodeBest(ctx, img, &t, j.Best)
		} else {
			out, err = e.enc.Encode(ctx, img, &t)
		}
		if err != nil {
			return err
		}
		if out.AutoQuality.Ignored != "" {
			r.skip(out.AutoQuality.Ignored)
		}
		if out.Best != nil && out.Best.Skipped != "" {
			r.skip(out.Best.Skipped)
		}
		last := r.last()
		res = &Result{
			Body:        out.Blob,
			Format:      out.Format,
			ContentType: out.ContentType,
			Width:       img.Width(),
			Height:      img.PageHeight(),
			Frames:      1,
			DPR:         last.Output.DPRScale,
			Quality:     out.Quality,
			Saves:       out.Attempts,
			Skipped:     r.skipped,
			Best:        out.Best,
			RawPath:     r.rawPath,
			RawDecoded:  r.rawDecoded,
			Thumbnail:   fromThumb || r.plan.Load.Thumbnail,
		}
		if t.Animated && animatable(out.Format) {
			res.Frames = img.Pages()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func animatable(f transform.Format) bool {
	return f == transform.FormatGIF || f == transform.FormatWebP || f == transform.FormatJXL
}

// ctxErr reports a done ctx with the matching errs kind.
func ctxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errs.Wrap(errs.KindOf(err), context.Cause(ctx))
	}
	return nil
}
