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

// Session is never opened without FFmpeg.
type Session struct{}

// Open reports errs.KindUnsupported: this build has no FFmpeg.
func Open(context.Context, *vips.Buffer, video.Limits) (*Session, error) {
	return nil, errUnavailable()
}

// Meta is empty.
func (*Session) Meta() video.Meta { return video.Meta{} }

// Reads is false.
func (*Session) Reads(*vips.Buffer) bool { return false }

// Close does nothing.
func (*Session) Close() {}

// Thumbnail reports errs.KindUnsupported.
func (*Session) Thumbnail(context.Context, *video.FrameRequest) (*Frame, error) {
	return nil, errUnavailable()
}

// Frames reports errs.KindUnsupported.
func (*Session) Frames(context.Context, *video.FramesRequest) ([]*Frame, error) {
	return nil, errUnavailable()
}

// Encoders is empty: this build writes no video.
func Encoders() video.EncoderSet { return video.EncoderSet{} }

// Encoder is never opened without FFmpeg.
type Encoder struct{}

// NewEncoder reports errs.KindUnsupported: this build has no FFmpeg.
func NewEncoder(context.Context, *video.EncodeParams) (*Encoder, error) {
	return nil, errs.WithPublic(
		errs.New(errs.KindUnsupported, "ffmpeg: this build has no video support (build tag ffmpeg)"),
		"video output is not supported by this image")
}

// Frame reports errs.KindUnsupported.
func (*Encoder) Frame(context.Context, []byte, int64, int64) error { return errUnavailable() }

// Finish reports errs.KindUnsupported.
func (*Encoder) Finish(context.Context) (*vips.Buffer, error) { return nil, errUnavailable() }

// Close does nothing.
func (*Encoder) Close() {}

// TranscodeCaps is empty: this build transcodes nothing.
func TranscodeCaps() video.TranscodeCaps { return video.TranscodeCaps{} }

// Transcoder is never opened without FFmpeg.
type Transcoder struct{}

// OpenTranscode reports errs.KindUnsupported: this build has no FFmpeg.
func OpenTranscode(context.Context, *vips.Buffer, video.Limits, int) (*Transcoder, error) {
	return nil, errs.WithPublic(
		errs.New(errs.KindUnsupported, "ffmpeg: this build has no video support (build tag ffmpeg)"),
		"video transformations are not supported by this image")
}

// Input is empty.
func (*Transcoder) Input() video.TranscodeInput { return video.TranscodeInput{} }

// Run reports errs.KindUnsupported.
func (*Transcoder) Run(context.Context, *video.TranscodePlan) (*Output, error) {
	return nil, errUnavailable()
}

// Close does nothing.
func (*Transcoder) Close() {}
