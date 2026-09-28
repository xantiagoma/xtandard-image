// Hand-written C shim for internal/vips: probe, load, thumbnail and
// synthetic images. See shim.h for the licence notice.

// memfd_create (Linux): the OpenEXR spool lives in memory, not in /tmp.
#define _GNU_SOURCE

#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#ifdef __linux__
#include <sys/mman.h>
#endif

#include <glib/gstdio.h>

#include "shim.h"

#define XI_SRC_BLOB_KEY "xi-src-blob"
#define XI_SPOOL_KEY "xi-spool"

// OpenEXR has only a file loader (openexrload): the buffer is spooled to a
// file it can open. Radiance files headed "#?RGBE" instead of "#?RADIANCE"
// are loaded by radload_buffer named directly, whose header parser skips
// that line either way (libvips' sniffer insists on "#?RADIANCE").
#define XI_EXR_LOADER "VipsForeignLoadOpenexr"
#define XI_RAD_LOADER "VipsForeignLoadRadBuffer"

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

// xi_is_pnm reports whether d starts with a portable anymap header that
// ppmload reads: PBM, PGM or PPM, ASCII or binary (P1-P6), or PFM (PF, Pf),
// followed by whitespace (FORMATS.md §1.4). PAM (P7) goes to ImageMagick.
static gboolean
xi_is_pnm(const unsigned char *d, size_t n)
{
  return n >= 3 && d[0] == 'P' &&
      ((d[1] >= '1' && d[1] <= '6') || d[1] == 'F' || d[1] == 'f') &&
      (d[2] == ' ' || d[2] == '\t' || d[2] == '\n' || d[2] == '\r');
}

static guint
xi_le16(const unsigned char *d)
{
  return d[0] | (guint) d[1] << 8;
}

// xi_is_tga mirrors codec.sniffTGA: TGA has no signature, so the 18-byte
// header must be self-consistent (colour map, image type, depth,
// descriptor) and the file must not be an ISO BMFF one.
static gboolean
xi_is_tga(const unsigned char *d, size_t n)
{
  guint cmap, type, cmap_len, cmap_bits, depth, desc;

  if (n < 18 || memcmp(d + 4, "ftyp", 4) == 0)
    return FALSE;
  cmap = d[1];
  type = d[2];
  depth = d[16];
  desc = d[17];
  if (cmap > 1)
    return FALSE;
  cmap_len = xi_le16(d + 5);
  cmap_bits = d[7];
  if (xi_le16(d + 12) == 0 || xi_le16(d + 14) == 0 || (desc & 0xC0) != 0 || (desc & 0x0F) > 8)
    return FALSE;
  switch (type) {
  case 1:
  case 9: // colour-mapped
    return cmap == 1 && cmap_len > 0 &&
        (cmap_bits == 15 || cmap_bits == 16 || cmap_bits == 24 || cmap_bits == 32) &&
        (depth == 8 || depth == 16);
  case 2:
  case 10: // true colour
    return cmap == 0 && (depth == 15 || depth == 16 || depth == 24 || depth == 32);
  case 3:
  case 11: // grey
    return cmap == 0 && (depth == 8 || depth == 16);
  default:
    return FALSE;
  }
}

// xi_is_magick_input reports whether d starts like a format ImageMagick is
// allowed to read (magick-policy.xml): the same checks as codec.Sniff for
// PSD/PSB, DDS, QOI, PCX, SGI, Sun raster, XBM, XPM, PAM, GIMP XCF and TGA.
static gboolean
xi_is_magick_input(const unsigned char *d, size_t n)
{
  if (n >= 4 && (memcmp(d, "8BPS", 4) == 0 || memcmp(d, "DDS ", 4) == 0 ||
                    memcmp(d, "qoif", 4) == 0 || memcmp(d, "\x59\xa6\x6a\x95", 4) == 0))
    return TRUE;
  if (n >= 4 && d[0] == 0x0A && (d[1] == 2 || d[1] == 5) && d[2] <= 1 &&
      (d[3] == 1 || d[3] == 2 || d[3] == 4 || d[3] == 8))
    return TRUE; // PCX
  if (n >= 4 && d[0] == 0x01 && d[1] == 0xDA && d[2] <= 1 && (d[3] == 1 || d[3] == 2))
    return TRUE; // SGI
  if (n >= 8 && memcmp(d, "#define ", 8) == 0)
    return TRUE; // XBM
  if (n >= 9 && memcmp(d, "/* XPM */", 9) == 0)
    return TRUE;
  if (n >= 14 && memcmp(d, "gimp xcf ", 9) == 0 && d[13] == '\0')
    return TRUE; // XCF (WP-107): one layer per load, flattened in Go
  if (n >= 3 && d[0] == 'P' && d[1] == '7' && (d[2] == '\n' || d[2] == '\r' || d[2] == ' '))
    return TRUE; // PAM
  return xi_is_tga(d, n);
}

