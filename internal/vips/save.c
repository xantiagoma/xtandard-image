// Hand-written C shim for internal/vips: savers. Every saver writes to a
// g_malloc'd buffer (vips_<fmt>save_buffer) with every option set
// explicitly; no option strings. See shim.h for the licence notice.

#include "shim.h"

#define XI_SAVE_FAIL() \
  do { \
    *err = xi_error_copy(); \
    return XI_ERR_SAVE; \
  } while (0)

static void
xi_save_reset(void **buf, size_t *len)
{
  *buf = NULL;
  *len = 0;
}

int
xi_jpegsave(VipsImage *in, const XiJpegOptions *o, int keep, void **buf, size_t *len,
    char **err)
{
  xi_save_reset(buf, len);
  if (vips_jpegsave_buffer(in, buf, len,
          "Q", o->quality,
          "interlace", o->interlace,
          "optimize_coding", o->optimize_coding,
          "subsample_mode", o->subsample,
          "restart_interval", o->restart_interval,
          "trellis_quant", o->trellis_quant,
          "overshoot_deringing", o->overshoot_deringing,
          "optimize_scans", o->optimize_scans,
          "quant_table", o->quant_table,
          "keep", keep,
          NULL))
    XI_SAVE_FAIL();
  return XI_OK;
}

int
xi_pngsave(VipsImage *in, const XiPngOptions *o, int keep, void **buf, size_t *len,
    char **err)
{
  int rc;

  xi_save_reset(buf, len);
  // bitdepth left unset lets the saver pick it from the image.
  if (o->bitdepth > 0)
    rc = vips_pngsave_buffer(in, buf, len,
        "compression", o->compression,
        "interlace", o->interlace,
        "filter", o->filter,
        "palette", o->palette,
        "Q", o->quality,
        "dither", o->dither,
        "effort", o->effort,
        "bitdepth", o->bitdepth,
        "keep", keep,
        NULL);
  else
    rc = vips_pngsave_buffer(in, buf, len,
        "compression", o->compression,
        "interlace", o->interlace,
        "filter", o->filter,
        "palette", o->palette,
        "Q", o->quality,
        "dither", o->dither,
        "effort", o->effort,
        "keep", keep,
        NULL);
  if (rc)
    XI_SAVE_FAIL();
  return XI_OK;
}

int
xi_webpsave(VipsImage *in, const XiWebpOptions *o, int keep, void **buf, size_t *len,
    char **err)
{
  xi_save_reset(buf, len);
  if (vips_webpsave_buffer(in, buf, len,
          "Q", o->quality,
          "lossless", o->lossless,
          "near_lossless", o->near_lossless,
          "effort", o->effort,
          "smart_subsample", o->smart_subsample,
          "preset", o->preset,
          "min_size", o->min_size,
          "kmin", o->kmin,
          "kmax", o->kmax,
          "alpha_q", o->alpha_quality,
          "mixed", o->mixed,
          "smart_deblock", o->smart_deblock,
          "exact", o->exact,
          "keep", keep,
          NULL))
    XI_SAVE_FAIL();
  return XI_OK;
}

int
xi_gifsave(VipsImage *in, const XiGifOptions *o, int keep, void **buf, size_t *len,
    char **err)
{
  xi_save_reset(buf, len);
  if (vips_gifsave_buffer(in, buf, len,
          "dither", o->dither,
          "effort", o->effort,
          "bitdepth", o->bitdepth,
          "interframe_maxerror", o->interframe_maxerror,
          "reuse", o->reuse,
          "interpalette_maxerror", o->interpalette_maxerror,
          "interlace", o->interlace,
          "keep_duplicate_frames", o->keep_duplicate_frames,
          "keep", keep,
          NULL))
    XI_SAVE_FAIL();
  return XI_OK;
}

int
xi_heifsave(VipsImage *in, const XiHeifOptions *o, int keep, void **buf, size_t *len,
    char **err)
{
  int rc;

  xi_save_reset(buf, len);
  // HEIC (HEVC) encoding is off unless XI_FORMATS_HEIC_ENCODE enabled it at
  // init (patent-encumbered codec; decode stays on).
  if (o->compression == VIPS_FOREIGN_HEIF_COMPRESSION_HEVC && !xi_heic_encode_enabled()) {
    vips_error("xi_heifsave", "HEVC encoding is disabled");
    *err = xi_error_copy();
    return XI_ERR_UNSUPPORTED;
  }
  if (o->bitdepth > 0)
    rc = vips_heifsave_buffer(in, buf, len,
        "Q", o->quality,
        "lossless", o->lossless,
        "effort", o->effort,
        "subsample_mode", o->subsample,
        "compression", o->compression,
        "bitdepth", o->bitdepth,
        "keep", keep,
        NULL);
  else
    rc = vips_heifsave_buffer(in, buf, len,
        "Q", o->quality,
        "lossless", o->lossless,
        "effort", o->effort,
        "subsample_mode", o->subsample,
        "compression", o->compression,
        "keep", keep,
        NULL);
  if (rc)
    XI_SAVE_FAIL();
  return XI_OK;
}

int
xi_jxlsave(VipsImage *in, const XiJxlOptions *o, int keep, void **buf, size_t *len,
    char **err)
{
  int rc;

  xi_save_reset(buf, len);
  // distance left unset lets the saver derive it from Q.
  if (o->distance > 0)
    rc = vips_jxlsave_buffer(in, buf, len,
        "distance", o->distance,
        "effort", o->effort,
        "lossless", o->lossless,
        "tier", o->tier,
        "keep", keep,
        NULL);
  else
    rc = vips_jxlsave_buffer(in, buf, len,
        "Q", o->quality,
        "effort", o->effort,
        "lossless", o->lossless,
        "tier", o->tier,
        "keep", keep,
        NULL);
  if (rc)
    XI_SAVE_FAIL();
  return XI_OK;
}

int
xi_tiffsave(VipsImage *in, const XiTiffOptions *o, int keep, void **buf, size_t *len,
    char **err)
{
  int rc;

  xi_save_reset(buf, len);
  if (o->bitdepth > 0)
    rc = vips_tiffsave_buffer(in, buf, len,
        "Q", o->quality,
        "compression", o->compression,
        "predictor", o->predictor,
        "lossless", o->lossless,
        "level", o->level,
        "bitdepth", o->bitdepth,
        "keep", keep,
        NULL);
  else
    rc = vips_tiffsave_buffer(in, buf, len,
        "Q", o->quality,
        "compression", o->compression,
        "predictor", o->predictor,
        "lossless", o->lossless,
        "level", o->level,
        "keep", keep,
        NULL);
  if (rc)
    XI_SAVE_FAIL();
  return XI_OK;
}
