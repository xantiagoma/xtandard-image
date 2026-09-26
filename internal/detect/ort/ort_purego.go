//go:build cgo && (linux || darwin)

package ort

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// apiVersion is the OrtApi version requested from the library. Every
// function below exists since ORT 1.6; 17 (ORT 1.17, 2024) is the oldest
// runtime we accept. GetApi returns the same append-only table for any
// version up to the library's own, so a newer library (the base image ships
// 1.30) serves it unchanged.
const apiVersion = 17

// OrtApi table indices (offsetof(OrtApi, f) / sizeof(void*)), printed from
// onnxruntime_c_api.h 1.30 by a C program in the full builder
// (docs/adr/0010). ORT never reorders the table.
const (
	apiGetErrorCode                     = 1
	apiGetErrorMessage                  = 2
	apiCreateEnv                        = 3
	apiCreateSession                    = 7
	apiRun                              = 9
	apiCreateSessionOptions             = 10
	apiDisableCPUMemArena               = 19
	apiSetSessionLogSeverityLevel       = 22
	apiSetSessionGraphOptimizationLevel = 23
	apiSetIntraOpNumThreads             = 24
	apiSetInterOpNumThreads             = 25
	apiSessionGetInputCount             = 30
	apiSessionGetOutputCount            = 31
	apiSessionGetInputTypeInfo          = 33
	apiSessionGetOutputTypeInfo         = 34
	apiSessionGetInputName              = 36
	apiSessionGetOutputName             = 37
	apiCreateRunOptions                 = 39
	apiRunOptionsSetTerminate           = 46
	apiCreateTensorWithDataAsOrtValue   = 49
	apiGetTensorMutableData             = 51
	apiCastTypeInfoToTensorInfo         = 55
	apiGetTensorElementType             = 60
	apiGetDimensionsCount               = 61
	apiGetDimensions                    = 62
	apiGetTensorShapeElementCount       = 64
	apiGetTensorTypeAndShape            = 65
	apiCreateCPUMemoryInfo              = 69
	apiAllocatorFree                    = 76
	apiGetAllocatorWithDefaultOptions   = 78
	apiReleaseEnv                       = 92
	apiReleaseStatus                    = 93
	apiReleaseMemoryInfo                = 94
	apiReleaseSession                   = 95
	apiReleaseValue                     = 96
	apiReleaseRunOptions                = 97
	apiReleaseTypeInfo                  = 98
	apiReleaseTensorTypeAndShapeInfo    = 99
	apiReleaseSessionOptions            = 100
	apiAddSessionConfigEntry            = 130

	apiTableLen = apiAddSessionConfigEntry + 1
)

// C enum values (onnxruntime_c_api.h).
const (
	loggingLevelError  = 3  // ORT_LOGGING_LEVEL_ERROR
	graphOptEnableAll  = 99 // ORT_ENABLE_ALL
	arenaAllocator     = 1  // OrtArenaAllocator
	memTypeDefault     = 0  // OrtMemTypeDefault
	elementTypeFloat32 = 1  // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT
)

// apiBase mirrors struct OrtApiBase.
type apiBase struct {
	getAPI           uintptr
	getVersionString uintptr
}

