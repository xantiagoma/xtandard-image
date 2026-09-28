#!/bin/sh
# Tile-compressed FITS fixtures (WP-108) for internal/codec/fits, made by
# cfitsio's fpack from the uncompressed fixtures `gen fits` writes, plus
# funpack's decompression of the quantised ones as references (the exact
# dequantised values cfitsio computes). Run from the repository root:
#
#   sh testdata/gen/fits-fpack.sh
#
# It runs fpack in a throwaway ubuntu:noble container (libcfitsio-bin); nothing
# from it is shipped.
set -eu
dir=testdata/fixtures/fits
docker run --rm -v "$PWD/$dir:/w" -w /w ubuntu:noble sh -euc '
	apt-get update -qq >/dev/null
	DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends libcfitsio-bin >/dev/null
	fpack -r -S u16.fits > rice-u16.fits.fz
	fpack -r -S f32-nan.fits > rice-f32.fits.fz
	fpack -g2 -S f32-nan.fits > gzip2-f32.fits.fz
	fpack -r -qz 4 -S f32-nan.fits > dither2-f32.fits.fz
	fpack -g1 -S mef-i32.fits > gzip1-mef-i32.fits.fz
	fpack -h -S u16.fits > hcompress-u16.fits.fz
	for f in rice-f32 gzip2-f32 dither2-f32; do funpack -S $f.fits.fz > $f.ref.fits; done
	fpack -V 2>&1 | head -n1
'
