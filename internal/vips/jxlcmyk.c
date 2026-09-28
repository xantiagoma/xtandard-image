// Hand-written C shim for internal/vips: CMYK JPEG XL through libjxl
// (BSD-3-Clause, already in the base image for libvips' jxlload). See
// shim.h for the licence notice.
//
// A CMYK JPEG XL stores C, M and Y as its three colour channels and K as an
// extra channel of type JXL_CHANNEL_BLACK, with a CMYK ICC profile. libvips
// 8.18's jxlload asks libjxl for every channel interleaved in one buffer,
// which libjxl refuses for more than four ("JxlDecoderImageOutBufferSize"),
// so such files do not load. This file registers a loader class for exactly
// those files, XiForeignLoadJxlCmykBuffer (nickname "jxlcmykload_buffer"),
// ahead of jxlload: is_a_buffer accepts a JPEG XL stream only when its
// basic info declares a black channel. It decodes the first frame (layers
// coalesced, orientation applied) with the colour channels in one buffer
// and K (and alpha, if any) in extra-channel buffers, and interleaves them
// as libvips CMYK[A] (0 = no ink), 8 or 16 bits, with the embedded profile,
// so the pipeline converts it to sRGB like a CMYK JPEG. Every other JPEG XL
// file still goes to jxlload.
//
// libjxl reads the blob's C memory, which the loader keeps a reference to;
// nothing points to Go memory. Pixels are decoded into g_malloc'd buffers
// that the output image owns.

#include <string.h>

#include "shim.h"

#include <jxl/decode.h>

// Live decoders and decoded buffers, for leak tests (libjxl's memory is not
// tracked by libvips).
static int xi_jxlcmyk_decoders, xi_jxlcmyk_buffers;

int
xi_jxlcmyk_live(void)
{
  return __atomic_load_n(&xi_jxlcmyk_decoders, __ATOMIC_RELAXED) +
      __atomic_load_n(&xi_jxlcmyk_buffers, __ATOMIC_RELAXED);
}

// Largest decode the loader accepts regardless of the configured limits
// (checked on the header before any pixel is decoded): it keeps every size
// computation below far from overflow.
#define XI_JXLCMYK_MAX_PIXELS ((size_t) 1 << 30)

typedef struct {
  JxlDecoder *dec;
  int width, height; // as shown (orientation applied)
  int bands;         // 4 (CMYK) or 5 (CMYK + alpha)
  VipsBandFormat format;
  int black, alpha; // extra channel indices; alpha -1 when absent
  void *icc;
  size_t icc_len;
} XiJxlCmyk;

typedef struct _XiForeignLoadJxlCmykBuffer {
  VipsForeignLoad parent_object;

  VipsBlob *blob;
  XiJxlCmyk jxl;
} XiForeignLoadJxlCmykBuffer;

typedef VipsForeignLoadClass XiForeignLoadJxlCmykBufferClass;

G_DEFINE_TYPE(XiForeignLoadJxlCmykBuffer, xi_foreign_load_jxlcmyk_buffer, VIPS_TYPE_FOREIGN_LOAD);

static void
xi_jxlcmyk_close(XiJxlCmyk *j)
{
  if (j->dec) {
    JxlDecoderDestroy(j->dec);
    j->dec = NULL;
    __atomic_sub_fetch(&xi_jxlcmyk_decoders, 1, __ATOMIC_RELAXED);
  }
}

static void
xi_jxlcmyk_clear(XiJxlCmyk *j)
{
  xi_jxlcmyk_close(j);
  g_free(j->icc);
  j->icc = NULL;
  j->icc_len = 0;
}

// xi_jxlcmyk_pixel_format is the libjxl buffer format for n channels.
static JxlPixelFormat
xi_jxlcmyk_pixel_format(const XiJxlCmyk *j, uint32_t n)
{
  JxlPixelFormat f = { n, j->format == VIPS_FORMAT_USHORT ? JXL_TYPE_UINT16 : JXL_TYPE_UINT8,
    JXL_NATIVE_ENDIAN, 0 };
  return f;
}

// xi_jxlcmyk_start creates a decoder over data that reports events, with
// the whole input set and closed.
static JxlDecoder *
xi_jxlcmyk_start(const void *data, size_t len, int events)
{
  JxlDecoder *dec = JxlDecoderCreate(NULL);

  if (!dec)
    return NULL;
  if (JxlDecoderSubscribeEvents(dec, events) != JXL_DEC_SUCCESS ||
      JxlDecoderSetInput(dec, data, len) != JXL_DEC_SUCCESS) {
    JxlDecoderDestroy(dec);
    return NULL;
  }
  JxlDecoderCloseInput(dec);
  return dec;
}

