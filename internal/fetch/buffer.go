package fetch

import "sync/atomic"

// Buffer is a reference-counted block of original image bytes
// (docs/ARCHITECTURE.md §7). In production it is a vips.Buffer backed by
// C-allocated memory that libvips owns; the fetcher only sees this interface
// so it stays pure Go.
//
// A Buffer returned by an Allocator starts with one reference. Retain adds
// one, Release drops one, and the Release that drops the last reference
// frees the memory. Implementations must be safe for concurrent Retain and
// Release.
type Buffer interface {
	// Bytes returns the buffer's memory, exactly n bytes long for a buffer
	// allocated with n. The fetcher writes into it once while filling it,
	// before sharing it; after that it is a read-only view, valid until the
	// final Release.
	Bytes() []byte
	Retain()
	Release()
}

// Allocator returns a new Buffer of exactly n bytes (n ≥ 0) holding one
// reference. Production injects vips.NewBuffer; tests use GoAllocator.
type Allocator func(n int) (Buffer, error)

// GoAllocator is a pure-Go Allocator for tests and tools that do not link
// libvips. Its buffers live on the Go heap.
func GoAllocator(n int) (Buffer, error) {
	return NewGoBuffer(n, nil), nil
}

// GoBuffer is a Buffer on the Go heap. The final Release drops the slice
// and calls the optional onFree hook, which tests use to count frees.
type GoBuffer struct {
	b      []byte
	onFree func()
	refs   atomic.Int64
}

// NewGoBuffer returns a GoBuffer of n bytes with one reference. onFree, if
// non-nil, runs once when the last reference is released.
func NewGoBuffer(n int, onFree func()) *GoBuffer {
	g := &GoBuffer{b: make([]byte, n), onFree: onFree}
	g.refs.Store(1)
	return g
}

// Bytes implements Buffer. It returns nil after the final Release.
func (g *GoBuffer) Bytes() []byte { return g.b }

// Retain implements Buffer. Retaining a freed buffer is a bug and panics.
func (g *GoBuffer) Retain() {
	if g.refs.Add(1) <= 1 {
		panic("fetch: Retain on a released GoBuffer")
	}
}

// Release implements Buffer. Releasing more often than retained panics.
func (g *GoBuffer) Release() {
	switch n := g.refs.Add(-1); {
	case n == 0:
		g.b = nil
		if g.onFree != nil {
			g.onFree()
		}
	case n < 0:
		panic("fetch: GoBuffer released too often")
	}
}

// Refs reports the current reference count (for tests).
func (g *GoBuffer) Refs() int64 { return g.refs.Load() }
