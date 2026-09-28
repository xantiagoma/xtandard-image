// Hand-written C shim for internal/vips: HEIF/AVIF through libheif
// directly (LGPL-3.0, already in the base image for libvips' heifload), for
// what libvips 8.18 does not do (WP-104). See shim.h for the licence notice.
//
//   - Image sequences. heifload refuses the AVIF sequence brand (`avis`)
//     and reads only the still items of a HEIF file. This file registers a
//     loader class, XiForeignLoadHeifSeqBuffer (nickname
//     "heifseqload_buffer"), ahead of heifload for files whose ftyp names a
//     sequence brand and whose moov box holds a visual track that libheif
//     can open. It decodes the track's frames (libheif's sequence API, the
//     media timeline once, edit lists ignored) as 8-bit sRGB[A] into a
//     vertical strip with page-height, n-pages, delay (ms) and loop, like
//     gifload and webpload, honouring page and n.
//   - Gain maps. xi_heif_gainmap finds the ISO 21496-1 `tmap` derived item
//     whose base image is the primary item, and returns the gain map image
//     (decoded 8-bit RGB, no colour conversion) and the tmap payload for
//     the Go parser (codec/gainmap).
//   - HDR AVIF. xi_avif_hdr_save encodes a linear scRGB float image
//     (1.0 = SDR reference white, 203 cd/m²) as BT.2100 PQ, BT.2020
//     primaries, 10 or 12 bits, with an nclx colour box and a content
//     light level box: HDR as browsers and HDR displays read it.
//
// libheif reads the blob's C memory, which the loader keeps a reference to;
// nothing points to Go memory. Decoded pixels live in g_malloc'd buffers
// that the output images own.

#include <math.h>
#include <string.h>

#include "shim.h"

#include <libheif/heif.h>
#include <libheif/heif_items.h>
#include <libheif/heif_security.h>
#include <libheif/heif_sequences.h>

// Live libheif contexts and decoded buffers, for leak tests (libheif's
// memory is not tracked by libvips).
static int xi_heif_contexts, xi_heif_buffers;

int
xi_heif_live(void)
{
  return __atomic_load_n(&xi_heif_contexts, __ATOMIC_RELAXED) +
      __atomic_load_n(&xi_heif_buffers, __ATOMIC_RELAXED);
}

// Largest decode (pixels over all loaded frames) regardless of the
// configured limits, checked on the header: it keeps every size
// computation far from overflow.
#define XI_HEIF_MAX_PIXELS ((size_t) 1 << 30)
// Samples a sequence may declare (sample table walk and delay array).
#define XI_HEIFSEQ_MAX_SAMPLES 100000
// Items xi_heif_gainmap looks through.
#define XI_HEIF_MAX_ITEMS 4096
// Largest tmap payload (the ISO 21496-1 metadata is ~150 bytes).
#define XI_HEIF_MAX_TMAP 65536

static guint32
xi_be32(const guchar *p)
{
  return (guint32) p[0] << 24 | (guint32) p[1] << 16 | (guint32) p[2] << 8 | p[3];
}

static guint64
xi_be64(const guchar *p)
{
  return (guint64) xi_be32(p) << 32 | xi_be32(p + 4);
}

#define XI_4CC(a, b, c, d) ((guint32) (a) << 24 | (guint32) (b) << 16 | (guint32) (c) << 8 | (d))

// ---- ISO BMFF sample tables ------------------------------------------------
//
// libheif has no call for a track's sample count, and walking its raw
// samples follows the edit list (a looping file repeats them). The count
// and the sample durations come from the track's stsz/stz2 and stts boxes
// instead, found by this bounded box walk.

typedef struct {
  const guchar *p;
  size_t n;
} XiSpan;

// xi_box reads the box at *off within s: its type and body. FALSE when
// nothing more fits or the size is inconsistent.
static gboolean
xi_box(XiSpan s, size_t *off, guint32 *type, XiSpan *body)
{
  size_t o = *off, hdr = 8;
  guint64 size;

  if (o > s.n || s.n - o < 8)
    return FALSE;
  size = xi_be32(s.p + o);
  *type = xi_be32(s.p + o + 4);
  if (size == 1) {
    if (s.n - o < 16)
      return FALSE;
    size = xi_be64(s.p + o + 8);
    hdr = 16;
  }
  else if (size == 0)
    size = s.n - o;
  if (size < hdr || size > s.n - o)
    return FALSE;
  body->p = s.p + o + hdr;
  body->n = (size_t) size - hdr;
  *off = o + (size_t) size;
  return TRUE;
}

// xi_child finds the first box of type in s.
static gboolean
xi_child(XiSpan s, guint32 type, XiSpan *out)
{
  size_t off = 0;
  guint32 t;
  XiSpan b;

  while (xi_box(s, &off, &t, &b))
    if (t == type) {
      *out = b;
      return TRUE;
    }
  return FALSE;
}

// xi_track_id reads tkhd's track_ID.
static gboolean
xi_track_id(XiSpan trak, guint32 *id)
{
  XiSpan tkhd;
  size_t at;

  if (!xi_child(trak, XI_4CC('t', 'k', 'h', 'd'), &tkhd) || tkhd.n < 4)
    return FALSE;
  at = tkhd.p[0] == 1 ? 4 + 16 : 4 + 8; // version 1: 64-bit times
  if (tkhd.n < at + 4)
    return FALSE;
  *id = xi_be32(tkhd.p + at);
  return TRUE;
}

