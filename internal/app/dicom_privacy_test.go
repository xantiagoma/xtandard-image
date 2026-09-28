//go:build cgo

package app

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// dicomMarker is the value testdata/gen writes into every patient, study
// and free-text element of the DICOM fixtures (testdata/gen/dicom.go).
const dicomMarker = "XIPRIVACYMARKER"

// lockedBuffer is a bytes.Buffer the service's logger and the test share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestDICOMPrivacy requests every DICOM fixture, and damaged copies of
// them, through the service (/info with every field, processing, debug
// headers on, logs at debug level) and checks that no patient value
// reaches a response body, a header or a log line (WP-108, FORMATS §1.8).
func TestDICOMPrivacy(t *testing.T) {
	root := golden.ModuleRoot(t)
	synth := t.TempDir()
	fixtures, err := filepath.Glob(filepath.Join(root, "testdata", "fixtures", "dicom", "*.dcm"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("fixtures: %v (%d)", err, len(fixtures))
	}
	var paths []string
	for _, f := range fixtures {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		paths = append(paths, "dicom/"+name)
		// Truncated inside the dataset and inside the pixel data, and one
		// byte flipped in every 97, to reach the error paths.
		flipped := bytes.Clone(b)
		for i := 132; i < len(flipped); i += 97 {
			flipped[i] ^= 0x5A
		}
		for suffix, data := range map[string][]byte{
			"-cut-header.dcm": b[:min(len(b), 400)],
			"-cut-pixels.dcm": b[:len(b)*3/4],
			"-flipped.dcm":    flipped,
		} {
			n := strings.TrimSuffix(name, ".dcm") + suffix
			if err := os.WriteFile(filepath.Join(synth, n), data, 0o600); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, "synth:"+n)
		}
	}
	var logs lockedBuffer
	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, stop := startLogged(t, designerConfig(t, synth), hooks{}, log)
	base := "http://" + a.public.String()

	check := func(what string, r response) {
		t.Helper()
		if bytes.Contains(r.body, []byte(dicomMarker)) {
			t.Errorf("%s: the response body carries patient data", what)
		}
		if h := fmt.Sprint(r.header); strings.Contains(h, dicomMarker) {
			t.Errorf("%s: a response header carries patient data: %s", what, h)
		}
	}
	for _, p := range paths {
		src := "plain/" + p
		if s, ok := strings.CutPrefix(p, "synth:"); ok {
			src = "plain/xi://synth/" + s
		}
		info := get(t, base+"/info/unsafe/s:1/f:1/d:1/exif:1/iptc:1/xmp:1/vm:1/"+src)
		check(p+" info", info)
		img := get(t, base+"/insecure/rs:fit:32:32/"+src+"@png")
		check(p+" image", img)
		if !strings.HasPrefix(p, "synth:") && (info.status != http.StatusOK || img.status != http.StatusOK) {
			t.Errorf("%s: info %d, image %d, want 200", p, info.status, img.status)
		}
		check(p+" raw", get(t, base+"/insecure/raw:1/"+src))
	}
	stop()
	if strings.Contains(logs.String(), dicomMarker) {
		t.Errorf("the logs carry patient data:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), `"msg"`) {
		t.Error("no log lines were captured")
	}
}
