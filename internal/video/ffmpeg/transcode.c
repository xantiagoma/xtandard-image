//go:build ffmpeg && cgo

// Video transformations (WP-121, ADR 0012): a source in C memory (or, past
// the spool threshold, a spool file read with pread through a duplicated
// descriptor, WP-121b) is demuxed, decoded, run through a libavfilter
// chain per stream, encoded and muxed into a growable buffer (XiFFOut),
// which moves into a spool file the Go side opened when it outgrows
// XiTxParams.spill_bytes. FFmpeg never opens a file or URL itself. The
// chains come from the Go side (internal/video.TranscodePlan), written
// from validated numbers only; the buffer source and sink are configured
// here from the decoders' parameters. Overlays (WP-124) run in a second
// graph the shim builds filter by filter with AVOption values (see
// "Overlays" below). Nothing here calls into Go or keeps a pointer to Go
// memory: the parameters (overlay pixels included, copied into frames)
// are read during xi_tx_run only, and the output is handed over with
// xi_tx_take and freed with xi_tx_out_free.

#include "internal.h"
#include "shim.h"

#include <math.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>

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
#include <libavutil/opt.h>
#include <libavutil/pixdesc.h>

// Demuxers and decoders a source may use; no protocol at all (every byte
// comes from the custom AVIOContext).
// The audio demuxers (mp3, aac, ac3, eac3, ogg, flac, wav) admit audio
// sources for audio-only outputs and waveforms (WP-123).
#define XI_TX_FORMATS "mov,matroska,avi,mp3,aac,ac3,eac3,ogg,flac,wav"
#define XI_TX_CODECS                                                                               \
  "h264,hevc,vp8,vp9,libdav1d,mpeg4,mjpeg,png,rawvideo,aac,aac_fixed,aac_latm,opus,libopus,mp3,"   \
  "mp3float,mp2,mp2float,ac3,eac3,vorbis,flac,alac,pcm_s16le,pcm_s16be,pcm_s24le,pcm_s24be,"       \
  "pcm_s32le,pcm_f32le,pcm_f64le,pcm_u8,pcm_alaw,pcm_mulaw"
#define XI_TX_IO (64 * 1024)
// Packets a decoder may reject in a row before the source is corrupt.
#define XI_TX_MAX_BAD_PACKETS 64
// Seconds of packets read past the cut's end before a stream stops: the
// decoder and B-frame reordering may still hold frames inside the cut.
#define XI_TX_END_SLACK 1.0
// Frames whose duration the video encoder may still hold (reordering).
#define XI_TX_DURATIONS 256
// A stream's audio frame within 1/XI_TX_SNAP s of the previous one's end
// continues it.
#define XI_TX_SNAP 10

// An overlay drawn from pixels: its buffer source in the overlay graph
// and the frame pushed into it (once, or with every video frame while it
// fades).
typedef struct {
  AVFilterContext *src;
  AVFrame *frame;
  int fade;
  int closed;
} XiTxOv;

typedef struct {
  int index; // input stream; -1 when none
  AVCodecContext *dec;
  AVFilterGraph *graph;
  // sink is the stream's last sink, which the encoder reads. With
  // overlays (WP-124) it is the overlay graph's: mid is then the chain's
  // sink, whose frames are moved into the overlay graph's main source.
  AVFilterContext *src, *sink;
  AVFilterGraph *ograph;
  AVFilterContext *mid, *osrc;
  XiTxOv ov[XI_TX_MAX_OVERLAYS];
  int nov;
  int ov_closed;
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
  // Audio of a stream (fragmented output): the timestamp the next frame
  // continues at (encoder time base); AV_NOPTS_VALUE before the first.
  int64_t next_pts;
} XiTxStream;