// api holds the C functions this package calls, bound with
// purego.RegisterFunc. Pointers to OrtXxx objects are uintptr; OrtStatus*
// results are uintptr (0 is success).
type api struct {
	getErrorCode    func(status uintptr) int32
	getErrorMessage func(status uintptr) string
	releaseStatus   func(status uintptr)

	createEnv  func(level int32, logID string, out *uintptr) uintptr
	releaseEnv func(env uintptr)

	createSessionOptions             func(out *uintptr) uintptr
	disableCPUMemArena               func(so uintptr) uintptr
	setSessionLogSeverityLevel       func(so uintptr, level int32) uintptr
	setSessionGraphOptimizationLevel func(so uintptr, level int32) uintptr
	setIntraOpNumThreads             func(so uintptr, n int32) uintptr
	setInterOpNumThreads             func(so uintptr, n int32) uintptr
	addSessionConfigEntry            func(so uintptr, key, value string) uintptr
	releaseSessionOptions            func(so uintptr)

	createSession  func(env uintptr, path string, so uintptr, out *uintptr) uintptr
	releaseSession func(s uintptr)

	sessionGetInputCount     func(s uintptr, out *uintptr) uintptr
	sessionGetOutputCount    func(s uintptr, out *uintptr) uintptr
	sessionGetInputTypeInfo  func(s, index uintptr, out *uintptr) uintptr
	sessionGetOutputTypeInfo func(s, index uintptr, out *uintptr) uintptr
	sessionGetInputName      func(s, index, alloc uintptr, out **byte) uintptr
	sessionGetOutputName     func(s, index, alloc uintptr, out **byte) uintptr

	castTypeInfoToTensorInfo     func(ti uintptr, out *uintptr) uintptr
	getTensorElementType         func(info uintptr, out *int32) uintptr
	getDimensionsCount           func(info uintptr, out *uintptr) uintptr
	getDimensions                func(info uintptr, dims *int64, n uintptr) uintptr
	getTensorShapeElementCount   func(info uintptr, out *uintptr) uintptr
	getTensorTypeAndShape        func(v uintptr, out *uintptr) uintptr
	releaseTypeInfo              func(ti uintptr)
	releaseTensorTypeAndShapeInf func(info uintptr)

	createCPUMemoryInfo func(allocType, memType int32, out *uintptr) uintptr
	releaseMemoryInfo   func(mi uintptr)

	getAllocatorWithDefaultOptions func(out *uintptr) uintptr
	allocatorFree                  func(alloc uintptr, p *byte) uintptr

	createTensorWithDataAsOrtValue func(mi uintptr, data unsafe.Pointer, size uintptr, shape *int64, dims uintptr, typ int32, out *uintptr) uintptr
	getTensorMutableData           func(v uintptr, out *unsafe.Pointer) uintptr
	releaseValue                   func(v uintptr)

	createRunOptions       func(out *uintptr) uintptr
	runOptionsSetTerminate func(ro uintptr) uintptr
	releaseRunOptions      func(ro uintptr)

	run func(s, ro uintptr, inNames **byte, inputs *uintptr, nIn uintptr, outNames **byte, nOut uintptr, outputs *uintptr) uintptr
}

// Runtime is a loaded ONNX Runtime: the library, its API table, one OrtEnv
// and the CPU memory info. It is safe for concurrent use. Close it after
// every Session.
type Runtime struct {
	api     api
	env     uintptr
	mem     uintptr
	alloc   uintptr // default allocator; owned by ORT, never released
	version string

	closeOnce sync.Once
}

