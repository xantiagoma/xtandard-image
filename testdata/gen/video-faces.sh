#!/bin/sh
# Regenerates the face video fixtures of testdata/fixtures/video/ (WP-91):
# the Apollo 11 crew photo (testdata/fixtures/detect/apollo11-crew.jpg,
# NASA, public domain) as a one-second still clip, for face detection on
# video frames. Run it on the host; the encoders run in the full base
# builder, whose FFmpeg is LGPL-only (docker/base, WP-70):
#
#   sh testdata/gen/video-faces.sh
#
# Requirements: Docker with xtandard-base:dev-full-builder (or
# XI_BASE_IMAGE). video.sh regenerates the other video fixtures.
set -eu
cd "$(dirname "$0")/../.."

F=testdata/fixtures
IMG=${XI_BASE_IMAGE:-xtandard-base:dev-full-builder}

docker run --rm -v "$PWD/$F:/f" -w /f/video "$IMG" sh -eu -c '
ff() { ffmpeg -hide_banner -loglevel error -y -fflags +bitexact "$@"; }
crew=/f/detect/apollo11-crew.jpg
tag="-colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv"
# VP9 in WebM, 640x503 (the photo size, odd height), 1 s at 10 fps, a
# keyframe every 5 frames.
ff -loop 1 -framerate 10 -t 1 -i $crew \
	-vf "scale=out_color_matrix=bt709:out_range=tv,format=yuv420p" $tag \
	-c:v libvpx-vp9 -g 5 -crf 32 -b:v 0 -deadline good -cpu-used 4 faces-vp9.webm
# AV1 in MP4, stored rotated 90 degrees counter-clockwise (503x640) with a
# display matrix that shows it upright again (like a phone video shot in
# portrait): detection must see the displayed frame.
ff -loop 1 -framerate 10 -t 1 -i $crew \
	-vf "transpose=2,scale=out_color_matrix=bt709:out_range=tv,format=yuv420p" $tag \
	-c:v libaom-av1 -g 5 -crf 28 -cpu-used 8 /tmp/stored.mp4
ff -display_rotation:v:0 -90 -i /tmp/stored.mp4 -c copy faces-av1-rotate90.mp4
for f in faces-vp9.webm faces-av1-rotate90.mp4; do ffprobe -v error -show_entries stream=codec_name,width,height,pix_fmt:stream_side_data=rotation -of compact "$f"; done
'
ls -l "$F/video/faces-vp9.webm" "$F/video/faces-av1-rotate90.mp4"
