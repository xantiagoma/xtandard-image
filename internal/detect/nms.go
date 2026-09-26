package detect

import (
	"cmp"
	"math"
	"slices"
)

// box is an axis-aligned box in model-input pixels: (x1, y1) top-left,
// (x2, y2) bottom-right, continuous coordinates.
type box struct{ x1, y1, x2, y2 float32 }

func (b box) area() float32 { return max(b.x2-b.x1, 0) * max(b.y2-b.y1, 0) }

// iou is the intersection over union of two boxes; 0 when either is empty.
func iou(a, b box) float32 {
	w := min(a.x2, b.x2) - max(a.x1, b.x1)
	h := min(a.y2, b.y2) - max(a.y1, b.y1)
	if w <= 0 || h <= 0 {
		return 0
	}
	inter := w * h
	union := a.area() + b.area() - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

// candidate is one decoded prediction before NMS.
type candidate struct {
	box       box
	score     float32
	class     int // index into the model's class names
	landmarks [2 * faceLandmarks]float32
}

// maxCandidates bounds the O(n²) NMS: only the best-scoring candidates
// survive to it. Real images yield a few hundred above the thresholds.
const maxCandidates = 3000

// nms runs greedy non-maximum suppression: candidates are taken in
// descending score order, and one is dropped when it overlaps a kept one of
// the same class (every class when classAware is false) by more than
// threshold. At most limit candidates are kept. The input slice is sorted
// in place.
func nms(cands []candidate, threshold float32, classAware bool, limit int) []candidate {
	slices.SortStableFunc(cands, func(a, b candidate) int { return cmp.Compare(b.score, a.score) })
	if len(cands) > maxCandidates {
		cands = cands[:maxCandidates]
	}
	kept := make([]candidate, 0, min(len(cands), limit))
	for _, c := range cands {
		if len(kept) == limit {
			break
		}
		if !finite(c) {
			continue
		}
		suppressed := false
		for i := range kept {
			if (!classAware || kept[i].class == c.class) && iou(kept[i].box, c.box) > threshold {
				suppressed = true
				break
			}
		}
		if !suppressed {
			kept = append(kept, c)
		}
	}
	return kept
}

// finite reports whether a candidate's score and box are usable numbers.
func finite(c candidate) bool {
	for _, v := range [...]float32{c.score, c.box.x1, c.box.y1, c.box.x2, c.box.y2} {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return false
		}
	}
	return c.box.x2 > c.box.x1 && c.box.y2 > c.box.y1
}

// clamp01 clamps a probability-like model output into [0, 1]; the
// exported models already apply the sigmoid.
func clamp01(v float32) float32 { return min(max(v, 0), 1) }

// expClamped is exp with the argument clamped, so a wild regression output
// yields a huge but finite box instead of +Inf.
func expClamped(v float32) float32 {
	return float32(math.Exp(float64(min(max(v, -expLimit), expLimit))))
}

// expLimit keeps exp(v)·stride far below float32 overflow.
const expLimit = 20