// Load dlopens the ONNX Runtime shared library at path (a file name is
// looked up by the dynamic linker, e.g. "libonnxruntime.so.1"), binds the
// API table and creates the environment. Every failure to find or bind the
// library wraps ErrUnavailable. The library is never unloaded (dlclose of
// ORT is unsafe while its threads exist); Close releases the environment.
func Load(path string) (*Runtime, error) {
	lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("%w: dlopen %s: %w", ErrUnavailable, path, err)
	}
	sym, err := purego.Dlsym(lib, "OrtGetApiBase")
	if err != nil {
		return nil, fmt.Errorf("%w: %s has no OrtGetApiBase: %w", ErrUnavailable, path, err)
	}
	var getAPIBase func() *apiBase
	purego.RegisterFunc(&getAPIBase, sym)
	base := getAPIBase()
	if base == nil || base.getAPI == 0 || base.getVersionString == 0 {
		return nil, fmt.Errorf("%w: OrtGetApiBase returned no table", ErrUnavailable)
	}
	var (
		getAPI     func(version uint32) *[apiTableLen]uintptr
		getVersion func() string
	)
	purego.RegisterFunc(&getAPI, base.getAPI)
	purego.RegisterFunc(&getVersion, base.getVersionString)
	version := getVersion()
	table := getAPI(apiVersion)
	if table == nil {
		return nil, fmt.Errorf("%w: onnx runtime %s does not serve API version %d", ErrUnavailable, version, apiVersion)
	}

	rt := &Runtime{version: version}
	bind(&rt.api, table)
	a := &rt.api
	if err := a.check("CreateEnv", a.createEnv(loggingLevelError, "xtandard-image", &rt.env)); err != nil {
		return nil, err
	}
	if err := a.check("CreateCpuMemoryInfo", a.createCPUMemoryInfo(arenaAllocator, memTypeDefault, &rt.mem)); err != nil {
		a.releaseEnv(rt.env)
		return nil, err
	}
	if err := a.check("GetAllocatorWithDefaultOptions", a.getAllocatorWithDefaultOptions(&rt.alloc)); err != nil {
		a.releaseMemoryInfo(rt.mem)
		a.releaseEnv(rt.env)
		return nil, err
	}
	return rt, nil
}

// bind registers every function of a from the API table.
func bind(a *api, t *[apiTableLen]uintptr) {
	reg := func(fptr any, index int) { purego.RegisterFunc(fptr, t[index]) }
	reg(&a.getErrorCode, apiGetErrorCode)
	reg(&a.getErrorMessage, apiGetErrorMessage)
	reg(&a.releaseStatus, apiReleaseStatus)
	reg(&a.createEnv, apiCreateEnv)
	reg(&a.releaseEnv, apiReleaseEnv)
	reg(&a.createSessionOptions, apiCreateSessionOptions)
	reg(&a.disableCPUMemArena, apiDisableCPUMemArena)
	reg(&a.setSessionLogSeverityLevel, apiSetSessionLogSeverityLevel)
	reg(&a.setSessionGraphOptimizationLevel, apiSetSessionGraphOptimizationLevel)
	reg(&a.setIntraOpNumThreads, apiSetIntraOpNumThreads)
	reg(&a.setInterOpNumThreads, apiSetInterOpNumThreads)
	reg(&a.addSessionConfigEntry, apiAddSessionConfigEntry)
	reg(&a.releaseSessionOptions, apiReleaseSessionOptions)
	reg(&a.createSession, apiCreateSession)
	reg(&a.releaseSession, apiReleaseSession)
	reg(&a.sessionGetInputCount, apiSessionGetInputCount)
	reg(&a.sessionGetOutputCount, apiSessionGetOutputCount)
	reg(&a.sessionGetInputTypeInfo, apiSessionGetInputTypeInfo)
	reg(&a.sessionGetOutputTypeInfo, apiSessionGetOutputTypeInfo)
	reg(&a.sessionGetInputName, apiSessionGetInputName)
	reg(&a.sessionGetOutputName, apiSessionGetOutputName)
	reg(&a.castTypeInfoToTensorInfo, apiCastTypeInfoToTensorInfo)
	reg(&a.getTensorElementType, apiGetTensorElementType)
	reg(&a.getDimensionsCount, apiGetDimensionsCount)
	reg(&a.getDimensions, apiGetDimensions)
	reg(&a.getTensorShapeElementCount, apiGetTensorShapeElementCount)
	reg(&a.getTensorTypeAndShape, apiGetTensorTypeAndShape)
	reg(&a.releaseTypeInfo, apiReleaseTypeInfo)
	reg(&a.releaseTensorTypeAndShapeInf, apiReleaseTensorTypeAndShapeInfo)
	reg(&a.createCPUMemoryInfo, apiCreateCPUMemoryInfo)
	reg(&a.releaseMemoryInfo, apiReleaseMemoryInfo)
	reg(&a.getAllocatorWithDefaultOptions, apiGetAllocatorWithDefaultOptions)
	reg(&a.allocatorFree, apiAllocatorFree)
	reg(&a.createTensorWithDataAsOrtValue, apiCreateTensorWithDataAsOrtValue)
	reg(&a.getTensorMutableData, apiGetTensorMutableData)
	reg(&a.releaseValue, apiReleaseValue)
	reg(&a.createRunOptions, apiCreateRunOptions)
	reg(&a.runOptionsSetTerminate, apiRunOptionsSetTerminate)
	reg(&a.releaseRunOptions, apiReleaseRunOptions)
	reg(&a.run, apiRun)
}

