// Hand-written C shim for internal/vips. See shim.h for the licence notice of
// the portions derived from imgproxy (Apache-2.0).

#include <string.h>

#include "shim.h"

#define XI_SRC_BLOB_KEY "xi-src-blob"

static char *
xi_error_copy(void)
{
  // Copies and clears the process-global error buffer. Called right at the
  // failure site so the text belongs to this operation (go-standards §4).
  return vips_error_buffer_copy();
}

int
xi_init(const char *argv0, int concurrency, int cache_max, size_t cache_max_mem,
    int cache_max_files, const char **allowed_loaders, int n_allowed, char **err)
{
  // vips_init is idempotent: a second call returns 0 without doing anything.
  if (VIPS_INIT(argv0)) {
    *err = xi_error_copy();
    return XI_ERR_INTERNAL;
  }

  // imgproxy vips.go Init: the operation cache can SIGSEGV on musl and does
  // not help a tuned pipeline; parallelism comes from the request semaphore.
  vips_cache_set_max(cache_max);
  vips_cache_set_max_mem(cache_max_mem);
  vips_cache_set_max_files(cache_max_files);
  vips_concurrency_set(concurrency);

  // Allowlist of loaders: block everything flagged untrusted, then every
  // loader, then re-enable exactly the configured ones (FORMATS.md M1).
  vips_block_untrusted_set(TRUE);
  vips_operation_block_set("VipsForeignLoad", TRUE);
  for (int i = 0; i < n_allowed; i++)
    vips_operation_block_set(allowed_loaders[i], FALSE);

  return XI_OK;
}

void
xi_shutdown(void)
{
  vips_shutdown();
}

void
xi_thread_cleanup(void)
{
  // imgproxy vips.c vips_cleanup: libvips keeps per-thread state that leaks
  // unless it is released on the thread that used it.
  vips_error_clear();
  vips_thread_shutdown();
}

size_t
xi_tracked_mem(void)
{
  return vips_tracked_get_mem();
}

size_t
xi_tracked_mem_highwater(void)
{
  return vips_tracked_get_mem_highwater();
}

int
xi_tracked_allocs(void)
{
  return vips_tracked_get_allocs();
}

static int
xi_blob_free(void *data, void *area)
{
  (void) area;
  vips_tracked_free(data);
  return 0;
}

VipsBlob *
xi_blob_new(size_t capacity)
{
  void *data;

  if (capacity == 0)
    capacity = 1;
  if (!(data = vips_tracked_malloc(capacity)))
    return NULL;
  return vips_blob_new((VipsCallbackFn) xi_blob_free, data, capacity);
}

VipsBlob *
xi_blob_grow(VipsBlob *blob, size_t used, size_t capacity)
{
  VipsBlob *next;

  if (!(next = xi_blob_new(capacity)))
    return NULL;
  memcpy(VIPS_AREA(next)->data, VIPS_AREA(blob)->data, used);
  vips_area_unref(VIPS_AREA(blob));
  return next;
}

void *
xi_blob_data(VipsBlob *blob)
{
  return VIPS_AREA(blob)->data;
}

void
xi_blob_set_length(VipsBlob *blob, size_t length)
{
  // The capacity may exceed the content; loaders must only see the content.
  VIPS_AREA(blob)->length = length;
}

void
xi_blob_unref(VipsBlob *blob)
{
  vips_area_unref(VIPS_AREA(blob));
}

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

int
xi_thumbnail(VipsBlob *blob, int width, int height, int size, int fail_on,
    VipsImage **out, char **err)
{
  VipsArea *area = VIPS_AREA(blob);
  const char *loader;

  *out = NULL;
  if (!(loader = vips_foreign_find_load_buffer(area->data, area->length))) {
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }
  // The libvips sniffer already skips blocked loaders; this is a second
  // line of defence should that ever change.
  if (xi_loader_blocked(loader)) {
    vips_error("xi_thumbnail", "loader %s is blocked", loader);
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }

  // vips_thumbnail_buffer does not copy or own the bytes: loading is lazy and
  // pixels are read on save. The output keeps a blob reference for that.
  if (vips_thumbnail_buffer(area->data, area->length, out, width,
          "height", height,
          "size", size,
          "fail_on", fail_on,
          NULL)) {
    *err = xi_error_copy();
    return XI_ERR_LOAD;
  }

  vips_area_copy(area);
  g_object_set_data_full(G_OBJECT(*out), XI_SRC_BLOB_KEY, area,
      (GDestroyNotify) vips_area_unref);
  return XI_OK;
}

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

void
xi_image_unref(VipsImage *img)
{
  VIPS_UNREF(img);
}

int
xi_jpegsave(VipsImage *in, int quality, void **buf, size_t *len, char **err)
{
  *buf = NULL;
  *len = 0;
  if (vips_jpegsave_buffer(in, buf, len,
          "Q", quality,
          "optimize_coding", TRUE,
          "keep", VIPS_FOREIGN_KEEP_NONE,
          NULL)) {
    *err = xi_error_copy();
    return XI_ERR_SAVE;
  }
  return XI_OK;
}

void
xi_free(void *p)
{
  g_free(p);
}
