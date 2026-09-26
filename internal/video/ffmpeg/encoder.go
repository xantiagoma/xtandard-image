//go:build ffmpeg && cgo

package ffmpeg

/*
#cgo nocallback xi_ff_encoders
#cgo noescape xi_ff_enc_new
#cgo nocallback xi_ff_enc_new
#cgo noescape xi_ff_enc_frame
#cgo nocallback xi_ff_enc_frame
#cgo noescape xi_ff_enc_finish
#cgo nocallback xi_ff_enc_finish
#cgo nocallback xi_ff_enc_size
#cgo nocallback xi_ff_enc_copy
#cgo nocallback xi_ff_enc_abort
#cgo nocallback xi_ff_enc_free
#include "shim.h"
*/
import "C"

import (
	"context"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Encoders probes the linked FFmpeg for the video encoders of the output
// contract (avcodec_find_encoder_by_name, and the muxer each needs). The
// application calls it once at startup.
func Encoders() video.EncoderSet {
	m := C.xi_ff_encoders()
	return video.EncoderSet{
		VP9:  m&C.XI_FF_ENC_VP9 != 0,
		AV1:  m&C.XI_FF_ENC_AV1 != 0,
		H264: m&C.XI_FF_ENC_H264 != 0,
		HEVC: m&C.XI_FF_ENC_HEVC != 0,
	}
}

// Encoder encodes frames into a video file held in C memory. It is not
// safe for concurrent use; each call is cancelled by its own ctx through
// the handle's abort flag.
type Encoder struct {
	e      *C.XiFFEnc
	stop   func()
	frame  int // bytes of one frame
	frames int
}

// NewEncoder opens p's encoder and muxer. errs.KindUnsupported when this
// build has no such encoder. The caller closes the encoder.
func NewEncoder(ctx context.Context, p *video.EncodeParams) (*Encoder, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	codec, ok := encoderCode(p.Codec)
	if !ok || p.Width <= 0 || p.Height <= 0 || p.Width%2 != 0 || p.Height%2 != 0 ||
		p.Width > maxSide || p.Height > maxSide || (p.Bands != 3 && p.Bands != 4) {
		return nil, errs.Newf(errs.KindInternal, "ffmpeg: encode %s %dx%dx%d", p.Codec, p.Width, p.Height, p.Bands)
	}
	cp := C.XiFFEncParams{
		codec:     codec,
		container: C.XI_FF_MUX_MP4,
		width:     C.int(p.Width),
		height:    C.int(p.Height),
		alpha:     cbool(p.Alpha),
		bands:     C.int(p.Bands),
		crf:       C.int(p.CRF),
		speed:     C.int(p.Speed),
		fps:       C.double(p.FPS),
		max_bytes: C.int64_t(p.MaxBytes),
	}
	if p.Container == video.ContainerWebM {
		cp.container = C.XI_FF_MUX_WEBM
	}
	for i := 0; i < len(p.Preset) && i < len(cp.preset)-1; i++ {
		cp.preset[i] = C.char(p.Preset[i])
	}
	var code C.int
	var cerr [256]C.char
	e := C.xi_ff_enc_new(&cp, &code, &cerr[0], C.size_t(len(cerr)))
	if e == nil {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		return nil, encodeError(code, C.GoString(&cerr[0]), "open")
	}
	return &Encoder{e: e, frame: p.Width * p.Height * p.Bands}, nil
}

// encoderCode maps a codec of the contract onto the shim's.
func encoderCode(c video.Codec) (C.int, bool) {
	switch c {
	case video.CodecVP9:
		return C.XI_FF_ENC_VP9, true
	case video.CodecAV1:
		return C.XI_FF_ENC_AV1, true
	case video.CodecH264:
		return C.XI_FF_ENC_H264, true
	case video.CodecHEVC:
		return C.XI_FF_ENC_HEVC, true
	default:
		return 0, false
	}
}

// Frame encodes one frame: width × height × bands packed 8-bit pixels in C
// memory (a vips.Blob or Buffer: C reads it during the call only), shown
// from ptsMs for durationMs milliseconds. Timestamps must increase.
func (enc *Encoder) Frame(ctx context.Context, pix []byte, ptsMs, durationMs int64) error {
	if enc.e == nil {
		return errs.New(errs.KindInternal, "ffmpeg: closed encoder")
	}
	if len(pix) < enc.frame {
		return errs.Newf(errs.KindInternal, "ffmpeg: frame of %d bytes, want %d", len(pix), enc.frame)
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	enc.arm(ctx)
	defer enc.disarm()
	var cerr [256]C.char
	rc := C.xi_ff_enc_frame(enc.e, (*C.uint8_t)(unsafe.Pointer(&pix[0])), C.int64_t(ptsMs),
		C.int64_t(durationMs), &cerr[0], C.size_t(len(cerr)))
	if rc != C.XI_FF_OK {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		return encodeError(rc, C.GoString(&cerr[0]), "frame")
	}
	enc.frames++
	return nil
}

// Finish flushes the encoder, writes the trailer and returns the file in a
// new buffer (C memory tracked by libvips; the caller releases it).
func (enc *Encoder) Finish(ctx context.Context) (*vips.Buffer, error) {
	if enc.e == nil {
		return nil, errs.New(errs.KindInternal, "ffmpeg: closed encoder")
	}
	if enc.frames == 0 {
		return nil, errs.New(errs.KindInternal, "ffmpeg: no frame encoded")
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	enc.arm(ctx)
	defer enc.disarm()
	var cerr [256]C.char
	if rc := C.xi_ff_enc_finish(enc.e, &cerr[0], C.size_t(len(cerr))); rc != C.XI_FF_OK {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		return nil, encodeError(rc, C.GoString(&cerr[0]), "finish")
	}
	n := int(C.xi_ff_enc_size(enc.e))
	if n <= 0 {
		return nil, errs.New(errs.KindInternal, "ffmpeg: empty output")
	}
	buf, err := vips.NewBuffer(n)
	if err != nil {
		return nil, err
	}
	C.xi_ff_enc_copy(enc.e, (*C.uint8_t)(unsafe.Pointer(&buf.Bytes()[0])))
	return buf, nil
}

// Close frees the encoder. It is nil-safe and idempotent.
func (enc *Encoder) Close() {
	if enc == nil || enc.e == nil {
		return
	}
	enc.disarm()
	C.xi_ff_enc_free(enc.e)
	enc.e = nil
}

func (enc *Encoder) arm(ctx context.Context) {
	enc.disarm()
	C.xi_ff_enc_abort(enc.e, 0)
	enc.stop = watch(ctx, func(on bool) { C.xi_ff_enc_abort(enc.e, cbool(on)) })
}

func (enc *Encoder) disarm() {
	if enc.stop != nil {
		enc.stop()
		enc.stop = nil
	}
}

// encodeError classifies a shim result code of the encoder; msg is kept for
// logs only.
func encodeError(rc C.int, msg, op string) error {
	switch rc {
	case C.XI_FF_ERR_UNSUPPORTED:
		return errs.WithPublic(errs.Newf(errs.KindUnsupported, "ffmpeg: encode %s: %s", op, msg),
			"this video output is not supported by this image")
	case C.XI_FF_ERR_LIMIT:
		return errs.Newf(errs.KindLimitExceeded, "ffmpeg: encode %s: output over the size limit", op)
	case C.XI_FF_ERR_ABORTED:
		return errs.Newf(errs.KindCanceled, "ffmpeg: encode %s: aborted", op)
	default:
		return errs.Newf(errs.KindInternal, "ffmpeg: encode %s: %s", op, msg)
	}
}
