// Hand-written C shim for internal/vips: camera RAW through LibRaw
// (libraw_r, the reentrant build). See shim.h for the licence notice.
//
// The demosaic parameters are those of libvips' dcrawload
// (libvips/foreign/dcrawload.c, LGPL-2.1+, used as a reference only):
// 8 bits per sample, camera white balance, sRGB output, no automatic
// rotation (the orientation is reported and applied by the pipeline).
// libvips 8.18.6's dcrawload has no half_size option, which is why the shim
// drives LibRaw itself instead of running that loader.

#include <string.h>
#include <time.h>

#include <libraw.h>

#include "shim.h"

// Live LibRaw handles and demosaiced buffers, for leak tests (LibRaw's
// memory is not tracked by libvips).
static int xi_raw_handles, xi_raw_images;

int
xi_raw_live(void)
{
  return __atomic_load_n(&xi_raw_handles, __ATOMIC_RELAXED) +
      __atomic_load_n(&xi_raw_images, __ATOMIC_RELAXED);
}

static void
xi_raw_close(libraw_data_t *lr)
{
  libraw_close(lr);
  __atomic_fetch_sub(&xi_raw_handles, 1, __ATOMIC_RELAXED);
}

static void
xi_raw_clear(libraw_processed_image_t *p)
{
  libraw_dcraw_clear_mem(p);
  __atomic_fetch_sub(&xi_raw_images, 1, __ATOMIC_RELAXED);
}

// xi_raw_error maps a LibRaw result code to an XI_ERR_* code and records
// libraw_strerror's text in the vips error buffer.
static int
xi_raw_error(int rc, const char *what, char **err)
{
  *err = xi_error_new("rawload", "%s: %s", what, libraw_strerror(rc));
  switch (rc) {
  case LIBRAW_FILE_UNSUPPORTED:
    return XI_ERR_UNSUPPORTED;
  case LIBRAW_TOO_BIG:
  case LIBRAW_UNSUFFICIENT_MEMORY:
    return XI_ERR_LIMIT;
  case LIBRAW_CANCELLED_BY_CALLBACK:
    return XI_ERR_INTERNAL;
  default:
    return XI_ERR_LOAD;
  }
}

static libraw_data_t *
xi_raw_open(VipsBlob *blob, int *rc)
{
  VipsArea *area = VIPS_AREA(blob);
  libraw_data_t *lr;

  // No data-error callback: it prints to stderr; failures are reported
  // through the result codes.
  if (!(lr = libraw_init(LIBRAW_OPTIONS_NO_DATAERR_CALLBACK))) {
    *rc = LIBRAW_UNSUFFICIENT_MEMORY;
    return NULL;
  }
  __atomic_fetch_add(&xi_raw_handles, 1, __ATOMIC_RELAXED);
  // Like dcrawload: the sizes libraw_adjust_sizes_info_only predicts and
  // the demosaiced image stay in the stored orientation.
  lr->params.user_flip = 0;
  if ((*rc = libraw_open_buffer(lr, area->data, area->length)) != LIBRAW_SUCCESS) {
    xi_raw_close(lr);
    return NULL;
  }
  return lr;
}

// xi_raw_undecodable says why LibRaw cannot demosaic the file lr opened
// (XI_RAW_*), before anything is unpacked.
//
// Nikon High Efficiency NEFs (intoPIX TicoRAW; makernote NEFCompression 13
// "HE" and 14 "HE*", as ExifTool names them) have no open decoder. LibRaw
// 0.22 flags them only for the Z 9, Z 8, Z f and Z6III (a JPEG XS marker
// check) and decodes other bodies, such as the Z50II, as lossless NEF:
// wrong pixels and no error. The makernote decides here for every body. A
// DNG converted from such a NEF carries the original makernote but
// ordinary DNG data, so DNGs are left to the decoder check.
//
// LibRaw also sets placeholder decoders it cannot run without optional
// SDKs (GoPro VC-5, JPEG XL DNG without the DNG SDK): the decoder's
// LIBRAW_DECODER_UNSUPPORTED_FORMAT flag.
static int
xi_raw_undecodable(libraw_data_t *lr)
{
  libraw_decoder_info_t d;
  unsigned nefc = lr->makernotes.nikon.NEFCompression;

  if (!lr->idata.dng_version && g_ascii_strncasecmp(lr->idata.make, "Nikon", 5) == 0 &&
      (nefc == 13 || nefc == 14))
    return XI_RAW_NIKON_HE;
  if (libraw_get_decoder_info(lr, &d) == LIBRAW_SUCCESS &&
      (d.decoder_flags & LIBRAW_DECODER_UNSUPPORTED_FORMAT))
    return XI_RAW_NO_DECODER;
  return XI_RAW_DECODABLE;
}

