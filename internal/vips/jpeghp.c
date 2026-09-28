// Hand-written C shim for internal/vips: high-precision JPEG through
// libjpeg-turbo 3's 12- and 16-bit APIs (IJG and BSD-3-Clause, the library
// libvips' jpegload already uses). See shim.h for the licence notice.
//
// libvips 8.18 reads JPEG with libjpeg's 8-bit API only: a 12-bit DCT frame
// (ITU T.81 extended/progressive, 12 bits per sample) fails once decoding
// starts, and a lossless frame (T.81 process 14, SOF3) of 2-16 bits either
// fails (above 8 bits, or coded as YCbCr, which libjpeg-turbo cannot convert
// losslessly) or loads with its samples in the wrong range (below 8 bits).
// This file registers a loader class for exactly those files,
// XiForeignLoadJpegHpBuffer (nickname "jpeghpload_buffer"), ahead of
// jpegload: is_a_buffer walks the markers to the first frame header and
// accepts a 12-bit DCT frame or any Huffman lossless frame. Every 8-bit DCT
// JPEG still goes to jpegload (shrink-on-load, UltraHDR and all).
//
// Output: 8-bit samples as uchar, 9-16 bits as ushort scaled to the full
// 0-65535 range (v × 65535 / (2^P − 1)), so a 12-bit image is an ordinary
// RGB16/GREY16 libvips image; lossless 2-7 bits are scaled to 0-255. Colour:
// grey, RGB (YCbCr converted by libjpeg-turbo for DCT frames, and by this
// file, full-range JFIF, for lossless YCbCr, which libjpeg-turbo refuses),
// CMYK (inverted like jpegload: libvips CMYK is 0 = no ink) and lossless
// YCCK. Metadata as jpegload reads it: EXIF (libvips parses the orientation
// and resolution when the blob is attached), XMP, IPTC, the ICC profile and
// the JFIF resolution.
//
// Decoding is sequential through vips_image_generate and vips_sequential,
// as jpegload does, over libjpeg-turbo's memory source on the blob's C
// memory (the loader keeps a reference to the blob); nothing points to Go
// memory. The decoder state belongs to the generated image and is freed
// when that image closes.

#include <setjmp.h>
#include <stdio.h>
#include <string.h>

#include "shim.h"

#include <jerror.h>
#include <jpeglib.h>

// Live decompressors, for leak tests (libjpeg's memory is not tracked by
// libvips).
static int xi_jpeghp_decoders;

int
xi_jpeghp_live(void)
{
  return __atomic_load_n(&xi_jpeghp_decoders, __ATOMIC_RELAXED);
}

// Largest image the loader accepts regardless of the configured limits
// (checked on the header before any pixel is decoded).
#define XI_JPEGHP_MAX_PIXELS ((size_t) 1 << 30)

// Walk bound for the marker scan (xi_jpeg_frame).
#define XI_JPEG_MAX_MARKERS 4096

gboolean
xi_jpeg_frame(const void *buf, size_t len, int *marker, int *precision)
{
  const guchar *b = buf;
  size_t i = 2;

  if (len < 2 || b[0] != 0xFF || b[1] != 0xD8)
    return FALSE;
  for (int n = 0; n < XI_JPEG_MAX_MARKERS; n++) {
    size_t seg;
    int m;

    while (i + 1 < len && b[i] == 0xFF && b[i + 1] == 0xFF)
      i++; // fill bytes
    if (i + 4 > len || b[i] != 0xFF)
      return FALSE;
    m = b[i + 1];
    if (m == 0x01 || (m >= 0xD0 && m <= 0xD7)) {
      i += 2;
      continue;
    }
    if (m == 0xD8 || m == 0xD9 || m == 0xDA || m == 0x00)
      return FALSE;
    seg = (size_t) b[i + 2] << 8 | b[i + 3];
    if (seg < 2 || i + 2 + seg > len)
      return FALSE;
    if ((m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC) || m == 0xF7) {
      if (seg < 3)
        return FALSE;
      *marker = m;
      *precision = b[i + 4];
      return TRUE;
    }
    i += 2 + seg;
  }
  return FALSE;
}

