//go:build ffmpeg && cgo

// Video output (WP-76, ADR 0010 amendment): animation frames → VP9, AV1,
// H.264 or HEVC, muxed into WebM or MP4 in a growable buffer the handle owns.
// The Go side copies the finished output into a libvips buffer (one copy of
// the compressed bytes) and frees the handle. Nothing here calls into Go or
// keeps a pointer to Go memory: the frames are C memory owned by libvips,
// read during xi_ff_enc_frame only.

#include "shim.h"
#include "internal.h"

#include <math.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/avutil.h>
#include <libavutil/imgutils.h>
#include <libswscale/swscale.h>

struct XiFFEnc {
  AVFormatContext *oc;
  AVCodecContext *enc;
  AVStream *st;
  AVFrame *frame;
  AVPacket *pkt;
  struct SwsContext *sws;
  int bands;
  atomic_int abort;
  XiFFOut out; // the muxed file
  // Timestamps (ms) and durations of the frames sent, to give each packet
  // the duration of its frame (the last sample's duration in MP4).
  int64_t *pts, *dur;
  int nframes, capframes;
};

static atomic_int xi_ff_encoders_live;

int
xi_ff_encoders(void)
{
  xi_ff_setup();
  int m = 0;
  if (avcodec_find_encoder_by_name("libvpx-vp9"))
    m |= XI_FF_ENC_VP9;
  if (avcodec_find_encoder_by_name("libaom-av1"))
    m |= XI_FF_ENC_AV1;
  if (avcodec_find_encoder_by_name("libx264"))
    m |= XI_FF_ENC_H264;
  if (avcodec_find_encoder_by_name("libx265"))
    m |= XI_FF_ENC_HEVC;
  // A codec is only usable with the muxer that holds it.
  if (!av_guess_format("webm", NULL, NULL))
    m &= ~XI_FF_ENC_VP9;
  if (!av_guess_format("mp4", NULL, NULL))
    m &= ~(XI_FF_ENC_AV1 | XI_FF_ENC_H264 | XI_FF_ENC_HEVC);
  return m;
}

int
xi_ff_live_encoders(void)
{
  return atomic_load(&xi_ff_encoders_live);
}

static int
xi_ff_enc_interrupt(void *opaque)
{
  XiFFEnc *e = opaque;
  return atomic_load(&e->abort);
}

void
xi_ff_enc_abort(XiFFEnc *e, int on)
{
  atomic_store(&e->abort, on);
}

void
xi_ff_enc_free(XiFFEnc *e)
{
  if (!e)
    return;
  sws_freeContext(e->sws);
  av_frame_free(&e->frame);
  av_packet_free(&e->pkt);
  avcodec_free_context(&e->enc);
  if (e->oc) {
    xi_ff_out_detach(e->oc);
    avformat_free_context(e->oc);
  }
  xi_ff_out_free(&e->out);
  av_free(e->pts);
  av_free(e->dur);
  av_free(e);
  atomic_fetch_sub(&xi_ff_encoders_live, 1);
}

static int
xi_ff_enc_code(XiFFEnc *e, int averr)
{
  if (atomic_load(&e->abort) || averr == AVERROR_EXIT)
    return XI_FF_ERR_ABORTED;
  if (e->out.over)
    return XI_FF_ERR_LIMIT;
  if (averr == AVERROR(ENOMEM))
    return XI_FF_ERR_NOMEM;
  if (averr == AVERROR_ENCODER_NOT_FOUND || averr == AVERROR_MUXER_NOT_FOUND
      || averr == AVERROR_PATCHWELCOME)
    return XI_FF_ERR_UNSUPPORTED;
  return XI_FF_ERR_INTERNAL;
}

static const char *
xi_ff_enc_name(int codec)
{
  switch (codec) {
  case XI_FF_ENC_VP9:
    return "libvpx-vp9";
  case XI_FF_ENC_AV1:
    return "libaom-av1";
  case XI_FF_ENC_H264:
    return "libx264";
  case XI_FF_ENC_HEVC:
    return "libx265";
  default:
    return NULL;
  }
}

// Fails xi_ff_enc_new: frees e, sets *code and returns NULL.
static XiFFEnc *
xi_ff_enc_fail(XiFFEnc *e, int *code, int c)
{
  *code = c;
  xi_ff_enc_free(e);
  return NULL;
}

