//go:build ffmpeg && cgo

#include "shim.h"

#include <math.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>

#if defined(__GLIBC__)
#include <malloc.h>
#endif

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/avutil.h>
#include <libavutil/channel_layout.h>
#include <libavutil/display.h>
#include <libavutil/imgutils.h>
#include <libavutil/pixdesc.h>
#include <libswscale/swscale.h>

// The demuxers a source may use, the only decoders it may reach, and no
// protocol at all: every byte comes from the custom AVIOContext, so a
// container that references another file (QuickTime data references, HLS)
// cannot open it.
#define XI_FF_FORMATS "mov,matroska,avi"
#define XI_FF_CODECS "h264,hevc,vp8,vp9,libdav1d,mpeg4,mjpeg,png,rawvideo"
#define XI_FF_PROTOCOLS "none"

// Size of the AVIO read buffer.
#define XI_FF_IO_BUFFER (64 * 1024)
// Width of the grey copy histograms are computed on.
#define XI_FF_HIST_WIDTH 64
// Consecutive packets the decoder may reject before the source is corrupt.
#define XI_FF_MAX_BAD_PACKETS 64

struct XiFF {
  AVFormatContext *fmt;
  AVIOContext *avio;
  AVCodecContext *dec;
  AVPacket *pkt;
  AVFrame *frame; // last decoded frame
  AVFrame *prev;  // the frame before it, while searching for a timestamp
  AVFrame *best;  // xi_ff_keep
  struct SwsContext *sws;
  struct SwsContext *grey;
  const uint8_t *data;
  int64_t size;
  int64_t pos;
  atomic_int abort;
  int vidx;
  int64_t max_pixels;
};

static atomic_int xi_ff_live_handles;
static pthread_once_t xi_ff_once = PTHREAD_ONCE_INIT;

// FFmpeg logs to stderr by default; the service logs JSON only, and every
// failure reaches Go as a result code with its message.
static void
xi_ff_init(void)
{
  av_log_set_level(AV_LOG_QUIET);
}

static void
xi_ff_set_err(char *err, size_t errlen, const char *what, int averr)
{
  if (!err || errlen == 0)
    return;
  if (averr) {
    char buf[AV_ERROR_MAX_STRING_SIZE] = { 0 };
    av_strerror(averr, buf, sizeof(buf));
    snprintf(err, errlen, "%s: %s", what, buf);
  } else {
    snprintf(err, errlen, "%s", what);
  }
}

static int
xi_ff_read(void *opaque, uint8_t *buf, int n)
{
  XiFF *h = opaque;
  if (atomic_load(&h->abort))
    return AVERROR_EXIT;
  int64_t left = h->size - h->pos;
  if (left <= 0)
    return AVERROR_EOF;
  if (n > left)
    n = (int)left;
  memcpy(buf, h->data + h->pos, (size_t)n);
  h->pos += n;
  return n;
}

static int64_t
xi_ff_seek(void *opaque, int64_t offset, int whence)
{
  XiFF *h = opaque;
  int64_t pos;
  switch (whence & ~AVSEEK_FORCE) {
  case AVSEEK_SIZE:
    return h->size;
  case SEEK_SET:
    pos = offset;
    break;
  case SEEK_CUR:
    pos = h->pos + offset;
    break;
  case SEEK_END:
    pos = h->size + offset;
    break;
  default:
    return AVERROR(EINVAL);
  }
  if (pos < 0 || pos > h->size)
    return AVERROR(EINVAL);
  h->pos = pos;
  return pos;
}

static int
xi_ff_interrupt(void *opaque)
{
  XiFF *h = opaque;
  return atomic_load(&h->abort);
}

void
xi_ff_abort(XiFF *h, int on)
{
  atomic_store(&h->abort, on);
}

int
xi_ff_aborted(XiFF *h)
{
  return atomic_load(&h->abort);
}

void
xi_ff_close(XiFF *h)
{
  if (!h)
    return;
  sws_freeContext(h->sws);
  sws_freeContext(h->grey);
  av_frame_free(&h->frame);
  av_frame_free(&h->prev);
  av_frame_free(&h->best);
  av_packet_free(&h->pkt);
  avcodec_free_context(&h->dec);
  // avformat_close_input does not free a custom AVIOContext.
  avformat_close_input(&h->fmt);
  if (h->avio) {
    av_freep(&h->avio->buffer);
    avio_context_free(&h->avio);
  }
  av_free(h);
  atomic_fetch_sub(&xi_ff_live_handles, 1);
}

