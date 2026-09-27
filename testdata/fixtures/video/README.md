# Video fixtures (WP-74)

Regenerate with `sh testdata/gen/video.sh` (see the script for the exact commands) and the face
clips (WP-91: detection on video frames) with `sh testdata/gen/video-faces.sh`. Every file is at
most 200 KiB.

| File | Container / codec | Size, duration | Source | Licence |
|---|---|---|---|---|
| `clip-vp9.webm` | WebM / VP9 (libvpx, CRF 45, keyframe every 12 frames), BT.709 tagged | 1280×720, 2 s, 25 fps | procedural clip (`gen video-frames`, `testdata/gen/video.go`) | CC0 |
| `clip-av1.mp4` | MP4 / AV1 (libaom, CRF 50, keyframe every 12 frames), BT.709 tagged | 1280×720, 2 s, 25 fps | procedural clip | CC0 |
| `clip-av1-rotate90.mp4` | `clip-av1.mp4` stream-copied with a display matrix: shown rotated 90° clockwise | 1280×720 stored, 720×1280 shown | procedural clip | CC0 |
| `clip-vp8.webm` | WebM / VP8 (libvpx) | 320×180, 1 s, 10 fps | procedural clip | CC0 |
| `clip-vp8-matroska.mkv` | `clip-vp8.webm` remuxed into Matroska (DocType `matroska`) | 320×180, 1 s | procedural clip | CC0 |
| `clip-png.mov` | QuickTime MOV (`ftyp qt  `) / PNG | 96×54, 1 s, 10 fps | procedural clip | CC0 |
| `clip-raw.avi` | AVI / rawvideo BGR24 | 96×54, 1 s, 10 fps | procedural clip | CC0 |
| `faces-vp9.webm` | WebM / VP9 (libvpx, CRF 32, keyframe every 5 frames), BT.709 tagged | 640×503 (odd height), 1 s, 10 fps, a still | `detect/apollo11-crew.jpg` (`sh testdata/gen/video-faces.sh`) | Public domain (NASA) |
| `faces-av1-rotate90.mp4` | MP4 / AV1 (libaom, CRF 28), stored rotated 90° counter-clockwise with a display matrix that shows it upright | 503×640 stored, 640×503 shown, 1 s, 10 fps, a still | `detect/apollo11-crew.jpg` | Public domain (NASA) |
| `bbb-h264-aac.mp4` | MP4 / H.264 High (stream copy) + AAC mono 16 kHz | 1280×720, 1 s, 30 fps | first second of *Big Buck Bunny* (see below) + a 440 Hz tone | video: **CC-BY 3.0**; audio: CC0 |

The procedural clip pans across the landscape of `testdata/gen/main.go`, with a red disc moving
along the lake and a colour bar at the top whose hue encodes the time. It fades in from black
over the first 0.4 s (frame 0 is black) and fades out to white over the last 0.3 s, which is
what best-frame selection must avoid. All encoding runs in the `full` base builder, whose FFmpeg
is LGPL-only (`--disable-gpl --disable-nonfree`); only the AVI mux and the AAC tone use the
host's `ffmpeg` (rawvideo, the AVI muxer and FFmpeg's native AAC encoder are LGPL; nothing
GPL touches any file).

## Big Buck Bunny (H.264)

*Big Buck Bunny* © 2008 Blender Foundation, www.bigbuckbunny.org, licensed under the
[Creative Commons Attribution 3.0](https://creativecommons.org/licenses/by/3.0/) licence
(sunflower version, Janus Bager Kristensen 2013, as tagged in the file). The H.264 bitstream is
the first second of the 720p "10s 1MB" transcode published by Test Videos
(<https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/720/Big_Buck_Bunny_720_10s_1MB.mp4>),
cut by stream copy (no re-encode: our FFmpeg has no H.264 encoder). Changes: trimmed to 1 s and
an audio track added. It is the only fixture with an attribution requirement; it is not
share-alike, so ADR 0006 allows it. It covers H.264 decoding (the most common codec of real
uploads), which our own encoders cannot produce.
