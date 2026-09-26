// Hand-written C shim for internal/vips.
//
// Portions (init sequence, cache and concurrency setup, per-thread cleanup,
// save-to-buffer pattern, flatten background scaling, trim preparation,
// per-frame processing of animated images) are derived from imgproxy's
// vips/vips.c, https://github.com/imgproxy/imgproxy, Copyright (c) 2017
// Sergey Alexandrovich, licensed under the Apache License, Version 2.0 (see
// /NOTICE). Modifications: reduced to the operations xtandard-image needs; one
// fixed-arity function per operation; options passed as plain structs; error
// text is captured at the failure site with vips_error_buffer_copy; input
// bytes live in a refcounted VipsBlob of tracked C memory whose decode
// failures are recorded for error attribution.

#ifndef XI_VIPS_SHIM_H
#define XI_VIPS_SHIM_H

#include <stdlib.h>
#include <vips/vips.h>

// Result codes of the xi_* functions. Anything but XI_OK sets *err to a
// g_malloc'd message (possibly NULL) that the caller frees with xi_free:
// the shim's own text (xi_error_new), or libvips' error buffer copied and
// cleared at the failure site (xi_error_copy). The text is a log attribute
// only; callers classify failures by the result code, never by the text.
enum {
  XI_OK = 0,
  XI_ERR_UNSUPPORTED = 1, // no loader matches, or the matching loader is blocked
  XI_ERR_LOAD = 2,        // the loader or thumbnail failed on this input
  XI_ERR_SAVE = 3,        // the saver (or a lazy upstream step) failed
  XI_ERR_INTERNAL = 4,    // failure not attributable to the input
  XI_ERR_LIMIT = 5,       // the header exceeds a configured limit
  XI_ERR_OP = 6,          // a processing operation failed
};

// ---- lifecycle (shim.c) ------------------------------------------------

int xi_init(const char *argv0, int concurrency, int cache_max, size_t cache_max_mem,
    int cache_max_files, const char **allowed_loaders, int n_loaders,
    const char **allowed_savers, int n_savers, int heic_encode, const char *settings,
    char **err);
int xi_shutdown(void);
int xi_run_begin(void);
void xi_run_end(void);
void xi_thread_cleanup(void);
void xi_leak_set(int leak);

size_t xi_tracked_mem(void);
size_t xi_tracked_mem_highwater(void);
int xi_tracked_allocs(void);
int xi_tracked_files(void);
int xi_live_objects(void);

void xi_free(void *p);

// ---- source buffers (shim.c) -------------------------------------------

// Input buffers: vips_tracked_malloc'd memory wrapped in a VipsBlob whose
// free function releases it. The blob refcount (vips_area) is the unit of
// ownership. xi_blob_grow returns a larger blob holding the first `used`
// bytes and drops the caller's reference to the old one.
//
// Every live blob is registered by its data pointer so that a loader that
// signals "invalidate" (a decode failure, possibly long after the header
// was read) marks it; xi_blob_failed then attributes a failed sink to the
// input instead of the saver.
VipsBlob *xi_blob_new(size_t capacity);
VipsBlob *xi_blob_grow(VipsBlob *blob, size_t used, size_t capacity);
void *xi_blob_data(VipsBlob *blob);
void xi_blob_set_length(VipsBlob *blob, size_t length);
VipsBlob *xi_blob_ref(VipsBlob *blob);
void xi_blob_unref(VipsBlob *blob);
int xi_blob_failed(VipsBlob *blob);

// ---- load (load.c) -----------------------------------------------------

typedef struct {
  int shrink;            // jpeg/uhdr shrink-on-load; <= 1 is off
  double scale;          // webp/svg scale; <= 0 is off
  double dpi;            // svg/pdf dpi; <= 0 is the loader default
  int page;              // first page/frame
  int n;                 // number of pages; -1 all, 0 loader default (1)
  int access;            // VipsAccess
  int fail_on;           // VipsFailOn
  int autorotate;        // apply and remove EXIF orientation after loading
  int heif_thumbnail;    // heif: load the embedded thumbnail
  long long max_pixels;  // > 0: width × total height limit
  int max_frames;        // > 0: loaded frame limit
  double background[4];  // pdf page background (0..255 per band)
  int n_background;      // bands in background; 0 is the loader default
} XiLoadOptions;

