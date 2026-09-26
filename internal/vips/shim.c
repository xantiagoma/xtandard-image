// Hand-written C shim for internal/vips: lifecycle, statistics and source
// buffers. See shim.h for the licence notice of the portions derived from
// imgproxy (Apache-2.0).

#include <string.h>

#include "shim.h"

// Set once by xi_init: whether heifsave may encode HEVC (HEIC). Process
// state that belongs to libvips' configuration, like the loader block list.
static int xi_heic_encode = 0;

int
xi_heic_encode_enabled(void)
{
  return g_atomic_int_get(&xi_heic_encode);
}

char *
xi_error_copy(void)
{
  // Copies and clears the process-global error buffer. Called right at the
  // failure site so the text belongs to this operation (go-standards §4).
  return vips_error_buffer_copy();
}

// ---- source registry -----------------------------------------------------
//
// data pointer -> GINT_TO_POINTER(failed + 1). The key is the tracked
// allocation of a live xi blob; loaders wrap the same pointer in their own
// blobs (thumbnail_buffer does), so the pointer, not the VipsBlob, is the
// identity of a source.

static GMutex xi_sources_lock;
static GHashTable *xi_sources = NULL;

static void
xi_sources_insert(void *data)
{
  g_mutex_lock(&xi_sources_lock);
  if (!xi_sources)
    xi_sources = g_hash_table_new(g_direct_hash, g_direct_equal);
  g_hash_table_insert(xi_sources, data, GINT_TO_POINTER(1));
  g_mutex_unlock(&xi_sources_lock);
}

static void
xi_sources_remove(void *data)
{
  g_mutex_lock(&xi_sources_lock);
  if (xi_sources)
    g_hash_table_remove(xi_sources, data);
  g_mutex_unlock(&xi_sources_lock);
}

static void
xi_sources_mark_failed(const void *data)
{
  g_mutex_lock(&xi_sources_lock);
  if (xi_sources && g_hash_table_contains(xi_sources, data))
    g_hash_table_insert(xi_sources, (void *) data, GINT_TO_POINTER(2));
  g_mutex_unlock(&xi_sources_lock);
}

static int
xi_sources_failed(const void *data)
{
  int failed;

  g_mutex_lock(&xi_sources_lock);
  failed = xi_sources &&
      GPOINTER_TO_INT(g_hash_table_lookup(xi_sources, data)) == 2;
  g_mutex_unlock(&xi_sources_lock);
  return failed;
}

// Emission hook for VipsOperation::invalidate. libvips emits it on a loader
// when decoding fails (foreign.c vips_foreign_load_start and
// vips_foreign_load_invalidate), also when that happens lazily inside a
// sink long after the header was read. Runs on whichever thread decoded.
static gboolean
xi_invalidate_hook(GSignalInvocationHint *ihint, guint n_param_values,
    const GValue *param_values, gpointer data)
{
  GObject *op;
  VipsBlob *blob = NULL;

  (void) ihint;
  (void) data;
  if (n_param_values < 1)
    return TRUE;
  op = g_value_get_object(&param_values[0]);
  if (!op || !VIPS_IS_FOREIGN_LOAD(op) ||
      !g_object_class_find_property(G_OBJECT_GET_CLASS(op), "buffer"))
    return TRUE;
  g_object_get(op, "buffer", &blob, NULL);
  if (blob) {
    xi_sources_mark_failed(VIPS_AREA(blob)->data);
    vips_area_unref(VIPS_AREA(blob));
  }
  return TRUE;
}

// ---- lifecycle -----------------------------------------------------------

int
xi_init(const char *argv0, int concurrency, int cache_max, size_t cache_max_mem,
    int cache_max_files, const char **allowed_loaders, int n_loaders,
    const char **allowed_savers, int n_savers, int heic_encode, char **err)
{
  static gsize hooked = 0;

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

  // Allowlists of loaders and savers: block everything flagged untrusted,
  // then every loader and saver, then re-enable exactly the configured ones
  // (FORMATS.md M1). jxlsave is flagged untrusted in 8.18 and comes back
  // only through this list.
  vips_block_untrusted_set(TRUE);
  vips_operation_block_set("VipsForeignLoad", TRUE);
  vips_operation_block_set("VipsForeignSave", TRUE);
  for (int i = 0; i < n_loaders; i++)
    vips_operation_block_set(allowed_loaders[i], FALSE);
  for (int i = 0; i < n_savers; i++)
    vips_operation_block_set(allowed_savers[i], FALSE);

  g_atomic_int_set(&xi_heic_encode, heic_encode ? 1 : 0);

  if (g_once_init_enter(&hooked)) {
    guint id = g_signal_lookup("invalidate", VIPS_TYPE_OPERATION);
    if (id)
      g_signal_add_emission_hook(id, 0, xi_invalidate_hook, NULL, NULL);
    g_once_init_leave(&hooked, 1);
  }

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

void
xi_leak_set(int leak)
{
  vips_leak_set(leak ? TRUE : FALSE);
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

int
xi_tracked_files(void)
{
  return vips_tracked_get_files();
}

static void *
xi_count_object(VipsObject *object, void *a, void *b)
{
  (void) object;
  (void) b;
  (*(int *) a)++;
  return NULL;
}

int
xi_live_objects(void)
{
  int n = 0;

  vips_object_map((VipsSListMap2Fn) xi_count_object, &n, NULL);
  return n;
}

void
xi_free(void *p)
{
  g_free(p);
}

// ---- source buffers --------------------------------------------------------

static int
xi_blob_free(void *data, void *area)
{
  (void) area;
  xi_sources_remove(data);
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
  xi_sources_insert(data);
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

VipsBlob *
xi_blob_ref(VipsBlob *blob)
{
  if (blob)
    vips_area_copy(VIPS_AREA(blob));
  return blob;
}

void
xi_blob_unref(VipsBlob *blob)
{
  if (blob)
    vips_area_unref(VIPS_AREA(blob));
}

int
xi_blob_failed(VipsBlob *blob)
{
  return blob && xi_sources_failed(VIPS_AREA(blob)->data);
}