// xi_is_rgbe reports a Radiance file whose first line is "#?RGBE".
static gboolean
xi_is_rgbe(const unsigned char *d, size_t n)
{
  return n >= 7 && memcmp(d, "#?RGBE", 6) == 0 && (d[6] == '\n' || d[6] == '\r');
}

// xi_check_loader refuses a blocked loader, ImageMagick for anything but
// the formats it is in the image for (PSD/PSB and the legacy formats of
// FORMATS.md; its policy.xml denies every other coder as well) and the PPM
// loader for anything but a PNM or PFM header.
static int
xi_check_loader(const char *loader, VipsBlob *blob, char **err)
{
  VipsArea *area = VIPS_AREA(blob);

  // The libvips sniffer already skips blocked loaders; this is a second
  // line of defence should that ever change.
  if (xi_loader_blocked(loader)) {
    *err = xi_error_new("xi_load", "loader %s is blocked", loader);
    return XI_ERR_UNSUPPORTED;
  }
  if (xi_is_magick(loader) && !xi_is_magick_input(area->data, area->length)) {
    *err = xi_error_new("xi_load", "%s is restricted to PSD/PSB, XCF and the legacy formats", loader);
    return XI_ERR_UNSUPPORTED;
  }
  if (g_str_has_prefix(loader, "VipsForeignLoadPpm") && !xi_is_pnm(area->data, area->length)) {
    *err = xi_error_new("xi_load", "%s is restricted to PNM/PFM", loader);
    return XI_ERR_UNSUPPORTED;
  }
  return XI_OK;
}

// xi_load_error reports a failed loader run as XI_ERR_LOAD with libvips'
// text copied (and cleared) right here, at the failure site. Nothing is
// classified by that text: the buffer is process-global, so under
// concurrency it may hold another thread's message or none. A PDF that needs
// a password is recognised on the Go side from the document's trailer
// (pdfEncrypted), not from PDFium's message.
static int
xi_load_error(char **err)
{
  *err = xi_error_copy();
  return XI_ERR_LOAD;
}

// xi_find_loader sniffs the loader class name for blob. It returns
// XI_ERR_UNSUPPORTED with *err set when nothing enabled matches.
static int
xi_find_loader(VipsBlob *blob, const char **loader, char **err)
{
  VipsArea *area = VIPS_AREA(blob);

  // The buffer sniffer never considers openexrload (a file loader) and
  // wants "#?RADIANCE" for Radiance: both are recognised here first.
  if (area->length >= 4 && memcmp(area->data, "v/1\x01", 4) == 0)
    *loader = XI_EXR_LOADER;
  else if (xi_is_rgbe(area->data, area->length))
    *loader = XI_RAD_LOADER;
  else if (!(*loader = vips_foreign_find_load_buffer(area->data, area->length))) {
    // The sniffer's own message is dropped: the shim states the failure,
    // so the text never depends on the shared buffer.
    vips_error_clear();
    *err = xi_error_new("xi_load", "buffer is not in a known format");
    return XI_ERR_UNSUPPORTED;
  }
  return xi_check_loader(*loader, blob, err);
}

// ---- OpenEXR spool ---------------------------------------------------------
//
// openexrload reads a file by name, lazily: the file must outlive every
// image derived from the load. The spool is attached to the loaded image and
// closed (a temporary file also unlinked) when that image is finalised. On
// Linux it is a memfd opened through /proc/self/fd, so nothing touches the
// filesystem; elsewhere a file in g_get_tmp_dir().
//
// The registry maps a spool's path to the source blob's data pointer, so a
// decode failure (the invalidate hook sees the loader's "filename", not a
// "buffer") is attributed to the source like any buffer load.

