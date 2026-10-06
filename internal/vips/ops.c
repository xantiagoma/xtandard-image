// Hand-written C shim for internal/vips: processing operations. See shim.h
// for the licence notice; xi_flatten's 16-bit background scaling,
// xi_find_trim's colourspace/alpha preparation and the per-frame handling of
// animated images follow imgproxy's vips/vips.c (Apache-2.0).

#include <math.h>
#include <string.h>

#include "shim.h"

#define XI_FAIL() \
  do { \
    *err = xi_error_copy(); \
    return XI_ERR_OP; \
  } while (0)

// xi_bg returns a background array for vips (NULL when nbg == 0, meaning the
// operation default). Free it with xi_bg_free.
static VipsArrayDouble *
xi_bg(const double *bg, int nbg)
{
  return nbg > 0 ? vips_array_double_new(bg, nbg) : NULL;
}

static void
xi_bg_free(VipsArrayDouble *a)
{
  if (a)
    vips_area_unref(VIPS_AREA(a));
}

static int
xi_frames(VipsImage *in, int *page_height)
{
  int ph = vips_image_get_page_height(in);

  if (page_height)
    *page_height = ph;
  return in->Ysize / ph;
}

// xi_set_page_height returns a copy of in with page-height set, or a plain
// reference to in when it is a single frame.
static int
xi_with_page_height(VipsImage *in, int page_height, int frames, VipsImage **out)
{
  if (frames <= 1) {
    *out = in;
    g_object_ref(in);
    return 0;
  }
  if (vips_copy(in, out, NULL))
    return -1;
  vips_image_set_int(*out, VIPS_META_PAGE_HEIGHT, page_height);
  return 0;
}

typedef int (*XiFrameFn)(VipsImage *in, VipsImage **out, const void *arg);

// xi_per_frame applies fn to every frame of a multi-page image (a vertical
// strip with page-height) and joins the results back into a strip with the
// new page-height. Single-frame images go straight to fn.
static int
xi_per_frame(VipsImage *in, VipsImage **out, XiFrameFn fn, const void *arg)
{
  int ph, n = xi_frames(in, &ph);
  VipsImage *base, **frames, **results, *joined = NULL;
  int rc = -1;

  if (n <= 1)
    return fn(in, out, arg);

  base = vips_image_new();
  frames = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), n);
  results = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), n);
  for (int i = 0; i < n; i++) {
    if (vips_extract_area(in, &frames[i], 0, i * ph, in->Xsize, ph, NULL) ||
        fn(frames[i], &results[i], arg))
      goto done;
  }
  if (vips_arrayjoin(results, &joined, n, "across", 1, NULL))
    goto done;
  rc = xi_with_page_height(joined, results[0]->Ysize, n, out);
  g_object_unref(joined);

done:
  g_object_unref(base);
  return rc;
}

int
xi_resize(VipsImage *in, double hscale, double vscale, int kernel, VipsImage **out,
    char **err)
{
  int ph, n = xi_frames(in, &ph), new_ph = 0;
  VipsImage *t = NULL;

  if (n > 1) {
    // Scale each frame to a whole number of rows so the strip stays an
    // exact multiple of page-height.
    new_ph = (int) VIPS_MAX(1, rint(ph * vscale));
    vscale = (double) new_ph / ph;
  }
  if (vips_resize(in, &t, hscale, "vscale", vscale, "kernel", kernel, NULL))
    XI_FAIL();
  if (n > 1 && t->Ysize != new_ph * n) {
    // Rounding in vips_resize: crop or pad to the exact strip height.
    VipsImage *u = NULL;
    int rc = vips_embed(t, &u, 0, 0, t->Xsize, new_ph * n, "extend", VIPS_EXTEND_COPY, NULL);
    g_object_unref(t);
    if (rc)
      XI_FAIL();
    t = u;
  }
  if (xi_with_page_height(t, new_ph, n, out)) {
    g_object_unref(t);
    XI_FAIL();
  }
  g_object_unref(t);
  return XI_OK;
}

int
xi_extract_area(VipsImage *in, int left, int top, int width, int height, VipsImage **out,
    char **err)
{
  if (vips_extract_area(in, out, left, top, width, height, NULL))
    XI_FAIL();
  return XI_OK;
}

typedef struct {
  int left, top, width, height;
} XiRect;