// Classifies an FFmpeg error of the demuxer or decoder.
static int
xi_ff_code(XiFF *h, int averr)
{
  if (atomic_load(&h->abort) || averr == AVERROR_EXIT)
    return XI_FF_ERR_ABORTED;
  if (averr == AVERROR(ENOMEM))
    return XI_FF_ERR_NOMEM;
  if (averr == AVERROR_DEMUXER_NOT_FOUND || averr == AVERROR_DECODER_NOT_FOUND
      || averr == AVERROR_STREAM_NOT_FOUND || averr == AVERROR_PATCHWELCOME)
    return XI_FF_ERR_UNSUPPORTED;
  return XI_FF_ERR_CORRUPT;
}

// Rotation (clockwise, after the mirror) and mirror of a display matrix,
// the way fftools/ffmpeg applies them.
static void
xi_ff_display(const AVCodecParameters *par, int *rotation, int *hflip)
{
  *rotation = 0;
  *hflip = 0;
  const AVPacketSideData *sd = av_packet_side_data_get(
      par->coded_side_data, par->nb_coded_side_data, AV_PKT_DATA_DISPLAYMATRIX);
  if (!sd || sd->size < 9 * sizeof(int32_t))
    return;
  int32_t m[9];
  memcpy(m, sd->data, sizeof(m));
  if ((int64_t)m[0] * m[4] - (int64_t)m[1] * m[3] < 0) {
    *hflip = 1;
    av_display_matrix_flip(m, 1, 0);
  }
  double theta = -av_display_rotation_get(m);
  if (isnan(theta))
    return;
  theta -= 360 * floor(theta / 360 + 0.9 / 360);
  int r = ((int)lround(theta / 90) % 4) * 90;
  *rotation = r;
}

static int
xi_ff_has_alpha(enum AVPixelFormat f)
{
  const AVPixFmtDescriptor *d = av_pix_fmt_desc_get(f);
  return d && (d->flags & AV_PIX_FMT_FLAG_ALPHA);
}

XiFF *
xi_ff_new(const uint8_t *data, size_t size, int64_t max_pixels)
{
  pthread_once(&xi_ff_once, xi_ff_init);
  XiFF *h = av_mallocz(sizeof(*h));
  if (!h)
    return NULL;
  atomic_fetch_add(&xi_ff_live_handles, 1);
  h->data = data;
  h->size = (int64_t)size;
  h->vidx = -1;
  h->max_pixels = max_pixels;
  uint8_t *iobuf = av_malloc(XI_FF_IO_BUFFER);
  h->fmt = avformat_alloc_context();
  h->pkt = av_packet_alloc();
  h->frame = av_frame_alloc();
  h->prev = av_frame_alloc();
  h->best = av_frame_alloc();
  if (iobuf)
    h->avio = avio_alloc_context(iobuf, XI_FF_IO_BUFFER, 0, h, xi_ff_read, NULL, xi_ff_seek);
  if (!h->avio)
    av_free(iobuf);
  if (!h->avio || !h->fmt || !h->pkt || !h->frame || !h->prev || !h->best) {
    xi_ff_close(h);
    return NULL;
  }
  return h;
}

