// Hand-written C shim for internal/vips: probe, load, thumbnail and
// synthetic images. See shim.h for the licence notice.

#include <stdio.h>
#include <string.h>

#include "shim.h"

#define XI_SRC_BLOB_KEY "xi-src-blob"

static gboolean
xi_loader_blocked(const char *loader)
{
  GType type;
  gpointer klass;
  gboolean blocked;

  if (!(type = g_type_from_name(loader)))
    return TRUE;
  klass = g_type_class_ref(type);
  blocked = (VIPS_OPERATION_CLASS(klass)->flags & VIPS_OPERATION_BLOCKED) != 0;
  g_type_class_unref(klass);
  return blocked;
}

// xi_is_magick reports whether loader is one of libvips' ImageMagick
// loaders (magickload*, Magick6 or Magick7 builds).
static gboolean
xi_is_magick(const char *loader)
{
  return g_str_has_prefix(loader, "VipsForeignLoadMagick");
}

// xi_check_loader refuses a blocked loader, and ImageMagick for anything but
// PSD/PSB: ImageMagick is in the image for Photoshop files only
// (FORMATS.md), and its policy.xml denies every other coder as well.
static int
xi_check_loader(const char *loader, VipsBlob *blob, char **err)
{
  VipsArea *area = VIPS_AREA(blob);

  // The libvips sniffer already skips blocked loaders; this is a second
  // line of defence should that ever change.
  if (xi_loader_blocked(loader)) {
    vips_error("xi_load", "loader %s is blocked", loader);
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }
  if (xi_is_magick(loader) &&
      (area->length < 4 || memcmp(area->data, "8BPS", 4) != 0)) {
    vips_error("xi_load", "%s is restricted to PSD/PSB", loader);
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }
  return XI_OK;
}

// xi_load_error classifies a failed loader run: a PDF that needs a password
// is XI_ERR_ENCRYPTED, anything else XI_ERR_LOAD. PDFium reports the
// password case only through FPDF_GetLastError, which pdfload turns into
// this message; the text is the only signal libvips gives.
static int
xi_load_error(const char *loader, char **err)
{
  const char *msg = vips_error_buffer();
  int rc = XI_ERR_LOAD;

  if (g_str_has_prefix(loader, "VipsForeignLoadPdf") && msg &&
      strstr(msg, "password required or incorrect password"))
    rc = XI_ERR_ENCRYPTED;
  *err = xi_error_copy();
  return rc;
}

// xi_find_loader sniffs the loader class name for blob. It returns
// XI_ERR_UNSUPPORTED with *err set when nothing enabled matches.
static int
xi_find_loader(VipsBlob *blob, const char **loader, char **err)
{
  VipsArea *area = VIPS_AREA(blob);

  if (!(*loader = vips_foreign_find_load_buffer(area->data, area->length))) {
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }
  return xi_check_loader(*loader, blob, err);
}

static gboolean
xi_has_prop(gpointer object, const char *name)
{
  return g_object_class_find_property(G_OBJECT_GET_CLASS(object), name) != NULL;
}

static gboolean
xi_class_has_prop(const char *type_name, const char *name)
{
  GType type;
  gpointer klass;
  gboolean has;

  if (!(type = g_type_from_name(type_name)))
    return FALSE;
  klass = g_type_class_ref(type);
  has = g_object_class_find_property(G_OBJECT_CLASS(klass), name) != NULL;
  g_type_class_unref(klass);
  return has;
}

// xi_keep_blob makes img hold its own reference to the source blob: loading
// is lazy and pixels are read on save, possibly after the Go side released
// its Buffer.
static void
xi_keep_blob(VipsImage *img, VipsBlob *blob)
{
  vips_area_copy(VIPS_AREA(blob));
  g_object_set_data_full(G_OBJECT(img), XI_SRC_BLOB_KEY, VIPS_AREA(blob),
      (GDestroyNotify) vips_area_unref);
}

// xi_build runs op (consuming the caller's reference) and returns its "out"
// image with a new reference.
static int
xi_build(VipsOperation *op, VipsImage **out)
{
  if (vips_cache_operation_buildp(&op)) {
    vips_object_unref_outputs(VIPS_OBJECT(op));
    g_object_unref(op);
    return -1;
  }
  g_object_get(op, "out", out, NULL);
  vips_object_unref_outputs(VIPS_OBJECT(op));
  g_object_unref(op);
  return 0;
}