// xi_jpeghp_kind classifies a frame the loader reads: 1 lossless Huffman
// (SOF3, 2-16 bits), 2 12-bit DCT (baseline, extended or progressive,
// Huffman or arithmetic), 0 anything else.
static int
xi_jpeghp_kind(int marker, int precision)
{
  switch (marker) {
  case 0xC3:
    return precision >= 2 && precision <= 16 ? 1 : 0;
  case 0xC0:
  case 0xC1:
  case 0xC2:
  case 0xC9:
  case 0xCA:
    return precision == 12 ? 2 : 0;
  default:
    return 0;
  }
}

gboolean
xi_is_jpeghp(const void *buf, size_t len)
{
  int marker, precision;

  return xi_jpeg_frame(buf, len, &marker, &precision) && xi_jpeghp_kind(marker, precision) != 0;
}

typedef struct {
  struct jpeg_error_mgr pub;
  jmp_buf jmp;
} XiJpegHpError;

typedef struct {
  struct jpeg_decompress_struct cinfo;
  XiJpegHpError err;
  gboolean created;
  VipsBlob *blob; // a reference: libjpeg reads its memory
  VipsFailOn fail_on;
  const char *nickname;
  char message[JMSG_LENGTH_MAX]; // the first libjpeg message

  int lossless;
  int precision;
  int width, height, bands;
  VipsBandFormat format;
  VipsInterpretation interpretation;
  gboolean invert; // CMYK: libjpeg's inks are inverted (255 = no ink)
  gboolean ycc;    // lossless YCbCr/YCCK: convert the first three samples here
  void *row;       // one scanline in libjpeg's sample type
} XiJpegHp;

static void
xi_jpeghp_error_exit(j_common_ptr cinfo)
{
  XiJpegHp *j = (XiJpegHp *) cinfo->client_data;

  (*cinfo->err->output_message)(cinfo);
  longjmp(j->err.jmp, 1);
}

static void
xi_jpeghp_output_message(j_common_ptr cinfo)
{
  XiJpegHp *j = (XiJpegHp *) cinfo->client_data;
  char text[JMSG_LENGTH_MAX];

  (*cinfo->err->format_message)(cinfo, text);
  if (!j->message[0])
    g_strlcpy(j->message, text, sizeof(j->message));
}

// xi_jpeghp_emit_message follows jpegload: warnings are counted and kept
// (the first one's text), a hundred of them are fatal, and a premature end
// of data is fatal under fail_on >= truncated (jpeg_mem_src inserts an EOI
// with a warning). Trace messages are dropped.
static void
xi_jpeghp_emit_message(j_common_ptr cinfo, int level)
{
  XiJpegHp *j = (XiJpegHp *) cinfo->client_data;

  if (level >= 0)
    return;
  cinfo->err->num_warnings++;
  (*cinfo->err->output_message)(cinfo);
  if (cinfo->err->num_warnings >= 100 ||
      (cinfo->err->msg_code == JWRN_JPEG_EOF && j->fail_on >= VIPS_FAIL_ON_TRUNCATED))
    (*cinfo->err->error_exit)(cinfo);
}

static void
xi_jpeghp_free(XiJpegHp *j)
{
  if (!j)
    return;
  if (j->created) {
    // Not jpeg_finish_decompress: the tail of the stream is not needed and
    // jpeg_destroy_decompress releases everything.
    jpeg_destroy_decompress(&j->cinfo);
    j->created = FALSE;
    __atomic_sub_fetch(&xi_jpeghp_decoders, 1, __ATOMIC_RELAXED);
  }
  if (j->blob)
    vips_area_unref(VIPS_AREA(j->blob));
  g_free(j->row);
  g_free(j);
}

// xi_jpeghp_fail reports the libjpeg failure (or the shim's own text).
static void
xi_jpeghp_fail(XiJpegHp *j, const char *what)
{
  if (j->message[0])
    vips_error(j->nickname, "%s: %s", what, j->message);
  else
    vips_error(j->nickname, "%s", what);
}

// The standard XMP APP1 identifier, NUL included.
static const char XI_XMP_NS[] = "http://ns.adobe.com/xap/1.0/";

static void
xi_jpeghp_attach(VipsImage *out, const char *field, const void *data, size_t len)
{
  // Only the first one, as jpegload.
  if (!vips_image_get_typeof(out, field))
    vips_image_set_blob_copy(out, field, data, len);
}

