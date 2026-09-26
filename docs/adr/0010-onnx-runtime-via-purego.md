# ADR 0010: ONNX Runtime is loaded with purego, not a second cgo package

- Status: accepted (WP-71)
- Date: 2026-09-26

## Context

Face and object detection (WP-71, ROADMAP open question 7: YuNet, MIT, and YOLOX-s, Apache-2.0)
runs ONNX models on ONNX Runtime (MIT), which the `full` base image ships as the official
prebuilt `libonnxruntime.so.1` (1.30.0, WP-70). The slim image has no ONNX Runtime and must
keep building, starting and passing its tests, and `CGO_ENABLED=0` builds of the module must
keep compiling.

Non-negotiable 3 (CLAUDE.md) says `internal/vips` is the only package that imports `"C"`, and
`scripts/check-cgo.sh` enforces it. Linking ONNX Runtime through cgo would need a second cgo
package behind a build tag and an amendment to that rule, plus a link-time dependency that the
slim image cannot satisfy (a `libonnxruntime` build tag and two binaries).

The ONNX Runtime C API is a single exported symbol, `OrtGetApiBase`, returning a struct whose
`GetApi(version)` hands out a table of function pointers (`OrtApi`). Every call we need takes and
returns pointers, integers and C strings: no struct is passed by value and no callback is
required. `github.com/ebitengine/purego` (Apache-2.0, v0.11.1 released 2026-09-18, no
dependencies) `dlopen`s a library and binds C function pointers to typed Go functions
(`RegisterFunc`) without cgo.

## Spike (2026-09-26)

`internal/detect/ort` binds the 41 `OrtApi` entries it uses with `purego.RegisterFunc`. The
table indices (`offsetof(OrtApi, f) / sizeof(void*)`) were printed by a C program compiled
against `onnxruntime_c_api.h` 1.30 in the full builder; ORT only appends to the table, so they
hold for every version from the one requested (17) up. In the full builder, under
`go test -race`:

- `Load("libonnxruntime.so.1")` reports version 1.30.0; a missing path returns
  `ErrUnavailable` without crashing.
- Sessions for both shipped models report their real shapes: YOLOX-s input `[1,3,640,640]`,
  output `output [1,8400,85]`; YuNet 2023mar input `[1,3,640,640]` (fixed, not dynamic),
  outputs `cls/obj/bbox/kps_{8,16,32}` with 6400/1600/400 anchors.
- A zero tensor created over a `vips.Buffer` (C memory from `vips_tracked_malloc`) runs through
  both models; a wrong shape comes back as an ORT status (`INVALID_ARGUMENT`), not a crash.
- End to end, detections match OpenCV's `FaceDetectorYN` and the official YOLOX demo
  post-processing on the fixtures with IoU 0.98–1.0, and 200 detections leave libvips' tracked
  memory, live objects and the process RSS flat.

No blocker was found, so the cgo fallback (a build-tagged `internal/detect/ort` cgo package with
an amendment to rule 3) is not needed.

## Decision

1. `internal/detect/ort` loads ONNX Runtime with purego: `purego.Dlopen` of
   `XI_DETECT_LIBRARY` (default `libonnxruntime.so.1`) at startup, when `internal/app` calls
   `detect.New` (never at init), then `OrtGetApiBase()->GetApi(17)` and `RegisterFunc` per
   table entry. The library is never `dlclose`d.
2. Non-negotiable 3 stands unchanged: `internal/vips` stays the only package that imports
   `"C"`; `scripts/check-cgo.sh` keeps passing. purego is confined to `internal/detect/ort` by a
   depguard rule (`purego-only-in-ort`).
3. The purego implementation builds only with `cgo && (linux || darwin)`: with cgo, purego uses
   the runtime's own `dlopen` and thread setup instead of its `fakecgo` shim. Other builds
   (including `CGO_ENABLED=0`) compile a stub whose `Load` returns `ErrUnavailable`.
4. Memory rule 6 holds: ORT never keeps a pointer to Go memory. Input tensors are created over
   C memory (`vips.Buffer`); names are ORT-allocated C strings kept per session; shapes, name
   arrays and out-parameters are Go memory ORT reads or writes only during the call (the cgo
   rules for transient pointers). Outputs are ORT-owned and released after decoding.
5. On any image without the library or the models (slim), `detect.New` returns
   `detect.ErrUnavailable` (kind `Unsupported`); with `XI_DETECT_ENABLED=auto` (the default) the
   app logs it and runs without detection, with `true` startup fails.

## Consequences

- One binary for both images; no build tag and no link-time dependency on ONNX Runtime.
- The C API signatures are declared by hand in Go, with no compiler check against the header.
  Mitigations: the indices come from `offsetof` on the real header (the method is recorded in
  `ort_purego.go`); the session tests assert the shipped models' input and output shapes, so a
  wrong index or signature fails CI in the full builder; the table is append-only upstream.
- `RegisterFunc` dispatch goes through reflection (a few microseconds per call); a detection
  makes about ten calls against 20–400 ms of inference.
- New dependency: `github.com/ebitengine/purego` v0.11.1, Apache-2.0, credited in `NOTICE`
  (go-standards §10).