// xi_sample_table reads the sample count and each sample's duration (in
// track timescale units) of track id. durations is g_malloc'd (count
// entries); a stts shorter than the count repeats its last delta.
static gboolean
xi_sample_table(const void *data, size_t len, guint32 id, guint32 *count, guint32 **durations)
{
  XiSpan file = { data, len }, moov, trak, box, stbl, stsz, stts;
  size_t off = 0;
  guint32 type, n = 0, entries, filled = 0, delta = 0;
  gboolean found = FALSE;

  *count = 0;
  *durations = NULL;
  if (!xi_child(file, XI_4CC('m', 'o', 'o', 'v'), &moov))
    return FALSE;
  while (xi_box(moov, &off, &type, &trak)) {
    guint32 tid;

    if (type == XI_4CC('t', 'r', 'a', 'k') && xi_track_id(trak, &tid) && tid == id) {
      found = TRUE;
      break;
    }
  }
  if (!found || !xi_child(trak, XI_4CC('m', 'd', 'i', 'a'), &box) ||
      !xi_child(box, XI_4CC('m', 'i', 'n', 'f'), &box) ||
      !xi_child(box, XI_4CC('s', 't', 'b', 'l'), &stbl))
    return FALSE;
  if (!(xi_child(stbl, XI_4CC('s', 't', 's', 'z'), &stsz) ||
          xi_child(stbl, XI_4CC('s', 't', 'z', '2'), &stsz)) ||
      stsz.n < 12)
    return FALSE;
  n = xi_be32(stsz.p + 8);
  if (n == 0 || n > XI_HEIFSEQ_MAX_SAMPLES)
    return FALSE;

  *durations = g_new0(guint32, n);
  if (xi_child(stbl, XI_4CC('s', 't', 't', 's'), &stts) && stts.n >= 8) {
    entries = xi_be32(stts.p + 4);
    for (guint32 e = 0; e < entries && filled < n; e++) {
      size_t at = 8 + (size_t) e * 8;
      guint32 k;

      if (at + 8 > stts.n)
        break;
      k = xi_be32(stts.p + at);
      delta = xi_be32(stts.p + at + 4);
      for (; k > 0 && filled < n; k--)
        (*durations)[filled++] = delta;
    }
  }
  for (; filled < n; filled++)
    (*durations)[filled] = delta;
  *count = n;
  return TRUE;
}

// xi_stsz_count reads the sample count of a trak's stsz or stz2 box (0
// when it has none).
static guint32
xi_stsz_count(XiSpan trak)
{
  XiSpan box, stbl, stsz;

  if (!xi_child(trak, XI_4CC('m', 'd', 'i', 'a'), &box) ||
      !xi_child(box, XI_4CC('m', 'i', 'n', 'f'), &box) ||
      !xi_child(box, XI_4CC('s', 't', 'b', 'l'), &stbl))
    return 0;
  if ((xi_child(stbl, XI_4CC('s', 't', 's', 'z'), &stsz) ||
          xi_child(stbl, XI_4CC('s', 't', 'z', '2'), &stsz)) &&
      stsz.n >= 12)
    return xi_be32(stsz.p + 8);
  return 0;
}

// xi_max_track_samples is the largest sample count of the file's tracks.
// libheif's sequence decoder keeps feeding samples to a decoder that
// returns no frame for as many samples as the edit list's (possibly
// indefinite) repetitions allow, capped only by max_sequence_frames
// (18 million by default: half a minute on a broken HEVC stream). The
// loader caps that limit to what the file really holds.
static guint32
xi_max_track_samples(const void *data, size_t len)
{
  XiSpan file = { data, len }, moov, trak;
  size_t off = 0;
  guint32 type, most = 0;

  if (!xi_child(file, XI_4CC('m', 'o', 'o', 'v'), &moov))
    return 0;
  while (xi_box(moov, &off, &type, &trak))
    if (type == XI_4CC('t', 'r', 'a', 'k'))
      most = MAX(most, xi_stsz_count(trak));
  return most;
}

// xi_ftyp_sequence reports an ISO BMFF file whose ftyp names an image
// sequence brand (avis, msf1, hevs, hevm) and which has a moov box: the
// cheap test before libheif parses anything.
static gboolean
xi_ftyp_sequence(const guchar *d, size_t n)
{
  XiSpan file = { d, n }, ftyp;
  size_t off = 0;
  guint32 type;
  XiSpan b;
  gboolean brand = FALSE, moov = FALSE;

  if (n < 16 || memcmp(d + 4, "ftyp", 4) != 0 || !xi_box(file, &off, &type, &ftyp) ||
      ftyp.n < 8)
    return FALSE;
  for (size_t i = 0; i + 4 <= ftyp.n && !brand; i += 4) {
    if (i == 4)
      continue; // minor version
    brand = memcmp(ftyp.p + i, "avis", 4) == 0 || memcmp(ftyp.p + i, "msf1", 4) == 0 ||
        memcmp(ftyp.p + i, "hevs", 4) == 0 || memcmp(ftyp.p + i, "hevm", 4) == 0;
  }
  if (!brand)
    return FALSE;
  for (int boxes = 0; boxes < 64 && xi_box(file, &off, &type, &b); boxes++)
    if (type == XI_4CC('m', 'o', 'o', 'v')) {
      moov = TRUE;
      break;
    }
  return moov;
}

