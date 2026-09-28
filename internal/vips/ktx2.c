// Hand-written C shim for internal/vips: Zstandard for KTX2 levels (WP-108).
// See shim.h for the licence notice. The Basis Universal transcoder is
// called directly from ktx2.go through its C wrapper (xibasisu.h).

#include <zstd.h>

#include "shim.h"

int
xi_zstd_decompress(void *dst, size_t cap, const void *src, size_t len, size_t *got)
{
  unsigned long long size;
  size_t n;

  *got = 0;
  size = ZSTD_getFrameContentSize(src, len);
  // A frame that declares its size must declare exactly the level's.
  if (size == ZSTD_CONTENTSIZE_ERROR || (size != ZSTD_CONTENTSIZE_UNKNOWN && size != cap))
    return -1;
  // Single shot into dst: the output can never exceed cap (a larger frame
  // is an error), and the destination is the window, so nothing else of
  // that size is allocated.
  n = ZSTD_decompress(dst, cap, src, len);
  if (ZSTD_isError(n))
    return -1;
  *got = n;
  return 0;
}
