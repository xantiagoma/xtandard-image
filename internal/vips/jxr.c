// Hand-written C shim for internal/vips: JPEG XR (ITU-T T.832, HD Photo)
// through jxrlib (Microsoft, BSD-2-Clause). See shim.h for the licence
// notice.
//
// libvips has no JPEG XR loader. This file registers one, a VipsForeignLoad
// subclass named XiForeignLoadJxrBuffer (nickname "jxrload_buffer"), so the
// format goes through the same paths as every libvips format: sniffing
// (is_a_buffer), header-only probes, lazy loads and vips_thumbnail_buffer.
// The class is flagged untrusted like the other non-core loaders and is
// unblocked by name (vips.DefaultLoaders).
//
// jxrlib reads the source through its memory stream (CreateWS_Memory) over
// the blob's C memory, which the loader keeps a reference to; nothing points
// to Go memory. Pixels are decoded into a g_malloc'd buffer that the output
// image owns.
//
// Output: the pixel format stored in the file when libvips can hold it as is
// (8-bit grey, RGB and RGBA, their 16-bit variants and 8/16-bit CMYK with or
// without alpha; BGR orders are swapped to RGB); every other format (1-bit,
// 5/6/10-bit packed, half, float, fixed point, RGBE) goes through jxrlib's
// own converters to 8-bit grey, RGB or RGBA. N-channel and YCC formats are
// refused.

#include <string.h>

// libvips and glib first: jxrlib's SAL annotation macros (wmsal.h: __reserved,
// __in, __out, ...) would rewrite identifiers in any header included after it.
#include "shim.h"

#include <JXRGlue.h>

// Live decoders and decoded buffers, for leak tests (jxrlib's memory is not
// tracked by libvips).
static int xi_jxr_decoders, xi_jxr_buffers;

int
xi_jxr_live(void)
{
  return __atomic_load_n(&xi_jxr_decoders, __ATOMIC_RELAXED) +
      __atomic_load_n(&xi_jxr_buffers, __ATOMIC_RELAXED);
}

// Largest decode the loader accepts regardless of the configured limits
// (they are checked on the header before any pixel is decoded): it keeps
// every size computation below far from overflow.
#define XI_JXR_MAX_PIXELS ((size_t) 1 << 30)

// How the decoded pixels reach the vips image.
typedef enum {
  XI_JXR_NATIVE,  // the stored format is what the image holds
  XI_JXR_SWAP,    // BGR(A), BGRX or RGBX: reorder to RGB(A), drop the padding
  XI_JXR_CONVERT, // jxrlib's converter to 8-bit grey, RGB or RGBA
} XiJxrPath;

typedef struct {
  PKImageDecode *dec;
  int width, height;    // as shown (orientation applied)
  int bands;
  VipsBandFormat format;
  VipsInterpretation interpretation;
  XiJxrPath path;
  PKPixelFormatGUID target; // XI_JXR_CONVERT only
  size_t native_bits;   // bits per pixel of the stored format
  gboolean bgr;         // XI_JXR_SWAP: stored blue first
  double xres, yres;    // pixels per millimetre
} XiJxr;

typedef struct _XiForeignLoadJxrBuffer {
  VipsForeignLoad parent_object;

  VipsBlob *blob;
  XiJxr jxr;
} XiForeignLoadJxrBuffer;

typedef VipsForeignLoadClass XiForeignLoadJxrBufferClass;

G_DEFINE_TYPE(XiForeignLoadJxrBuffer, xi_foreign_load_jxr_buffer, VIPS_TYPE_FOREIGN_LOAD);

// xi_is_jxr reports the JPEG XR / HD Photo container signature: "II", 0xBC
// and the version (0 or 1), the only ones jxrlib accepts.
gboolean
xi_is_jxr(const void *buf, size_t len)
{
  const unsigned char *p = buf;

  return len >= 8 && p[0] == 'I' && p[1] == 'I' && p[2] == 0xBC && p[3] <= 1;
}

static void
xi_jxr_close(XiJxr *j)
{
  if (j->dec) {
    j->dec->Release(&j->dec);
    j->dec = NULL;
    __atomic_sub_fetch(&xi_jxr_decoders, 1, __ATOMIC_RELAXED);
  }
}