// ---- contexts ------------------------------------------------------------

// xi_heif_open reads data into a new libheif context. max_frames > 0
// lowers the context's max_sequence_frames to it (xi_max_track_samples).
static heif_context *
xi_heif_open(const void *data, size_t len, guint32 max_frames, const char *nickname)
{
  heif_context *ctx = heif_context_alloc();
  heif_error e;

  if (!ctx) {
    vips_error(nickname, "cannot create the libheif context");
    return NULL;
  }
  __atomic_add_fetch(&xi_heif_contexts, 1, __ATOMIC_RELAXED);
  if (max_frames > 0) {
    heif_security_limits limits = *heif_get_global_security_limits();

    // A few more than the samples: the edit list's own entries.
    limits.max_sequence_frames = MIN(max_frames, XI_HEIFSEQ_MAX_SAMPLES) + 8;
    heif_context_set_security_limits(ctx, &limits);
  }
  e = heif_context_read_from_memory_without_copy(ctx, data, len, NULL);
  if (e.code != heif_error_Ok) {
    vips_error(nickname, "%s", e.message ? e.message : "cannot read the HEIF file");
    heif_context_free(ctx);
    __atomic_sub_fetch(&xi_heif_contexts, 1, __ATOMIC_RELAXED);
    return NULL;
  }
  return ctx;
}

static void
xi_heif_close(heif_context **ctx)
{
  if (*ctx) {
    heif_context_free(*ctx);
    *ctx = NULL;
    __atomic_sub_fetch(&xi_heif_contexts, 1, __ATOMIC_RELAXED);
  }
}

static void
xi_heif_free_pixels(VipsImage *img, gpointer pixels)
{
  (void) img;
  g_free(pixels);
  __atomic_sub_fetch(&xi_heif_buffers, 1, __ATOMIC_RELAXED);
}

// xi_heif_wrap makes a VipsImage that owns pixels (g_malloc'd, counted in
// xi_heif_buffers by the caller).
static VipsImage *
xi_heif_wrap(void *pixels, int width, int height, int bands)
{
  size_t size = (size_t) width * height * bands;
  VipsImage *img = vips_image_new_from_memory(pixels, size, width, height, bands,
      VIPS_FORMAT_UCHAR);

  if (!img) {
    xi_heif_free_pixels(NULL, pixels);
    return NULL;
  }
  g_signal_connect(img, "close", G_CALLBACK(xi_heif_free_pixels), pixels);
  return img;
}

// xi_copy_plane copies an interleaved 8-bit plane of img (width × height ×
// bands, checked against the plane's own size) to dst rows of width ×
// bands bytes.
static gboolean
xi_copy_plane(const heif_image *img, int width, int height, int bands, guchar *dst)
{
  size_t stride = 0, row = (size_t) width * bands;
  const uint8_t *p;

  if (heif_image_get_width(img, heif_channel_interleaved) != width ||
      heif_image_get_height(img, heif_channel_interleaved) != height)
    return FALSE;
  if (!(p = heif_image_get_plane_readonly2(img, heif_channel_interleaved, &stride)) ||
      stride < row)
    return FALSE;
  for (int y = 0; y < height; y++)
    memcpy(dst + (size_t) y * row, p + (size_t) y * stride, row);
  return TRUE;
}

// ---- sequence loader ---------------------------------------------------------

typedef struct {
  heif_context *ctx;
  heif_track *track;
  int width, height, bands;
  guint32 n_samples;
  guint32 *durations; // n_samples, track timescale units
  guint32 timescale;
  int loop;
  char compression[8];
} XiHeifSeq;

typedef struct _XiForeignLoadHeifSeqBuffer {
  VipsForeignLoad parent_object;

  VipsBlob *blob;
  int page;
  int n;
  XiHeifSeq seq;
} XiForeignLoadHeifSeqBuffer;

typedef VipsForeignLoadClass XiForeignLoadHeifSeqBufferClass;

G_DEFINE_TYPE(XiForeignLoadHeifSeqBuffer, xi_foreign_load_heifseq_buffer, VIPS_TYPE_FOREIGN_LOAD);

static void
xi_heifseq_close(XiHeifSeq *s)
{
  if (s->track) {
    heif_track_release(s->track);
    s->track = NULL;
  }
  xi_heif_close(&s->ctx);
}

static void
xi_heifseq_clear(XiHeifSeq *s)
{
  xi_heifseq_close(s);
  g_free(s->durations);
  s->durations = NULL;
}

// xi_heifseq_track opens the first visual track of an open context.
static heif_track *
xi_heifseq_track(heif_context *ctx)
{
  heif_track *track;
  heif_track_type t;

  if (!heif_context_has_sequence(ctx) || !(track = heif_context_get_track(ctx, 0)))
    return NULL;
  t = heif_track_get_track_handler_type(track);
  if (t != heif_track_type_image_sequence && t != heif_track_type_video) {
    heif_track_release(track);
    return NULL;
  }
  return track;
}