int
xi_ff_open(XiFF *h, char *err, size_t errlen)
{
  int ret;
  int64_t max_pixels = h->max_pixels;
  h->fmt->pb = h->avio;
  h->fmt->flags |= AVFMT_FLAG_CUSTOM_IO;
  h->fmt->interrupt_callback.callback = xi_ff_interrupt;
  h->fmt->interrupt_callback.opaque = h;

  AVDictionary *opts = NULL;
  av_dict_set(&opts, "format_whitelist", XI_FF_FORMATS, 0);
  av_dict_set(&opts, "codec_whitelist", XI_FF_CODECS, 0);
  av_dict_set(&opts, "protocol_whitelist", XI_FF_PROTOCOLS, 0);
  ret = avformat_open_input(&h->fmt, NULL, NULL, &opts);
  av_dict_free(&opts);
  if (ret < 0) {
    // avformat_open_input freed the context on failure.
    h->fmt = NULL;
    xi_ff_set_err(err, errlen, "open", ret);
    return xi_ff_code(h, ret);
  }

  // Container headers (MP4 track headers, Matroska PixelWidth) usually
  // carry the frame size: refuse oversized streams before decoding anything.
  unsigned n = h->fmt->nb_streams;
  for (unsigned i = 0; max_pixels > 0 && i < n; i++) {
    const AVCodecParameters *par = h->fmt->streams[i]->codecpar;
    if (par->codec_type == AVMEDIA_TYPE_VIDEO && (int64_t)par->width * par->height > max_pixels) {
      xi_ff_set_err(err, errlen, "frame exceeds the pixel limit", 0);
      return XI_FF_ERR_LIMIT;
    }
  }

  // Stream parameters: every decoder the probe opens runs single-threaded
  // and refuses frames over the pixel limit.
  AVDictionary **sopts = n ? av_calloc(n, sizeof(*sopts)) : NULL;
  if (n && !sopts) {
    return XI_FF_ERR_NOMEM;
  }
  for (unsigned i = 0; i < n; i++) {
    av_dict_set(&sopts[i], "threads", "1", 0);
    if (max_pixels > 0)
      av_dict_set_int(&sopts[i], "max_pixels", max_pixels, 0);
  }
  ret = avformat_find_stream_info(h->fmt, sopts);
  for (unsigned i = 0; i < n; i++)
    av_dict_free(&sopts[i]);
  av_free(sopts);
  if (ret < 0) {
    xi_ff_set_err(err, errlen, "stream info", ret);
    int code = xi_ff_code(h, ret);
    return code;
  }

  const AVCodec *codec = NULL;
  ret = av_find_best_stream(h->fmt, AVMEDIA_TYPE_VIDEO, -1, -1, &codec, 0);
  if (ret < 0 || !codec) {
    xi_ff_set_err(err, errlen, ret == AVERROR_DECODER_NOT_FOUND ? "no decoder for the video stream"
                                                                 : "no video stream",
        0);
    return XI_FF_ERR_UNSUPPORTED;
  }
  h->vidx = ret;
  AVStream *st = h->fmt->streams[h->vidx];
  AVCodecParameters *par = st->codecpar;
  if (par->width <= 0 || par->height <= 0) {
    xi_ff_set_err(err, errlen, "video stream without dimensions", 0);
    return XI_FF_ERR_CORRUPT;
  }
  if (max_pixels > 0 && (int64_t)par->width * par->height > max_pixels) {
    xi_ff_set_err(err, errlen, "frame exceeds the pixel limit", 0);
    return XI_FF_ERR_LIMIT;
  }

  h->dec = avcodec_alloc_context3(codec);
  if (!h->dec) {
    return XI_FF_ERR_NOMEM;
  }
  if ((ret = avcodec_parameters_to_context(h->dec, par)) < 0) {
    xi_ff_set_err(err, errlen, "decoder parameters", ret);
    int code = xi_ff_code(h, ret);
    return code;
  }
  h->dec->thread_count = 1;
  h->dec->pkt_timebase = st->time_base;
  if (max_pixels > 0)
    h->dec->max_pixels = max_pixels;
  if ((ret = avcodec_open2(h->dec, codec, NULL)) < 0) {
    xi_ff_set_err(err, errlen, "open decoder", ret);
    int code = xi_ff_code(h, ret);
    if (code == XI_FF_ERR_CORRUPT)
      code = XI_FF_ERR_UNSUPPORTED;
    return code;
  }
  return XI_FF_OK;
}

static double
xi_ff_seconds(int64_t v, AVRational tb)
{
  if (v == AV_NOPTS_VALUE)
    return -1;
  return v * av_q2d(tb);
}

void
xi_ff_info(XiFF *h, XiFFInfo *info)
{
  memset(info, 0, sizeof(*info));
  const char *name = h->fmt->iformat ? h->fmt->iformat->name : "";
  size_t k = strcspn(name, ",");
  if (k >= sizeof(info->container))
    k = sizeof(info->container) - 1;
  memcpy(info->container, name, k);
  info->duration
      = h->fmt->duration == AV_NOPTS_VALUE ? -1 : (double)h->fmt->duration / AV_TIME_BASE;
  info->bit_rate = h->fmt->bit_rate;
  info->nb_streams = (int)h->fmt->nb_streams;
  info->video_index = h->vidx;

  AVStream *st = h->fmt->streams[h->vidx];
  AVCodecParameters *par = st->codecpar;
  snprintf(info->codec, sizeof(info->codec), "%s", avcodec_get_name(par->codec_id));
  info->width = par->width;
  info->height = par->height;
  info->display_width = par->width;
  info->display_height = par->height;
  AVRational sar = av_guess_sample_aspect_ratio(h->fmt, st, NULL);
  if (sar.num > 0 && sar.den > 0 && av_cmp_q(sar, (AVRational) { 1, 1 }) != 0) {
    // Scale the width, like FFmpeg's players and imgproxy.
    int64_t w = av_rescale(par->width, sar.num, sar.den);
    if (w > 0 && w <= INT32_MAX / 2)
      info->display_width = (int)((w + 1) & ~1LL);
  }
  AVRational fr = av_guess_frame_rate(h->fmt, st, NULL);
  if (fr.num > 0 && fr.den > 0)
    info->fps = av_q2d(fr);
  info->frames = st->nb_frames;
  if (info->duration < 0) {
    double d = xi_ff_seconds(st->duration, st->time_base);
    if (d > 0)
      info->duration = d;
  }
  xi_ff_display(par, &info->rotation, &info->hflip);
  info->alpha = xi_ff_has_alpha((enum AVPixelFormat)par->format);
}

