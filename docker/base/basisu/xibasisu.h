/*
 * xibasisu: a small C API over the Basis Universal KTX2 transcoder (WP-108).
 *
 * Copyright (c) 2026 Santiago Montoya and the xtandard-image contributors.
 * SPDX-License-Identifier: MIT
 *
 * libxibasisu.so is this wrapper compiled together with the Basis Universal
 * transcoder (Binomial LLC, Apache-2.0, docker/base/build-basisu.sh). It decodes
 * the first image (mipmap level 0, layer 0, face 0) of a KTX2 file whose payload
 * Basis Universal can transcode (ETC1S/BasisLZ, UASTC LDR and HDR, ASTC and
 * XUASTC, XUBC7; Zstandard supercompression through the system libzstd) to
 * straight RGBA: 8 bits per channel, or half floats for HDR payloads.
 *
 * Thread-safe: every call builds its own transcoder; the transcoder's global
 * tables are initialised once.
 */
#ifndef XIBASISU_H
#define XIBASISU_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* The library is built with -fvisibility=hidden: only these functions are exported. */
#if defined(__GNUC__)
#define XI_BASISU_API __attribute__((visibility("default")))
#else
#define XI_BASISU_API
#endif

#define XI_BASISU_OK 0
/* Not a KTX2 file with a payload Basis Universal transcodes. */
#define XI_BASISU_UNSUPPORTED 1
/* The file is damaged or the transcoder refused it. */
#define XI_BASISU_CORRUPT 2
/* The image is larger than max_pixels. */
#define XI_BASISU_LIMIT 3
/* An allocation failed. */
#define XI_BASISU_NOMEM 4

typedef struct {
  uint32_t width, height; /* level 0 (the image's own size, not the block-padded one) */
  uint32_t levels, layers, faces;
  int has_alpha;
  int hdr;    /* decode returns RGBA half floats (8 bytes per pixel) */
  int format; /* basist::basis_tex_format */
} xi_basisu_info;

/* xi_basisu_version is the Basis Universal release the library was built from. */
XI_BASISU_API const char *xi_basisu_version(void);

/* xi_basisu_ktx2_info reads the KTX2 header and level index (no decoding). */
XI_BASISU_API int xi_basisu_ktx2_info(const void *data, size_t len, xi_basisu_info *info);

/* xi_basisu_ktx2_decode transcodes level 0, layer 0, face 0 into *out, a
 * malloc'd buffer of width*height*4 bytes (uint8 RGBA) or width*height*8 bytes
 * (half-float RGBA, info->hdr) that the caller frees with free(). Images over
 * max_pixels (> 0) fail with XI_BASISU_LIMIT before anything is decoded. */
XI_BASISU_API int xi_basisu_ktx2_decode(const void *data, size_t len, uint64_t max_pixels, void **out,
    size_t *out_len, xi_basisu_info *info);

#ifdef __cplusplus
}
#endif

#endif /* XIBASISU_H */
