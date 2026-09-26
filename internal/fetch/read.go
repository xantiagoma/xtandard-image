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
	limit := maxBytes + 1 // one byte past the limit proves the body is too large
	buf, err := allocate(alloc, int(min(initialChunk, limit)))
	if err != nil {
		return nil, err
	}
	n := 0
	for {
		if n == len(buf.Bytes()) {
			if int64(n) >= limit {
				buf.Release()
				return nil, tooLarge(-1, maxBytes)
			}
			bigger, err := allocate(alloc, int(min(2*int64(n), limit)))
			if err != nil {
				buf.Release()
				return nil, err
			}
			copy(bigger.Bytes(), buf.Bytes()[:n])
			buf.Release()
			buf = bigger
		}
		m, err := r.Read(buf.Bytes()[n:])
		n += m
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			buf.Release()
			return nil, readError(err)
		}
	}
	switch {
	case int64(n) > maxBytes:
		buf.Release()
		return nil, tooLarge(-1, maxBytes)
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
