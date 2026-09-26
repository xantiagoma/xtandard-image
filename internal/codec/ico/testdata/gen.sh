#!/bin/sh
# Regenerates the ICO fixture corpus with ImageMagick 7 (`magick`). Each entry
# i of <name>.ico gets <name>-<i>.ref.png, ImageMagick's decode of that entry,
# used as the test reference. ImageMagick only writes DIB entries; PNG-in-ICO,
# CUR, a paletted DIB with a non-trivial AND mask and malformed files are
# built in ico_test.go.
#
# Generated with ImageMagick 7.1.2-31. Run from anywhere: sh testdata/gen.sh
set -eu
cd "$(dirname "$0")"

magick -size 64x64 xc: -sparse-color bilinear '0,0 red 63,0 lime 0,63 blue 63,63 yellow' -depth 8 src.png
magick -size 64x64 radial-gradient:white-black -depth 8 mask.png
magick src.png mask.png -alpha off -compose copyopacity -composite -depth 8 srca.png
rm mask.png src.png

# Four 32-bit BGRA DIB entries (64, 48, 32, 16) with AND masks.
magick srca.png -define icon:auto-resize=64,48,32,16 multi.ico
# One 4-bit paletted DIB entry (32x32) with an (opaque) AND mask.
magick srca.png -alpha off -resize '32x32!' -colors 16 -type Palette pal16.ico
# 32-bit entry with binary alpha.
magick srca.png -resize '16x16!' -channel A -threshold 50% +channel mask1.ico

magick multi.ico PNG32:multi-%d.ref.png
magick pal16.ico PNG32:pal16-0.ref.png
magick mask1.ico PNG32:mask1-0.ref.png

rm srca.png