// xi_jpeghp_open creates the decompressor over blob and reads the header.
// Returns NULL (vips_error set) when the stream is not one the loader reads.
static XiJpegHp *
xi_jpeghp_open(VipsBlob *blob, VipsFailOn fail_on, const char *nickname)
{
  VipsArea *area = VIPS_AREA(blob);
  XiJpegHp *j = g_new0(XiJpegHp, 1);
  j_decompress_ptr cinfo = &j->cinfo;
  int marker, precision, kind;

  j->nickname = nickname;
  j->fail_on = fail_on;
  vips_area_copy(area);
  j->blob = blob;
  if (!xi_jpeg_frame(area->data, area->length, &marker, &precision) ||
      !(kind = xi_jpeghp_kind(marker, precision))) {
    vips_error(nickname, "not a high-precision JPEG");
    xi_jpeghp_free(j);
    return NULL;
  }
  j->lossless = kind == 1;

  cinfo->err = jpeg_std_error(&j->err.pub);
  j->err.pub.error_exit = xi_jpeghp_error_exit;
  j->err.pub.emit_message = xi_jpeghp_emit_message;
  j->err.pub.output_message = xi_jpeghp_output_message;
  cinfo->client_data = j;
  if (setjmp(j->err.jmp)) {
    xi_jpeghp_fail(j, "bad JPEG header");
    xi_jpeghp_free(j);
    return NULL;
  }
  jpeg_create_decompress(cinfo);
  j->created = TRUE;
  __atomic_add_fetch(&xi_jpeghp_decoders, 1, __ATOMIC_RELAXED);
  jpeg_mem_src(cinfo, area->data, area->length);
  jpeg_save_markers(cinfo, JPEG_APP0 + 1, 0xffff);
  jpeg_save_markers(cinfo, JPEG_APP0 + 2, 0xffff);
  jpeg_save_markers(cinfo, JPEG_APP0 + 13, 0xffff);
  jpeg_read_header(cinfo, TRUE);

  // The frame libjpeg read must be the one the sniffer saw.
  if (cinfo->data_precision != precision || cinfo->image_width == 0 || cinfo->image_height == 0 ||
      cinfo->image_width > VIPS_MAX_COORD || cinfo->image_height > VIPS_MAX_COORD ||
      (size_t) cinfo->image_width * cinfo->image_height > XI_JPEGHP_MAX_PIXELS) {
    vips_error(nickname, "unsupported JPEG frame (%ux%u, %d bits)", cinfo->image_width,
        cinfo->image_height, cinfo->data_precision);
    xi_jpeghp_free(j);
    return NULL;
  }
  j->precision = precision;

  // Output colour: libjpeg-turbo converts DCT YCbCr/YCCK itself; in
  // lossless mode it converts nothing, so YCbCr and YCCK come out as coded
  // and xi_jpeghp_row converts them.
  switch (cinfo->jpeg_color_space) {
  case JCS_GRAYSCALE:
    cinfo->out_color_space = JCS_GRAYSCALE;
    j->bands = 1;
    break;
  case JCS_RGB:
    cinfo->out_color_space = JCS_RGB;
    j->bands = 3;
    break;
  case JCS_YCbCr:
    cinfo->out_color_space = j->lossless ? JCS_YCbCr : JCS_RGB;
    j->ycc = j->lossless;
    j->bands = 3;
    break;
  case JCS_CMYK:
    cinfo->out_color_space = JCS_CMYK;
    j->bands = 4;
    j->invert = TRUE;
    break;
  case JCS_YCCK:
    cinfo->out_color_space = j->lossless ? JCS_YCCK : JCS_CMYK;
    j->ycc = j->lossless;
    j->bands = 4;
    j->invert = TRUE;
    break;
  default:
    vips_error(nickname, "unsupported JPEG colour space (%d components)", cinfo->num_components);
    xi_jpeghp_free(j);
    return NULL;
  }
  jpeg_calc_output_dimensions(cinfo);
  if (cinfo->output_components != j->bands || cinfo->output_width != cinfo->image_width ||
      cinfo->output_height != cinfo->image_height) {
    vips_error(nickname, "unexpected JPEG output shape");
    xi_jpeghp_free(j);
    return NULL;
  }
  j->width = (int) cinfo->output_width;
  j->height = (int) cinfo->output_height;
  j->format = precision > 8 ? VIPS_FORMAT_USHORT : VIPS_FORMAT_UCHAR;
  switch (j->bands) {
  case 1:
    j->interpretation = precision > 8 ? VIPS_INTERPRETATION_GREY16 : VIPS_INTERPRETATION_B_W;
    break;
  case 3:
    j->interpretation = precision > 8 ? VIPS_INTERPRETATION_RGB16 : VIPS_INTERPRETATION_sRGB;
    break;
  default:
    j->interpretation = VIPS_INTERPRETATION_CMYK;
    break;
  }
  return j;
}