struct XiTx {
  AVFormatContext *fmt;
  AVIOContext *avio;
  const uint8_t *data;
  int fd;      // a spooled source (xi_tx_new_fd): owned duplicate; -1 for data
  int64_t off; // the source's offset in fd
  int64_t size, pos;
  atomic_int abort;
  int64_t max_pixels;
  int threads;
  int audio_decodable;
  XiTxStream v, a;
  AVFormatContext *oc;
  XiFFOut out;
  AVPacket *pkt, *opkt;
  AVFrame *frame, *filt, *mid;
  // Fragmented output (WP-126): see XiTxParams.
  int frag;
  int64_t frag_offset; // added to every timestamp, in the output stream's time base
  int64_t frag_len;    // audio fragment length, encoder time base; 0 = one fragment
  int64_t frag_seg;    // index of the fragment being written; -1 before the first
  // Audio chunks (WP-131): the fragments kept and the stream's count (see
  // XiTxParams), and the packet held back until the next one shows it is
  // not the final one.
  int64_t frag_first, frag_end, frag_final;
  AVPacket *pend;
  int64_t pend_seg;
  int has_pend;
  // MPEG-TS segment (WP-131): the copied audio stream and the next of its
  // packets to write (XiTxParams.copy_*, valid during xi_tx_run).
  int ts;
  AVStream *cst;
  const XiTxParams *cp;
  int icopy;
  int64_t copy_off; // byte offset of packet icopy in cp->copy_data
  AVPacket *cpkt;
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
  static const char *const ovf[] = { "buffer", "buffersink", "overlay", "fade", "format", NULL };
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
  if (xi_tx_has_filters(ovf))
    m |= XI_TX_CAP_OVERLAYS;
  if (avfilter_get_by_name("drawtext"))
    m |= XI_TX_CAP_DRAWTEXT;
  static const char *const effects[] = { "volume", "loudnorm", "afade", NULL };
  static const char *const vfade[] = { "fade", NULL };
  static const char *const wave[] = { "showwavespic", NULL };
  if (avcodec_find_encoder_by_name("flac"))
    m |= XI_TX_CAP_FLAC;
  if (avcodec_find_encoder_by_name("pcm_s16le"))
    m |= XI_TX_CAP_PCM;
  if (av_guess_format("flac", NULL, NULL))
    m |= XI_TX_CAP_MUX_FLAC;
  if (av_guess_format("wav", NULL, NULL))
    m |= XI_TX_CAP_MUX_WAV;
  if (av_guess_format("mpegts", NULL, NULL))
    m |= XI_TX_CAP_MUX_MPEGTS;
  if (xi_tx_has_filters(effects))
    m |= XI_TX_CAP_AEFFECTS;
  if (xi_tx_has_filters(vfade))
    m |= XI_TX_CAP_VFADE;
  if (xi_tx_has_filters(wave) && xi_tx_has_filters(vf) && avcodec_find_encoder_by_name("rawvideo")
      && av_guess_format("rawvideo", NULL, NULL))
    m |= XI_TX_CAP_WAVEFORM;
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
  if (h->fd >= 0) {
    n = xi_ff_pread(h->fd, buf, n, h->off + h->pos);
    if (n < 0)
      return n;
  } else
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
  for (int i = 0; i < s->nov; i++)
    av_frame_free(&s->ov[i].frame);
  s->nov = 0;
  avfilter_graph_free(&s->ograph);
  s->mid = s->osrc = NULL;
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
  av_frame_free(&h->mid);
  av_packet_free(&h->pkt);
  av_packet_free(&h->opkt);
  av_packet_free(&h->cpkt);
  av_packet_free(&h->pend);
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
  if (h->fd >= 0)
    close(h->fd);
  av_free(h);
  atomic_fetch_sub(&xi_tx_handles, 1);
}

static XiTx *
xi_tx_new_handle(const uint8_t *data, int fd, int64_t off, int64_t size, int64_t max_pixels, int threads)
{
  xi_ff_setup();
  XiTx *h = av_mallocz(sizeof(*h));
  if (!h) {
    if (fd >= 0)
      close(fd);
    return NULL;
  }
  atomic_fetch_add(&xi_tx_handles, 1);
  h->data = data;
  h->fd = fd;
  h->off = off;
  h->size = size;
  h->out.spill_fd = -1;
  h->max_pixels = max_pixels;
  h->threads = threads > 0 ? threads : 1;
  h->v.index = h->a.index = -1;
  h->v.next_pts = h->a.next_pts = AV_NOPTS_VALUE;

  h->out.abort = &h->abort;
  uint8_t *iobuf = av_malloc(XI_TX_IO);
  h->fmt = avformat_alloc_context();
  h->pkt = av_packet_alloc();
  h->opkt = av_packet_alloc();
  h->cpkt = av_packet_alloc();
  h->pend = av_packet_alloc();
  h->frame = av_frame_alloc();
  h->filt = av_frame_alloc();
  h->mid = av_frame_alloc();
  if (iobuf)
    h->avio = avio_alloc_context(iobuf, XI_TX_IO, 0, h, xi_tx_read, NULL, xi_tx_seek);
  if (!h->avio)
    av_free(iobuf);
  if (!h->avio || !h->fmt || !h->pkt || !h->opkt || !h->cpkt || !h->pend || !h->frame || !h->filt
      || !h->mid) {
    xi_tx_free(h);
    return NULL;
  }
  return h;
}

XiTx *
xi_tx_new(const uint8_t *data, size_t size, int64_t max_pixels, int threads)
{
  return xi_tx_new_handle(data, -1, 0, (int64_t)size, max_pixels, threads);
}

