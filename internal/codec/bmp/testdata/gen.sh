#!/bin/sh
# Regenerates the BMP fixture corpus with ImageMagick 7 (`magick`). Each
# <name>.bmp gets <name>.ref.png, ImageMagick's own decode of it, which the
# tests use as the reference where golang.org/x/image/bmp cannot decode the
# variant. Variants ImageMagick cannot write (RLE4, RLE deltas, 16-bit BI_RGB,
# 32-bit BI_RGB, embedded PNG, malformed files) are built in bmp_test.go.
#
# Generated with ImageMagick 7.1.2-31. Run from anywhere: sh testdata/gen.sh
set -eu
cd "$(dirname "$0")"

# 13x7: odd width exercises row padding at every bit depth.
magick -size 13x7 xc: -sparse-color bilinear '0,0 red 12,0 lime 0,6 blue 12,6 yellow' -depth 8 src.png
magick -size 13x7 gradient:white-black -depth 8 mask.png
magick src.png mask.png -alpha off -compose copyopacity -composite -depth 8 srca.png
rm mask.png

magick src.png -monochrome BMP3:p1.bmp                                    # 1-bit
magick src.png -colors 16 -type Palette -compress None BMP3:p4.bmp        # 4-bit
magick src.png -colors 200 -type Palette -compress None BMP3:p8.bmp       # 8-bit
magick src.png -colors 200 -type Palette -compress RLE BMP3:rle8.bmp      # RLE8
magick src.png -type TrueColor BMP3:rgb24.bmp                             # 24-bit
magick src.png -type TrueColor BMP2:os2.bmp                               # OS/2 1.x 24-bit
magick src.png -colors 16 -type Palette BMP2:os2p4.bmp                    # OS/2 1.x 4-bit
magick src.png -type TrueColor -define bmp:subtype=RGB565 BMP:rgb565.bmp  # V5 bitfields 5-6-5
magick src.png -type TrueColor -define bmp:subtype=RGB555 BMP:rgb555.bmp  # V5 bitfields 5-5-5
magick srca.png -define bmp:subtype=ARGB1555 BMP:argb1555.bmp             # V5 16-bit alpha
magick srca.png -define bmp:subtype=ARGB4444 BMP:argb4444.bmp             # V5 16-bit alpha
magick srca.png BMP:v5a.bmp                                               # V5 32-bit alpha

for f in *.bmp; do
  magick "$f" PNG32:"${f%.bmp}.ref.png"
done
