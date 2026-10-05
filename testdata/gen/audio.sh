#!/bin/sh
# Regenerates testdata/fixtures/audio/ (WP-123). Run it on the host:
#
#   sh testdata/gen/audio.sh
#
# The tones are synthesised by the host ffmpeg's aevalsrc filter into 16-bit
# PCM (LGPL code only), then encoded to MP3 with LAME (LGPL-2.0+) in the full
# base builder (xtandard-base:dev-full-builder or XI_BASE_IMAGE), whose
# FFmpeg is LGPL-only. See testdata/fixtures/audio/README.md.
set -eu
cd "$(dirname "$0")/../.."

F=testdata/fixtures/audio
IMG=${XI_BASE_IMAGE:-xtandard-base:dev-full-builder}
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$F"

# 4 s of stereo 44.1 kHz: 440 Hz on the left, 660 Hz on the right, at
# amplitude 0.1 (-20 dBFS), three times louder between 2 s and 3 s, so a
# waveform shows a step and loudness normalisation has work to do.
ffmpeg -hide_banner -loglevel error -y -f lavfi \
  -i "aevalsrc=0.1*(1+2*between(t\,2\,3))*sin(2*PI*440*t)|0.1*(1+2*between(t\,2\,3))*sin(2*PI*660*t):s=44100:d=4" \
  -c:a pcm_s16le "$T/tones.wav"

docker run --rm -v "$PWD/$F:/out" -v "$T:/t" -w /t "$IMG" sh -eu -c '
ffmpeg -hide_banner -loglevel error -y -fflags +bitexact -i tones.wav \
  -c:a libmp3lame -b:a 128k -flags +bitexact -map_metadata -1 /out/tones-stereo.mp3
'
ls -l "$F"