static int
xi_jxr_error(const char *nickname, const char *what, ERR e)
{
  vips_error(nickname, "%s (jxrlib error %d)", what, (int) e);
  return -1;
}

// xi_jxr_layout picks how the stored pixel format pi reaches libvips.
static int
xi_jxr_layout(XiJxr *j, const PKPixelInfo *pi, const char *nickname)
{
  gboolean alpha = (pi->grBit & PK_pixfmtHasAlpha) != 0;
  gboolean bgr = (pi->grBit & PK_pixfmtBGR) != 0;

  j->native_bits = pi->cbitUnit;
  j->format = VIPS_FORMAT_UCHAR;
  j->path = XI_JXR_NATIVE;

  switch (pi->cfColorFormat) {
  case Y_ONLY:
    j->bands = 1;
    j->interpretation = VIPS_INTERPRETATION_B_W;
    if (pi->bdBitDepth == BD_8 && pi->cbitUnit == 8)
      return 0;
    if (pi->bdBitDepth == BD_16 && pi->cbitUnit == 16) {
      j->format = VIPS_FORMAT_USHORT;
      j->interpretation = VIPS_INTERPRETATION_GREY16;
      return 0;
    }
    j->path = XI_JXR_CONVERT;
    j->target = GUID_PKPixelFormat8bppGray;
    return 0;

  case CF_RGB:
    j->bands = alpha ? 4 : 3;
    j->interpretation = VIPS_INTERPRETATION_sRGB;
    if (pi->bdBitDepth == BD_8 && (pi->cbitUnit == 24 || pi->cbitUnit == 32)) {
      if (bgr || pi->cbitUnit != (guint) j->bands * 8) {
        j->path = XI_JXR_SWAP;
        j->bgr = bgr;
      }
      return 0;
    }
    if (pi->bdBitDepth == BD_16 && !bgr && pi->cbitUnit == (guint) j->bands * 16) {
      j->format = VIPS_FORMAT_USHORT;
      j->interpretation = VIPS_INTERPRETATION_RGB16;
      return 0;
    }
    j->path = XI_JXR_CONVERT;
    j->target = alpha ? GUID_PKPixelFormat32bppRGBA : GUID_PKPixelFormat24bppRGB;
    return 0;

  case CF_RGBE:
    j->bands = 3;
    j->interpretation = VIPS_INTERPRETATION_sRGB;
    j->path = XI_JXR_CONVERT;
    j->target = GUID_PKPixelFormat24bppRGB;
    return 0;

  case CMYK:
    j->bands = alpha ? 5 : 4;
    j->interpretation = VIPS_INTERPRETATION_CMYK;
    if (pi->bdBitDepth == BD_8 && pi->cbitUnit == (guint) j->bands * 8)
      return 0;
    if (pi->bdBitDepth == BD_16 && pi->cbitUnit == (guint) j->bands * 16) {
      j->format = VIPS_FORMAT_USHORT;
      return 0;
    }
    break;

  default:
    break;
  }
  vips_error(nickname, "unsupported JPEG XR pixel format");
  return -1;
}