// xi_is_heif_sequence reports a HEIF/AVIF file with a visual sequence track.
gboolean
xi_is_heif_sequence(const void *buf, size_t len)
{
  heif_context *ctx;
  heif_track *track;
  gboolean yes = FALSE;

  guint32 samples;

  if (!xi_ftyp_sequence(buf, len) || !(samples = xi_max_track_samples(buf, len)))
    return FALSE;
  if (!(ctx = xi_heif_open(buf, len, samples, "heifseqload_buffer"))) {
    vips_error_clear();
    return FALSE;
  }
  if ((track = xi_heifseq_track(ctx))) {
    yes = TRUE;
    heif_track_release(track);
  }
  xi_heif_close(&ctx);
  return yes;
}

static int
xi_heifseq_open(XiHeifSeq *s, VipsBlob *blob, const char *nickname)
{
  VipsArea *area = VIPS_AREA(blob);
  uint16_t w = 0, h = 0;
  guint32 reps, entry;
  heif_error e;

  xi_heifseq_clear(s);
  memset(s, 0, sizeof(*s));
  if (!(s->ctx = xi_heif_open(area->data, area->length,
            MAX(xi_max_track_samples(area->data, area->length), 1), nickname)))
    return -1;
  if (!(s->track = xi_heifseq_track(s->ctx))) {
    xi_heifseq_clear(s);
    vips_error(nickname, "no visual sequence track");
    return -1;
  }
  e = heif_track_get_image_resolution(s->track, &w, &h);
  if (e.code != heif_error_Ok || w == 0 || h == 0) {
    xi_heifseq_clear(s);
    vips_error(nickname, "bad sequence track resolution");
    return -1;
  }
  s->width = w;
  s->height = h;
  s->bands = heif_track_has_alpha_channel(s->track) ? 4 : 3;
  s->timescale = heif_track_get_timescale(s->track);
  if (!xi_sample_table(area->data, area->length, heif_track_get_id(s->track), &s->n_samples,
          &s->durations)) {
    xi_heifseq_clear(s);
    vips_error(nickname, "bad sequence sample table");
    return -1;
  }
  // libvips' loop: 0 plays forever, n plays n times.
  reps = heif_track_get_number_of_repetitions(s->track);
  s->loop = reps == heif_sequence_track_number_of_repetitions_infinite ? 0 : (int) MAX(reps, 1);
  entry = heif_track_get_sample_entry_type_of_first_cluster(s->track);
  if (entry == XI_4CC('a', 'v', '0', '1'))
    g_strlcpy(s->compression, "av1", sizeof(s->compression));
  else if (entry == XI_4CC('h', 'v', 'c', '1') || entry == XI_4CC('h', 'e', 'v', '1'))
    g_strlcpy(s->compression, "hevc", sizeof(s->compression));
  else
    g_strlcpy(s->compression, "other", sizeof(s->compression));
  return 0;
}

// xi_heifseq_range resolves page and n (-1: to the end) against the
// sample count.
static int
xi_heifseq_range(XiForeignLoadHeifSeqBuffer *l, const char *nickname, int *n)
{
  XiHeifSeq *s = &l->seq;

  if (l->page < 0 || (guint32) l->page >= s->n_samples) {
    vips_error(nickname, "bad page number %d", l->page);
    return -1;
  }
  *n = l->n == -1 ? (int) s->n_samples - l->page : l->n;
  if (*n < 1 || (guint32) l->page + (guint32) *n > s->n_samples) {
    vips_error(nickname, "bad number of pages %d", l->n);
    return -1;
  }
  if ((size_t) s->width * s->height * *n > XI_HEIF_MAX_PIXELS) {
    vips_error(nickname, "sequence too large");
    return -1;
  }
  return 0;
}

static void
xi_heifseq_set_fields(XiForeignLoadHeifSeqBuffer *l, VipsImage *out, int n)
{
  XiHeifSeq *s = &l->seq;
  guint32 frames = s->n_samples - (guint32) l->page;
  int *delay = g_new(int, frames);

  vips_image_init_fields(out, s->width, s->height * n, s->bands, VIPS_FORMAT_UCHAR,
      VIPS_CODING_NONE, VIPS_INTERPRETATION_sRGB, 1.0, 1.0);
  vips_image_set_int(out, VIPS_META_PAGE_HEIGHT, s->height);
  vips_image_set_int(out, VIPS_META_N_PAGES, (int) s->n_samples);
  // Delays from the first loaded frame on, in milliseconds (the executor
  // keeps as many as it has frames).
  for (guint32 i = 0; i < frames; i++) {
    guint64 d = s->durations[l->page + i];

    delay[i] = s->timescale ? (int) MIN((d * 1000 + s->timescale / 2) / s->timescale, G_MAXINT)
                            : 0;
  }
  vips_image_set_array_int(out, "delay", delay, (int) frames);
  g_free(delay);
  vips_image_set_int(out, "loop", s->loop);
  vips_image_set_string(out, "heif-compression", s->compression);
  vips_image_set_int(out, "bits-per-sample", 8);
}

