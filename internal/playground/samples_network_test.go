//go:build network

package playground

import (
	"net/http"
	"testing"
	"time"
)

// TestGalleryURLsAlive HEADs every public sample (go test -tags network):
// dead links in samples.json fail here instead of in the gallery. It is
// behind a build tag so the default test run stays offline.
func TestGalleryURLsAlive(t *testing.T) {
	t.Parallel()
	g, err := Gallery(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for _, s := range g {
		t.Run(s.Format+"/"+s.Name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, s.URL, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatalf("HEAD %s: %v", s.URL, err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Errorf("HEAD %s = %d", s.URL, res.StatusCode)
			}
			if res.ContentLength > 0 && res.ContentLength != s.Bytes {
				t.Errorf("%s: %d bytes, samples.json says %d", s.URL, res.ContentLength, s.Bytes)
			}
		})
	}
}
