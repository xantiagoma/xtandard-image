//go:build !cgo || !(linux || darwin)

package ort

import "context"

// Stubs for builds without cgo or on other systems: Load always fails with
// ErrUnavailable, so the types below are never constructed.

// Runtime is unavailable in this build.
type Runtime struct{}

// Load fails: ONNX Runtime is only loaded in cgo builds on Linux and macOS.
func Load(string) (*Runtime, error) { return nil, ErrUnavailable }

// Version returns "".
func (*Runtime) Version() string { return "" }

// Close does nothing.
func (*Runtime) Close() {}

// NewSession fails.
func (*Runtime) NewSession(string, SessionOptions) (*Session, error) { return nil, ErrUnavailable }

// Session is unavailable in this build.
type Session struct{}

// Inputs returns nil.
func (*Session) Inputs() []TensorInfo { return nil }

// Outputs returns nil.
func (*Session) Outputs() []TensorInfo { return nil }

// Close does nothing.
func (*Session) Close() {}

// Run fails.
func (*Session) Run(context.Context, []byte, []int64) ([]Tensor, error) { return nil, ErrUnavailable }

// Tensor is unavailable in this build.
type Tensor struct {
	Name  string
	Shape []int64
	Data  []float32
}

// Release does nothing.
func (*Tensor) Release() {}
