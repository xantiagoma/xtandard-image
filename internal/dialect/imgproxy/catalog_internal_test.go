package imgproxy

import (
	"maps"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestCatalogEnumsMatchParser covers the enumerations the catalogue spells
// out instead of reading from a parser table.
func TestCatalogEnumsMatchParser(t *testing.T) {
	t.Parallel()
	if diff := cmp.Diff(slices.Sorted(maps.Keys(compass)), slices.Sorted(slices.Values(compassNames()))); diff != "" {
		t.Errorf("compass gravity types (-parser +catalogue):\n%s", diff)
	}
	for _, best := range []bool{false, true} {
		for _, f := range formatNames(best) {
			if _, err := parseFormat("format", f, best); err != nil {
				t.Errorf("format %q (best allowed: %v): %v", f, best, err)
			}
		}
	}
	if _, err := parseFormat("format", "best", false); err == nil {
		t.Error("best must be rejected where formatNames(false) is used")
	}
}