typedef struct {
  int width;
  int height;       // one page
  int bands;
  int band_format;  // VipsBandFormat
  int interpretation;
  int n_pages;      // pages in the file (raw n-pages, or 1)
  int orientation;  // 1..8
  int has_alpha;
  int has_icc;
  int bits;         // bits-per-sample, or derived from band_format
  int loop;         // -1 when absent
  int *delay;       // g_malloc'd copy, freed by the caller with xi_free
  int n_delay;
  char loader[48];  // vips-loader, e.g. "jpegload"
  char subformat[16]; // heif-compression, e.g. "av1" or "hevc"
} XiHeader;

typedef struct {
  int width;
  int height;
  int size;          // VipsSize
  int no_rotate;
  int crop;          // VipsInteresting
  int linear;
  int intent;        // VipsIntent
  int fail_on;       // VipsFailOn
  int page;
  int n;             // -1 all, 0 loader default
} XiThumbnailOptions;

// dpi > 0 measures vector pages (svg, pdf) at that resolution.
int xi_probe(VipsBlob *blob, double dpi, XiHeader *h, char **err);
int xi_load(VipsBlob *blob, const XiLoadOptions *o, VipsImage **out, char **err);
// xi_load_with is xi_load with the loader named instead of detected (a type
// name such as "VipsForeignLoadMagick7Buffer"); a blocked loader still fails,
// but the PSD-only restriction of ImageMagick is skipped. Tests use it to
// check ImageMagick's policy.xml behind that restriction.
int xi_load_with(const char *loader, VipsBlob *blob, const XiLoadOptions *o, VipsImage **out,
    char **err);
// import_profile and export_profile may be NULL (none); they are copied.
int xi_thumbnail(VipsBlob *blob, const XiThumbnailOptions *o, const char *import_profile,
    const char *export_profile, VipsImage **out, char **err);
// xi_spool_source returns the source blob data of a live OpenEXR spool path
// (load.c), or NULL; the invalidate hook (shim.c) attributes decode
// failures with it.
const void *xi_spool_source(const char *path);
int xi_black(int width, int height, int bands, VipsImage **out, char **err);
int xi_new_from_memory(const void *data, size_t size, int width, int height, int bands,
    int band_format, VipsImage **out, char **err);
// xi_new_from_blob wraps the blob's pixels without copying; the image keeps
// a reference to the blob until it closes.
int xi_new_from_blob(VipsBlob *blob, int width, int height, int bands, int band_format,
    VipsImage **out, char **err);

// ---- camera RAW (raw.c, LibRaw) ------------------------------------------

#define XI_RAW_MAX_THUMBS 8

typedef struct {
  int jpeg;             // LibRaw classifies it as a JPEG stream
  int width;            // as LibRaw's header parse reports it; may be 0
  int height;
  unsigned length;      // bytes at offset in the source buffer
  long long offset;
} XiRawThumb;

typedef struct {
  int width;            // demosaiced size (half_size off), stored orientation
  int height;
  int raw_width;        // sensor data size
  int raw_height;
  int flip;             // LibRaw sizes.flip (dcraw convention)
  int colors;           // colours of the sensor filter (1 = monochrome)
  int dng;
  float iso;
  float shutter;        // seconds
  float aperture;       // f-number
  float focal_len;      // mm
  int year, month, day, hour, minute, second; // capture time; year 0 = unknown
  char make[64];
  char model[64];
  char software[64];
  char lens[128];
  int n_thumbs;
  XiRawThumb thumbs[XI_RAW_MAX_THUMBS];
} XiRawHeader;

typedef struct {
  int half_size;        // LibRaw half-size demosaic (each side halved)
  long long max_pixels; // > 0: sensor width × height limit
  int *cancel;          // C memory, set to non-zero by xi_raw_cancel; may be NULL
} XiRawOptions;

