// heifthumb writes a HEIF/AVIF still with an embedded thumbnail whose
// pixels come from a *different* image, so tests can tell which one was
// decoded (enforce_thumbnail). Inputs are raw interleaved 8-bit RGB files
// (`vips rawsave`). libheif scales the thumbnail source to fit bbox with
// nearest neighbour.
//
//   cc -O2 -o heifthumb testdata/gen/heifthumb/heifthumb.c $(pkg-config --cflags --libs libheif)
//   heifthumb hevc|av1 main.raw W H thumb.raw TW TH BBOX out.heic
//
// CC0, like the fixtures it writes.
#include <libheif/heif.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int
die(const char *what, struct heif_error err)
{
  fprintf(stderr, "%s: %s\n", what, err.message);
  return 1;
}

static struct heif_image *
load_raw(const char *path, int w, int h)
{
  FILE *f = fopen(path, "rb");
  if (!f)
    return NULL;
  struct heif_image *img = NULL;
  if (heif_image_create(w, h, heif_colorspace_RGB, heif_chroma_interleaved_RGB, &img).code ||
      heif_image_add_plane(img, heif_channel_interleaved, w, h, 8).code) {
    fclose(f);
    return NULL;
  }
  size_t stride = 0;
  uint8_t *p = heif_image_get_plane2(img, heif_channel_interleaved, &stride);
  for (int y = 0; y < h; y++) {
    if (fread(p + y * stride, 3, w, f) != (size_t) w) {
      fclose(f);
      heif_image_release(img);
      return NULL;
    }
  }
  fclose(f);
  return img;
}

int
main(int argc, char **argv)
{
  if (argc != 10) {
    fprintf(stderr, "usage: heifthumb hevc|av1 main.raw W H thumb.raw TW TH BBOX out\n");
    return 2;
  }
  enum heif_compression_format fmt = strcmp(argv[1], "av1") == 0 ? heif_compression_AV1 : heif_compression_HEVC;
  struct heif_image *main_img = load_raw(argv[2], atoi(argv[3]), atoi(argv[4]));
  struct heif_image *thumb_img = load_raw(argv[5], atoi(argv[6]), atoi(argv[7]));
  if (!main_img || !thumb_img) {
    fprintf(stderr, "cannot read the raw inputs\n");
    return 1;
  }
  struct heif_context *ctx = heif_context_alloc();
  struct heif_encoder *enc = NULL;
  struct heif_error err = heif_context_get_encoder_for_format(ctx, fmt, &enc);
  if (err.code)
    return die("encoder", err);
  heif_encoder_set_lossy_quality(enc, 60);
  struct heif_image_handle *handle = NULL;
  if ((err = heif_context_encode_image(ctx, main_img, enc, NULL, &handle)).code)
    return die("encode image", err);
  struct heif_image_handle *thumb = NULL;
  if ((err = heif_context_encode_thumbnail(ctx, thumb_img, handle, enc, NULL, atoi(argv[8]), &thumb)).code)
    return die("encode thumbnail", err);
  if ((err = heif_context_write_to_file(ctx, argv[9])).code)
    return die("write", err);
  heif_image_handle_release(thumb);
  heif_image_handle_release(handle);
  heif_encoder_release(enc);
  heif_context_free(ctx);
  heif_image_release(main_img);
  heif_image_release(thumb_img);
  return 0;
}
