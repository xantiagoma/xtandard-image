//go:build ffmpeg && cgo

package ffmpeg

/*
#cgo nocallback xi_tx_caps
#cgo nocallback xi_tx_new
#cgo nocallback xi_tx_new_fd
#cgo nocallback xi_tx_spilled
#cgo noescape xi_tx_spilled
#cgo noescape xi_tx_open
#cgo nocallback xi_tx_open
#cgo noescape xi_tx_run
#cgo nocallback xi_tx_run
#cgo noescape xi_tx_take
#cgo nocallback xi_tx_take
#cgo nocallback xi_tx_out_free
#cgo nocallback xi_tx_abort
#cgo nocallback xi_tx_free
#cgo nocallback xi_tx_live
#cgo nocallback xi_tx_live_outputs
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"context"
	"math"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TranscodeCaps probes the linked FFmpeg for what video transformations
// can write: the video encoders (Encoders), the audio encoders, the
// muxers and the filters. The application calls it once at startup.
func TranscodeCaps() video.TranscodeCaps {
	m := C.xi_tx_caps()
	return video.TranscodeCaps{
		Video:        Encoders(),
		AAC:          m&C.XI_TX_CAP_AAC != 0,
		Opus:         m&C.XI_TX_CAP_OPUS != 0,
		MP3:          m&C.XI_TX_CAP_MP3 != 0,
		MuxMP4:       m&C.XI_TX_CAP_MUX_MP4 != 0,
		MuxWebM:      m&C.XI_TX_CAP_MUX_WEBM != 0,
		MuxOgg:       m&C.XI_TX_CAP_MUX_OGG != 0,
		MuxMP3:       m&C.XI_TX_CAP_MUX_MP3 != 0,
		VideoFilters: m&C.XI_TX_CAP_VFILTERS != 0,
		AudioFilters: m&C.XI_TX_CAP_AFILTERS != 0,
		Overlays:     m&C.XI_TX_CAP_OVERLAYS != 0,
		Timecode:     m&C.XI_TX_CAP_DRAWTEXT != 0,
		FLAC:         m&C.XI_TX_CAP_FLAC != 0,
		PCM:          m&C.XI_TX_CAP_PCM != 0,
		MuxFLAC:      m&C.XI_TX_CAP_MUX_FLAC != 0,
		MuxWAV:       m&C.XI_TX_CAP_MUX_WAV != 0,
		AudioEffects: m&C.XI_TX_CAP_AEFFECTS != 0,
		VideoFade:    m&C.XI_TX_CAP_VFADE != 0,
		Waveform:     m&C.XI_TX_CAP_WAVEFORM != 0,
		MuxMPEGTS:    m&C.XI_TX_CAP_MUX_MPEGTS != 0,
	}
}

// Transcoder is an opened source for one video transformation: probed by
// OpenTranscode, run once by Run. It retains its source until Close and is
// not safe for concurrent use.
type Transcoder struct {
	h   *C.XiTx
	src interface{ Release() }
	in  video.TranscodeInput
}

// OpenTranscode demuxes src, probes it (single-threaded, under the pixel
// limit) and opens its decoders with threads threads; lim is checked like
// Probe does. The caller closes the transcoder.
func OpenTranscode(ctx context.Context, src *vips.Buffer, lim video.Limits, threads int) (*Transcoder, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if src == nil || src.Len() == 0 {
		return nil, errs.New(errs.KindCorrupt, "ffmpeg: empty source")
	}
	if lim.MaxBytes > 0 && int64(src.Len()) > lim.MaxBytes {
		return nil, errs.WithPublic(errs.Newf(errs.KindLimitExceeded, "ffmpeg: video of %d bytes exceeds the limit of %d",
			src.Len(), lim.MaxBytes), "the video is larger than the size limit")
	}
	src.Retain()
	data := src.Bytes() // C memory owned by libvips: FFmpeg reads it until Close
	h := C.xi_tx_new((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), C.int64_t(lim.MaxPixels), C.int(max(threads, 1)))
	if h == nil {
		src.Release()
		return nil, errs.New(errs.KindInternal, "ffmpeg: out of memory")
	}
	return openTranscoder(ctx, &Transcoder{h: h, src: src}, lim)
}

// OpenTranscodeFile is OpenTranscode over a source in a file (WP-121b):
// the shim reads it with pread through a duplicate of its descriptor, so
// no FFmpeg protocol is enabled and no pointer or path crosses into C.
func OpenTranscodeFile(ctx context.Context, src FileSource, lim video.Limits, threads int) (*Transcoder, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if src == nil || src.Len() <= 0 {
		return nil, errs.New(errs.KindCorrupt, "ffmpeg: empty source")
	}
	if lim.MaxBytes > 0 && src.Len() > lim.MaxBytes {
		return nil, errs.WithPublic(errs.Newf(errs.KindLimitExceeded, "ffmpeg: video of %d bytes exceeds the limit of %d",
			src.Len(), lim.MaxBytes), "the video is larger than the size limit")
	}
	f, off := src.File()
	h := C.xi_tx_new_fd(C.int(f.Fd()), C.int64_t(off), C.int64_t(src.Len()), C.int64_t(lim.MaxPixels), C.int(max(threads, 1)))
	runtime.KeepAlive(f)
	if h == nil {
		return nil, errs.New(errs.KindInternal, "ffmpeg: out of memory or descriptors")
	}
	src.Retain() // released by Close, like a memory source
	return openTranscoder(ctx, &Transcoder{h: h, src: src}, lim)
}

// openTranscoder probes t's source and checks lim; it closes t on failure.
func openTranscoder(ctx context.Context, t *Transcoder, lim video.Limits) (*Transcoder, error) {
	h := t.h
	stop := watch(ctx, t.setAbort)
	defer stop()
	var info C.XiTxInfo
	var cerr [256]C.char
	if rc := C.xi_tx_open(h, &info, &cerr[0], C.size_t(len(cerr))); rc != C.XI_FF_OK {
		err := t.check(ctx, rc, &cerr[0], "open")
		t.Close()
		return nil, err
	}
	t.in = video.TranscodeInput{
		Container:      C.GoString(&info.container[0]),
		Duration:       max(float64(info.duration), 0),
		StartTime:      float64(info.start_time),
		Video:          info.video != 0,
		VideoCodec:     C.GoString(&info.vcodec[0]),
		Width:          int(info.width),
		Height:         int(info.height),
		SARNum:         int(info.sar_num),
		SARDen:         int(info.sar_den),
		Rotation:       int(info.rotation),
		HFlip:          info.hflip != 0,
		FPS:            float64(info.fps),
		Audio:          info.audio != 0,
		AudioCodec:     C.GoString(&info.acodec[0]),
		AudioDecodable: info.audio_decodable != 0,
		SampleRate:     int(info.sample_rate),
		Channels:       int(info.channels),
	}
	if err := checkInput(&t.in, lim); err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

// checkInput enforces the duration and pixel limits on the probed source.
func checkInput(in *video.TranscodeInput, lim video.Limits) error {
	if lim.MaxDuration > 0 && in.Duration > lim.MaxDuration.Seconds() {
		return errs.WithPublic(errs.Newf(errs.KindLimitExceeded, "ffmpeg: video of %s exceeds the duration limit of %s",
			time.Duration(in.Duration*float64(time.Second)), lim.MaxDuration), "the video is longer than the duration limit")
	}
	if lim.MaxPixels > 0 && int64(in.Width)*int64(in.Height) > lim.MaxPixels {
		return errs.WithPublic(errs.Newf(errs.KindLimitExceeded, "ffmpeg: %dx%d video frames exceed the limit of %d pixels",
			in.Width, in.Height, lim.MaxPixels), "the video frames are larger than the pixel limit")
	}
	return nil
}

// Input is the probed source.
func (t *Transcoder) Input() video.TranscodeInput { return t.in }

// Run transcodes the source with p (video.PlanTranscode) and returns the
// file. pixels[i] holds the rendered RGBA pixels of image overlay
// p.Video.Overlays[i] (nil for a timecode; exec.RenderOverlay), read
// during the call only. ctx aborts the work in C at its next packet,
// frame or write; a file over p.MaxBytes is errs.KindLimitExceeded. A
// transcoder runs once. The caller releases the output.
func (t *Transcoder) Run(ctx context.Context, p *video.TranscodePlan, pixels ...*vips.Buffer) (*Output, error) {
	out, _, err := t.RunSpill(ctx, p, nil, 0, pixels...)
	return out, err
}

// RunSpill is Run that moves the output into spill (a spool file, written
// from offset 0 through a duplicate of its descriptor) once it grows past
// spillBytes (WP-121b): the output is then nil and spilled is its size in
// the file. With a nil spill or spillBytes ≤ 0 it is Run.
func (t *Transcoder) RunSpill(ctx context.Context, p *video.TranscodePlan, spill *os.File, spillBytes int64, pixels ...*vips.Buffer) (out *Output, spilled int64, err error) {
	if t == nil || t.h == nil {
		return nil, 0, errs.New(errs.KindInternal, "ffmpeg: closed transcoder")
	}
	if err := ctxErr(ctx); err != nil {
		return nil, 0, err
	}
	var cp C.XiTxParams
	if err := params(&cp, p, pixels); err != nil {
		return nil, 0, err
	}
	free, err := copyAudioParams(&cp, p)
	if err != nil {
		return nil, 0, err
	}
	defer free()
	cp.spill_fd = -1
	if spill != nil && spillBytes > 0 {
		cp.spill_fd = C.int(spill.Fd())
		cp.spill_bytes = C.int64_t(spillBytes)
	}
	t.setAbort(false)
	stop := watch(ctx, t.setAbort)
	defer stop()
	var cerr [256]C.char
	rc := C.xi_tx_run(t.h, &cp, &cerr[0], C.size_t(len(cerr)))
	runtime.KeepAlive(spill) // the shim duplicated its descriptor during the call
	if rc != C.XI_FF_OK {
		if rc == C.XI_FF_ERR_LIMIT {
			if err := ctxErr(ctx); err != nil {
				return nil, 0, err
			}
			return nil, 0, errs.WithPublic(errs.Newf(errs.KindLimitExceeded, "ffmpeg: transcode: output over %d bytes", p.MaxBytes),
				"the output is larger than the size limit")
		}
		return nil, 0, t.check(ctx, rc, &cerr[0], "transcode")
	}
	var n C.int64_t
	if C.xi_tx_spilled(t.h, &n) != 0 {
		if n <= 0 || p.Waveform != nil {
			return nil, 0, errs.New(errs.KindInternal, "ffmpeg: empty or waveform output in the spool file")
		}
		return nil, int64(n), nil
	}
	data := C.xi_tx_take(t.h, &n)
	if data == nil || n <= 0 {
		return nil, 0, errs.New(errs.KindInternal, "ffmpeg: empty output")
	}
	if w := p.Waveform; w != nil && int64(n) != int64(w.Width)*int64(w.Height)*4 {
		C.xi_tx_out_free(data)
		return nil, 0, errs.Newf(errs.KindInternal, "ffmpeg: waveform of %d bytes, want %dx%d RGBA", int64(n), w.Width, w.Height)
	}
	return newOutput(unsafe.Pointer(data), int(n), func(p unsafe.Pointer) { C.xi_tx_out_free((*C.uint8_t)(p)) }), 0, nil
}

// newOutput wraps n bytes at p with one reference; free releases them.
func newOutput(p unsafe.Pointer, n int, free func(unsafe.Pointer)) *Output {
	o := &Output{p: p, n: n, free: free}
	o.refs.Store(1)
	return o
}

// params copies p into the C parameters, checking what C relies on.
func params(cp *C.XiTxParams, p *video.TranscodePlan, pixels []*vips.Buffer) error {
	if !cstr(cp.muxer[:], p.Output.Muxer) {
		return errs.Newf(errs.KindInternal, "ffmpeg: muxer %q", p.Output.Muxer)
	}
	cp.faststart = cbool(p.FastStart)
	cp.max_bytes = C.int64_t(p.MaxBytes)
	cp.start = C.double(p.Start)
	cp.end = C.double(p.End)
	cp.threads = C.int(max(p.Threads, 1))
	if v := p.Video; v != nil {
		codec, ok := encoderCode(v.Codec)
		if !ok || !cstr(cp.vfilters[:], v.Filters) || !cstr(cp.preset[:], v.Preset) {
			return errs.Newf(errs.KindInternal, "ffmpeg: video plan %s %q", v.Codec, v.Filters)
		}
		cp.vcodec = codec
		cp.fps = C.double(v.FPS)
		cp.crf = C.int(v.CRF)
		cp.speed = C.int(v.Speed)
		cp.max_bitrate = C.int64_t(v.MaxBitrate)
		cp.gop = C.int(max(v.GOP, 1))
		if err := overlayParams(cp, v, pixels); err != nil {
			return err
		}
	} else if len(pixels) > 0 {
		return errs.New(errs.KindInternal, "ffmpeg: overlay pixels without a video stream")
	}
	if a := p.Audio; a != nil {
		var codec C.int
		switch a.Codec {
		case video.AudioAAC:
			codec = C.XI_TX_AAC
		case video.AudioOpus:
			codec = C.XI_TX_OPUS
		case video.AudioMP3:
			codec = C.XI_TX_MP3
		case video.AudioFLAC:
			codec = C.XI_TX_FLAC
		case video.AudioPCM:
			codec = C.XI_TX_PCM
		case video.AudioNone: // refused below
		}
		if codec == 0 || !cstr(cp.afilters[:], a.Filters) {
			return errs.Newf(errs.KindInternal, "ffmpeg: audio plan %s %q", a.Codec, a.Filters)
		}
		cp.acodec = codec
		cp.abitrate = C.int64_t(a.Bitrate)
	}
	if w := p.Waveform; w != nil {
		if p.Video != nil || p.Audio != nil || !cstr(cp.afilters[:], w.Filters) {
			return errs.Newf(errs.KindInternal, "ffmpeg: waveform plan %q", w.Filters)
		}
		cp.waveform = 1
		return nil
	}
	if p.Video == nil && p.Audio == nil {
		return errs.New(errs.KindInternal, "ffmpeg: empty plan")
	}
	if ts := p.TS; ts != nil {
		if p.Video == nil || p.Audio != nil || p.Fragment != nil || p.Output.Muxer != "mpegts" {
			return errs.New(errs.KindInternal, "ffmpeg: an mpeg-ts segment takes one video stream and copied audio")
		}
		cp.ts = 1
		cp.frag_offset = C.int64_t(ts.Offset90k)
		cp.level = C.int(p.Video.Level)
	}
	if f := p.Fragment; f != nil {
		if (p.Video == nil) == (p.Audio == nil) || p.Output.Muxer != "mp4" {
			return errs.New(errs.KindInternal, "ffmpeg: fragmented output takes one stream in mp4")
		}
		cp.frag = 1
		cp.frag_discont = cbool(f.Discont)
		cp.frag_offset = C.int64_t(f.Offset90k)
		cp.frag_seconds = C.int(f.Seconds)
		cp.audio_priming = C.int(f.Priming)
		if p.Video != nil {
			cp.level = C.int(p.Video.Level)
		}
	}
	return nil
}

// copyAudioParams copies the packets of an MPEG-TS segment's audio
// (TSPlan.Audio) into C memory for the call; free releases it.
func copyAudioParams(cp *C.XiTxParams, p *video.TranscodePlan) (free func(), err error) {
	free = func() {}
	if p.TS == nil || p.TS.Audio == nil {
		return free, nil
	}
	a := p.TS.Audio
	cp.copy_rate, cp.copy_channels = C.int(a.SampleRate), C.int(a.Channels)
	n := len(a.Packets)
	if n == 0 {
		return free, nil
	}
	var total int64
	for _, pk := range a.Packets {
		if len(pk.Data) == 0 || len(pk.Data) > math.MaxInt32 || pk.PTS < 0 || pk.Duration < 0 {
			return free, errs.New(errs.KindInternal, "ffmpeg: copied audio packet")
		}
		total += int64(len(pk.Data))
	}
	data := C.malloc(C.size_t(total))
	sizes := C.malloc(C.size_t(n) * 4)
	pts := C.malloc(C.size_t(n) * 8)
	durs := C.malloc(C.size_t(n) * 8)
	free = func() {
		C.free(data)
		C.free(sizes)
		C.free(pts)
		C.free(durs)
	}
	if data == nil || sizes == nil || pts == nil || durs == nil {
		free()
		return func() {}, errs.New(errs.KindInternal, "ffmpeg: out of memory")
	}
	buf := unsafe.Slice((*byte)(data), total)
	sz := unsafe.Slice((*C.int32_t)(sizes), n)
	ps := unsafe.Slice((*C.int64_t)(pts), n)
	ds := unsafe.Slice((*C.int64_t)(durs), n)
	off := 0
	for i, pk := range a.Packets {
		off += copy(buf[off:], pk.Data)
		sz[i], ps[i], ds[i] = C.int32_t(len(pk.Data)), C.int64_t(pk.PTS), C.int64_t(pk.Duration)
	}
	cp.ncopy = C.int(n)
	cp.copy_data = (*C.uint8_t)(data)
	cp.copy_bytes = C.int64_t(total)
	cp.copy_size = (*C.int32_t)(sizes)
	cp.copy_pts = (*C.int64_t)(pts)
	cp.copy_dur = (*C.int64_t)(durs)
	return free, nil
}

// overlayParams copies the video stream's overlays and their pixels.

// The pixels stay where they are (C memory owned by libvips): C copies
// them into frames during the call.
func overlayParams(cp *C.XiTxParams, v *video.VideoStreamPlan, pixels []*vips.Buffer) error {
	if err := v.CheckOverlays(); err != nil {
		return err
	}
	if len(pixels) != len(v.Overlays) || len(v.Overlays) > len(cp.overlays) {
		return errs.Newf(errs.KindInternal, "ffmpeg: %d overlays with %d pixel buffers", len(v.Overlays), len(pixels))
	}
	cp.noverlays = C.int(len(v.Overlays))
	for i := range v.Overlays {
		o, co := &v.Overlays[i], &cp.overlays[i]
		if !cstr(co.enable[:], o.Enable) {
			return errs.Newf(errs.KindInternal, "ffmpeg: overlay %d enable %q", o.Index, o.Enable)
		}
		if o.FadeIn > 0 {
			co.fade_in_start, co.fade_in_dur = micros(o.Start), micros(o.FadeIn)
		}
		if o.FadeOut > 0 {
			co.fade_out_start, co.fade_out_dur = micros(o.FadeEnd-o.FadeOut), micros(o.FadeOut)
		}
		if tc := o.Timecode; tc != nil {
			if pixels[i] != nil || tc.Size <= 0 || !cstr(co.text[:], tc.Text) || !cstr(co.tx[:], tc.X) || !cstr(co.ty[:], tc.Y) ||
				!cstr(co.color[:], tc.Color) || !cstr(co.alpha[:], tc.Alpha) {
				return errs.Newf(errs.KindInternal, "ffmpeg: timecode overlay %d %+v", o.Index, tc)
			}
			co.size, co.shadow = C.int(tc.Size), C.int(max(tc.Shadow, 0))
			continue
		}
		px := pixels[i]
		if px == nil || px.Len() < o.Width*o.Height*4 {
			return errs.Newf(errs.KindInternal, "ffmpeg: overlay %d: %dx%d without its pixels", o.Index, o.Width, o.Height)
		}
		co.rgba = (*C.uint8_t)(unsafe.Pointer(&px.Bytes()[0]))
		co.x, co.y, co.width, co.height = C.int(o.X), C.int(o.Y), C.int(o.Width), C.int(o.Height)
	}
	return nil
}

// micros is seconds in microseconds (AVOption durations).
func micros(s float64) C.int64_t { return C.int64_t(math.Round(s * 1e6)) }

// cstr copies s into dst, NUL-terminated; false when it does not fit or
// holds a NUL.
func cstr(dst []C.char, s string) bool {
	if len(s) >= len(dst) {
		return false
	}
	for i := range len(s) {
		if s[i] == 0 {
			return false
		}
		dst[i] = C.char(s[i])
	}
	dst[len(s)] = 0
	return true
}

// Close frees the handle and releases the source. It is nil-safe and
// idempotent.
func (t *Transcoder) Close() {
	if t == nil || t.h == nil {
		return
	}
	C.xi_tx_free(t.h)
	t.h = nil
	t.src.Release()
}

func (t *Transcoder) setAbort(on bool) { C.xi_tx_abort(t.h, cbool(on)) }

// check turns a shim result code into an error: an abort caused by ctx
// reports ctx's kind.
func (t *Transcoder) check(ctx context.Context, rc C.int, cerr *C.char, op string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return codeError(rc, C.GoString(cerr), op)
}

// transcodeStats are the transcoder counters of ReadStats.
func transcodeStats() (handles, outputs int) {
	return int(C.xi_tx_live()), int(C.xi_tx_live_outputs())
}