// Opens the buffer with libraw_open_buffer and reads the header and the
// thumbnail list; nothing is unpacked.
int xi_raw_probe(VipsBlob *blob, XiRawHeader *h, char **err);
// Unpacks and demosaics to an 8-bit sRGB (or grey) image that owns LibRaw's
// output buffer.
int xi_raw_load(VipsBlob *blob, const XiRawOptions *o, VipsImage **out, char **err);
void xi_raw_cancel(int *flag);
// Live LibRaw handles plus live demosaiced buffers (leak tests).
int xi_raw_live(void);

// ---- image accessors and metadata (image.c) ------------------------------

int xi_image_width(VipsImage *img);
int xi_image_height(VipsImage *img);
int xi_image_bands(VipsImage *img);
int xi_image_band_format(VipsImage *img);
int xi_image_interpretation(VipsImage *img);
int xi_image_has_alpha(VipsImage *img);
int xi_image_orientation(VipsImage *img);
int xi_image_page_height(VipsImage *img);
double xi_image_xres(VipsImage *img);
double xi_image_yres(VipsImage *img);
size_t xi_image_sizeof(VipsImage *img);
void xi_image_unref(VipsImage *img);
void xi_image_ref(VipsImage *img);
void xi_image_set_kill(VipsImage *img, int kill);

int xi_meta_get_int(VipsImage *img, const char *name, int *out);
int xi_meta_get_blob(VipsImage *img, const char *name, const void **data, size_t *len);
int xi_meta_get_array_int(VipsImage *img, const char *name, int **out, int *n);
int xi_meta_set_int(VipsImage *in, const char *name, int v, VipsImage **out, char **err);
int xi_meta_set_blob(VipsImage *in, const char *name, const void *data, size_t len,
    VipsImage **out, char **err);
int xi_meta_set_array_int(VipsImage *in, const char *name, const int *v, int n,
    VipsImage **out, char **err);
// names and values each hold n NUL-terminated strings back to back; every
// value is copied.
int xi_meta_set_strings(VipsImage *in, const char *names, const char *values, int n,
    VipsImage **out, char **err);
// names holds n NUL-terminated names back to back.
int xi_meta_remove(VipsImage *in, const char *names, int n, VipsImage **out, char **err);

int xi_meta_get_string(VipsImage *img, const char *name, const char **out);
// Field names back to back, each NUL-terminated, in a g_malloc'd buffer of
// *len bytes (NULL and 0 when the image has none); free with xi_free.
char *xi_meta_fields(VipsImage *img, size_t *len);

int xi_write_to_memory(VipsImage *in, void **data, size_t *len, char **err);
// Renders in to a memory image (vips_image_copy_memory); XI_ERR_SAVE on
// failure, like the other sinks.
int xi_copy_memory(VipsImage *in, VipsImage **out, char **err);
// Renders in to a fresh memory image with in's size, bands, format and
// interpretation but no metadata and no link to in's pipeline (vips.Share);
// XI_ERR_SAVE when rendering fails.
int xi_share(VipsImage *in, VipsImage **out, char **err);

// ---- operations (ops.c) ------------------------------------------------
//
// Every function returns XI_OK or XI_ERR_OP. Functions marked "per frame"
// treat a multi-page image (page-height) as frames and fix page-height on
// the output.

int xi_resize(VipsImage *in, double hscale, double vscale, int kernel, VipsImage **out,
    char **err); // keeps page-height consistent
int xi_extract_area(VipsImage *in, int left, int top, int width, int height,
    VipsImage **out, char **err);
int xi_crop(VipsImage *in, int left, int top, int width, int height, VipsImage **out,
    char **err); // per frame
int xi_embed(VipsImage *in, int x, int y, int width, int height, int extend,
    const double *bg, int nbg, VipsImage **out, char **err); // per frame
int xi_gravity(VipsImage *in, int direction, int width, int height, int extend,
    const double *bg, int nbg, VipsImage **out, char **err); // per frame
int xi_flip(VipsImage *in, int direction, VipsImage **out, char **err); // per frame
int xi_rot(VipsImage *in, int angle, VipsImage **out, char **err);      // per frame
int xi_autorot(VipsImage *in, VipsImage **out, char **err);              // per frame
int xi_smartcrop(VipsImage *in, int width, int height, int interesting, VipsImage **out,
    char **err);