static int
xi_crop_frame(VipsImage *in, VipsImage **out, const void *arg)
{
  const XiRect *r = arg;

  return vips_extract_area(in, out, r->left, r->top, r->width, r->height, NULL);
}

int
xi_crop(VipsImage *in, int left, int top, int width, int height, VipsImage **out,
    char **err)
{
  XiRect r = { left, top, width, height };

  if (xi_per_frame(in, out, xi_crop_frame, &r))
    XI_FAIL();
  return XI_OK;
}

typedef struct {
  int x, y, width, height, extend, direction;
  VipsArrayDouble *bg;
} XiEmbedArgs;

static int
xi_embed_frame(VipsImage *in, VipsImage **out, const void *arg)
{
  const XiEmbedArgs *a = arg;

  if (a->bg)
    return vips_embed(in, out, a->x, a->y, a->width, a->height, "extend", a->extend,
        "background", a->bg, NULL);
  return vips_embed(in, out, a->x, a->y, a->width, a->height, "extend", a->extend, NULL);
}

int
xi_embed(VipsImage *in, int x, int y, int width, int height, int extend, const double *bg,
    int nbg, VipsImage **out, char **err)
{
  XiEmbedArgs a = { x, y, width, height, extend, 0, xi_bg(bg, nbg) };
  int rc = xi_per_frame(in, out, xi_embed_frame, &a);

  xi_bg_free(a.bg);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

static int
xi_gravity_frame(VipsImage *in, VipsImage **out, const void *arg)
{
  const XiEmbedArgs *a = arg;

  if (a->bg)
    return vips_gravity(in, out, a->direction, a->width, a->height, "extend", a->extend,
        "background", a->bg, NULL);
  return vips_gravity(in, out, a->direction, a->width, a->height, "extend", a->extend, NULL);
}

int
xi_gravity(VipsImage *in, int direction, int width, int height, int extend,
    const double *bg, int nbg, VipsImage **out, char **err)
{
  XiEmbedArgs a = { 0, 0, width, height, extend, direction, xi_bg(bg, nbg) };
  int rc = xi_per_frame(in, out, xi_gravity_frame, &a);

  xi_bg_free(a.bg);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

static int
xi_flip_frame(VipsImage *in, VipsImage **out, const void *arg)
{
  return vips_flip(in, out, *(const int *) arg, NULL);
}

int
xi_flip(VipsImage *in, int direction, VipsImage **out, char **err)
{
  int rc;

  // A horizontal flip of the whole strip flips every frame in place.
  if (direction == VIPS_DIRECTION_HORIZONTAL)
    rc = vips_flip(in, out, direction, NULL);
  else
    rc = xi_per_frame(in, out, xi_flip_frame, &direction);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

static int
xi_rot_frame(VipsImage *in, VipsImage **out, const void *arg)
{
  return vips_rot(in, out, *(const int *) arg, NULL);
}

int
xi_rot(VipsImage *in, int angle, VipsImage **out, char **err)
{
  if (xi_per_frame(in, out, xi_rot_frame, &angle))
    XI_FAIL();
  return XI_OK;
}

static int
xi_autorot_frame(VipsImage *in, VipsImage **out, const void *arg)
{
  (void) arg;
  return vips_autorot(in, out, NULL);
}

int
xi_autorot(VipsImage *in, VipsImage **out, char **err)
{
  VipsImage *t = NULL;

  // vips_autorot removes the orientation tag from its output; frames of an
  // animation share the strip's tag, so per-frame rotation is exact.
  if (xi_per_frame(in, &t, xi_autorot_frame, NULL))
    XI_FAIL();
  if (vips_image_get_typeof(t, VIPS_META_ORIENTATION)) {
    int rc = vips_copy(t, out, NULL);
    g_object_unref(t);
    if (rc)
      XI_FAIL();
    vips_image_remove(*out, VIPS_META_ORIENTATION);
    return XI_OK;
  }
  *out = t;
  return XI_OK;
}

int
xi_smartcrop(VipsImage *in, int width, int height, int interesting, VipsImage **out,
    char **err)
{
  if (xi_frames(in, NULL) > 1) {
    *err = xi_error_new("xi_smartcrop", "smartcrop needs a single-frame image");
    return XI_ERR_OP;
  }
  if (vips_smartcrop(in, out, width, height, "interesting", interesting, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_flatten(VipsImage *in, const double *bg, int nbg, VipsImage **out, char **err)
{
  VipsArrayDouble *a = NULL;
  double scaled[4];
  int rc;

  if (!vips_image_hasalpha(in)) {
    if (vips_copy(in, out, NULL))
      XI_FAIL();
    return XI_OK;
  }
  if (nbg > 0) {
    // Backgrounds are given on the 8-bit scale; 16-bit images need them
    // scaled like their alpha (imgproxy vips_flatten_go).
    double k = vips_interpretation_max_alpha(vips_image_guess_interpretation(in)) / 255.0;

    nbg = VIPS_MIN(nbg, 4);
    for (int i = 0; i < nbg; i++)
      scaled[i] = bg[i] * k;
    a = vips_array_double_new(scaled, nbg);
    rc = vips_flatten(in, out, "background", a, NULL);
    vips_area_unref(VIPS_AREA(a));
  }
  else
    rc = vips_flatten(in, out, NULL);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

int
xi_premultiply(VipsImage *in, VipsImage **out, char **err)
{
  if (vips_premultiply(in, out, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_unpremultiply(VipsImage *in, VipsImage **out, char **err)
{
  VipsImage *t = NULL;
  int rc;

  // unpremultiply yields float; cast back to the premultiplied input's
  // format as sharp and imgproxy do.
  if (vips_unpremultiply(in, &t, NULL))
    XI_FAIL();
  rc = vips_cast(t, out, in->BandFmt, NULL);
  g_object_unref(t);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

int
xi_colourspace(VipsImage *in, int interpretation, VipsImage **out, char **err)
{
  if (vips_colourspace(in, out, interpretation, NULL))
    XI_FAIL();
  return XI_OK;
}

// xi_icc_pcs is the connection space of the embedded profile (imgproxy
// vips_icc_get_pcs). Importing to Lab when the profile's PCS is XYZ shifts
// colours by several levels after the conversion to sRGB.
static VipsPCS
xi_icc_pcs(VipsImage *in)
{
  const void *data;
  size_t len;

  if (vips_image_get_typeof(in, VIPS_META_ICC_NAME) &&
      !vips_image_get_blob(in, VIPS_META_ICC_NAME, &data, &len) && len >= 24 &&
      memcmp((const char *) data + 20, "XYZ ", 4) == 0)
    return VIPS_PCS_XYZ;
  return VIPS_PCS_LAB;
}

int
xi_icc_import(VipsImage *in, const char *profile, int intent, int embedded,
    VipsImage **out, char **err)
{
  int rc;
  VipsPCS pcs = embedded ? xi_icc_pcs(in) : VIPS_PCS_LAB;

  if (profile)
    rc = vips_icc_import(in, out, "intent", intent, "embedded", embedded,
        "input_profile", profile, "pcs", pcs, NULL);
  else
    rc = vips_icc_import(in, out, "intent", intent, "embedded", embedded, "pcs", pcs, NULL);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

int
xi_icc_export(VipsImage *in, const char *profile, int intent, int depth, VipsImage **out,
    char **err)
{
  int rc;

  if (profile)
    rc = vips_icc_export(in, out, "intent", intent, "depth", depth, "output_profile",
        profile, NULL);
  else
    rc = vips_icc_export(in, out, "intent", intent, "depth", depth, NULL);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

int
xi_icc_transform(VipsImage *in, const char *profile, int intent, int embedded, int depth,
    VipsImage **out, char **err)
{
  if (vips_icc_transform(in, out, profile, "intent", intent, "embedded", embedded, "depth",
          depth, NULL))
    XI_FAIL();
  return XI_OK;
}

// xi_profile_load loads a named ICC profile (a libvips built-in such as
// "srgb" or "sgrey", or a path). *blob is NULL for "none"; the caller unrefs
// it after copying *data.
int
xi_profile_load(const char *name, VipsBlob **blob, const void **data, size_t *len, char **err)
{
  *blob = NULL;
  *data = NULL;
  *len = 0;
  if (vips_profile_load(name, blob, NULL))
    XI_FAIL();
  if (*blob)
    *data = vips_blob_get(*blob, len);
  return XI_OK;
}

int
xi_cast(VipsImage *in, int band_format, VipsImage **out, char **err)
{
  if (vips_cast(in, out, band_format, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_copy(VipsImage *in, int interpretation, double xres, double yres, int page_height,
    VipsImage **out, char **err)
{
  if (vips_copy(in, out,
          "interpretation", interpretation > 0 ? interpretation : in->Type,
          "xres", xres > 0 ? xres : in->Xres,
          "yres", yres > 0 ? yres : in->Yres,
          NULL))
    XI_FAIL();
  // The copy is a fresh node that only we reference: safe to modify.
  if (page_height > 0)
    vips_image_set_int(*out, VIPS_META_PAGE_HEIGHT, page_height);
  return XI_OK;
}

int
xi_gaussblur(VipsImage *in, double sigma, VipsImage **out, char **err)
{
  if (vips_gaussblur(in, out, sigma, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_sharpen(VipsImage *in, double sigma, double x1, double y2, double y3, double m1,
    double m2, VipsImage **out, char **err)
{
  if (vips_sharpen(in, out, "sigma", sigma, "x1", x1, "y2", y2, "y3", y3, "m1", m1, "m2",
          m2, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_shrink(VipsImage *in, int hshrink, int vshrink, VipsImage **out, char **err)
{
  if (vips_shrink(in, out, (double) hshrink, (double) vshrink, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_composite2(VipsImage *base, VipsImage *overlay, int mode, int x, int y, VipsImage **out,
    char **err)
{
  if (vips_composite2(base, overlay, out, mode, "x", x, "y", y, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_replicate(VipsImage *in, int across, int down, VipsImage **out, char **err)
{
  if (vips_replicate(in, out, across, down, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_join(VipsImage *a, VipsImage *b, int direction, VipsImage **out, char **err)
{
  if (vips_join(a, b, out, direction, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_arrayjoin(VipsImage **in, int n, int across, VipsImage **out, char **err)
{
  if (vips_arrayjoin(in, out, n, "across", across, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_linear(VipsImage *in, const double *a, const double *b, int n, VipsImage **out,
    char **err)
{
  if (vips_linear(in, out, (double *) a, (double *) b, n, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_invert(VipsImage *in, VipsImage **out, char **err)
{
  if (vips_invert(in, out, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_bandjoin(VipsImage **in, int n, VipsImage **out, char **err)
{
  if (vips_bandjoin(in, out, n, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_bandjoin_const(VipsImage *in, const double *c, int n, VipsImage **out, char **err)
{
  if (vips_bandjoin_const(in, out, (double *) c, n, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_extract_band(VipsImage *in, int band, int n, VipsImage **out, char **err)
{
  if (vips_extract_band(in, out, band, "n", n, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_addalpha(VipsImage *in, VipsImage **out, char **err)
{
  if (vips_addalpha(in, out, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_find_trim(VipsImage *in, double threshold, const double *bg, int nbg, int line_art,
    int *left, int *top, int *width, int *height, char **err)
{
  VipsImage *base = vips_image_new();
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), 2);
  VipsImage *tmp = in;
  VipsArrayDouble *a = NULL;
  double *corner = NULL;
  int ncorner = 0, rc;

  if (xi_frames(in, NULL) > 1) {
    g_object_unref(base);
    *err = xi_error_new("xi_find_trim", "trim needs a single-frame image");
    return XI_ERR_OP;
  }
  // imgproxy vips_trim: compare in sRGB with transparency flattened onto a
  // colour that cannot be mistaken for image content.
  if (vips_image_guess_interpretation(tmp) != VIPS_INTERPRETATION_sRGB) {
    if (vips_colourspace(tmp, &t[0], VIPS_INTERPRETATION_sRGB, NULL))
      goto fail;
    tmp = t[0];
  }
  if (vips_image_hasalpha(tmp)) {
    double magenta[3] = { 255.0, 0.0, 255.0 };
    VipsArrayDouble *m = vips_array_double_new(magenta, 3);

    rc = vips_flatten(tmp, &t[1], "background", m, NULL);
    vips_area_unref(VIPS_AREA(m));
    if (rc)
      goto fail;
    tmp = t[1];
  }
  if (nbg > 0)
    a = vips_array_double_new(bg, nbg);
  else {
    // No background given: use the top-left pixel, like imgproxy's smart
    // trim and vips_find_trim's own default.
    if (vips_getpoint(tmp, &corner, &ncorner, 0, 0, NULL))
      goto fail;
    a = vips_array_double_new(corner, ncorner);
    g_free(corner);
  }
  rc = vips_find_trim(tmp, left, top, width, height, "threshold", threshold, "background",
      a, "line_art", line_art, NULL);
  vips_area_unref(VIPS_AREA(a));
  if (rc)
    goto fail;
  g_object_unref(base);
  return XI_OK;

fail:
  *err = xi_error_copy();
  g_object_unref(base);
  return XI_ERR_OP;
}

typedef struct {
  int affine;
  double scale, angle;
  double a, b, c, d;
  VipsArrayDouble *bg;
} XiTransformArgs;

static int
xi_transform_frame(VipsImage *in, VipsImage **out, const void *arg)
{
  const XiTransformArgs *t = arg;

  if (t->affine) {
    if (t->bg)
      return vips_affine(in, out, t->a, t->b, t->c, t->d, "background", t->bg, NULL);
    return vips_affine(in, out, t->a, t->b, t->c, t->d, NULL);
  }
  if (t->bg)
    return vips_similarity(in, out, "scale", t->scale, "angle", t->angle, "background",
        t->bg, NULL);
  return vips_similarity(in, out, "scale", t->scale, "angle", t->angle, NULL);
}

int
xi_similarity(VipsImage *in, double scale, double angle, const double *bg, int nbg,
    VipsImage **out, char **err)
{
  XiTransformArgs t = { 0, scale, angle, 0, 0, 0, 0, xi_bg(bg, nbg) };
  int rc = xi_per_frame(in, out, xi_transform_frame, &t);

  xi_bg_free(t.bg);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

int
xi_affine(VipsImage *in, double a, double b, double c, double d, const double *bg, int nbg,
    VipsImage **out, char **err)
{
  XiTransformArgs t = { 1, 0, 0, a, b, c, d, xi_bg(bg, nbg) };
  int rc = xi_per_frame(in, out, xi_transform_frame, &t);

  xi_bg_free(t.bg);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

// xi_round_cast_to rounds float pixels to nearest before casting them to an
// integer format, since vips_cast truncates. Integer inputs, and any cast to
// a float or complex format, are cast directly: rounding there would drop the
// fraction a float (scRGB, HDR) result must keep.
static int
xi_round_cast_to(VipsImage *in, VipsBandFormat format, VipsImage **out)
{
  VipsImage *r = NULL;
  int rc;

  if ((!vips_band_format_isfloat(in->BandFmt) && !vips_band_format_iscomplex(in->BandFmt)) ||
      vips_band_format_isfloat(format) || vips_band_format_iscomplex(format))
    return vips_cast(in, out, format, NULL);
  if (vips_rint(in, &r, NULL))
    return -1;
  rc = vips_cast(r, out, format, NULL);
  g_object_unref(r);
  return rc;
}

int
xi_cast_round(VipsImage *in, int band_format, VipsImage **out, char **err)
{
  if (xi_round_cast_to(in, band_format, out))
    XI_FAIL();
  return XI_OK;
}

int
xi_maplut(VipsImage *in, VipsImage *lut, VipsImage **out, char **err)
{
  if (vips_maplut(in, out, lut, NULL))
    XI_FAIL();
  return XI_OK;
}

// xi_recomb multiplies every pixel (a column of cols bands) by the rows×cols
// matrix m, giving rows bands in the input's band format.
int
xi_recomb(VipsImage *in, const double *m, int rows, int cols, VipsImage **out, char **err)
{
  VipsImage *base = vips_image_new();
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), 2);

  t[0] = vips_image_new_matrix_from_array(cols, rows, m, rows * cols);
  if (!t[0])
    goto fail;
  if (vips_recomb(in, &t[1], t[0], NULL))
    goto fail;
  if (xi_round_cast_to(t[1], in->BandFmt, out))
    goto fail;
  g_object_unref(base);
  return XI_OK;

fail:
  *err = xi_error_copy();
  g_object_unref(base);
  return XI_ERR_OP;
}

// xi_mix is a × (1 − t) + b × t in a's band format.
int
xi_mix(VipsImage *a, VipsImage *b, double t, VipsImage **out, char **err)
{
  VipsImage *base = vips_image_new();
  VipsImage **l = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), 3);

  if (vips_linear1(a, &l[0], 1.0 - t, 0.0, NULL) || vips_linear1(b, &l[1], t, 0.0, NULL) ||
      vips_add(l[0], l[1], &l[2], NULL) || xi_round_cast_to(l[2], a->BandFmt, out)) {
    *err = xi_error_copy();
    g_object_unref(base);
    return XI_ERR_OP;
  }
  g_object_unref(base);
  return XI_OK;
}

// xi_gradient_mask makes a one-band uchar width×height image whose pixel
// (x, y) is round(opacity × clamp(ax·x + ay·y + b, 0, 255)): a linear ramp
// along (ax, ay), clamped by the uchar cast.
int
xi_gradient_mask(int width, int height, double ax, double ay, double b, double opacity,
    VipsImage **out, char **err)
{
  VipsImage *base = vips_image_new();
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), 6);
  double m[2] = { ax, ay };

  if (vips_xyz(&t[0], width, height, NULL))
    goto fail;
  t[1] = vips_image_new_matrix_from_array(2, 1, m, 2);
  if (!t[1])
    goto fail;
  if (vips_recomb(t[0], &t[2], t[1], NULL) || vips_linear1(t[2], &t[3], 1.0, b, NULL) ||
      xi_round_cast_to(t[3], VIPS_FORMAT_UCHAR, &t[4]))
    goto fail;
  if (opacity < 1.0) {
    if (vips_linear1(t[4], &t[5], opacity, 0.0, NULL) ||
        xi_round_cast_to(t[5], VIPS_FORMAT_UCHAR, out))
      goto fail;
  }
  else if (vips_copy(t[4], out, "interpretation", VIPS_INTERPRETATION_B_W, NULL))
    goto fail;
  g_object_unref(base);
  return XI_OK;

fail:
  *err = xi_error_copy();
  g_object_unref(base);
  return XI_ERR_OP;
}

// Filter extras (WP-61). Results keep the input's band format: float
// intermediates are rounded to nearest, then clamped by the cast.

// xi_conv3 convolves in with the 3×3 row-major matrix m, used as it is
// (scale 1, offset 0), in float precision. Edges repeat the border pixels
// (vips_convf embeds with VIPS_EXTEND_COPY), so the size is kept.
int
xi_conv3(VipsImage *in, const double *m, VipsImage **out, char **err)
{
  VipsImage *base = vips_image_new();
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), 2);

  t[0] = vips_image_new_matrix_from_array(3, 3, m, 9);
  if (!t[0])
    goto fail;
  if (vips_conv(in, &t[1], t[0], "precision", VIPS_PRECISION_FLOAT, NULL) ||
      xi_round_cast_to(t[1], in->BandFmt, out))
    goto fail;
  g_object_unref(base);
  return XI_OK;

fail:
  *err = xi_error_copy();
  g_object_unref(base);
  return XI_ERR_OP;
}

// xi_unsharp is the classic unsharp mask in + amount × (in − blur), blur
// being a float-precision gaussian of sigma, computed as
// (1 + amount) × in − amount × blur.
int
xi_unsharp(VipsImage *in, double sigma, double amount, VipsImage **out, char **err)
{
  VipsImage *base = vips_image_new();
  VipsImage **t = (VipsImage **) vips_object_local_array(VIPS_OBJECT(base), 4);

  if (vips_gaussblur(in, &t[0], sigma, "precision", VIPS_PRECISION_FLOAT, NULL) ||
      vips_linear1(in, &t[1], 1.0 + amount, 0.0, NULL) ||
      vips_linear1(t[0], &t[2], -amount, 0.0, NULL) || vips_add(t[1], t[2], &t[3], NULL) ||
      xi_round_cast_to(t[3], in->BandFmt, out)) {
    *err = xi_error_copy();
    g_object_unref(base);
    return XI_ERR_OP;
  }
  g_object_unref(base);
  return XI_OK;
}

// xi_insert places sub over main with its top-left corner at (x, y),
// clipped to main (vips_insert without expand).
int
xi_insert(VipsImage *main, VipsImage *sub, int x, int y, VipsImage **out, char **err)
{
  if (vips_insert(main, sub, out, x, y, NULL))
    XI_FAIL();
  return XI_OK;
}

// xi_blend_mask is a × m/255 + b × (1 − m/255) for the one-band uchar mask
// m (vips_ifthenelse with blend), in a's band format.
int
xi_blend_mask(VipsImage *mask, VipsImage *a, VipsImage *b, VipsImage **out, char **err)
{
  VipsImage *t = NULL;
  int rc;

  if (vips_ifthenelse(mask, a, b, &t, "blend", TRUE, NULL))
    XI_FAIL();
  rc = xi_round_cast_to(t, a->BandFmt, out);
  g_object_unref(t);
  if (rc)
    XI_FAIL();
  return XI_OK;
}

// ---- HDR (WP-67) -----------------------------------------------------------

int
xi_decode(VipsImage *in, VipsImage **out, char **err)
{
  // Radiance RGBE (and LabQ) to float; any other image is passed through.
  if (vips_image_decode(in, out))
    XI_FAIL();
  return XI_OK;
}

int
xi_clamp(VipsImage *in, double min, double max, VipsImage **out, char **err)
{
  if (vips_clamp(in, out, "min", min, "max", max, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_multiply(VipsImage *a, VipsImage *b, VipsImage **out, char **err)
{
  if (vips_multiply(a, b, out, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_divide(VipsImage *a, VipsImage *b, VipsImage **out, char **err)
{
  if (vips_divide(a, b, out, NULL))
    XI_FAIL();
  return XI_OK;
}

int
xi_math2_const(VipsImage *in, int op, const double *c, int n, VipsImage **out, char **err)
{
  if (vips_math2_const(in, out, op, c, n, NULL))
    XI_FAIL();
  return XI_OK;
}

// ---- gain maps (WP-104) -----------------------------------------------------

// xi_get3 reads a three-value double array field.
static gboolean
xi_get3(VipsImage *in, const char *name, double out[3])
{
  double *d;
  int n;

  if (!vips_image_get_typeof(in, name) || vips_image_get_array_double(in, name, &d, &n) ||
      n != 3)
    return FALSE;
  memcpy(out, d, 3 * sizeof(double));
  return TRUE;
}

int
xi_uhdr_gainmap(VipsImage *in, VipsImage **gm, XiUhdrGainMap *p, char **err)
{
  memset(p, 0, sizeof(*p));
  *gm = NULL;
  if (!vips_image_get_typeof(in, "gainmap") && !vips_image_get_typeof(in, "gainmap-data"))
    return XI_OK;
  if (!xi_get3(in, "gainmap-min-content-boost", p->min_boost) ||
      !xi_get3(in, "gainmap-max-content-boost", p->max_boost) ||
      !xi_get3(in, "gainmap-gamma", p->gamma) ||
      !xi_get3(in, "gainmap-offset-sdr", p->offset_sdr) ||
      !xi_get3(in, "gainmap-offset-hdr", p->offset_hdr) ||
      vips_image_get_double(in, "gainmap-hdr-capacity-min", &p->capacity_min) ||
      vips_image_get_double(in, "gainmap-hdr-capacity-max", &p->capacity_max)) {
    vips_error_clear();
    *err = xi_error_new("xi_uhdr_gainmap", "incomplete gain map metadata");
    return XI_ERR_LOAD;
  }
  // Decompresses "gainmap-data" (a JPEG) unless the image carries one.
  if (!(*gm = vips_image_get_gainmap(in))) {
    *err = xi_error_copy();
    return XI_ERR_LOAD;
  }
  return XI_OK;
}

// Text (WP-78).

// xi_text renders Pango markup with vips_text as 4-band sRGB (rgba), built
// through the operation API because the optional arguments vary.
int
xi_text(const char *text, const char *font, const char *fontfile, int width, int height,
    int dpi, VipsImage **out, int *fit_dpi, char **err)
{
  VipsOperation *op = vips_operation_new("text");

  if (!op)
    XI_FAIL();
  g_object_set(op, "text", text, "rgba", TRUE, "wrap", VIPS_TEXT_WRAP_WORD_CHAR, NULL);
  if (font)
    g_object_set(op, "font", font, NULL);
  if (fontfile)
    g_object_set(op, "fontfile", fontfile, NULL);
  if (width > 0)
    g_object_set(op, "width", width, NULL);
  if (height > 0)
    g_object_set(op, "height", height, NULL);
  else if (dpi > 0)
    g_object_set(op, "dpi", dpi, NULL);
  if (vips_cache_operation_buildp(&op)) {
    vips_object_unref_outputs(VIPS_OBJECT(op));
    g_object_unref(op);
    XI_FAIL();
  }
  g_object_get(op, "out", out, NULL);
  if (height > 0)
    g_object_get(op, "autofit_dpi", fit_dpi, NULL);
  else
    *fit_dpi = dpi;
  vips_object_unref_outputs(VIPS_OBJECT(op));
  g_object_unref(op);
  return XI_OK;
}
