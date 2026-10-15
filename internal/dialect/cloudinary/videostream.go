package cloudinary

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Adaptive streaming (WP-126): an m3u8 (HLS) or mpd (DASH) output, by
// f_ or the public ID's extension, is a stream of the video
// (transform.VideoStream), its renditions from sp_<profile>
// (https://cloudinary.com/documentation/adaptive_bitrate_streaming):
// sp_auto (and no sp_) is the configured ladder (XI_VIDEO_TRANSFORM_ABR_LADDER);
// the predefined profiles are their sets of heights, at this server's bit
// rates (the ladder's, else the standard ones of each height). Everything
// else of the URL (trimming, c_crop, rotation, fps, quality, codecs, audio,
// and since WP-131 l_ layers, laid out on the largest rendition and scaled
// to the others) applies to every rendition; a resize (the renditions are
// the sizes) and a bit rate (theirs) are recorded and not applied.

// Reasons of the stream mapping.
const (
	reasonNoStream      = "applies to m3u8 and mpd output only"
	reasonNoHLS         = "applies to m3u8 output only"
	reasonStreamApplied = "not applied to streams"
)

// streamProtocols are the stream output formats.
var streamProtocols = map[string]transform.StreamProtocol{
	"m3u8": transform.StreamHLS,
	"mpd":  transform.StreamDASH,
}

// streamingProfiles are Cloudinary's predefined streaming profiles, by
// their renditions' heights; auto is the configured ladder.
var streamingProfiles = map[string][]int{
	"auto":         nil,
	"4k":           {2160, 1440, 1080, 720, 480, 360, 240},
	"full_hd":      {1080, 720, 480, 360, 240},
	"hd":           {720, 480, 360, 240},
	"sd":           {480, 360, 240},
	"full_hd_wifi": {1080, 720, 480},
	"full_hd_lean": {1080, 720, 360},
	"hd_lean":      {720, 360, 240},
}

// streamingProfile parses sp_<profile>; a custom (account) profile is
// recorded and the configured ladder used.
func (va *videoApplier) streamingProfile(v string) error {
	name := strings.ToLower(v)
	if name == "" {
		return dialect.InvalidArgument("sp", v, "auto or a streaming profile name")
	}
	if _, ok := streamingProfiles[name]; !ok {
		if err := va.skip(truncate("sp_"+v), reasonUnsupported); err != nil {
			return err
		}
		name = "auto"
	}
	va.profile = name
	return nil
}

// finishStream completes a stream: the profile's renditions, and what a
// stream cannot apply recorded and dropped.
func (va *videoApplier) finishStream() error {
	op, st := &va.op, va.stream
	if va.profile != "" {
		st.Renditions = streamingProfiles[va.profile]
	}
	if va.vcNone {
		return dialect.InvalidArgument("vc", "none", "a video codec for m3u8 and mpd")
	}
	drop := func(name string, set bool) error {
		if !set {
			return nil
		}
		return va.skip(name, reasonStreamApplied)
	}
	if err := drop("w", op.Width != 0); err != nil {
		return err
	}
	if err := drop("h", op.Height != 0); err != nil {
		return err
	}
	if err := drop("br", op.MaxBitrate != 0); err != nil {
		return err
	}

	if err := drop("ac", op.AudioCodec != transform.AudioCodecAuto && op.AudioCodec != transform.AudioCodecAAC); err != nil {
		return err
	}
	if err := drop("vc", op.VideoCodec == transform.VideoCodecHEVC); err != nil {
		return err
	}
	op.Width, op.Height, op.Fit, op.Gravity, op.MaxBitrate = 0, 0, transform.VideoFitContain, 0, 0
	if op.AudioCodec != transform.AudioCodecAAC {
		op.AudioCodec = transform.AudioCodecAuto
	}
	if op.VideoCodec == transform.VideoCodecHEVC {
		op.VideoCodec = transform.VideoCodecAuto
	}
	if op.SampleRate != 0 && op.SampleRate != 48000 {
		if err := va.skip("af", reasonStreamApplied); err != nil {
			return err
		}
		op.SampleRate = 0
	}
	if va.hlsv3 {
		// HLS version 3: MPEG-TS segments with H.264 (WP-131).
		if st.Protocol != transform.StreamHLS {
			if err := va.skip("fl_hlsv3", reasonNoHLS); err != nil {
				return err
			}
		} else {
			st.Format = transform.SegmentTS
			if op.VideoCodec != transform.VideoCodecAuto && op.VideoCodec != transform.VideoCodecH264 {
				if err := va.skip("vc", "mpeg-ts segments are h264"); err != nil {
					return err
				}
				op.VideoCodec = transform.VideoCodecAuto
			}
		}
	}
	op.Container, op.AutoContainer, op.Stream = transform.VideoContainerMP4, false, st
	return nil
}