// xi_run_loader builds loader over blob with the options it supports.
static int
xi_run_loader(const char *loader, VipsBlob *blob, const XiLoadOptions *o, VipsImage **out)
{
  VipsOperation *op;

  if (!(op = vips_operation_new(loader)))
    return -1;
  g_object_set(op, "buffer", blob, NULL);
  g_object_set(op, "access", o->access, "fail_on", o->fail_on, NULL);
  // Always limited: never lift the libraries' own safety limits.
  if (xi_has_prop(op, "unlimited"))
    g_object_set(op, "unlimited", FALSE, NULL);
  if (o->shrink > 1 && xi_has_prop(op, "shrink"))
    g_object_set(op, "shrink", o->shrink, NULL);
  if (o->scale > 0 && xi_has_prop(op, "scale"))
    g_object_set(op, "scale", o->scale, NULL);
  if (o->dpi > 0 && xi_has_prop(op, "dpi"))
    g_object_set(op, "dpi", o->dpi, NULL);
  if (o->page > 0 && xi_has_prop(op, "page"))
    g_object_set(op, "page", o->page, NULL);
  if (o->n != 0 && xi_has_prop(op, "n"))
    g_object_set(op, "n", o->n, NULL);
  if (o->heif_thumbnail && xi_has_prop(op, "thumbnail"))
    g_object_set(op, "thumbnail", TRUE, NULL);
  if (o->n_background > 0 && xi_has_prop(op, "background")) {
    VipsArrayDouble *bg = vips_array_double_new(o->background, o->n_background);

    g_object_set(op, "background", bg, NULL);
    vips_area_unref(VIPS_AREA(bg));
  }
  return xi_build(op, out);
}

static int
xi_bits(VipsImage *img)
{
  int bits;

  if (vips_image_get_typeof(img, "bits-per-sample") &&
      !vips_image_get_int(img, "bits-per-sample", &bits))
    return bits;
  return (int) vips_format_sizeof(img->BandFmt) * 8;
}

static void
xi_copy_string(VipsImage *img, const char *name, char *dst, size_t size)
{
  const char *s;

  dst[0] = '\0';
  if (vips_image_get_typeof(img, name) && !vips_image_get_string(img, name, &s))
    g_strlcpy(dst, s, size);
}

int
xi_probe(VipsBlob *blob, double dpi, XiHeader *h, char **err)
{
  const char *loader;
  VipsImage *img = NULL;
  XiLoadOptions o = { 0 };
  int rc, *delay, n;

  memset(h, 0, sizeof(*h));
  if ((rc = xi_find_loader(blob, &loader, err)))
    return rc;

  // Header only: the pixels are never requested, so nothing is decoded.
  o.access = VIPS_ACCESS_SEQUENTIAL;
  o.fail_on = VIPS_FAIL_ON_NONE;
  o.dpi = dpi;
  if (xi_run_loader(loader, blob, &o, &img))
    return xi_load_error(loader, err);

  h->width = img->Xsize;
  h->height = vips_image_get_page_height(img);
  h->bands = img->Bands;
  h->band_format = img->BandFmt;
  h->interpretation = vips_image_guess_interpretation(img);
  // The raw n-pages: vips_image_get_n_pages reports 1 for 10000 pages or
  // more, which would slip past the page limits.
  h->n_pages = 1;
  if (vips_image_get_typeof(img, VIPS_META_N_PAGES))
    vips_image_get_int(img, VIPS_META_N_PAGES, &h->n_pages);
  h->orientation = vips_image_get_orientation(img);
  h->has_alpha = vips_image_hasalpha(img);
  h->has_icc = vips_image_get_typeof(img, VIPS_META_ICC_NAME) != 0;
  h->bits = xi_bits(img);
  h->loop = -1;
  if (vips_image_get_typeof(img, "loop"))
    vips_image_get_int(img, "loop", &h->loop);
  if (vips_image_get_typeof(img, "delay") &&
      !vips_image_get_array_int(img, "delay", &delay, &n) && n > 0) {
    h->delay = g_memdup2(delay, (gsize) n * sizeof(int));
    h->n_delay = n;
  }
  xi_copy_string(img, VIPS_META_LOADER, h->loader, sizeof(h->loader));
  xi_copy_string(img, "heif-compression", h->subformat, sizeof(h->subformat));

  g_object_unref(img);
  return XI_OK;
}

static int xi_load_run(const char *loader, VipsBlob *blob, const XiLoadOptions *o,
    VipsImage **out, char **err);

int
xi_load(VipsBlob *blob, const XiLoadOptions *o, VipsImage **out, char **err)
{
  const char *loader;
  int rc;

  *out = NULL;
  if ((rc = xi_find_loader(blob, &loader, err)))
    return rc;
  return xi_load_run(loader, blob, o, out, err);
}

int
xi_load_with(const char *loader, VipsBlob *blob, const XiLoadOptions *o, VipsImage **out,
    char **err)
{
  GType type;

  *out = NULL;
  if (!(type = g_type_from_name(loader)) || !g_type_is_a(type, VIPS_TYPE_FOREIGN_LOAD)) {
    vips_error("xi_load", "no loader %s", loader);
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }
  if (xi_loader_blocked(loader)) {
    vips_error("xi_load", "loader %s is blocked", loader);
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }
  return xi_load_run(loader, blob, o, out, err);
}

