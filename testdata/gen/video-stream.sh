#!/bin/sh
# Regenerates testdata/fixtures/video/stream-vp9-opus.webm (WP-126: HLS and
# DASH segments). Run it on the host:
#
#   sh testdata/gen/video-stream.sh
#
# The frames are the procedural clip of testdata/gen/video.go (640x360,
# 25 fps, 165 frames = 6.6 s: three 2 s segments and a 0.6 s one); the
# audio is a stereo tone synthesised by the host ffmpeg's aevalsrc filter
# into 16-bit PCM (LGPL code only): 440 Hz on the left and 660 Hz on the
# right at -20 dBFS, so a gap or a click at a segment boundary shows up as a
# discontinuity of a pure sine. Both are encoded in the full base builder
# (xtandard-base:dev-full-builder or XI_BASE_IMAGE), whose FFmpeg is
# LGPL-only: VP9 (libvpx, BSD) with a keyframe every second, which is not
# the segment grid, and Opus (libopus, BSD) at 48 kHz.
set -eu
cd "$(dirname "$0")/../.."

F=testdata/fixtures/video
IMG=${XI_BASE_IMAGE:-xtandard-base:dev-full-builder}
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

go run ./testdata/gen video-frames 640 360 25 165 "$T/f360.rgb"
ffmpeg -hide_banner -loglevel error -y -f lavfi \
  -i "aevalsrc=0.1*sin(2*PI*440*t)|0.1*sin(2*PI*660*t):s=48000:d=6.6" \
  -c:a pcm_s16le "$T/tone.wav"

docker run --rm -v "$PWD/$F:/out" -v "$T:/t" -w /t "$IMG" sh -eu -c '
ffmpeg -hide_banner -loglevel error -y -fflags +bitexact \
  -f rawvideo -pix_fmt rgb24 -s 640x360 -r 25 -i f360.rgb -i tone.wav \
  -vf scale=out_color_matrix=bt709:out_range=tv -colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv \
  -c:v libvpx-vp9 -pix_fmt yuv420p -g 25 -crf 50 -b:v 0 -deadline good -cpu-used 4 -row-mt 1 \
  -c:a libopus -b:a 48k -map_metadata -1 /out/stream-vp9-opus.webm
'
ls -l "$F/stream-vp9-opus.webm"

# stream-vp9-aac44k.mp4 (WP-131: chunked stream audio): the same video
# stream copied into MP4 with the tone at 44.1 kHz in AAC-LC (the host
# ffmpeg's native encoder, LGPL), so the audio's timestamps name its samples
# (time base 1/44100) and chunks go through the resampler.
ffmpeg -hide_banner -loglevel error -y -fflags +bitexact -i "$F/stream-vp9-opus.webm" -f lavfi \
  -i "aevalsrc=0.1*sin(2*PI*440*t)|0.1*sin(2*PI*660*t):s=44100:d=6.6" \
  -map 0:v -map 1:a -c:v copy -c:a aac -b:a 64k -map_metadata -1 -movflags +faststart \
  "$F/stream-vp9-aac44k.mp4"
ls -l "$F/stream-vp9-aac44k.mp4"