// xi_jpeghp_set_fields describes the image and attaches the metadata, as
// jpegload's read_jpeg_header does.
static void
xi_jpeghp_set_fields(XiJpegHp *j, VipsImage *out)
{
  j_decompress_ptr cinfo = &j->cinfo;
  double xres = 72.0 / 25.4, yres = 72.0 / 25.4;
  JOCTET *icc = NULL;
  unsigned int icc_len = 0;
  long warnings;

  // JFIF resolution (EXIF, attached below, may override it).
  if (cinfo->saw_JFIF_marker && cinfo->X_density != 1U && cinfo->Y_density != 1U) {
    switch (cinfo->density_unit) {
    case 0:
      if (cinfo->Y_density > 0)
        yres = xres * cinfo->X_density / cinfo->Y_density;
      break;
    case 1:
      xres = cinfo->X_density / 25.4;
      yres = cinfo->Y_density / 25.4;
      vips_image_set_string(out, VIPS_META_RESOLUTION_UNIT, "in");
      break;
    case 2:
      xres = cinfo->X_density / 10.0;
      yres = cinfo->Y_density / 10.0;
      vips_image_set_string(out, VIPS_META_RESOLUTION_UNIT, "cm");
      break;
    }
  }
  vips_image_init_fields(out, j->width, j->height, j->bands, j->format, VIPS_CODING_NONE,
      j->interpretation, xres, yres);
  vips_image_set_int(out, "bits-per-sample", j->precision);
  vips_image_set_string(out, "jpeg-chroma-subsample",
      cinfo->max_h_samp_factor > 1 || cinfo->max_v_samp_factor > 1
          ? (j->bands > 3 ? "4:2:0:4" : "4:2:0")
          : (j->bands > 3 ? "4:4:4:4" : "4:4:4"));
  if (jpeg_has_multiple_scans(cinfo)) {
    vips_image_set_int(out, "jpeg-multiscan", 1);
    vips_image_set_int(out, "interlaced", 1);
  }

  for (jpeg_saved_marker_ptr p = cinfo->marker_list; p; p = p->next) {
    if (p->marker == JPEG_APP0 + 1 && p->data_length > 4 && memcmp(p->data, "Exif", 4) == 0)
      // Setting the EXIF blob makes libvips parse it (orientation,
      // resolution, exif-ifd* fields), as for jpegload.
      xi_jpeghp_attach(out, VIPS_META_EXIF_NAME, p->data, p->data_length);
    else if (p->marker == JPEG_APP0 + 1 && p->data_length > sizeof(XI_XMP_NS) &&
        memcmp(p->data, XI_XMP_NS, sizeof(XI_XMP_NS)) == 0)
      // The XMP packet follows the namespace and its NUL (XMP part 3).
      xi_jpeghp_attach(out, VIPS_META_XMP_NAME, p->data + sizeof(XI_XMP_NS),
          p->data_length - sizeof(XI_XMP_NS));
    else if (p->marker == JPEG_APP0 + 13 && p->data_length > 5 &&
        memcmp(p->data, "Photo", 5) == 0)
      xi_jpeghp_attach(out, VIPS_META_IPTC_NAME, p->data, p->data_length);
  }
  // libjpeg-turbo reassembles the APP2 chunks in sequence order and checks
  // them; a broken sequence gives no profile and a warning, which is not a
  // decode warning (jpegload ignores such profiles too).
  warnings = cinfo->err->num_warnings;
  if (jpeg_read_icc_profile(cinfo, &icc, &icc_len) && icc && icc_len > 0)
    vips_image_set_blob_copy(out, VIPS_META_ICC_NAME, icc, icc_len);
  free(icc);
  cinfo->err->num_warnings = warnings;
}

// xi_jpeghp_convert turns one pixel's samples (native precision, max =
// 2^P − 1) into output samples: lossless YCbCr to RGB (full-range JFIF
// equations, as libjpeg's own converter), YCCK to inverted CMY + K, CMYK
// inversion.
static inline void
xi_jpeghp_convert(const XiJpegHp *j, long *v, long max)
{
  if (j->ycc) {
    double y = (double) v[0], cb = v[1] - (double) (max + 1) / 2, cr = v[2] - (double) (max + 1) / 2;
    double rgb[3] = { y + 1.402 * cr, y - 0.344136286 * cb - 0.714136286 * cr, y + 1.772 * cb };

    for (int c = 0; c < 3; c++) {
      long s = (long) (rgb[c] + 0.5);

      s = s < 0 ? 0 : s > max ? max : s;
      // YCCK stores inverted CMY as YCC: libjpeg gives max − RGB.
      v[c] = j->bands == 4 ? max - s : s;
    }
  }
  if (j->invert)
    for (int c = 0; c < j->bands; c++)
      v[c] = max - v[c];
}

