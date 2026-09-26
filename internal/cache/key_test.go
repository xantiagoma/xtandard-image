package cache

import (
	"strings"
	"testing"
)

func goldenKey() ResultKey {
	return ResultKey{
		Canonical:    "5f0b6d3f4c0d8a1e3b2a9c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f",
		Source:       "origin\x00\x00https://example.com/a.jpg\x00",
		Format:       "f:webp",
		QualityDelta: -10,
		Hints:        "Sec-CH-DPR, DPR|2",
		Buster:       "b1",
		Build:        "1.2.3",
	}
}

// TestResultKeyGolden pins the string form of ResultKey (ARCHITECTURE
// §9.2). A change here invalidates every persistent cache: bump
// resultKeyVersion instead of editing the expected value.
func TestResultKeyGolden(t *testing.T) {
	t.Parallel()
	k := goldenKey()
	const want = "v1/40e51a65fe4e60e198920fdceb6c56f6f61ed6ffd861a92c712207dbc119f36e"
	got := k.String()
	if !strings.HasPrefix(got, "v1/") || len(got) != len("v1/")+64 {
		t.Fatalf("key %q is not v1/<64 hex>", got)
	}
	if got != want {
		t.Errorf("ResultKey.String() = %q, want %q", got, want)
	}
}

// TestResultKeyFields: every field changes the key, and the length prefix
// keeps adjacent fields from bleeding into each other.
func TestResultKeyFields(t *testing.T) {
	t.Parallel()
	base := goldenKey()
	seen := map[string]string{base.String(): "base"}
	for _, tc := range []struct {
		name string
		mut  func(*ResultKey)
	}{
		{"canonical", func(k *ResultKey) { k.Canonical = "x" }},
		{"source", func(k *ResultKey) { k.Source = "other\x00\x00https://example.com/a.jpg\x00" }},
		{"format", func(k *ResultKey) { k.Format = "f:avif" }},
		{"accept", func(k *ResultKey) { k.Format = "a:jpeg=1000/0" }},
		{"quality delta", func(k *ResultKey) { k.QualityDelta = 0 }},
		{"hints", func(k *ResultKey) { k.Hints = "|0" }},
		{"buster", func(k *ResultKey) { k.Buster = "" }},
		{"build", func(k *ResultKey) { k.Build = "1.2.4" }},
		{"shifted boundary", func(k *ResultKey) { k.Buster, k.Build = "b11.2.3", "" }},
	} {
		k := goldenKey()
		tc.mut(&k)
		s := k.String()
		if prev, dup := seen[s]; dup {
			t.Errorf("%s: key equals %s's", tc.name, prev)
		}
		seen[s] = tc.name
	}
}