int
xi_ff_stream(XiFF *h, int i, XiFFStream *out)
{
  memset(out, 0, sizeof(*out));
  if (i < 0 || (unsigned)i >= h->fmt->nb_streams)
    return 0;
  AVStream *st = h->fmt->streams[i];
  AVCodecParameters *par = st->codecpar;
  out->type = par->codec_type;
  snprintf(out->codec, sizeof(out->codec), "%s", avcodec_get_name(par->codec_id));
  out->bit_rate = par->bit_rate;
  out->duration = xi_ff_seconds(st->duration, st->time_base);
  const AVDictionaryEntry *lang = av_dict_get(st->metadata, "language", NULL, 0);
  if (lang && lang->value && strcmp(lang->value, "und") != 0)
    snprintf(out->language, sizeof(out->language), "%s", lang->value);
  if (par->codec_type == AVMEDIA_TYPE_VIDEO) {
    out->width = par->width;
    out->height = par->height;
    if (st->avg_frame_rate.num > 0 && st->avg_frame_rate.den > 0)
      out->fps = av_q2d(st->avg_frame_rate);
    xi_ff_display(par, &out->rotation, &out->hflip);
  } else if (par->codec_type == AVMEDIA_TYPE_AUDIO) {
    out->sample_rate = par->sample_rate;
    if (par->ch_layout.nb_channels > 0) {
      AVChannelLayout layout = { 0 };
      if (par->ch_layout.order == AV_CHANNEL_ORDER_UNSPEC)
        av_channel_layout_default(&layout, par->ch_layout.nb_channels);
      else
        av_channel_layout_copy(&layout, &par->ch_layout);
      av_channel_layout_describe(&layout, out->layout, sizeof(out->layout));
      av_channel_layout_uninit(&layout);
    }
  }
  return 1;
}

int
xi_ff_tag(XiFF *h, int i, const char **key, const char **value)
{
  const AVDictionaryEntry *e = NULL;
  int n = 0;
  while ((e = av_dict_iterate(h->fmt->metadata, e))) {
    if (n++ == i) {
      *key = e->key;
      *value = e->value;
      return 1;
    }
  }
  return 0;
}

// Luma histogram of f from a grey copy at most XI_FF_HIST_WIDTH wide.
static int
xi_ff_histogram(XiFF *h, const AVFrame *f, uint32_t *hist)
{
  int w = f->width < XI_FF_HIST_WIDTH ? f->width : XI_FF_HIST_WIDTH;
  int ht = (int)av_rescale(f->height, w, f->width);
  if (ht < 1)
    ht = 1;
  h->grey = sws_getCachedContext(h->grey, f->width, f->height, (enum AVPixelFormat)f->format, w,
      ht, AV_PIX_FMT_GRAY8, SWS_AREA, NULL, NULL, NULL);
  if (!h->grey)
    return XI_FF_ERR_UNSUPPORTED;
  uint8_t *buf = av_malloc((size_t)w * ht + 64);
  if (!buf)
    return XI_FF_ERR_NOMEM;
  uint8_t *dst[4] = { buf, NULL, NULL, NULL };
  int stride[4] = { w, 0, 0, 0 };
  sws_scale(h->grey, (const uint8_t *const *)f->data, f->linesize, 0, f->height, dst, stride);
  memset(hist, 0, 256 * sizeof(*hist));
  for (int i = 0; i < w * ht; i++)
    hist[buf[i]]++;
  av_free(buf);
  return XI_FF_OK;
}

