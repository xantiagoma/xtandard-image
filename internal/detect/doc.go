// Package detect is the face and object detection runtime (WP-71): ONNX
// Runtime loaded with purego (internal/detect/ort, ADR 0010), a registry of
// the two shipped models, and their pre- and post-processing in Go.
//
//   - Faces: YuNet (face_detection_yunet_2023mar.onnx, MIT, opencv_zoo),
//     decoded like OpenCV's FaceDetectorYN: per-stride cls/obj/bbox/kps
//     outputs, score sqrt(cls·obj), NMS 0.3, five landmarks per face.
//   - Objects: YOLOX-s (yolox_s.onnx, Apache-2.0, COCO 80 classes), decoded
//     like the official demo/ONNXRuntime/onnx_inference.py: grids for
//     strides 8/16/32, xy·stride, exp(wh)·stride, obj·cls, class-aware NMS
//     0.45.
//
// Both models take a 640×640 BGR float32 NCHW tensor with raw 0–255 values.
// The source is resized with libvips (letterbox: scaled to fit, top-left,
// padded with 114 for YOLOX and 0 for YuNet), rendered to 8-bit sRGB in C
// memory (vips.Image.ToRGB8), and packed in Go into a vips.Buffer that ONNX
// Runtime reads in place; C never holds a Go pointer.
//
// Boxes come back in the pixel space of the image the caller passes, which
// in the pipeline is already rotated upright (EXIF orientation applied at
// load), so they line up with the crop geometry WP-72 computes. Multi-page
// images are detected on their first page.
//
// Construction is explicit ([New] from internal/app, behind
// XI_DETECT_ENABLED): nothing is loaded at init. Without the library or the
// model files (the slim image, CGO_ENABLED=0 builds) New fails with
// [ErrUnavailable] (kind Unsupported) and the app runs without detection;
// a nil *Detector is valid and reports the same error. Inferences are
// bounded by a weighted semaphore (XI_DETECT_WORKERS); sessions are created
// once and shared, since ORT sessions are safe for concurrent Run.
package detect
