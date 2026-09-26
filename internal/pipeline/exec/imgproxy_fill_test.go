//go:build cgo && integration

package exec_test

import (
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
)

// TestAgainstImgproxyFill compares the WP-26b cases (trim, extend,
// extend_aspect_ratio, padding, per-frame operations on animations,
// disable_animation) with imgproxy, like TestAgainstImgproxy. imgproxy
// processes animations only with IMGPROXY_MAX_ANIMATION_FRAMES > 1 (its
// default is 1):
//
//	docker run -d -p 18080:8080 -v "$PWD/testdata/fixtures:/fixtures:ro" \
//	  -e IMGPROXY_LOCAL_FILESYSTEM_ROOT=/fixtures -e IMGPROXY_MAX_ANIMATION_FRAMES=256 \
//	  darthsim/imgproxy:latest
//	XI_IMGPROXY_URL=http://host.docker.internal:18080 go test -tags integration \
//	  -run 'TestAgainstImgproxy' -v ./internal/pipeline/exec/
func TestAgainstImgproxyFill(t *testing.T) {
	t.Parallel()
	base := os.Getenv("XI_IMGPROXY_URL")
	if base == "" {
		t.Skip("XI_IMGPROXY_URL is not set")
	}
	for _, tc := range fillCases {
		if tc.noImgproxy {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ours := process(t, tc.fixture, tc.options, tc.out)
			theirs, frames := decode(t, fetchImgproxy(t, base, &tc))
			if frames != ours.frames {
				t.Errorf("frames: ours %d, imgproxy %d", ours.frames, frames)
			}
			if a, b := ours.img.Bounds(), theirs.Bounds(); a != b {
				t.Fatalf("size: ours %v, imgproxy %v", a.Size(), b.Size())
			}
			r, err := ssim.Compare(ours.img, theirs)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("imgproxy %-24s %-40s dssim %.5f", tc.name, tc.options, r.DSSIM)
			if r.DSSIM > imgproxyTolerance {
				t.Errorf("dssim %.5f > %.3f", r.DSSIM, imgproxyTolerance)
			}
		})
	}
}