static void
xi_foreign_load_heifseq_buffer_dispose(GObject *gobject)
{
  XiForeignLoadHeifSeqBuffer *l = (XiForeignLoadHeifSeqBuffer *) gobject;

  xi_heifseq_clear(&l->seq);
  G_OBJECT_CLASS(xi_foreign_load_heifseq_buffer_parent_class)->dispose(gobject);
}

static VipsForeignFlags
xi_foreign_load_heifseq_buffer_get_flags(VipsForeignLoad *load)
{
  (void) load;
  return 0;
}

static int
xi_foreign_load_heifseq_buffer_header(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadHeifSeqBuffer *l = (XiForeignLoadHeifSeqBuffer *) load;
  int n;

  if (xi_heifseq_open(&l->seq, l->blob, class->nickname))
    return -1;
  if (xi_heifseq_range(l, class->nickname, &n)) {
    xi_heifseq_clear(&l->seq);
    return -1;
  }
  xi_heifseq_set_fields(l, load->out, n);
  // The header is all a probe needs: release libheif now; load opens the
  // file again.
  xi_heifseq_clear(&l->seq);
  return 0;
}

// xi_heifseq_decode decodes frames 0 .. page + n - 1 (frames are coded
// against earlier ones) and keeps the last n, into one buffer.
static void *
xi_heifseq_decode(XiForeignLoadHeifSeqBuffer *l, int n, const char *nickname)
{
  XiHeifSeq *s = &l->seq;
  size_t frame = (size_t) s->width * s->height * s->bands;
  heif_chroma chroma = s->bands == 4 ? heif_chroma_interleaved_RGBA : heif_chroma_interleaved_RGB;
  heif_decoding_options *opts = heif_decoding_options_alloc();
  guchar *out = g_try_malloc(frame * n);
  int last = l->page + n;

  if (!opts || !out) {
    vips_error(nickname, "out of memory decoding %d frames of %dx%d", n, s->width, s->height);
    goto fail;
  }
  // The media timeline once: a looping edit list would repeat frames.
  opts->ignore_sequence_editlist = 1;
  for (int i = 0; i < last; i++) {
    heif_image *img = NULL;
    heif_error e = heif_track_decode_next_image(s->track, &img, heif_colorspace_RGB, chroma, opts);
    gboolean ok;

    if (e.code != heif_error_Ok) {
      vips_error(nickname, "frame %d: %s", i, e.message ? e.message : "decode failed");
      goto fail;
    }
    ok = i < l->page ||
        xi_copy_plane(img, s->width, s->height, s->bands, out + frame * (i - l->page));
    heif_image_release(img);
    if (!ok) {
      vips_error(nickname, "frame %d does not match the track size", i);
      goto fail;
    }
  }
  heif_decoding_options_free(opts);
  __atomic_add_fetch(&xi_heif_buffers, 1, __ATOMIC_RELAXED);
  return out;

fail:
  if (opts)
    heif_decoding_options_free(opts);
  g_free(out);
  return NULL;
}

static int
xi_foreign_load_heifseq_buffer_load(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadHeifSeqBuffer *l = (XiForeignLoadHeifSeqBuffer *) load;
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(load), 1);
  void *pixels;
  int n;

  if (xi_heifseq_open(&l->seq, l->blob, class->nickname))
    return -1;
  if (xi_heifseq_range(l, class->nickname, &n)) {
    xi_heifseq_clear(&l->seq);
    return -1;
  }
  pixels = xi_heifseq_decode(l, n, class->nickname);
  // libheif is done: its memory goes now, not with the load.
  xi_heifseq_close(&l->seq);
  if (!pixels) {
    xi_heifseq_clear(&l->seq);
    return -1;
  }
  if (!(t[0] = xi_heif_wrap(pixels, l->seq.width, l->seq.height * n, l->seq.bands))) {
    xi_heifseq_clear(&l->seq);
    return -1;
  }
  xi_heifseq_set_fields(l, t[0], n);
  xi_heifseq_clear(&l->seq);
  return vips_image_write(t[0], load->real);
}

static gboolean
xi_foreign_load_heifseq_buffer_is_a_buffer(const void *buf, size_t len)
{
  return xi_is_heif_sequence(buf, len);
}

static const char *xi_heifseq_suffs[] = { ".avif", ".avifs", ".heic", ".heif", ".heics",
  ".heifs", NULL };

