package encode

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// videoOutput maps a video format onto the output contract.
func videoOutput(f transform.Format) video.Output {
	switch f {
	case transform.FormatWebM:
		return video.OutputWebM
	case transform.FormatHEVC:
		return video.OutputHEVC
	default:
		return video.OutputMP4
	}
}

// encodeVideo encodes img (every frame when t.Animated, else the first) as
// MP4, WebM or HEVC with FFmpeg (codec.EncodeVideo). The quality is the
// request's (quality, then format_quality) mapped onto the codec's CRF;
// without one the configured CRF applies. max_bytes and auto-quality do
// not search video: they are reported as ignored.
func (e *Encoder) encodeVideo(ctx context.Context, img *vips.Image, t *Target) (*Result, error) {
	if !e.cfg.Video.Enabled {
		return nil, errs.WithPublic(
			errs.Newf(errs.KindUnsupported, "encode: %s output needs video support (XI_VIDEO_ENABLED)", t.Format),
			"video output is not supported by this image")
	}
	var a vips.Arena
	defer a.Close()
	img, err := e.frames(ctx, &a, img, t)
	if err != nil {
		return nil, err
	}
	q := t.Quality
	if q <= 0 {
		q = t.FormatQuality[t.Format]
	}
	body, info, err := codec.EncodeVideo(ctx, img, &codec.VideoEncodeRequest{
		Output:   videoOutput(t.Format),
		Quality:  q,
		Encoders: e.cfg.Video.Encoders,
		Settings: e.cfg.Video.Settings,
	})
	if err != nil {
		return nil, err
	}
	res := &Result{
		Blob: body, Format: t.Format, ContentType: negotiate.ContentType(t.Format),
		Quality: max(q, 0), Attempts: 1, Video: &info,
		OverBudget: t.MaxBytes > 0 && body.Len() > t.MaxBytes,
	}
	switch {
	case t.MaxBytes > 0:
		res.AutoQuality.Ignored = "max_bytes"
	case t.AutoQuality != nil && t.AutoQuality.Method != transform.AutoQualityUnset &&
		t.AutoQuality.Method != transform.AutoQualityNone:
		res.AutoQuality.Ignored = "autoquality"
	}
	return res, nil
}