// Encoder options: constant quality (crf, no bit rate), one thread, and
// the fastest settings that keep the quality (realtime usage for libvpx and
// libaom, whose "good" modes are an order of magnitude slower).
static void
xi_ff_enc_options(const XiFFEncParams *p, AVDictionary **opts)
{
  av_dict_set_int(opts, "crf", p->crf, 0);
  av_dict_set(opts, "threads", "1", 0);
  switch (p->codec) {
  case XI_FF_ENC_VP9:
    av_dict_set(opts, "deadline", "realtime", 0);
    av_dict_set_int(opts, "cpu-used", p->speed, 0);
    av_dict_set(opts, "row-mt", "0", 0);
    break;
  case XI_FF_ENC_AV1:
    av_dict_set(opts, "usage", "realtime", 0);
    av_dict_set_int(opts, "cpu-used", p->speed, 0);
    av_dict_set(opts, "row-mt", "0", 0);
    break;
  case XI_FF_ENC_H264:
    av_dict_set(opts, "preset", p->preset, 0);
    av_dict_set(opts, "profile", "high", 0);
    break;
  case XI_FF_ENC_HEVC:
    av_dict_set(opts, "preset", p->preset, 0);
    av_dict_set(opts, "x265-params", "log-level=none:pools=none:frame-threads=1", 0);
    break;
  }
}

