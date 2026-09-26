// Hand-written C shim for internal/vips: image accessors and metadata. See
// shim.h for the licence notice.

#include <string.h>

#include "shim.h"

int
xi_image_width(VipsImage *img)
{
  return vips_image_get_width(img);
}

int
xi_image_height(VipsImage *img)
{
  return vips_image_get_height(img);
}

int
xi_image_bands(VipsImage *img)
{
  return vips_image_get_bands(img);
}

int
xi_image_band_format(VipsImage *img)
{
  return vips_image_get_format(img);
}

int
xi_image_interpretation(VipsImage *img)
{
  return vips_image_guess_interpretation(img);
}

int
xi_image_has_alpha(VipsImage *img)
{
  return vips_image_hasalpha(img);
}

int
xi_image_orientation(VipsImage *img)
{
  return vips_image_get_orientation(img);
}

int
xi_image_page_height(VipsImage *img)
{
  return vips_image_get_page_height(img);
}

double
xi_image_xres(VipsImage *img)
{
  return vips_image_get_xres(img);
}

double
xi_image_yres(VipsImage *img)
{
  return vips_image_get_yres(img);
}

size_t
xi_image_sizeof(VipsImage *img)
{
  return VIPS_IMAGE_SIZEOF_IMAGE(img);
}

void
xi_image_unref(VipsImage *img)
{
  VIPS_UNREF(img);
}

void
xi_image_ref(VipsImage *img)
{
  g_object_ref(img);
}

void
xi_image_set_kill(VipsImage *img, int kill)
{
  // A plain flag that the pipeline polls per region (region.c
  // vips_region_prepare), so it is safe to set from another thread while a
  // sink runs.
  vips_image_set_kill(img, kill ? TRUE : FALSE);
}

int
xi_meta_get_int(VipsImage *img, const char *name, int *out)
{
  if (!vips_image_get_typeof(img, name))
    return -1;
  if (vips_image_get_int(img, name, out)) {
    vips_error_clear();
    return -1;
  }
  return 0;
}

int
xi_meta_get_blob(VipsImage *img, const char *name, const void **data, size_t *len)
{
  if (!vips_image_get_typeof(img, name))
    return -1;
  if (vips_image_get_blob(img, name, data, len)) {
    vips_error_clear();
    return -1;
  }
  return 0;
}

int
xi_meta_get_array_int(VipsImage *img, const char *name, int **out, int *n)
{
  if (!vips_image_get_typeof(img, name))
    return -1;
  if (vips_image_get_array_int(img, name, out, n)) {
    vips_error_clear();
    return -1;
  }
  return 0;
}

// Metadata setters never touch their input, which other graph nodes may
// share: they return a fresh copy (a cheap, lazy node) and change that.
static int
xi_fresh_copy(VipsImage *in, VipsImage **out, char **err)
{
  if (vips_copy(in, out, NULL)) {
    *err = xi_error_copy();
    return XI_ERR_OP;
  }
  return XI_OK;
}

int
xi_meta_set_int(VipsImage *in, const char *name, int v, VipsImage **out, char **err)
{
  if (xi_fresh_copy(in, out, err))
    return XI_ERR_OP;
  vips_image_set_int(*out, name, v);
  return XI_OK;
}

int
xi_meta_set_blob(VipsImage *in, const char *name, const void *data, size_t len,
    VipsImage **out, char **err)
{
  if (xi_fresh_copy(in, out, err))
    return XI_ERR_OP;
  // Copies: data may be Go memory that C must not keep.
  vips_image_set_blob_copy(*out, name, data, len);
  return XI_OK;
}

int
xi_meta_set_array_int(VipsImage *in, const char *name, const int *v, int n,
    VipsImage **out, char **err)
{
  if (xi_fresh_copy(in, out, err))
    return XI_ERR_OP;
  // vips_image_set_array_int copies the array.
  vips_image_set_array_int(*out, name, v, n);
  return XI_OK;
}

int
xi_meta_remove(VipsImage *in, const char *names, int n, VipsImage **out, char **err)
{
  if (xi_fresh_copy(in, out, err))
    return XI_ERR_OP;
  for (int i = 0; i < n; i++) {
    vips_image_remove(*out, names);
    names += strlen(names) + 1;
  }
  return XI_OK;
}

int
xi_meta_get_string(VipsImage *img, const char *name, const char **out)
{
  if (!vips_image_get_typeof(img, name))
    return -1;
  if (vips_image_get_string(img, name, out)) {
    vips_error_clear();
    return -1;
  }
  return 0;
}

char *
xi_meta_fields(VipsImage *img, size_t *len)
{
  gchar **fields = vips_image_get_fields(img);
  size_t n = 0;
  char *buf, *p;

  for (int i = 0; fields[i]; i++)
    n += strlen(fields[i]) + 1;
  *len = n;
  if (n == 0) {
    g_strfreev(fields);
    return NULL;
  }
  buf = g_malloc(n);
  p = buf;
  for (int i = 0; fields[i]; i++) {
    size_t l = strlen(fields[i]) + 1;
    memcpy(p, fields[i], l);
    p += l;
  }
  g_strfreev(fields);
  return buf;
}

int
xi_copy_memory(VipsImage *in, VipsImage **out, char **err)
{
  if (!(*out = vips_image_copy_memory(in))) {
    *err = xi_error_copy();
    return XI_ERR_SAVE;
  }
  return XI_OK;
}

int
xi_share(VipsImage *in, VipsImage **out, char **err)
{
  size_t len;
  void *data = vips_image_write_to_memory(in, &len);
  if (!data) {
    *err = xi_error_copy();
    return XI_ERR_SAVE;
  }
  // A new root image: no upstream links (so no progress or kill signal is
  // shared with the pipeline that produced it) and no metadata fields.
  *out = vips_image_new_from_memory_copy(data, len, in->Xsize, in->Ysize, in->Bands,
      in->BandFmt);
  g_free(data);
  if (!*out) {
    *err = xi_error_copy();
    return XI_ERR_SAVE;
  }
  (*out)->Type = in->Type;
  return XI_OK;
}

int
xi_write_to_memory(VipsImage *in, void **data, size_t *len, char **err)
{
  if (!(*data = vips_image_write_to_memory(in, len))) {
    *err = xi_error_copy();
    return XI_ERR_SAVE;
  }
  return XI_OK;
}