// xi_jxlcmyk_black finds the black extra channel (and the first alpha
// channel) of the basic info just read; FALSE when there is none.
static gboolean
xi_jxlcmyk_black(JxlDecoder *dec, const JxlBasicInfo *info, int *black, int *alpha)
{
  *black = -1;
  *alpha = -1;
  for (uint32_t i = 0; i < info->num_extra_channels && i < 256; i++) {
    JxlExtraChannelInfo ec;

    if (JxlDecoderGetExtraChannelInfo(dec, i, &ec) != JXL_DEC_SUCCESS)
      return FALSE;
    if (ec.type == JXL_CHANNEL_BLACK && *black < 0)
      *black = (int) i;
    else if (ec.type == JXL_CHANNEL_ALPHA && *alpha < 0)
      *alpha = (int) i;
  }
  return *black >= 0 && info->num_color_channels == 3;
}

// xi_is_jxl_cmyk reports a JPEG XL stream whose basic info declares a black
// extra channel.
gboolean
xi_is_jxl_cmyk(const void *buf, size_t len)
{
  JxlSignature sig = JxlSignatureCheck(buf, len < 12 ? len : 12);
  JxlDecoder *dec;
  JxlBasicInfo info;
  gboolean yes = FALSE;
  int black, alpha;

  if (sig != JXL_SIG_CODESTREAM && sig != JXL_SIG_CONTAINER)
    return FALSE;
  if (!(dec = xi_jxlcmyk_start(buf, len, JXL_DEC_BASIC_INFO)))
    return FALSE;
  if (JxlDecoderProcessInput(dec) == JXL_DEC_BASIC_INFO &&
      JxlDecoderGetBasicInfo(dec, &info) == JXL_DEC_SUCCESS)
    yes = xi_jxlcmyk_black(dec, &info, &black, &alpha);
  JxlDecoderDestroy(dec);
  return yes;
}

// xi_jxlcmyk_open reads the basic info and the ICC profile and leaves the
// decoder ready for the frame.
static int
xi_jxlcmyk_open(XiJxlCmyk *j, VipsBlob *blob, const char *nickname)
{
  VipsArea *area = VIPS_AREA(blob);
  JxlBasicInfo info;
  JxlDecoderStatus st;

  memset(j, 0, sizeof(*j));
  j->alpha = -1;
  if (!(j->dec = xi_jxlcmyk_start(area->data, area->length,
            JXL_DEC_BASIC_INFO | JXL_DEC_COLOR_ENCODING | JXL_DEC_FULL_IMAGE))) {
    vips_error(nickname, "cannot create the JPEG XL decoder");
    return -1;
  }
  __atomic_add_fetch(&xi_jxlcmyk_decoders, 1, __ATOMIC_RELAXED);

  if (JxlDecoderProcessInput(j->dec) != JXL_DEC_BASIC_INFO ||
      JxlDecoderGetBasicInfo(j->dec, &info) != JXL_DEC_SUCCESS)
    goto bad;
  if (!xi_jxlcmyk_black(j->dec, &info, &j->black, &j->alpha))
    goto bad;
  j->width = (int) info.xsize;
  j->height = (int) info.ysize;
  if (info.orientation >= JXL_ORIENT_TRANSPOSE) { // 5-8 swap the axes
    j->width = (int) info.ysize;
    j->height = (int) info.xsize;
  }
  if (info.xsize == 0 || info.ysize == 0 || info.xsize > VIPS_MAX_COORD ||
      info.ysize > VIPS_MAX_COORD ||
      (size_t) info.xsize * info.ysize > XI_JXLCMYK_MAX_PIXELS) {
    xi_jxlcmyk_clear(j);
    vips_error(nickname, "JPEG XL image too large");
    return -1;
  }
  j->bands = j->alpha >= 0 ? 5 : 4;
  j->format = info.bits_per_sample > 8 || info.exponent_bits_per_sample > 0 ? VIPS_FORMAT_USHORT
                                                                            : VIPS_FORMAT_UCHAR;

  if ((st = JxlDecoderProcessInput(j->dec)) != JXL_DEC_COLOR_ENCODING)
    goto bad;
  if (JxlDecoderGetICCProfileSize(j->dec, JXL_COLOR_PROFILE_TARGET_DATA, &j->icc_len) ==
          JXL_DEC_SUCCESS &&
      j->icc_len > 0 && j->icc_len < 16 * 1024 * 1024) {
    j->icc = g_malloc(j->icc_len);
    if (JxlDecoderGetColorAsICCProfile(j->dec, JXL_COLOR_PROFILE_TARGET_DATA, j->icc,
            j->icc_len) != JXL_DEC_SUCCESS) {
      g_free(j->icc);
      j->icc = NULL;
      j->icc_len = 0;
    }
  }
  else
    j->icc_len = 0;
  return 0;

bad:
  xi_jxlcmyk_clear(j);
  vips_error(nickname, "bad CMYK JPEG XL header");
  return -1;
}

