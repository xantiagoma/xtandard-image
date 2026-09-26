// Package video holds the cgo-free half of video thumbnails (WP-74,
// docs/FORMATS.md §1.5): the metadata a probe reports (Meta, Stream), the
// frame request (FrameRequest) and its limits, the timestamp rules of
// imgproxy's video_thumbnail_second (Seek), the sample points and the
// score of best-frame selection (SamplePoints, Score) and the display
// matrix → EXIF orientation mapping (Orientation); the layout of sprites
// and animations of frames (Layout, WP-75); and the video output contract
// (EncoderSet.Resolve, CRF, FrameDelay, WP-76).
//
// Decoding and encoding live in internal/video/ffmpeg, which is built
// against FFmpeg only with the `ffmpeg` build tag (the full and gpl
// images); without it that
// package reports every video as unsupported. This package builds and
// tests with CGO_ENABLED=0.
package video

import (
	"math"
	"time"
)

// Limits bound what a video source may declare. They are checked from the
// container and stream headers, before any frame is decoded.
type Limits struct {
	// MaxBytes caps the size of the source (XI_VIDEO_MAX_BYTES); 0 is
	// unlimited (XI_LIMITS_MAX_SRC_BYTES still applies at fetch).
	MaxBytes int64
	// MaxDuration caps the container duration (XI_VIDEO_MAX_DURATION); 0
	// is unlimited.
	MaxDuration time.Duration
	// MaxPixels caps the width × height of the video stream
	// (XI_VIDEO_MAX_PIXELS); 0 is unlimited. The decoder refuses larger
	// frames too (AVCodecContext.max_pixels).
	MaxPixels int64
}

// Meta describes a video source: the container, every stream, and the
// video stream thumbnails are taken from (the "best" one by FFmpeg's
// av_find_best_stream).
type Meta struct {
	// Container is the demuxer that read the file: "mov" (MP4, MOV, 3GP),
	// "matroska" (MKV, WebM) or "avi".
	Container string
	// Duration is the container duration in seconds; 0 when unknown.
	Duration float64
	// BitRate is the container bit rate in bits per second; 0 when unknown.
	BitRate int64
	// Tags are the container metadata tags (major_brand, creation_time,
	// encoder, title, ...), as FFmpeg reads them.
	Tags map[string]string
	// Streams lists every stream of the container, in file order.
	Streams []Stream
	// VideoStream is the index in Streams of the stream thumbnails are
	// decoded from.
	VideoStream int
	// Codec is the codec of that stream ("h264", "hevc", "vp9", "av1",
	// ...).
	Codec string
	// Width and Height are the coded size of a frame, as stored.
	Width, Height int
	// DisplayWidth and DisplayHeight are the size a frame is shown at
	// before rotation: the coded size with the sample aspect ratio applied
	// (anamorphic video). Thumbnails are decoded at this size.
	DisplayWidth, DisplayHeight int
	// FPS is the average frame rate; 0 when unknown.
	FPS float64
	// Frames is the number of frames of the stream, from the container or
	// estimated from its duration and frame rate; 0 when unknown.
	Frames int64
	// Rotation is the clockwise rotation in degrees (0, 90, 180 or 270)
	// the display matrix asks for, after HFlip.
	Rotation int
	// HFlip reports a mirrored display matrix.
	HFlip bool
	// Alpha reports a pixel format with an alpha channel (for example
	// PNG or rawvideo RGBA; VP8/VP9 alpha side data is not decoded).
	Alpha bool
}

// Orientation returns the EXIF orientation (1–8) equivalent to m's display
// matrix, so the pipeline rotates a thumbnail like a photo.
func (m *Meta) Orientation() int { return Orientation(m.Rotation, m.HFlip) }

