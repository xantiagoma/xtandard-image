/*
 * xi-metafile2svg: Windows metafiles (WMF, EMF) to SVG for the gpl image (WP-108).
 *
 * Copyright (c) 2026 Santiago Montoya and the xtandard-image contributors.
 * SPDX-License-Identifier: MIT
 *
 * This source is MIT. The program as built links libwmf (LGPL-2.0-or-later) and
 * libemf2svg with its vendored libUEMF (GPL-2.0), so the binary in the gpl image is a
 * GPL-2.0 work (docker/base/build-gpl-metafile.sh, LICENSE-GPL.md).
 *
 * The server runs it in the same sandbox as Ghostscript (internal/sandbox): one process
 * per source, the metafile on stdin, one SVG document on stdout, no file read or
 * written but libwmf's fonts and fontmap. The SVG then goes through the server's SVG
 * sanitiser and librsvg like any other SVG.
 *
 *   xi-metafile2svg --version
 *   xi-metafile2svg wmf|emf <max-input-bytes> <max-side-px> < in > out.svg
 *
 * Exit status: 0 converted; 1 the converter refused the input; 2 usage or input
 * errors (empty, too large).
 */

#include <errno.h>
#include <limits.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <emf2svg.h>
#include <libwmf/api.h>
#include <libwmf/svg.h>

#ifndef XI_METAFILE_VERSION
#define XI_METAFILE_VERSION "unknown"
#endif

/* read_all reads stdin into a malloc'd buffer of at most max bytes. */
static unsigned char *
read_all(size_t max, size_t *len)
{
  size_t cap = 1 << 16, n = 0;
  unsigned char *buf = malloc(cap);

  if (!buf)
    return NULL;
  for (;;) {
    ssize_t r;

    if (n == cap) {
      unsigned char *p;

      if (cap >= max + 1) {
        free(buf);
        errno = EFBIG;
        return NULL;
      }
      cap = cap * 2 > max + 1 ? max + 1 : cap * 2;
      if (!(p = realloc(buf, cap))) {
        free(buf);
        return NULL;
      }
      buf = p;
    }
    r = read(STDIN_FILENO, buf + n, cap - n);
    if (r < 0) {
      if (errno == EINTR)
        continue;
      free(buf);
      return NULL;
    }
    if (r == 0)
      break;
    n += (size_t) r;
    if (n > max) {
      free(buf);
      errno = EFBIG;
      return NULL;
    }
  }
  *len = n;
  return buf;
}

/* wmf_to_svg converts with libwmf's SVG device, images inline (no file is written).
 * Text metrics come from libwmf's own fonts (the URW Type 1 faces it installs, its
 * compile-time font directory and fontmap); the SVG names the metafile's fonts. */
static int
wmf_to_svg(unsigned char *data, size_t len, unsigned int max_side)
{
  wmfAPI_Options opts;
  wmfAPI *api = NULL;
  wmf_svg_t *dd;
  wmfD_Rect bbox;
  float w, h, scale = 1;
  unsigned long flags;
  wmf_error_t err;

  memset(&opts, 0, sizeof(opts));
  flags = WMF_OPT_FUNCTION | WMF_OPT_IGNORE_NONFATAL | WMF_OPT_NO_DEBUG | WMF_OPT_NO_ERROR;
  opts.function = wmf_svg_function;
  if ((err = wmf_api_create(&api, flags, &opts)) != wmf_E_None) {
    if (api)
      wmf_api_destroy(api);
    return 1;
  }
  if (len > LONG_MAX || wmf_mem_open(api, data, (long) len) != wmf_E_None ||
      wmf_scan(api, 0, &bbox) != wmf_E_None || wmf_size(api, &w, &h) != wmf_E_None ||
      !(w > 0) || !(h > 0) || !isfinite(w) || !isfinite(h)) {
    wmf_api_destroy(api);
    return 1;
  }
  if (w > max_side || h > max_side)
    scale = (w > h ? max_side / w : max_side / h);
  dd = WMF_SVG_GetData(api);
  dd->out = wmf_stream_create(api, stdout);
  dd->Description = "wmf";
  dd->bbox = bbox;
  dd->width = (unsigned int) ceilf(w * scale);
  dd->height = (unsigned int) ceilf(h * scale);
  if (dd->width == 0)
    dd->width = 1;
  if (dd->height == 0)
    dd->height = 1;
  dd->flags |= WMF_SVG_INLINE_IMAGES;
  err = wmf_play(api, 0, &bbox);
  wmf_api_destroy(api);
  return err == wmf_E_None ? 0 : 1;
}