XiFFEnc *
xi_ff_enc_new(const XiFFEncParams *p, int *code, char *err, size_t errlen)
{
  xi_ff_setup();
  *code = XI_FF_ERR_NOMEM;
  XiFFEnc *e = av_mallocz(sizeof(*e));
  if (!e)
    return NULL;
  atomic_fetch_add(&xi_ff_encoders_live, 1);
  e->bands = p->bands;
  e->out.max_bytes = p->max_bytes;
  e->out.abort = &e->abort;
  int ret;

  const char *name = xi_ff_enc_name(p->codec);
  const char *muxer = p->container == XI_FF_MUX_WEBM ? "webm" : "mp4";
  const AVCodec *codec = name ? avcodec_find_encoder_by_name(name) : NULL;
  if (!codec) {
    xi_ff_set_err(err, errlen, "encoder not found", 0);
    return xi_ff_enc_fail(e, code, XI_FF_ERR_UNSUPPORTED);
  }
  if (p->width <= 0 || p->height <= 0 || (p->width & 1) || (p->height & 1)
      || (p->bands != 3 && p->bands != 4) || (p->alpha && p->bands != 4)) {
    xi_ff_set_err(err, errlen, "encoder parameters", 0);
    return xi_ff_enc_fail(e, code, XI_FF_ERR_INTERNAL);
  }

  ret = avformat_alloc_output_context2(&e->oc, NULL, muxer, NULL);
  if (ret < 0 || !e->oc) {
    xi_ff_set_err(err, errlen, "muxer", ret);
    return xi_ff_enc_fail(e, code, XI_FF_ERR_UNSUPPORTED);
  }
  if (xi_ff_out_attach(e->oc, &e->out) < 0)
    return xi_ff_enc_fail(e, code, XI_FF_ERR_NOMEM);
  e->oc->interrupt_callback.callback = xi_ff_enc_interrupt;
  e->oc->interrupt_callback.opaque = e;
  // No library version strings in the output: the same input gives the
  // same bytes.
  e->oc->flags |= AVFMT_FLAG_BITEXACT;

  e->enc = avcodec_alloc_context3(codec);
  e->frame = av_frame_alloc();
  e->pkt = av_packet_alloc();
  if (!e->enc || !e->frame || !e->pkt)
    return xi_ff_enc_fail(e, code, XI_FF_ERR_NOMEM);
  AVCodecContext *c = e->enc;
  c->width = p->width;
  c->height = p->height;
  c->pix_fmt = p->alpha ? AV_PIX_FMT_YUVA420P : AV_PIX_FMT_YUV420P;
  c->time_base = (AVRational) { 1, 1000 };
  c->framerate = av_d2q(p->fps > 0 ? p->fps : 10, 100000);
  c->sample_aspect_ratio = (AVRational) { 1, 1 };
  c->thread_count = 1;
  c->bit_rate = 0;
  c->flags |= AV_CODEC_FLAG_BITEXACT;
  // sRGB content: BT.709 matrix and primaries in limited range, with the
  // sRGB transfer tagged.
  c->color_range = AVCOL_RANGE_MPEG;
  c->colorspace = AVCOL_SPC_BT709;
  c->color_primaries = AVCOL_PRI_BT709;
  c->color_trc = AVCOL_TRC_IEC61966_2_1;
  // A keyframe every ten seconds at most: seeking stays cheap, and short
  // loops need only the first.
  c->gop_size = (int)fmin(fmax(av_q2d(c->framerate) * 10, 1), 600);
  if (e->oc->oformat->flags & AVFMT_GLOBALHEADER)
    c->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;

  AVDictionary *opts = NULL;
  xi_ff_enc_options(p, &opts);
  ret = avcodec_open2(c, codec, &opts);
  av_dict_free(&opts);
  if (ret < 0) {
    xi_ff_set_err(err, errlen, "open encoder", ret);
    return xi_ff_enc_fail(e, code, xi_ff_enc_code(e, ret));
  }

  e->st = avformat_new_stream(e->oc, NULL);
  if (!e->st)
    return xi_ff_enc_fail(e, code, XI_FF_ERR_NOMEM);
  e->st->time_base = c->time_base;
  e->st->avg_frame_rate = c->framerate;
  if ((ret = avcodec_parameters_from_context(e->st->codecpar, c)) < 0) {
    xi_ff_set_err(err, errlen, "stream parameters", ret);
    return xi_ff_enc_fail(e, code, xi_ff_enc_code(e, ret));
  }
  if (p->codec == XI_FF_ENC_HEVC) // Apple players want hvc1, not hev1
    e->st->codecpar->codec_tag = MKTAG('h', 'v', 'c', '1');
  if (p->alpha) // WebM AlphaMode: players decode the alpha stream
    av_dict_set(&e->st->metadata, "alpha_mode", "1", 0);

  AVDictionary *mopts = NULL;
  if (p->container == XI_FF_MUX_MP4)
    av_dict_set(&mopts, "movflags", "+faststart", 0);
  ret = avformat_write_header(e->oc, &mopts);
  av_dict_free(&mopts);
  if (ret < 0) {
    xi_ff_set_err(err, errlen, "write header", ret);
    return xi_ff_enc_fail(e, code, xi_ff_enc_code(e, ret));
  }

  e->frame->format = c->pix_fmt;
  e->frame->width = c->width;
  e->frame->height = c->height;
  e->frame->color_range = c->color_range;
  e->frame->colorspace = c->colorspace;
  e->frame->color_primaries = c->color_primaries;
  e->frame->color_trc = c->color_trc;
  if ((ret = av_frame_get_buffer(e->frame, 0)) < 0) {
    xi_ff_set_err(err, errlen, "frame buffer", ret);
    return xi_ff_enc_fail(e, code, xi_ff_enc_code(e, ret));
  }
  enum AVPixelFormat in = p->bands == 4 ? AV_PIX_FMT_RGBA : AV_PIX_FMT_RGB24;
  e->sws = sws_getContext(c->width, c->height, in, c->width, c->height, c->pix_fmt,
      SWS_BICUBIC | SWS_ACCURATE_RND | SWS_FULL_CHR_H_INP, NULL, NULL, NULL);
  if (!e->sws) {
    xi_ff_set_err(err, errlen, "scaler", 0);
    return xi_ff_enc_fail(e, code, XI_FF_ERR_INTERNAL);
  }
  // Full-range RGB → BT.709 limited-range YUV.
  sws_setColorspaceDetails(e->sws, sws_getCoefficients(SWS_CS_ITU709), 1,
      sws_getCoefficients(SWS_CS_ITU709), 0, 0, 1 << 16, 1 << 16);
  *code = XI_FF_OK;
  return e;
}

// Index of the frame sent with timestamp pts (the timestamps increase), or
// -1.
static int
xi_ff_enc_find(const XiFFEnc *e, int64_t pts)
{
  int lo = 0, hi = e->nframes - 1;
  while (lo <= hi) {
    int mid = lo + (hi - lo) / 2;
    if (e->pts[mid] == pts)
      return mid;
    if (e->pts[mid] < pts)
      lo = mid + 1;
    else
      hi = mid - 1;
  }
  return -1;
}

