package codec

import (
	"context"
	"fmt"
	"strconv"

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
func probeVideo(ctx context.Context, buf *vips.Buffer, f Format, s *video.Settings, keep bool) (ImageMeta, error) {
	if s == nil || !s.Enabled {
		return ImageMeta{}, videoDisabled(f)
	}
	var vm video.Meta
	var sess *VideoSession
	if keep {
		fs, err := ffmpeg.Open(ctx, buf, s.Limits)
		if err != nil {
			return ImageMeta{}, err
		}
		sess, vm = &VideoSession{s: fs}, fs.Meta()
	} else {
		var err error
		if vm, err = ffmpeg.Probe(ctx, buf, s.Limits); err != nil {
			return ImageMeta{}, err
		}
	}
	if vm.Container != containerOf(f) {
		sess.Close()
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
		Session:     sess,
	}, nil
}

// VideoSession is a video's FFmpeg handle kept open from the probe
// (ProbeSession) to the decode. It is not safe for concurrent use.
type VideoSession struct {
	s *ffmpeg.Session
}

// Close frees the handle. It is nil-safe and idempotent.
func (v *VideoSession) Close() {
	if v != nil {
		v.s.Close()
	}
}

// session returns the open handle when it reads buf's memory, else nil
// (the caller opens buf again).
func (v *VideoSession) session(buf *vips.Buffer) *ffmpeg.Session {
	if v == nil || !v.s.Reads(buf) {
		return nil
	}
	return v.s
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
	// Composite describes a sprite or animation made of several frames
	// (VideoComposite); the fields above are then zero.
	Composite *CompositeInfo
}

// String renders f for the X-Xi-Video-Frame debug header: the timestamp,
// "key" for a keyframe and, after best-frame selection, "best" with the
// number of keyframes compared and the winning score ("0.960s key best 4
// 5.21"). Empty for a nil f.
func (f *VideoFrameInfo) String() string {
	if f == nil {
		return ""
	}
	if c := f.Composite; c != nil {
		return c.String()
	}
	b := strconv.AppendFloat(nil, f.Time, 'f', 3, 64)
	b = append(b, 's')
	if f.Keyframe {
		b = append(b, " key"...)
	}
	if f.BestFrame {
		b = append(b, " best "...)
		b = strconv.AppendInt(b, int64(f.Sampled), 10)
		b = append(b, ' ')
		b = strconv.AppendFloat(b, f.Score, 'f', 2, 64)
	}
	return string(b)
}

// VideoVersion is the FFmpeg version of this build, "" without video.
func VideoVersion() string { return ffmpeg.Version() }

// DecodedFrame is a decoded video frame that is not a libvips image yet:
// packed 8-bit sRGB (Bands 3) or RGBA (Bands 4) pixels in C memory, in the
// stored orientation. It outlives vips.Run, so one decode serves several
// runs (detection, then processing: WP-91). The caller releases it.
type DecodedFrame struct {
	pixels        *vips.Buffer
	Width, Height int
	Bands         int
	// Info describes the frame.
	Info VideoFrameInfo
}

// Release frees the pixels once every image made from them is closed. It is
// nil-safe and idempotent.
func (f *DecodedFrame) Release() {
	if f == nil || f.pixels == nil {
		return
	}
	f.pixels.Release()
	f.pixels = nil
}

// Image wraps the pixels as a libvips image without copying (the image
// keeps its own reference to them). It must run inside vips.Run; the
// caller closes the image.
func (f *DecodedFrame) Image(ctx context.Context) (*vips.Image, error) {
	if f == nil || f.pixels == nil {
		return nil, errs.New(errs.KindInternal, "codec: released video frame")
	}
	return vips.NewFromBuffer(ctx, f.pixels, f.Width, f.Height, f.Bands, vips.BandFormatUchar)
}

// DecodeVideoFrame decodes the frame req selects from a video source
// (FFmpeg, internal/video/ffmpeg): req.Width × req.Height (the display size
// when 0), in the stored orientation (apply ImageMeta.Orientation to
// display it). Without the ffmpeg build tag it fails with
// errs.KindUnsupported. sess, when it reads buf (ProbeSession), decodes on
// the probe's handle instead of opening buf again.
func DecodeVideoFrame(ctx context.Context, buf *vips.Buffer, sess *VideoSession, req *video.FrameRequest) (*DecodedFrame, error) {
	var fr *ffmpeg.Frame
	var err error
	if s := sess.session(buf); s != nil {
		fr, err = s.Thumbnail(ctx, req)
	} else {
		fr, err = ffmpeg.Thumbnail(ctx, buf, req)
	}
	if err != nil {
		return nil, err
	}
	return &DecodedFrame{
		pixels: fr.Pixels, Width: fr.Width, Height: fr.Height, Bands: fr.Bands,
		Info: VideoFrameInfo{
			Time:      fr.Time,
			Keyframe:  fr.Keyframe,
			BestFrame: req.Second == nil,
			Sampled:   fr.Sampled,
			Score:     fr.Score,
		},
	}, nil
}

// VideoFrame is DecodeVideoFrame wrapped as a libvips image. It must run
// inside vips.Run; the caller closes the image.
func VideoFrame(ctx context.Context, buf *vips.Buffer, sess *VideoSession, req *video.FrameRequest) (*vips.Image, VideoFrameInfo, error) {
	f, err := DecodeVideoFrame(ctx, buf, sess, req)
	if err != nil {
		return nil, VideoFrameInfo{}, err
	}
	defer f.Release() // the image keeps its own reference to the pixels
	img, err := f.Image(ctx)
	if err != nil {
		return nil, VideoFrameInfo{}, err
	}
	return img, f.Info, nil
}

// SampleVideoFrame decodes the frame s's default selection picks
// (XI_VIDEO_THUMBNAIL_*) fitted inside maxSide × maxSide, in the stored
// orientation, and returns it with the orientation that displays it. It
// opens buf on its own handle.
func SampleVideoFrame(ctx context.Context, buf *vips.Buffer, maxSide int, s *video.Settings) (*DecodedFrame, int, error) {
	if s == nil || !s.Enabled {
		return nil, 0, videoDisabled(FormatMP4)
	}
	fs, err := ffmpeg.Open(ctx, buf, s.Limits)
	if err != nil {
		return nil, 0, err
	}
	sess := &VideoSession{s: fs}
	defer sess.Close()
	vm := fs.Meta()
	w, h := fitInside(vm.DisplayWidth, vm.DisplayHeight, maxSide)
	req := s.Request(nil, nil, w, h)
	f, err := DecodeVideoFrame(ctx, buf, sess, &req)
	if err != nil {
		return nil, 0, err
	}
	return f, vm.Orientation(), nil
}

// UprightImage wraps f as a libvips image tracked by a and applies
// orientation (EXIF 1..8, the display matrix) so it shows upright. It must
// run inside vips.Run.
func (f *DecodedFrame) UprightImage(ctx context.Context, a *vips.Arena, orientation int) (*vips.Image, error) {
	img, err := a.Add(f.Image(ctx))
	if err != nil {
		return nil, err
	}
	if orientation > 1 && orientation <= 8 {
		if img, err = a.Add(img.SetOrientation(ctx, orientation)); err != nil {
			return nil, err
		}
		if img, err = a.Add(img.Autorot(ctx)); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// sampleVideo decodes the default frame (s's selection) at most maxSide
// pixels a side, upright, as RGBA.
func sampleVideo(ctx context.Context, buf *vips.Buffer, maxSide int, s *video.Settings) (Raw, error) {
	f, orientation, err := SampleVideoFrame(ctx, buf, maxSide, s)
	if err != nil {
		return Raw{}, fmt.Errorf("codec: sample video: %w", err)
	}
	defer f.Release()
	var a vips.Arena
	defer a.Close()
	img, err := f.UprightImage(ctx, &a, orientation)
	if err != nil {
		return Raw{}, err
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
