package encode

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// fakeCurve is a DSSIM(quality) curve for the search tests.
type fakeCurve struct {
	name string
	d    func(q int) float64
}

// curves spans the shapes measured on the fixture corpus: exponential
// falls of different steepness, a flat graphic that passes everywhere, a
// hard photo that fails everywhere, steps (quantisation tables) and a
// slightly non-monotone curve.
func curves() []fakeCurve {
	exp := func(d40, slope float64) func(int) float64 {
		return func(q int) float64 { return d40 * math.Exp(-slope*float64(q-40)) }
	}
	return []fakeCurve{
		{"steep", exp(0.08, 0.08)},
		{"shallow", exp(0.03, 0.025)},
		{"typical", exp(0.045, 0.045)},
		{"flat graphic", func(q int) float64 { return 0.004 - 0.00001*float64(q) }},
		{"hard photo", exp(0.4, 0.01)},
		{"steps", func(q int) float64 { return 0.06 * math.Exp(-0.05*float64(q/5*5-40)) }},
		{"noisy", func(q int) float64 {
			return 0.05 * math.Exp(-0.04*float64(q-40)) * (1 + 0.04*math.Sin(float64(q)*1.7))
		}},
	}
}

func TestSearchDSSIM(t *testing.T) {
	t.Parallel()
	params := []aqParams{
		{target: 0.02, allowedError: 0.001, lo: 40, hi: 90},
		{target: 0.02, allowedError: 0.001, lo: 30, hi: 80},
		{target: 0.01, allowedError: 0.0005, lo: 40, hi: 90},
		{target: 0.02, allowedError: 0, lo: 1, hi: 100},
	}
	for _, c := range curves() {
		for _, p := range params {
			for _, start := range []int{p.lo, 60, 80, p.hi} {
				name := fmt.Sprintf("%s/t%g-%d..%d/start%d", c.name, p.target, p.lo, p.hi, start)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					checkSearch(t, c, p, start)
				})
			}
		}
	}
}

func checkSearch(t *testing.T, c fakeCurve, p aqParams, start int) {
	t.Helper()
	s := &fakeSaver{size: func(q int) int { return 100 * q }}
	measure := func(_ context.Context, b Body) (float64, error) { return c.d(b.Len() / 100), nil }
	r, err := searchDSSIM(t.Context(), p, start, s.save, measure)
	if err != nil {
		t.Fatal(err)
	}
	if r.attempts != s.saves || s.saves > MaxSaves {
		t.Fatalf("attempts %d, saves %d (qualities %v), want ≤ %d", r.attempts, s.saves, s.qs, MaxSaves)
	}
	if s.released != s.saves-1 {
		t.Errorf("released %d of %d non-returned bodies", s.released, s.saves-1)
	}
	if r.q < p.lo || r.q > p.hi || r.d != c.d(r.q) || r.body.Len() != 100*r.q {
		t.Fatalf("returned q%d d=%g body %d, inconsistent", r.q, r.d, r.body.Len())
	}
	within := math.Abs(r.d-p.target) <= p.allowedError
	passes := func(q int) bool { return c.d(q) <= p.target+p.allowedError }
	if !r.met {
		// Unmet only when even the top quality fails.
		if passes(p.hi) {
			t.Errorf("unmet at q%d (d=%g), but q%d passes (qualities %v)", r.q, r.d, p.hi, s.qs)
		}
		if r.q != p.hi {
			t.Errorf("unmet search returned q%d, want the top q%d", r.q, p.hi)
		}
		return
	}
	if !passes(r.q) {
		t.Fatalf("met at q%d with d=%g > %g", r.q, r.d, p.target+p.allowedError)
	}
	// Tight: within the allowed error, at the bottom of the range, or no
	// more than two steps above the lowest passing quality.
	lowest := r.q
	for q := r.q - 1; q >= p.lo && c.d(q) <= p.target; q-- {
		lowest = q
	}
	if !within && r.q > p.lo && r.q-lowest > 2 {
		t.Errorf("q%d (d=%g) but q%d already passes (d=%g); qualities %v", r.q, r.d, lowest, c.d(lowest), s.qs)
	}
}

