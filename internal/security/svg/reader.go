package svg

import (
	"io"
)

// rawReader feeds encoding/xml one byte at a time (it implements io.ByteReader,
// so the decoder does not add its own bufio.Reader) while retaining every byte
// the decoder has consumed since the end of the previous token. Combined with
// Decoder.InputOffset this yields the exact source bytes of each token, which
// is what lets unchanged tokens be copied through verbatim.
//
// The decoder may read one byte past the end of a token and keep it in its
// own look-ahead; that byte stays in the window and belongs to the next token.
type rawReader struct {
	src   io.Reader
	buf   []byte
	start int   // buf index of the first byte not yet returned by take
	pos   int   // buf index of the next byte for ReadByte
	end   int   // buf index one past the last valid byte
	base  int64 // stream offset of buf[start]
	read  int64 // bytes read from src so far
	limit int64
	err   error // sticky error from src or the byte limit
}

const rawReaderInitialSize = 8 << 10

func newRawReader(src io.Reader, limit int64) *rawReader {
	return &rawReader{src: src, buf: make([]byte, rawReaderInitialSize), limit: limit}
}

// ReadByte implements io.ByteReader.
func (r *rawReader) ReadByte() (byte, error) {
	if r.pos == r.end {
		if err := r.fill(); err != nil {
			return 0, err
		}
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

// Read implements io.Reader for xml.NewDecoder's signature. The decoder only
// uses ReadByte, so a one-byte Read is enough.
func (r *rawReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = b
	return 1, nil
}

func (r *rawReader) fill() error {
	if r.err != nil {
		return r.err
	}
	if r.start > 0 {
		n := copy(r.buf, r.buf[r.start:r.end])
		r.pos -= r.start
		r.end = n
		r.start = 0
	}
	if r.end == len(r.buf) {
		// A single token larger than the window: grow. Bounded by limit.
		nb := make([]byte, 2*len(r.buf))
		copy(nb, r.buf[:r.end])
		r.buf = nb
	}
	for range 100 {
		n, err := r.src.Read(r.buf[r.end:])
		r.end += n
		r.read += int64(n)
		if r.read > r.limit {
			r.err = ErrTooLarge
			return r.err
		}
		if err != nil {
			r.err = err
		}
		if n > 0 {
			return nil
		}
		if err != nil {
			return err
		}
	}
	r.err = io.ErrNoProgress
	return r.err
}

// take returns the bytes from the end of the previous token up to stream
// offset off (the decoder's InputOffset after a token). The slice is only
// valid until the next ReadByte.
func (r *rawReader) take(off int64) []byte {
	n := int(off - r.base)
	b := r.buf[r.start : r.start+n]
	r.start += n
	r.base = off
	return b
}
