package imagekit

import (
	"slices"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Adaptive bitrate streaming (WP-126,
// https://imagekit.io/docs/adaptive-bitrate-streaming):
// <video>/ik-master.m3u8 is an HLS stream of the video and
// <video>/ik-master.mpd a DASH one (transform.VideoStream), their
// renditions the heights of sr-<h>_<h>… (240, 360, 480, 720, 1080, 1440,
// 2160; without sr-, the configured ladder) at this server's bit rates.
// The video's other transformations (trimming, cm-extract, rotation, fps,
// quality, codecs, and since WP-131 l-image/l-text layers, laid out on the
// largest rendition and scaled to the others) apply to every rendition; a
// resize (the renditions are the sizes) is recorded and not applied.

// Reasons of the stream mapping.
const (
	reasonNoStream      = "applies to ik-master.m3u8 and ik-master.mpd only"
	reasonStreamApplied = "not applied to streams"
)

// masterProtocols are the ik-master suffixes.
var masterProtocols = map[string]transform.StreamProtocol{
	"m3u8": transform.StreamHLS,
	"mpd":  transform.StreamDASH,
}

// expRenditions is what sr- takes.
const expRenditions = "heights joined by _ (240_360_480_720_1080), from 64 to 4320, at most 8"

// renditions parses sr-<h>_<h>….
func (vs *videoState) renditions(v string) error {
	vs.sr = true
	var hs []int
	for item := range strings.SplitSeq(v, "_") {
		h, err := strconv.Atoi(item)
		if err != nil || h < transform.MinRendition || h > transform.MaxRendition || len(hs) == transform.MaxRenditions {
			return dialect.InvalidArgument("sr", v, expRenditions)
		}
		if !slices.Contains(hs, h) {
			hs = append(hs, h)
		}
	}
	if st := vs.ps.stream; st != nil {
		st.Renditions = hs
	}
	return nil
}

// finishStream completes an ik-master stream: what a stream cannot apply
// is recorded and dropped.
func (vs *videoState) finishStream() error {
	op := &vs.op
	if vs.vcNone {
		return dialect.InvalidArgument("vc", "none", "a video codec for ik-master")
	}
	for _, d := range []struct {
		name string
		set  bool
	}{
		{"w", op.Width != 0},
		{"h", op.Height != 0},

		{"ac", op.AudioCodec != transform.AudioCodecAuto && op.AudioCodec != transform.AudioCodecAAC},
		{"vc", op.VideoCodec == transform.VideoCodecHEVC},
	} {
		if d.set {
			if err := vs.skip(d.name, reasonStreamApplied); err != nil {
				return err
			}
		}
	}
	op.Width, op.Height, op.Fit, op.Gravity, op.MaxBitrate = 0, 0, transform.VideoFitContain, 0, 0
	if op.AudioCodec != transform.AudioCodecAAC {
		op.AudioCodec = transform.AudioCodecAuto
	}
	if op.VideoCodec == transform.VideoCodecHEVC {
		op.VideoCodec = transform.VideoCodecAuto
	}
	op.Container, op.AutoContainer, op.Stream = transform.VideoContainerMP4, false, vs.ps.stream
	return nil
}
