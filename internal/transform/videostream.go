package transform

import (
	"slices"
	"strconv"
	"time"
)

// Adaptive streaming (WP-126, ADR 0012 §6): a VideoOp with a Stream is
// delivered as an HLS master playlist or a DASH MPD instead of a file. The
// renditions come from the configured ABR ladder (or the subset a URL
// names), never larger than the source; their segments are CMAF fragments
// of a fixed grid, transcoded when a player asks for them. Everything else
// in the VideoOp (the cut, the crop, rotation, frame rate, quality, the
// audio options) applies to every rendition.

// VideoStream asks for adaptive streaming.
type VideoStream struct {
	// Protocol is the manifest: HLS (the zero value) or DASH.
	Protocol StreamProtocol
	// Renditions are the rungs to offer, by the short side of their
	// frame in pixels (720 is 1280×720 for a landscape source); empty is
	// the configured ladder. Rungs larger than the source are dropped.
	Renditions []int
	// MinHeight and MaxHeight keep only the rungs within them (0: no
	// bound), and MaxRenditions only that many of the largest (0: all):
	// Cloudinary's streaming profiles.
	MinHeight, MaxHeight int
	MaxRenditions        int
	// SegmentDuration is the length of every segment but the last, in
	// whole seconds; 0 is the configured one.
	SegmentDuration time.Duration
}

// Stream limits.
const (
	// MinRendition and MaxRendition bound a rung (short side, pixels).
	MinRendition = 64
	MaxRendition = 4320
	// MaxRenditions bounds VideoStream.Renditions.
	MaxRenditions = 8
	// MinSegmentDuration and MaxSegmentDuration bound
	// VideoStream.SegmentDuration (whole seconds).
	MinSegmentDuration = time.Second
	MaxSegmentDuration = 30 * time.Second
)

// validateStream checks the stream and what a stream cannot combine with:
// the renditions decide the size and the bit rate, the output is CMAF
// (MP4 with AAC), and overlays (placed in pixels of one output size) are
// not drawn on streams.
func (o *VideoOp) validateStream(v *validator, f string) {
	st := o.Stream
	f += ".stream"
	v.enum(f+".protocol", validEnum(st.Protocol, numStreamProtocols))
	if len(st.Renditions) > MaxRenditions {
		v.fail(f+".renditions", "at most "+strconv.Itoa(MaxRenditions)+" renditions")
	}
	for i, r := range st.Renditions {
		v.intRange(f+".renditions["+strconv.Itoa(i)+"]", r, MinRendition, MaxRendition)
		if slices.Index(st.Renditions, r) != i {
			v.fail(f+".renditions["+strconv.Itoa(i)+"]", "duplicate rendition")
		}
	}
	v.intRange(f+".min_height", st.MinHeight, 0, MaxRendition)
	v.intRange(f+".max_height", st.MaxHeight, 0, MaxRendition)
	if st.MinHeight > 0 && st.MaxHeight > 0 && st.MinHeight > st.MaxHeight {
		v.fail(f+".min_height", "must not be above max_height")
	}
	v.intRange(f+".max_renditions", st.MaxRenditions, 0, MaxRenditions)
	if d := st.SegmentDuration; d != 0 && (d < MinSegmentDuration || d > MaxSegmentDuration || d%time.Second != 0) {
		v.fail(f+".segment_duration", "whole seconds between "+MinSegmentDuration.String()+" and "+MaxSegmentDuration.String())
	}
	switch {
	case o.Container != VideoContainerMP4 || o.AutoContainer:
		v.fail(f, "a stream is CMAF (mp4)")
	case o.Width != 0 || o.Height != 0 || o.Fit != VideoFitContain:
		v.fail(f, "a stream's sizes are its renditions'")
	case o.MaxBitrate != 0:
		v.fail(f, "a stream's bit rates are its renditions'")
	case len(o.Overlays) > 0:
		v.fail(f, "overlays are not drawn on streams")
	case o.VideoCodec == VideoCodecHEVC:
		v.fail(f, "hevc streams are not supported")
	case o.AudioCodec != AudioCodecAuto && o.AudioCodec != AudioCodecAAC:
		v.fail(f, "a stream's audio is aac")
	case o.SampleRate != 0 && o.SampleRate != 48000:
		v.fail(f, "a stream's audio is 48 kHz")
	}
}

// ---------------------------------------------------------------------------
// StreamProtocol

// StreamProtocol is the manifest format of a stream.
type StreamProtocol uint8

// StreamProtocol values. The zero value is HLS.
const (
	StreamHLS  StreamProtocol = iota // HLS: a master playlist (.m3u8)
	StreamDASH                       // MPEG-DASH: an MPD (.mpd)
)

const numStreamProtocols = 2

func (p StreamProtocol) String() string {
	switch p {
	case StreamHLS:
		return "hls"
	case StreamDASH:
		return "dash"
	}
	return invalidEnum("StreamProtocol", uint8(p))
}

// ParseStreamProtocol parses a canonical StreamProtocol name.
func ParseStreamProtocol(s string) (StreamProtocol, error) {
	return parseEnum[StreamProtocol]("StreamProtocol", s, numStreamProtocols)
}

// MarshalText implements encoding.TextMarshaler.
func (p StreamProtocol) MarshalText() ([]byte, error) {
	return marshalEnum("StreamProtocol", p, numStreamProtocols)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (p *StreamProtocol) UnmarshalText(b []byte) error {
	return unmarshalEnum("StreamProtocol", p, b, numStreamProtocols)
}
