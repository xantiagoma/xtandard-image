package codec

import (
	"context"
	"image"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// VideoEncoders probes the video encoders of this build (FFmpeg's
// avcodec_find_encoder_by_name): none without the ffmpeg build tag, VP9 and
// AV1 in the full image, plus H.264 and HEVC in the gpl image.
func VideoEncoders() video.EncoderSet { return ffmpeg.Encoders() }

// VideoEncodeRequest is one video encode (WP-76, FORMATS §5.1).
type VideoEncodeRequest struct {
	Output video.Output
	// Quality is the request's quality (1–100) mapped onto the codec's CRF
	// (video.CRF); 0 takes the configured CRF.
	Quality int
	// Encoders are the encoders of this build (VideoEncoders at startup).
	Encoders video.EncoderSet
	Settings video.EncodeSettings
}

// VideoEncodeInfo describes an encoded video.
type VideoEncodeInfo struct {
	Codec     video.Codec
	Container video.Container
	// Width and Height are the encoded frame size (even: an odd side is
	// padded by repeating its last row or column).
	Width, Height int
	Frames        int
	// Duration is the sum of the frame delays.
	Duration time.Duration
	CRF      int
}

// EncodeVideo encodes img, a still or an animation strip (page-height,
// delay), as req.Output with the codec the build's encoders give it
// (video.EncoderSet.Resolve): frame timing from the per-frame delays
// (video.FrameDelay), the loop count ignored. Alpha is kept for VP9 and
// flattened on white otherwise. The frames are rendered one at a time
// into C memory and handed to FFmpeg; the muxed file comes back in a new
// buffer the caller releases. errs.KindUnsupported when this build cannot
// write the output, errs.KindLimitExceeded over the configured frame,
// duration or pixel limits. It must run inside vips.Run.
func EncodeVideo(ctx context.Context, img *vips.Image, req *VideoEncodeRequest) (*vips.Buffer, VideoEncodeInfo, error) {
	codec, container, ok := req.Encoders.Resolve(req.Output)
	if !ok {
		return nil, VideoEncodeInfo{}, errs.WithPublic(
			errs.Newf(errs.KindUnsupported, "codec: no encoder for video output %d in this build", req.Output),
			"this video output format is not supported by this image")
	}
	n, w, ph := img.Pages(), img.Width(), img.PageHeight()
	delays, _ := img.Ints(vips.FieldDelay)
	info := VideoEncodeInfo{Codec: codec, Container: container, Width: w + w%2, Height: ph + ph%2, Frames: n}
	durs := make([]int64, n)
	var total int64
	for i := range durs {
		d := 0
		if i < len(delays) {
			d = delays[i]
		}
		durs[i] = int64(video.FrameDelay(d))
		total += durs[i]
	}
	info.Duration = time.Duration(total) * time.Millisecond
	s := &req.Settings
	switch {
	case s.MaxFrames > 0 && n > s.MaxFrames:
		return nil, info, errs.Newf(errs.KindLimitExceeded, "codec: %d frames exceed the video output limit of %d", n, s.MaxFrames)
	case s.MaxDuration > 0 && info.Duration > s.MaxDuration:
		return nil, info, errs.Newf(errs.KindLimitExceeded, "codec: %s exceeds the video output duration limit of %s", info.Duration, s.MaxDuration)
	case s.MaxPixels > 0 && int64(info.Width)*int64(info.Height) > s.MaxPixels:
		return nil, info, errs.Newf(errs.KindLimitExceeded, "codec: %dx%d frames exceed the video output limit of %d pixels", w, ph, s.MaxPixels)
	case info.Width > video.MaxCompositeSide || info.Height > video.MaxCompositeSide:
		return nil, info, errs.Newf(errs.KindLimitExceeded, "codec: %dx%d frames are too large for video output", w, ph)
	}

	var a vips.Arena
	defer a.Close()
	strip, err := rgb8(ctx, &a, img, codec.Alpha())
	if err != nil {
		return nil, info, err
	}
	def, ok := s.CRF[codec]
	if !ok {
		def = video.DefaultCRF(codec)
	}
	info.CRF = video.CRF(codec, req.Quality, def)
	speed, ok := s.Speed[codec]
	if !ok {
		speed = video.DefaultSpeed
	}
	preset := s.Preset[codec]
	if preset == "" {
		preset = video.DefaultPreset
	}
	fps := 10.0
	if total > 0 {
		fps = float64(n) * 1000 / float64(total)
	}
	enc, err := ffmpeg.NewEncoder(ctx, &video.EncodeParams{
		Codec: codec, Container: container, Width: info.Width, Height: info.Height,
		Alpha: strip.Bands() == 4, Bands: strip.Bands(), CRF: info.CRF, Speed: speed, Preset: preset, FPS: fps,
	})
	if err != nil {
		return nil, info, err
	}
	defer enc.Close()
	var pts int64
	for i := range n {
		if err := ctxErr(ctx); err != nil {
			return nil, info, err
		}
		if err := encodeFrame(ctx, enc, strip, i, info.Width, info.Height, pts, durs[i]); err != nil {
			return nil, info, err
		}
		pts += durs[i]
	}
	out, err := enc.Finish(ctx)
	if err != nil {
		return nil, info, err
	}
	return out, info, nil
}

// encodeFrame renders frame i of strip, padded to w × h, into C memory and
// encodes it.
func encodeFrame(ctx context.Context, enc *ffmpeg.Encoder, strip *vips.Image, i, w, h int, pts, dur int64) error {
	var a vips.Arena
	defer a.Close()
	ph := strip.PageHeight()
	f, err := a.Add(strip.ExtractArea(ctx, image.Rect(0, i*ph, strip.Width(), (i+1)*ph)))
	if err != nil {
		return err
	}
	if f.Width() != w || f.Height() != h {
		// 4:2:0 needs even sides: repeat the last column or row.
		if f, err = a.Add(f.Embed(ctx, 0, 0, w, h, vips.ExtendCopy, nil)); err != nil {
			return err
		}
	}
	pix, err := f.WritePixels(ctx)
	if err != nil {
		return err
	}
	defer pix.Release()
	return enc.Frame(ctx, pix.Bytes(), pts, dur)
}

// rgb8 converts img to 8-bit sRGB with three bands, or four when it has
// alpha and alpha is kept (otherwise it is flattened on white).
func rgb8(ctx context.Context, a *vips.Arena, img *vips.Image, keepAlpha bool) (*vips.Image, error) {
	var err error
	if img.HasAlpha() && !keepAlpha {
		if img, err = a.Add(img.Flatten(ctx, []float64{255, 255, 255})); err != nil {
			return nil, err
		}
	}
	switch img.Interpretation() {
	case vips.InterpretationSRGB, vips.InterpretationRGB:
	case vips.InterpretationMultiband, vips.InterpretationError, vips.InterpretationHistogram,
		vips.InterpretationFourier, vips.InterpretationMatrix:
		if img.Bands() <= 2 {
			if img, err = a.Add(img.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationBW})); err != nil {
				return nil, err
			}
			if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
				return nil, err
			}
		}
	default:
		if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	want := 3
	if keepAlpha && img.HasAlpha() {
		want = 4
	}
	if img.Bands() > want {
		if img, err = a.Add(img.ExtractBand(ctx, 0, want)); err != nil {
			return nil, err
		}
	}
	if img.Bands() != want {
		return nil, errs.Newf(errs.KindUnsupported, "codec: %d-band image for video output", img.Bands())
	}
	return img, nil
}
