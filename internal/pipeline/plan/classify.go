package plan

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

// Classification is a classifier's output for the source (the /info
// classify option, WP-110): a probability per class in class-id order.
// It is immutable once built (the detection cache shares it).
type Classification struct {
	// Labels are the class names in class-id order; Scores the matching
	// probabilities.
	Labels []string
	Scores []float32
	// MinScore drops classes below it (XI_DETECT_CLASSIFY_THRESHOLD) and
	// MaxTopK caps the number of classes a request gets
	// (XI_DETECT_CLASSIFY_TOP_K_MAX; 0 is no cap).
	MinScore float64
	MaxTopK  int
}

// Class is one class of a classification.
type Class struct {
	ID    int
	Name  string
	Score float64
}

// Top returns the k best classes (capped at MaxTopK), best first, with a
// score of at least MinScore. names restricts them to those classes
// (imgproxy's class_names: "classify only those classes"), matched without
// regard to case and with '_' standing for a space, so that
// "golden_retriever" names "Golden Retriever" in a URL; unknown lists the
// names that match no class (nil when all do). Equal scores keep class-id
// order.
func (c *Classification) Top(k int, names []string) (top []Class, unknown []string) {
	if c == nil || k <= 0 {
		return nil, nil
	}
	if c.MaxTopK > 0 {
		k = min(k, c.MaxTopK)
	}
	n := min(len(c.Labels), len(c.Scores))
	var keep []bool
	if len(names) > 0 {
		index := make(map[string]int, n)
		for i := range n {
			index[classKey(c.Labels[i])] = i
		}
		keep = make([]bool, n)
		for _, name := range names {
			i, ok := index[classKey(name)]
			if !ok {
				unknown = append(unknown, name)
				continue
			}
			keep[i] = true
		}
	}
	for i := range n {
		s := float64(c.Scores[i])
		if (len(keep) == 0 || keep[i]) && s >= c.MinScore && !math.IsNaN(s) {
			top = append(top, Class{ID: i, Name: c.Labels[i], Score: s})
		}
	}
	slices.SortStableFunc(top, func(a, b Class) int { return cmp.Compare(b.Score, a.Score) })
	if len(top) > k {
		top = top[:k]
	}
	return top, unknown
}

// classKey is the matching form of a class name.
func classKey(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, "_", " ")))
}