// Stream describes one stream of a container, in imgproxy's /info
// video_streams shape.
type Stream struct {
	// Type is "video", "audio", "subtitle", "data" or "attachment".
	Type string
	// Codec is FFmpeg's codec name ("h264", "aac", "subrip", ...).
	Codec string
	// BitRate in bits per second; 0 when unknown.
	BitRate int64
	// FPS is the average frame rate of a video stream; 0 otherwise.
	FPS float64
	// Duration in seconds; 0 when unknown.
	Duration float64
	// Language is the ISO 639-2 language tag; "" when absent or "und".
	Language string
	// Width and Height are the coded size of a video stream.
	Width, Height int
	// Rotation is the clockwise display rotation of a video stream.
	Rotation int
	// SampleRate and Layout describe an audio stream (Hz, "mono",
	// "stereo", "5.1(side)", ...).
	SampleRate int
	Layout     string
}

// FrameRequest selects the frame a thumbnail is made of and the size it is
// decoded at.
type FrameRequest struct {
	// Second is the timestamp (imgproxy video_thumbnail_second): negative
	// counts from the end, and it is clamped to the stream (Seek). Nil
	// selects the best frame among Samples keyframes (best-frame
	// selection, SamplePoints and Score).
	Second *float64
	// Keyframes decodes the keyframe at or before Second instead of the
	// exact frame (imgproxy video_thumbnail_keyframes): faster, less
	// precise. Best-frame selection always uses keyframes.
	Keyframes bool
	// Samples is the number of keyframes best-frame selection compares
	// (XI_VIDEO_THUMBNAIL_SAMPLES); ≤ 0 means DefaultSamples.
	Samples int
	// Span is how much of the start of the video best-frame selection
	// samples (XI_VIDEO_THUMBNAIL_SPAN); ≤ 0 means DefaultSpan.
	Span time.Duration
	// Width and Height are the size to scale the frame to, as stored
	// (before rotation); 0 decodes at Meta.DisplayWidth ×
	// Meta.DisplayHeight.
	Width, Height int
	// Limits are checked again before decoding.
	Limits Limits
}

// Best-frame defaults (XI_VIDEO_THUMBNAIL_SAMPLES, XI_VIDEO_THUMBNAIL_SPAN).
const (
	DefaultSamples = 5
	DefaultSpan    = 10 * time.Second
	// MaxSamples bounds XI_VIDEO_THUMBNAIL_SAMPLES: each sample is a seek
	// and a keyframe decode.
	MaxSamples = 32
)

// Seek returns the timestamp in seconds that second asks for in a video of
// duration seconds, following imgproxy's video_thumbnail_second: a
// negative second counts back from the end, and the result is clamped to
// [0, duration). A duration ≤ 0 (unknown) keeps a non-negative second and
// maps a negative one to 0. The frame nearest to the end is taken when
// the timestamp is past the last frame.
func Seek(second, duration float64) float64 {
	if math.IsNaN(second) || math.IsInf(second, 0) {
		return 0
	}
	if !(duration > 0) || math.IsInf(duration, 0) {
		return max(second, 0)
	}
	if second < 0 {
		second += duration
	}
	return min(max(second, 0), duration)
}

// SamplePoints returns the n timestamps best-frame selection decodes: the
// interior points (i+1)/(n+1) of the first span seconds of a video of
// duration seconds (the whole video when it is shorter or its duration is
// unknown and span is used as is). The first and last frames, often black
// or a fade, are never sampled. The points are in increasing order.
func SamplePoints(duration float64, span time.Duration, n int) []float64 {
	if n <= 0 {
		n = DefaultSamples
	}
	n = min(n, MaxSamples)
	s := span.Seconds()
	if s <= 0 {
		s = DefaultSpan.Seconds()
	}
	if duration > 0 && !math.IsInf(duration, 0) {
		s = min(s, duration)
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = s * float64(i+1) / float64(n+1)
	}
	return out
}

// Histogram is a 256-bin histogram of the luma of a frame.
type Histogram [256]uint32

// Luma thresholds of Score: a pixel darker than darkLevel or brighter than
// brightLevel counts as "extreme", and a frame whose mean is outside
// [darkMean, brightMean] is a near-black or near-white frame.
const (
	darkLevel   = 24
	brightLevel = 232
	darkMean    = 32
	brightMean  = 224
)