static void
xi_foreign_load_heifseq_buffer_class_init(XiForeignLoadHeifSeqBufferClass *class)
{
  GObjectClass *gobject_class = G_OBJECT_CLASS(class);
  VipsObjectClass *object_class = (VipsObjectClass *) class;
  VipsOperationClass *operation_class = VIPS_OPERATION_CLASS(class);
  VipsForeignClass *foreign_class = (VipsForeignClass *) class;
  VipsForeignLoadClass *load_class = (VipsForeignLoadClass *) class;

  gobject_class->dispose = xi_foreign_load_heifseq_buffer_dispose;
  gobject_class->set_property = vips_object_set_property;
  gobject_class->get_property = vips_object_get_property;

  object_class->nickname = "heifseqload_buffer";
  object_class->description = "load HEIF/AVIF image sequence from buffer (libheif)";

  // Unblocked by name only, like the shim's other loaders.
  operation_class->flags |= VIPS_OPERATION_UNTRUSTED;

  foreign_class->suffs = xi_heifseq_suffs;
  // Ahead of heifload, which refuses avis and reads only still items.
  foreign_class->priority = 100;

  load_class->get_flags = xi_foreign_load_heifseq_buffer_get_flags;
  load_class->header = xi_foreign_load_heifseq_buffer_header;
  load_class->load = xi_foreign_load_heifseq_buffer_load;
  load_class->is_a_buffer = xi_foreign_load_heifseq_buffer_is_a_buffer;

  VIPS_ARG_BOXED(class, "buffer", 1, "Buffer", "Buffer to load from",
      VIPS_ARGUMENT_REQUIRED_INPUT, G_STRUCT_OFFSET(XiForeignLoadHeifSeqBuffer, blob),
      VIPS_TYPE_BLOB);
  VIPS_ARG_INT(class, "page", 20, "Page", "First frame to load", VIPS_ARGUMENT_OPTIONAL_INPUT,
      G_STRUCT_OFFSET(XiForeignLoadHeifSeqBuffer, page), 0, XI_HEIFSEQ_MAX_SAMPLES, 0);
  VIPS_ARG_INT(class, "n", 21, "n", "Number of frames to load, -1 for all",
      VIPS_ARGUMENT_OPTIONAL_INPUT, G_STRUCT_OFFSET(XiForeignLoadHeifSeqBuffer, n), -1,
      XI_HEIFSEQ_MAX_SAMPLES, 1);
}

static void
xi_foreign_load_heifseq_buffer_init(XiForeignLoadHeifSeqBuffer *l)
{
  l->n = 1;
}

void
xi_heifseq_register(void)
{
  // Referencing the class registers it with libvips' loader search.
  g_type_class_unref(g_type_class_ref(xi_foreign_load_heifseq_buffer_get_type()));
}

// ---- gain maps (tmap) ---------------------------------------------------------

// xi_heif_tmap finds a tmap item whose dimg references are (primary, gain
// map). FALSE when there is none.
static gboolean
xi_heif_tmap(heif_context *ctx, heif_item_id *tmap, heif_item_id *gainmap)
{
  heif_item_id primary, *ids;
  int n = heif_context_get_number_of_items(ctx);
  gboolean found = FALSE;

  if (n <= 0 || n > XI_HEIF_MAX_ITEMS ||
      heif_context_get_primary_image_ID(ctx, &primary).code != heif_error_Ok)
    return FALSE;
  ids = g_new(heif_item_id, n);
  n = heif_context_get_list_of_item_IDs(ctx, ids, n);
  for (int i = 0; i < n && !found; i++) {
    if (heif_item_get_item_type(ctx, ids[i]) != XI_4CC('t', 'm', 'a', 'p'))
      continue;
    for (int r = 0; r < 16 && !found; r++) {
      uint32_t type = 0;
      heif_item_id *refs = NULL;
      size_t k = heif_context_get_item_references(ctx, ids[i], r, &type, &refs);

      if (k == 0) {
        heif_release_item_references(ctx, &refs);
        break;
      }
      if (type == XI_4CC('d', 'i', 'm', 'g') && k >= 2 && refs[0] == primary) {
        *tmap = ids[i];
        *gainmap = refs[1];
        found = TRUE;
      }
      heif_release_item_references(ctx, &refs);
    }
  }
  g_free(ids);
  return found;
}

