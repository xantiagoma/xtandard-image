package codec

import (
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// VideoAvailable reports whether this build decodes video (the ffmpeg
// build tag: the full image).
func VideoAvailable() bool { return ffmpeg.Available() }

// videoDisabled is the error for a video source when video is off.
func videoDisabled(f Format) error {
	msg := "video sources are disabled"
	if !ffmpeg.Available() {
		msg = "video sources are not supported by this image"
	}
	return errs.WithPublic(errs.Newf(errs.KindUnsupported, "codec: %s: video decoding is off", f), msg)
}

// containerOf is the FFmpeg demuxer each sniffed video format must be read
// by; a disagreement means a polyglot or mislabelled file.
func containerOf(f Format) string {
	switch f {
	case FormatWebM, FormatMKV:
		return video.ContainerMatroska
	case FormatAVI:
		return video.ContainerAVI
	default:
		return video.ContainerMOV
	}
}

// probeVideo reads a video's headers with FFmpeg. The frame size is the
// display size (sample aspect ratio applied) in the stored orientation;
// the display matrix becomes the EXIF orientation, so the pipeline rotates
// the thumbnail like a photo.
func probeVideo(ctx context.Context, buf *vips.Buffer, f Format, s *video.Settings) (ImageMeta, error) {
	if !s.Enabled {
		return ImageMeta{}, videoDisabled(f)
	}
	vm, err := ffmpeg.Probe(ctx, buf, s.Limits)
	if err != nil {
		return ImageMeta{}, err
	}
	if vm.Container != containerOf(f) {
		return ImageMeta{}, errs.Newf(errs.KindUnsupported, "codec: sniffed %s but FFmpeg read %s", f, vm.Container)
	}
	cs := ColorSpaceSRGB
	return ImageMeta{
		Format:      f,
		Width:       vm.DisplayWidth,
		Height:      vm.DisplayHeight,
		Frames:      1,
		Pages:       1,
		PageHeight:  vm.DisplayHeight,
		HasAlpha:    vm.Alpha,
		Orientation: vm.Orientation(),
		BitDepth:    8,
		Loop:        -1,
		ColorSpace:  cs,
		Video:       &vm,
	}, nil
}

// VideoFrameInfo describes the frame VideoFrame decoded.
type VideoFrameInfo struct {
	// Time is its timestamp in seconds.
	Time float64
	// Keyframe reports a keyframe.
	Keyframe bool
	// BestFrame reports that best-frame selection chose it, among Sampled
	// keyframes, with Score.
	BestFrame bool
	Sampled   int
	Score     float64
}

// VideoFrame decodes the frame req selects from a video source (FFmpeg,
// internal/video/ffmpeg) and wraps it as a libvips image without copying:
// 8-bit sRGB, RGBA when the stream has alpha, req.Width × req.Height (the
// display size when 0), in the stored orientation (apply
// ImageMeta.Orientation to display it). Without the ffmpeg build tag it
// fails with errs.KindUnsupported. It must run inside vips.Run; the caller
// closes the image.
func VideoFrame(ctx context.Context, buf *vips.Buffer, req *video.FrameRequest) (*vips.Image, VideoFrameInfo, error) {
	fr, err := ffmpeg.Thumbnail(ctx, buf, req)
	if err != nil {
		return nil, VideoFrameInfo{}, err
	}
	defer fr.Release() // the image keeps its own reference to the pixels
	img, err := vips.NewFromBuffer(ctx, fr.Pixels, fr.Width, fr.Height, fr.Bands, vips.BandFormatUchar)
	if err != nil {
		return nil, VideoFrameInfo{}, err
	}
	return img, VideoFrameInfo{
		Time:      fr.Time,
		Keyframe:  fr.Keyframe,
		BestFrame: req.Second == nil,
		Sampled:   fr.Sampled,
		Score:     fr.Score,
	}, nil
}

// sampleVideo decodes the default frame (s's selection) at most maxSide
// pixels a side, upright, as RGBA.
func sampleVideo(ctx context.Context, buf *vips.Buffer, maxSide int, s *video.Settings) (Raw, error) {
	if !s.Enabled {
		return Raw{}, videoDisabled(FormatMP4)
	}
	vm, err := ffmpeg.Probe(ctx, buf, s.Limits)
	if err != nil {
		return Raw{}, err
	}
	w, h := fitInside(vm.DisplayWidth, vm.DisplayHeight, maxSide)
	req := s.Request(nil, nil, w, h)
	var a vips.Arena
	defer a.Close()
	img, _, err := VideoFrame(ctx, buf, &req)
	if err != nil {
		return Raw{}, fmt.Errorf("codec: sample video: %w", err)
	}
	a.Track(img)
	if o := vm.Orientation(); o != 1 {
		if img, err = a.Add(img.SetOrientation(ctx, o)); err != nil {
			return Raw{}, err
		}
		if img, err = a.Add(img.Autorot(ctx)); err != nil {
			return Raw{}, err
		}
	}
	if img.Bands() == 3 {
		if img, err = a.Add(img.BandjoinConst(ctx, []float64{255})); err != nil {
			return Raw{}, err
		}
	}
	pix, err := img.WriteToMemory(ctx)
	if err != nil {
		return Raw{}, fmt.Errorf("codec: sample video: %w", err)
	}
	return Raw{Width: img.Width(), Height: img.Height(), Bands: 4, Data: pix}, nil
}

// fitInside scales w×h down to fit inside side×side, keeping the aspect
// ratio (never up).
func fitInside(w, h, side int) (int, int) {
	if w <= side && h <= side {
		return w, h
	}
	if w >= h {
		return side, max(1, int(int64(h)*int64(side)/int64(w)))
	}
	return max(1, int(int64(w)*int64(side)/int64(h))), side
}
