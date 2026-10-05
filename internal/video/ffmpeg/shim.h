// Hand-written C shim over FFmpeg (libavformat, libavcodec, libavutil,
// libswscale) for video thumbnails. It is compiled only with the `ffmpeg`
// build tag (ffmpeg.go) and is the only C that talks to FFmpeg; no FFmpeg
// type crosses into Go. ADR 0010.
//
// Memory: the source bytes and the output pixels are C memory owned by
// libvips (internal/vips Buffer); the shim reads the source through a custom
// AVIOContext and writes the pixels in place, and keeps neither pointer past
// xi_ff_close. Nothing here calls back into Go: cancellation is an atomic
// flag (xi_ff_abort) that the AVIO interrupt callback and the decode loop
// poll.
#ifndef XI_FF_SHIM_H
#define XI_FF_SHIM_H

#include <stddef.h>
#include <stdint.h>

// Result codes. The Go side maps each to an errs kind.
enum {
  XI_FF_OK = 0,
  XI_FF_ERR_UNSUPPORTED = 1, // no demuxer, no video stream or no decoder
  XI_FF_ERR_CORRUPT = 2,     // demuxing or decoding failed
  XI_FF_ERR_LIMIT = 3,       // frame larger than max_pixels
  XI_FF_ERR_ABORTED = 4,     // xi_ff_abort was called
  XI_FF_ERR_NOMEM = 5,
  XI_FF_ERR_NOFRAME = 6, // the stream ended before any frame was decoded
  XI_FF_ERR_INTERNAL = 7,
};

typedef struct XiFF XiFF;

// XiFFInfo describes the container and the chosen video stream.
typedef struct {
  char container[32]; // first name of the demuxer: "mov", "matroska", "avi"
  double duration;    // seconds; < 0 when unknown
  int64_t bit_rate;
  int nb_streams;
  int video_index; // index of the chosen video stream
  char codec[32];
  int width, height;                 // coded size
  int display_width, display_height; // sample aspect ratio applied
  double fps;                        // average frame rate; 0 when unknown
  int64_t frames;                    // container frame count; 0 when unknown
  int rotation;                      // clockwise degrees, after hflip
  int hflip;
  int alpha;
} XiFFInfo;

// XiFFStream describes one stream (imgproxy's video_streams entries).
typedef struct {
  int type; // AVMediaType: 0 video, 1 audio, 2 data, 3 subtitle, 4 attachment
  char codec[32];
  int64_t bit_rate;
  double fps;
  double duration; // seconds; < 0 when unknown
  char language[16];
  int width, height;
  int rotation;
  int hflip;
  int sample_rate;
  char layout[64];
  int channels; // audio channels (WP-123)
} XiFFStream;

// xi_ff_new allocates a handle over data[0..size) (C memory that must
// outlive the handle); max_pixels > 0 refuses larger frames with
// XI_FF_ERR_LIMIT. NULL when out of memory. Close it with xi_ff_close
// whatever xi_ff_open returns.
XiFF *xi_ff_new(const uint8_t *data, size_t size, int64_t max_pixels);
// xi_ff_open demuxes the data, reads the stream parameters
// (avformat_find_stream_info, one decoder thread per stream), picks the best
// video stream and opens its decoder with one thread. On failure err
// receives a message (errlen bytes, NUL-terminated).
int xi_ff_open(XiFF *h, char *err, size_t errlen);
void xi_ff_close(XiFF *h);
void xi_ff_info(XiFF *h, XiFFInfo *info);
int xi_ff_stream(XiFF *h, int i, XiFFStream *out);
// xi_ff_tag returns the i-th container tag (0 when i is out of range).
// The strings belong to the handle.
int xi_ff_tag(XiFF *h, int i, const char **key, const char **value);

// xi_ff_abort makes the running and every later call on h fail with
// XI_FF_ERR_ABORTED (the AVIO interrupt callback and the decode loop poll
// the flag). Safe from any thread; clear restores the handle.
void xi_ff_abort(XiFF *h, int on);
int xi_ff_aborted(XiFF *h);

// xi_ff_decode_at seeks to seconds (from the stream start) and decodes the
// keyframe at or before it (keyframe != 0) or the first frame whose
// timestamp reaches it; past the end, the last frame. The frame is kept in
// the handle; *got receives its timestamp in seconds and *key whether it is
// a keyframe. With hist != NULL, hist[256] receives the luma histogram of
// a copy at most 64 pixels wide.
int xi_ff_decode_at(XiFF *h, double seconds, int keyframe, double *got, int *key, uint32_t *hist,
    char *err, size_t errlen);
// xi_ff_keyframe_before stores in *at the index timestamp (decode order, in
// seconds; it may be negative) of the keyframe the demuxer's index has at or
// before seconds and returns 1, or returns 0 when the index does not know (no
// index yet, or none at all). Nothing is decoded.
int xi_ff_keyframe_before(XiFF *h, double seconds, double *at);
// xi_ff_keep moves the decoded frame into the handle's "best" slot.
void xi_ff_keep(XiFF *h);
// xi_ff_convert scales the decoded frame (best != 0: the best slot) to
// width × height and writes it as packed RGB (bands 3) or RGBA (bands 4)
// into dst (at least width × height × bands bytes; C memory).
int xi_ff_convert(XiFF *h, int best, uint8_t *dst, int width, int height, int bands, char *err,
    size_t errlen);