// check turns an OrtStatus into a *StatusError and releases it.
func (a *api) check(op string, status uintptr) error {
	if status == 0 {
		return nil
	}
	err := &StatusError{Op: op, Code: int(a.getErrorCode(status)), Msg: a.getErrorMessage(status)}
	a.releaseStatus(status)
	return err
}

// Version returns the library's version string, e.g. "1.30.0".
func (rt *Runtime) Version() string { return rt.version }

// Close releases the environment. Every Session must be closed first.
func (rt *Runtime) Close() {
	rt.closeOnce.Do(func() {
		rt.api.releaseMemoryInfo(rt.mem)
		rt.api.releaseEnv(rt.env)
	})
}

// Session is a loaded model. Run is safe for concurrent use (ORT sessions
// are thread-safe for Run); Close is not, and must follow every Run.
type Session struct {
	rt       *Runtime
	s        uintptr
	inNames  []*byte // ORT-allocated C strings
	outNames []*byte
	inputs   []TensorInfo
	outputs  []TensorInfo

	closeOnce sync.Once
}

// NewSession loads the ONNX model at path.
func (rt *Runtime) NewSession(path string, opts SessionOptions) (*Session, error) {
	a := &rt.api
	var so uintptr
	if err := a.check("CreateSessionOptions", a.createSessionOptions(&so)); err != nil {
		return nil, err
	}
	defer a.releaseSessionOptions(so)
	threads := int32(max(opts.IntraOpThreads, 1)) //nolint:gosec // G115: thread counts are validated small ints
	for _, step := range []struct {
		op     string
		status func() uintptr
	}{
		{"SetIntraOpNumThreads", func() uintptr { return a.setIntraOpNumThreads(so, threads) }},
		{"SetInterOpNumThreads", func() uintptr { return a.setInterOpNumThreads(so, 1) }},
		{"SetSessionGraphOptimizationLevel", func() uintptr { return a.setSessionGraphOptimizationLevel(so, graphOptEnableAll) }},
		{"SetSessionLogSeverityLevel", func() uintptr { return a.setSessionLogSeverityLevel(so, loggingLevelError) }},
		// No arena: memory goes back to the allocator after every Run, so
		// the process footprint does not ratchet up to the largest input.
		{"DisableCpuMemArena", func() uintptr { return a.disableCPUMemArena(so) }},
		// Idle intra-op threads sleep instead of spinning (only matters
		// with IntraOpThreads > 1).
		{"AddSessionConfigEntry", func() uintptr { return a.addSessionConfigEntry(so, "session.intra_op.allow_spinning", "0") }},
	} {
		if err := a.check(step.op, step.status()); err != nil {
			return nil, err
		}
	}
	s := &Session{rt: rt}
	if err := a.check("CreateSession", a.createSession(rt.env, path, so, &s.s)); err != nil {
		return nil, err
	}
	if err := s.describe(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// describe reads the input and output names, shapes and element types.
func (s *Session) describe() error {
	a := &s.rt.api
	var nIn, nOut uintptr
	if err := a.check("SessionGetInputCount", a.sessionGetInputCount(s.s, &nIn)); err != nil {
		return err
	}
	if err := a.check("SessionGetOutputCount", a.sessionGetOutputCount(s.s, &nOut)); err != nil {
		return err
	}
	for i := range nIn {
		var name *byte
		if err := a.check("SessionGetInputName", a.sessionGetInputName(s.s, i, s.rt.alloc, &name)); err != nil {
			return err
		}
		s.inNames = append(s.inNames, name)
		var ti uintptr
		if err := a.check("SessionGetInputTypeInfo", a.sessionGetInputTypeInfo(s.s, i, &ti)); err != nil {
			return err
		}
		info, err := s.tensorInfo(ti, name)
		a.releaseTypeInfo(ti)
		if err != nil {
			return err
		}
		s.inputs = append(s.inputs, info)
	}
	for i := range nOut {
		var name *byte
		if err := a.check("SessionGetOutputName", a.sessionGetOutputName(s.s, i, s.rt.alloc, &name)); err != nil {
			return err
		}
		s.outNames = append(s.outNames, name)
		var ti uintptr
		if err := a.check("SessionGetOutputTypeInfo", a.sessionGetOutputTypeInfo(s.s, i, &ti)); err != nil {
			return err
		}
		info, err := s.tensorInfo(ti, name)
		a.releaseTypeInfo(ti)
		if err != nil {
			return err
		}
		s.outputs = append(s.outputs, info)
	}
	return nil
}

// tensorInfo reads a type info's shape and element type. The tensor info
// belongs to ti.
func (s *Session) tensorInfo(ti uintptr, name *byte) (TensorInfo, error) {
	a := &s.rt.api
	info := TensorInfo{Name: goString(name)}
	var tsi uintptr
	if err := a.check("CastTypeInfoToTensorInfo", a.castTypeInfoToTensorInfo(ti, &tsi)); err != nil {
		return info, err
	}
	if tsi == 0 {
		return info, fmt.Errorf("ort: %s is not a tensor", info.Name)
	}
	var typ int32
	if err := a.check("GetTensorElementType", a.getTensorElementType(tsi, &typ)); err != nil {
		return info, err
	}
	info.Float = typ == elementTypeFloat32
	shape, err := a.shape(tsi)
	info.Shape = shape
	return info, err
}

// shape reads the dimensions of an OrtTensorTypeAndShapeInfo.
func (a *api) shape(tsi uintptr) ([]int64, error) {
	var n uintptr
	if err := a.check("GetDimensionsCount", a.getDimensionsCount(tsi, &n)); err != nil {
		return nil, err
	}
	if n == 0 {
		return []int64{}, nil
	}
	if n > 16 {
		return nil, fmt.Errorf("ort: tensor has %d dimensions", n)
	}
	dims := make([]int64, n)
	return dims, a.check("GetDimensions", a.getDimensions(tsi, &dims[0], n))
}

// Inputs describes the model inputs.
func (s *Session) Inputs() []TensorInfo { return s.inputs }

// Outputs describes the model outputs, in the order Run returns them.
func (s *Session) Outputs() []TensorInfo { return s.outputs }

// Close releases the session and its names. It must not race with Run.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		a := &s.rt.api
		for _, p := range append(s.inNames, s.outNames...) {
			// AllocatorFree reports no error for a pointer it allocated.
			_ = a.check("AllocatorFree", a.allocatorFree(s.rt.alloc, p))
		}
		s.inNames, s.outNames = nil, nil
		if s.s != 0 {
			a.releaseSession(s.s)
			s.s = 0
		}
	})
}

