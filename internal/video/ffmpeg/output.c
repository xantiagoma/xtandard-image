//go:build ffmpeg && cgo

// Muxer output in memory (internal.h XiFFOut): shared by the animation
// encoder (encode.c, WP-76) and the transcoder (transcode.c, WP-121),
// which can move it into a spool file past a threshold (WP-121b).

#include "internal.h"

#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

#include <libavutil/mem.h>

// Write buffer of the muxer's AVIOContext, first capacity of the output, and
// read buffer of the context a second pass reopens the output with.
#define XI_FF_OUT_IO (64 * 1024)
#define XI_FF_OUT_INITIAL (64 * 1024)
#define XI_FF_OUT_READ (32 * 1024)

// Writes n bytes of buf at off, retrying short writes and EINTR.
static int
xi_ff_pwrite_all(int fd, const uint8_t *buf, int64_t n, int64_t off)
{
  while (n > 0) {
    ssize_t w = pwrite(fd, buf, (size_t)n, (off_t)off);
    if (w < 0) {
      if (errno == EINTR)
        continue;
      return AVERROR(errno);
    }
    buf += w;
    n -= w;
    off += w;
  }
  return 0;
}

int
xi_ff_pread(int fd, uint8_t *buf, int n, int64_t off)
{
  for (;;) {
    ssize_t r = pread(fd, buf, (size_t)n, (off_t)off);
    if (r < 0 && errno == EINTR)
      continue;
    if (r < 0)
      return AVERROR(errno);
    if (r == 0)
      return AVERROR_EOF;
    return (int)r;
  }
}

// Moves the output written so far into the spill file; from now on it
// lives there.
static int
xi_ff_out_spill(XiFFOut *o)
{
  int ret = xi_ff_pwrite_all(o->spill_fd, o->data, o->size, 0);
  if (ret < 0)
    return ret;
  av_freep(&o->data);
  o->cap = 0;
  o->file = 1;
  return 0;
}

static int
xi_ff_out_reserve(XiFFOut *o, int64_t need)
{
  if (need <= o->cap)
    return 0;
  if (o->max_bytes > 0 && need > o->max_bytes) {
    o->over = 1;
    return AVERROR(EFBIG);
  }
  int64_t cap = o->cap ? o->cap : XI_FF_OUT_INITIAL;
  while (cap < need)
    cap *= 2;
  if (o->max_bytes > 0 && cap > o->max_bytes)
    cap = o->max_bytes;
  uint8_t *p = av_realloc(o->data, (size_t)cap);
  if (!p)
    return AVERROR(ENOMEM);
  o->data = p;
  o->cap = cap;
  return 0;
}

static int
xi_ff_out_write(void *opaque, const uint8_t *buf, int n)
{
  XiFFOut *o = opaque;
  if (atomic_load(o->abort))
    return AVERROR_EXIT;
  if (n <= 0)
    return 0;
  int64_t need = o->pos + n;
  if (o->max_bytes > 0 && need > o->max_bytes) {
    o->over = 1;
    return AVERROR(EFBIG);
  }
  int ret;
  if (!o->file && o->spill > 0 && need > o->spill && (ret = xi_ff_out_spill(o)) < 0)
    return ret;
  if (o->file) { // a hole left by a seek past the end reads as zeros
    if ((ret = xi_ff_pwrite_all(o->spill_fd, buf, n, o->pos)) < 0)
      return ret;
    o->pos = need;
    if (o->pos > o->size)
      o->size = o->pos;
    return n;
  }
  ret = xi_ff_out_reserve(o, need);
  if (ret < 0)
    return ret;
  if (o->pos > o->size) // a seek past the end leaves a hole: zero it
    memset(o->data + o->size, 0, (size_t)(o->pos - o->size));
  memcpy(o->data + o->pos, buf, (size_t)n);
  o->pos += n;
  if (o->pos > o->size)
    o->size = o->pos;
  return n;
}

static int64_t
xi_ff_out_seek(void *opaque, int64_t offset, int whence)
{
  XiFFOut *o = opaque;
  int64_t pos;
  switch (whence & ~AVSEEK_FORCE) {
  case AVSEEK_SIZE:
    return o->size;
  case SEEK_SET:
    pos = offset;
    break;
  case SEEK_CUR:
    pos = o->pos + offset;
    break;
  case SEEK_END:
    pos = o->size + offset;
    break;
  default:
    return AVERROR(EINVAL);
  }
  if (pos < 0)
    return AVERROR(EINVAL);
  o->pos = pos;
  return pos;
}

