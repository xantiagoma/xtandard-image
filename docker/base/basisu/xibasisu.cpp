// xibasisu: a small C API over the Basis Universal KTX2 transcoder (WP-108).
//
// Copyright (c) 2026 Santiago Montoya and the xtandard-image contributors.
// SPDX-License-Identifier: MIT
//
// See xibasisu.h. Compiled by docker/base/build-basisu.sh together with
// transcoder/basisu_transcoder.cpp (Apache-2.0), whose container_abort() is
// patched to throw std::bad_alloc instead of terminating the process
// (patches/basisu), so a hostile file ends in an error here.

#include "xibasisu.h"

#include <cstdlib>
#include <cstring>
#include <mutex>
#include <new>

#include "basisu_transcoder.h"

#ifndef XI_BASISU_VERSION
#define XI_BASISU_VERSION "unknown"
#endif

namespace {

std::once_flag init_once;

void init() {
  std::call_once(init_once, [] { basist::basisu_transcoder_init(); });
}

// open initialises t over data and fills info from the header and the level
// index of level 0, layer 0, face 0.
int open(basist::ktx2_transcoder &t, const void *data, size_t len, xi_basisu_info *info) {
  if (data == nullptr || len < 80 || len > UINT32_MAX || info == nullptr)
    return XI_BASISU_UNSUPPORTED;
  std::memset(info, 0, sizeof(*info));
  if (!t.init(data, static_cast<uint32_t>(len)))
    return XI_BASISU_UNSUPPORTED;
  basist::ktx2_image_level_info li;
  if (!t.get_image_level_info(li, 0, 0, 0))
    return XI_BASISU_CORRUPT;
  if (li.m_orig_width == 0 || li.m_orig_height == 0)
    return XI_BASISU_CORRUPT;
  info->width = li.m_orig_width;
  info->height = li.m_orig_height;
  info->levels = t.get_levels();
  info->layers = t.get_layers();
  info->faces = t.get_faces();
  info->has_alpha = t.get_has_alpha() ? 1 : 0;
  info->hdr = t.is_hdr() ? 1 : 0;
  info->format = static_cast<int>(t.get_basis_tex_format());
  return XI_BASISU_OK;
}

} // namespace

extern "C" const char *xi_basisu_version(void) { return XI_BASISU_VERSION; }

extern "C" int xi_basisu_ktx2_info(const void *data, size_t len, xi_basisu_info *info) {
  try {
    init();
    basist::ktx2_transcoder t;
    return open(t, data, len, info);
  } catch (const std::bad_alloc &) {
    return XI_BASISU_NOMEM;
  } catch (...) {
    return XI_BASISU_CORRUPT;
  }
}

extern "C" int xi_basisu_ktx2_decode(const void *data, size_t len, uint64_t max_pixels,
    void **out, size_t *out_len, xi_basisu_info *info) {
  if (out == nullptr || out_len == nullptr)
    return XI_BASISU_UNSUPPORTED;
  *out = nullptr;
  *out_len = 0;
  void *px = nullptr;
  try {
    init();
    basist::ktx2_transcoder t;
    int rc = open(t, data, len, info);
    if (rc != XI_BASISU_OK)
      return rc;
    const uint64_t pixels = static_cast<uint64_t>(info->width) * info->height;
    if (max_pixels > 0 && pixels > max_pixels)
      return XI_BASISU_LIMIT;
    const uint64_t bpp = info->hdr ? 8 : 4;
    if (pixels > UINT32_MAX || pixels * bpp > SIZE_MAX)
      return XI_BASISU_LIMIT;
    if (!t.start_transcoding())
      return XI_BASISU_CORRUPT;
    const size_t n = static_cast<size_t>(pixels * bpp);
    px = std::malloc(n);
    if (px == nullptr)
      return XI_BASISU_NOMEM;
    const auto fmt = info->hdr ? basist::transcoder_texture_format::cTFRGBA_HALF
                               : basist::transcoder_texture_format::cTFRGBA32;
    // The state has no constructor: clear() sets the cached level index to
    // -1, or a stale stack value would skip the Zstandard decompression.
    basist::ktx2_transcoder_state state;
    state.clear();
    if (!t.transcode_image_level(0, 0, 0, px, static_cast<uint32_t>(pixels), fmt, 0, info->width,
            info->height, -1, -1, &state)) {
      std::free(px);
      return XI_BASISU_CORRUPT;
    }
    *out = px;
    *out_len = n;
    return XI_BASISU_OK;
  } catch (const std::bad_alloc &) {
    std::free(px);
    return XI_BASISU_NOMEM;
  } catch (...) {
    std::free(px);
    return XI_BASISU_CORRUPT;
  }
}