static int
xi_load_run(const char *loader, VipsBlob *blob, const XiLoadOptions *o, VipsImage **out,
    char **err)
{
  VipsImage *img = NULL, *rot = NULL;
  int frames;

  if (xi_run_loader(loader, blob, o, &img))
    return xi_load_error(loader, err);

  // Limits are checked on the header, before any pixel is decoded.
  frames = img->Ysize / vips_image_get_page_height(img);
  if (o->max_frames > 0 && frames > o->max_frames) {
    vips_error("xi_load", "%d frames exceed the limit of %d", frames, o->max_frames);
    *err = xi_error_copy();
    g_object_unref(img);
    return XI_ERR_LIMIT;
  }
  if (o->max_pixels > 0 && (long long) img->Xsize * img->Ysize > o->max_pixels) {
    vips_error("xi_load", "%dx%d pixels exceed the limit of %lld", img->Xsize, img->Ysize,
        o->max_pixels);
    *err = xi_error_copy();
    g_object_unref(img);
    return XI_ERR_LIMIT;
  }

  xi_keep_blob(img, blob);

  if (o->autorotate) {
    if (xi_autorot(img, &rot, err)) {
      g_object_unref(img);
      return XI_ERR_OP;
    }
    g_object_unref(img);
    img = rot;
  }

  *out = img;
  return XI_OK;
}

int
xi_thumbnail(VipsBlob *blob, const XiThumbnailOptions *o, const char *import_profile,
    const char *export_profile, VipsImage **out, char **err)
{
  const char *loader;
  VipsOperation *op;
  char options[64];
  int rc, len = 0;

  *out = NULL;
  if ((rc = xi_find_loader(blob, &loader, err)))
    return rc;

  // Loader options travel in option_string; only pass what this loader has,
  // since unknown keys are an error.
  // thumbnail_buffer does not hand its own fail_on to the loader (8.18
  // vips_thumbnail_buffer_open), so it travels here too.
  options[0] = '\0';
  if (xi_class_has_prop(loader, "fail_on"))
    len += snprintf(options + len, sizeof(options) - len, "fail_on=%s",
        vips_enum_nick(VIPS_TYPE_FAIL_ON, o->fail_on));
  if (o->page > 0 && xi_class_has_prop(loader, "page"))
    len += snprintf(options + len, sizeof(options) - len, "%spage=%d", len ? "," : "",
        o->page);
  if (o->n != 0 && xi_class_has_prop(loader, "n"))
    len += snprintf(options + len, sizeof(options) - len, "%sn=%d", len ? "," : "", o->n);

  if (!(op = vips_operation_new("thumbnail_buffer"))) {
    *err = xi_error_copy();
    return XI_ERR_INTERNAL;
  }
  // thumbnail_buffer reads the blob's data without copying; the output
  // keeps its own reference below.
  g_object_set(op,
      "buffer", blob,
      "width", o->width,
      "height", o->height,
      "size", o->size,
      "no_rotate", o->no_rotate,
      "crop", o->crop,
      "linear", o->linear,
      "intent", o->intent,
      "fail_on", o->fail_on,
      NULL);
  if (len > 0)
    g_object_set(op, "option_string", options, NULL);
  if (import_profile)
    g_object_set(op, "input_profile", import_profile, NULL);
  if (export_profile)
    g_object_set(op, "output_profile", export_profile, NULL);
  if (xi_build(op, out))
    return xi_load_error(loader, err);

  xi_keep_blob(*out, blob);
  return XI_OK;
}

int
xi_black(int width, int height, int bands, VipsImage **out, char **err)
{
  if (vips_black(out, width, height, "bands", bands, NULL)) {
    *err = xi_error_copy();
    return XI_ERR_OP;
  }
  return XI_OK;
}

int
xi_new_from_memory(const void *data, size_t size, int width, int height, int bands,
    int band_format, VipsImage **out, char **err)
{
  // Copies: the Go memory is only read during this call.
  if (!(*out = vips_image_new_from_memory_copy(data, size, width, height, bands,
            band_format))) {
    *err = xi_error_copy();
    return XI_ERR_OP;
  }
  // A fresh image: give it the interpretation its bands imply, so that
  // alpha is recognised and colour operations have a route.
  if (band_format == VIPS_FORMAT_USHORT)
    (*out)->Type = bands >= 3 ? VIPS_INTERPRETATION_RGB16 : VIPS_INTERPRETATION_GREY16;
  else
    (*out)->Type = bands >= 3 ? VIPS_INTERPRETATION_sRGB : VIPS_INTERPRETATION_B_W;
  return XI_OK;
}
