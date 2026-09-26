//go:build !cgo

package vips

import (
	"context"
	"io"
)

// Stubs for CGO_ENABLED=0 builds: the module keeps compiling and every
// operation fails with ErrUnavailable. The real implementation is in the
// cgo files of this package.

// Init fails: libvips is not linked.
func Init(Config) error { return ErrUnavailable }

// Shutdown does nothing.
func Shutdown() {}

// Run fails without calling fn.
func Run(context.Context, func() error) error { return ErrUnavailable }

// ReadMemStats returns zeros.
func ReadMemStats() MemStats { return MemStats{} }

// Buffer is unavailable without cgo.
type Buffer struct{}

// NewBuffer fails.
func NewBuffer(int) (*Buffer, error) { return nil, ErrUnavailable }

// NewBufferFromReader fails.
func NewBufferFromReader(io.Reader, int64) (*Buffer, error) { return nil, ErrUnavailable }

// Bytes returns nil.
func (*Buffer) Bytes() []byte { return nil }

// Len returns 0.
func (*Buffer) Len() int { return 0 }

// Retain does nothing.
func (*Buffer) Retain() {}

// Release does nothing.
func (*Buffer) Release() {}

// Image is unavailable without cgo.
type Image struct{}

// LoadThumbnail fails.
func LoadThumbnail(context.Context, *Buffer, int, int) (*Image, error) {
	return nil, ErrUnavailable
}

// Width returns 0.
func (*Image) Width() int { return 0 }

// Height returns 0.
func (*Image) Height() int { return 0 }

// Close does nothing.
func (*Image) Close() {}

// SaveJPEG fails.
func (*Image) SaveJPEG(context.Context, int) (*Blob, error) { return nil, ErrUnavailable }

// Blob is unavailable without cgo.
type Blob struct{}

// Bytes returns nil.
func (*Blob) Bytes() []byte { return nil }

// Len returns 0.
func (*Blob) Len() int { return 0 }

// Release does nothing.
func (*Blob) Release() {}