// Score rates how good a frame is as a thumbnail from its luma histogram:
// the Shannon entropy of the histogram (bits, 0–8) plus the standard
// deviation of the luma over 64 (0–2), times (1 − share of near-black and
// near-white pixels)², and quartered for a frame whose mean luma is
// near black or near white (fades, title cards, flashes). A flat frame
// scores 0; busy, well-exposed frames score highest. It is deterministic,
// and best-frame selection keeps the first of equal scores.
func Score(h *Histogram) float64 {
	var n, sum, sumSq, extreme float64
	for v, c := range h {
		f := float64(c)
		n += f
		sum += f * float64(v)
		sumSq += f * float64(v) * float64(v)
		if v < darkLevel || v > brightLevel {
			extreme += f
		}
	}
	if n == 0 {
		return 0
	}
	var entropy float64
	for _, c := range h {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		entropy -= p * math.Log2(p)
	}
	mean := sum / n
	sd := math.Sqrt(max(sumSq/n-mean*mean, 0))
	keep := 1 - extreme/n
	score := (entropy + sd/64) * keep * keep
	if mean < darkMean || mean > brightMean {
		score /= 4
	}
	return score
}

// Orientation maps a display rotation (clockwise degrees, any multiple of
// 90; others round to the nearest) applied after an optional horizontal
// mirror to the EXIF orientation that displays the stored frame the same
// way: 90° is 6, 180° is 3, 270° is 8; mirrored, 0° is 2, 90° is 7, 180°
// is 4 and 270° is 5.
func Orientation(rotation int, hflip bool) int {
	r := ((int(math.Round(float64(rotation)/90)) % 4) + 4) % 4
	if hflip {
		return [4]int{2, 7, 4, 5}[r]
	}
	return [4]int{1, 6, 3, 8}[r]
}

// Container names Meta.Container reports for the demuxers the full image
// has.
const (
	ContainerMOV      = "mov"
	ContainerMatroska = "matroska"
	ContainerAVI      = "avi"
)

// Settings are the configured video defaults (XI_VIDEO_*), resolved
// against the build: Enabled is false on an image without FFmpeg.
type Settings struct {
	// Enabled decodes video sources; false refuses them with
	// errs.KindUnsupported.
	Enabled bool
	Limits  Limits
	// Second is the frame taken when a request names none; nil selects
	// the best frame.
	Second *float64
	// Keyframes takes the keyframe at or before the timestamp by default.
	Keyframes bool
	// Samples and Span configure best-frame selection.
	Samples int
	Span    time.Duration
	// MaxCompositeFrames caps the frames a sprite (video_thumbnail_tile)
	// or an animation (video_thumbnail_animation) decodes
	// (XI_VIDEO_MAX_ANIMATION_FRAMES); ≤ 0 is DefaultMaxCompositeFrames.
	MaxCompositeFrames int
}

// Request returns the FrameRequest for a request's video options (nil
// takes the setting) at width × height (0: the display size).
func (s *Settings) Request(second *float64, keyframes *bool, width, height int) FrameRequest {
	r := FrameRequest{
		Second:    s.Second,
		Keyframes: s.Keyframes,
		Samples:   s.Samples,
		Span:      s.Span,
		Width:     width,
		Height:    height,
		Limits:    s.Limits,
	}
	if second != nil {
		r.Second = second
	}
	if keyframes != nil {
		r.Keyframes = *keyframes
	}
	return r
}

// FramesRequest selects several frames of one video, decoded on one open
// handle (sprites and animations, WP-75).
type FramesRequest struct {
	// Times are the timestamps in seconds, in increasing order; each is
	// clamped to the stream like Seek. Consecutive timestamps without a
	// keyframe between them are decoded forward, without seeking.
	Times []float64
	// Keyframes decodes the keyframe at or before each timestamp.
	Keyframes bool
	// Width and Height are the size every frame is scaled to, as stored
	// (before rotation); 0 decodes at the display size.
	Width, Height int
	// Limits are checked again before decoding.
	Limits Limits
}