int
xi_heif_gainmap(VipsBlob *blob, VipsImage **out, void **meta, size_t *meta_len, char **err)
{
  VipsArea *area = VIPS_AREA(blob);
  heif_context *ctx;
  heif_item_id tmap, gm;
  heif_image_handle *h = NULL;
  heif_image *img = NULL;
  heif_decoding_options *opts = NULL;
  uint8_t *data = NULL;
  size_t size = 0;
  guchar *pixels = NULL;
  int w, hh, rc = XI_ERR_LOAD;
  heif_error e;

  *out = NULL;
  *meta = NULL;
  *meta_len = 0;
  if (!(ctx = xi_heif_open(area->data, area->length, 0, "xi_heif_gainmap"))) {
    *err = xi_error_copy();
    return XI_ERR_LOAD;
  }
  if (!xi_heif_tmap(ctx, &tmap, &gm)) {
    xi_heif_close(&ctx);
    return XI_OK;
  }
  e = heif_item_get_item_data(ctx, tmap, NULL, &data, &size);
  if (e.code != heif_error_Ok || size == 0 || size > XI_HEIF_MAX_TMAP) {
    *err = xi_error_new("xi_heif_gainmap", "bad tmap item");
    goto done;
  }
  *meta = g_memdup2(data, size);
  *meta_len = size;

  e = heif_context_get_image_handle(ctx, gm, &h);
  if (e.code != heif_error_Ok) {
    *err = xi_error_new("xi_heif_gainmap", "gain map: %s", e.message ? e.message : "no image");
    goto done;
  }
  w = heif_image_handle_get_width(h);
  hh = heif_image_handle_get_height(h);
  if (w <= 0 || hh <= 0 || (size_t) w * hh > XI_HEIF_MAX_PIXELS) {
    *err = xi_error_new("xi_heif_gainmap", "bad gain map size %dx%d", w, hh);
    goto done;
  }
  // The gain map's code values, not colours: no primaries conversion.
  if (!(opts = heif_decoding_options_alloc())) {
    *err = xi_error_new("xi_heif_gainmap", "out of memory");
    rc = XI_ERR_INTERNAL;
    goto done;
  }
  opts->output_image_nclx_profile_passthrough = 1;
  e = heif_decode_image(h, &img, heif_colorspace_RGB, heif_chroma_interleaved_RGB, opts);
  if (e.code != heif_error_Ok) {
    *err = xi_error_new("xi_heif_gainmap", "gain map: %s", e.message ? e.message : "decode failed");
    goto done;
  }
  w = heif_image_get_width(img, heif_channel_interleaved);
  hh = heif_image_get_height(img, heif_channel_interleaved);
  if (w <= 0 || hh <= 0 || (size_t) w * hh > XI_HEIF_MAX_PIXELS ||
      !(pixels = g_try_malloc((size_t) w * hh * 3))) {
    *err = xi_error_new("xi_heif_gainmap", "bad gain map size %dx%d", w, hh);
    goto done;
  }
  if (!xi_copy_plane(img, w, hh, 3, pixels)) {
    g_free(pixels);
    *err = xi_error_new("xi_heif_gainmap", "bad gain map plane");
    goto done;
  }
  __atomic_add_fetch(&xi_heif_buffers, 1, __ATOMIC_RELAXED);
  if (!(*out = xi_heif_wrap(pixels, w, hh, 3))) {
    *err = xi_error_copy();
    rc = XI_ERR_INTERNAL;
    goto done;
  }
  (*out)->Type = VIPS_INTERPRETATION_MULTIBAND;
  rc = XI_OK;

done:
  if (rc != XI_OK) {
    g_free(*meta);
    *meta = NULL;
    *meta_len = 0;
  }
  if (img)
    heif_image_release(img);
  if (opts)
    heif_decoding_options_free(opts);
  if (h)
    heif_image_handle_release(h);
  if (data)
    heif_release_item_data(ctx, &data);
  xi_heif_close(&ctx);
  return rc;
}

// ---- HDR AVIF --------------------------------------------------------------------

// BT.2100 PQ (SMPTE ST 2084) inverse EOTF of y = cd/m² / 10000 in [0, 1].
static double
xi_pq(double y)
{
  const double m1 = 2610.0 / 16384, m2 = 2523.0 / 4096 * 128;
  const double c1 = 3424.0 / 4096, c2 = 2413.0 / 4096 * 32, c3 = 2392.0 / 4096 * 32;
  double p;

  if (y <= 0)
    return 0;
  if (y >= 1)
    return 1;
  p = pow(y, m1);
  return pow((c1 + c2 * p) / (1 + c3 * p), m2);
}

// Linear BT.709 → linear BT.2020 (BT.2087).
static const double xi_709_to_2020[9] = {
  0.6274039, 0.3292830, 0.0433131,
  0.0690973, 0.9195404, 0.0113623,
  0.0163914, 0.0880133, 0.8955953,
};

// SDR reference white of scRGB 1.0 (BT.2408).
#define XI_SDR_WHITE_NITS 203.0

static heif_error
xi_heif_write(heif_context *ctx, const void *data, size_t size, void *user)
{
  heif_error ok = { heif_error_Ok, heif_suberror_Unspecified, "" };

  (void) ctx;
  g_byte_array_append((GByteArray *) user, data, (guint) size);
  return ok;
}

// xi_avif_hdr_fill converts in (float scRGB, 3 or 4 bands) strip by strip
// into the interleaved little-endian plane of `bits` bits and measures the
// content light level.
static int
xi_avif_hdr_fill(VipsImage *in, heif_image *img, int bits, heif_content_light_level *cll)
{
  VipsRegion *region = vips_region_new(in);
  const int bands = in->Bands, strip = 64;
  const double top = (double) ((1 << bits) - 1);
  size_t stride = 0;
  uint8_t *plane = heif_image_get_plane2(img, heif_channel_interleaved, &stride);
  double max_nits = 0, sum_nits = 0;

  if (!region || !plane) {
    VIPS_UNREF(region);
    return -1;
  }
  for (int y0 = 0; y0 < in->Ysize; y0 += strip) {
    VipsRect r = { 0, y0, in->Xsize, MIN(strip, in->Ysize - y0) };

    if (vips_region_prepare(region, &r)) {
      g_object_unref(region);
      return -1;
    }
    for (int y = r.top; y < r.top + r.height; y++) {
      const float *p = (const float *) VIPS_REGION_ADDR(region, 0, y);
      uint8_t *q = plane + (size_t) y * stride;

      for (int x = 0; x < in->Xsize; x++, p += bands) {
        double rgb[3], row_max = 0;

        for (int c = 0; c < 3; c++) {
          double v = xi_709_to_2020[3 * c] * p[0] + xi_709_to_2020[3 * c + 1] * p[1] +
              xi_709_to_2020[3 * c + 2] * p[2];

          rgb[c] = isfinite(v) && v > 0 ? v * XI_SDR_WHITE_NITS : 0;
          row_max = MAX(row_max, rgb[c]);
        }
        max_nits = MAX(max_nits, row_max);
        sum_nits += row_max;
        for (int c = 0; c < bands; c++) {
          double e = c < 3 ? xi_pq(rgb[c] / 10000) : CLAMP(isfinite(p[c]) ? p[c] : 0, 0, 1);
          guint16 v = (guint16) lrint(e * top);

          *q++ = v & 0xff;
          *q++ = v >> 8;
        }
      }
    }
  }
  g_object_unref(region);
  cll->max_content_light_level = (uint16_t) MIN(ceil(max_nits), 10000);
  cll->max_pic_average_light_level =
      (uint16_t) MIN(ceil(sum_nits / MAX((double) in->Xsize * in->Ysize, 1)), 10000);
  return 0;
}

