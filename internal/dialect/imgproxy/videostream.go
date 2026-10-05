// Adaptive streaming options of /video URLs (WP-126). Copyright
// xtandard-image contributors, MIT. imgproxy has no streaming: the names
// follow the /video options of docs/URL-DIALECTS.md §3.4.

package imgproxy

import (
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// A /video URL is an HLS or DASH stream (transform.VideoStream) with the
// m3u8 or mpd extension (or format), or with stream:
//
//	stream:%protocol[:%height...]       (st)  hls or dash, then the rungs
//	segment_duration:%seconds           (sgd) whole seconds, 1–30
//
// The rungs are the short sides of the renditions (360 is 640×360 for a
// landscape 16:9 source); none is the configured ladder
// (XI_VIDEO_TRANSFORM_ABR_LADDER). The other /video options (trimming,
// crop, rotation, fps, quality, codecs, keyframe_interval, audio) apply to
// every rendition; a size, a video bit rate or a watermark is a 400 on a
// stream.

// streamProtocols are the stream extensions and protocol names.
var streamProtocols = map[string]transform.StreamProtocol{
	"m3u8": transform.StreamHLS, "hls": transform.StreamHLS,
	"mpd": transform.StreamDASH, "dash": transform.StreamDASH,
}

// Expected-value texts of the stream options.
const (
	expStream          = "hls or dash, then rendition heights between 64 and 4320 (at most 8)"
	expSegmentDuration = "whole seconds between 1 and 30"
)

// streamOp returns the request's stream, created on first use.
func (st *state) streamOp() *transform.VideoStream {
	op := st.r.Video
	if op.Stream == nil {
		op.Stream = &transform.VideoStream{}
	}
	return op.Stream
}

// streamContainer applies an m3u8 or mpd extension or format: a stream in
// CMAF (MP4). It reports whether s was one.
func (st *state) streamContainer(s string) bool {
	ext := strings.ToLower(s)
	if ext != "m3u8" && ext != "mpd" {
		return false
	}
	st.streamOp().Protocol = streamProtocols[ext]
	st.r.Video.Container = transform.VideoContainerMP4
	st.video.containerSet = true
	return true
}

// videoStream parses stream:%protocol[:%height...].
func (st *state) videoStream(args []string) error {
	if len(args) > 1+transform.MaxRenditions {
		return dialect.InvalidArgument("stream", joinArgs(args), expStream)
	}
	p, ok := streamProtocols[strings.ToLower(args[0])]
	if !ok {
		return dialect.InvalidArgument("stream", args[0], expStream)
	}
	var hs []int
	for _, a := range args[1:] {
		h, err := strconv.Atoi(a)
		if err != nil || h < transform.MinRendition || h > transform.MaxRendition {
			return dialect.InvalidArgument("stream", a, expStream)
		}
		hs = append(hs, h)
	}
	s := st.streamOp()
	s.Protocol, s.Renditions = p, hs
	st.r.Video.Container = transform.VideoContainerMP4
	st.video.containerSet = true
	return nil
}

// videoSegmentDuration parses segment_duration:%seconds.
func (st *state) videoSegmentDuration(args []string) error {
	if err := maxArgs("segment_duration", args, 1); err != nil {
		return err
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 || n > int(transform.MaxSegmentDuration/time.Second) {
		return dialect.InvalidArgument("segment_duration", args[0], expSegmentDuration)
	}
	st.streamOp().SegmentDuration = time.Duration(n) * time.Second
	return nil
}
