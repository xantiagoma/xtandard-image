package app

import (
	"context"
	"errors"
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
	"github.com/xantiagoma/xtandard-image/internal/spool"
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
// sp, when set, holds large outputs (WP-121b).
func buildVideoTransform(cfg *config.Config, met *metrics.Metrics, ex *exec.Executor, sp *spool.Spool) (*media.Service, error) {
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
	opts := []media.Option{media.WithMetrics(videoMetrics{obs: obs})}
	if ex != nil {
		opts = append(opts, media.WithOverlayRenderer(overlayRenderer{ex: ex}))
	}
	s, err := media.New(&mc, ffmpeg.TranscodeCaps(), ffmpegEngine{spool: sp}, opts...)
	if err != nil {
		return nil, err
	}
	obs.Capacity(s.Stats().Capacity)
	return s, nil
}

// mediaConfig maps XI_VIDEO_TRANSFORM_* and XI_VIDEO_ENCODE_*.
func mediaConfig(cfg *config.Config) media.Config {
	t, e := &cfg.Video.Transform, &cfg.Video.Encode
	return media.Config{
		Workers:      t.Workers,
		Capacity:     t.Capacity,
		MaxJobs:      t.MaxJobs,
		Queue:        t.Queue,
		QueueCost:    t.QueueCost,
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
		Stream: streamSettings(cfg),
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

// ffmpegEngine is media.Engine over ffmpeg.OpenTranscode: a spooled
// source (a *cache.FileBuffer) is read from its file, and an output
// larger than the spool's threshold is written into a spool file
// (WP-121b). A nil spool keeps every output in memory.
type ffmpegEngine struct{ spool *spool.Spool }

func (e ffmpegEngine) Open(ctx context.Context, src media.Buffer, lim video.Limits, threads int) (media.Job, error) {
	if fs, ok := src.(ffmpeg.FileSource); ok {
		t, err := ffmpeg.OpenTranscodeFile(ctx, fs, lim, threads)
		if err != nil {
			return nil, err
		}
		return ffmpegJob{t: t, spool: e.spool}, nil
	}
	vb, err := vipsBuffer(src)
	if err != nil {
		return nil, err
	}
	defer vb.Release() // the transcoder retains its own reference
	t, err := ffmpeg.OpenTranscode(ctx, vb, lim, threads)
	if err != nil {
		return nil, err
	}
	return ffmpegJob{t: t, spool: e.spool}, nil
}

type ffmpegJob struct {
	t     *ffmpeg.Transcoder
	spool *spool.Spool
}

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
	if j.spills(p) {
		return j.runSpill(ctx, p, px)
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

// spills reports whether p's output may go to a spool file: a whole file
// (not a stream part, which is cut in memory, nor a waveform's pixels)
// whose limit passes the threshold (or that has none).
func (j ffmpegJob) spills(p *video.TranscodePlan) bool {
	return j.spool.Threshold() > 0 && p.Fragment == nil && p.Waveform == nil &&
		(p.MaxBytes <= 0 || j.spool.Spills(p.MaxBytes))
}

// runSpill transcodes with a spool file ready: the shim writes the output
// into it once it outgrows the threshold. The file reserves the output
// limit of the spool budget while it runs (a full budget is 503 before
// any work), then only its size; an output that stayed small is returned
// in memory and the file discarded.
func (j ffmpegJob) runSpill(ctx context.Context, p *video.TranscodePlan, px []*vips.Buffer) (media.Buffer, error) {
	w, err := j.spool.Create(spool.KindOutput, max(p.MaxBytes, 0))
	if err != nil {
		return nil, err
	}
	out, n, err := j.t.RunSpill(ctx, p, w.File(), j.spool.Threshold(), px...)
	switch {
	case err != nil:
		w.Abort()
		return nil, err
	case out != nil:
		w.Abort()
		return out, nil
	}
	fb, err := w.Finish(n)
	if err != nil {
		return nil, err
	}
	return fb, nil
}

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

// videoMetrics adapts media.Metrics to the xi_video_transform_* metrics.
type videoMetrics struct{ obs metrics.VideoObserver }

func (m videoMetrics) Transcoded(result string, d time.Duration, in, out int64) {
	m.obs.Transcoded(result, d, in, out)
}

func (m videoMetrics) Coalesced()            { m.obs.Coalesced() }
func (m videoMetrics) JobCost(units float64) { m.obs.JobCost(units) }

func (m videoMetrics) Rejected(r limiter.Reason) {
	switch r {
	case limiter.ReasonQueueFull:
		m.obs.Rejected(metrics.OverloadQueueFull)
	case limiter.ReasonQueueCost:
		m.obs.Rejected(metrics.OverloadQueueCost)
	case limiter.ReasonQueueTimeout:
		m.obs.Rejected(metrics.OverloadWaitTimeout)
	default: // canceled: not an overload
	}
}

func (m videoMetrics) Load(l media.Load) {
	m.obs.QueueDepth(int(l.Jobs))
	m.obs.WorkersBusy(int(l.Running))
	m.obs.CapacityUsed(l.CapacityUsed)
	m.obs.QueuedCost(l.QueuedCost)
}
