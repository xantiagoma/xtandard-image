package app

import (
	"context"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// The playground's video mode (WP-127): what the video subsystem of this
// build can write, and media probes for its Source vs result table.

// videoContainers are the containers the playground offers, in menu order.
var videoContainers = []transform.VideoContainer{
	transform.VideoContainerMP4, transform.VideoContainerWebM, transform.VideoContainerM4A, transform.VideoContainerMP3,
	transform.VideoContainerOpus, transform.VideoContainerOgg, transform.VideoContainerFLAC, transform.VideoContainerWAV,
	transform.VideoContainerPNG, transform.VideoContainerWebP,
}

// playgroundVideoTransform describes caps and the output limits of cfg for
// api/meta; probe reports the api/probe endpoints.
func playgroundVideoTransform(cfg *config.Config, caps video.TranscodeCaps, probe bool) *playground.VideoTransform {
	t := &cfg.Video.Transform
	vt := &playground.VideoTransform{
		UnsignedPolicy: string(t.UnsignedPolicy), Codecs: caps.Names(), Containers: []string{},
		Overlays: caps.Overlays, Timecode: caps.Timecode, AudioEffects: caps.AudioEffects,
		MaxOutputDuration: t.MaxOutputDuration.Seconds(), MaxOutputPixels: int(t.MaxOutputPixels), MaxFPS: t.MaxFPS,
		Probe: probe,
	}
	for _, c := range videoContainers {
		op := transform.VideoOp{Container: c}
		if c.Image() {
			op.Waveform = &transform.VideoWaveform{}
		}
		out, err := caps.Resolve(&op)
		if err != nil {
			continue
		}
		vt.Containers = append(vt.Containers, c.String())
		if c == transform.VideoContainerMP4 {
			vt.MP4Video = out.Video.String()
		}
	}
	if vt.Codecs == nil {
		vt.Codecs = []string{}
	}
	return vt
}

// mediaProber implements playground.MediaProber with the transcoder's
// probe (ffmpeg.OpenTranscode: demux, stream headers, no decode beyond
// what avformat_find_stream_info needs): it reads audio files too, which
// /info refuses.
type mediaProber struct {
	reg     *source.Registry
	fetcher *fetch.Fetcher
	limits  video.Limits
	// maxBody bounds api/probe/result (XI_VIDEO_TRANSFORM_MAX_OUTPUT_BYTES).
	maxBody int64
}

var _ playground.MediaProber = (*mediaProber)(nil)

func newMediaProber(cfg *config.Config, reg *source.Registry, fetcher *fetch.Fetcher) *mediaProber {
	t := &cfg.Video.Transform
	return &mediaProber{
		reg: reg, fetcher: fetcher, maxBody: int64(t.MaxOutputBytes),
		limits: video.Limits{MaxBytes: int64(t.MaxInputBytes), MaxDuration: t.MaxInputDuration, MaxPixels: t.MaxInputPixels},
	}
}

// MaxBytes implements playground.MediaProber.
func (p *mediaProber) MaxBytes() int64 { return p.maxBody }

// ProbeSource implements playground.MediaProber: the source is resolved
// and fetched as a request's would be (allowlists, the SSRF-safe fetcher,
// XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES); playground:// resolves because ctx
// is a playground request.
func (p *mediaProber) ProbeSource(ctx context.Context, src string) (*playground.MediaInfo, error) {
	key, err := p.reg.Resolve(transform.SourceSpec{Raw: src})
	if err != nil {
		return nil, err
	}
	if key.Source == source.PlaygroundSourceName && !source.IsPlaygroundRequest(ctx) {
		return nil, errs.New(errs.KindNotFound, "app: playground source outside the playground")
	}
	fr, err := p.fetcher.Fetch(ctx, key, source.FetchOptions{MaxBytes: p.limits.MaxBytes})
	if err != nil {
		return nil, err
	}
	defer fr.Release()
	vb, err := vipsBuffer(fr.Buffer)
	if err != nil {
		return nil, err
	}
	defer vb.Release()
	return p.probe(ctx, vb, p.limits)
}

// ProbeBytes implements playground.MediaProber: b is copied into C memory
// (FFmpeg never reads Go memory).
func (p *mediaProber) ProbeBytes(ctx context.Context, b []byte) (*playground.MediaInfo, error) {
	vb, err := vips.NewBuffer(len(b))
	if err != nil {
		return nil, err
	}
	defer vb.Release()
	copy(vb.Bytes(), b)
	lim := p.limits
	lim.MaxBytes = max(lim.MaxBytes, int64(len(b)))
	return p.probe(ctx, vb, lim)
}

func (p *mediaProber) probe(ctx context.Context, vb *vips.Buffer, lim video.Limits) (*playground.MediaInfo, error) {
	t, err := ffmpeg.OpenTranscode(ctx, vb, lim, 1)
	if err != nil {
		return nil, err
	}
	in := t.Input()
	t.Close()
	return mediaInfo(&in, int64(vb.Len())), nil
}

// mediaInfo is in as the playground shows it.
func mediaInfo(in *video.TranscodeInput, size int64) *playground.MediaInfo {
	mi := &playground.MediaInfo{Container: in.Container, Duration: round3(in.Duration), Size: size}
	if in.Duration > 0 {
		mi.BitRate = int64(math.Round(float64(size) * 8 / in.Duration))
	}
	if in.Video {
		w, h := in.DisplaySize()
		mi.Video = &playground.MediaVideo{Codec: in.VideoCodec, Width: w, Height: h, FPS: round3(in.FPS), Rotation: in.Rotation}
	}
	if in.Audio {
		mi.Audio = &playground.MediaAudio{Codec: in.AudioCodec, SampleRate: in.SampleRate, Channels: in.Channels}
	}
	return mi
}

// round3 keeps three decimals (29.97002997… → 29.97).
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