int
xi_avif_hdr_save(VipsImage *in, const XiAvifHdrOptions *o, void **buf, size_t *len, char **err)
{
  VipsImage *f = NULL;
  heif_context *ctx = NULL;
  heif_encoder *enc = NULL;
  heif_image *img = NULL;
  heif_encoding_options *opts = NULL;
  heif_color_profile_nclx *nclx = NULL;
  heif_content_light_level cll = { 0, 0 };
  heif_writer writer = { 1, xi_heif_write };
  GByteArray *bytes = NULL;
  int bits = o->bitdepth == 10 ? 10 : 12, alpha, rc = XI_ERR_SAVE;
  heif_error e;

  *buf = NULL;
  *len = 0;
  if (in->Bands != 3 && in->Bands != 4) {
    *err = xi_error_new("xi_avif_hdr_save", "%d bands: want RGB or RGBA", in->Bands);
    return XI_ERR_SAVE;
  }
  alpha = in->Bands == 4;
  if (vips_cast(in, &f, VIPS_FORMAT_FLOAT, NULL)) {
    *err = xi_error_copy();
    return XI_ERR_SAVE;
  }
  if (!(ctx = heif_context_alloc())) {
    *err = xi_error_new("xi_avif_hdr_save", "cannot create the libheif context");
    goto done;
  }
  __atomic_add_fetch(&xi_heif_contexts, 1, __ATOMIC_RELAXED);
  e = heif_context_get_encoder_for_format(ctx, heif_compression_AV1, &enc);
  if (e.code != heif_error_Ok) {
    *err = xi_error_new("xi_avif_hdr_save", "no AV1 encoder: %s", e.message);
    goto done;
  }
  if (o->lossless)
    heif_encoder_set_lossless(enc, 1);
  else
    heif_encoder_set_lossy_quality(enc, CLAMP(o->quality, 1, 100));
  // Like heifsave: effort 0..9 is aom's speed 9..0. Encoders without the
  // parameter keep their default.
  heif_encoder_set_parameter_integer(enc, "speed", 9 - CLAMP(o->effort, 0, 9));

  e = heif_image_create(f->Xsize, f->Ysize, heif_colorspace_RGB,
      alpha ? heif_chroma_interleaved_RRGGBBAA_LE : heif_chroma_interleaved_RRGGBB_LE, &img);
  if (e.code == heif_error_Ok)
    e = heif_image_add_plane(img, heif_channel_interleaved, f->Xsize, f->Ysize, bits);
  if (e.code != heif_error_Ok) {
    *err = xi_error_new("xi_avif_hdr_save", "image: %s", e.message);
    goto done;
  }
  if (xi_avif_hdr_fill(f, img, bits, &cll)) {
    *err = xi_error_copy();
    goto done;
  }
  heif_image_set_content_light_level(img, &cll);

  nclx = heif_nclx_color_profile_alloc();
  if (!nclx) {
    *err = xi_error_new("xi_avif_hdr_save", "out of memory");
    goto done;
  }
  heif_nclx_color_profile_set_color_primaries(nclx, 9);         // BT.2020
  heif_nclx_color_profile_set_transfer_characteristics(nclx, 16); // PQ
  heif_nclx_color_profile_set_matrix_coefficients(nclx, 9);     // BT.2020 NCL
  nclx->full_range_flag = 1;
  heif_image_set_nclx_color_profile(img, nclx);
  opts = heif_encoding_options_alloc();
  opts->output_nclx_profile = nclx;
  opts->save_alpha_channel = alpha;

  e = heif_context_encode_image(ctx, img, enc, opts, NULL);
  if (e.code != heif_error_Ok) {
    *err = xi_error_new("xi_avif_hdr_save", "encode: %s", e.message);
    goto done;
  }
  bytes = g_byte_array_new();
  e = heif_context_write(ctx, &writer, bytes);
  if (e.code != heif_error_Ok) {
    *err = xi_error_new("xi_avif_hdr_save", "write: %s", e.message);
    goto done;
  }
  *len = bytes->len;
  *buf = g_byte_array_free(bytes, FALSE);
  bytes = NULL;
  rc = XI_OK;

done:
  if (bytes)
    g_byte_array_free(bytes, TRUE);
  if (opts)
    heif_encoding_options_free(opts);
  if (nclx)
    heif_nclx_color_profile_free(nclx);
  if (img)
    heif_image_release(img);
  if (enc)
    heif_encoder_release(enc);
  xi_heif_close(&ctx);
  VIPS_UNREF(f);
  return rc;
}
