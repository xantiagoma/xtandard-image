// Hand-written C shim for internal/vips: EXIF tag titles from libexif, the
// library libvips itself parses EXIF with. See shim.h for the licence
// notice.

#include <string.h>

#include <libexif/exif-ifd.h>
#include <libexif/exif-tag.h>

#include "shim.h"

const char *
xi_exif_title(const char *name, int ifd)
{
  ExifTag tag;
  const char *canonical;

  if (name == NULL || ifd < 0 || ifd >= EXIF_IFD_COUNT)
    return NULL;

  // exif_tag_from_name returns 0 both for unknown names and for tag 0
  // (GPSVersionID), so check the round trip.
  tag = exif_tag_from_name(name);
  canonical = exif_tag_get_name_in_ifd(tag, (ExifIfd) ifd);
  if (canonical == NULL || strcmp(canonical, name) != 0)
    return NULL;

  return exif_tag_get_title_in_ifd(tag, (ExifIfd) ifd);
}
