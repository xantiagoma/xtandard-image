package exec

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// loadVideo decodes the frame of a video source that the request's
// video_thumbnail_second / video_thumbnail_keyframes (else
// XI_VIDEO_THUMBNAIL_*) select, scaled on load when the plan says so
// (LoadStep.Scale: twice the target, libvips does the rest). The frame is
// in the stored orientation; the plan applies the display matrix like EXIF
// orientation.
func (r *runner) loadVideo(ctx context.Context, ls *plan.LoadStep) (*vips.Image, error) {
	s := &r.e.cfg.Video
	if !s.Enabled {
		return nil, errs.WithPublic(errs.New(errs.KindUnsupported, "exec: video decoding is off"),
			"video sources are not supported by this image")
	}
	var w, h int
	if ls.Scale > 0 {
		w, h = ls.Width, ls.Height
	}
	var second *float64
	var keyframes *bool
	if len(r.req.Pipelines) > 0 {
		if vo := r.req.Pipelines[0].Video; vo != nil {
			second, keyframes = vo.Second, vo.Keyframes
		}
	}
	req := s.Request(second, keyframes, w, h)
	img, info, err := codec.VideoFrame(ctx, r.j.Source, &req)
	if err != nil {
		return nil, err
	}
	r.a.Track(img)
	r.videoFrame = &info
	return img, nil
}