// xi_jpeghp_row decodes the next scanline into dst (the output row).
static void
xi_jpeghp_row(XiJpegHp *j, VipsPel *dst)
{
  j_decompress_ptr cinfo = &j->cinfo;
  const int n = j->width, bands = j->bands, p = j->precision;
  const long max = (1L << p) - 1;
  long v[4];

  if (p <= 8) {
    JSAMPROW row = j->row;

    jpeg_read_scanlines(cinfo, &row, 1);
    for (int x = 0; x < n; x++) {
      for (int c = 0; c < bands; c++)
        v[c] = MIN((long) row[x * bands + c], max);
      xi_jpeghp_convert(j, v, max);
      for (int c = 0; c < bands; c++)
        dst[x * bands + c] = (VipsPel) (p == 8 ? v[c] : (v[c] * 255 + max / 2) / max);
    }
  }
  else {
    guint16 *out = (guint16 *) dst;

    if (p <= 12) {
      J12SAMPROW row = j->row;

      jpeg12_read_scanlines(cinfo, &row, 1);
      for (int x = 0; x < n; x++) {
        for (int c = 0; c < bands; c++)
          v[c] = VIPS_CLIP(0, (long) row[x * bands + c], max);
        xi_jpeghp_convert(j, v, max);
        for (int c = 0; c < bands; c++)
          out[x * bands + c] = (guint16) ((v[c] * 65535 + max / 2) / max);
      }
    }
    else {
      J16SAMPROW row = j->row;

      jpeg16_read_scanlines(cinfo, &row, 1);
      for (int x = 0; x < n; x++) {
        for (int c = 0; c < bands; c++)
          v[c] = MIN((long) row[x * bands + c], max);
        xi_jpeghp_convert(j, v, max);
        for (int c = 0; c < bands; c++)
          out[x * bands + c] = (guint16) (p == 16 ? v[c] : (v[c] * 65535 + max / 2) / max);
      }
    }
  }
}

static int
xi_jpeghp_generate(VipsRegion *out_region, void *seq, void *a, void *b, gboolean *stop)
{
  VipsRect *r = &out_region->valid;
  XiJpegHp *j = (XiJpegHp *) a;

  (void) seq;
  (void) b;
  (void) stop;
  // Inside vips_sequential: whole-width strips, in order.
  if (r->left != 0 || r->width != j->width || r->top != (int) j->cinfo.output_scanline) {
    vips_error(j->nickname, "out of order read at line %u", j->cinfo.output_scanline);
    return -1;
  }
  if (setjmp(j->err.jmp)) {
    xi_jpeghp_fail(j, "JPEG decode failed");
    return -1;
  }
  for (int y = 0; y < r->height; y++)
    xi_jpeghp_row(j, VIPS_REGION_ADDR(out_region, 0, r->top + y));
  if (j->err.pub.num_warnings > 0 && j->fail_on >= VIPS_FAIL_ON_WARNING) {
    xi_jpeghp_fail(j, "JPEG decode warning");
    return -1;
  }
  return 0;
}

static void
xi_jpeghp_close_cb(VipsImage *image, XiJpegHp *j)
{
  (void) image;
  xi_jpeghp_free(j);
}

typedef struct _XiForeignLoadJpegHpBuffer {
  VipsForeignLoad parent_object;

  VipsBlob *blob;
} XiForeignLoadJpegHpBuffer;

typedef VipsForeignLoadClass XiForeignLoadJpegHpBufferClass;

G_DEFINE_TYPE(XiForeignLoadJpegHpBuffer, xi_foreign_load_jpeghp_buffer, VIPS_TYPE_FOREIGN_LOAD);

static VipsForeignFlags
xi_foreign_load_jpeghp_buffer_get_flags(VipsForeignLoad *load)
{
  (void) load;
  return VIPS_FOREIGN_SEQUENTIAL;
}

