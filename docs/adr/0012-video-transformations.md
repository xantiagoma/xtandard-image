# ADR 0012: Video transformations run in their own subsystem over FFmpeg filter graphs

- Status: accepted (WP-120; owner decision 2026-10-04)
- Date: 2026-10-04

## Context

Until M4 video is an *input* of the image pipeline: a frame, a sprite or an animation is decoded by `internal/video/ffmpeg` (ADR 0010) and processed by libvips, and an animated result can be encoded to MP4/WebM from RGB frames (WP-76). The owner (2026-10-04) asked for **video transformations comparable to Cloudinary video** and **Cloudflare Media Transformations / Stream** inside this project:

- **Phase A**, short synchronous transforms: trim, resize/crop/fit, fps, rotate/flip, quality/bitrate, MP4/WebM output, mute, audio-only output (M4A/AAC, Opus, MP3), image and text watermarks; poster frames and sprites already exist (WP-74/75). Results are materialised, cached and served with HTTP `Range`.
- **Phase B**, HLS and DASH for VOD with just-in-time segment transcoding and adaptive-bitrate (ABR) ladders.
- Same binary and the same `full`/`gpl` images (`slim` stays without video), a **separate subsystem with its own worker pool and limits**, **off by default**, deployable as a **dedicated role** (`XI_ROLE=all|image|video`).
- Out of scope: live streaming, upload/catalogue management (a media library), DRM.

Video work differs from image work in every dimension that the image pipeline's design depends on: a job takes seconds to minutes instead of milliseconds, its input and output are tens to hundreds of megabytes, it has an audio track, the output is consumed with `Range` requests by players, and most of the cost is in the encoder. Re-using the image path (libvips per frame, the image semaphore, in-memory results) would let one video request starve the image service and hold whole videos in memory.

## Decision

### 1. Packages

- **`internal/video/ffmpeg`** stays the only cgo package besides `internal/vips` (ADR 0010, CLAUDE.md rule 3 as amended there), behind the `ffmpeg` build tag. It gains a transcoder: a third C file (`transcode.c`) with an opaque job handle that demuxes, decodes, runs one libavfilter graph, encodes and muxes, video and audio, in one call per chunk of work. No FFmpeg type leaves the package; the stub answers `Unsupported` without the tag.
- **A cgo-free subsystem package** (WP-121 names it; `internal/video/transcode` is the working name) owns the request model (`video.Transform`: trim, geometry, rate, codecs, bitrates, audio, overlays), the planner that turns it into a typed filter-graph description and encoder settings, the limits, the job runner (semaphore, coalescing, spool files), and the HTTP side (cache, `Range`). Dialects map their syntax onto `video.Transform` as they map image options onto `TransformRequest` today.
- The image pipeline keeps posters, sprites, animations and GIF → video (WP-74/75/76); the video subsystem does not route through libvips except to render overlay images (§3).

### 2. Pipeline: FFmpeg end to end, not libvips

decode (libavformat + libavcodec) → **one libavfilter graph** (video and audio chains) → encode (libavcodec) → mux (libavformat), streamed frame by frame. libvips is not used per frame: a YUV → RGB → libvips → RGB → YUV round trip costs more than the transform for every option on the Phase A list (scale, crop, pad, fps, transpose, overlay are native, SIMD, YUV filters), libvips has no audio, and a per-frame libvips call would put the image library inside the encoder's loop.

- **Graphs are built, never parsed from user input.** The planner emits a list of typed filter nodes with numeric, validated arguments (`scale=w:h:flags=…`, `crop`, `pad`, `fps`, `trim`/`atrim` + `setpts`/`asetpts`, `transpose`/`hflip`/`vflip`, `format`, `overlay`, `aresample`, `volume`, `loudnorm`, `afade`/`fade`); the shim creates each node with `avfilter_graph_alloc_filter` and sets options through the AVOption API, so no user string reaches `avfilter_graph_parse`. Text (§3) goes in as an option value, never as graph syntax.
- **Trim** seeks to the keyframe before the start and drops frames up to it (exact trims; `-ss` style fast keyframe trims only when the request asks for them), so a 10 s clip of a 1 h source decodes about one GOP more than the clip.
- **Geometry** follows the image pipeline's semantics (fit, fill, crop with gravity, pad with a colour, display matrix applied before anything else) and its option names where they exist; even dimensions for 4:2:0 output are enforced by the planner, not by the encoder failing.
- **Encoding** uses the encoders the base image has (§7), chosen at startup by probing (`avcodec_find_encoder_by_name`, as WP-76), with CRF/quality or target bitrate (+ `maxrate`/`bufsize`), a keyframe interval in seconds, and a `threads` setting from the job's budget (§4) instead of WP-76's single thread: a transcode is one long job, not many short ones.
- **Audio**: decoded, resampled by libswresample (`aresample`), mixed to mono/stereo, optionally normalised (`loudnorm`, single pass) and encoded; muted outputs drop the stream; audio-only outputs drop video.

