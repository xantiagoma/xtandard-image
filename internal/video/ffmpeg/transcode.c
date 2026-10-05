//go:build ffmpeg && cgo

// Video transformations (WP-121, ADR 0012): a source in C memory is
// demuxed, decoded, run through a libavfilter chain per stream, encoded and
// muxed into a growable buffer (XiFFOut), with no temporary file. The
// chains come from the Go side (internal/video.TranscodePlan), written
// from validated numbers only; the buffer source and sink are configured
// here from the decoders' parameters. Nothing here calls into Go or keeps
// a pointer to Go memory: the parameters are read during xi_tx_run only,
// and the output is handed over with xi_tx_take and freed with
// xi_tx_out_free.

#include "internal.h"
#include "shim.h"

#include <math.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>

#include <libavcodec/avcodec.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libavformat/avformat.h>
#include <libavutil/avstring.h>
#include <libavutil/avutil.h>
#include <libavutil/channel_layout.h>
#include <libavutil/mathematics.h>
#include <libavutil/mem.h>

// Demuxers and decoders a source may use; no protocol at all (every byte
// comes from the custom AVIOContext).
#define XI_TX_FORMATS "mov,matroska,avi"
#define XI_TX_CODECS                                                                               \
  "h264,hevc,vp8,vp9,libdav1d,mpeg4,mjpeg,png,rawvideo,aac,aac_fixed,opus,libopus,mp3,mp3float,"   \
  "vorbis,flac,alac,pcm_s16le,pcm_s16be,pcm_s24le,pcm_s32le,pcm_f32le"
#define XI_TX_IO (64 * 1024)
// Packets a decoder may reject in a row before the source is corrupt.
#define XI_TX_MAX_BAD_PACKETS 64
// Seconds of packets read past the cut's end before a stream stops: the
// decoder and B-frame reordering may still hold frames inside the cut.
#define XI_TX_END_SLACK 1.0
// Frames whose duration the video encoder may still hold (reordering).
#define XI_TX_DURATIONS 256

typedef struct {
  int index; // input stream; -1 when none
  AVCodecContext *dec;
  AVFilterGraph *graph;
  AVFilterContext *src, *sink;
  AVCodecContext *enc;
  AVStream *ost;
  int active;  // this stream is transcoded
  int in_done; // the decoder is flushed and the buffer source closed
  int bad;     // packets rejected in a row
  int64_t packets;
  // Video: the duration of the frames sent (encoder time base), to give
  // each packet its frame's.
  int64_t pts[XI_TX_DURATIONS], dur[XI_TX_DURATIONS];
  int ndur;
  int64_t frame_dur;
} XiTxStream;

struct XiTx {
  AVFormatContext *fmt;
  AVIOContext *avio;
  const uint8_t *data;
  int64_t size, pos;
  atomic_int abort;
  int64_t max_pixels;
  int threads;
  int audio_decodable;
  XiTxStream v, a;
  AVFormatContext *oc;
  XiFFOut out;
  AVPacket *pkt, *opkt;
  AVFrame *frame, *filt;
};

static atomic_int xi_tx_handles;
static atomic_int xi_tx_outputs;

int
xi_tx_live(void)
{
  return atomic_load(&xi_tx_handles);
}

int
xi_tx_live_outputs(void)
{
  return atomic_load(&xi_tx_outputs);
}

static int
xi_tx_has_filters(const char *const *names)
{
  for (; *names; names++)
    if (!avfilter_get_by_name(*names))
      return 0;
  return 1;
}

int
xi_tx_caps(void)
{
  xi_ff_setup();
  static const char *const vf[] = { "buffer", "buffersink", "null", "trim", "setpts", "scale",
    "crop", "pad", "transpose", "hflip", "vflip", "fps", "format", NULL };
  static const char *const af[]
      = { "abuffer", "abuffersink", "atrim", "asetpts", "aresample", "aformat", NULL };
  int m = 0;
  if (avcodec_find_encoder_by_name("aac"))
    m |= XI_TX_CAP_AAC;
  if (avcodec_find_encoder_by_name("libopus"))
    m |= XI_TX_CAP_OPUS;
  if (avcodec_find_encoder_by_name("libmp3lame"))
    m |= XI_TX_CAP_MP3;
  if (av_guess_format("mp4", NULL, NULL))
    m |= XI_TX_CAP_MUX_MP4;
  if (av_guess_format("webm", NULL, NULL))
    m |= XI_TX_CAP_MUX_WEBM;
  if (av_guess_format("ogg", NULL, NULL))
    m |= XI_TX_CAP_MUX_OGG;
  if (av_guess_format("mp3", NULL, NULL))
    m |= XI_TX_CAP_MUX_MP3;
  if (xi_tx_has_filters(vf))
    m |= XI_TX_CAP_VFILTERS;
  if (xi_tx_has_filters(af))
    m |= XI_TX_CAP_AFILTERS;
  return m;
}