static int
xi_foreign_load_jpeghp_buffer_header(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadJpegHpBuffer *jpeg = (XiForeignLoadJpegHpBuffer *) load;
  XiJpegHp *j;

  if (!(j = xi_jpeghp_open(jpeg->blob, load->fail_on, class->nickname)))
    return -1;
  if (setjmp(j->err.jmp)) {
    xi_jpeghp_fail(j, "bad JPEG header");
    xi_jpeghp_free(j);
    return -1;
  }
  xi_jpeghp_set_fields(j, load->out);
  // The header is all a probe needs: load opens the stream again.
  xi_jpeghp_free(j);
  return 0;
}

static int
xi_foreign_load_jpeghp_buffer_load(VipsForeignLoad *load)
{
  VipsObjectClass *class = VIPS_OBJECT_GET_CLASS(load);
  XiForeignLoadJpegHpBuffer *jpeg = (XiForeignLoadJpegHpBuffer *) load;
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(load), 2);
  XiJpegHp *j;

  if (!(j = xi_jpeghp_open(jpeg->blob, load->fail_on, class->nickname)))
    return -1;
  t[0] = vips_image_new();
  // From here t[0] owns the decoder: it goes when the image closes.
  g_signal_connect(t[0], "close", G_CALLBACK(xi_jpeghp_close_cb), j);
  if (setjmp(j->err.jmp)) {
    xi_jpeghp_fail(j, "JPEG decode failed");
    return -1;
  }
  xi_jpeghp_set_fields(j, t[0]);
  if (vips_image_pipelinev(t[0], VIPS_DEMAND_STYLE_FATSTRIP, NULL))
    return -1;
  jpeg_start_decompress(&j->cinfo);
  j->row = g_malloc((size_t) j->width * j->bands * (j->precision > 8 ? 2 : 1));
  if (vips_image_generate(t[0], NULL, xi_jpeghp_generate, NULL, j, NULL) ||
      vips_sequential(t[0], &t[1], "tile_height", 8, NULL) || vips_image_write(t[1], load->real))
    return -1;
  return 0;
}

static gboolean
xi_foreign_load_jpeghp_buffer_is_a_buffer(const void *buf, size_t len)
{
  return xi_is_jpeghp(buf, len);
}

static const char *xi_jpeghp_suffs[] = { ".jpg", ".jpeg", ".jpe", ".jfif", NULL };

static void
xi_foreign_load_jpeghp_buffer_class_init(XiForeignLoadJpegHpBufferClass *class)
{
  GObjectClass *gobject_class = G_OBJECT_CLASS(class);
  VipsObjectClass *object_class = (VipsObjectClass *) class;
  VipsOperationClass *operation_class = VIPS_OPERATION_CLASS(class);
  VipsForeignClass *foreign_class = (VipsForeignClass *) class;
  VipsForeignLoadClass *load_class = (VipsForeignLoadClass *) class;

  gobject_class->set_property = vips_object_set_property;
  gobject_class->get_property = vips_object_get_property;

  object_class->nickname = "jpeghpload_buffer";
  object_class->description = "load 12-bit and lossless JPEG from buffer (libjpeg-turbo)";

  // Unblocked by name only (vips.DefaultLoaders), like the other shim
  // loaders.
  operation_class->flags |= VIPS_OPERATION_UNTRUSTED;

  foreign_class->suffs = xi_jpeghp_suffs;
  // Ahead of jpegload, which claims every JPEG stream.
  foreign_class->priority = 100;

  load_class->get_flags = xi_foreign_load_jpeghp_buffer_get_flags;
  load_class->header = xi_foreign_load_jpeghp_buffer_header;
  load_class->load = xi_foreign_load_jpeghp_buffer_load;
  load_class->is_a_buffer = xi_foreign_load_jpeghp_buffer_is_a_buffer;

  VIPS_ARG_BOXED(class, "buffer", 1, "Buffer", "Buffer to load from",
      VIPS_ARGUMENT_REQUIRED_INPUT, G_STRUCT_OFFSET(XiForeignLoadJpegHpBuffer, blob),
      VIPS_TYPE_BLOB);
}

static void
xi_foreign_load_jpeghp_buffer_init(XiForeignLoadJpegHpBuffer *jpeg)
{
  (void) jpeg;
}

void
xi_jpeghp_register(void)
{
  // Referencing the class registers it with libvips' loader search.
  g_type_class_unref(g_type_class_ref(xi_foreign_load_jpeghp_buffer_get_type()));
}