func TestSearchDSSIMErrors(t *testing.T) {
	t.Parallel()
	p := aqParams{target: 0.02, allowedError: 0.001, lo: 40, hi: 90}
	boom := errors.New("boom")
	t.Run("measure", func(t *testing.T) {
		t.Parallel()
		s := &fakeSaver{size: func(q int) int { return q }}
		n := 0
		measure := func(context.Context, Body) (float64, error) {
			if n++; n == 3 {
				return 0, boom
			}
			return 0.05, nil
		}
		if _, err := searchDSSIM(t.Context(), p, 60, s.save, measure); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if s.released != s.saves {
			t.Errorf("released %d of %d bodies", s.released, s.saves)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		s := &fakeSaver{size: func(q int) int { return q }}
		measure := func(context.Context, Body) (float64, error) { cancel(); return 0.05, nil }
		if _, err := searchDSSIM(ctx, p, 60, s.save, measure); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if s.released != s.saves {
			t.Errorf("released %d of %d bodies", s.released, s.saves)
		}
	})
}

// TestAutoQualityPrecedence covers request > per-format config > global
// config for each field, the explicit-quality rule and the skip reasons.
func TestAutoQualityPrecedence(t *testing.T) {
	t.Parallel()
	cfg := DefaultAutoQuality()
	cfg.FormatTarget = map[transform.Format]float64{transform.FormatAVIF: 0.015}
	dssimCfg := cfg
	dssimCfg.Method = transform.AutoQualityDSSIM
	jpeg, avif, jxl := transform.FormatJPEG, transform.FormatAVIF, transform.FormatJXL
	for _, tc := range []struct {
		name        string
		cfg         AutoQualityConfig
		t           Target
		quality     int
		want        aqParams
		wantIgnored string
	}{
		{name: "config none", cfg: cfg, t: Target{Format: jpeg}, quality: 80},
		{
			name: "config dssim, per-format bounds", cfg: dssimCfg, t: Target{Format: jpeg}, quality: 80,
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.02, lo: 40, hi: 90, allowedError: 0.001},
		},
		{
			name: "per-format target and bounds", cfg: dssimCfg, t: Target{Format: avif}, quality: 60,
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.015, lo: 30, hi: 80, allowedError: 0.001},
		},
		{
			name: "global bounds without per-format ones", cfg: dssimCfg, t: Target{Format: jxl}, quality: 75,
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.02, lo: 60, hi: 85, allowedError: 0.001},
		},
		{
			name: "request overrides every field", cfg: dssimCfg, quality: 80,
			t: Target{Format: jpeg, AutoQuality: &transform.AutoQuality{
				Method: transform.AutoQualityDSSIM, Target: 0.03, Min: 50, Max: 70, AllowedError: 0.002,
			}},
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.03, lo: 50, hi: 70, allowedError: 0.002},
		},
		{
			name: "request method over config none", cfg: cfg, quality: 80,
			t:    Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM}},
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.02, lo: 40, hi: 90, allowedError: 0.001},
		},
		{
			name: "request fields with the config method", cfg: dssimCfg, quality: 80,
			t:    Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Min: 70}},
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.02, lo: 70, hi: 90, allowedError: 0.001},
		},
		{
			name: "request min above configured max", cfg: dssimCfg, quality: 80,
			t:    Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM, Min: 95}},
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.02, lo: 95, hi: 95, allowedError: 0.001},
		},
		{
			name: "request max below configured min", cfg: dssimCfg, quality: 80,
			t:    Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM, Max: 30}},
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.02, lo: 30, hi: 30, allowedError: 0.001},
		},
		{name: "explicit quality beats config method", cfg: dssimCfg, t: Target{Format: jpeg, Quality: 70}, quality: 70},
		{
			name: "explicit format quality beats config method", cfg: dssimCfg, quality: 70,
			t: Target{Format: jpeg, FormatQuality: map[transform.Format]int{jpeg: 70}},
		},
		{
			name: "request method beats explicit quality", cfg: cfg, quality: 70,
			t:    Target{Format: jpeg, Quality: 70, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM}},
			want: aqParams{method: transform.AutoQualityDSSIM, target: 0.02, lo: 40, hi: 90, allowedError: 0.001},
		},
		{
			name: "request none beats config", cfg: dssimCfg, quality: 80,
			t: Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityNone}},
		},
		{
			name: "no quality knob", cfg: dssimCfg, quality: 0,
			t: Target{Format: transform.FormatPNG, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityDSSIM}},
		},
		{
			name: "ml ignored", cfg: cfg, quality: 80, wantIgnored: IgnoredML,
			t: Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualityML}},
		},
		{
			name: "size with byte target", cfg: cfg, quality: 80,
			t:    Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualitySize, Target: 10240.7}},
			want: aqParams{method: transform.AutoQualitySize, target: 10240, lo: 40, hi: 90, allowedError: 0.001},
		},
		{
			name: "size capped by max_bytes", cfg: cfg, quality: 80,
			t: Target{Format: jpeg, MaxBytes: 5000, AutoQuality: &transform.AutoQuality{
				Method: transform.AutoQualitySize, Target: 10240,
			}},
			want: aqParams{method: transform.AutoQualitySize, target: 5000, lo: 40, hi: 90, allowedError: 0.001},
		},
		{
			name: "size without target uses max_bytes", cfg: cfg, quality: 80,
			t:    Target{Format: jpeg, MaxBytes: 5000, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualitySize}},
			want: aqParams{method: transform.AutoQualitySize, target: 5000, lo: 40, hi: 90, allowedError: 0.001},
		},
		{
			name: "size without any byte target", cfg: cfg, quality: 80, wantIgnored: IgnoredSizeTarget,
			t: Target{Format: jpeg, AutoQuality: &transform.AutoQuality{Method: transform.AutoQualitySize}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ignored := tc.cfg.autoQuality(&tc.t, tc.quality)
			if d := cmp.Diff(tc.want, got, cmp.AllowUnexported(aqParams{})); d != "" {
				t.Errorf("params (-want +got):\n%s", d)
			}
			if ignored != tc.wantIgnored {
				t.Errorf("ignored = %q, want %q", ignored, tc.wantIgnored)
			}
		})
	}
}

func TestAutoQualityConfigValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*AutoQualityConfig)
		ok     bool
	}{
		{"defaults", func(*AutoQualityConfig) {}, true},
		{"size with bytes", func(c *AutoQualityConfig) { c.Method, c.Target = transform.AutoQualitySize, 20000 }, true},
		{"size with dssim target", func(c *AutoQualityConfig) { c.Method = transform.AutoQualitySize }, false},
		{"ml", func(c *AutoQualityConfig) { c.Method = transform.AutoQualityML }, false},
		{"zero target", func(c *AutoQualityConfig) { c.Target = 0 }, false},
		{"allowed error over 1", func(c *AutoQualityConfig) { c.AllowedError = 2 }, false},
		{"global min over max", func(c *AutoQualityConfig) { c.Min, c.Max = 90, 80 }, false},
		{"per-format min over its max", func(c *AutoQualityConfig) { c.FormatMin[transform.FormatAVIF] = 85 }, false},
		{"per-format min over global max", func(c *AutoQualityConfig) {
			c.FormatMin = map[transform.Format]int{transform.FormatJXL: 90}
		}, false},
		{"per-format target zero", func(c *AutoQualityConfig) {
			c.FormatTarget = map[transform.Format]float64{transform.FormatJPEG: 0}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultConfig()
			tc.mutate(&cfg.AutoQuality)
			if err := cfg.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate() = %v, want ok %v", err, tc.ok)
			}
		})
	}
}
