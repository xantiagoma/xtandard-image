package fetch

import (
	"errors"
	"fmt"
	"io"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// initialChunk is the first allocation when the body length is unknown;
// the buffer then doubles up to the byte limit.
const initialChunk = 64 << 10

// readBody streams r into a Buffer from alloc, enforcing maxBytes while
// reading. size is the declared length (-1 unknown). With a known size the
// buffer is allocated once and a body longer or shorter than declared is an
// upstream error; otherwise it grows in doubling chunks and is shrunk to
// the exact length at the end, so Bytes() is always exactly the content.
func readBody(alloc Allocator, r io.Reader, size, maxBytes int64) (Buffer, error) {
	if size > maxBytes {
		return nil, tooLarge(size, maxBytes)
	}
	if size == 0 {
		return nil, errs.New(errs.KindCorrupt, "empty source body")
	}
	if size > 0 {
		return readKnown(alloc, r, int(size))
	}
	return readUnknown(alloc, r, maxBytes)
}

func readKnown(alloc Allocator, r io.Reader, n int) (Buffer, error) {
	buf, err := allocate(alloc, n)
	if err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(r, buf.Bytes()); err != nil {
		buf.Release()
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, errs.Wrapf(errs.KindUpstreamError, err, "source body shorter than its declared %d bytes", n)
		}
		return nil, readError(err)
	}
	var extra [1]byte
	switch _, err := io.ReadFull(r, extra[:]); {
	case err == nil:
		buf.Release()
		return nil, errs.Newf(errs.KindUpstreamError, "source body longer than its declared %d bytes", n)
	case errors.Is(err, io.EOF):
		return buf, nil
	default:
		buf.Release()
		return nil, readError(err)
	}
}

func readUnknown(alloc Allocator, r io.Reader, maxBytes int64) (Buffer, error) {
	// One byte past the limit proves the body is too large.
	buf, n, full, err := readPrefix(alloc, r, maxBytes+1)
	if err != nil {
		return nil, err
	}
	if full {
		buf.Release()
		return nil, tooLarge(-1, maxBytes)
	}
	return exactBuffer(alloc, buf, n)
}

// readPrefix reads r into a Buffer from alloc, growing in doubling chunks,
// until EOF or limit bytes; full reports that limit bytes were read (the
// body may go on). The caller owns buf, of which n bytes are the data.
func readPrefix(alloc Allocator, r io.Reader, limit int64) (buf Buffer, n int, full bool, err error) {
	buf, err = allocate(alloc, int(min(initialChunk, limit)))
	if err != nil {
		return nil, 0, false, err
	}
	for {
		if n == len(buf.Bytes()) {
			if int64(n) >= limit {
				return buf, n, true, nil
			}
			bigger, err := allocate(alloc, int(min(2*int64(n), limit)))
			if err != nil {
				buf.Release()
				return nil, 0, false, err
			}
			copy(bigger.Bytes(), buf.Bytes()[:n])
			buf.Release()
			buf = bigger
		}
		m, err := r.Read(buf.Bytes()[n:])
		n += m
		if errors.Is(err, io.EOF) {
			return buf, n, false, nil
		}
		if err != nil {
			buf.Release()
			return nil, 0, false, readError(err)
		}
	}
}

// exactBuffer returns the n bytes of buf in a buffer of exactly n bytes
// (buf itself when it fits), taking over the caller's reference.
func exactBuffer(alloc Allocator, buf Buffer, n int) (Buffer, error) {
	switch {
	case n == 0:
		buf.Release()
		return nil, errs.New(errs.KindCorrupt, "empty source body")
	case n == len(buf.Bytes()):
		return buf, nil
	}
	exact, err := allocate(alloc, n)
	if err != nil {
		buf.Release()
		return nil, err
	}
	copy(exact.Bytes(), buf.Bytes()[:n])
	buf.Release()
	return exact, nil
}

func allocate(alloc Allocator, n int) (Buffer, error) {
	buf, err := alloc(n)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "allocate %d bytes", n)
	}
	return buf, nil
}

func tooLarge(size, maxBytes int64) error {
	if size >= 0 {
		return errs.Newf(errs.KindSourceTooLarge, "source declares %d bytes, limit %d", size, maxBytes)
	}
	return errs.Newf(errs.KindSourceTooLarge, "source body exceeds %d bytes", maxBytes)
}

// readError classifies a body read failure: a kind the source attached is
// kept, anything else is an upstream error.
func readError(err error) error {
	if _, ok := errors.AsType[*errs.Error](err); ok {
		return err
	}
	return errs.Wrap(errs.KindUpstreamError, fmt.Errorf("read source body: %w", err))
}