// xi_jxr_open reads the container and codestream headers of the blob and
// prepares the decoder like jxrlib's JxrDecApp does for a decode to the
// stored pixel format.
static int
xi_jxr_open(XiJxr *j, VipsBlob *blob, const char *nickname)
{
  VipsArea *area = VIPS_AREA(blob);
  struct WMPStream *stream = NULL;
  PKPixelInfo pi;
  PKPixelFormatGUID guid;
  CWMImageInfo *ii;
  Float rx = 0, ry = 0;
  I32 w, h;
  ERR e;

  memset(j, 0, sizeof(*j));
  if (!xi_is_jxr(area->data, area->length)) {
    vips_error(nickname, "not a JPEG XR file");
    return -1;
  }
  // The stream only reads: jxrlib's memory stream API is not const.
  if ((e = CreateWS_Memory(&stream, (void *) area->data, area->length)) != WMP_errSuccess)
    return xi_jxr_error(nickname, "cannot create stream", e);
  if ((e = PKImageDecode_Create_WMP(&j->dec)) != WMP_errSuccess) {
    stream->Close(&stream);
    return xi_jxr_error(nickname, "cannot create decoder", e);
  }
  __atomic_add_fetch(&xi_jxr_decoders, 1, __ATOMIC_RELAXED);
  // The decoder owns the stream from here on (Release closes it).
  j->dec->fStreamOwner = TRUE;
  if ((e = j->dec->Initialize(j->dec, stream)) != WMP_errSuccess) {
    if (!j->dec->pStream)
      stream->Close(&stream);
    xi_jxr_close(j);
    return xi_jxr_error(nickname, "bad JPEG XR header", e);
  }

  if (j->dec->GetPixelFormat(j->dec, &guid) != WMP_errSuccess)
    goto bad;
  memset(&pi, 0, sizeof(pi));
  pi.pGUIDPixFmt = &guid;
  if (PixelFormatLookup(&pi, LOOKUP_FORWARD) != WMP_errSuccess)
    goto bad;
  if (xi_jxr_layout(j, &pi, nickname)) {
    xi_jxr_close(j);
    return -1;
  }

  // The codestream header declares its output format too. jxrlib can
  // transcode between some formats (JxrDecApp asks it to by overwriting
  // these fields), but a container that disagrees with its codestream is a
  // corrupt or hostile file, and decoding it through that path is where
  // fuzzing found jxrlib writing past its buffers: refuse it, and decode to
  // the stored format only.
  ii = &j->dec->WMP.wmiI;
  if (ii->cfColorFormat != pi.cfColorFormat || ii->bdBitDepth != pi.bdBitDepth ||
      ii->cBitsPerUnit != pi.cbitUnit) {
    xi_jxr_close(j);
    vips_error(nickname, "JPEG XR container and codestream pixel formats differ");
    return -1;
  }

  // As JxrDecApp: decode the whole image, with alpha when the format has
  // it, in the container's orientation.
  ii->cThumbnailWidth = ii->cWidth;
  ii->cThumbnailHeight = ii->cHeight;
  ii->bSkipFlexbits = FALSE;
  ii->cROILeftX = 0;
  ii->cROITopY = 0;
  ii->cROIWidth = ii->cThumbnailWidth;
  ii->cROIHeight = ii->cThumbnailHeight;
  ii->cPostProcStrength = 0;
  j->dec->WMP.wmiSCP.uAlphaMode = (pi.grBit & PK_pixfmtHasAlpha) ? 2 : 0;
  j->dec->WMP.wmiSCP.sbSubband = SB_ALL;
  j->dec->WMP.wmiSCP.bfBitstreamFormat = SPATIAL;
  j->dec->WMP.bIgnoreOverlap = FALSE;
  j->dec->WMP.wmiSCP.bVerbose = FALSE;

  w = (I32) ii->cROIWidth;
  h = (I32) ii->cROIHeight;
  if (ii->oOrientation > O_FLIPVH) {
    I32 t = w;

    w = h;
    h = t;
  }
  if (w <= 0 || h <= 0 || w >= VIPS_MAX_COORD || h >= VIPS_MAX_COORD ||
      (size_t) w * (size_t) h > XI_JXR_MAX_PIXELS) {
    vips_error(nickname, "bad JPEG XR dimensions %dx%d", (int) w, (int) h);
    xi_jxr_close(j);
    return -1;
  }
  j->width = w;
  j->height = h;

  // Resolution in DPI; libvips wants pixels per millimetre.
  j->xres = j->yres = 1.0;
  if (j->dec->GetResolution(j->dec, &rx, &ry) == WMP_errSuccess && rx > 0 && ry > 0 &&
      rx < 1e6 && ry < 1e6) {
    j->xres = rx / 25.4;
    j->yres = ry / 25.4;
  }
  return 0;

bad:
  xi_jxr_close(j);
  vips_error(nickname, "bad JPEG XR pixel format");
  return -1;
}

