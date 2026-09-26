// Package ort is a minimal binding of the ONNX Runtime C API, loaded at run
// time with purego (github.com/ebitengine/purego, Apache-2.0) instead of cgo,
// so internal/vips stays the only cgo package (CLAUDE.md rule 3, ADR 0010).
//
// The library is dlopen'ed by [Load], never at init: a build or an image
// without libonnxruntime only fails there, with [ErrUnavailable]. The C API
// is a versioned table of function pointers (OrtGetApiBase()->GetApi(v));
// the indices this package calls are ABI-stable (ORT only appends to the
// table), so requesting an older API version works with every newer
// library.
//
// # Memory
//
// ORT never keeps a pointer to Go memory:
//
//   - input tensors are created over caller-provided C memory
//     (CreateTensorWithDataAsOrtValue keeps the pointer for the life of the
//     value, so [Session.Run] requires the input bytes to be C memory, such
//     as a vips.Buffer);
//   - input/output names are ORT-allocated C strings, kept per session;
//   - shapes, name arrays and out-parameters are Go memory that ORT only
//     reads or writes during the call (the cgo rules for transient
//     pointers);
//   - output tensors are ORT-allocated; [Tensor.Data] views them until
//     [Tensor.Release].
//
// No C type leaves the package: handles are opaque uintptrs in unexported
// fields.
package ort

import (
	"errors"
	"fmt"
)

// ErrUnavailable is returned by Load when ONNX Runtime cannot be used: a
// build without cgo, an unsupported OS, a missing or incompatible library.
var ErrUnavailable = errors.New("ort: onnx runtime unavailable")

// StatusError is an ONNX Runtime error (an OrtStatus), copied to Go memory.
type StatusError struct {
	Op   string // the C API function that failed
	Code int    // OrtErrorCode
	Msg  string
}

func (s *StatusError) Error() string {
	return fmt.Sprintf("ort: %s: code %d: %s", s.Op, s.Code, s.Msg)
}

// ErrorCode values of OrtErrorCode used by callers.
const (
	CodeFail       = 1
	CodeInvalidArg = 2
	CodeNoSuchFile = 3
	CodeNoModel    = 4
	CodeInvalidPB  = 7 // INVALID_PROTOBUF
)

// SessionOptions configures a session.
type SessionOptions struct {
	// IntraOpThreads is the number of threads one Run may use. 1 runs on the
	// calling goroutine's thread only (the default, like libvips).
	IntraOpThreads int
}

// TensorInfo describes one model input or output. Dimensions that the model
// leaves dynamic are negative.
type TensorInfo struct {
	Name  string
	Shape []int64
	// Float reports whether the element type is float32, the only one this
	// package feeds or reads.
	Float bool
}
