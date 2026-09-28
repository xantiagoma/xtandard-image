// heifseq writes a small HEIF/AVIF image sequence (a libheif sequence
// track, no still items) for the WP-104 loader tests. Frame i is a solid
// colour from a fixed palette with a white 16×16 square at x = 4 + 12·i,
// y = 16, so tests can tell the frames apart; frame i lasts (i + 1) × 100 ms.
// With alpha, the left half of every frame is half transparent.
//
//   cc -O2 -o heifseq testdata/gen/heifseq/heifseq.c $(pkg-config --cflags --libs libheif)
//   heifseq av1|hevc W H FRAMES alpha(0|1) loop(0 = forever, n = plays) out.avif
//
// CC0, like the fixtures it writes.
#include <libheif/heif.h>
#include <libheif/heif_sequences.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const unsigned char palette[][3] = {
  { 200, 40, 40 }, { 40, 170, 60 }, { 40, 70, 200 }, { 220, 200, 40 },
  { 150, 60, 180 }, { 40, 180, 190 }, { 230, 120, 30 }, { 90, 90, 90 },
};

static int
die(const char *what, struct heif_error err)
{
  fprintf(stderr, "%s: %s\n", what, err.message);
  return 1;
}

static struct heif_image *
frame(int w, int h, int i, int alpha)
{
  struct heif_image *img = NULL;
  int bands = alpha ? 4 : 3;
  size_t stride = 0;
  const unsigned char *c = palette[i % 8];

  if (heif_image_create(w, h, heif_colorspace_RGB,
          alpha ? heif_chroma_interleaved_RGBA : heif_chroma_interleaved_RGB, &img).code ||
      heif_image_add_plane(img, heif_channel_interleaved, w, h, 8).code)
    return NULL;
  uint8_t *p = heif_image_get_plane2(img, heif_channel_interleaved, &stride);
  for (int y = 0; y < h; y++)
    for (int x = 0; x < w; x++) {
      uint8_t *q = p + y * stride + x * bands;
      int sq = x >= 4 + 12 * i && x < 20 + 12 * i && y >= 16 && y < 32;

      q[0] = sq ? 255 : c[0];
      q[1] = sq ? 255 : c[1];
      q[2] = sq ? 255 : c[2];
      if (alpha)
        q[3] = x < w / 2 ? 128 : 255;
    }
  return img;
}

int
main(int argc, char **argv)
{
  if (argc != 8) {
    fprintf(stderr, "usage: heifseq av1|hevc W H FRAMES alpha loop out\n");
    return 2;
  }
  enum heif_compression_format fmt = strcmp(argv[1], "hevc") == 0 ? heif_compression_HEVC
                                                                  : heif_compression_AV1;
  int w = atoi(argv[2]), h = atoi(argv[3]), n = atoi(argv[4]), alpha = atoi(argv[5]);
  int loop = atoi(argv[6]);
  const char *out = argv[7];
  struct heif_error err;

  heif_init(NULL);
  struct heif_context *ctx = heif_context_alloc();
  struct heif_encoder *enc = NULL;
  if ((err = heif_context_get_encoder_for_format(ctx, fmt, &enc)).code)
    return die("encoder", err);
  heif_encoder_set_lossy_quality(enc, 90);
  heif_context_set_sequence_timescale(ctx, 1000);
  heif_context_set_number_of_sequence_repetitions(ctx,
      loop == 0 ? heif_sequence_maximum_number_of_repetitions : (uint32_t) loop);

  heif_track_options *topts = heif_track_options_alloc();
  heif_track_options_set_timescale(topts, 1000);
  heif_sequence_encoding_options *sopts = heif_sequence_encoding_options_alloc();
  sopts->save_alpha_channel = alpha;
  heif_track *track = NULL;
  if ((err = heif_context_add_visual_sequence_track(ctx, (uint16_t) w, (uint16_t) h,
           heif_track_type_image_sequence, topts, sopts, &track))
          .code)
    return die("track", err);
  for (int i = 0; i < n; i++) {
    struct heif_image *img = frame(w, h, i, alpha);
    if (!img)
      return die("frame", (struct heif_error) { heif_error_Memory_allocation_error, 0, "frame" });
    heif_image_set_duration(img, (uint32_t) (i + 1) * 100);
    if ((err = heif_track_encode_sequence_image(track, img, enc, sopts)).code)
      return die("encode", err);
    heif_image_release(img);
  }
  if ((err = heif_track_encode_end_of_sequence(track, enc)).code)
    return die("end", err);
  if ((err = heif_context_write_to_file(ctx, out)).code)
    return die("write", err);
  heif_track_release(track);
  heif_sequence_encoding_options_release(sopts);
  heif_track_options_release(topts);
  heif_encoder_release(enc);
  heif_context_free(ctx);
  heif_deinit();
  return 0;
}