static void
xi_jxr_set_fields(XiJxr *j, VipsImage *out, PKImageDecode *dec)
{
  U32 n = 0;

  vips_image_init_fields(out, j->width, j->height, j->bands, j->format, VIPS_CODING_NONE,
      j->interpretation, j->xres, j->yres);
  // An embedded ICC profile (the colour context).
  if (dec && dec->GetColorContext(dec, NULL, &n) == WMP_errSuccess && n > 0 &&
      n < 16 * 1024 * 1024) {
    U8 *icc = g_malloc(n);

    if (dec->GetColorContext(dec, icc, &n) == WMP_errSuccess && n > 0)
      vips_image_set_blob_copy(out, VIPS_META_ICC_NAME, icc, n);
    g_free(icc);
  }
}

static void
xi_jxr_free_pixels(VipsImage *img, gpointer pixels)
{
  (void) img;
  g_free(pixels);
  __atomic_sub_fetch(&xi_jxr_buffers, 1, __ATOMIC_RELAXED);
}

// xi_jxr_decode decodes every pixel into a new buffer laid out as the vips
// image wants it (packed rows of j->bands samples).
static void *
xi_jxr_decode(XiJxr *j, const char *nickname)
{
  size_t sample = vips_format_sizeof(j->format);
  size_t out_line = (size_t) j->width * j->bands * sample;
  size_t native_line = ((size_t) j->width * j->native_bits + 7) / 8;
  size_t target_line = j->path == XI_JXR_CONVERT ? out_line : native_line;
  // jxrlib writes the stored format and converts in place, so a row must
  // hold the larger of both; 16-byte aligned rows.
  size_t stride = (MAX(native_line, target_line) + 15) & ~(size_t) 15;
  PKFormatConverter *conv = NULL;
  PKRect rect = { 0, 0, j->width, j->height };
  guchar *buf, *out;
  ERR e;

  if (stride > G_MAXUINT32 || stride > G_MAXSIZE / (size_t) j->height) {
    vips_error(nickname, "JPEG XR image too large");
    return NULL;
  }
  if (!(buf = g_try_malloc0(stride * (size_t) j->height))) {
    vips_error(nickname, "out of memory decoding %dx%d", j->width, j->height);
    return NULL;
  }

  if (j->path == XI_JXR_CONVERT) {
    if ((e = PKCodecFactory_CreateFormatConverter(&conv)) != WMP_errSuccess) {
      g_free(buf);
      xi_jxr_error(nickname, "cannot create converter", e);
      return NULL;
    }
    if ((e = conv->Initialize(conv, j->dec, NULL, j->target)) != WMP_errSuccess) {
      conv->Release(&conv);
      g_free(buf);
      vips_error(nickname, "unsupported JPEG XR pixel format conversion");
      return NULL;
    }
    e = conv->Copy(conv, &rect, buf, (U32) stride);
    conv->Release(&conv);
  }
  else
    e = j->dec->Copy(j->dec, &rect, buf, (U32) stride);
  if (e != WMP_errSuccess) {
    g_free(buf);
    xi_jxr_error(nickname, "JPEG XR decode failed", e);
    return NULL;
  }

  // Pack the rows (and swap BGR) into the output layout, in place: every
  // output row is at most as long as its source row and starts no later.
  out = buf;
  for (int y = 0; y < j->height; y++) {
    const guchar *src = buf + (size_t) y * stride;
    guchar *dst = out + (size_t) y * out_line;

    if (j->path == XI_JXR_SWAP) {
      size_t step = j->native_bits / 8;
      int ri = j->bgr ? 2 : 0, bi = j->bgr ? 0 : 2;

      // dst never overtakes src: a pixel shrinks or keeps its size.
      for (int x = 0; x < j->width; x++) {
        const guchar *s = src + (size_t) x * step;
        guchar *d = dst + (size_t) x * j->bands;
        guchar r = s[ri], g = s[1], b = s[bi];

        if (j->bands == 4)
          d[3] = s[3];
        d[0] = r;
        d[1] = g;
        d[2] = b;
      }
    }
    else
      memmove(dst, src, out_line);
  }
  __atomic_add_fetch(&xi_jxr_buffers, 1, __ATOMIC_RELAXED);
  return buf;
}

