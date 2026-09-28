/* Writes a lossless CMYK JPEG XL fixture (WP-105): the colour channels hold
 * C, M, Y and an extra channel of type JXL_CHANNEL_BLACK holds K, all
 * stored the JPEG XL way (1.0 = no ink), with the given CMYK ICC profile;
 * a fifth input sample becomes the alpha channel.
 *
 *   cc -o /tmp/jxlcmyk testdata/gen/jxlcmyk/encode.c $(pkg-config --cflags --libs libjxl)
 *   /tmp/jxlcmyk W H BANDS in.cmyk profile.icc out.jxl
 *
 * in.cmyk is W×H×BANDS interleaved 8-bit samples, 0 = no ink (gen cmyk-raw).
 */
#include <jxl/encode.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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
  b = malloc(*len);
  if (b && fread(b, 1, *len, f) != *len) {
    free(b);
    b = NULL;
  }
  fclose(f);
  return b;
}

#define CHECK(x)                                   \
  do {                                             \
    if ((x) != JXL_ENC_SUCCESS) {                  \
      fprintf(stderr, "jxlcmyk: %s failed\n", #x); \
      return 1;                                    \
    }                                              \
  } while (0)

int
main(int argc, char **argv)
{
  if (argc != 7) {
    fprintf(stderr, "usage: jxlcmyk W H BANDS in.cmyk profile.icc out.jxl\n");
    return 2;
  }
  size_t w = strtoul(argv[1], NULL, 10), h = strtoul(argv[2], NULL, 10);
  int bands = atoi(argv[3]);
  size_t n = w * h, rawlen, icclen;
  unsigned char *raw = slurp(argv[4], &rawlen), *icc = slurp(argv[5], &icclen);

  if (!raw || !icc || (bands != 4 && bands != 5) || rawlen != n * bands) {
    fprintf(stderr, "jxlcmyk: bad input\n");
    return 1;
  }
  unsigned char *cmy = malloc(n * 3), *k = malloc(n), *a = malloc(n);
  for (size_t i = 0; i < n; i++) {
    for (int c = 0; c < 3; c++)
      cmy[3 * i + c] = 255 - raw[bands * i + c];
    k[i] = 255 - raw[bands * i + 3];
    a[i] = bands == 5 ? raw[bands * i + 4] : 255;
  }

  JxlEncoder *enc = JxlEncoderCreate(NULL);
  JxlBasicInfo info;
  JxlEncoderInitBasicInfo(&info);
  info.xsize = (uint32_t) w;
  info.ysize = (uint32_t) h;
  info.bits_per_sample = 8;
  info.num_color_channels = 3;
  info.num_extra_channels = bands == 5 ? 2 : 1;
  info.alpha_bits = bands == 5 ? 8 : 0;
  info.uses_original_profile = JXL_TRUE;
  CHECK(JxlEncoderSetBasicInfo(enc, &info));
  CHECK(JxlEncoderSetICCProfile(enc, icc, icclen));
  uint32_t black = bands == 5 ? 1 : 0; // alpha is extra channel 0
  JxlExtraChannelInfo ec;
  JxlEncoderInitExtraChannelInfo(JXL_CHANNEL_BLACK, &ec);
  ec.bits_per_sample = 8;
  CHECK(JxlEncoderSetExtraChannelInfo(enc, black, &ec));
  if (bands == 5) {
    JxlEncoderInitExtraChannelInfo(JXL_CHANNEL_ALPHA, &ec);
    ec.bits_per_sample = 8;
    CHECK(JxlEncoderSetExtraChannelInfo(enc, 0, &ec));
  }
  JxlEncoderFrameSettings *fs = JxlEncoderFrameSettingsCreate(enc, NULL);
  CHECK(JxlEncoderSetFrameLossless(fs, JXL_TRUE));
  JxlPixelFormat f3 = { 3, JXL_TYPE_UINT8, JXL_NATIVE_ENDIAN, 0 };
  JxlPixelFormat f1 = { 1, JXL_TYPE_UINT8, JXL_NATIVE_ENDIAN, 0 };
  CHECK(JxlEncoderAddImageFrame(fs, &f3, cmy, n * 3));
  CHECK(JxlEncoderSetExtraChannelBuffer(fs, &f1, k, n, black));
  if (bands == 5)
    CHECK(JxlEncoderSetExtraChannelBuffer(fs, &f1, a, n, 0));
  JxlEncoderCloseInput(enc);

  size_t cap = 1 << 16, used = 0;
  unsigned char *out = malloc(cap);
  for (;;) {
    uint8_t *next = out + used;
    size_t avail = cap - used;
    JxlEncoderStatus st = JxlEncoderProcessOutput(enc, &next, &avail);
    used = (size_t) (next - out);
    if (st == JXL_ENC_SUCCESS)
      break;
    if (st != JXL_ENC_NEED_MORE_OUTPUT) {
      fprintf(stderr, "jxlcmyk: encode failed\n");
      return 1;
    }
    cap *= 2;
    out = realloc(out, cap);
  }
  FILE *o = fopen(argv[6], "wb");
  if (!o || fwrite(out, 1, used, o) != used || fclose(o)) {
    fprintf(stderr, "jxlcmyk: cannot write %s\n", argv[6]);
    return 1;
  }
  JxlEncoderDestroy(enc);
  return 0;
}