### 3. Overlays

- **Image watermarks** are fetched through the normal source path (allowlists, signing, size limits), decoded and prepared by libvips (resize, opacity, rotation, as the image watermark options already do) into an RGBA buffer in C memory, which the shim feeds once to the graph through a `buffer` source and `overlay` (with `enable='between(t,a,b)'` for timed watermarks). One decoder (libvips) for every watermark image, no FFmpeg image decoder on untrusted overlays.
- **Static text** is rendered the same way, with the existing libvips/Pango text watermark code, so text looks identical on images and videos and uses the same fonts and shaping.
- **Dynamic text** (timecode, per-frame values) uses FFmpeg's `drawtext` (FreeType + HarfBuzz + FriBidi + Fontconfig, the base's DejaVu fonts): `expansion=none` unless the planner itself builds a whitelisted expansion (`%{pts\:hms}`), `fontfile`/`textfile` never set from requests (Fontconfig family names from a server list only), text length capped.

### 4. Concurrency, limits and roles

- **Own pool.** A second `internal/limiter` instance: `XI_VIDEO_WORKERS` concurrent transcodes (default `max(1, GOMAXPROCS/4)`), `XI_VIDEO_QUEUE_SIZE` waiting jobs (fail fast: 429 + `Retry-After`), `XI_VIDEO_QUEUE_TIMEOUT` (503), and `XI_VIDEO_THREADS` codec threads per job (default `GOMAXPROCS / workers`). The image semaphore is never taken by a video job and vice versa, so a burst of video work cannot raise image latency beyond the CPU it shares (and with the role split, not even that).
- **Coalescing.** Identical jobs (same cache key, §5) run once; later requests wait on the running job. A job's context is detached from any single request and cancelled when its last waiter leaves or `XI_VIDEO_JOB_TIMEOUT` expires (rule 8: `ctx.Err()` is checked between packets through the existing abort flag).
- **Time.** Video routes extend their write deadline per request with `http.ResponseController` (the server-wide `WriteTimeout` stays sized for images); a client waits for the job, then gets the materialised file.
- **Roles.** `XI_ROLE=all` (default) serves both; `image` never creates the video pool and its routes answer 404; `video` mounts only the video routes plus health, metrics and the admin listener. One binary and one config schema: an operator routes `/video/*`-style paths (or a dialect's video endpoints) to a `video` deployment and keeps a C crash in a demuxer or encoder away from the image fleet.
- **Off by default.** `XI_VIDEO_TRANSFORM_ENABLED=false`; `true` fails startup on an image without FFmpeg (slim) or without the encoders the configured defaults need. It is independent of `XI_VIDEO_ENABLED` (video sources for thumbnails).

### 5. Materialise, cache, serve with `Range`

- Phase A does not stream an encode to the client: MP4 needs its `moov` box at the front (`+faststart`) for progressive playback and players issue `Range` requests that need a known length. The job encodes into a **spool file** (`XI_VIDEO_SPOOL_DIR`, default the temp dir; bounded by `XI_VIDEO_SPOOL_MAX_BYTES`) through the shim's custom write/seek AVIO (no FFmpeg protocol; the faststart pass reads back through `io_open` as WP-76 does), then the file is committed to the result cache and served with `http.ServeContent` semantics: `Range`, `If-Range`, `206`, `416`, `ETag`, `Accept-Ranges: bytes`.
- **Sources are spooled too.** A video source is fetched to a spool file (not held in RAM) and read by the shim through a read/seek AVIO over the file descriptor; `XI_VIDEO_MAX_BYTES` bounds it.
- **Cache.** The key is the canonical `video.Transform` + source identity + the encoder fingerprint (the same URL produces AV1 in `full` and H.264 in `gpl`, so the flavour is part of the key). Results above `XI_VIDEO_CACHE_MEMORY_MAX` skip the memory tier. Stores that can serve byte ranges (disk: a file; S3: ranged GET) implement an optional range interface (WP-121) so a `Range` request on a cached result never reads the whole object; other stores fall back to a full read into the spool.

### 6. Phase B: HLS and DASH by just-in-time segments

- **Manifests from the probe alone.** The master/MPD lists renditions from a configured ladder (`XI_VIDEO_ABR_LADDER`, e.g. 1080p/720p/480p/360p with bitrates; rungs above the source are dropped). Segment boundaries are a fixed grid (`XI_VIDEO_SEGMENT_DURATION`, 4–6 s) known before any transcode, so the playlists (VOD, `EXT-X-ENDLIST`, `$Number$` templates) are generated in milliseconds.
- **Segments on demand.** A segment request transcodes `[n·d, (n+1)·d)` of its rendition: seek to the keyframe before the start, decode, drop to the boundary, encode with a forced keyframe at the start and closed GOPs, timestamps offset to the segment's position so segments join seamlessly. Output is **CMAF fragmented MP4** (one `init` segment per rendition, `moof`/`mdat` segments) used by both HLS (`EXT-X-MAP`) and DASH; MPEG-TS segments only where H.264 is available (gpl) and a client needs them.
- **Audio** renditions are cheap: the first audio segment request transcodes the whole audio track once, splits it into segments on the same grid (no priming gaps between independently encoded segments) and caches them.
- **Cache and prefetch.** Each segment and init segment is a cache entry; a segment request schedules the next `XI_VIDEO_PREFETCH_SEGMENTS` (default 2) at low priority on the same pool, never beyond its queue limits.
- **Codecs.** `full`: AV1 (and VP9 for DASH/WebM) video with AAC or Opus audio; `gpl`: H.264 (and HEVC) with AAC, the widely playable HLS ladder. The manifest's `CODECS` strings come from the encoder actually used.

### 7. Codecs and licences per flavour

| | `slim` | `full` (LGPL FFmpeg) | `gpl` (GPL FFmpeg) |
|---|---|---|---|
| Video transforms | no (`Unsupported`, startup error with `XI_VIDEO_TRANSFORM_ENABLED=true`) | yes | yes |
| Video decode | none | H.264, HEVC, VP8, VP9, AV1 (dav1d), MPEG-4, MJPEG, PNG | same |
| Video encode | none | VP9, VP8 (libvpx, BSD), AV1 (libaom, BSD) | + H.264 (libx264), HEVC (libx265) |
| Audio decode | none | AAC, MP3, MP2, Opus, Vorbis, FLAC, ALAC, AC-3, E-AC-3, PCM (native FFmpeg, LGPL) | same |
| Audio encode | none | AAC (native, LGPL), Opus (libopus, BSD-3-Clause), MP3 (LAME, LGPL-2.0+), FLAC, PCM | same |
| Containers | none | MP4/MOV/M4A, WebM/Matroska, MPEG-TS, HLS, DASH, MP3, Ogg/Opus, ADTS, WAV, FLAC | same |

Defaults: `mp4` = AV1 + AAC (full) or H.264 + AAC (gpl); `webm` = VP9 + Opus; `m4a` = AAC; `mp3` = MP3; `opus`/`ogg` = Opus. FDK-AAC (nonfree) is never built (`--enable-nonfree` fails the licence gate). The base build, licence gate and checks are in `docker/base/` (README "Full flavor") and the ADR 0006 amendment of 2026-10-04. Patents: MP3's have expired; AAC-LC, AC-3/E-AC-3 decode, H.264 and HEVC may be covered by pools in some jurisdictions; as for H.264/HEVC since 2026-09-27, the deployer reviews what applies.

### 8. Security and DoS posture

Video is the most expensive request a public endpoint can receive; the defaults assume hostile input.

- **Signed only by default.** `XI_VIDEO_ALLOW_UNSIGNED=false`: unsigned video transforms answer 403 even where unsigned image URLs are allowed; presets can whitelist named transforms.
- **Input limits before decoding:** `XI_VIDEO_MAX_BYTES`, `XI_VIDEO_MAX_DURATION`, `XI_VIDEO_MAX_PIXELS` (as today) plus stream count, and demuxer/decoder whitelists (`format_whitelist`, `codec_whitelist`) per job; `protocol_whitelist` is empty (custom AVIO only), HLS/DASH/concat demuxers are not built, so a source cannot make FFmpeg open another file or URL.
- **Output limits:** `XI_VIDEO_TRANSFORM_MAX_DURATION` (output seconds; default 60 s for Phase A synchronous jobs), max output width × height (default 1920 × 1080), max fps (60), max bitrate, max output bytes (the spool cap), and a job wall-clock timeout. Every limit is checked by the planner from the probe before an encoder opens (422), and again by the shim while running.
- **Cost accounting.** Admission charges a per-job cost estimate (output pixels × frames × codec factor) against an optional `XI_VIDEO_COST_BUDGET`, so ten 4K AV1 jobs do not get the same admission as ten 360p VP9 jobs. Metrics: queue depth, wait time, job duration, frames/s, bytes, coalesced waiters, aborts by reason.
- **Process isolation.** FFmpeg runs in-process like libvips: whitelists, fuzzing of the shim's entry points with the fixtures, and the `video` role keep a crash on the video fleet. If fuzzing or field reports show instability, the fallback is the subprocess sandbox of ADR 0011 (`internal/sandbox`) around a dedicated transcoder program; the job model (spool in, spool out) already fits it.

## Alternatives considered

- **A separate service (own repo or binary).** Clean isolation, but it would duplicate sources and their credentials, signing and encryption, allowlists, the dialect parsers (Cloudinary and Cloudflare URLs mix image and video options), presets, cache stores, config schema, metrics and the release train, and every security fix would ship twice. The `XI_ROLE` split gives the same operational isolation (separate deployments, scaling and crash domains) from one codebase. Rejected.
- **Go plugins (`plugin` package).** Linux-only, the plugin and host must be built with the identical toolchain and module versions, plugins cannot be unloaded, and cgo inside a plugin adds nothing over a build tag; the flavour difference (FFmpeg or not) is already a build tag plus runtime encoder probing. Rejected.
- **The `ffmpeg` CLI per job** (`exec.Command`). Easiest way to get every filter, and a natural crash boundary. But a filter graph assembled as a command line is an injection surface that needs escaping rules for every filter, cancellation is a signal, progress and errors are parsed from stderr, the CLI re-probes what the server already probed, limits move to rlimits, and HLS JIT segments would pay process start-up per segment. ADR 0010 measured the CLI at 2–6× the shim for single frames. The shim with a built (not parsed) graph keeps typed parameters, the abort flag and in-process metrics. Kept only as the sandboxed fallback above, not the default. Rejected as the main path.
- **libvips per frame** (decode with FFmpeg, transform with libvips, encode with FFmpeg). Reuses image options literally, but costs two colour conversions and a copy per frame, has no audio, and would put libvips work inside a long-running encode. Rejected; libvips renders overlays only.
- **GStreamer** (LGPL). A second large multimedia framework next to FFmpeg, with plugins whose licences vary per element and more C on untrusted input. Rejected.
- **go-astiav** or another Go binding: rejected for the reasons in ADR 0010 (third-party cgo outside rule 3, exposes FFmpeg objects to Go).

## Consequences

- The `full` and `gpl` base images grow by the audio encoders, libswresample and the extra muxers and filters (WP-120; sizes in `docker/base/README.md`); slim is unchanged.
- `internal/video/ffmpeg` grows a third C file; the leak, abort and fuzz tests of ADR 0010 extend to it.
- The config gains a `video` section (`XI_ROLE`, `XI_VIDEO_TRANSFORM_*`, `XI_VIDEO_WORKERS`, …), documented in `docs/CONFIG.md` by the WPs that add each setting.
- Work breakdown: ROADMAP M5 (WP-120 to WP-127).