static void
xi_foreign_load_jxr_buffer_dispose(GObject *gobject)
{
  XiForeignLoadJxrBuffer *jxr = (XiForeignLoadJxrBuffer *) gobject;

  xi_jxr_close(&jxr->jxr);
  G_OBJECT_CLASS(xi_foreign_load_jxr_buffer_parent_class)->dispose(gobject);
}

static VipsForeignFlags
xi_foreign_load_jxr_buffer_get_flags(VipsForeignLoad *load)
{
  (void) load;
  return 0;
}

static int
xi_foreign_load_jxr_buffer_header(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadJxrBuffer *jxr = (XiForeignLoadJxrBuffer *) load;

  if (xi_jxr_open(&jxr->jxr, jxr->blob, class->nickname))
    return -1;
  xi_jxr_set_fields(&jxr->jxr, load->out, jxr->jxr.dec);
  return 0;
}

static int
xi_foreign_load_jxr_buffer_load(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadJxrBuffer *jxr = (XiForeignLoadJxrBuffer *) load;
  XiJxr *j = &jxr->jxr;
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(load), 1);
  void *pixels;
  size_t size;

  if (!j->dec && xi_jxr_open(j, jxr->blob, class->nickname))
    return -1;
  pixels = xi_jxr_decode(j, class->nickname);
  // The decoder is done: its stream and tables go now, not with the load.
  xi_jxr_close(j);
  if (!pixels)
    return -1;

  size = (size_t) j->width * j->height * j->bands * vips_format_sizeof(j->format);
  if (!(t[0] = vips_image_new_from_memory(pixels, size, j->width, j->height, j->bands,
            j->format))) {
    xi_jxr_free_pixels(NULL, pixels);
    return -1;
  }
  g_signal_connect(t[0], "close", G_CALLBACK(xi_jxr_free_pixels), pixels);
  xi_jxr_set_fields(j, t[0], NULL);
  return vips_image_write(t[0], load->real);
}

static gboolean
xi_foreign_load_jxr_buffer_is_a_buffer(const void *buf, size_t len)
{
  return xi_is_jxr(buf, len);
}

static const char *xi_jxr_suffs[] = { ".jxr", ".wdp", ".hdp", NULL };

static void
xi_foreign_load_jxr_buffer_class_init(XiForeignLoadJxrBufferClass *class)
{
  GObjectClass *gobject_class = G_OBJECT_CLASS(class);
  VipsObjectClass *object_class = (VipsObjectClass *) class;
  VipsOperationClass *operation_class = VIPS_OPERATION_CLASS(class);
  VipsForeignClass *foreign_class = (VipsForeignClass *) class;
  VipsForeignLoadClass *load_class = (VipsForeignLoadClass *) class;

  gobject_class->dispose = xi_foreign_load_jxr_buffer_dispose;
  gobject_class->set_property = vips_object_set_property;
  gobject_class->get_property = vips_object_get_property;

  object_class->nickname = "jxrload_buffer";
  object_class->description = "load JPEG XR from buffer (jxrlib)";

  // jxrlib is old code: unblocked by name only.
  operation_class->flags |= VIPS_OPERATION_UNTRUSTED;

  foreign_class->suffs = xi_jxr_suffs;
  foreign_class->priority = 100;

  load_class->get_flags = xi_foreign_load_jxr_buffer_get_flags;
  load_class->header = xi_foreign_load_jxr_buffer_header;
  load_class->load = xi_foreign_load_jxr_buffer_load;
  load_class->is_a_buffer = xi_foreign_load_jxr_buffer_is_a_buffer;

  VIPS_ARG_BOXED(class, "buffer", 1, "Buffer", "Buffer to load from",
      VIPS_ARGUMENT_REQUIRED_INPUT, G_STRUCT_OFFSET(XiForeignLoadJxrBuffer, blob),
      VIPS_TYPE_BLOB);
}

static void
xi_foreign_load_jxr_buffer_init(XiForeignLoadJxrBuffer *jxr)
{
  (void) jxr;
}

void
xi_jxr_register(void)
{
  // Referencing the class registers it with libvips' loader search.
  g_type_class_unref(g_type_class_ref(xi_foreign_load_jxr_buffer_get_type()));
}
