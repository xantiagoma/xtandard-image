package encode

import (
	"context"
	"errors"
	"testing"
)

// fakeBody counts releases of a fake save.
type fakeBody struct {
	n        int
	released *int
}

func (f *fakeBody) Bytes() []byte { return nil }
func (f *fakeBody) Len() int      { return f.n }
func (*fakeBody) Retain()         {}
func (f *fakeBody) Release()      { *f.released++ }

// fakeSaver returns sizes from size(q) and counts saves and releases.
type fakeSaver struct {
	size     func(q int) int
	saves    int
	released int
	qs       []int
}

func (s *fakeSaver) save(_ context.Context, q int) (Body, error) {
	s.saves++
	s.qs = append(s.qs, q)
	return &fakeBody{n: s.size(q), released: &s.released}, nil
}

func TestFitBytes(t *testing.T) {
	t.Parallel()
	linear := func(q int) int { return 1000 + 100*q } // q80 = 9000 bytes
	steps := func(q int) int { return 5000 + 1000*(q/10) }
	for _, tc := range []struct {
		name  string
		size  func(int) int
		start int
		limit int
		// wantQ is the largest quality ≤ start that fits among those tried;
		// wantOver when nothing fits.
		wantQ    int
		wantOver bool
		wantN    int // exact saves, 0 = only check ≤ MaxSaves
	}{
		{name: "fits at start", size: linear, start: 80, limit: 9000, wantQ: 80, wantN: 1},
		{name: "far below", size: linear, start: 80, limit: 2000, wantQ: 10},
		{name: "just below", size: linear, start: 80, limit: 8900, wantQ: 77}, // 79 needs a 7th save
		{name: "middle", size: linear, start: 80, limit: 5000, wantQ: 40},
		{name: "steps", size: steps, start: 90, limit: 8000},
		{name: "nothing fits", size: linear, start: 80, limit: 500, wantOver: true},
		{name: "start 1", size: linear, start: 1, limit: 500, wantOver: true, wantN: 1},
		{name: "start 100 fits", size: linear, start: 100, limit: 1 << 20, wantQ: 100, wantN: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &fakeSaver{size: tc.size}
			r, err := fitBytes(t.Context(), tc.start, tc.limit, s.save)
			if err != nil {
				t.Fatal(err)
			}
			if s.saves > MaxSaves {
				t.Errorf("saves = %d, want ≤ %d (qualities %v)", s.saves, MaxSaves, s.qs)
			}
			if tc.wantN > 0 && s.saves != tc.wantN {
				t.Errorf("saves = %d, want %d (qualities %v)", s.saves, tc.wantN, s.qs)
			}
			if r.attempts != s.saves {
				t.Errorf("attempts = %d, saves = %d", r.attempts, s.saves)
			}
			if s.qs[0] != tc.start {
				t.Errorf("first quality = %d, want the start %d", s.qs[0], tc.start)
			}
			// Every body but the returned one is released exactly once.
			if s.released != s.saves-1 {
				t.Errorf("released %d of %d non-returned bodies", s.released, s.saves-1)
			}
			if r.overBudget != tc.wantOver {
				t.Fatalf("overBudget = %v, want %v (qualities %v)", r.overBudget, tc.wantOver, s.qs)
			}
			if tc.wantOver {
				for _, q := range s.qs {
					if tc.size(q) < r.body.Len() {
						t.Errorf("returned %d bytes, but quality %d gave %d", r.body.Len(), q, tc.size(q))
					}
				}
				return
			}
			if r.body.Len() > tc.limit {
				t.Errorf("size %d > limit %d", r.body.Len(), tc.limit)
			}
			// The returned quality is the largest tried that fits.
			for _, q := range s.qs {
				if tc.size(q) <= tc.limit && q > r.quality {
					t.Errorf("quality %d fits too but %d was returned", q, r.quality)
				}
			}
			if tc.wantQ > 0 && r.quality != tc.wantQ {
				t.Errorf("quality = %d, want %d (qualities %v)", r.quality, tc.wantQ, s.qs)
			}
		})
	}
}

func TestFitBytesErrors(t *testing.T) {
	t.Parallel()
	t.Run("save error releases", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		released := 0
		calls := 0
		save := func(context.Context, int) (Body, error) {
			calls++
			if calls == 3 {
				return nil, boom
			}
			return &fakeBody{n: 1 << 20, released: &released}, nil
		}
		if _, err := fitBytes(t.Context(), 80, 10, save); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		// Two over-budget saves: the larger was released when the smaller
		// arrived (equal sizes keep the first), the kept one on error.
		if released != 2 {
			t.Errorf("released = %d, want 2", released)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s := &fakeSaver{size: func(int) int { return 1 }}
		if _, err := fitBytes(ctx, 80, 10, s.save); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if s.saves != 0 {
			t.Errorf("saves = %d after cancel", s.saves)
		}
	})
}
