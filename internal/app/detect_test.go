package app

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/detect"
	"github.com/xantiagoma/xtandard-image/internal/obs/metrics"
)

// TestBuildDetector: XI_DETECT_ENABLED=auto runs without detection when the
// runtime is missing (the slim image), true fails startup, false never
// loads anything, and a present runtime is logged with its models.
func TestBuildDetector(t *testing.T) {
	t.Parallel()
	met, err := metrics.New(metrics.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		env     []string
		wantErr bool
		wantLog string
	}{
		{"auto without runtime", []string{"XI_DETECT_LIBRARY=/nonexistent/libonnxruntime.so.1"}, false, "detection unavailable"},
		{"true without runtime", []string{"XI_DETECT_ENABLED=true", "XI_DETECT_LIBRARY=/nonexistent/libonnxruntime.so.1"}, true, ""},
		{"false", []string{"XI_DETECT_ENABLED=false", "XI_DETECT_LIBRARY=/nonexistent/libonnxruntime.so.1"}, false, "detection disabled"},
		{"auto without models", []string{"XI_DETECT_MODELS_DIR=/nonexistent"}, false, "detection unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, _, err := config.Load(config.LoadOptions{Environ: append([]string{"XI_DEV_MODE=true"}, tc.env...), ConfigFile: os.DevNull})
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			d, err := buildDetector(t.Context(), &cfg, met, slog.New(slog.NewJSONHandler(&logs, nil)))
			if (err != nil) != tc.wantErr || d != nil {
				t.Fatalf("buildDetector = %v, %v; want nil and error %v", d, err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, detect.ErrUnavailable) {
				t.Errorf("error %v does not wrap detect.ErrUnavailable", err)
			}
			if !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("logs %q do not contain %q", logs.String(), tc.wantLog)
			}
		})
	}

	// With the defaults: enabled on the full image, unavailable elsewhere.
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	d, err := buildDetector(t.Context(), &cfg, met, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if d == nil {
		t.Skipf("detection unavailable here: %s", logs.String())
	}
	if !d.HasFaces() || !d.HasObjects() || !strings.Contains(logs.String(), `"face_model":{"path":"/usr/share/xtandard/models/face_detection_yunet_2023mar.onnx"`) {
		t.Errorf("full image: faces %v, objects %v, logs %s", d.HasFaces(), d.HasObjects(), logs.String())
	}
}