// xi_raw_thumbs copies LibRaw's thumbnail list into h.
static void
xi_raw_thumbs(libraw_data_t *lr, XiRawHeader *h)
{
  for (int i = 0; i < lr->thumbs_list.thumbcount && i < LIBRAW_THUMBNAIL_MAXCOUNT &&
       h->n_thumbs < XI_RAW_MAX_THUMBS; i++) {
    libraw_thumbnail_item_t *t = &lr->thumbs_list.thumblist[i];
    XiRawThumb *o = &h->thumbs[h->n_thumbs];

    if (t->toffset <= 0 || t->tlength == 0)
      continue;
    o->jpeg = t->tformat == LIBRAW_INTERNAL_THUMBNAIL_JPEG;
    o->width = t->twidth;
    o->height = t->theight;
    o->offset = t->toffset;
    o->length = t->tlength;
    h->n_thumbs++;
  }
}

int
xi_raw_probe(VipsBlob *blob, XiRawHeader *h, char **err)
{
  VipsArea *area = VIPS_AREA(blob);
  libraw_data_t *lr;
  int rc;
  struct tm tm;
  time_t ts;

  memset(h, 0, sizeof(*h));
  if (!(lr = libraw_init(LIBRAW_OPTIONS_NO_DATAERR_CALLBACK)))
    return xi_raw_error(LIBRAW_UNSUFFICIENT_MEMORY, "unable to read", err);
  __atomic_fetch_add(&xi_raw_handles, 1, __ATOMIC_RELAXED);
  lr->params.user_flip = 0;
  rc = libraw_open_buffer(lr, area->data, area->length);
  if (rc == LIBRAW_FILE_UNSUPPORTED && lr->thumbs_list.thumbcount > 0 && lr->idata.make[0]) {
    // A camera LibRaw does not know yet (a body newer than this release):
    // identify still parsed the TIFF/EXIF structure, so its maker and
    // embedded previews are known. Nothing else is trusted. (A JPEG with
    // an EXIF thumbnail looks the same: callers decide it is a RAW first,
    // as codec/raw.Sniff does.)
    h->undecodable = XI_RAW_UNKNOWN_CAMERA;
    h->flip = lr->sizes.flip;
    g_strlcpy(h->make, lr->idata.make, sizeof(h->make));
    g_strlcpy(h->model, lr->idata.model, sizeof(h->model));
    xi_raw_thumbs(lr, h);
    xi_raw_close(lr);
    return XI_OK;
  }
  if (rc != LIBRAW_SUCCESS) {
    xi_raw_close(lr);
    return xi_raw_error(rc, "unable to read", err);
  }
  h->undecodable = xi_raw_undecodable(lr);

  // Header only: nothing is unpacked. flip and the metadata are read
  // before libraw_adjust_sizes_info_only, which resets flip.
  h->flip = lr->sizes.flip;
  h->colors = lr->idata.colors;
  h->dng = lr->idata.dng_version != 0;
  h->raw_width = lr->sizes.raw_width;
  h->raw_height = lr->sizes.raw_height;
  h->iso = lr->other.iso_speed;
  h->shutter = lr->other.shutter;
  h->aperture = lr->other.aperture;
  h->focal_len = lr->other.focal_len;
  // LibRaw parses EXIF dates with mktime (local time); invert it the same
  // way to get the camera's wall-clock time back.
  ts = lr->other.timestamp;
  if (ts > 0 && localtime_r(&ts, &tm)) {
    h->year = tm.tm_year + 1900;
    h->month = tm.tm_mon + 1;
    h->day = tm.tm_mday;
    h->hour = tm.tm_hour;
    h->minute = tm.tm_min;
    h->second = tm.tm_sec;
  }
  g_strlcpy(h->make, lr->idata.make, sizeof(h->make));
  g_strlcpy(h->model, lr->idata.model, sizeof(h->model));
  g_strlcpy(h->software, lr->idata.software, sizeof(h->software));
  g_strlcpy(h->lens, lr->lens.Lens, sizeof(h->lens));
  xi_raw_thumbs(lr, h);

  libraw_adjust_sizes_info_only(lr);
  h->width = lr->sizes.iwidth;
  h->height = lr->sizes.iheight;
  xi_raw_close(lr);

  if (h->width <= 0 || h->height <= 0) {
    *err = xi_error_new("rawload", "no image in file");
    return XI_ERR_LOAD;
  }
  return XI_OK;
}

