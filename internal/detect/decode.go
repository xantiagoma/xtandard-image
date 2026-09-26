package detect

import (
	"fmt"
	"math"
	"strconv"
)

// Feature-map strides of both model families (YuNet and YOLOX heads at
// P3/P4/P5): 8, 16 and 32, iterated as stride *= 2. Model inputs are
// multiples of maxStride.
const (
	minStride = 8
	maxStride = 32
)

// faceLandmarks is the number of YuNet landmarks: right eye, left eye, nose
// tip, right and left mouth corners (from the subject's point of view, as
// OpenCV's FaceDetectorYN documents them).
const faceLandmarks = 5

// yunetOutputs lists the YuNet output tensor names per stride, in the order
// OpenCV's FaceDetectorYN requests them (modules/objdetect/src/face_detect.cpp).
func yunetOutputs(stride int) (cls, obj, bbox, kps string) {
	s := strconv.Itoa(stride)
	return "cls_" + s, "obj_" + s, "bbox_" + s, "kps_" + s
}

// gridCells returns the number of anchor points of a stride over a w×h
// input, and fails when the input is not a multiple of 32 (a model or
// caller bug: the letterbox pads to it).
func gridCells(w, h, stride int) (cols, rows int, err error) {
	if w <= 0 || h <= 0 || w%maxStride != 0 || h%maxStride != 0 {
		return 0, 0, fmt.Errorf("detect: model input %dx%d is not a positive multiple of 32", w, h)
	}
	return w / stride, h / stride, nil
}

// decodeYuNet turns YuNet's per-stride outputs into face candidates in
// model-input pixels, following OpenCV's FaceDetectorYNImpl::postProcess:
// score = sqrt(cls·obj) (both clamped to [0, 1]); the box centre is
// (col + dx, row + dy)·stride and its size exp(dw, dh)·stride; each
// landmark is (col + kx, row + ky)·stride. outputs maps a tensor name to
// its values; every tensor must have exactly the size the input implies.
func decodeYuNet(outputs map[string][]float32, w, h int, threshold float32) ([]candidate, error) {
	var cands []candidate
	for stride := minStride; stride <= maxStride; stride *= 2 {
		cols, rows, err := gridCells(w, h, stride)
		if err != nil {
			return nil, err
		}
		n := cols * rows
		clsName, objName, bboxName, kpsName := yunetOutputs(stride)
		cls, obj, bbox, kps := outputs[clsName], outputs[objName], outputs[bboxName], outputs[kpsName]
		if len(cls) != n || len(obj) != n || len(bbox) != 4*n || len(kps) != 2*faceLandmarks*n {
			return nil, fmt.Errorf("detect: yunet stride %d: got %d/%d/%d/%d values, want %d/%d/%d/%d",
				stride, len(cls), len(obj), len(bbox), len(kps), n, n, 4*n, 2*faceLandmarks*n)
		}
		s := float32(stride)
		for i := range n {
			score := float32(math.Sqrt(float64(clamp01(cls[i]) * clamp01(obj[i]))))
			if !(score >= threshold) {
				continue
			}
			c, r := float32(i%cols), float32(i/cols)
			b := bbox[4*i : 4*i+4]
			cx, cy := (c+b[0])*s, (r+b[1])*s
			bw, bh := expClamped(b[2])*s, expClamped(b[3])*s
			cand := candidate{
				box:   box{cx - bw/2, cy - bh/2, cx + bw/2, cy + bh/2},
				score: score,
			}
			k := kps[2*faceLandmarks*i : 2*faceLandmarks*(i+1)]
			for j := range faceLandmarks {
				cand.landmarks[2*j] = (k[2*j] + c) * s
				cand.landmarks[2*j+1] = (k[2*j+1] + r) * s
			}
			cands = append(cands, cand)
		}
	}
	return cands, nil
}

// decodeYOLOX turns YOLOX's single output [1, anchors, 5+classes] into
// object candidates in model-input pixels, following the official
// demo/ONNXRuntime/onnx_inference.py (yolox/utils/demo_utils.py
// demo_postprocess): anchors run over strides 8, 16 and 32, row-major
// within each grid; the centre is (col + dx, row + dy)·stride, the size
// exp(dw, dh)·stride, and the score of class c is obj·cls[c] (the exported
// model applies the sigmoids). Every class above threshold becomes its own
// candidate, as in the class-aware multiclass_nms. keep, when not nil,
// restricts the classes considered.
func decodeYOLOX(out []float32, w, h, classes int, threshold float32, keep []bool) ([]candidate, error) {
	anchors := 0
	for stride := minStride; stride <= maxStride; stride *= 2 {
		cols, rows, err := gridCells(w, h, stride)
		if err != nil {
			return nil, err
		}
		anchors += cols * rows
	}
	width := 5 + classes
	if classes <= 0 || len(out) != anchors*width {
		return nil, fmt.Errorf("detect: yolox output has %d values, want %d×%d", len(out), anchors, width)
	}
	if keep != nil && len(keep) != classes {
		return nil, fmt.Errorf("detect: class filter has %d entries for %d classes", len(keep), classes)
	}
	var cands []candidate
	a := 0
	for stride := minStride; stride <= maxStride; stride *= 2 {
		cols, rows, _ := gridCells(w, h, stride)
		s := float32(stride)
		for i := range cols * rows {
			p := out[(a+i)*width : (a+i+1)*width]
			obj := clamp01(p[4])
			if !(obj >= threshold) { // obj·cls ≤ obj
				continue
			}
			c, r := float32(i%cols), float32(i/cols)
			cx, cy := (p[0]+c)*s, (p[1]+r)*s
			bw, bh := expClamped(p[2])*s, expClamped(p[3])*s
			b := box{cx - bw/2, cy - bh/2, cx + bw/2, cy + bh/2}
			for k, cls := range p[5:] {
				if len(keep) > 0 && !keep[k] {
					continue
				}
				if score := obj * clamp01(cls); score > threshold {
					cands = append(cands, candidate{box: b, score: score, class: k})
				}
			}
		}
		a += cols * rows
	}
	return cands, nil
}
