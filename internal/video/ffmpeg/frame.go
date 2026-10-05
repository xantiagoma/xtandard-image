package ffmpeg

import (
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Frame is a decoded thumbnail frame: packed 8-bit RGB (Bands 3) or RGBA
// (Bands 4) pixels in C memory, Width × Height × Bands bytes (the buffer
// may be longer), in the stored orientation (Meta.Orientation says how to
// display it). The caller owns Pixels and releases it; vips.NewFromBuffer
// wraps it without copying.
type Frame struct {
	Pixels        *vips.Buffer
	Width, Height int
	Bands         int
	// Time is the timestamp of the frame in seconds from the stream start.
	Time float64
	// Keyframe reports that the frame is a keyframe.
	Keyframe bool
	// Score is the best-frame score (video.Score) of the chosen frame; 0
	// when a timestamp was requested.
	Score float64
	// Sampled is the number of frames best-frame selection compared; 0
	// when a timestamp was requested.
	Sampled int
}

// Release frees the pixels. It is nil-safe and idempotent.
func (f *Frame) Release() {
	if f == nil || f.Pixels == nil {
		return
	}
	f.Pixels.Release()
	f.Pixels = nil
}

// Stats are the shim's counters, for leak tests.
type Stats struct {
	// Handles is the number of open decoder handles (sessions included).
	Handles int
	// Encoders is the number of open video encoders.
	Encoders int
	// Transcoders is the number of open transcoders, and Outputs the
	// transcoded files not released yet.
	Transcoders int
	Outputs     int
	// HeapInUse is glibc's in-use heap in bytes (mallinfo2), 0 where it is
	// not available.
	HeapInUse int64
}