typedef struct {
  int fd;
  char *path;
  gboolean temp;    // a named temporary file: unlink when done
  const void *data; // the source blob's data (registry value)
} XiSpool;

static GMutex xi_spools_lock;
static GHashTable *xi_spools = NULL; // path -> source data pointer

static void
xi_spool_free(XiSpool *s)
{
  g_mutex_lock(&xi_spools_lock);
  if (xi_spools && s->path)
    g_hash_table_remove(xi_spools, s->path);
  g_mutex_unlock(&xi_spools_lock);
  if (s->fd >= 0)
    close(s->fd);
  if (s->temp && s->path)
    g_unlink(s->path);
  g_free(s->path);
  g_free(s);
}

static XiSpool *
xi_spool_new(VipsBlob *blob)
{
  VipsArea *area = VIPS_AREA(blob);
  const char *p = area->data;
  size_t left = area->length;
  XiSpool *s = g_new0(XiSpool, 1);

  s->fd = -1;
  s->data = area->data;
#ifdef __linux__
  if ((s->fd = memfd_create("xi-exr", MFD_CLOEXEC)) >= 0)
    s->path = g_strdup_printf("/proc/self/fd/%d", s->fd);
#endif
  if (s->fd < 0) {
    GError *gerr = NULL;

    if ((s->fd = g_file_open_tmp("xi-XXXXXX.exr", &s->path, &gerr)) < 0) {
      vips_error("xi_load", "spool: %s", gerr ? gerr->message : "cannot create");
      g_clear_error(&gerr);
      xi_spool_free(s);
      return NULL;
    }
    s->temp = TRUE;
  }
  while (left > 0) {
    ssize_t n = write(s->fd, p, left);

    if (n < 0 && errno == EINTR)
      continue;
    if (n <= 0) {
      vips_error_system(errno, "xi_load", "spool write");
      xi_spool_free(s);
      return NULL;
    }
    p += n;
    left -= (size_t) n;
  }
  g_mutex_lock(&xi_spools_lock);
  if (!xi_spools)
    xi_spools = g_hash_table_new(g_str_hash, g_str_equal);
  g_hash_table_insert(xi_spools, s->path, (gpointer) s->data);
  g_mutex_unlock(&xi_spools_lock);
  return s;
}