// xi_ff_live counts open handles (leak tests).
int xi_ff_live(void);
// xi_ff_heap_in_use is glibc's in-use heap (mallinfo2 uordblks), or 0
// where it is unavailable (another libc, jemalloc preloaded).
size_t xi_ff_heap_in_use(void);
// xi_ff_version is FFmpeg's version string ("8.1.3").
const char *xi_ff_version(void);

// ---------------------------------------------------------------------------
// Encoding (encode.c, WP-76): RGB(A) frames → VP9, AV1, H.264 or HEVC, muxed
// into WebM or MP4 in a buffer the handle owns. No temporary file: the
// muxer writes and seeks in that buffer through a custom AVIOContext, and
// MP4's faststart pass reads it back through the context's io_open.

// Encoders (bit mask of xi_ff_encoders) and containers.
enum {
  XI_FF_ENC_VP9 = 1,
  XI_FF_ENC_AV1 = 2,
  XI_FF_ENC_H264 = 4,
  XI_FF_ENC_HEVC = 8,
};
enum {
  XI_FF_MUX_MP4 = 1,
  XI_FF_MUX_WEBM = 2,
};

typedef struct {
  int codec;         // one XI_FF_ENC_* value
  int container;     // XI_FF_MUX_*
  int width, height; // even
  int alpha;         // yuva420p (VP9 in WebM)
  int bands;         // 3 (RGB) or 4 (RGBA) input frames
  int crf;
  int speed;         // cpu-used (VP9, AV1)
  char preset[16];   // x264/x265 preset
  double fps;        // average frame rate: a rate-control hint
  int64_t max_bytes; // the muxed output may not grow past it; 0 = unlimited
} XiFFEncParams;

typedef struct XiFFEnc XiFFEnc;

// xi_ff_encoders probes the linked FFmpeg (avcodec_find_encoder_by_name, and
// the muxer each codec goes into) and returns the XI_FF_ENC_* it can use.
int xi_ff_encoders(void);
// xi_ff_enc_new opens the encoder and the muxer and writes the container
// header. NULL on failure, with *code an XI_FF_* result and err a message.
XiFFEnc *xi_ff_enc_new(const XiFFEncParams *p, int *code, char *err, size_t errlen);
// xi_ff_enc_frame encodes one frame: width × height × bands packed bytes at
// pix (C memory, read during the call only), shown at pts_ms for
// duration_ms. Timestamps must increase.
int xi_ff_enc_frame(
    XiFFEnc *e, const uint8_t *pix, int64_t pts_ms, int64_t duration_ms, char *err, size_t errlen);
// xi_ff_enc_finish flushes the encoder and writes the trailer; the output is
// then xi_ff_enc_size bytes, copied out by xi_ff_enc_copy.
int xi_ff_enc_finish(XiFFEnc *e, char *err, size_t errlen);
size_t xi_ff_enc_size(XiFFEnc *e);
void xi_ff_enc_copy(XiFFEnc *e, uint8_t *dst);
// xi_ff_enc_abort makes the running and later calls fail with
// XI_FF_ERR_ABORTED (checked per frame, per packet and by the muxer's I/O).
void xi_ff_enc_abort(XiFFEnc *e, int on);
void xi_ff_enc_free(XiFFEnc *e);
// xi_ff_live_encoders counts open encoders (leak tests).
int xi_ff_live_encoders(void);

// ---------------------------------------------------------------------------
// Video transformations (transcode.c, WP-121): a source in C memory ->
// demux -> decode -> libavfilter chain per stream -> encode -> mux into a
// buffer the handle owns, handed over with xi_tx_take.

typedef struct XiTx XiTx;

// XiTxInfo describes a source for the plan (internal/video.TranscodeInput).
typedef struct {
  char container[32];
  double duration;   // seconds; < 0 when unknown
  double start_time; // seconds: the container's first timestamp
  int video;         // a video stream was found and its decoder opened
  char vcodec[32];
  int width, height;
  int sar_num, sar_den;
  int rotation, hflip; // display matrix: mirror, then clockwise rotation
  double fps;          // average frame rate; 0 when unknown
  int audio;           // an audio stream was found
  char acodec[32];
  int audio_decodable; // and this build decodes it
  int sample_rate, channels;
} XiTxInfo;

// Audio encoders of XiTxParams.acodec.
enum {
  XI_TX_AAC = 1,
  XI_TX_OPUS = 2,
  XI_TX_MP3 = 3,
  XI_TX_FLAC = 4, // WP-123
  XI_TX_PCM = 5,  // pcm_s16le (WAV)
};

