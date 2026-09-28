package plan_test

import (
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func TestClassificationTop(t *testing.T) {
	t.Parallel()
	c := &plan.Classification{
		Labels: []string{"tench", "Golden Retriever", "tabby cat", "pizza", "banana"},
		Scores: []float32{0.05, 0.4, 0.3, 0.15, 0.1},
	}
	for _, tc := range []struct {
		name        string
		c           *plan.Classification
		k           int
		names       []string
		want        []plan.Class
		wantUnknown []string
	}{
		{name: "top 2", c: c, k: 2, want: []plan.Class{cl(1, "Golden Retriever", 0.4), cl(2, "tabby cat", 0.3)}},
		{name: "k over the classes", c: c, k: 64, want: []plan.Class{
			cl(1, "Golden Retriever", 0.4), cl(2, "tabby cat", 0.3), cl(3, "pizza", 0.15), cl(4, "banana", 0.1), cl(0, "tench", 0.05),
		}},
		{
			name: "filter, case and underscores", c: c, k: 5, names: []string{"PIZZA", "golden_retriever", " banana "},
			want: []plan.Class{cl(1, "Golden Retriever", 0.4), cl(3, "pizza", 0.15), cl(4, "banana", 0.1)},
		},
		{name: "filter then k", c: c, k: 1, names: []string{"pizza", "banana"}, want: []plan.Class{cl(3, "pizza", 0.15)}},
		{
			name: "unknown names", c: c, k: 3, names: []string{"pizza", "unicorn", "cat"},
			want: []plan.Class{cl(3, "pizza", 0.15)}, wantUnknown: []string{"unicorn", "cat"},
		},
		{name: "threshold", c: &plan.Classification{Labels: c.Labels, Scores: c.Scores, MinScore: 0.2}, k: 5, want: []plan.Class{
			cl(1, "Golden Retriever", 0.4), cl(2, "tabby cat", 0.3),
		}},
		{name: "max top k", c: &plan.Classification{Labels: c.Labels, Scores: c.Scores, MaxTopK: 1}, k: 5, want: []plan.Class{
			cl(1, "Golden Retriever", 0.4),
		}},
		{name: "ties keep id order", c: &plan.Classification{Labels: []string{"a", "b", "c"}, Scores: []float32{0.2, 0.4, 0.4}}, k: 3, want: []plan.Class{
			cl(1, "b", 0.4), cl(2, "c", 0.4), cl(0, "a", 0.2),
		}},
		{name: "nan dropped", c: &plan.Classification{Labels: []string{"a", "b"}, Scores: []float32{float32(math.NaN()), 1}}, k: 2, want: []plan.Class{cl(1, "b", 1)}},
		{name: "off", c: c, k: 0},
		{name: "nil", k: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, unknown := tc.c.Top(tc.k, tc.names)
			approx := cmp.Comparer(func(a, b float64) bool { return math.Abs(a-b) < 1e-6 })
			if diff := cmp.Diff(tc.want, got, approx); diff != "" {
				t.Errorf("Top (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantUnknown, unknown); diff != "" {
				t.Errorf("unknown (-want +got):\n%s", diff)
			}
		})
	}
}

func cl(id int, name string, score float64) plan.Class {
	return plan.Class{ID: id, Name: name, Score: score}
}

func TestNeedsClassify(t *testing.T) {
	t.Parallel()
	info := func(k int) *transform.Request {
		return &transform.Request{Kind: transform.KindInfo, Info: &transform.InfoOptions{ClassifyTopK: k}}
	}
	if n := plan.NeedsDetection(info(3)); !n.Classify || n.Faces || n.Objects || !n.Any() {
		t.Errorf("cl:3 = %+v", n)
	}
	if n := plan.NeedsDetection(info(0)); n.Any() {
		t.Errorf("cl:0 = %+v", n)
	}
	// A processing request never classifies.
	proc := info(3)
	proc.Kind = transform.KindProcess
	if n := plan.NeedsDetection(proc); n.Classify {
		t.Errorf("processing = %+v", n)
	}
}
