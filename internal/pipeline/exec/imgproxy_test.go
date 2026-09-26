//go:build cgo && integration

package exec_test

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
)

// imgproxyTolerance is the DSSIM above which an output is reported as
// different from imgproxy's. Both run libvips with the same geometry; the
// remaining differences are encoder noise (lossy outputs), libvips versions
// and resampling rounding.
const imgproxyTolerance = 0.015

// TestAgainstImgproxy renders every golden case with a running imgproxy
// (darthsim/imgproxy with IMGPROXY_LOCAL_FILESYSTEM_ROOT on
// testdata/fixtures) and compares both outputs by size, frame count and
// DSSIM. It is skipped unless XI_IMGPROXY_URL is set:
//
//	docker run -d -p 18080:8080 -v "$PWD/testdata/fixtures:/fixtures:ro" \
//	  -e IMGPROXY_LOCAL_FILESYSTEM_ROOT=/fixtures darthsim/imgproxy:latest
//	XI_IMGPROXY_URL=http://host.docker.internal:18080 go test -tags integration \
//	  -run TestAgainstImgproxy -v ./internal/pipeline/exec/
func TestAgainstImgproxy(t *testing.T) {
	t.Parallel()
	base := os.Getenv("XI_IMGPROXY_URL")
	if base == "" {
		t.Skip("XI_IMGPROXY_URL is not set")
	}
	for _, tc := range goldenCases {
		if tc.noImgproxy {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ours := process(t, tc.fixture, tc.options, tc.out)
			ref := fetchImgproxy(t, base, &tc)
			theirs, frames := decode(t, ref)
			if frames != ours.frames {
				t.Errorf("frames: ours %d, imgproxy %d", ours.frames, frames)
			}
			a, b := ours.img.Bounds(), theirs.Bounds()
			if a != b {
				t.Fatalf("size: ours %v, imgproxy %v", a.Size(), b.Size())
			}
			r, err := ssim.Compare(ours.img, theirs)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("imgproxy %-22s %-44s dssim %.5f", tc.name, tc.options, r.DSSIM)
			if r.DSSIM > imgproxyTolerance {
				t.Errorf("dssim %.5f > %.3f", r.DSSIM, imgproxyTolerance)
			}
		})
	}
}

func fetchImgproxy(t *testing.T, base string, tc *goldenCase) []byte {
	t.Helper()
	u := strings.TrimSuffix(base, "/") + "/insecure/" + tc.options + "/plain/local:///" + tc.fixture + "@" + tc.out.String()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("imgproxy %s: %d %s", u, resp.StatusCode, body)
	}
	return body
}
