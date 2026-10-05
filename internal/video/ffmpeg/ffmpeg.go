//go:build ffmpeg && cgo

package ffmpeg

/*
#cgo pkg-config: libavformat libavcodec libavutil libswscale libavfilter
#cgo LDFLAGS: -lm
#cgo noescape xi_ff_open
#cgo nocallback xi_ff_new
#cgo nocallback xi_ff_open
#cgo noescape xi_ff_info
#cgo nocallback xi_ff_info
#cgo noescape xi_ff_stream
#cgo nocallback xi_ff_stream
#cgo noescape xi_ff_tag
#cgo nocallback xi_ff_tag
#cgo noescape xi_ff_decode_at
#cgo nocallback xi_ff_decode_at
#cgo noescape xi_ff_convert
#cgo nocallback xi_ff_convert
#cgo nocallback xi_ff_close
#cgo nocallback xi_ff_abort
#cgo nocallback xi_ff_aborted
#cgo nocallback xi_ff_keep
#cgo noescape xi_ff_keyframe_before
#cgo nocallback xi_ff_keyframe_before
#cgo nocallback xi_ff_live
#cgo nocallback xi_ff_live_encoders
#cgo nocallback xi_ff_heap_in_use
#cgo nocallback xi_ff_version
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"context"
	"math"
	"sync"
	"time"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// maxSide bounds the size a frame is scaled to, whatever the request.
const maxSide = 16384

// Available reports whether this build decodes video: true with the
// ffmpeg build tag and cgo.
func Available() bool { return true }

// Version is FFmpeg's version string ("8.1.3").
func Version() string { return C.GoString(C.xi_ff_version()) }

// ReadStats returns the shim's counters (open handles, glibc heap in use).
func ReadStats() Stats {
	tx, out := transcodeStats()
	return Stats{
		Handles:     int(C.xi_ff_live()),
		Encoders:    int(C.xi_ff_live_encoders()),
		Transcoders: tx,
		Outputs:     out,
		HeapInUse:   int64(C.xi_ff_heap_in_use()),
	}
}

// Probe reads the container and stream headers of src (FFmpeg decodes at
// most the few frames avformat_find_stream_info needs, single-threaded and
// under the pixel limit) and checks lim: more bytes, a longer duration or
// larger frames than allowed fail with errs.KindLimitExceeded. An
// unrecognised container, a file without a video stream or a codec this
// build cannot decode is errs.KindUnsupported; an unreadable file
// errs.KindCorrupt; a done ctx its kind.
func Probe(ctx context.Context, src *vips.Buffer, lim video.Limits) (video.Meta, error) {
	d, err := open(ctx, src, lim)
	if err != nil {
		return video.Meta{}, err
	}
	defer d.close()
	return d.meta, nil
}

// Thumbnail decodes the frame req selects from src and scales it to the
// requested size (FrameRequest.Width × Height, the display size when 0):
// the frame at req.Second (video.Seek; the keyframe at or before it with
// req.Keyframes), or, when req.Second is nil, the best-scoring of
// req.Samples keyframes across the first req.Span of the video. The limits
// of req are checked like Probe does. The caller releases the frame.
func Thumbnail(ctx context.Context, src *vips.Buffer, req *video.FrameRequest) (*Frame, error) {
	d, err := open(ctx, src, req.Limits)
	if err != nil {
		return nil, err
	}
	defer d.close()
	return d.thumbnail(ctx, req)
}

// Session is an open video: the probe's FFmpeg handle, kept so the frames
// a request needs are decoded without opening and probing the file again.
// It retains its source until Close. It is not safe for concurrent use:
// one call at a time, each cancelled by its own ctx.
type Session struct {
	d *decoder
}

// Open probes src like Probe and keeps the handle open. The caller closes
// the session.
func Open(ctx context.Context, src *vips.Buffer, lim video.Limits) (*Session, error) {
	d, err := open(ctx, src, lim)
	if err != nil {
		return nil, err
	}
	d.disarm() // ctx ends with this call; later calls arm their own
	return &Session{d: d}, nil
}

// Meta is the probed metadata.
func (s *Session) Meta() video.Meta { return s.d.meta }

// Reads reports whether the session reads src's memory: a buffer over the
// same C memory (the server's and the executor's wrappers of one fetched
// source), so its handle can decode src.
func (s *Session) Reads(src *vips.Buffer) bool {
	if s == nil || s.d == nil || s.d.h == nil || src == nil || src.Len() == 0 || src.Len() != s.d.src.Len() {
		return false
	}
	return &src.Bytes()[0] == &s.d.src.Bytes()[0]
}

// Close frees the handle and releases the source. It is nil-safe and
// idempotent.
func (s *Session) Close() {
	if s == nil || s.d == nil {
		return
	}
	s.d.close()
}

// Thumbnail is the package's Thumbnail on the open handle; req.Limits are
// checked against the probed metadata.
func (s *Session) Thumbnail(ctx context.Context, req *video.FrameRequest) (*Frame, error) {
	if err := s.usable(ctx, req.Limits); err != nil {
		return nil, err
	}
	s.d.arm(ctx)
	defer s.d.disarm()
	return s.d.thumbnail(ctx, req)
}

// Frames decodes the frames at req.Times on the open handle, every one
// scaled to req.Width × req.Height (the display size when 0), in order.
// Timestamps are clamped like Seek, and consecutive ones without a
// keyframe between them are decoded forward instead of seeking back. The
// caller releases every frame; on error none is returned.
func (s *Session) Frames(ctx context.Context, req *video.FramesRequest) ([]*Frame, error) {
	if err := s.usable(ctx, req.Limits); err != nil {
		return nil, err
	}
	s.d.arm(ctx)
	defer s.d.disarm()
	d := s.d
	w, h, bands, err := d.size(req.Width, req.Height)
	if err != nil {
		return nil, err
	}
	out := make([]*Frame, 0, len(req.Times))
	fail := func(err error) ([]*Frame, error) {
		for _, f := range out {
			f.Release()
		}
		return nil, err
	}
	for _, t := range req.Times {
		f := &Frame{Width: w, Height: h, Bands: bands}
		if f.Time, f.Keyframe, err = d.decodeAt(ctx, video.Seek(t, d.meta.Duration), req.Keyframes, nil); err != nil {
			return fail(err)
		}
		if f.Pixels, err = d.convert(ctx, false, w, h, bands); err != nil {
			return fail(err)
		}
		out = append(out, f)
	}
	return out, nil
}

// usable checks that the session is open, ctx is not done and the source is
// within lim.
func (s *Session) usable(ctx context.Context, lim video.Limits) error {
	if s == nil || s.d == nil || s.d.h == nil {
		return errs.New(errs.KindInternal, "ffmpeg: closed session")
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if lim.MaxBytes > 0 && int64(s.d.src.Len()) > lim.MaxBytes {
		return errs.Newf(errs.KindLimitExceeded, "ffmpeg: video of %d bytes exceeds the limit of %d",
			s.d.src.Len(), lim.MaxBytes)
	}
	return checkLimits(&s.d.meta, lim)
}

// size is the frame size and band count of a request (the display size
// when w or h is 0).
func (d *decoder) size(w, h int) (int, int, int, error) {
	if w <= 0 || h <= 0 {
		w, h = d.meta.DisplayWidth, d.meta.DisplayHeight
	}
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide {
		return 0, 0, 0, errs.Newf(errs.KindLimitExceeded, "ffmpeg: frame size %dx%d out of range", w, h)
	}
	bands := 3
	if d.meta.Alpha {
		bands = 4
	}
	return w, h, bands, nil
}

// thumbnail decodes and converts the frame req selects (Thumbnail).
func (d *decoder) thumbnail(ctx context.Context, req *video.FrameRequest) (*Frame, error) {
	w, h, bands, err := d.size(req.Width, req.Height)
	if err != nil {
		return nil, err
	}
	f := &Frame{Width: w, Height: h, Bands: bands}
	fromBest := false
	if req.Second != nil {
		at := video.Seek(*req.Second, d.meta.Duration)
		if f.Time, f.Keyframe, err = d.decodeAt(ctx, at, req.Keyframes, nil); err != nil {
			return nil, err
		}
	} else {
		if err := d.best(ctx, req, f); err != nil {
			return nil, err
		}
		fromBest = true
	}
	if f.Pixels, err = d.convert(ctx, fromBest, w, h, bands); err != nil {
		return nil, err
	}
	return f, nil
}

// convert scales the decoded frame (the best slot with best) into a new
// buffer of w × h × bands packed pixels.
func (d *decoder) convert(ctx context.Context, best bool, w, h, bands int) (*vips.Buffer, error) {
	// A few bytes of slack: libswscale's SIMD paths may store a little past
	// the last pixel of a row.
	buf, err := vips.NewBuffer(w*h*bands + 64)
	if err != nil {
		return nil, err
	}
	var cerr [256]C.char
	rc := C.xi_ff_convert(d.h, cbool(best), (*C.uint8_t)(unsafe.Pointer(&buf.Bytes()[0])),
		C.int(w), C.int(h), C.int(bands), &cerr[0], C.size_t(len(cerr)))
	if err := d.check(ctx, rc, &cerr[0], "convert"); err != nil {
		buf.Release()
		return nil, err
	}
	return buf, nil
}

// decoder is one open FFmpeg handle over a source buffer. It is not safe
// for concurrent use (the abort watcher only sets an atomic flag).
type decoder struct {
	h    *C.XiFF
	src  *vips.Buffer
	meta video.Meta
	stop func() // disarms the current abort watcher; nil when none
}

// open retains src, opens it and checks lim. ctx's watcher stays armed
// until close or disarm. The caller closes the decoder.
func open(ctx context.Context, src *vips.Buffer, lim video.Limits) (*decoder, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if src == nil || src.Len() == 0 {
		return nil, errs.New(errs.KindCorrupt, "ffmpeg: empty source")
	}
	if lim.MaxBytes > 0 && int64(src.Len()) > lim.MaxBytes {
		return nil, errs.Newf(errs.KindLimitExceeded, "ffmpeg: video of %d bytes exceeds the limit of %d",
			src.Len(), lim.MaxBytes)
	}
	src.Retain()
	data := src.Bytes() // C memory owned by libvips: FFmpeg may keep reading it
	h := C.xi_ff_new((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), C.int64_t(lim.MaxPixels))
	if h == nil {
		src.Release()
		return nil, errs.New(errs.KindInternal, "ffmpeg: out of memory")
	}
	d := &decoder{h: h, src: src}
	d.arm(ctx)
	var cerr [256]C.char
	rc := C.xi_ff_open(h, &cerr[0], C.size_t(len(cerr)))
	if err := d.check(ctx, rc, &cerr[0], "open"); err != nil {
		d.close()
		return nil, err
	}
	d.meta = d.readMeta()
	if err := checkLimits(&d.meta, lim); err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

// arm clears the abort flag and aborts the handle's calls when ctx is done
// (at once when it already is), replacing the previous watcher.
func (d *decoder) arm(ctx context.Context) {
	d.disarm()
	d.setAbort(false)
	d.stop = watch(ctx, d.setAbort)
}

// disarm stops the current watcher and waits for an abort in progress.
func (d *decoder) disarm() {
	if d.stop != nil {
		d.stop()
		d.stop = nil
	}
}

// watch calls abort(true) when ctx is done. The returned stop disarms the
// watcher and waits for an abort in progress: abort is never called after
// stop returns.
func watch(ctx context.Context, abort func(bool)) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	var mu sync.Mutex
	armed := true
	stopWatch := context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		if armed {
			abort(true)
		}
	})
	return func() {
		stopWatch()
		mu.Lock()
		defer mu.Unlock()
		armed = false
	}
}

// setAbort sets or clears the handle's abort flag: the running call and
// every later one fail with XI_FF_ERR_ABORTED until it is cleared.
func (d *decoder) setAbort(on bool) { C.xi_ff_abort(d.h, cbool(on)) }

// aborted reports the handle's abort flag.
func (d *decoder) aborted() bool { return C.xi_ff_aborted(d.h) != 0 }

func (d *decoder) close() {
	if d.h == nil {
		return
	}
	d.disarm()
	C.xi_ff_close(d.h)
	d.h = nil
	d.src.Release()
}

func (d *decoder) readMeta() video.Meta {
	var info C.XiFFInfo
	C.xi_ff_info(d.h, &info)
	m := video.Meta{
		Container:     C.GoString(&info.container[0]),
		Duration:      max(float64(info.duration), 0),
		BitRate:       int64(info.bit_rate),
		VideoStream:   int(info.video_index),
		Codec:         C.GoString(&info.codec[0]),
		Width:         int(info.width),
		Height:        int(info.height),
		DisplayWidth:  int(info.display_width),
		DisplayHeight: int(info.display_height),
		FPS:           float64(info.fps),
		Frames:        int64(info.frames),
		Rotation:      int(info.rotation),
		HFlip:         info.hflip != 0,
		Alpha:         info.alpha != 0,
	}
	if m.Frames <= 0 && m.Duration > 0 && m.FPS > 0 {
		m.Frames = int64(math.Round(m.Duration * m.FPS))
	}
	for i := range int(info.nb_streams) {
		var s C.XiFFStream
		if C.xi_ff_stream(d.h, C.int(i), &s) == 0 {
			break
		}
		m.Streams = append(m.Streams, video.Stream{
			Type:       mediaType(int(s._type)),
			Codec:      C.GoString(&s.codec[0]),
			BitRate:    int64(s.bit_rate),
			FPS:        float64(s.fps),
			Duration:   max(float64(s.duration), 0),
			Language:   C.GoString(&s.language[0]),
			Width:      int(s.width),
			Height:     int(s.height),
			Rotation:   int(s.rotation),
			SampleRate: int(s.sample_rate),
			Layout:     C.GoString(&s.layout[0]),
			Channels:   int(s.channels),
		})
	}
	for i := 0; ; i++ {
		var k, v *C.char
		if C.xi_ff_tag(d.h, C.int(i), &k, &v) == 0 {
			break
		}
		if m.Tags == nil {
			m.Tags = make(map[string]string)
		}
		m.Tags[C.GoString(k)] = C.GoString(v)
	}
	return m
}

// mediaType names an AVMediaType.
func mediaType(t int) string {
	switch t {
	case 0:
		return "video"
	case 1:
		return "audio"
	case 2:
		return "data"
	case 3:
		return "subtitle"
	case 4:
		return "attachment"
	default:
		return "unknown"
	}
}

// checkLimits enforces the duration and pixel limits on the probed meta
// (the byte limit was checked before opening).
func checkLimits(m *video.Meta, lim video.Limits) error {
	if lim.MaxDuration > 0 && m.Duration > lim.MaxDuration.Seconds() {
		return errs.Newf(errs.KindLimitExceeded, "ffmpeg: video of %s exceeds the duration limit of %s",
			time.Duration(m.Duration*float64(time.Second)), lim.MaxDuration)
	}
	if lim.MaxPixels > 0 {
		px := max(int64(m.Width)*int64(m.Height), int64(m.DisplayWidth)*int64(m.DisplayHeight))
		if px > lim.MaxPixels {
			return errs.Newf(errs.KindLimitExceeded, "ffmpeg: %dx%d video frames exceed the limit of %d pixels",
				m.Width, m.Height, lim.MaxPixels)
		}
	}
	return nil
}

// decodeAt decodes the frame at seconds (the keyframe at or before it with
// keyframe) into the handle, and its luma histogram when hist is not nil.
func (d *decoder) decodeAt(ctx context.Context, seconds float64, keyframe bool, hist *video.Histogram) (at float64, key bool, err error) {
	if err := ctxErr(ctx); err != nil {
		return 0, false, err
	}
	var got C.double
	var isKey C.int
	var cerr [256]C.char
	var hp *C.uint32_t
	if hist != nil {
		hp = (*C.uint32_t)(unsafe.Pointer(&hist[0]))
	}
	rc := C.xi_ff_decode_at(d.h, C.double(seconds), cbool(keyframe), &got, &isKey, hp,
		&cerr[0], C.size_t(len(cerr)))
	if err := d.check(ctx, rc, &cerr[0], "decode"); err != nil {
		return 0, false, err
	}
	return float64(got), isKey != 0, nil
}

// best runs best-frame selection: it decodes the keyframe at or before
// every sample point, scores its luma histogram and keeps the first
// best-scoring one in the handle's best slot. Samples that land on a
// keyframe already scored are skipped, before decoding when the demuxer's
// index tells which keyframe a point falls on.
func (d *decoder) best(ctx context.Context, req *video.FrameRequest, f *Frame) error {
	points := video.SamplePoints(d.meta.Duration, req.Span, req.Samples)
	bestScore, lastTime, lastKey := -1.0, math.NaN(), math.NaN()
	var firstErr error
	for _, p := range points {
		if k, ok := d.keyframeBefore(p); ok {
			if k == lastKey {
				continue
			}
			lastKey = k
		}
		var hist video.Histogram
		at, key, err := d.decodeAt(ctx, p, true, &hist)
		if err != nil {
			if k := errs.KindOf(err); k == errs.KindCanceled || k == errs.KindTimeout || k == errs.KindLimitExceeded {
				return err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if at == lastTime {
			continue
		}
		lastTime = at
		f.Sampled++
		if s := video.Score(&hist); s > bestScore {
			bestScore = s
			f.Time, f.Keyframe, f.Score = at, key, s
			C.xi_ff_keep(d.h)
		}
	}
	if bestScore < 0 {
		if firstErr != nil {
			return firstErr
		}
		return errs.New(errs.KindCorrupt, "ffmpeg: no frame decoded")
	}
	return nil
}

// keyframeBefore is the index timestamp of the keyframe the demuxer's
// index has at or before seconds (decode order: it may be negative); ok is
// false when the index does not know.
func (d *decoder) keyframeBefore(seconds float64) (at float64, ok bool) {
	var c C.double
	if C.xi_ff_keyframe_before(d.h, C.double(seconds), &c) == 0 {
		return 0, false
	}
	return float64(c), true
}

// check turns a shim result code into an error: an abort caused by ctx
// reports ctx's kind.
func (d *decoder) check(ctx context.Context, rc C.int, cerr *C.char, op string) error {
	if rc == C.XI_FF_OK {
		return nil
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return codeError(rc, C.GoString(cerr), op)
}

// codeError classifies a shim result code; msg is FFmpeg's text, kept for
// logs only.
func codeError(rc C.int, msg, op string) error {
	var k errs.Kind
	switch rc {
	case C.XI_FF_ERR_UNSUPPORTED:
		return errs.WithPublic(errs.Newf(errs.KindUnsupported, "ffmpeg: %s: %s", op, msg),
			"unsupported video format or codec")
	case C.XI_FF_ERR_CORRUPT, C.XI_FF_ERR_NOFRAME:
		k = errs.KindCorrupt
	case C.XI_FF_ERR_LIMIT:
		k = errs.KindLimitExceeded
	case C.XI_FF_ERR_ABORTED:
		k = errs.KindCanceled
	default:
		k = errs.KindInternal
	}
	return errs.Newf(k, "ffmpeg: %s: %s", op, msg)
}

// ctxErr classifies a done context.
func ctxErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errs.Wrapf(errs.KindOf(err), context.Cause(ctx), "ffmpeg")
	}
	return nil
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
