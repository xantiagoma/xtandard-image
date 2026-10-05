# Audio fixtures (WP-123)

Regenerate with `sh testdata/gen/audio.sh` (see the script for the exact commands).

| File | Container / codec | Content | Source | Licence |
|---|---|---|---|---|
| `tones-stereo.mp3` | MP3 / LAME 128 kbit/s | 4 s, 44.1 kHz stereo: 440 Hz left, 660 Hz right at -20 dBFS, three times louder from 2 s to 3 s (integrated loudness about -15.7 LUFS) | procedural (`aevalsrc`) | CC0 |

The tones are synthesised by FFmpeg's `aevalsrc` filter and encoded with LAME (LGPL-2.0+) in the
`full` base builder; nothing GPL touches the file. Transcode, loudness and waveform tests use it as
an audio-only source; `video/bbb-h264-aac.mp4` (mono 16 kHz AAC) is the video source with audio.