int xi_flatten(VipsImage *in, const double *bg, int nbg, VipsImage **out, char **err);
int xi_premultiply(VipsImage *in, VipsImage **out, char **err);
int xi_unpremultiply(VipsImage *in, VipsImage **out, char **err);
int xi_colourspace(VipsImage *in, int interpretation, VipsImage **out, char **err);
int xi_icc_import(VipsImage *in, const char *profile, int intent, int embedded,
    VipsImage **out, char **err);
int xi_icc_export(VipsImage *in, const char *profile, int intent, int depth,
    VipsImage **out, char **err);
int xi_icc_transform(VipsImage *in, const char *profile, int intent, int embedded,
    int depth, VipsImage **out, char **err);
int xi_profile_load(const char *name, VipsBlob **blob, const void **data, size_t *len,
    char **err);
int xi_cast(VipsImage *in, int band_format, VipsImage **out, char **err);
int xi_copy(VipsImage *in, int interpretation, double xres, double yres, int page_height,
    VipsImage **out, char **err);
int xi_gaussblur(VipsImage *in, double sigma, VipsImage **out, char **err);
int xi_sharpen(VipsImage *in, double sigma, double x1, double y2, double y3, double m1,
    double m2, VipsImage **out, char **err);
int xi_shrink(VipsImage *in, int hshrink, int vshrink, VipsImage **out, char **err);
int xi_composite2(VipsImage *base, VipsImage *overlay, int mode, int x, int y,
    VipsImage **out, char **err);
int xi_replicate(VipsImage *in, int across, int down, VipsImage **out, char **err);
int xi_join(VipsImage *a, VipsImage *b, int direction, VipsImage **out, char **err);
int xi_arrayjoin(VipsImage **in, int n, int across, VipsImage **out, char **err);
int xi_linear(VipsImage *in, const double *a, const double *b, int n, VipsImage **out,
    char **err);
int xi_invert(VipsImage *in, VipsImage **out, char **err);
int xi_bandjoin(VipsImage **in, int n, VipsImage **out, char **err);
int xi_bandjoin_const(VipsImage *in, const double *c, int n, VipsImage **out, char **err);
int xi_extract_band(VipsImage *in, int band, int n, VipsImage **out, char **err);
int xi_addalpha(VipsImage *in, VipsImage **out, char **err);
int xi_find_trim(VipsImage *in, double threshold, const double *bg, int nbg, int line_art,
    int *left, int *top, int *width, int *height, char **err);
int xi_similarity(VipsImage *in, double scale, double angle, const double *bg, int nbg,
    VipsImage **out, char **err); // per frame
int xi_affine(VipsImage *in, double a, double b, double c, double d, const double *bg,
    int nbg, VipsImage **out, char **err); // per frame

// Colour operations (WP-59). Results keep the input's band format: float
// intermediates are rounded to nearest, then clamped by the cast.
int xi_cast_round(VipsImage *in, int band_format, VipsImage **out, char **err);
int xi_maplut(VipsImage *in, VipsImage *lut, VipsImage **out, char **err);
int xi_recomb(VipsImage *in, const double *m, int rows, int cols, VipsImage **out,
    char **err);
int xi_mix(VipsImage *a, VipsImage *b, double t, VipsImage **out, char **err);
int xi_gradient_mask(int width, int height, double ax, double ay, double b, double opacity,
    VipsImage **out, char **err);

// Filter extras (WP-61), in the input's band format.
int xi_conv3(VipsImage *in, const double *m, VipsImage **out, char **err);
int xi_unsharp(VipsImage *in, double sigma, double amount, VipsImage **out, char **err);
int xi_insert(VipsImage *main, VipsImage *sub, int x, int y, VipsImage **out, char **err);
int xi_blend_mask(VipsImage *mask, VipsImage *a, VipsImage *b, VipsImage **out,
    char **err);

// Text (WP-78): vips_text with RGBA output, wrapped at width (word, then
// character boundaries). height > 0 autofits: the DPI is searched so the
// text fits width×height (*fit_dpi reports it) and dpi is ignored.
// fontfile, when non-NULL, is registered with fontconfig first.
int xi_text(const char *text, const char *font, const char *fontfile, int width, int height,
    int dpi, VipsImage **out, int *fit_dpi, char **err);

