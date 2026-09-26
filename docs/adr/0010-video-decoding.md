# ADR 0010: Video thumbnails decode through a second, build-tagged cgo package over FFmpeg

- Status: accepted (WP-74)
- Date: 2026-09-26

## Context

WP-74 turns a frame of a video (MP4, MOV, WebM, MKV, AVI) into an image, like imgproxy Pro's video thumbnails (`video_thumbnail_second`, `video_thumbnail_keyframes`, `/info` `video_meta`). The `full` base image (WP-70, ADR 0005, ADR 0006) ships FFmpeg 8.1 under the LGPL as shared libraries (`libavformat`, `libavcodec`, `libavutil`, `libswscale`, `libavfilter`) plus the `ffmpeg`/`ffprobe` CLIs; the `slim` image has no FFmpeg at all. Constraints:

- CLAUDE.md rule 3: `internal/vips` is the only package that imports `"C"`, and no C type leaves it.
- Rule 6: C never keeps a pointer to Go memory; source bytes live in C memory libvips owns (`vips.Buffer`), output is sent zero-copy.
- Rules 5, 7, 8: decoding is bounded by the worker semaphore, runs on the job's locked thread, and honours `ctx`.
- The slim image must compile the same code and answer `Unsupported` (422) for video sources, never crash.
- `docs/ARCHITECTURE.md` §3 sketched `internal/video` with the build tag `ffmpeg` and go-astiav.

## Options

The spike (≤ 15% of the WP) built option (a) end to end first, because it is the only one that satisfies every constraint without a workaround; (b) and (c) were assessed against it.

**(a) A second cgo package, `internal/video/ffmpeg`, behind the build tag `ffmpeg`.** A hand-written C shim (`shim.c`, about 600 lines, the same style as `internal/vips`) does several FFmpeg calls per Go call: open and probe, seek and decode, histogram, convert. The Go side sees an opaque handle and plain result structs. The spike decoded H.264, AV1 (dav1d), VP8, VP9, PNG and rawvideo from MP4, MOV, WebM, MKV and AVI fixtures on the first run, including a 90° display matrix.

**(b) purego `dlopen` of libavformat/libavcodec.** No cgo, one binary for both flavours. But every useful FFmpeg API passes or returns structs whose fields must be read or written directly (`AVFormatContext.pb`/`flags`/`interrupt_callback`/`streams`, `AVStream.codecpar`/`time_base`, `AVFrame.data`/`linesize`/`format`/`best_effort_timestamp`, `AVCodecContext.thread_count`/`skip_frame`). Their layouts change with each major version (FFmpeg 8 is libavformat 62, libavcodec 62), and purego would mirror them in Go by hand. A custom `AVIOContext` would need Go callbacks for read and seek on every 64 KiB block. Rejected: layout fragility and callback overhead, the two risks the WP named.

**(c) Spawn the `ffmpeg` CLI per request.** A process per thumbnail (fork/exec and FFmpeg start-up; measured below), the source on stdin is not seekable (MP4 files with the `moov` box at the end, the common case for camera files, cannot be read from a pipe) so it would need a temporary file (a read-only root filesystem allows only `/tmp`), cancellation is a `SIGKILL`, and memory and pixel limits are process-wide instead of per decode. Rejected.

**go-astiav** (the binding ARCHITECTURE.md named): MIT, but a third-party cgo package would put `import "C"` in the module graph outside our control, its release train follows FFmpeg majors, and it exposes FFmpeg's objects to Go (frames as Go-managed wrappers), which is what rule 3 exists to prevent. The shim needs about 20 FFmpeg functions; a binding of the whole API is not worth a dependency (go-standards §10).

## Decision

