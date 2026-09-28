/* Writes the high-precision JPEG fixtures that cjpeg cannot make (WP-106):
 * CMYK (Adobe-style, stored inverted) and lossless JPEG coded as YCbCr,
 * at any precision libjpeg-turbo 3 supports (8 or 12 bits for DCT, 2-16 for
 * lossless), with an optional ICC profile.
 *
 *   cc -o /tmp/jpeghp testdata/gen/jpeghp/encode.c $(pkg-config --cflags --libs libjpeg)
 *   /tmp/jpeghp W H MODE PRECISION in.raw [profile.icc|-] out.jpg
 *
 * MODE is one of
 *   cmyk      in.raw holds W×H×4 8-bit inks, 0 = no ink (gen cmyk-raw);
 *             DCT, quality 95, stored inverted (255 = no ink) with an Adobe
 *             marker, as Photoshop writes CMYK JPEG
 *   cmyk-ll   the same, lossless (predictor 1)
 *   ycc-ll    in.raw holds W×H×3 16-bit little-endian RGB samples; they are
 *             converted to full-range YCbCr (JFIF) at PRECISION and stored
 *             lossless (predictor 6) with a JFIF marker and component IDs
 *             1, 2, 3, which libjpeg-turbo decodes but cannot convert to RGB
 *
 * libjpeg-turbo's lossless compressor always codes the input colour space
 * (jcmaster.c calls jpeg_default_colorspace) and gave a stream it cannot
 * decode itself for JCS_YCbCr input (3.2.0), so ycc-ll codes the YCbCr
 * samples through the RGB path and relabels the result: the Adobe APP14
 * marker becomes a JFIF APP0 marker and the component IDs 'R', 'G', 'B'
 * become 1, 2, 3 (T.81 and JFIF say nothing else about the colour space).
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <jpeglib.h>

static unsigned char *
slurp(const char *path, size_t *len)
{
  FILE *f = fopen(path, "rb");
  unsigned char *b;

  if (!f)
    return NULL;
  fseek(f, 0, SEEK_END);
  *len = (size_t) ftell(f);
  fseek(f, 0, SEEK_SET);
  b = malloc(*len ? *len : 1);
  if (b && fread(b, 1, *len, f) != *len) {
    free(b);
    b = NULL;
  }
  fclose(f);
  return b;
}

static long
clampl(double v, long max)
{
  long r = (long) (v + 0.5);

  return r < 0 ? 0 : r > max ? max : r;
}

/* relabel_ycc writes the stream in b to f with the Adobe APP14 segment
 * replaced by a JFIF APP0 segment and the component IDs of the frame and
 * scan headers set to 1, 2, 3. */
static int
relabel_ycc(FILE *f, unsigned char *b, size_t len)
{
  static const unsigned char jfif[] = { 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01,
    0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00 };
  size_t i = 2;

  fwrite(b, 1, 2, f);
  fwrite(jfif, 1, sizeof(jfif), f);
  while (i + 4 <= len && b[i] == 0xFF) {
    int m = b[i + 1];
    size_t seg = (size_t) b[i + 2] << 8 | b[i + 3];

    if (m == 0xEE) { /* Adobe APP14: dropped */
      i += 2 + seg;
      continue;
    }
    if (m == 0xC3)
      for (int c = 0; c < b[i + 9]; c++)
        b[i + 10 + 3 * c] = (unsigned char) (c + 1);
    if (m == 0xDA) {
      for (int c = 0; c < b[i + 4]; c++)
        b[i + 5 + 2 * c] = (unsigned char) (c + 1);
      fwrite(b + i, 1, len - i, f); /* the scan and the rest */
      return 0;
    }
    fwrite(b + i, 1, 2 + seg, f);
    i += 2 + seg;
  }
  return 1;
}