void
xi_raw_cancel(int *flag)
{
  __atomic_store_n(flag, 1, __ATOMIC_RELAXED);
}

// LibRaw calls this between processing stages; non-zero cancels.
static int
xi_raw_progress(void *data, enum LibRaw_progress stage, int iteration, int expected)
{
  (void) stage;
  (void) iteration;
  (void) expected;
  return __atomic_load_n((int *) data, __ATOMIC_RELAXED);
}

static void
xi_raw_free_processed(VipsImage *img, gpointer processed)
{
  (void) img;
  xi_raw_clear((libraw_processed_image_t *) processed);
}

int
xi_raw_load(VipsBlob *blob, const XiRawOptions *o, VipsImage **out, char **err)
{
  libraw_data_t *lr;
  libraw_processed_image_t *p;
  VipsImage *img;
  int rc;
  long long px;

  *out = NULL;
  if (!(lr = xi_raw_open(blob, &rc)))
    return xi_raw_error(rc, "unable to read", err);

  // The sensor size bounds the work and memory of the demosaic (LibRaw
  // holds four 16-bit samples per pixel), so check it before unpacking.
  px = (long long) lr->sizes.width * lr->sizes.height;
  if (o->max_pixels > 0 && px > o->max_pixels) {
    *err = xi_error_new("rawload", "%dx%d sensor pixels exceed the demosaic limit of %lld",
        lr->sizes.width, lr->sizes.height, o->max_pixels);
    xi_raw_close(lr);
    return XI_ERR_LIMIT;
  }
  // Never demosaic what LibRaw would decode into wrong pixels (Nikon HE on
  // bodies it does not flag) or cannot decode at all; the planner serves
  // those from the embedded preview (FORMATS.md §2).
  if (xi_raw_undecodable(lr) != XI_RAW_DECODABLE) {
    *err = xi_error_new("rawload", "%s %s: no decoder for this RAW compression",
        lr->idata.make, lr->idata.model);
    xi_raw_close(lr);
    return XI_ERR_UNSUPPORTED;
  }

  lr->params.output_bps = 8;
  lr->params.use_camera_wb = 1;
  lr->params.output_color = 1; // sRGB
  lr->params.half_size = o->half_size ? 1 : 0;
  if (o->cancel)
    libraw_set_progress_handler(lr, xi_raw_progress, o->cancel);

  if ((rc = libraw_unpack(lr)) != LIBRAW_SUCCESS) {
    xi_raw_close(lr);
    return xi_raw_error(rc, "unable to unpack", err);
  }
  if ((rc = libraw_dcraw_process(lr)) != LIBRAW_SUCCESS) {
    xi_raw_close(lr);
    return xi_raw_error(rc, "unable to demosaic", err);
  }
  p = libraw_dcraw_make_mem_image(lr, &rc);
  xi_raw_close(lr);
  if (!p)
    return xi_raw_error(rc, "unable to build the image", err);
  __atomic_fetch_add(&xi_raw_images, 1, __ATOMIC_RELAXED);
  if (p->type != LIBRAW_IMAGE_BITMAP || p->bits != 8 || (p->colors != 1 && p->colors != 3) ||
      p->width == 0 || p->height == 0 ||
      p->data_size != (unsigned) p->width * p->height * p->colors) {
    xi_raw_clear(p);
    *err = xi_error_new("rawload", "unexpected demosaic output");
    return XI_ERR_LOAD;
  }

  // The image owns LibRaw's buffer and frees it when it closes.
  if (!(img = vips_image_new_from_memory(p->data, p->data_size, p->width, p->height,
            p->colors, VIPS_FORMAT_UCHAR))) {
    xi_raw_clear(p);
    *err = xi_error_copy();
    return XI_ERR_INTERNAL;
  }
  g_signal_connect(img, "close", G_CALLBACK(xi_raw_free_processed), p);
  img->Type = p->colors == 1 ? VIPS_INTERPRETATION_B_W : VIPS_INTERPRETATION_sRGB;
  vips_image_set_string(img, VIPS_META_LOADER, "rawload");

  *out = img;
  return XI_OK;
}