// Muxes every packet the encoder has ready.
static int
xi_ff_enc_drain(XiFFEnc *e, char *err, size_t errlen)
{
  for (;;) {
    if (atomic_load(&e->abort))
      return XI_FF_ERR_ABORTED;
    int ret = avcodec_receive_packet(e->enc, e->pkt);
    if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
      return XI_FF_OK;
    if (ret < 0) {
      xi_ff_set_err(err, errlen, "encode", ret);
      return xi_ff_enc_code(e, ret);
    }
    if (e->pkt->pts != AV_NOPTS_VALUE) {
      int i = xi_ff_enc_find(e, e->pkt->pts);
      if (i >= 0)
        e->pkt->duration = e->dur[i];
    }
    av_packet_rescale_ts(e->pkt, e->enc->time_base, e->st->time_base);
    e->pkt->stream_index = e->st->index;
    ret = av_interleaved_write_frame(e->oc, e->pkt); // takes the packet's reference
    if (ret < 0) {
      xi_ff_set_err(err, errlen, "mux", ret);
      return xi_ff_enc_code(e, ret);
    }
  }
}

int
xi_ff_enc_frame(
    XiFFEnc *e, const uint8_t *pix, int64_t pts_ms, int64_t duration_ms, char *err, size_t errlen)
{
  if (atomic_load(&e->abort))
    return XI_FF_ERR_ABORTED;
  if (e->nframes > 0 && pts_ms <= e->pts[e->nframes - 1]) {
    xi_ff_set_err(err, errlen, "timestamps must increase", 0);
    return XI_FF_ERR_INTERNAL;
  }
  if (e->nframes == e->capframes) {
    int n = e->capframes ? e->capframes * 2 : 64;
    int64_t *pts = av_realloc_array(e->pts, (size_t)n, sizeof(*pts));
    if (!pts)
      return XI_FF_ERR_NOMEM;
    e->pts = pts;
    int64_t *dur = av_realloc_array(e->dur, (size_t)n, sizeof(*dur));
    if (!dur)
      return XI_FF_ERR_NOMEM;
    e->dur = dur;
    e->capframes = n;
  }
  e->pts[e->nframes] = pts_ms;
  e->dur[e->nframes] = duration_ms;
  e->nframes++;

  int ret = av_frame_make_writable(e->frame);
  if (ret < 0) {
    xi_ff_set_err(err, errlen, "frame", ret);
    return xi_ff_enc_code(e, ret);
  }
  const uint8_t *src[4] = { pix, NULL, NULL, NULL };
  int stride[4] = { e->enc->width * e->bands, 0, 0, 0 };
  int rows = sws_scale(e->sws, src, stride, 0, e->enc->height, e->frame->data, e->frame->linesize);
  if (rows != e->enc->height) {
    xi_ff_set_err(err, errlen, "convert", rows < 0 ? rows : 0);
    return XI_FF_ERR_INTERNAL;
  }
  e->frame->pts = pts_ms;
  e->frame->duration = duration_ms;
  ret = avcodec_send_frame(e->enc, e->frame);
  if (ret < 0) {
    xi_ff_set_err(err, errlen, "encode", ret);
    return xi_ff_enc_code(e, ret);
  }
  return xi_ff_enc_drain(e, err, errlen);
}

int
xi_ff_enc_finish(XiFFEnc *e, char *err, size_t errlen)
{
  if (atomic_load(&e->abort))
    return XI_FF_ERR_ABORTED;
  int ret = avcodec_send_frame(e->enc, NULL);
  if (ret < 0 && ret != AVERROR_EOF) {
    xi_ff_set_err(err, errlen, "flush", ret);
    return xi_ff_enc_code(e, ret);
  }
  if ((ret = xi_ff_enc_drain(e, err, errlen)) != XI_FF_OK)
    return ret;
  if ((ret = av_write_trailer(e->oc)) < 0) {
    xi_ff_set_err(err, errlen, "write trailer", ret);
    return xi_ff_enc_code(e, ret);
  }
  avio_flush(e->oc->pb);
  if (e->oc->pb->error < 0) {
    xi_ff_set_err(err, errlen, "write", e->oc->pb->error);
    return xi_ff_enc_code(e, e->oc->pb->error);
  }
  return XI_FF_OK;
}

size_t
xi_ff_enc_size(XiFFEnc *e)
{
  return (size_t)e->out.size;
}

void
xi_ff_enc_copy(XiFFEnc *e, uint8_t *dst)
{
  if (e->out.size > 0)
    memcpy(dst, e->out.data, (size_t)e->out.size);
}
