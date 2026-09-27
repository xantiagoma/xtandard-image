//go:build cgo

package exec_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/detectfix"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
)

// TestChainedDetectionsFollowGeometry is the oracle of the chained
// detection goldens (WP-92): pipelines that give the source back (a
// rotation undone, a flip undone, padding trimmed away, an extend cropped
// off) followed by a face gravity crop like the face gravity on the
// source, and not like the smart crop the chained pipeline fell back to
// before. The trim and the smart crop rectangles come from the executor.
func TestChainedDetectionsFollowGeometry(t *testing.T) {
	t.Parallel()
	dets := detectfix.Load(t, detectfix.Crew)
	const face = "rs:fill:240:120/g:obj:face"
	direct := processDetected(t, detectfix.Crew, parse(t, face), dets)
	fallback := processDetected(t, detectfix.Crew, parse(t, "rs:fill:240:120/g:sm"), nil)
	if r, err := ssim.Compare(direct.img, fallback.img); err != nil || r.DSSIM < 0.01 {
		t.Fatalf("face and smart crops are alike (%+v, %v): the test cannot tell them apart", r.DSSIM, err)
	}
	for _, chain := range []string{
		"rot:90/-/rot:270",
		"fl:1:1/-/fl:1:1",
		"rot:180/fl:1:0/-/fl:0:1",
		// Magenta padding trimmed off again (the trim's rectangle).
		"pd:40:20:10:30/bg:255:0:255/-/t:10:FF00FF",
		// The source extended onto a larger canvas, cropped back.
		"rs:fit:800:800/ex:1:we/-/c:640:503:we",
	} {
		t.Run(chain, func(t *testing.T) {
			t.Parallel()
			got := processDetected(t, detectfix.Crew, parse(t, chain+"/-/"+face), dets)
			if len(got.res.Skipped) != 0 {
				t.Errorf("Skipped = %q", got.res.Skipped)
			}
			r, err := ssim.Compare(direct.img, got.img)
			if err != nil {
				t.Fatal(err)
			}
			if r.DSSIM > 0.01 {
				t.Errorf("DSSIM(face gravity on the source, after %s) = %.4f", chain, r.DSSIM)
			}
		})
	}
}