// A read-only view of the output, for the muxer's second pass (movenc's
// faststart and matroskaenc's cues_to_front reopen the "file" to shift the
// media data behind the index).
typedef struct {
  XiFFOut *o;
  int64_t pos;
} XiFFOutReader;

static int
xi_ff_out_read(void *opaque, uint8_t *buf, int n)
{
  XiFFOutReader *r = opaque;
  if (atomic_load(r->o->abort))
    return AVERROR_EXIT;
  int64_t left = r->o->size - r->pos;
  if (left <= 0)
    return AVERROR_EOF;
  if (n > left)
    n = (int)left;
  if (r->o->file) {
    n = xi_ff_pread(r->o->spill_fd, buf, n, r->pos);
    if (n < 0)
      return n;
  } else
    memcpy(buf, r->o->data + r->pos, (size_t)n);
  r->pos += n;
  return n;
}

static int64_t
xi_ff_out_read_seek(void *opaque, int64_t offset, int whence)
{
  XiFFOutReader *r = opaque;
  int64_t pos;
  switch (whence & ~AVSEEK_FORCE) {
  case AVSEEK_SIZE:
    return r->o->size;
  case SEEK_SET:
    pos = offset;
    break;
  case SEEK_CUR:
    pos = r->pos + offset;
    break;
  case SEEK_END:
    pos = r->o->size + offset;
    break;
  default:
    return AVERROR(EINVAL);
  }
  if (pos < 0 || pos > r->o->size)
    return AVERROR(EINVAL);
  r->pos = pos;
  return pos;
}

// io_open of the output context: only reading the output back is allowed;
// no file or URL is ever opened.
static int
xi_ff_io_open(struct AVFormatContext *s, AVIOContext **pb, const char *url, int flags,
    AVDictionary **options)
{
  (void)url;
  (void)options;
  XiFFOut *o = s->opaque;
  if (!o || (flags & AVIO_FLAG_WRITE))
    return AVERROR(EPERM);
  XiFFOutReader *r = av_mallocz(sizeof(*r));
  uint8_t *buf = av_malloc(XI_FF_OUT_READ);
  if (!r || !buf) {
    av_free(r);
    av_free(buf);
    return AVERROR(ENOMEM);
  }
  r->o = o;
  *pb = avio_alloc_context(buf, XI_FF_OUT_READ, 0, r, xi_ff_out_read, NULL, xi_ff_out_read_seek);
  if (!*pb) {
    av_free(r);
    av_free(buf);
    return AVERROR(ENOMEM);
  }
  return 0;
}

static int
xi_ff_io_close2(struct AVFormatContext *s, AVIOContext *pb)
{
  (void)s;
  if (!pb)
    return 0;
  av_free(pb->opaque);
  av_freep(&pb->buffer);
  avio_context_free(&pb);
  return 0;
}

int
xi_ff_out_attach(AVFormatContext *oc, XiFFOut *o)
{
  uint8_t *iobuf = av_malloc(XI_FF_OUT_IO);
  if (!iobuf)
    return AVERROR(ENOMEM);
  oc->pb = avio_alloc_context(iobuf, XI_FF_OUT_IO, 1, o, NULL, xi_ff_out_write, xi_ff_out_seek);
  if (!oc->pb) {
    av_free(iobuf);
    return AVERROR(ENOMEM);
  }
  oc->opaque = o;
  oc->io_open = xi_ff_io_open;
  oc->io_close2 = xi_ff_io_close2;
  return 0;
}

void
xi_ff_out_detach(AVFormatContext *oc)
{
  if (oc && oc->pb) {
    av_freep(&oc->pb->buffer);
    avio_context_free(&oc->pb);
  }
}

void
xi_ff_out_free(XiFFOut *o)
{
  av_freep(&o->data);
  o->size = o->cap = o->pos = 0;
  if (o->spill > 0 && o->spill_fd >= 0)
    close(o->spill_fd);
  o->spill = 0;
  o->spill_fd = -1;
  o->file = 0;
}