// Tensor is one float32 output of Run. Data views ORT-owned C memory and is
// valid until Release.
type Tensor struct {
	Name  string
	Shape []int64
	Data  []float32

	api *api
	v   uintptr
}

// Release frees the tensor. Extra calls are no-ops.
func (t *Tensor) Release() {
	if t.v != 0 {
		t.api.releaseValue(t.v)
		t.v, t.Data = 0, nil
	}
}

// Run feeds input, a float32 tensor of the given shape to the session's
// single input, and returns every output. input must be C memory (for
// example a vips.Buffer): ORT keeps a pointer to it for the life of the
// input value. When ctx ends during the run, ORT is asked to terminate
// (RunOptionsSetTerminate) and Run returns the context's error.
func (s *Session) Run(ctx context.Context, input []byte, shape []int64) (_ []Tensor, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(s.inNames) != 1 {
		return nil, fmt.Errorf("ort: model has %d inputs, want 1", len(s.inNames))
	}
	count := int64(1)
	for _, d := range shape {
		count *= d
	}
	if len(shape) == 0 || count <= 0 || int64(len(input)) != count*4 {
		return nil, fmt.Errorf("ort: %d input bytes for shape %v", len(input), shape)
	}
	a := &s.rt.api
	var in uintptr
	if err := a.check("CreateTensorWithDataAsOrtValue", a.createTensorWithDataAsOrtValue(s.rt.mem,
		unsafe.Pointer(unsafe.SliceData(input)), uintptr(len(input)), &shape[0], uintptr(len(shape)),
		elementTypeFloat32, &in)); err != nil {
		return nil, err
	}
	defer a.releaseValue(in)
	var ro uintptr
	if err := a.check("CreateRunOptions", a.createRunOptions(&ro)); err != nil {
		return nil, err
	}
	defer a.releaseRunOptions(ro)
	terminated := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(terminated)
		_ = a.check("RunOptionsSetTerminate", a.runOptionsSetTerminate(ro))
	})
	defer func() {
		if !stop() {
			<-terminated // the terminate call holds ro: wait before releasing it
		}
	}()

	outs := make([]uintptr, len(s.outNames))
	status := a.run(s.s, ro, &s.inNames[0], &in, 1, &s.outNames[0], uintptr(len(outs)), &outs[0])
	tensors := make([]Tensor, 0, len(outs))
	defer func() {
		if err != nil {
			for i := range tensors {
				tensors[i].Release()
			}
			for _, v := range outs[len(tensors):] {
				if v != 0 {
					a.releaseValue(v)
				}
			}
		}
	}()
	if err := a.check("Run", status); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, err
	}
	for i, v := range outs {
		t, err := a.tensor(v, s.outputs[i].Name)
		if err != nil {
			return nil, err
		}
		tensors = append(tensors, t)
	}
	return tensors, nil
}

