// Hand-written C shim for internal/vips.
//
// Portions (init sequence, cache and concurrency setup, per-thread cleanup,
// save-to-buffer pattern) are derived from imgproxy's vips/vips.c,
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: reduced to the operations xtandard-image needs; error text is
// captured at the failure site with vips_error_buffer_copy; input bytes live
// in a refcounted VipsBlob of tracked C memory.

#ifndef XI_VIPS_SHIM_H
#define XI_VIPS_SHIM_H

#include <stdlib.h>
#include <vips/vips.h>

// Result codes of the xi_* functions. Anything but XI_OK sets *err to a
// g_malloc'd copy of the vips error buffer (possibly NULL) that the caller
// frees with xi_free.
enum {
  XI_OK = 0,
  XI_ERR_UNSUPPORTED = 1, // no loader matches, or the matching loader is blocked
  XI_ERR_LOAD = 2,        // the loader or thumbnail failed on this input
  XI_ERR_SAVE = 3,        // the saver failed
  XI_ERR_INTERNAL = 4,    // failure not attributable to the input
};

int xi_init(const char *argv0, int concurrency, int cache_max, size_t cache_max_mem,
    int cache_max_files, const char **allowed_loaders, int n_allowed, char **err);
void xi_shutdown(void);
void xi_thread_cleanup(void);

size_t xi_tracked_mem(void);
size_t xi_tracked_mem_highwater(void);
int xi_tracked_allocs(void);

// Input buffers: vips_tracked_malloc'd memory wrapped in a VipsBlob whose
// free function releases it. The blob refcount (vips_area) is the unit of
// ownership. xi_blob_grow returns a larger blob holding the first `used`
// bytes and drops the caller's reference to the old one.
VipsBlob *xi_blob_new(size_t capacity);
VipsBlob *xi_blob_grow(VipsBlob *blob, size_t used, size_t capacity);
void *xi_blob_data(VipsBlob *blob);
void xi_blob_set_length(VipsBlob *blob, size_t length);
void xi_blob_unref(VipsBlob *blob);

// Shrink-on-load thumbnail. On success the output image holds its own
// reference to blob, so the bytes stay alive while any node of the graph
// may still read them.
int xi_thumbnail(VipsBlob *blob, int width, int height, int size, int fail_on,
    VipsImage **out, char **err);

int xi_image_width(VipsImage *img);
int xi_image_height(VipsImage *img);
void xi_image_unref(VipsImage *img);

// JPEG save to a g_malloc'd buffer that the caller releases with xi_free.
int xi_jpegsave(VipsImage *in, int quality, void **buf, size_t *len, char **err);

void xi_free(void *p);

#endif // XI_VIPS_SHIM_H