1. **Package and rule 3.** `internal/video/ffmpeg` is the **only** other package allowed to import `"C"`, and only in files guarded by `//go:build ffmpeg && cgo` (`ffmpeg.go`, `shim.c`). CLAUDE.md rule 3 reads as amended by this ADR; `scripts/check-cgo.sh` enforces it for both build variants (with and without `-tags ffmpeg`) and checks that the package is pure Go without the tag. No FFmpeg or C type crosses the package boundary: the API is `Available`, `Version`, `Probe(ctx, *vips.Buffer, video.Limits) (video.Meta, error)`, `Thumbnail(ctx, *vips.Buffer, *video.FrameRequest) (*Frame, error)` and `ReadStats`. The cgo-free types and rules (metadata, limits, `Seek`, `SamplePoints`, `Score`, `Orientation`) live in `internal/video`, which builds with `CGO_ENABLED=0`.
2. **Slim builds.** Without the tag, or with `CGO_ENABLED=0`, `stub.go` provides the same API: `Available()` is false and every call returns `errs.KindUnsupported` with the public message "video sources are not supported by this image". The sniffer still recognises video containers, so a video on slim is a clean 422, not "unrecognised format".
3. **Memory.** The source is read by a custom `AVIOContext` (read and seek callbacks written in C) straight from the C memory of the source `vips.Buffer`, which the decoder retains for its lifetime: no temporary file, no copy, no protocol (`protocol_whitelist=none`, so QuickTime data references or HLS playlists cannot open anything). The thumbnail is converted by libswscale directly into a new `vips.Buffer` (C memory, tracked by libvips), and `vips.NewFromBuffer` wraps it without copying (the image holds a reference to the blob until libvips closes it). C never sees Go memory except transient out-parameters (`#cgo noescape`), and every FFmpeg object is freed by `xi_ff_close` before `Thumbnail` returns.
4. **Limits and cancellation.** Demuxers are limited to `mov,matroska,avi` and decoders to `h264,hevc,vp8,vp9,libdav1d,mpeg4,mjpeg,png,rawvideo` (`format_whitelist`, `codec_whitelist`). Every decoder, including the ones `avformat_find_stream_info` opens, runs with `threads=1` and `max_pixels` = `XI_VIDEO_MAX_PIXELS`; `XI_VIDEO_MAX_BYTES` and `XI_VIDEO_MAX_DURATION` are checked before any frame is decoded. A `context.AfterFunc` watcher sets an atomic abort flag that the `AVIOContext` interrupt callback and the decode loop poll (nothing calls back into Go); the call then fails with the context's kind. Decoding runs inside `vips.Run` (locked thread) under the worker semaphore like any image.
5. **Build plumbing.** The `full` flavour builds with `-tags ffmpeg`: the app `Dockerfile` has `ARG GO_TAGS` (set by `task release:build` when the base is full), and `compose.dev.yml` passes `XI_GO_TAGS` into `GOFLAGS`, which the Taskfile sets to `ffmpeg` when `XI_BASE_IMAGE` (or `XI_IMAGE_FLAVOR`) is full. `XI_BASE_IMAGE=xtandard-base:dev-full-builder task docker:test` therefore runs the FFmpeg tests; the slim builder runs the stub's.
6. **No new Go module.** The shim links FFmpeg through `#cgo pkg-config: libavformat libavcodec libavutil libswscale` (go-standards §4: pkg-config only). FFmpeg stays LGPL-2.1+, dynamically linked, already listed in `NOTICE` and ADR 0006 by WP-70.

## Consequences

- Two cgo packages instead of one: reviewers check `internal/video/ffmpeg` with the same rules as `internal/vips` (C strings cached or stack-allocated, `noescape`/`nocallback` on every entry point, deterministic frees, leak test). The stub keeps `go test ./...` meaningful on slim and without cgo.
- FFmpeg major upgrades touch only `shim.c`. The APIs it uses are the stable ones of FFmpeg 7–8 (`av_packet_side_data_get`, `AVChannelLayout`, `av_dict_iterate`, the send/receive decode API, `sws_getCachedContext`).
- WP-75 (tiles, animation) extends the same handle: `xi_ff_decode_at` already seeks and decodes any timestamp, so tiles are N calls with `xi_ff_convert` into one buffer each. WP-76 (GIF → video) needs encoding (`libavcodec` encoders, `libavformat` muxers) and will add its own entry points to this package under the same rules.

## Measurements

Benchmarks (`BenchmarkThumbnail` in `internal/video/ffmpeg`, full builder on arm64, `-count=10`, benchstat medians, 1280×720 fixtures, frame at 1 s decoded and converted to RGB): see `docs/FORMATS.md` §1.5 for the table. The CLI alternative (c) was timed on the same fixtures with `ffmpeg -ss 1 -i <file> -frames:v 1 -f rawvideo -pix_fmt rgb24 -` in the same container; the numbers are in the same section.