int
xi_ff_decode_at(XiFF *h, double seconds, int keyframe, double *got, int *key, uint32_t *hist,
    char *err, size_t errlen)
{
  AVStream *st = h->fmt->streams[h->vidx];
  int64_t start = st->start_time == AV_NOPTS_VALUE ? 0 : st->start_time;
  if (seconds < 0)
    seconds = 0;
  int64_t target = start + av_rescale_q(llrint(seconds * AV_TIME_BASE), AV_TIME_BASE_Q, st->time_base);
  int ret;

  if (atomic_load(&h->abort))
    return XI_FF_ERR_ABORTED;
  av_frame_unref(h->frame);
  av_frame_unref(h->prev);
  // Seek to the keyframe at or before the target; streams without an index
  // (some AVI files) fall back to the start and decode forward.
  ret = av_seek_frame(h->fmt, h->vidx, target, AVSEEK_FLAG_BACKWARD);
  if (ret < 0)
    ret = avformat_seek_file(h->fmt, h->vidx, INT64_MIN, start, start, 0);
  if (ret < 0) {
    xi_ff_set_err(err, errlen, "seek", ret);
    return xi_ff_code(h, ret);
  }
  avcodec_flush_buffers(h->dec);
  h->dec->skip_frame = keyframe ? AVDISCARD_NONKEY : AVDISCARD_DEFAULT;

  int draining = 0, bad = 0, found = 0;
  for (;;) {
    if (atomic_load(&h->abort))
      return XI_FF_ERR_ABORTED;
    ret = avcodec_receive_frame(h->dec, h->frame);
    if (ret == 0) {
      int64_t pts = h->frame->best_effort_timestamp;
      if (keyframe || pts == AV_NOPTS_VALUE || pts >= target) {
        found = 1;
        break;
      }
      // Before the target: keep it in case the stream ends first.
      av_frame_unref(h->prev);
      av_frame_move_ref(h->prev, h->frame);
      continue;
    }
    if (ret == AVERROR_EOF)
      break;
    if (ret != AVERROR(EAGAIN)) {
      xi_ff_set_err(err, errlen, "decode", ret);
      return xi_ff_code(h, ret);
    }
    if (draining) // EAGAIN after a flush cannot happen; be safe
      break;
    ret = av_read_frame(h->fmt, h->pkt);
    if (ret == AVERROR_EOF) {
      draining = 1;
      avcodec_send_packet(h->dec, NULL);
      continue;
    }
    if (ret < 0) {
      xi_ff_set_err(err, errlen, "read", ret);
      return xi_ff_code(h, ret);
    }
    if (h->pkt->stream_index != h->vidx) {
      av_packet_unref(h->pkt);
      continue;
    }
    ret = avcodec_send_packet(h->dec, h->pkt);
    av_packet_unref(h->pkt);
    if (ret == AVERROR_INVALIDDATA) {
      if (++bad > XI_FF_MAX_BAD_PACKETS) {
        xi_ff_set_err(err, errlen, "decode", ret);
        return XI_FF_ERR_CORRUPT;
      }
      continue;
    }
    if (ret < 0 && ret != AVERROR(EAGAIN)) {
      xi_ff_set_err(err, errlen, "decode", ret);
      return xi_ff_code(h, ret);
    }
    bad = 0;
  }
  h->dec->skip_frame = AVDISCARD_DEFAULT;
  if (!found) {
    // Past the end: the last frame before it.
    if (!h->prev->buf[0]) {
      xi_ff_set_err(err, errlen, "no frame decoded", 0);
      return XI_FF_ERR_NOFRAME;
    }
    av_frame_unref(h->frame);
    av_frame_move_ref(h->frame, h->prev);
  }
  av_frame_unref(h->prev);
  if (h->frame->width <= 0 || h->frame->height <= 0) {
    xi_ff_set_err(err, errlen, "empty frame", 0);
    return XI_FF_ERR_CORRUPT;
  }
  if (h->max_pixels > 0 && (int64_t)h->frame->width * h->frame->height > h->max_pixels) {
    xi_ff_set_err(err, errlen, "frame exceeds the pixel limit", 0);
    return XI_FF_ERR_LIMIT;
  }
  int64_t pts = h->frame->best_effort_timestamp;
  *got = pts == AV_NOPTS_VALUE ? seconds : (pts - start) * av_q2d(st->time_base);
  *key = (h->frame->flags & AV_FRAME_FLAG_KEY) != 0;
  if (hist) {
    ret = xi_ff_histogram(h, h->frame, hist);
    if (ret != XI_FF_OK) {
      xi_ff_set_err(err, errlen, "histogram", 0);
      return ret;
    }
  }
  return XI_FF_OK;
}