int
main(int argc, char **argv)
{
  struct jpeg_compress_struct cinfo;
  struct jpeg_error_mgr jerr;
  int w, h, precision, comps, cmyk, lossless, rc = 0;
  long max;
  size_t len, icc_len = 0;
  unsigned char *in, *icc = NULL, *jpeg = NULL;
  unsigned long jpeg_len = 0;
  const char *mode;
  FILE *out;
  unsigned short *row;

  if (argc != 8) {
    fprintf(stderr, "usage: jpeghp W H cmyk|cmyk-ll|ycc-ll PRECISION in.raw profile.icc|- out.jpg\n");
    return 2;
  }
  w = atoi(argv[1]);
  h = atoi(argv[2]);
  mode = argv[3];
  precision = atoi(argv[4]);
  max = (1L << precision) - 1;
  cmyk = strncmp(mode, "cmyk", 4) == 0;
  lossless = strstr(mode, "-ll") != NULL;
  comps = cmyk ? 4 : 3;
  if (w < 1 || h < 1 || precision < 2 || precision > 16 ||
      (!cmyk && strcmp(mode, "ycc-ll") != 0)) {
    fprintf(stderr, "jpeghp: bad arguments\n");
    return 2;
  }
  if (!(in = slurp(argv[5], &len)) ||
      len != (size_t) w * h * comps * (cmyk ? 1 : 2)) {
    fprintf(stderr, "jpeghp: %s: want %d×%d×%d samples\n", argv[5], w, h, comps);
    return 1;
  }
  if (strcmp(argv[6], "-") != 0 && !(icc = slurp(argv[6], &icc_len))) {
    fprintf(stderr, "jpeghp: cannot read %s\n", argv[6]);
    return 1;
  }

  cinfo.err = jpeg_std_error(&jerr);
  jpeg_create_compress(&cinfo);
  jpeg_mem_dest(&cinfo, &jpeg, &jpeg_len);
  cinfo.image_width = (JDIMENSION) w;
  cinfo.image_height = (JDIMENSION) h;
  cinfo.input_components = comps;
  cinfo.in_color_space = cmyk ? JCS_CMYK : JCS_RGB;
  jpeg_set_defaults(&cinfo);
  cinfo.data_precision = precision;
  cinfo.optimize_coding = TRUE; /* the standard tables are for 8 bits */
  jpeg_set_colorspace(&cinfo, cinfo.in_color_space);
  if (lossless)
    jpeg_enable_lossless(&cinfo, cmyk ? 1 : 6, 0);
  else
    jpeg_set_quality(&cinfo, 95, TRUE);
  jpeg_start_compress(&cinfo, TRUE);
  if (icc)
    jpeg_write_icc_profile(&cinfo, icc, (unsigned int) icc_len);

  row = malloc(sizeof(*row) * (size_t) w * comps);
  for (int y = 0; y < h; y++) {
    for (int x = 0; x < w; x++) {
      unsigned short *o = row + (size_t) x * comps;

      if (cmyk) {
        const unsigned char *p = in + ((size_t) y * w + x) * 4;

        for (int c = 0; c < 4; c++)
          o[c] = (unsigned short) (max - clampl(p[c] * (double) max / 255.0, max));
      }
      else {
        const unsigned char *p = in + ((size_t) y * w + x) * 6;
        double r = (p[0] | p[1] << 8) * (double) max / 65535.0;
        double g = (p[2] | p[3] << 8) * (double) max / 65535.0;
        double b = (p[4] | p[5] << 8) * (double) max / 65535.0;
        double centre = (double) (1L << (precision - 1));

        o[0] = (unsigned short) clampl(0.299 * r + 0.587 * g + 0.114 * b, max);
        o[1] = (unsigned short) clampl(-0.168736 * r - 0.331264 * g + 0.5 * b + centre, max);
        o[2] = (unsigned short) clampl(0.5 * r - 0.418688 * g - 0.081312 * b + centre, max);
      }
    }
    if (precision <= 8) {
      JSAMPLE *r8 = malloc((size_t) w * comps);
      JSAMPROW rp = r8;

      for (int i = 0; i < w * comps; i++)
        r8[i] = (JSAMPLE) row[i];
      jpeg_write_scanlines(&cinfo, &rp, 1);
      free(r8);
    }
    else if (precision <= 12) {
      J12SAMPLE *r12 = malloc(sizeof(J12SAMPLE) * (size_t) w * comps);
      J12SAMPROW rp = r12;

      for (int i = 0; i < w * comps; i++)
        r12[i] = (J12SAMPLE) row[i];
      jpeg12_write_scanlines(&cinfo, &rp, 1);
      free(r12);
    }
    else {
      J16SAMPROW rp = row;

      jpeg16_write_scanlines(&cinfo, &rp, 1);
    }
  }
  jpeg_finish_compress(&cinfo);
  jpeg_destroy_compress(&cinfo);

  if (!(out = fopen(argv[7], "wb"))) {
    fprintf(stderr, "jpeghp: cannot create %s\n", argv[7]);
    return 1;
  }
  if (cmyk)
    fwrite(jpeg, 1, jpeg_len, out);
  else if (relabel_ycc(out, jpeg, jpeg_len)) {
    fprintf(stderr, "jpeghp: no scan in the stream\n");
    rc = 1;
  }
  fclose(out);
  free(jpeg);
  free(row);
  free(in);
  free(icc);
  return rc;
}
