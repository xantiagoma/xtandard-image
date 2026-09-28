//go:build cgo && network

package codec_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
)

// TestProbeHighPrecisionJPEGSamples: the gallery's 12-bit and lossless JPEG
// samples (WP-100) probe as JPEG at their precision, through the shim's
// jpeghpload (WP-106; WP-101 refused the 12-bit ones with a 422).
func TestProbeHighPrecisionJPEGSamples(t *testing.T) {
	t.Parallel()
	for url, bits := range map[string]int{
		"https://raw.githubusercontent.com/cgohlke/imagecodecs-samples/main/images/rgb_u2.jpg":       12,
		"https://raw.githubusercontent.com/libjpeg-turbo/libjpeg-turbo/main/testimages/monkey12.jpg": 12,
		"https://raw.githubusercontent.com/cgohlke/imagecodecs-samples/main/images/rgb_u1.ljpg":      8,
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
			m, err := probe(t.Context(), t, data, codec.Limits{}, "image/jpeg")
			if err != nil || m.Format != codec.FormatJPEG || m.BitDepth != bits {
				t.Errorf("probe: %+v, %v; want JPEG at %d bits", m, err, bits)
			}
		})
	}
}