static void
xi_jxlcmyk_set_fields(XiJxlCmyk *j, VipsImage *out)
{
  vips_image_init_fields(out, j->width, j->height, j->bands, j->format, VIPS_CODING_NONE,
      VIPS_INTERPRETATION_CMYK, 1.0, 1.0);
  if (j->icc)
    vips_image_set_blob_copy(out, VIPS_META_ICC_NAME, j->icc, j->icc_len);
}

static void
xi_jxlcmyk_free_pixels(VipsImage *img, gpointer pixels)
{
  (void) img;
  g_free(pixels);
  __atomic_sub_fetch(&xi_jxlcmyk_buffers, 1, __ATOMIC_RELAXED);
}

// xi_jxlcmyk_decode decodes the first frame into a new buffer of
// interleaved C, M, Y, K[, A] samples, inks inverted to 0 = no ink.
static void *
xi_jxlcmyk_decode(XiJxlCmyk *j, const char *nickname)
{
  size_t sample = vips_format_sizeof(j->format);
  size_t n = (size_t) j->width * j->height;
  JxlPixelFormat cf = xi_jxlcmyk_pixel_format(j, 3);
  JxlPixelFormat ef = xi_jxlcmyk_pixel_format(j, 1);
  guchar *colour = NULL, *black = NULL, *alpha = NULL, *out = NULL;
  JxlDecoderStatus st;

  colour = g_try_malloc(n * 3 * sample);
  black = g_try_malloc(n * sample);
  alpha = j->alpha >= 0 ? g_try_malloc(n * sample) : NULL;
  out = g_try_malloc(n * j->bands * sample);
  if (!colour || !black || (j->alpha >= 0 && !alpha) || !out) {
    vips_error(nickname, "out of memory decoding %dx%d", j->width, j->height);
    goto fail;
  }
  for (;;) {
    st = JxlDecoderProcessInput(j->dec);
    if (st == JXL_DEC_NEED_IMAGE_OUT_BUFFER) {
      size_t size;

      if (JxlDecoderImageOutBufferSize(j->dec, &cf, &size) != JXL_DEC_SUCCESS ||
          size != n * 3 * sample ||
          JxlDecoderSetImageOutBuffer(j->dec, &cf, colour, size) != JXL_DEC_SUCCESS ||
          JxlDecoderExtraChannelBufferSize(j->dec, &ef, &size, (uint32_t) j->black) !=
              JXL_DEC_SUCCESS ||
          size != n * sample ||
          JxlDecoderSetExtraChannelBuffer(j->dec, &ef, black, size, (uint32_t) j->black) !=
              JXL_DEC_SUCCESS) {
        vips_error(nickname, "cannot set the JPEG XL output buffers");
        goto fail;
      }
      if (alpha && (JxlDecoderExtraChannelBufferSize(j->dec, &ef, &size,
                        (uint32_t) j->alpha) != JXL_DEC_SUCCESS ||
                       size != n * sample ||
                       JxlDecoderSetExtraChannelBuffer(j->dec, &ef, alpha, size,
                           (uint32_t) j->alpha) != JXL_DEC_SUCCESS)) {
        vips_error(nickname, "cannot set the JPEG XL alpha buffer");
        goto fail;
      }
    }
    else if (st == JXL_DEC_FULL_IMAGE)
      break;
    else if (st != JXL_DEC_FRAME && st != JXL_DEC_COLOR_ENCODING) {
      vips_error(nickname, "JPEG XL decode failed");
      goto fail;
    }
  }

  // Interleave. JPEG XL stores inks like RGB, 1.0 = no ink (the colour
  // channels and K alike); libvips CMYK is 0 = no ink.
  if (j->format == VIPS_FORMAT_USHORT) {
    const guint16 *c = (const guint16 *) colour, *k = (const guint16 *) black;
    const guint16 *a = (const guint16 *) alpha;
    guint16 *o = (guint16 *) out;

    for (size_t i = 0; i < n; i++, o += j->bands) {
      o[0] = 65535 - c[3 * i];
      o[1] = 65535 - c[3 * i + 1];
      o[2] = 65535 - c[3 * i + 2];
      o[3] = 65535 - k[i];
      if (a)
        o[4] = a[i];
    }
  }
  else {
    guchar *o = out;

    for (size_t i = 0; i < n; i++, o += j->bands) {
      o[0] = 255 - colour[3 * i];
      o[1] = 255 - colour[3 * i + 1];
      o[2] = 255 - colour[3 * i + 2];
      o[3] = 255 - black[i];
      if (alpha)
        o[4] = alpha[i];
    }
  }
  g_free(colour);
  g_free(black);
  g_free(alpha);
  __atomic_add_fetch(&xi_jxlcmyk_buffers, 1, __ATOMIC_RELAXED);
  return out;

fail:
  g_free(colour);
  g_free(black);
  g_free(alpha);
  g_free(out);
  return NULL;
}

