package ffmpeg

import (
	"sync/atomic"
	"unsafe"
)

// Output is a transcoded file in C memory (FFmpeg's allocator), handed
// over by the muxer without a copy. It is reference counted like a
// vips.Buffer: Bytes is valid until the last Release, which frees the
// memory. It satisfies cache.Buffer and server.Blob, so the result cache
// and the response share it.
type Output struct {
	p    unsafe.Pointer
	n    int
	refs atomic.Int64
	free func(unsafe.Pointer)
}

// newOutput wraps n bytes at p with one reference; free releases them.
func newOutput(p unsafe.Pointer, n int, free func(unsafe.Pointer)) *Output {
	o := &Output{p: p, n: n, free: free}
	o.refs.Store(1)
	return o
}

// Bytes is the file. It must not be written.
func (o *Output) Bytes() []byte {
	if o == nil || o.p == nil {
		return nil
	}
	return unsafe.Slice((*byte)(o.p), o.n)
}

// Len is the file's size in bytes.
func (o *Output) Len() int {
	if o == nil {
		return 0
	}
	return o.n
}

// Retain adds a reference.
func (o *Output) Retain() { o.refs.Add(1) }

// Release drops a reference; the last one frees the memory (extra
// releases do nothing).
func (o *Output) Release() {
	if o.refs.Add(-1) == 0 {
		p := o.p
		o.p = nil
		o.free(p)
	}
}
