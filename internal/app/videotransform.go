package app

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/limiter"
	"github.com/xantiagoma/xtandard-image/internal/media"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Video transformations (WP-121, ADR 0012): internal/media over the FFmpeg
// transcoder, adapted to server.VideoTransformer.

// buildVideoTransform returns the video transformation service, or nil
// when XI_VIDEO_TRANSFORM_ENABLED is off or XI_ROLE is image (that role
// never creates the video pool, ADR 0012 §4). Enabled on an image without
// FFmpeg fails startup, whatever the role. ex renders image and text
// overlays (WP-124) with the image watermark code; nil leaves them
// unsupported.
func buildVideoTransform(cfg *config.Config, met *metrics.Metrics, ex *exec.Executor) (*media.Service, error) {
	t := &cfg.Video.Transform
	if !t.Enabled {
		return nil, nil
	}
	if !ffmpeg.Available() {
		return nil, errors.New("video.transform.enabled (XI_VIDEO_TRANSFORM_ENABLED) is true but this image has no FFmpeg; use the full or gpl image")
	}
	if !videoTransformServed(cfg) {
		return nil, nil
	}
	obs := met.Video()
	mc := mediaConfig(cfg)
	opts := []media.Option{media.WithMetrics(obs), media.WithLimiterMetrics(&videoLimiterMetrics{obs: obs})}
	if ex != nil {
		opts = append(opts, media.WithOverlayRenderer(overlayRenderer{ex: ex}))
	}
	return media.New(&mc, ffmpeg.TranscodeCaps(), ffmpegEngine{}, opts...)
}

// mediaConfig maps XI_VIDEO_TRANSFORM_* and XI_VIDEO_ENCODE_*.
func mediaConfig(cfg *config.Config) media.Config {
	t, e := &cfg.Video.Transform, &cfg.Video.Encode
	return media.Config{
		Workers:      t.Workers,
		Queue:        t.Queue,
		QueueTimeout: t.QueueTimeout,
		Timeout:      t.Timeout,
		Input: video.Limits{
			MaxBytes:    int64(t.MaxInputBytes),
			MaxDuration: t.MaxInputDuration,
			MaxPixels:   t.MaxInputPixels,
		},
		Transcode: video.TranscodeSettings{
			Encode: video.EncodeSettings{
				CRF: map[video.Codec]int{
					video.CodecVP9: e.VP9CRF, video.CodecAV1: e.AV1CRF, video.CodecH264: e.H264CRF, video.CodecHEVC: e.HEVCCRF,
				},
				Speed:  map[video.Codec]int{video.CodecVP9: e.VP9Speed, video.CodecAV1: e.AV1Speed},
				Preset: map[video.Codec]string{video.CodecH264: e.H264Preset, video.CodecHEVC: e.HEVCPreset},
			},
			Threads:           t.Threads,
			MaxOutputPixels:   t.MaxOutputPixels,
			MaxOutputDuration: t.MaxOutputDuration,
			MaxFPS:            t.MaxFPS,
			MaxOutputBytes:    int64(t.MaxOutputBytes),
		},
	}
}

// videoServerConfig is the server's view: a video request's budget covers
// the fetch, the wait for a video worker and the transcode.
func videoServerConfig(cfg *config.Config) server.VideoConfig {
	t := &cfg.Video.Transform
	return server.VideoConfig{
		Budget:        cfg.Fetch.Timeout + t.QueueTimeout + t.Timeout,
		MaxSrcBytes:   int64(t.MaxInputBytes),
		AllowUnsigned: t.UnsignedPolicy == config.UnsignedAllow,
	}
}

// videoTransformServed reports whether this instance runs video
// transformations: enabled, and a role that serves them.
func videoTransformServed(cfg *config.Config) bool {
	return cfg.Video.Transform.Enabled && cfg.Role.Videos()
}

// serverRole maps XI_ROLE.
func serverRole(r config.Role) server.Role {
	switch r {
	case config.RoleImage:
		return server.RoleImage
	case config.RoleVideo:
		return server.RoleVideo
	default:
		return server.RoleAll
	}
}

// fetchMaxBytes is the fetcher's global cap: video requests may fetch up
// to XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES, images still pass their own
// (smaller) limit per request.
func fetchMaxBytes(cfg *config.Config) int64 {
	n := int64(cfg.Limits.MaxSrcBytes)
	if t := &cfg.Video.Transform; videoTransformServed(cfg) && int64(t.MaxInputBytes) > n {
		n = int64(t.MaxInputBytes)
	}
	return n
}

// ffmpegEngine is media.Engine over ffmpeg.OpenTranscode.
type ffmpegEngine struct{}

func (ffmpegEngine) Open(ctx context.Context, src media.Buffer, lim video.Limits, threads int) (media.Job, error) {
	vb, err := vipsBuffer(src)
	if err != nil {
		return nil, err
	}
	defer vb.Release() // the transcoder retains its own reference
	t, err := ffmpeg.OpenTranscode(ctx, vb, lim, threads)
	if err != nil {
		return nil, err
	}
	return ffmpegJob{t: t}, nil
}

type ffmpegJob struct{ t *ffmpeg.Transcoder }

func (j ffmpegJob) Input() video.TranscodeInput { return j.t.Input() }