// ---------------------------------------------------------------------------
// Input

static int
xi_tx_read(void *opaque, uint8_t *buf, int n)
{
  XiTx *h = opaque;
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
xi_tx_seek(void *opaque, int64_t offset, int whence)
{
  XiTx *h = opaque;
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
xi_tx_interrupt(void *opaque)
{
  XiTx *h = opaque;
  return atomic_load(&h->abort);
}

void
xi_tx_abort(XiTx *h, int on)
{
  atomic_store(&h->abort, on);
}

static void
xi_tx_stream_free(XiTxStream *s)
{
  avfilter_graph_free(&s->graph);
  avcodec_free_context(&s->dec);
  avcodec_free_context(&s->enc);
  s->src = s->sink = NULL;
  s->ost = NULL;
}

void
xi_tx_free(XiTx *h)
{
  if (!h)
    return;
  xi_tx_stream_free(&h->v);
  xi_tx_stream_free(&h->a);
  av_frame_free(&h->frame);
  av_frame_free(&h->filt);
  av_packet_free(&h->pkt);
  av_packet_free(&h->opkt);
  if (h->oc) {
    xi_ff_out_detach(h->oc);
    avformat_free_context(h->oc);
  }
  xi_ff_out_free(&h->out);
  avformat_close_input(&h->fmt);
  if (h->avio) {
    av_freep(&h->avio->buffer);
    avio_context_free(&h->avio);
  }
  av_free(h);
  atomic_fetch_sub(&xi_tx_handles, 1);
}

XiTx *
xi_tx_new(const uint8_t *data, size_t size, int64_t max_pixels, int threads)
{
  xi_ff_setup();
  XiTx *h = av_mallocz(sizeof(*h));
  if (!h)
    return NULL;
  atomic_fetch_add(&xi_tx_handles, 1);
  h->data = data;
  h->size = (int64_t)size;
  h->max_pixels = max_pixels;
  h->threads = threads > 0 ? threads : 1;
  h->v.index = h->a.index = -1;
  h->out.abort = &h->abort;
  uint8_t *iobuf = av_malloc(XI_TX_IO);
  h->fmt = avformat_alloc_context();
  h->pkt = av_packet_alloc();
  h->opkt = av_packet_alloc();
  h->frame = av_frame_alloc();
  h->filt = av_frame_alloc();
  if (iobuf)
    h->avio = avio_alloc_context(iobuf, XI_TX_IO, 0, h, xi_tx_read, NULL, xi_tx_seek);
  if (!h->avio)
    av_free(iobuf);
  if (!h->avio || !h->fmt || !h->pkt || !h->opkt || !h->frame || !h->filt) {
    xi_tx_free(h);
    return NULL;
  }
  return h;
}

// Classifies an FFmpeg error of the demuxer or a decoder.
static int
xi_tx_code(XiTx *h, int averr)
{
  if (atomic_load(&h->abort) || averr == AVERROR_EXIT)
    return XI_FF_ERR_ABORTED;
  if (h->out.over)
    return XI_FF_ERR_LIMIT;
  if (averr == AVERROR(ENOMEM))
    return XI_FF_ERR_NOMEM;
  if (averr == AVERROR_DEMUXER_NOT_FOUND || averr == AVERROR_DECODER_NOT_FOUND
      || averr == AVERROR_STREAM_NOT_FOUND || averr == AVERROR_PATCHWELCOME
      || averr == AVERROR_ENCODER_NOT_FOUND || averr == AVERROR_MUXER_NOT_FOUND
      || averr == AVERROR_FILTER_NOT_FOUND)
    return XI_FF_ERR_UNSUPPORTED;
  return XI_FF_ERR_CORRUPT;
}

// Opens the decoder of stream s->index with threads threads.
static int
xi_tx_open_decoder(XiTx *h, XiTxStream *s, const AVCodec *codec, int threads)
{
  AVStream *st = h->fmt->streams[s->index];
  s->dec = avcodec_alloc_context3(codec);
  if (!s->dec)
    return AVERROR(ENOMEM);
  int ret = avcodec_parameters_to_context(s->dec, st->codecpar);
  if (ret < 0)
    return ret;
  s->dec->pkt_timebase = st->time_base;
  AVDictionary *opts = NULL;
  av_dict_set_int(&opts, "threads", threads, 0);
  av_dict_set(&opts, "codec_whitelist", XI_TX_CODECS, 0);
  if (h->max_pixels > 0)
    av_dict_set_int(&opts, "max_pixels", h->max_pixels, 0);
  ret = avcodec_open2(s->dec, codec, &opts);
  av_dict_free(&opts);
  return ret;
}

int
xi_tx_open(XiTx *h, XiTxInfo *info, char *err, size_t errlen)
{
  memset(info, 0, sizeof(*info));
  h->fmt->pb = h->avio;
  h->fmt->flags |= AVFMT_FLAG_CUSTOM_IO;
  h->fmt->interrupt_callback.callback = xi_tx_interrupt;
  h->fmt->interrupt_callback.opaque = h;

  AVDictionary *opts = NULL;
  av_dict_set(&opts, "format_whitelist", XI_TX_FORMATS, 0);
  av_dict_set(&opts, "codec_whitelist", XI_TX_CODECS, 0);
  av_dict_set(&opts, "protocol_whitelist", "none", 0);
  int ret = avformat_open_input(&h->fmt, NULL, NULL, &opts);
  av_dict_free(&opts);
  if (ret < 0) {
    h->fmt = NULL; // freed by avformat_open_input
    xi_ff_set_err(err, errlen, "open", ret);
    return xi_tx_code(h, ret);
  }
  unsigned n = h->fmt->nb_streams;
  for (unsigned i = 0; h->max_pixels > 0 && i < n; i++) {
    const AVCodecParameters *par = h->fmt->streams[i]->codecpar;
    if (par->codec_type == AVMEDIA_TYPE_VIDEO
        && (int64_t)par->width * par->height > h->max_pixels) {
      xi_ff_set_err(err, errlen, "frame exceeds the pixel limit", 0);
      return XI_FF_ERR_LIMIT;
    }
  }
  AVDictionary **sopts = n ? av_calloc(n, sizeof(*sopts)) : NULL;
  if (n && !sopts)
    return XI_FF_ERR_NOMEM;
  for (unsigned i = 0; i < n; i++) {
    av_dict_set(&sopts[i], "threads", "1", 0);
    if (h->max_pixels > 0)
      av_dict_set_int(&sopts[i], "max_pixels", h->max_pixels, 0);
  }
  ret = avformat_find_stream_info(h->fmt, sopts);
  for (unsigned i = 0; i < n; i++)
    av_dict_free(&sopts[i]);
  av_free(sopts);
  if (ret < 0) {
    xi_ff_set_err(err, errlen, "stream info", ret);
    return xi_tx_code(h, ret);
  }

  snprintf(info->container, sizeof(info->container), "%s", h->fmt->iformat->name);
  char *comma = strchr(info->container, ',');
  if (comma)
    *comma = 0;
  info->duration = h->fmt->duration > 0 ? (double)h->fmt->duration / AV_TIME_BASE : -1;
  info->start_time
      = h->fmt->start_time != AV_NOPTS_VALUE ? (double)h->fmt->start_time / AV_TIME_BASE : 0;

  const AVCodec *vcodec = NULL;
  ret = av_find_best_stream(h->fmt, AVMEDIA_TYPE_VIDEO, -1, -1, &vcodec, 0);
  if (ret >= 0 && vcodec) {
    h->v.index = ret;
    AVStream *st = h->fmt->streams[ret];
    AVCodecParameters *par = st->codecpar;
    if (par->width <= 0 || par->height <= 0) {
      xi_ff_set_err(err, errlen, "video stream without dimensions", 0);
      return XI_FF_ERR_CORRUPT;
    }
    info->video = 1;
    snprintf(info->vcodec, sizeof(info->vcodec), "%s", avcodec_get_name(par->codec_id));
    info->width = par->width;
    info->height = par->height;
    AVRational sar = av_guess_sample_aspect_ratio(h->fmt, st, NULL);
    info->sar_num = sar.num;
    info->sar_den = sar.den;
    xi_ff_display(par, &info->rotation, &info->hflip);
    AVRational fr = st->avg_frame_rate.num > 0 ? st->avg_frame_rate : st->r_frame_rate;
    info->fps = fr.num > 0 && fr.den > 0 ? av_q2d(fr) : 0;
    if ((ret = xi_tx_open_decoder(h, &h->v, vcodec, h->threads)) < 0) {
      xi_ff_set_err(err, errlen, "open video decoder", ret);
      return xi_tx_code(h, ret);
    }
  }
  ret = av_find_best_stream(h->fmt, AVMEDIA_TYPE_AUDIO, -1, h->v.index, NULL, 0);
  if (ret >= 0) {
    h->a.index = ret;
    AVCodecParameters *par = h->fmt->streams[ret]->codecpar;
    info->audio = 1;
    snprintf(info->acodec, sizeof(info->acodec), "%s", avcodec_get_name(par->codec_id));
    info->sample_rate = par->sample_rate;
    info->channels = par->ch_layout.nb_channels;
    // An audio stream this build cannot decode is reported, not fatal:
    // the plan refuses it unless the request mutes the audio.
    const AVCodec *acodec = avcodec_find_decoder(par->codec_id);
    if (acodec && av_match_list(acodec->name, XI_TX_CODECS, ',') > 0 && par->sample_rate > 0
        && par->ch_layout.nb_channels > 0 && xi_tx_open_decoder(h, &h->a, acodec, 1) >= 0)
      h->audio_decodable = 1;
    else
      avcodec_free_context(&h->a.dec);
    info->audio_decodable = h->audio_decodable;
  }
  if (!info->video && !info->audio) {
    xi_ff_set_err(err, errlen, "no video or audio stream", 0);
    return XI_FF_ERR_UNSUPPORTED;
  }
  return XI_FF_OK;
}

// ---------------------------------------------------------------------------
// Filters and encoders

// Builds s's graph: a buffer source configured from the decoder, chain,
// and a buffer sink.
static int
xi_tx_graph(XiTx *h, XiTxStream *s, int audio, const char *chain, int threads)
{
  AVStream *st = h->fmt->streams[s->index];
  AVCodecContext *dec = s->dec;
  s->graph = avfilter_graph_alloc();
  if (!s->graph)
    return AVERROR(ENOMEM);
  s->graph->nb_threads = threads;
  const AVFilter *fsrc = avfilter_get_by_name(audio ? "abuffer" : "buffer");
  const AVFilter *fsink = avfilter_get_by_name(audio ? "abuffersink" : "buffersink");
  if (!fsrc || !fsink)
    return AVERROR_FILTER_NOT_FOUND;
  s->src = avfilter_graph_alloc_filter(s->graph, fsrc, "in");
  s->sink = avfilter_graph_alloc_filter(s->graph, fsink, "out");
  AVBufferSrcParameters *par = av_buffersrc_parameters_alloc();
  if (!s->src || !s->sink || !par) {
    av_free(par);
    return AVERROR(ENOMEM);
  }
  par->time_base = st->time_base;
  int ret;
  if (audio) {
    par->format = dec->sample_fmt;
    par->sample_rate = dec->sample_rate;
    if (dec->ch_layout.order == AV_CHANNEL_ORDER_UNSPEC)
      av_channel_layout_default(&par->ch_layout, dec->ch_layout.nb_channels);
    else if ((ret = av_channel_layout_copy(&par->ch_layout, &dec->ch_layout)) < 0) {
      av_free(par);
      return ret;
    }
  } else {
    par->format = dec->pix_fmt;
    par->width = dec->width;
    par->height = dec->height;
    par->sample_aspect_ratio = av_guess_sample_aspect_ratio(h->fmt, st, NULL);
    if (par->sample_aspect_ratio.num <= 0 || par->sample_aspect_ratio.den <= 0)
      par->sample_aspect_ratio = (AVRational) { 1, 1 };
    par->color_space = dec->colorspace;
    par->color_range = dec->color_range;
  }
  if (par->format < 0) {
    av_channel_layout_uninit(&par->ch_layout);
    av_free(par);
    return AVERROR_INVALIDDATA;
  }
  ret = av_buffersrc_parameters_set(s->src, par);
  av_channel_layout_uninit(&par->ch_layout);
  av_free(par);
  if (ret < 0 || (ret = avfilter_init_str(s->src, NULL)) < 0
      || (ret = avfilter_init_str(s->sink, NULL)) < 0)
    return ret;

  AVFilterInOut *outputs = avfilter_inout_alloc();
  AVFilterInOut *inputs = avfilter_inout_alloc();
  if (!outputs || !inputs) {
    avfilter_inout_free(&outputs);
    avfilter_inout_free(&inputs);
    return AVERROR(ENOMEM);
  }
  outputs->name = av_strdup("in");
  outputs->filter_ctx = s->src;
  inputs->name = av_strdup("out");
  inputs->filter_ctx = s->sink;
  ret = avfilter_graph_parse_ptr(s->graph, chain, &inputs, &outputs, NULL);
  avfilter_inout_free(&outputs);
  avfilter_inout_free(&inputs);
  if (ret < 0)
    return ret;
  return avfilter_graph_config(s->graph, NULL);
}

static const char *
xi_tx_vencoder(int codec)
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

static const char *
xi_tx_aencoder(int codec)
{
  switch (codec) {
  case XI_TX_AAC:
    return "aac";
  case XI_TX_OPUS:
    return "libopus";
  case XI_TX_MP3:
    return "libmp3lame";
  default:
    return NULL;
  }
}

// Adds the encoder and the output stream of s, configured from its sink.
static int
xi_tx_encoder(XiTx *h, XiTxStream *s, const char *name, const XiTxParams *p, int audio)
{
  const AVCodec *codec = name ? avcodec_find_encoder_by_name(name) : NULL;
  if (!codec)
    return AVERROR_ENCODER_NOT_FOUND;
  AVCodecContext *c = s->enc = avcodec_alloc_context3(codec);
  if (!c)
    return AVERROR(ENOMEM);
  AVDictionary *opts = NULL;
  int ret;
  c->flags |= AV_CODEC_FLAG_BITEXACT;
  if (audio) {
    c->sample_fmt = av_buffersink_get_format(s->sink);
    c->sample_rate = av_buffersink_get_sample_rate(s->sink);
    if ((ret = av_buffersink_get_ch_layout(s->sink, &c->ch_layout)) < 0)
      return ret;
    c->time_base = (AVRational) { 1, c->sample_rate };
    c->bit_rate = p->abitrate;
    c->thread_count = 1;
  } else {
    c->width = av_buffersink_get_w(s->sink);
    c->height = av_buffersink_get_h(s->sink);
    c->pix_fmt = av_buffersink_get_format(s->sink);
    c->sample_aspect_ratio = (AVRational) { 1, 1 };
    c->time_base = av_buffersink_get_time_base(s->sink);
    AVRational fr = av_buffersink_get_frame_rate(s->sink);
    c->framerate = fr.num > 0 && fr.den > 0 ? fr : av_d2q(p->fps > 0 ? p->fps : 25, 100000);
    s->frame_dur = av_rescale_q(1, av_inv_q(c->framerate), c->time_base);
    c->gop_size = p->gop;
    c->thread_count = p->threads;
    c->bit_rate = 0;
    c->color_range = av_buffersink_get_color_range(s->sink);
    if (c->color_range == AVCOL_RANGE_UNSPECIFIED)
      c->color_range = AVCOL_RANGE_MPEG;
    c->colorspace = av_buffersink_get_colorspace(s->sink);
    c->color_primaries = s->dec->color_primaries;
    c->color_trc = s->dec->color_trc;
    av_dict_set_int(&opts, "crf", p->crf, 0);
    av_dict_set_int(&opts, "threads", p->threads, 0);
    switch (p->vcodec) {
    case XI_FF_ENC_VP9:
      av_dict_set(&opts, "deadline", "realtime", 0);
      av_dict_set_int(&opts, "cpu-used", p->speed, 0);
      av_dict_set(&opts, "row-mt", p->threads > 1 ? "1" : "0", 0);
      c->bit_rate = p->max_bitrate; // with crf: constrained quality
      break;
    case XI_FF_ENC_AV1:
      av_dict_set(&opts, "usage", "realtime", 0);
      av_dict_set_int(&opts, "cpu-used", p->speed, 0);
      av_dict_set(&opts, "row-mt", p->threads > 1 ? "1" : "0", 0);
      c->bit_rate = p->max_bitrate;
      break;
    case XI_FF_ENC_H264:
      av_dict_set(&opts, "preset", p->preset, 0);
      av_dict_set(&opts, "profile", "high", 0);
      if (p->max_bitrate > 0) {
        c->rc_max_rate = p->max_bitrate;
        c->rc_buffer_size = (int)FFMIN(p->max_bitrate * 2, INT32_MAX);
      }
      break;
    case XI_FF_ENC_HEVC: {
      char x265[128];
      if (p->max_bitrate > 0)
        snprintf(x265, sizeof(x265),
            "log-level=none:pools=none:frame-threads=1:vbv-maxrate=%lld:vbv-bufsize=%lld",
            (long long)(p->max_bitrate / 1000), (long long)(p->max_bitrate * 2 / 1000));
      else
        snprintf(x265, sizeof(x265), "log-level=none:pools=none:frame-threads=1");
      av_dict_set(&opts, "preset", p->preset, 0);
      av_dict_set(&opts, "x265-params", x265, 0);
      break;
    }
    }
  }
  if (h->oc->oformat->flags & AVFMT_GLOBALHEADER)
    c->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
  ret = avcodec_open2(c, codec, &opts);
  av_dict_free(&opts);
  if (ret < 0)
    return ret;
  if (audio && c->frame_size > 0 && !(codec->capabilities & AV_CODEC_CAP_VARIABLE_FRAME_SIZE))
    av_buffersink_set_frame_size(s->sink, (unsigned)c->frame_size);
  s->ost = avformat_new_stream(h->oc, NULL);
  if (!s->ost)
    return AVERROR(ENOMEM);
  s->ost->time_base = c->time_base;
  if (!audio)
    s->ost->avg_frame_rate = c->framerate;
  if ((ret = avcodec_parameters_from_context(s->ost->codecpar, c)) < 0)
    return ret;
  if (!audio && p->vcodec == XI_FF_ENC_HEVC) // Apple players want hvc1
    s->ost->codecpar->codec_tag = MKTAG('h', 'v', 'c', '1');
  s->active = 1;
  return 0;
}

// Muxes every packet s's encoder has ready (frame NULL flushes it first;
// otherwise frame is sent).
static int
xi_tx_encode(XiTx *h, XiTxStream *s, AVFrame *frame)
{
  int ret = avcodec_send_frame(s->enc, frame);
  if (ret < 0 && !(frame == NULL && ret == AVERROR_EOF))
    return ret;
  for (;;) {
    if (atomic_load(&h->abort))
      return AVERROR_EXIT;
    ret = avcodec_receive_packet(s->enc, h->opkt);
    if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
      return 0;
    if (ret < 0)
      return ret;
    s->packets++;
    if (s->enc->codec_type == AVMEDIA_TYPE_VIDEO && h->opkt->duration <= 0) {
      h->opkt->duration = s->frame_dur;
      for (int i = 0; i < s->ndur && i < XI_TX_DURATIONS; i++) {
        int k = (s->ndur - 1 - i) % XI_TX_DURATIONS;
        if (s->pts[k] == h->opkt->pts) {
          if (s->dur[k] > 0)
            h->opkt->duration = s->dur[k];
          break;
        }
      }
    }
    av_packet_rescale_ts(h->opkt, s->enc->time_base, s->ost->time_base);
    h->opkt->stream_index = s->ost->index;
    if ((ret = av_interleaved_write_frame(h->oc, h->opkt)) < 0)
      return ret;
  }
}

// Encodes every frame s's sink has ready.
static int
xi_tx_pull(XiTx *h, XiTxStream *s)
{
  AVRational tb = av_buffersink_get_time_base(s->sink);
  for (;;) {
    if (atomic_load(&h->abort))
      return AVERROR_EXIT;
    int ret = av_buffersink_get_frame(s->sink, h->filt);
    if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
      return 0;
    if (ret < 0)
      return ret;
    AVFrame *f = h->filt;
    if (f->pts != AV_NOPTS_VALUE)
      f->pts = av_rescale_q(f->pts, tb, s->enc->time_base);
    if (f->duration > 0)
      f->duration = av_rescale_q(f->duration, tb, s->enc->time_base);
    if (s->enc->codec_type == AVMEDIA_TYPE_VIDEO) {
      f->pict_type = AV_PICTURE_TYPE_NONE;
      int k = s->ndur % XI_TX_DURATIONS;
      s->pts[k] = f->pts;
      s->dur[k] = f->duration;
      s->ndur++;
    }
    ret = xi_tx_encode(h, s, f);
    av_frame_unref(f);
    if (ret < 0)
      return ret;
  }
}

// Sends a decoded frame (NULL: end of stream) through s's graph and
// encodes what comes out. A source the graph no longer accepts (the cut
// ended) drops it.
static int
xi_tx_filter(XiTx *h, XiTxStream *s, AVFrame *frame)
{
  if (av_buffersrc_get_status(s->src) < 0) {
    if (frame)
      av_frame_unref(frame);
    return xi_tx_pull(h, s);
  }
  if (frame)
    frame->pts = frame->best_effort_timestamp;
  int ret = av_buffersrc_add_frame(s->src, frame);
  if (ret == AVERROR_EOF)
    ret = 0;
  if (ret < 0) {
    if (frame)
      av_frame_unref(frame);
    return ret;
  }
  return xi_tx_pull(h, s);
}

// Decodes a packet (NULL: flush) of s and filters and encodes its frames.
static int
xi_tx_decode(XiTx *h, XiTxStream *s, const AVPacket *pkt)
{
  int ret = avcodec_send_packet(s->dec, pkt);
  if (ret < 0 && ret != AVERROR_EOF) {
    if (ret == AVERROR(ENOMEM) || ret == AVERROR_EXIT || atomic_load(&h->abort))
      return ret;
    if (++s->bad > XI_TX_MAX_BAD_PACKETS)
      return ret;
    return 0; // a damaged packet: skip it, like ffmpeg(1)
  }
  if (pkt)
    s->bad = 0;
  for (;;) {
    ret = avcodec_receive_frame(s->dec, h->frame);
    if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF)
      return 0;
    if (ret < 0) {
      if (ret == AVERROR(ENOMEM) || ret == AVERROR_EXIT || atomic_load(&h->abort))
        return ret;
      if (++s->bad > XI_TX_MAX_BAD_PACKETS)
        return ret;
      continue;
    }
    if ((ret = xi_tx_filter(h, s, h->frame)) < 0)
      return ret;
  }
}

// Ends s's input: flushes its decoder, closes its graph and flushes its
// encoder.
static int
xi_tx_finish(XiTx *h, XiTxStream *s)
{
  if (!s->active || s->in_done)
    return 0;
  s->in_done = 1;
  int ret;
  if (av_buffersrc_get_status(s->src) >= 0 && (ret = xi_tx_decode(h, s, NULL)) < 0)
    return ret;
  if ((ret = xi_tx_filter(h, s, NULL)) < 0)
    return ret;
  return xi_tx_encode(h, s, NULL);
}

static int
xi_tx_fail(XiTx *h, int averr, const char *what, char *err, size_t errlen)
{
  xi_ff_set_err(err, errlen, what, averr);
  return xi_tx_code(h, averr);
}

int
xi_tx_run(XiTx *h, const XiTxParams *p, char *err, size_t errlen)
{
  if (atomic_load(&h->abort))
    return XI_FF_ERR_ABORTED;
  if (h->oc) {
    xi_ff_set_err(err, errlen, "transcode ran twice", 0);
    return XI_FF_ERR_INTERNAL;
  }
  char muxer[sizeof(p->muxer) + 1] = { 0 };
  memcpy(muxer, p->muxer, sizeof(p->muxer));
  int ret = avformat_alloc_output_context2(&h->oc, NULL, muxer, NULL);
  if (ret < 0 || !h->oc) {
    h->oc = NULL;
    return xi_tx_fail(h, ret < 0 ? ret : AVERROR_MUXER_NOT_FOUND, "muxer", err, errlen);
  }
  h->out.max_bytes = p->max_bytes;
  if ((ret = xi_ff_out_attach(h->oc, &h->out)) < 0)
    return xi_tx_fail(h, ret, "output", err, errlen);
  h->oc->interrupt_callback.callback = xi_tx_interrupt;
  h->oc->interrupt_callback.opaque = h;
  h->oc->flags |= AVFMT_FLAG_BITEXACT; // no library version strings

  char chain[sizeof(p->vfilters) + 1] = { 0 };
  if (p->vcodec) {
    if (h->v.index < 0 || !h->v.dec) {
      xi_ff_set_err(err, errlen, "no video stream", 0);
      return XI_FF_ERR_UNSUPPORTED;
    }
    memcpy(chain, p->vfilters, sizeof(p->vfilters));
    if ((ret = xi_tx_graph(h, &h->v, 0, chain, p->threads)) < 0)
      return xi_tx_fail(h, ret, "video filters", err, errlen);
    if ((ret = xi_tx_encoder(h, &h->v, xi_tx_vencoder(p->vcodec), p, 0)) < 0)
      return xi_tx_fail(h, ret, "video encoder", err, errlen);
  }
  if (p->acodec) {
    if (h->a.index < 0 || !h->audio_decodable) {
      xi_ff_set_err(err, errlen, "no decodable audio stream", 0);
      return XI_FF_ERR_UNSUPPORTED;
    }
    memset(chain, 0, sizeof(chain));
    memcpy(chain, p->afilters, sizeof(p->afilters));
    if ((ret = xi_tx_graph(h, &h->a, 1, chain, 1)) < 0)
      return xi_tx_fail(h, ret, "audio filters", err, errlen);
    if ((ret = xi_tx_encoder(h, &h->a, xi_tx_aencoder(p->acodec), p, 1)) < 0)
      return xi_tx_fail(h, ret, "audio encoder", err, errlen);
  }
  if (!h->v.active && !h->a.active) {
    xi_ff_set_err(err, errlen, "nothing to transcode", 0);
    return XI_FF_ERR_INTERNAL;
  }

  AVDictionary *mopts = NULL;
  if (p->faststart)
    av_dict_set(&mopts, "movflags", "+faststart", 0);
  if (!strcmp(muxer, "webm")) // the index first, for players that read ranges
    av_dict_set(&mopts, "cues_to_front", "1", 0);
  ret = avformat_write_header(h->oc, &mopts);
  av_dict_free(&mopts);
  if (ret < 0)
    return xi_tx_fail(h, ret, "write header", err, errlen);

  if (p->start > 0) {
    // The keyframe at or before the cut; trim drops what precedes it.
    int64_t ts = (int64_t)llround(p->start * AV_TIME_BASE);
    if (avformat_seek_file(h->fmt, -1, INT64_MIN, ts, ts, 0) < 0 && atomic_load(&h->abort))
      return XI_FF_ERR_ABORTED;
  }

  for (;;) {
    if (atomic_load(&h->abort))
      return XI_FF_ERR_ABORTED;
    if ((!h->v.active || h->v.in_done) && (!h->a.active || h->a.in_done))
      break;
    ret = av_read_frame(h->fmt, h->pkt);
    if (ret < 0) {
      if (ret == AVERROR_EXIT || atomic_load(&h->abort))
        return XI_FF_ERR_ABORTED;
      break; // end of the source (a truncated file ends here too)
    }
    XiTxStream *s = NULL;
    if (h->v.active && h->pkt->stream_index == h->v.index)
      s = &h->v;
    else if (h->a.active && h->pkt->stream_index == h->a.index)
      s = &h->a;
    if (!s || s->in_done) {
      av_packet_unref(h->pkt);
      continue;
    }
    if (p->end > 0) {
      int64_t ts = h->pkt->dts != AV_NOPTS_VALUE ? h->pkt->dts : h->pkt->pts;
      AVRational tb = h->fmt->streams[s->index]->time_base;
      if (ts != AV_NOPTS_VALUE && ts * av_q2d(tb) > p->end + XI_TX_END_SLACK) {
        av_packet_unref(h->pkt);
        if ((ret = xi_tx_finish(h, s)) < 0)
          return xi_tx_fail(h, ret, "finish", err, errlen);
        continue;
      }
    }
    ret = xi_tx_decode(h, s, h->pkt);
    av_packet_unref(h->pkt);
    if (ret < 0)
      return xi_tx_fail(h, ret, "transcode", err, errlen);
    if (av_buffersrc_get_status(s->src) < 0 && (ret = xi_tx_finish(h, s)) < 0)
      return xi_tx_fail(h, ret, "finish", err, errlen);
  }
  if ((ret = xi_tx_finish(h, &h->v)) < 0 || (ret = xi_tx_finish(h, &h->a)) < 0)
    return xi_tx_fail(h, ret, "flush", err, errlen);
  if (h->v.active && h->v.packets == 0) {
    xi_ff_set_err(err, errlen, "no video frame in the cut", 0);
    return XI_FF_ERR_NOFRAME;
  }
  if (!h->v.active && h->a.packets == 0) {
    xi_ff_set_err(err, errlen, "no audio in the cut", 0);
    return XI_FF_ERR_NOFRAME;
  }
  if ((ret = av_write_trailer(h->oc)) < 0)
    return xi_tx_fail(h, ret, "write trailer", err, errlen);
  avio_flush(h->oc->pb);
  if (h->oc->pb->error < 0)
    return xi_tx_fail(h, h->oc->pb->error, "write", err, errlen);
  return XI_FF_OK;
}

uint8_t *
xi_tx_take(XiTx *h, int64_t *size)
{
  *size = h->out.size;
  if (!h->out.data || h->out.size <= 0)
    return NULL;
  uint8_t *data = h->out.data;
  // Give the slack of the last doubling back.
  uint8_t *shrunk = av_realloc(data, (size_t)h->out.size);
  if (shrunk)
    data = shrunk;
  h->out.data = NULL;
  h->out.size = h->out.cap = h->out.pos = 0;
  atomic_fetch_add(&xi_tx_outputs, 1);
  return data;
}

void
xi_tx_out_free(uint8_t *data)
{
  if (!data)
    return;
  av_free(data);
  atomic_fetch_sub(&xi_tx_outputs, 1);
}