const void *
xi_spool_source(const char *path)
{
  const void *data = NULL;

  g_mutex_lock(&xi_spools_lock);
  if (xi_spools && path)
    data = g_hash_table_lookup(xi_spools, path);
  g_mutex_unlock(&xi_spools_lock);
  return data;
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

// ---- decode guard --------------------------------------------------------
//
// libvips signals "invalidate" on a loader (shim.c's hook marks the source
// failed) only for some failures: a truncated file, a failed load start. A
// decoder that fails while a region is computed returns -1 from its
// generate function and nothing else: libjpeg refusing 12-bit data under
// sequential access, for example. The sink then fails with the saver's
// name and would be taken for our fault (500). xi_guard puts a
// pass-through image on a load's output whose generate marks the source
// failed when the region below it cannot be computed, so such a failure is
// the input's (Corrupt, 422) whichever loader it was. Pixels are shared
// with the region below (vips_region_region), as vips_copy does.

static int
xi_guard_gen(VipsRegion *out_region, void *seq, void *a, void *b, gboolean *stop)
{
  VipsRegion *ir = (VipsRegion *) seq;
  VipsRect *r = &out_region->valid;

  (void) a;
  (void) stop;
  if (vips_region_prepare(ir, r) || vips_region_region(out_region, ir, r, r->left, r->top)) {
    xi_blob_mark_failed((VipsBlob *) b);
    return -1;
  }
  return 0;
}

// xi_guard replaces *img (a load's output, whose reference it takes) with
// the guard over it. blob is the source; the caller keeps it alive at least
// as long as the returned image (xi_keep_blob).
static int
xi_guard(VipsImage **img, VipsBlob *blob)
{
  VipsImage *in = *img;
  VipsImage *out = vips_image_new();

  if (vips_image_pipelinev(out, in->dhint, in, NULL) ||
      vips_image_generate(out, vips_start_one, xi_guard_gen, vips_stop_one, in, blob)) {
    g_object_unref(out);
    return -1;
  }
  // out owns the caller's reference to in: the load lives as long as out.
  g_object_set_data_full(G_OBJECT(out), "xi-guarded", in, g_object_unref);
  *img = out;
  return 0;
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

// xi_set_password hands password to op's "password" property without an
// intermediate heap copy (a static-string GValue), so the only copy libvips
// makes is the loader's own (VIPS_SETSTR), which xi_wipe_password clears.
static void
xi_set_password(VipsOperation *op, const char *password)
{
  GValue v = G_VALUE_INIT;

  g_value_init(&v, G_TYPE_STRING);
  g_value_set_static_string(&v, password);
  g_object_set_property(G_OBJECT(op), "password", &v);
  g_value_unset(&v);
}

// xi_wipe_password zeroes the loader's copy of the password. PDFium reads it
// only while the loader is built (FPDF_LoadCustomDocument), so the loader
// never needs it again, even while it renders pages lazily.
static void
xi_wipe_password(VipsOperation *op)
{
  GParamSpec *pspec;
  VipsArgumentClass *aclass;
  VipsArgumentInstance *ainst;
  char *s;

  if (vips_object_get_argument(VIPS_OBJECT(op), "password", &pspec, &aclass, &ainst))
    return;
  if ((s = G_STRUCT_MEMBER(char *, op, aclass->offset)))
    xi_wipe(s, strlen(s));
}

// xi_run_loader builds loader over blob with the options it supports.
static int
xi_run_loader(const char *loader, VipsBlob *blob, const XiLoadOptions *o, VipsImage **out)
{
  VipsOperation *op;
  XiSpool *spool = NULL;
  gboolean password;
  int rc;

  if (!(op = vips_operation_new(loader)))
    return -1;
  if (strcmp(loader, XI_EXR_LOADER) == 0) {
    if (!(spool = xi_spool_new(blob))) {
      g_object_unref(op);
      return -1;
    }
    g_object_set(op, "filename", spool->path, NULL);
  } else
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
  password = o->password && o->password[0] && xi_has_prop(op, "password");
  if (password) {
    xi_set_password(op, o->password);
    // xi_build drops the caller's reference: keep one to wipe the copy
    // afterwards, whether the build succeeded or not.
    g_object_ref(op);
  }
  rc = xi_build(op, out);
  if (password) {
    xi_wipe_password(op);
    g_object_unref(op);
  }
  if (rc) {
    if (spool)
      xi_spool_free(spool);
    return -1;
  }
  if (spool)
    g_object_set_data_full(G_OBJECT(*out), XI_SPOOL_KEY, spool,
        (GDestroyNotify) xi_spool_free);
  return 0;
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
xi_probe(VipsBlob *blob, double dpi, const char *password, XiHeader *h, char **err)
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
  o.password = password;
  if (xi_run_loader(loader, blob, &o, &img))
    return xi_load_error(err);

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
  if (!h->subformat[0])
    xi_copy_string(img, "magick-format", h->subformat, sizeof(h->subformat));

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
    *err = xi_error_new("xi_load", "no loader %s", loader);
    return XI_ERR_UNSUPPORTED;
  }
  if (xi_loader_blocked(loader)) {
    *err = xi_error_new("xi_load", "loader %s is blocked", loader);
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
    return xi_load_error(err);

  // Limits are checked on the header, before any pixel is decoded.
  frames = img->Ysize / vips_image_get_page_height(img);
  if (o->max_frames > 0 && frames > o->max_frames) {
    *err = xi_error_new("xi_load", "%d frames exceed the limit of %d", frames, o->max_frames);
    g_object_unref(img);
    return XI_ERR_LIMIT;
  }
  if (o->max_pixels > 0 && (long long) img->Xsize * img->Ysize > o->max_pixels) {
    *err = xi_error_new("xi_load", "%dx%d pixels exceed the limit of %lld", img->Xsize, img->Ysize,
        o->max_pixels);
    g_object_unref(img);
    return XI_ERR_LIMIT;
  }

  if (xi_guard(&img, blob)) {
    g_object_unref(img);
    *err = xi_error_copy();
    return XI_ERR_INTERNAL;
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

// xi_thumbnail_password is xi_thumbnail for a document opened with a
// password: the loader runs directly (xi_run_loader passes and then wipes the
// password) and thumbnail_image resizes its output. thumbnail_buffer would
// need the password in its option string, a copy nobody wipes. No
// shrink-on-load: PDFium renders at the loader's default density.
static int
xi_thumbnail_password(const char *loader, VipsBlob *blob, const XiThumbnailOptions *o,
    const char *import_profile, const char *export_profile, VipsImage **out, char **err)
{
  XiLoadOptions lo = { 0 };
  VipsImage *in = NULL;
  VipsOperation *op;

  lo.access = VIPS_ACCESS_SEQUENTIAL;
  lo.fail_on = o->fail_on;
  lo.page = o->page;
  lo.n = o->n;
  lo.password = o->password;
  if (xi_run_loader(loader, blob, &lo, &in))
    return xi_load_error(err);
  if (!(op = vips_operation_new("thumbnail_image"))) {
    g_object_unref(in);
    *err = xi_error_copy();
    return XI_ERR_INTERNAL;
  }
  g_object_set(op,
      "in", in,
      "width", o->width,
      "height", o->height,
      "size", o->size,
      "no_rotate", o->no_rotate,
      "crop", o->crop,
      "linear", o->linear,
      "intent", o->intent,
      NULL);
  g_object_unref(in); // op holds its own reference
  if (xi_has_prop(op, "fail_on"))
    g_object_set(op, "fail_on", o->fail_on, NULL);
  if (import_profile)
    g_object_set(op, "input_profile", import_profile, NULL);
  if (export_profile)
    g_object_set(op, "output_profile", export_profile, NULL);
  if (xi_build(op, out))
    return xi_load_error(err);
  if (xi_guard(out, blob)) {
    g_object_unref(*out);
    *out = NULL;
    *err = xi_error_copy();
    return XI_ERR_INTERNAL;
  }
  xi_keep_blob(*out, blob);
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
  if (strcmp(loader, XI_EXR_LOADER) == 0) {
    *err = xi_error_new("xi_thumbnail", "OpenEXR has no buffer loader");
    return XI_ERR_UNSUPPORTED;
  }
  if (o->password && o->password[0] && xi_class_has_prop(loader, "password"))
    return xi_thumbnail_password(loader, blob, o, import_profile, export_profile, out, err);

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
    return xi_load_error(err);

  // The guard sits on the resized output: a failure there is the loader's
  // in practice (the shrink and resize run on valid pixels).
  if (xi_guard(out, blob)) {
    g_object_unref(*out);
    *out = NULL;
    *err = xi_error_copy();
    return XI_ERR_INTERNAL;
  }
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

static void
xi_blob_image_close(VipsImage *img, gpointer blob)
{
  (void) img;
  vips_area_unref(VIPS_AREA(blob));
}

int
xi_new_from_blob(VipsBlob *blob, int width, int height, int bands, int band_format,
    VipsImage **out, char **err)
{
  VipsArea *area = VIPS_AREA(blob);

  // No copy: the image reads the blob's memory and holds a reference to the
  // blob until it closes (the pixels outlive every Go handle).
  if (!(*out = vips_image_new_from_memory(area->data, area->length, width, height, bands,
            band_format))) {
    *err = xi_error_copy();
    return XI_ERR_OP;
  }
  vips_area_copy(area);
  g_signal_connect(*out, "close", G_CALLBACK(xi_blob_image_close), blob);
  if (band_format == VIPS_FORMAT_USHORT)
    (*out)->Type = bands >= 3 ? VIPS_INTERPRETATION_RGB16 : VIPS_INTERPRETATION_GREY16;
  else
    (*out)->Type = bands >= 3 ? VIPS_INTERPRETATION_sRGB : VIPS_INTERPRETATION_B_W;
  return XI_OK;
}