func (j ffmpegJob) Run(ctx context.Context, p *video.TranscodePlan, pixels []media.Buffer) (media.Buffer, error) {
	px := make([]*vips.Buffer, len(pixels))
	defer func() {
		for _, b := range px {
			if b != nil {
				b.Release()
			}
		}
	}()
	for i, b := range pixels {
		if b == nil {
			continue
		}
		vb, err := vipsBuffer(b)
		if err != nil {
			return nil, err
		}
		px[i] = vb
	}
	out, err := j.t.Run(ctx, p, px...)
	if err != nil {
		return nil, err
	}
	if p.Waveform != nil {
		defer out.Release()
		return encodeWaveform(ctx, out.Bytes(), p.Waveform)
	}
	return out, nil
}

// overlayRenderer is media.OverlayRenderer over the image watermark code
// (exec.Executor.RenderOverlay): the configured watermark, the fonts and
// XI_PROCESSING_WATERMARK_OPACITY apply as on images.
type overlayRenderer struct{ ex *exec.Executor }

func (r overlayRenderer) Render(ctx context.Context, o *transform.VideoOverlay, img media.Buffer, w, h int) (media.Layer, bool, error) {
	var oi *exec.OverlayImage
	if img != nil {
		f, ok := codec.Sniff(img.Bytes())
		if !ok {
			return media.Layer{}, false, errs.WithPublic(errs.New(errs.KindUnsupported, "app: unrecognised overlay format"),
				"unsupported overlay image format")
		}
		vb, err := vipsBuffer(img)
		if err != nil {
			return media.Layer{}, false, err
		}
		defer vb.Release()
		name := f.String()
		if f == codec.FormatUHDR {
			name = "jpeg"
		}
		oi = &exec.OverlayImage{Source: vb, Format: name}
	}
	ov, err := r.ex.RenderOverlay(ctx, &o.Watermark, oi, w, h)
	if err != nil || ov == nil {
		return media.Layer{}, false, err
	}
	return media.Layer{Pixels: ov.Pixels, X: ov.X, Y: ov.Y, Width: ov.Width, Height: ov.Height}, true, nil
}

// encodeWaveform encodes the RGBA pixels of a waveform picture (WP-123)
// with libvips: flattened onto the background when it is opaque, saved as
// PNG or WebP (lossless).
func encodeWaveform(ctx context.Context, rgba []byte, w *video.WaveformPlan) (media.Buffer, error) {
	buf, err := vips.NewBuffer(len(rgba))
	if err != nil {
		return nil, err
	}
	copy(buf.Bytes(), rgba)
	defer buf.Release()
	format := vips.FormatPNG
	if w.Format == transform.VideoContainerWebP {
		format = vips.FormatWebP
	}
	var blob *vips.Blob
	err = vips.Run(ctx, func() error {
		img, err := vips.NewFromBuffer(ctx, buf, w.Width, w.Height, 4, vips.BandFormatUchar)
		if err != nil {
			return err
		}
		defer img.Close()
		if bg := w.Background; bg.A != 0 {
			flat, err := img.Flatten(ctx, []float64{float64(bg.R), float64(bg.G), float64(bg.B)})
			if err != nil {
				return err
			}
			defer flat.Close()
			img = flat
		}
		o := vips.DefaultSaveOptions()
		o.WebP.Lossless = true
		blob, err = img.Save(ctx, format, &o)
		return err
	})
	if err != nil {
		return nil, err
	}
	return blob, nil
}

func (j ffmpegJob) Close() { j.t.Close() }

// mediaTransformer adapts media.Service to server.VideoTransformer.
type mediaTransformer struct{ s *media.Service }

func (m mediaTransformer) Resolve(op *transform.VideoOp) (video.TranscodeOutput, error) {
	return m.s.Resolve(op)
}

func (m mediaTransformer) Transform(ctx context.Context, key string, src fetch.Buffer, op *transform.VideoOp, overlays []fetch.Buffer) (*server.VideoResult, error) {
	var imgs []media.Buffer
	for _, o := range overlays {
		if o == nil {
			imgs = append(imgs, nil)
		} else {
			imgs = append(imgs, o)
		}
	}
	r, err := m.s.Transform(ctx, key, src, op, imgs)
	if err != nil {
		return nil, err
	}
	return &server.VideoResult{Body: r.Body, Output: r.Output, Width: r.Width, Height: r.Height, Shared: r.Shared}, nil
}

// videoLimiterMetrics adapts limiter.Metrics to the xi_video_transform_*
// queue gauges and overload counter.
type videoLimiterMetrics struct {
	obs      metrics.VideoObserver
	waiting  atomic.Int64
	inFlight atomic.Int64
}

func (m *videoLimiterMetrics) OnQueued()                {}
func (m *videoLimiterMetrics) OnAdmitted(time.Duration) {}

func (m *videoLimiterMetrics) OnRejected(r limiter.Reason) {
	switch r {
	case limiter.ReasonQueueFull:
		m.obs.Rejected(metrics.OverloadQueueFull)
	case limiter.ReasonQueueTimeout:
		m.obs.Rejected(metrics.OverloadWaitTimeout)
	default: // canceled: not an overload
	}
}

func (m *videoLimiterMetrics) AddQueueDepth(delta int64) {
	w := m.waiting.Add(delta)
	m.obs.QueueDepth(int(w + m.inFlight.Load()))
}

func (m *videoLimiterMetrics) AddInFlight(delta int64) {
	n := m.inFlight.Add(delta)
	m.obs.WorkersBusy(int(n))
	m.obs.QueueDepth(int(n + m.waiting.Load()))
}
