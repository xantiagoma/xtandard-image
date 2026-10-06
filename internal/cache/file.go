package cache

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// FileBuffer is a read-only Buffer whose bytes live in a file: a video
// source or output spooled to disk (internal/spool, WP-121b) or a large
// entry of the disk store. It is read with ReadAt (and Reader, an
// io.ReadSeeker for http.ServeContent) without holding the file in memory;
// Bytes maps the file read-only (once, lazily) for the callers that need a
// slice, so it costs page cache, not heap. The file is closed, and free
// run, by the last Release.
type FileBuffer struct {
	f      *os.File
	off, n int64
	free   func()
	refs   atomic.Int64

	mapOnce sync.Once
	mapping []byte // the mapped region (page aligned); nil when not mapped
	view    []byte // the buffer's bytes inside mapping, or a heap copy
	mapped  atomic.Bool
}

// NewFileBuffer returns a FileBuffer over the n bytes of f at off,
// holding one reference; it takes f over. free, if non-nil, runs once
// after the last Release has closed f.
func NewFileBuffer(f *os.File, off, n int64, free func()) *FileBuffer {
	b := &FileBuffer{f: f, off: off, n: n, free: free}
	b.refs.Store(1)
	return b
}

// Len is the buffer's length in bytes.
func (b *FileBuffer) Len() int64 { return b.n }

// File is the underlying file and the offset of the buffer in it, for
// readers that use the descriptor themselves (the FFmpeg shim). Both are
// valid until the last Release; the file must not be written or closed.
func (b *FileBuffer) File() (*os.File, int64) { return b.f, b.off }

// ReadAt implements io.ReaderAt over the buffer's bytes.
func (b *FileBuffer) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("cache: negative offset")
	}
	if off >= b.n {
		return 0, io.EOF
	}
	if rem := b.n - off; int64(len(p)) > rem {
		p = p[:rem]
		n, err := b.f.ReadAt(p, b.off+off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return b.f.ReadAt(p, b.off+off)
}

// Reader returns a new reader over the buffer (each has its own offset).
func (b *FileBuffer) Reader() *io.SectionReader { return io.NewSectionReader(b, 0, b.n) }

// Bytes maps the buffer's bytes read-only and returns them; nil when the
// file cannot be read. Prefer Reader or ReadAt: they never fault the
// whole file into memory.
func (b *FileBuffer) Bytes() []byte {
	b.mapOnce.Do(func() {
		if b.n == 0 {
			return
		}
		m, v, err := mapFile(b.f, b.off, b.n)
		if err != nil {
			// No mmap (or it failed): one read into the heap.
			v = make([]byte, b.n)
			if _, err := b.f.ReadAt(v, b.off); err != nil {
				return
			}
			m = nil
		}
		b.mapping, b.view = m, v
		b.mapped.Store(true)
	})
	return b.view
}

// Mapped reports whether Bytes was called (tests check that a path never
// maps a spooled file).
func (b *FileBuffer) Mapped() bool { return b.mapped.Load() }

// Retain adds a reference; retaining a released buffer panics.
func (b *FileBuffer) Retain() {
	if b.refs.Add(1) <= 1 {
		panic("cache: Retain on a released file buffer")
	}
}

// Release drops a reference; the last one unmaps and closes the file and
// runs free. Releasing more often than retained panics.
func (b *FileBuffer) Release() {
	switch n := b.refs.Add(-1); {
	case n == 0:
		if b.mapping != nil {
			_ = unmapFile(b.mapping) // a failed munmap leaks address space only
		}
		b.mapping, b.view = nil, nil
		_ = b.f.Close() // read-only use: nothing to flush
		if b.free != nil {
			b.free()
		}
	case n < 0:
		panic("cache: file buffer released too often")
	}
}

// ReaderBody is a body read from a file rather than held in memory
// (*FileBuffer and the spool's files).
type ReaderBody interface {
	Len() int64
	ReadAt(p []byte, off int64) (int, error)
}

// BodyLen is b's length without mapping a file body.
func BodyLen(b interface{ Bytes() []byte }) int64 {
	if rb, ok := b.(ReaderBody); ok {
		return rb.Len()
	}
	return int64(len(b.Bytes()))
}

// BodyReader reads b from the start: a file body from its file, anything
// else from its bytes.
func BodyReader(b interface{ Bytes() []byte }) io.ReadSeeker {
	if rb, ok := b.(ReaderBody); ok {
		return io.NewSectionReader(rb, 0, rb.Len())
	}
	return bytes.NewReader(b.Bytes())
}

// IsFileBody reports whether b is read from a file.
func IsFileBody(b interface{ Bytes() []byte }) bool {
	_, ok := b.(ReaderBody)
	return ok
}

// WriteBody writes b to w: a file body is streamed from its file, any
// other body written in one call.
func WriteBody(w io.Writer, b interface{ Bytes() []byte }) error {
	if _, ok := b.(ReaderBody); ok {
		if _, err := io.Copy(w, BodyReader(b)); err != nil {
			return fmt.Errorf("cache: copy body: %w", err)
		}
		return nil
	}
	_, err := w.Write(b.Bytes())
	return err
}