static long
le32(const unsigned char *p)
{
  return (long) (int32_t) ((uint32_t) p[0] | (uint32_t) p[1] << 8 | (uint32_t) p[2] << 16 |
      (uint32_t) p[3] << 24);
}

/* emf_to_svg converts with libemf2svg, EMF+ records included, at the header's own
 * size (rclBounds, device pixels) unless a side is over max_side: libemf2svg then
 * fits the drawing inside max_side × max_side (it fits into whatever box it is given,
 * so a smaller drawing gets no box). */
static int
emf_to_svg(unsigned char *data, size_t len, unsigned int max_side)
{
  generatorOptions opts;
  char *svg = NULL;
  size_t svg_len = 0;
  long w, h;
  int ok;

  if (len < 88 || le32(data) != 1 || memcmp(data + 40, " EMF", 4) != 0)
    return 1;
  w = labs(le32(data + 16) - le32(data + 8));
  h = labs(le32(data + 20) - le32(data + 12));
  memset(&opts, 0, sizeof(opts));
  opts.emfplus = true;
  opts.svgDelimiter = true;
  if (w > (long) max_side || h > (long) max_side) {
    opts.imgWidth = max_side;
    opts.imgHeight = max_side;
  }
  ok = emf2svg((char *) data, len, &svg, &svg_len, &opts);
  if (!ok || !svg || svg_len == 0) {
    free(svg);
    return 1;
  }
  if (fwrite(svg, 1, svg_len, stdout) != svg_len) {
    free(svg);
    return 1;
  }
  free(svg);
  return 0;
}

int
main(int argc, char **argv)
{
  unsigned char *data;
  size_t len = 0;
  unsigned long long max_bytes, max_side;
  char *end;
  int rc;

  if (argc == 2 && strcmp(argv[1], "--version") == 0) {
    printf("xi-metafile2svg %s (libwmf, libemf2svg)\n", XI_METAFILE_VERSION);
    return 0;
  }
  if (argc != 4 || (strcmp(argv[1], "wmf") != 0 && strcmp(argv[1], "emf") != 0)) {
    fputs("usage: xi-metafile2svg --version | wmf|emf <max-input-bytes> <max-side-px>\n", stderr);
    return 2;
  }
  errno = 0;
  max_bytes = strtoull(argv[2], &end, 10);
  if (errno || *end || max_bytes == 0 || max_bytes > (1ULL << 30)) {
    fputs("xi-metafile2svg: bad max-input-bytes\n", stderr);
    return 2;
  }
  max_side = strtoull(argv[3], &end, 10);
  if (errno || *end || max_side == 0 || max_side > 65535) {
    fputs("xi-metafile2svg: bad max-side-px\n", stderr);
    return 2;
  }
  if (!(data = read_all((size_t) max_bytes, &len)) || len == 0) {
    fputs("xi-metafile2svg: empty or oversized input\n", stderr);
    free(data);
    return 2;
  }
  rc = strcmp(argv[1], "wmf") == 0 ? wmf_to_svg(data, len, (unsigned int) max_side)
                                   : emf_to_svg(data, len, (unsigned int) max_side);
  free(data);
  if (rc == 0 && fflush(stdout) != 0)
    rc = 1;
  if (rc != 0)
    fputs("xi-metafile2svg: conversion failed\n", stderr);
  return rc;
}
