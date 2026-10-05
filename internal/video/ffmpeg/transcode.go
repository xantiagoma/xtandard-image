//go:build ffmpeg && cgo

package ffmpeg

/*
#cgo nocallback xi_tx_caps
#cgo nocallback xi_tx_new
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
	}
}

// Transcoder is an opened source for one video transformation: probed by
// OpenTranscode, run once by Run. It retains its source until Close and is
// not safe for concurrent use.
type Transcoder struct {
	h   *C.XiTx
	src *vips.Buffer
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
	t := &Transcoder{h: h, src: src}
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
// file. ctx aborts the work in C at its next packet, frame or write; a
// file over p.MaxBytes is errs.KindLimitExceeded. A transcoder runs once.
// The caller releases the output.
func (t *Transcoder) Run(ctx context.Context, p *video.TranscodePlan) (*Output, error) {
	if t == nil || t.h == nil {
		return nil, errs.New(errs.KindInternal, "ffmpeg: closed transcoder")
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	var cp C.XiTxParams
	if err := params(&cp, p); err != nil {
		return nil, err
	}
	t.setAbort(false)
	stop := watch(ctx, t.setAbort)
	defer stop()
	var cerr [256]C.char
	if rc := C.xi_tx_run(t.h, &cp, &cerr[0], C.size_t(len(cerr))); rc != C.XI_FF_OK {
		if rc == C.XI_FF_ERR_LIMIT {
			if err := ctxErr(ctx); err != nil {
				return nil, err
			}
			return nil, errs.WithPublic(errs.Newf(errs.KindLimitExceeded, "ffmpeg: transcode: output over %d bytes", p.MaxBytes),
				"the output is larger than the size limit")
		}
		return nil, t.check(ctx, rc, &cerr[0], "transcode")
	}
	var n C.int64_t
	data := C.xi_tx_take(t.h, &n)
	if data == nil || n <= 0 {
		return nil, errs.New(errs.KindInternal, "ffmpeg: empty output")
	}
	return newOutput(unsafe.Pointer(data), int(n), func(p unsafe.Pointer) { C.xi_tx_out_free((*C.uint8_t)(p)) }), nil
}

// params copies p into the C parameters, checking what C relies on.
func params(cp *C.XiTxParams, p *video.TranscodePlan) error {
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
		}
		if codec == 0 || !cstr(cp.afilters[:], a.Filters) {
			return errs.Newf(errs.KindInternal, "ffmpeg: audio plan %s %q", a.Codec, a.Filters)
		}
		cp.acodec = codec
		cp.abitrate = C.int64_t(a.Bitrate)
	}
	if p.Video == nil && p.Audio == nil {
		return errs.New(errs.KindInternal, "ffmpeg: empty plan")
	}
	return nil
}

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
