package vips

/*
#include "shim.h"
*/
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// initialReadCapacity is the first allocation of NewBufferFromReader when the
// size is unknown; the buffer doubles from there up to max.
const initialReadCapacity = 64 << 10

// Buffer is source bytes in C memory owned by libvips (a VipsBlob of
// vips_tracked_malloc'd memory), so libvips may keep pointers into it while
// decoding lazily. It is reference counted: NewBuffer* returns a Buffer with
// one reference, Retain adds one and the final Release frees the memory
// unless an image still holds its own C-side reference.
//
// Buffer implements the fetch.Buffer interface of ARCHITECTURE §7.
type Buffer struct {
	blob *C.VipsBlob
	data []byte // view over the content; C memory, never Go memory
	refs atomic.Int32
	// password opens a password-protected document (WithPassword): a
	// NUL-terminated C copy, wiped and freed by the final Release. nil:
	// none.
	password *C.char
}

// NewBuffer allocates an n-byte Buffer for the caller to fill through Bytes
// (for example with io.ReadFull when Content-Length is known).
func NewBuffer(n int) (*Buffer, error) {
	if n < 0 {
		return nil, errs.Newf(errs.KindInternal, "vips: negative buffer size %d", n)
	}
	blob := C.xi_blob_new(C.size_t(n))
	if blob == nil {
		return nil, allocError(n)
	}
	C.xi_blob_set_length(blob, C.size_t(n))
	return newBuffer(blob, n), nil
}

// NewBufferFromReader reads r to EOF into a new Buffer, growing the C
// allocation geometrically. More than limit bytes fail with
// errs.KindSourceTooLarge. Errors from r are returned wrapped, keeping their
// kind.
func NewBufferFromReader(r io.Reader, limit int64) (*Buffer, error) {
	if limit <= 0 {
		return nil, errs.Newf(errs.KindInternal, "vips: buffer limit must be > 0, got %d", limit)
	}
	capacity := min(int64(initialReadCapacity), limit)
	blob := C.xi_blob_new(C.size_t(capacity))
	if blob == nil {
		return nil, allocError(int(capacity))
	}
	var n int64
	for {
		if n == capacity {
			if capacity == limit {
				if overflows(r) {
					C.xi_blob_unref(blob)
					return nil, errs.Newf(errs.KindSourceTooLarge, "vips: source over %d bytes", limit)
				}
				break
			}
			next := min(capacity*2, limit)
			grown := C.xi_blob_grow(blob, C.size_t(n), C.size_t(next))
			if grown == nil {
				C.xi_blob_unref(blob)
				return nil, allocError(int(next))
			}
			blob, capacity = grown, next
		}
		// Reading into C memory is fine: Go code writes through the slice
		// and never retains it.
		dst := unsafe.Slice((*byte)(C.xi_blob_data(blob)), capacity)
		m, err := r.Read(dst[n:])
		n += int64(m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			C.xi_blob_unref(blob)
			return nil, fmt.Errorf("vips: read source: %w", err)
		}
	}
	C.xi_blob_set_length(blob, C.size_t(n))
	return newBuffer(blob, int(n)), nil
}

// overflows reports whether r has at least one more byte.
func overflows(r io.Reader) bool {
	var one [1]byte
	for {
		m, err := r.Read(one[:])
		if m > 0 {
			return true
		}
		if err != nil {
			return false
		}
	}
}

func newBuffer(blob *C.VipsBlob, n int) *Buffer {
	b := &Buffer{blob: blob}
	if n > 0 {
		b.data = unsafe.Slice((*byte)(C.xi_blob_data(blob)), n)
	} else {
		b.data = []byte{}
	}
	b.refs.Store(1)
	return b
}

func allocError(n int) error {
	return errs.Wrap(errs.KindInternal, &OpError{Op: "alloc", Msg: fmt.Sprintf("allocate %d bytes", n)})
}

// Bytes returns the content, backed by C memory. It is valid until the final
// Release; callers must not retain it beyond that and must not write to it
// once an image was loaded from the Buffer.
func (b *Buffer) Bytes() []byte { return b.data }

// Len returns the content length in bytes.
func (b *Buffer) Len() int { return len(b.data) }

// Retain adds a reference; each Retain needs one Release. Retaining a
// Buffer whose last reference is gone is a bug and has no effect.
func (b *Buffer) Retain() {
	for {
		n := b.refs.Load()
		if n <= 0 || b.refs.CompareAndSwap(n, n+1) {
			return
		}
	}
}

// Release drops a reference. The last one unrefs the blob; images loaded
// from it keep their own C-side reference until they are closed. Extra
// Releases are no-ops.
func (b *Buffer) Release() {
	for {
		n := b.refs.Load()
		if n <= 0 {
			return
		}
		if b.refs.CompareAndSwap(n, n-1) {
			if n == 1 {
				C.xi_secret_free(b.password)
				C.xi_blob_unref(b.blob)
				b.blob, b.data, b.password = nil, nil, nil
			}
			return
		}
	}
}

// ErrPasswordNUL is returned by WithPassword for a password containing a
// NUL byte, which PDFium's C string cannot carry.
var ErrPasswordNUL = errors.New("vips: document password contains a NUL byte")

// WithPassword returns a new Buffer, with one reference, over the same
// bytes as b (no copy) that opens password-protected documents with
// password: PDFium gets it when Probe, Load or Thumbnail load a PDF from the
// returned Buffer; other loaders never see it. A per-request view, so that
// buffers shared between requests (coalesced fetches) never carry one
// request's password. The password is copied to C memory, handed to libvips
// without an intermediate copy, wiped from the loader once it is built, and
// wiped and freed with the Buffer's final Release; the caller may clear its
// own slice as soon as WithPassword returns. An empty password returns b
// with an extra reference. b keeps its references either way.
//
// The copy PDFium keeps inside an open document is freed, not wiped, when
// the document closes (out of our reach).
func (b *Buffer) WithPassword(password []byte) (*Buffer, error) {
	if b == nil || b.blob == nil {
		return nil, errs.New(errs.KindInternal, "vips: password on a released buffer")
	}
	if len(password) == 0 {
		b.Retain()
		return b, nil
	}
	if bytes.IndexByte(password, 0) >= 0 {
		return nil, errs.Wrap(errs.KindInvalidURL, ErrPasswordNUL)
	}
	nb := newBuffer(C.xi_blob_ref(b.blob), len(b.data))
	// Transient Go pointer: C copies the bytes during the call.
	nb.password = C.xi_secret_new(unsafe.Pointer(&password[0]), C.size_t(len(password)))
	return nb, nil
}

// HasPassword reports whether b opens documents with a password
// (WithPassword).
func (b *Buffer) HasPassword() bool { return b != nil && b.password != nil }