// HDR operations (WP-67): float arithmetic for tone mapping.
int xi_decode(VipsImage *in, VipsImage **out, char **err); // Radiance/LabQ coding → float
int xi_clamp(VipsImage *in, double min, double max, VipsImage **out, char **err);
int xi_multiply(VipsImage *a, VipsImage *b, VipsImage **out, char **err);
int xi_divide(VipsImage *a, VipsImage *b, VipsImage **out, char **err);

// ---- save (save.c) -----------------------------------------------------
//
// Every saver writes to a g_malloc'd buffer that the caller releases with
// xi_free and returns XI_OK or XI_ERR_SAVE (XI_ERR_UNSUPPORTED for a
// disabled encoder).

typedef struct {
  int quality;
  int interlace;
  int optimize_coding;
  int subsample; // VipsForeignSubsample
  int restart_interval;
  // mozjpeg extensions: libvips ignores them (with a warning, only when
  // set) when built against libjpeg-turbo.
  int trellis_quant;
  int overshoot_deringing;
  int optimize_scans;
  int quant_table; // 0..8
} XiJpegOptions;

typedef struct {
  int compression;
  int interlace;
  int filter; // VipsForeignPngFilter
  int palette;
  int quality;
  double dither;
  int bitdepth; // 0: from the image
  int effort;
} XiPngOptions;

typedef struct {
  int quality;
  int lossless;
  int near_lossless;
  int effort;
  int smart_subsample;
  int preset; // VipsForeignWebpPreset
  int min_size;
  int kmin;
  int kmax;
  int alpha_quality;
  int mixed;
  int smart_deblock;
  int exact;
} XiWebpOptions;

typedef struct {
  double dither;
  int effort;
  int bitdepth;
  double interframe_maxerror;
  int reuse;
  double interpalette_maxerror;
  int interlace;
  int keep_duplicate_frames;
} XiGifOptions;

typedef struct {
  int quality;
  int lossless;
  int effort;
  int subsample; // VipsForeignSubsample
  int bitdepth;  // 0: from the image
  int compression; // VipsForeignHeifCompression
} XiHeifOptions;

typedef struct {
  int quality;
  double distance; // > 0 overrides quality
  int effort;
  int lossless;
  int tier;
} XiJxlOptions;

typedef struct {
  int quality;
  int compression; // VipsForeignTiffCompression
  int predictor;   // VipsForeignTiffPredictor
  int lossless;
  int level;       // deflate/zstd level; 0 default
  int bitdepth;    // 0: from the image
} XiTiffOptions;

int xi_jpegsave(VipsImage *in, const XiJpegOptions *o, int keep, void **buf, size_t *len,
    char **err);
int xi_pngsave(VipsImage *in, const XiPngOptions *o, int keep, void **buf, size_t *len,
    char **err);
int xi_webpsave(VipsImage *in, const XiWebpOptions *o, int keep, void **buf, size_t *len,
    char **err);
int xi_gifsave(VipsImage *in, const XiGifOptions *o, int keep, void **buf, size_t *len,
    char **err);
int xi_heifsave(VipsImage *in, const XiHeifOptions *o, int keep, void **buf, size_t *len,
    char **err);
int xi_jxlsave(VipsImage *in, const XiJxlOptions *o, int keep, void **buf, size_t *len,
    char **err);
int xi_tiffsave(VipsImage *in, const XiTiffOptions *o, int keep, void **buf, size_t *len,
    char **err);

// ---- EXIF tag titles (libexif, exif.c) ----------------------------------------

// Returns libexif's static human-readable title of the tag that libvips
// names `name` in IFD `ifd` (the "exif-ifd<ifd>-<name>" fields), or NULL
// when libexif does not know that tag in that IFD.
const char *xi_exif_title(const char *name, int ifd);

// ---- internal helpers shared by the .c files ------------------------------

char *xi_error_copy(void);
char *xi_error_new(const char *domain, const char *fmt, ...) G_GNUC_PRINTF(2, 3);
int xi_heic_encode_enabled(void);

#endif // XI_VIPS_SHIM_H
