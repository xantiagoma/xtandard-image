// Package ffmpeg decodes video frames and encodes video with FFmpeg (WP-74,
// WP-75, WP-76, ADR 0010 and its amendment): Probe and Thumbnail, Session
// (the probe's handle kept open; Frames decodes several timestamps on it,
// forward where it can), and Encoders/Encoder (RGB(A) frames → VP9, AV1,
// H.264 or HEVC, muxed into WebM or MP4 in C memory, no temporary file).
//
// It is the second and last package allowed to import "C" (ADR 0010 amends
// CLAUDE.md rule 3), and only when built with the `ffmpeg` build tag and
// cgo: the full image builds with `-tags ffmpeg` against the LGPL FFmpeg of
// docker/base (libavformat, libavcodec, libavutil, libswscale). Without the
// tag, or with CGO_ENABLED=0, the same API reports every video as
// errs.KindUnsupported and Available returns false, so the slim image
// answers 422 for video sources and never crashes.
//
// No FFmpeg or C type leaves the package. The source is read from the
// C memory of a vips.Buffer through a custom AVIOContext (no temporary
// file, no protocol), and the frame is written by libswscale straight into
// a new vips.Buffer, which vips.NewFromBuffer wraps without copying: C
// never holds Go memory, and Go never holds FFmpeg memory. Cancellation is
// an atomic flag the AVIO interrupt callback and the decode loop poll, set
// by a context.AfterFunc watcher; nothing calls back into Go.
//
// Every decoder runs with one thread, and the calls run on the caller's
// locked thread inside vips.Run, bounded by the worker semaphore like any
// image.
package ffmpeg