int
xi_ff_keyframe_before(XiFF *h, double seconds, double *at)
{
  AVStream *st = h->fmt->streams[h->vidx];
  int64_t start = st->start_time == AV_NOPTS_VALUE ? 0 : st->start_time;
  if (seconds < 0)
    seconds = 0;
  int64_t target = start + av_rescale_q(llrint(seconds * AV_TIME_BASE), AV_TIME_BASE_Q, st->time_base);
  int i = av_index_search_timestamp(st, target, AVSEEK_FLAG_BACKWARD);
  if (i < 0)
    return 0;
  const AVIndexEntry *e = avformat_index_get_entry(st, i);
  if (!e || !(e->flags & AVINDEX_KEYFRAME))
    return 0;
  *at = (e->timestamp - start) * av_q2d(st->time_base);
  return 1;
}

void
xi_ff_keep(XiFF *h)
{
  av_frame_unref(h->best);
  av_frame_move_ref(h->best, h->frame);
}

static int
xi_ff_is_full_range(const AVFrame *f)
{
  if (f->color_range == AVCOL_RANGE_JPEG)
    return 1;
  switch (f->format) {
  case AV_PIX_FMT_YUVJ420P:
  case AV_PIX_FMT_YUVJ422P:
  case AV_PIX_FMT_YUVJ444P:
  case AV_PIX_FMT_YUVJ440P:
  case AV_PIX_FMT_YUVJ411P:
    return 1;
  default:
    return 0;
  }
}

int
xi_ff_convert(XiFF *h, int best, uint8_t *dst, int width, int height, int bands, char *err,
    size_t errlen)
{
  const AVFrame *f = best ? h->best : h->frame;
  if (!f->buf[0] || width <= 0 || height <= 0 || (bands != 3 && bands != 4)) {
    xi_ff_set_err(err, errlen, "convert: no frame", 0);
    return XI_FF_ERR_INTERNAL;
  }
  if (atomic_load(&h->abort))
    return XI_FF_ERR_ABORTED;
  // Area averaging for real reductions, bicubic otherwise; exact rounding
  // and full chroma interpolation for quality.
  int flags = (width * 2 <= f->width || height * 2 <= f->height) ? SWS_AREA : SWS_BICUBIC;
  flags |= SWS_ACCURATE_RND | SWS_FULL_CHR_H_INT | SWS_FULL_CHR_H_INP;
  enum AVPixelFormat out = bands == 4 ? AV_PIX_FMT_RGBA : AV_PIX_FMT_RGB24;
  h->sws = sws_getCachedContext(h->sws, f->width, f->height, (enum AVPixelFormat)f->format, width,
      height, out, flags, NULL, NULL, NULL);
  if (!h->sws) {
    xi_ff_set_err(err, errlen, "convert: unsupported pixel format", 0);
    return XI_FF_ERR_UNSUPPORTED;
  }
  // YUV → RGB with the matrix and range the stream declares (unspecified
  // is BT.601, like FFmpeg); ignored for RGB sources.
  int cs = f->colorspace;
  if (cs == AVCOL_SPC_UNSPECIFIED || cs == AVCOL_SPC_RGB)
    cs = SWS_CS_DEFAULT;
  sws_setColorspaceDetails(h->sws, sws_getCoefficients(cs), xi_ff_is_full_range(f),
      sws_getCoefficients(SWS_CS_DEFAULT), 1, 0, 1 << 16, 1 << 16);
  uint8_t *planes[4] = { dst, NULL, NULL, NULL };
  int strides[4] = { width * bands, 0, 0, 0 };
  int rows = sws_scale(
      h->sws, (const uint8_t *const *)f->data, f->linesize, 0, f->height, planes, strides);
  if (rows != height) {
    xi_ff_set_err(err, errlen, "convert: scale failed", rows < 0 ? rows : 0);
    return XI_FF_ERR_INTERNAL;
  }
  return XI_FF_OK;
}

int
xi_ff_live(void)
{
  return atomic_load(&xi_ff_live_handles);
}

size_t
xi_ff_heap_in_use(void)
{
#if defined(__GLIBC__) && (__GLIBC__ > 2 || (__GLIBC__ == 2 && __GLIBC_MINOR__ >= 33))
  return mallinfo2().uordblks;
#else
  return 0;
#endif
}

const char *
xi_ff_version(void)
{
  return av_version_info();
}
