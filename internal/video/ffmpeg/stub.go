//go:build !ffmpeg || !cgo

package ffmpeg

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// errUnavailable is every result of a build without FFmpeg.
func errUnavailable() error {
	return errs.WithPublic(
		errs.New(errs.KindUnsupported, "ffmpeg: this build has no video support (build tag ffmpeg)"),
		"video sources are not supported by this image")
}

// Available reports whether this build decodes video: false without the
// ffmpeg build tag or cgo.
func Available() bool { return false }

// Version is FFmpeg's version, "" without it.
func Version() string { return "" }

// Probe reports errs.KindUnsupported: this build has no FFmpeg.
func Probe(context.Context, *vips.Buffer, video.Limits) (video.Meta, error) {
	return video.Meta{}, errUnavailable()
}

// Thumbnail reports errs.KindUnsupported: this build has no FFmpeg.
func Thumbnail(context.Context, *vips.Buffer, *video.FrameRequest) (*Frame, error) {
	return nil, errUnavailable()
}

// ReadStats returns zero counters.
func ReadStats() Stats { return Stats{} }