// tensor wraps an output value, reading its shape and data pointer.
func (a *api) tensor(v uintptr, name string) (Tensor, error) {
	if v == 0 {
		return Tensor{}, errors.New("ort: run returned no value for " + strconv.Quote(name))
	}
	var tsi uintptr
	if err := a.check("GetTensorTypeAndShape", a.getTensorTypeAndShape(v, &tsi)); err != nil {
		return Tensor{}, err
	}
	defer a.releaseTensorTypeAndShapeInf(tsi)
	var typ int32
	if err := a.check("GetTensorElementType", a.getTensorElementType(tsi, &typ)); err != nil {
		return Tensor{}, err
	}
	if typ != elementTypeFloat32 {
		return Tensor{}, fmt.Errorf("ort: output %s has element type %d, want float32", name, typ)
	}
	shape, err := a.shape(tsi)
	if err != nil {
		return Tensor{}, err
	}
	var n uintptr
	if err := a.check("GetTensorShapeElementCount", a.getTensorShapeElementCount(tsi, &n)); err != nil {
		return Tensor{}, err
	}
	var p unsafe.Pointer
	if err := a.check("GetTensorMutableData", a.getTensorMutableData(v, &p)); err != nil {
		return Tensor{}, err
	}
	t := Tensor{Name: name, Shape: shape, api: a, v: v}
	if n > 0 && p != nil {
		t.Data = unsafe.Slice((*float32)(p), n)
	}
	return t, nil
}

// goString copies a NUL-terminated C string.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
