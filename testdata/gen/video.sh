#!/bin/sh
# Regenerates testdata/fixtures/video/ (WP-74). Run it on the host; the
# encoders run in the full base builder, whose FFmpeg is LGPL-only
# (docker/base, WP-70):
#
#   sh testdata/gen/video.sh
#
# Requirements: Go 1.27 (the frame generator), Docker with
# xtandard-base:dev-full-builder (or XI_BASE_IMAGE), curl and a host ffmpeg
# for one mux step. The H.264 fixture is the first second of a public Big
# Buck Bunny transcode (CC-BY 3.0, Blender Foundation); it is cut by stream
# copy, never re-encoded (there is no H.264 encoder in our FFmpeg), and a
# short AAC tone is muxed next to it with FFmpeg's native (LGPL) AAC encoder.
# See testdata/fixtures/video/README.md.
set -eu
cd "$(dirname "$0")/../.."

F=testdata/fixtures/video
IMG=${XI_BASE_IMAGE:-xtandard-base:dev-full-builder}
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
BBB_URL=https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/720/Big_Buck_Bunny_720_10s_1MB.mp4

mkdir -p "$F"
rm -f "$F"/*.webm "$F"/*.mkv "$F"/*.mp4 "$F"/*.mov "$F"/*.avi

# Procedural frames (testdata/gen/video.go): 2 s at 25 fps in 1280x720, 1 s at
# 10 fps in 320x180 and 96x54.
go run ./testdata/gen video-frames 1280 720 25 50 "$T/f720.rgb"
go run ./testdata/gen video-frames 320 180 10 10 "$T/f180.rgb"
go run ./testdata/gen video-frames 96 54 10 10 "$T/f54.rgb"
curl -fsSL -o "$T/bbb.mp4" "$BBB_URL"

docker run --rm -v "$PWD/$F:/out" -v "$T:/t" -w /t "$IMG" sh -eu -c '
# bitexact: no muxing date or library version strings in the files (libvpx
# still varies the bitstream slightly between runs).
ff() { ffmpeg -hide_banner -loglevel error -y -fflags +bitexact "$@"; }
# BT.709 matrix, tagged; a keyframe every 12 frames (0.48 s).
in720="-f rawvideo -pix_fmt rgb24 -s 1280x720 -r 25 -i f720.rgb"
tag="-vf scale=out_color_matrix=bt709:out_range=tv -colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv"
ff $in720 $tag -c:v libvpx-vp9 -pix_fmt yuv420p -g 12 -crf 45 -b:v 0 -deadline good -cpu-used 4 -row-mt 1 /out/clip-vp9.webm
ff $in720 $tag -c:v libaom-av1 -pix_fmt yuv420p -g 12 -crf 50 -cpu-used 8 /out/clip-av1.mp4
# The AV1 clip again with a display matrix (stream copy): shown rotated 90
# degrees clockwise, like a phone video shot in portrait.
ff -display_rotation:v:0 -90 -i /out/clip-av1.mp4 -c copy /out/clip-av1-rotate90.mp4
# VP8 in WebM, 320x180, 1 s.
ff -f rawvideo -pix_fmt rgb24 -s 320x180 -r 10 -i f180.rgb -c:v libvpx -b:v 300k -g 5 /out/clip-vp8.webm
# PNG in QuickTime MOV, 96x54, 10 frames.
ff -f rawvideo -pix_fmt rgb24 -s 96x54 -r 10 -i f54.rgb -c:v png -pix_fmt rgb24 /out/clip-png.mov
# The VP8 clip remuxed into Matroska (DocType "matroska", not "webm").
ff -i /out/clip-vp8.webm -c copy -f matroska /out/clip-vp8-matroska.mkv
# H.264: the first second of Big Buck Bunny by stream copy (it starts on a
# keyframe), the source metadata kept.
ff -i bbb.mp4 -t 1 -map 0:v:0 -c copy bbb-v.mp4
'
# Uncompressed AVI (rawvideo, BGR24), 96x54, 10 frames. Our FFmpeg has no
# AVI muxer, so the host's ffmpeg writes it (rawvideo and the AVI muxer are
# LGPL; there is nothing to encode).
ffmpeg -hide_banner -loglevel error -y -f rawvideo -pix_fmt rgb24 -s 96x54 -r 10 -i "$T/f54.rgb" \
	-c:v rawvideo -pix_fmt bgr24 "$F/clip-raw.avi"
# A 1 s AAC tone next to the H.264 stream (FFmpeg's native AAC encoder,
# LGPL) gives /info an audio stream. Our FFmpeg has no audio encoder, so this
# step uses the host's ffmpeg; the video is stream-copied.
ffmpeg -hide_banner -loglevel error -y -i "$T/bbb-v.mp4" \
	-f lavfi -i "sine=frequency=440:sample_rate=16000:duration=1" \
	-map 0:v:0 -map 1:a:0 -c:v copy -c:a aac -b:a 24k -ac 1 \
	-metadata:s:a:0 language=eng -movflags +faststart -shortest "$F/bbb-h264-aac.mp4"
# Reference frames rendered by the ffmpeg CLI (accurate seek, autorotation,
# the stream's colour matrix): exec's TestVideoAgainstFFmpeg compares our
# thumbnails with them.
R=testdata/video-ref
mkdir -p "$R"
docker run --rm -v "$PWD/$F:/in:ro" -v "$PWD/$R:/out" "$IMG" sh -eu -c '
ff() { ffmpeg -hide_banner -loglevel error -y "$@"; }
ff -ss 1 -i /in/clip-vp9.webm -frames:v 1 -vf scale=320:180:flags=bicubic -pix_fmt rgb24 /out/vp9-1s-320.png
ff -ss 1 -i /in/clip-av1-rotate90.mp4 -frames:v 1 -vf scale=180:320:flags=bicubic -pix_fmt rgb24 /out/av1-rotate90-1s-180.png
ff -ss 0.5 -i /in/bbb-h264-aac.mp4 -frames:v 1 -vf scale=320:180:flags=bicubic -pix_fmt rgb24 /out/h264-0.5s-320.png
'
ls -l "$F" "$R"