// XiTxOverlay is one overlay drawn over the video chain's output (WP-124),
// in order. Strings are NUL-terminated and are AVOption values of filters
// the shim creates, never filter-graph syntax.
typedef struct {
  // Image overlay (rgba != NULL): straight 8-bit RGBA, width x height x 4
  // bytes of C memory, read during xi_tx_run only (copied into a frame),
  // drawn at x, y inside the frame.
  const uint8_t *rgba;
  int x, y, width, height;
  // Timecode (rgba == NULL): drawtext options.
  char text[16];
  char tx[64], ty[64]; // position expressions
  char color[16];      // "0xRRGGBB"
  char alpha[192];     // opacity expression
  int size, shadow;
  // Timeline expression ("" = every frame) and fades on the output
  // timeline, microseconds (a duration 0 is no fade).
  char enable[64];
  int64_t fade_in_start, fade_in_dur, fade_out_start, fade_out_dur;
} XiTxOverlay;

#define XI_TX_MAX_OVERLAYS 8

// XiTxParams is one plan (internal/video.TranscodePlan); strings are
// NUL-terminated or fill their array.
typedef struct {
  char muxer[16]; // "mp4", "webm", "ogg", "mp3", "flac", "wav", "rawvideo"
  int faststart;
  int64_t max_bytes; // the output may not grow past it; 0 = unlimited
  double start, end; // the cut on the source timeline, seconds; end <= 0 = none
  int threads;
  // Video (vcodec: an XI_FF_ENC_* value; 0 = no video stream).
  int vcodec;
  double fps; // rate-control hint when the chain sets no rate
  int crf, speed;
  char preset[16];
  int64_t max_bitrate;
  int gop;
  char vfilters[1024];
  int noverlays;
  XiTxOverlay overlays[XI_TX_MAX_OVERLAYS];
  // Audio (acodec: an XI_TX_* value; 0 = no audio stream).
  int acodec;
  int64_t abitrate;
  char afilters[512];
  // Waveform picture (WP-123): afilters turns the audio into one RGBA
  // video frame, written by the rawvideo encoder; vcodec and acodec are 0.
  int waveform;
} XiTxParams;

// Capabilities (xi_tx_caps).
enum {
  XI_TX_CAP_AAC = 1,
  XI_TX_CAP_OPUS = 2,
  XI_TX_CAP_MP3 = 4,
  XI_TX_CAP_MUX_MP4 = 8,
  XI_TX_CAP_MUX_WEBM = 16,
  XI_TX_CAP_MUX_OGG = 32,
  XI_TX_CAP_MUX_MP3 = 64,
  XI_TX_CAP_VFILTERS = 128,
  XI_TX_CAP_AFILTERS = 256,
  XI_TX_CAP_OVERLAYS = 512, // overlay and fade (image and text overlays)
  XI_TX_CAP_DRAWTEXT = 1024, // drawtext (timecodes)
  XI_TX_CAP_FLAC = 2048,       // WP-123: flac encoder
  XI_TX_CAP_PCM = 4096,        // pcm_s16le encoder
  XI_TX_CAP_MUX_FLAC = 8192,   // flac muxer
  XI_TX_CAP_MUX_WAV = 16384,   // wav muxer
  XI_TX_CAP_AEFFECTS = 32768,  // volume, loudnorm, afade
  XI_TX_CAP_VFADE = 65536,     // fade
  XI_TX_CAP_WAVEFORM = 131072, // showwavespic, rawvideo encoder and muxer
};

// xi_tx_caps probes the audio encoders, muxers and filters transcodes use
// (the video encoders are xi_ff_encoders').
int xi_tx_caps(void);
// xi_tx_new allocates a handle over data[0..size) (C memory that must
// outlive the handle); decoders refuse frames over max_pixels (> 0) and run
// threads threads. NULL when out of memory; free it with xi_tx_free.
XiTx *xi_tx_new(const uint8_t *data, size_t size, int64_t max_pixels, int threads);
// xi_tx_open demuxes the source, probes it and opens its decoders.
int xi_tx_open(XiTx *h, XiTxInfo *info, char *err, size_t errlen);
// xi_tx_run transcodes the opened source once. p is read during the call
// only.
int xi_tx_run(XiTx *h, const XiTxParams *p, char *err, size_t errlen);
// xi_tx_take hands the output over (NULL when empty): the caller frees it
// with xi_tx_out_free.
uint8_t *xi_tx_take(XiTx *h, int64_t *size);
void xi_tx_out_free(uint8_t *data);
// xi_tx_abort makes the running and later calls fail with
// XI_FF_ERR_ABORTED (polled per packet, per frame and by all I/O).
void xi_tx_abort(XiTx *h, int on);
void xi_tx_free(XiTx *h);
// xi_tx_live and xi_tx_live_outputs count open handles and outputs not
// freed yet (leak tests).
int xi_tx_live(void);
int xi_tx_live_outputs(void);

// Shared by shim.c, encode.c and transcode.c.
void xi_ff_setup(void);
void xi_ff_set_err(char *err, size_t errlen, const char *what, int averr);

#endif
