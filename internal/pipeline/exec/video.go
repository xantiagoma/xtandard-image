package exec

import (
	"context"
	"image"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// loadVideo decodes the frame of a video source that the request's
// video_thumbnail_second / video_thumbnail_keyframes (else
// XI_VIDEO_THUMBNAIL_*) select, scaled on load when the plan says so
// (LoadStep.Scale: twice the target, libvips does the rest). The frame is
// in the stored orientation; the plan applies the display matrix like EXIF
// orientation. A video_thumbnail_tile or _animation request assembles the
// sprite or animation instead (loadComposite). The probe's FFmpeg handle
// (Job.Video) is reused when there is one, and a frame the caller already
// decoded (Job.Frame, to detect on it) is used as it is.
func (r *runner) loadVideo(ctx context.Context, ls *plan.LoadStep, anim *plan.AnimationStep) (*vips.Image, error) {
	s := &r.e.cfg.Video
	if !s.Enabled {
		return nil, errs.WithPublic(errs.New(errs.KindUnsupported, "exec: video decoding is off"),
			"video sources are not supported by this image")
	}
	vo := firstVideo(r.req)
	if o, ok := video.Composite(vo); ok {
		return r.loadComposite(ctx, &o, ls, anim)
	}
	if f := r.j.Frame; f != nil {
		img, err := r.a.Add(f.Image(ctx))
		if err != nil {
			return nil, err
		}
		info := f.Info
		r.videoFrame = &info
		return img, nil
	}
	var w, h int
	if ls.Scale > 0 {
		w, h = ls.Width, ls.Height
	}
	var second *float64
	var keyframes *bool
	if vo != nil {
		second, keyframes = vo.Second, vo.Keyframes
	}
	req := s.Request(second, keyframes, w, h)
	img, info, err := codec.VideoFrame(ctx, r.j.Source, r.j.Video, &req)
	if err != nil {
		return nil, err
	}
	r.a.Track(img)
	r.videoFrame = &info
	return img, nil
}

// firstVideo is the video options of the first pipeline, which decide how
// the source is decoded.
func firstVideo(req *transform.Request) *transform.VideoOptions {
	if len(req.Pipelines) == 0 {
		return nil
	}
	return req.Pipelines[0].Video
}

// loadComposite assembles the sprite or animation of o
// (codec.VideoComposite). An animation the plan does not keep as one (a
// still output format, disable_animation) is reduced to the frames the
// load step selects, stacked like the pages of a still; one the plan
// keeps is cut to the frames it processes.
func (r *runner) loadComposite(ctx context.Context, o *video.CompositeOptions, ls *plan.LoadStep, anim *plan.AnimationStep) (*vips.Image, error) {
	s := &r.e.cfg.Video
	keyframes := s.Keyframes
	if vo := firstVideo(r.req); vo != nil && vo.Keyframes != nil {
		keyframes = *vo.Keyframes
	}
	img, info, err := codec.VideoComposite(ctx, r.a, r.j.Source, r.j.Video, &codec.CompositeRequest{
		Options: *o, MaxFrames: s.MaxCompositeFrames, Keyframes: keyframes, Limits: s.Limits,
	})
	if err != nil {
		return nil, err
	}
	r.videoFrame = &codec.VideoFrameInfo{Composite: &info}
	l := &info.Layout
	if l.Tile || l.Frames <= 1 {
		if l.Frames == 1 && !l.Tile {
			return r.a.Add(img.RemoveMetadata(ctx, vips.FieldPageHeight, vips.FieldNPages, vips.FieldDelay))
		}
		return img, nil
	}
	first, n := ls.Page, max(ls.NPages, 1)
	if anim != nil {
		n = anim.Frames
	}
	first = min(max(first, 0), l.Frames-1)
	n = min(n, l.Frames-first)
	if first == 0 && n == l.Frames {
		return img, nil
	}
	ph := l.CellHeight
	if img, err = r.a.Add(img.ExtractArea(ctx, image.Rect(0, first*ph, img.Width(), (first+n)*ph))); err != nil {
		return nil, err
	}
	if anim == nil {
		// Stacked like the pages of a still: one image.
		return r.a.Add(img.RemoveMetadata(ctx, vips.FieldPageHeight, vips.FieldNPages, vips.FieldDelay))
	}
	if img, err = r.a.Add(img.SetInt(ctx, vips.FieldNPages, n)); err != nil {
		return nil, err
	}
	return r.a.Add(img.SetInts(ctx, vips.FieldDelay, slices.Repeat([]int{l.Delay}, n)))
}
