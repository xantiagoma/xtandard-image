// Declarations shared by the C files of this package (shim.c, encode.c,
// output.c, transcode.c) that name FFmpeg types. The Go side includes
// shim.h only, which names none.
#ifndef XI_FF_INTERNAL_H
#define XI_FF_INTERNAL_H

#include <stdatomic.h>
#include <stdint.h>

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>

// XiFFOut is a muxer's output: a growable buffer the muxer writes and
// seeks in through a custom AVIOContext (output.c), or past a threshold a
// spool file the Go side opened (WP-121b), so no file is ever opened by
// FFmpeg. A pass that rewrites the file (MP4 faststart, Matroska
// cues_to_front) reads it back through the context's io_open, which serves
// a read-only view of the buffer and refuses anything else.
typedef struct {
  uint8_t *data;
  int64_t size; // bytes written (the file)
  int64_t cap;  // bytes allocated
  int64_t pos;  // write position
  // max_bytes > 0 caps the file: a write past it fails and sets over.
  int64_t max_bytes;
  int over;
  // abort is the owner's flag: every read and write fails once it is set.
  const atomic_int *abort;
  // Spill to a file (WP-121b): with spill > 0, spill_fd is a descriptor
  // o owns (closed by xi_ff_out_free) and the output moves into that file
  // when it would grow past spill bytes; file is then set, data is freed
  // and every write, seek and read-back goes to the file (pwrite/pread at
  // the position, so the descriptor's offset is never used). spill_fd
  // means nothing while spill is 0 (zeroed structs never spill).
  int64_t spill;
  int spill_fd;
  int file;
} XiFFOut;

// xi_ff_out_attach gives oc a write/seek AVIOContext over o, the io_open
// and io_close2 callbacks that read o back, and oc->opaque = o. Returns 0
// or an AVERROR.
int xi_ff_out_attach(AVFormatContext *oc, XiFFOut *o);
// xi_ff_out_detach frees oc's AVIOContext (avformat_free_context does not
// free a custom one). The data stays in o.
void xi_ff_out_detach(AVFormatContext *oc);
// xi_ff_out_free frees o's data and closes its spill descriptor.
void xi_ff_out_free(XiFFOut *o);
// xi_ff_pread reads up to n bytes of fd at off (EINTR retried): the count,
// AVERROR_EOF at the end, or an AVERROR.
int xi_ff_pread(int fd, uint8_t *buf, int n, int64_t off);

// xi_ff_display reads a stream's display matrix: the clockwise rotation
// (0, 90, 180, 270) applied after the mirror, and the mirror (shim.c).
void xi_ff_display(const AVCodecParameters *par, int *rotation, int *hflip);

#endif