static void
xi_foreign_load_jxlcmyk_buffer_dispose(GObject *gobject)
{
  XiForeignLoadJxlCmykBuffer *jxl = (XiForeignLoadJxlCmykBuffer *) gobject;

  xi_jxlcmyk_clear(&jxl->jxl);
  G_OBJECT_CLASS(xi_foreign_load_jxlcmyk_buffer_parent_class)->dispose(gobject);
}

static VipsForeignFlags
xi_foreign_load_jxlcmyk_buffer_get_flags(VipsForeignLoad *load)
{
  (void) load;
  return 0;
}

static int
xi_foreign_load_jxlcmyk_buffer_header(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadJxlCmykBuffer *jxl = (XiForeignLoadJxlCmykBuffer *) load;

  if (xi_jxlcmyk_open(&jxl->jxl, jxl->blob, class->nickname))
    return -1;
  xi_jxlcmyk_set_fields(&jxl->jxl, load->out);
  // The header is all a probe needs: release the decoder now; load opens
  // it again.
  xi_jxlcmyk_close(&jxl->jxl);
  return 0;
}

static int
xi_foreign_load_jxlcmyk_buffer_load(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadJxlCmykBuffer *jxl = (XiForeignLoadJxlCmykBuffer *) load;
  XiJxlCmyk *j = &jxl->jxl;
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(load), 1);
  void *pixels;
  size_t size;

  xi_jxlcmyk_clear(j);
  if (xi_jxlcmyk_open(j, jxl->blob, class->nickname))
    return -1;
  pixels = xi_jxlcmyk_decode(j, class->nickname);
  // The decoder is done: its memory goes now, not with the load.
  xi_jxlcmyk_close(j);
  if (!pixels)
    return -1;

  size = (size_t) j->width * j->height * j->bands * vips_format_sizeof(j->format);
  if (!(t[0] = vips_image_new_from_memory(pixels, size, j->width, j->height, j->bands,
            j->format))) {
    xi_jxlcmyk_free_pixels(NULL, pixels);
    return -1;
  }
  g_signal_connect(t[0], "close", G_CALLBACK(xi_jxlcmyk_free_pixels), pixels);
  xi_jxlcmyk_set_fields(j, t[0]);
  return vips_image_write(t[0], load->real);
}

static gboolean
xi_foreign_load_jxlcmyk_buffer_is_a_buffer(const void *buf, size_t len)
{
  return xi_is_jxl_cmyk(buf, len);
}

static const char *xi_jxlcmyk_suffs[] = { ".jxl", NULL };

static void
xi_foreign_load_jxlcmyk_buffer_class_init(XiForeignLoadJxlCmykBufferClass *class)
{
  GObjectClass *gobject_class = G_OBJECT_CLASS(class);
  VipsObjectClass *object_class = (VipsObjectClass *) class;
  VipsOperationClass *operation_class = VIPS_OPERATION_CLASS(class);
  VipsForeignClass *foreign_class = (VipsForeignClass *) class;
  VipsForeignLoadClass *load_class = (VipsForeignLoadClass *) class;

  gobject_class->dispose = xi_foreign_load_jxlcmyk_buffer_dispose;
  gobject_class->set_property = vips_object_set_property;
  gobject_class->get_property = vips_object_get_property;

  object_class->nickname = "jxlcmykload_buffer";
  object_class->description = "load CMYK JPEG XL from buffer (libjxl)";

  // Like jxlload in 8.18: unblocked by name only.
  operation_class->flags |= VIPS_OPERATION_UNTRUSTED;

  foreign_class->suffs = xi_jxlcmyk_suffs;
  // Ahead of jxlload, which claims every JPEG XL stream.
  foreign_class->priority = 100;

  load_class->get_flags = xi_foreign_load_jxlcmyk_buffer_get_flags;
  load_class->header = xi_foreign_load_jxlcmyk_buffer_header;
  load_class->load = xi_foreign_load_jxlcmyk_buffer_load;
  load_class->is_a_buffer = xi_foreign_load_jxlcmyk_buffer_is_a_buffer;

  VIPS_ARG_BOXED(class, "buffer", 1, "Buffer", "Buffer to load from",
      VIPS_ARGUMENT_REQUIRED_INPUT, G_STRUCT_OFFSET(XiForeignLoadJxlCmykBuffer, blob),
      VIPS_TYPE_BLOB);
}

static void
xi_foreign_load_jxlcmyk_buffer_init(XiForeignLoadJxlCmykBuffer *jxl)
{
  (void) jxl;
}

void
xi_jxlcmyk_register(void)
{
  // Referencing the class registers it with libvips' loader search.
  g_type_class_unref(g_type_class_ref(xi_foreign_load_jxlcmyk_buffer_get_type()));
}
