//go:build cgo && network

package codec_test

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// TestProbeHighPrecisionJPEGSamples: the gallery's 12-bit JPEG samples
// (WP-100) answer 422 with a clear message instead of failing in the saver
// with a 500 (WP-101; decoding them is WP-106).
func TestProbeHighPrecisionJPEGSamples(t *testing.T) {
	t.Parallel()
	for _, url := range []string{
		"https://raw.githubusercontent.com/cgohlke/imagecodecs-samples/main/images/rgb_u2.jpg",
		"https://raw.githubusercontent.com/libjpeg-turbo/libjpeg-turbo/main/testimages/monkey12.jpg",
	} {
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Skipf("offline: %v", err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Skipf("GET %s: %s, %v", url, resp.Status, err)
			}
			_, err = probe(t.Context(), t, data, codec.Limits{}, "image/jpeg")
			e, ok := errors.AsType[*errs.Error](err)
			if errs.KindOf(err) != errs.KindUnsupported || !ok || e.PublicMessage() != "12-bit JPEG is not supported yet" {
				t.Errorf("probe: %v, want unsupported %q", err, "12-bit JPEG is not supported yet")
			}
		})
	}
}