XiTx *
xi_tx_new_fd(int fd, int64_t off, int64_t size, int64_t max_pixels, int threads)
{
  if (fd < 0 || off < 0 || size <= 0)
    return NULL;
  int dup_fd = fcntl(fd, F_DUPFD_CLOEXEC, 0); // never inherited by a subprocess
  if (dup_fd < 0)
    return NULL;
  return xi_tx_new_handle(NULL, dup_fd, off, size, max_pixels, threads);
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
    AVStream *ast = h->fmt->streams[ret];
    AVCodecParameters *par = ast->codecpar;
    info->audio = 1;
    info->audio_tb_num = ast->time_base.num;
    info->audio_tb_den = ast->time_base.den;
    info->audio_start = ast->start_time != AV_NOPTS_VALUE ? ast->start_time * av_q2d(ast->time_base)
                                                          : info->start_time;
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
// and a buffer sink. The sink is audio when audio is set, unless
// video_sink is (a waveform: the chain turns audio into video frames).
static int
xi_tx_graph(XiTx *h, XiTxStream *s, int audio, int video_sink, const char *chain, int threads)
{
  AVStream *st = h->fmt->streams[s->index];
  AVCodecContext *dec = s->dec;
  s->graph = avfilter_graph_alloc();
  if (!s->graph)
    return AVERROR(ENOMEM);
  s->graph->nb_threads = threads;
  const AVFilter *fsrc = avfilter_get_by_name(audio ? "abuffer" : "buffer");
  const AVFilter *fsink = avfilter_get_by_name(audio && !video_sink ? "abuffersink" : "buffersink");
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

// ---------------------------------------------------------------------------
// Overlays (WP-124, ADR 0012 §3)
//
// The overlays run in a second graph after the chain's sink, so that every
// frame entering it has its final timestamp: an image overlay's frame is
// pushed with the timestamp of the video frame it goes on. The graph is
// built here, filter by filter, every option an AVOption value (no graph
// syntax is parsed): main buffer source -> overlay (one per image, its
// pixels from a buffer source, faded by fade filters) or drawtext (one per
// timecode) -> format -> buffer sink.

// Allocates filter name as inst in g.
static int
xi_tx_alloc(AVFilterGraph *g, const char *name, const char *inst, AVFilterContext **out)
{
  const AVFilter *f = avfilter_get_by_name(name);
  if (!f)
    return AVERROR_FILTER_NOT_FOUND;
  *out = avfilter_graph_alloc_filter(g, f, inst);
  return *out ? 0 : AVERROR(ENOMEM);
}

// Configures and initialises buffer source src with the chain's output
// time base, rate and colour, for frames of format, w x h.
static int
xi_tx_buffer_init(XiTxStream *s, AVFilterContext *src, int format, int w, int h, int colour)
{
  AVBufferSrcParameters *par = av_buffersrc_parameters_alloc();
  if (!par)
    return AVERROR(ENOMEM);
  par->format = format;
  par->width = w;
  par->height = h;
  par->time_base = av_buffersink_get_time_base(s->mid);
  par->frame_rate = av_buffersink_get_frame_rate(s->mid);
  par->sample_aspect_ratio = (AVRational) { 1, 1 };
  if (colour) {
    par->color_space = av_buffersink_get_colorspace(s->mid);
    par->color_range = av_buffersink_get_color_range(s->mid);
  }
  int ret = av_buffersrc_parameters_set(src, par);
  av_free(par);
  if (ret < 0)
    return ret;
  return avfilter_init_dict(src, NULL);
}

// Adds a fade of the overlay's alpha (type 0 in, 1 out) after *last.
static int
xi_tx_fade(AVFilterGraph *g, AVFilterContext **last, const char *inst, int type, int64_t start,
    int64_t dur)
{
  AVFilterContext *f;
  int ret = xi_tx_alloc(g, "fade", inst, &f);
  if (ret < 0 || (ret = av_opt_set_int(f, "type", type, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set_int(f, "start_time", start, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set_int(f, "duration", dur, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set_int(f, "alpha", 1, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = avfilter_init_dict(f, NULL)) < 0 || (ret = avfilter_link(*last, 0, f, 0)) < 0)
    return ret;
  *last = f;
  return 0;
}

// Adds image overlay o (index i) after *last.
static int
xi_tx_overlay_image(XiTxStream *s, const XiTxOverlay *o, int i, AVFilterContext **last)
{
  AVFilterGraph *g = s->ograph;
  XiTxOv *ov = &s->ov[s->nov++];
  AVFrame *f = ov->frame = av_frame_alloc();
  if (!f)
    return AVERROR(ENOMEM);
  f->format = AV_PIX_FMT_RGBA;
  f->width = o->width;
  f->height = o->height;
  f->sample_aspect_ratio = (AVRational) { 1, 1 };
  int ret = av_frame_get_buffer(f, 0);
  if (ret < 0)
    return ret;
  for (int y = 0; y < o->height; y++)
    memcpy(f->data[0] + (ptrdiff_t)y * f->linesize[0], o->rgba + (size_t)y * o->width * 4,
        (size_t)o->width * 4);

  char inst[32];
  snprintf(inst, sizeof(inst), "pixels%d", i);
  if ((ret = xi_tx_alloc(g, "buffer", inst, &ov->src)) < 0
      || (ret = xi_tx_buffer_init(s, ov->src, AV_PIX_FMT_RGBA, o->width, o->height, 0)) < 0)
    return ret;
  AVFilterContext *branch = ov->src;
  if (o->fade_in_dur > 0) {
    snprintf(inst, sizeof(inst), "fadein%d", i);
    if ((ret = xi_tx_fade(g, &branch, inst, 0, o->fade_in_start, o->fade_in_dur)) < 0)
      return ret;
  }
  if (o->fade_out_dur > 0) {
    snprintf(inst, sizeof(inst), "fadeout%d", i);
    if ((ret = xi_tx_fade(g, &branch, inst, 1, o->fade_out_start, o->fade_out_dur)) < 0)
      return ret;
  }
  // A faded overlay changes with time: its frame goes with every video
  // frame. Otherwise one frame, repeated by the overlay filter.
  ov->fade = o->fade_in_dur > 0 || o->fade_out_dur > 0;

  AVFilterContext *mix;
  char x[16], y[16];
  snprintf(inst, sizeof(inst), "overlay%d", i);
  snprintf(x, sizeof(x), "%d", o->x);
  snprintf(y, sizeof(y), "%d", o->y);
  if ((ret = xi_tx_alloc(g, "overlay", inst, &mix)) < 0
      || (ret = av_opt_set(mix, "x", x, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set(mix, "y", y, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set_int(mix, "eof_action", 0, AV_OPT_SEARCH_CHILDREN)) < 0 // repeat
      || (o->enable[0] && (ret = av_opt_set(mix, "enable", o->enable, AV_OPT_SEARCH_CHILDREN)) < 0)
      || (ret = avfilter_init_dict(mix, NULL)) < 0 || (ret = avfilter_link(*last, 0, mix, 0)) < 0
      || (ret = avfilter_link(branch, 0, mix, 1)) < 0)
    return ret;
  *last = mix;
  return 0;
}

// Adds timecode o (index i) after *last.
static int
xi_tx_overlay_timecode(XiTxStream *s, const XiTxOverlay *o, int i, AVFilterContext **last)
{
  AVFilterContext *dt;
  char inst[32], size[16];
  snprintf(inst, sizeof(inst), "timecode%d", i);
  snprintf(size, sizeof(size), "%d", o->size);
  int ret = xi_tx_alloc(s->ograph, "drawtext", inst, &dt);
  if (ret < 0 || (ret = av_opt_set(dt, "text", o->text, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set(dt, "expansion", "normal", AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set(dt, "fontsize", size, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set(dt, "fontcolor", o->color, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set(dt, "x", o->tx, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set(dt, "y", o->ty, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set(dt, "alpha", o->alpha, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set_int(dt, "shadowx", o->shadow, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = av_opt_set_int(dt, "shadowy", o->shadow, AV_OPT_SEARCH_CHILDREN)) < 0
      || (o->enable[0] && (ret = av_opt_set(dt, "enable", o->enable, AV_OPT_SEARCH_CHILDREN)) < 0)
      || (ret = avfilter_init_dict(dt, NULL)) < 0 || (ret = avfilter_link(*last, 0, dt, 0)) < 0)
    return ret;
  *last = dt;
  return 0;
}

// Builds s's overlay graph after its chain (s->sink becomes the overlay
// graph's sink).
static int
xi_tx_overlays(XiTxStream *s, const XiTxParams *p)
{
  if (p->noverlays < 0 || p->noverlays > XI_TX_MAX_OVERLAYS)
    return AVERROR(EINVAL);
  s->mid = s->sink;
  s->sink = NULL;
  AVFilterGraph *g = s->ograph = avfilter_graph_alloc();
  if (!g)
    return AVERROR(ENOMEM);
  g->nb_threads = p->threads;
  int format = av_buffersink_get_format(s->mid);
  int w = av_buffersink_get_w(s->mid), h = av_buffersink_get_h(s->mid);
  int ret = xi_tx_alloc(g, "buffer", "main", &s->osrc);
  if (ret < 0 || (ret = xi_tx_buffer_init(s, s->osrc, format, w, h, 1)) < 0)
    return ret;
  AVFilterContext *last = s->osrc;
  for (int i = 0; i < p->noverlays; i++) {
    const XiTxOverlay *o = &p->overlays[i];
    if (o->rgba) {
      if (o->width <= 0 || o->height <= 0 || o->x < 0 || o->y < 0 || o->x + o->width > w
          || o->y + o->height > h)
        return AVERROR(EINVAL);
      ret = xi_tx_overlay_image(s, o, i, &last);
    } else {
      ret = xi_tx_overlay_timecode(s, o, i, &last);
    }
    if (ret < 0)
      return ret;
  }
  // The overlays write 4:2:0 already; the format filter pins the
  // encoder's input to the chain's.
  AVFilterContext *fmt;
  const char *name = av_get_pix_fmt_name(format);
  if (!name)
    return AVERROR(EINVAL);
  if ((ret = xi_tx_alloc(g, "format", "pixfmt", &fmt)) < 0
      || (ret = av_opt_set(fmt, "pix_fmts", name, AV_OPT_SEARCH_CHILDREN)) < 0
      || (ret = avfilter_init_dict(fmt, NULL)) < 0 || (ret = avfilter_link(last, 0, fmt, 0)) < 0
      || (ret = xi_tx_alloc(g, "buffersink", "out", &s->sink)) < 0
      || (ret = avfilter_init_dict(s->sink, NULL)) < 0
      || (ret = avfilter_link(fmt, 0, s->sink, 0)) < 0)
    return ret;
  return avfilter_graph_config(g, NULL);
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
  case XI_TX_FLAC:
    return "flac";
  case XI_TX_PCM:
    return "pcm_s16le";
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
    AVRational fr = av_buffersink_get_frame_rate(s->mid ? s->mid : s->sink);
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
      if (p->level > 0) // the level the manifest's CODECS names (WP-126)
        c->level = p->level;
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
  if (audio && p->frag) {
    // The fragments are cut on the source's timeline, so the priming
    // the encoder adds must be the one the plan expects (WP-126).
    if (c->initial_padding != p->audio_priming)
      return AVERROR(EINVAL);
    h->frag_len = (int64_t)p->frag_seconds * c->sample_rate;
    if (p->frag_first < 0 || p->frag_end < 0 || p->frag_final < 0
        || (p->frag_end > 0 && p->frag_end <= p->frag_first))
      return AVERROR(EINVAL);
    h->frag_first = p->frag_first;
    h->frag_end = p->frag_end;
    h->frag_final = p->frag_final;
  }
  s->ost = avformat_new_stream(h->oc, NULL);
  if (!s->ost)
    return AVERROR(ENOMEM);
  s->ost->time_base = c->time_base;
  if (!audio && p->frag) // HLS's clock: every frame rate and the offset are exact
    s->ost->time_base = (AVRational) { 1, 90000 };
  if (!audio)
    s->ost->avg_frame_rate = c->framerate;
  if ((ret = avcodec_parameters_from_context(s->ost->codecpar, c)) < 0)
    return ret;
  if (!audio && p->vcodec == XI_FF_ENC_HEVC) // Apple players want hvc1
    s->ost->codecpar->codec_tag = MKTAG('h', 'v', 'c', '1');
  s->active = 1;
  return 0;
}

// Writes pkt (encoder time base) into fragmented output (WP-126). The
// output has one stream, so packets go straight to the muxer
// (av_write_frame), and an audio fragment is flushed before the first
// packet of fragment seg (>= 0 for audio). Then every timestamp moves by
// frag_offset, which is at least the audio priming and the video
// encoder's reordering delay, so no decode time is negative and the
// source's instant t plays at t + offset in every segment of every
// rendition, audio and video alike.
static int
xi_tx_put_frag(XiTx *h, XiTxStream *s, AVPacket *pkt, int64_t seg)
{
  int ret;
  if (seg > h->frag_seg) {
    if (h->frag_seg >= 0 && (ret = av_write_frame(h->oc, NULL)) < 0) {
      av_packet_unref(pkt);
      return ret;
    }
    h->frag_seg = seg;
  }
  av_packet_rescale_ts(pkt, s->enc->time_base, s->ost->time_base);
  if (pkt->pts != AV_NOPTS_VALUE)
    pkt->pts += h->frag_offset;
  if (pkt->dts != AV_NOPTS_VALUE)
    pkt->dts += h->frag_offset;
  if ((pkt->dts != AV_NOPTS_VALUE && pkt->dts < 0) || (pkt->pts != AV_NOPTS_VALUE && pkt->pts < 0)) {
    av_packet_unref(pkt);
    return AVERROR(EINVAL); // an offset below the encoder's delay
  }
  pkt->stream_index = s->ost->index;
  ret = av_write_frame(h->oc, pkt);
  av_packet_unref(pkt);
  return ret;
}

// Writes h->opkt into fragmented output. An audio packet belongs to the
// fragment its source time falls in (the priming packet, before 0, to the
// first); packets of fragments outside [frag_first, frag_end) are dropped
// (a chunk's pre-roll and post-roll, WP-131), and each one is held back
// until the next arrives, so the final one can still move (xi_tx_frag_end).
static int
xi_tx_write_frag(XiTx *h, XiTxStream *s)
{
  AVPacket *pkt = h->opkt;
  if (h->frag_len <= 0)
    return xi_tx_put_frag(h, s, pkt, -1);
  int64_t seg = pkt->pts == AV_NOPTS_VALUE ? FFMAX(h->frag_seg, h->frag_first)
      : pkt->pts < 0                       ? 0
                                           : pkt->pts / h->frag_len;
  if (seg < h->frag_first || (h->frag_end > 0 && seg >= h->frag_end)) {
    av_packet_unref(pkt);
    return 0;
  }
  if (h->has_pend) {
    h->has_pend = 0;
    int ret = xi_tx_put_frag(h, s, h->pend, h->pend_seg);
    if (ret < 0) {
      av_packet_unref(pkt);
      return ret;
    }
  }
  av_packet_move_ref(h->pend, pkt);
  h->pend_seg = seg;
  h->has_pend = 1;
  return 0;
}

// Writes the audio packet held back, the stream's final one when this
// output holds the last segment: into that segment's fragment if no
// packet reached it (a last segment shorter than a packet), so every
// segment has audio.
static int
xi_tx_frag_end(XiTx *h, XiTxStream *s)
{
  if (!h->has_pend)
    return 0;
  h->has_pend = 0;
  int64_t seg = h->pend_seg;
  if (h->frag_final > 0 && seg < h->frag_final - 1)
    seg = h->frag_final - 1;
  return xi_tx_put_frag(h, s, h->pend, seg);
}

// Writes the copied audio packets (MPEG-TS, WP-131) whose decode time is
// at most limit (1/90000 s), in order.
static int
xi_tx_copy_audio(XiTx *h, int64_t limit)
{
  const XiTxParams *p = h->cp;
  if (!h->cst)
    return 0;
  AVRational tb = { 1, p->copy_rate };
  for (; h->icopy < p->ncopy; h->icopy++) {
    int i = h->icopy;
    if (av_rescale_q(p->copy_pts[i], tb, (AVRational) { 1, 90000 }) > limit)
      return 0;
    int32_t n = p->copy_size[i];
    if (n <= 0 || h->copy_off + n > p->copy_bytes || p->copy_pts[i] < 0)
      return AVERROR(EINVAL);
    AVPacket *pkt = h->cpkt;
    int ret = av_new_packet(pkt, n);
    if (ret < 0)
      return ret;
    memcpy(pkt->data, p->copy_data + h->copy_off, (size_t)n);
    h->copy_off += n;
    pkt->pts = pkt->dts = p->copy_pts[i];
    pkt->duration = p->copy_dur[i];
    pkt->flags |= AV_PKT_FLAG_KEY;
    av_packet_rescale_ts(pkt, tb, h->cst->time_base);
    pkt->stream_index = h->cst->index;
    if ((ret = av_interleaved_write_frame(h->oc, pkt)) < 0)
      return ret;
  }
  return 0;
}

// Writes h->opkt (encoder time base) into an MPEG-TS segment (WP-131):
// moved by the stream offset like a CMAF segment's packets, after the
// copied audio packets that decode before it.
static int
xi_tx_write_ts(XiTx *h, XiTxStream *s)
{
  AVPacket *pkt = h->opkt;
  av_packet_rescale_ts(pkt, s->enc->time_base, s->ost->time_base);
  if (pkt->pts != AV_NOPTS_VALUE)
    pkt->pts += h->frag_offset;
  if (pkt->dts != AV_NOPTS_VALUE)
    pkt->dts += h->frag_offset;
  if ((pkt->dts != AV_NOPTS_VALUE && pkt->dts < 0) || (pkt->pts != AV_NOPTS_VALUE && pkt->pts < 0)) {
    av_packet_unref(pkt);
    return AVERROR(EINVAL); // an offset below the encoder's delay
  }
  int64_t dts = pkt->dts != AV_NOPTS_VALUE ? pkt->dts : pkt->pts;
  int ret = xi_tx_copy_audio(h, av_rescale_q(dts, s->ost->time_base, (AVRational) { 1, 90000 }));
  if (ret < 0) {
    av_packet_unref(pkt);
    return ret;
  }
  pkt->stream_index = s->ost->index;
  return av_interleaved_write_frame(h->oc, pkt);
}

// Adds the copied AAC-LC stream of an MPEG-TS segment (WP-131): its
// AudioSpecificConfig (object type 2, the rate's index, the channel
// configuration) lets the muxer write ADTS headers.
static int
xi_tx_copy_stream(XiTx *h, const XiTxParams *p)
{
  static const int rates[]
      = { 96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350 };
  int idx = -1;
  for (int i = 0; i < (int)(sizeof(rates) / sizeof(rates[0])); i++)
    if (rates[i] == p->copy_rate)
      idx = i;
  if (idx < 0 || p->copy_channels < 1 || p->copy_channels > 2 || p->ncopy < 0
      || (p->ncopy > 0 && (!p->copy_data || !p->copy_size || !p->copy_pts || !p->copy_dur)))
    return AVERROR(EINVAL);
  AVStream *st = avformat_new_stream(h->oc, NULL);
  if (!st)
    return AVERROR(ENOMEM);
  AVCodecParameters *par = st->codecpar;
  par->codec_type = AVMEDIA_TYPE_AUDIO;
  par->codec_id = AV_CODEC_ID_AAC;
  par->profile = AV_PROFILE_AAC_LOW;
  par->sample_rate = p->copy_rate;
  par->frame_size = 1024;
  av_channel_layout_default(&par->ch_layout, p->copy_channels);
  par->extradata = av_mallocz(2 + AV_INPUT_BUFFER_PADDING_SIZE);
  if (!par->extradata)
    return AVERROR(ENOMEM);
  par->extradata_size = 2;
  par->extradata[0] = (uint8_t)((2 << 3) | (idx >> 1));
  par->extradata[1] = (uint8_t)(((idx & 1) << 7) | (p->copy_channels << 3));
  st->time_base = (AVRational) { 1, p->copy_rate };
  h->cst = st;
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
    if (h->frag) {
      if ((ret = xi_tx_write_frag(h, s)) < 0)
        return ret;
      continue;
    }
    if (h->ts) {
      if ((ret = xi_tx_write_ts(h, s)) < 0)
        return ret;
      continue;
    }
    av_packet_rescale_ts(h->opkt, s->enc->time_base, s->ost->time_base);
    h->opkt->stream_index = s->ost->index;
    if ((ret = av_interleaved_write_frame(h->oc, h->opkt)) < 0)
      return ret;
  }
}

// Encodes every frame s's sink has ready.
static int
xi_tx_drain(XiTx *h, XiTxStream *s)
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
    if (h->frag && s->enc->codec_type == AVMEDIA_TYPE_AUDIO && f->pts != AV_NOPTS_VALUE) {
      // A stream's audio runs on a continuous sample clock: a frame that
      // starts within XI_TX_SNAP of where the previous one ended continues
      // it (sources with coarse timestamps, such as Matroska's
      // milliseconds, would otherwise leave gaps and overlaps of a few
      // samples that the encoder turns into timestamp jumps); a larger
      // gap is the source's and stays.
      if (s->next_pts != AV_NOPTS_VALUE && llabs(f->pts - s->next_pts) <= s->enc->sample_rate / XI_TX_SNAP)
        f->pts = s->next_pts;
      s->next_pts = f->pts + f->nb_samples;
    }
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

// Ends the overlay graph's inputs once the chain has ended.
static int
xi_tx_overlay_close(XiTxStream *s)
{
  if (s->ov_closed)
    return 0;
  s->ov_closed = 1;
  for (int i = 0; i < s->nov; i++) {
    XiTxOv *ov = &s->ov[i];
    if (!ov->closed) {
      ov->closed = 1;
      int ret = av_buffersrc_add_frame(ov->src, NULL);
      if (ret < 0 && ret != AVERROR_EOF)
        return ret;
    }
  }
  int ret = av_buffersrc_add_frame(s->osrc, NULL);
  return ret == AVERROR_EOF ? 0 : ret;
}

// Moves every frame the chain has ready into the overlay graph, each with
// the overlays' frames at its timestamp, and encodes what comes out.
static int
xi_tx_overlay_feed(XiTx *h, XiTxStream *s)
{
  for (;;) {
    if (atomic_load(&h->abort))
      return AVERROR_EXIT;
    int ret = av_buffersink_get_frame(s->mid, h->mid);
    if (ret == AVERROR(EAGAIN))
      return 0;
    if (ret == AVERROR_EOF)
      return xi_tx_overlay_close(s);
    if (ret < 0)
      return ret;
    for (int i = 0; i < s->nov; i++) {
      XiTxOv *ov = &s->ov[i];
      if (ov->closed)
        continue;
      ov->frame->pts = h->mid->pts;
      ret = av_buffersrc_add_frame_flags(ov->src, ov->frame, AV_BUFFERSRC_FLAG_KEEP_REF);
      if (ret >= 0 && !ov->fade) {
        ov->closed = 1; // the overlay filter repeats it
        ret = av_buffersrc_add_frame(ov->src, NULL);
      }
      if (ret < 0) {
        av_frame_unref(h->mid);
        return ret;
      }
    }
    if ((ret = av_buffersrc_add_frame(s->osrc, h->mid)) < 0) {
      av_frame_unref(h->mid);
      return ret;
    }
    if ((ret = xi_tx_drain(h, s)) < 0)
      return ret;
  }
}

// Encodes every frame s's graphs have ready.
static int
xi_tx_pull(XiTx *h, XiTxStream *s)
{
  if (s->mid) {
    int ret = xi_tx_overlay_feed(h, s);
    if (ret < 0)
      return ret;
  }
  return xi_tx_drain(h, s);
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
  if (p->spill_bytes > 0) {
    if ((h->out.spill_fd = fcntl(p->spill_fd, F_DUPFD_CLOEXEC, 0)) < 0) {
      xi_ff_set_err(err, errlen, "spool file", 0);
      return XI_FF_ERR_INTERNAL;
    }
    h->out.spill = p->spill_bytes;
  }
  if ((ret = xi_ff_out_attach(h->oc, &h->out)) < 0)
    return xi_tx_fail(h, ret, "output", err, errlen);
  h->oc->interrupt_callback.callback = xi_tx_interrupt;
  h->oc->interrupt_callback.opaque = h;
  h->oc->flags |= AVFMT_FLAG_BITEXACT; // no library version strings
  if (p->frag) {
    if (strcmp(muxer, "mp4") || !p->vcodec == !p->acodec || p->frag_seconds < 0) {
      xi_ff_set_err(err, errlen, "fragmented output takes one stream in mp4", 0);
      return XI_FF_ERR_INTERNAL;
    }
    h->frag = 1;
    h->frag_seg = -1;
    // Timestamps are the plan's (the segment's place on the stream's
    // timeline): never shifted to start at zero.
    h->oc->avoid_negative_ts = AVFMT_AVOID_NEG_TS_DISABLED;
  }
  if (p->ts) {
    if (strcmp(muxer, "mpegts") || !p->vcodec || p->acodec || p->frag) {
      xi_ff_set_err(err, errlen, "an mpeg-ts segment takes one video stream and copied audio", 0);
      return XI_FF_ERR_INTERNAL;
    }
    h->ts = 1;
    h->cp = p;
    // The stream's timestamps, as for CMAF segments.
    h->oc->avoid_negative_ts = AVFMT_AVOID_NEG_TS_DISABLED;
  }

  char chain[sizeof(p->vfilters) + 1] = { 0 };
  if (p->vcodec) {
    if (h->v.index < 0 || !h->v.dec) {
      xi_ff_set_err(err, errlen, "no video stream", 0);
      return XI_FF_ERR_UNSUPPORTED;
    }
    memcpy(chain, p->vfilters, sizeof(p->vfilters));
    if ((ret = xi_tx_graph(h, &h->v, 0, 0, chain, p->threads)) < 0)
      return xi_tx_fail(h, ret, "video filters", err, errlen);
    if (p->noverlays > 0 && (ret = xi_tx_overlays(&h->v, p)) < 0)
      return xi_tx_fail(h, ret, "video overlays", err, errlen);
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
    if ((ret = xi_tx_graph(h, &h->a, 1, 0, chain, 1)) < 0)
      return xi_tx_fail(h, ret, "audio filters", err, errlen);
    if ((ret = xi_tx_encoder(h, &h->a, xi_tx_aencoder(p->acodec), p, 1)) < 0)
      return xi_tx_fail(h, ret, "audio encoder", err, errlen);
  }
  if (p->waveform && !p->vcodec && !p->acodec) {
    // The audio stream's chain ends in one RGBA picture (showwavespic),
    // written as raw pixels the Go side encodes.
    if (h->a.index < 0 || !h->audio_decodable) {
      xi_ff_set_err(err, errlen, "no decodable audio stream", 0);
      return XI_FF_ERR_UNSUPPORTED;
    }
    memset(chain, 0, sizeof(chain));
    memcpy(chain, p->afilters, sizeof(p->afilters));
    if ((ret = xi_tx_graph(h, &h->a, 1, 1, chain, 1)) < 0)
      return xi_tx_fail(h, ret, "waveform filters", err, errlen);
    if ((ret = xi_tx_encoder(h, &h->a, "rawvideo", p, 0)) < 0)
      return xi_tx_fail(h, ret, "waveform encoder", err, errlen);
  }
  if (!h->v.active && !h->a.active) {
    xi_ff_set_err(err, errlen, "nothing to transcode", 0);
    return XI_FF_ERR_INTERNAL;
  }
  if (h->ts && p->copy_rate > 0 && (ret = xi_tx_copy_stream(h, p)) < 0)
    return xi_tx_fail(h, ret, "copied audio", err, errlen);

  AVDictionary *mopts = NULL;
  if (p->frag) {
    // An empty moov, fragments that end where xi_tx_write_frag (or the
    // trailer) flushes them, based on their moof, with non-negative
    // composition offsets (every reader agrees on them), and no mfra
    // index at the end. frag_discont: the first fragment's decode time
    // is its first packet's, not 0. A video segment is one fragment whose
    // moov waits for it (delay_moov: AV1's configuration only arrives with
    // its first packet); the audio's configuration is known up front, and
    // a delayed moov would swallow its first fragment boundary.
    char flags[128];
    snprintf(flags, sizeof(flags), "+empty_moov+default_base_moof+frag_custom+skip_trailer%s%s",
        p->frag_discont ? "+frag_discont" : "", h->v.active ? "+delay_moov" : "");
    av_dict_set(&mopts, "movflags", flags, 0);
    av_dict_set(&mopts, "use_editlist", "0", 0);
  } else if (h->ts) {
    // Timestamps as given (no muxing delay added), and a discontinuity
    // indicator on each PID's first packet: the continuity counters of an
    // independently muxed segment start again at 0 (ADR 0012 §6).
    av_dict_set(&mopts, "mpegts_copyts", "1", 0);
    av_dict_set(&mopts, "mpegts_flags", "+initial_discontinuity", 0);
  } else if (p->faststart)
    av_dict_set(&mopts, "movflags", "+faststart", 0);
  if (!strcmp(muxer, "webm")) // the index first, for players that read ranges
    av_dict_set(&mopts, "cues_to_front", "1", 0);
  ret = avformat_write_header(h->oc, &mopts);
  av_dict_free(&mopts);
  if (ret < 0)
    return xi_tx_fail(h, ret, "write header", err, errlen);
  if (h->frag || h->ts) { // the header fixed the stream's time base
    AVStream *ost = h->v.active ? h->v.ost : h->a.ost;
    h->frag_offset = av_rescale_q(p->frag_offset, (AVRational) { 1, 90000 }, ost->time_base);
    if (h->a.active
        && h->frag_offset < av_rescale_q(h->a.enc->initial_padding,
               (AVRational) { 1, h->a.enc->sample_rate }, ost->time_base)) {
      xi_ff_set_err(err, errlen, "stream offset below the audio priming", 0);
      return XI_FF_ERR_INTERNAL;
    }
  }

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
  if (h->ts && (ret = xi_tx_copy_audio(h, INT64_MAX)) < 0)
    return xi_tx_fail(h, ret, "copied audio", err, errlen);
  if (h->frag && h->a.active && (ret = xi_tx_frag_end(h, &h->a)) < 0)
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

int
xi_tx_spilled(XiTx *h, int64_t *size)
{
  *size = h->out.size;
  return h->out.file;
}

uint8_t *
xi_tx_take(XiTx *h, int64_t *size)
{
  *size = h->out.size;
  if (h->out.file || !h->out.data || h->out.size <= 0)
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
