package ort_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/detect/ort"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// modelsDir is where the full base image ships the detection models (WP-70).
const modelsDir = "/usr/share/xtandard/models"

func TestMain(m *testing.M) {
	cfg := vips.DefaultConfig()
	if err := vips.Init(&cfg); err != nil && !errors.Is(err, vips.ErrUnavailable) {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

func libraryName() string {
	if runtime.GOOS == "darwin" {
		return "libonnxruntime.dylib"
	}
	return "libonnxruntime.so.1"
}

// load returns the runtime of the full image, or skips (slim image, host).
func load(t *testing.T) *ort.Runtime {
	t.Helper()
	rt, err := ort.Load(libraryName())
	if errors.Is(err, ort.ErrUnavailable) {
		t.Skipf("onnx runtime not installed: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func model(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(modelsDir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("model not installed: %v", err)
	}
	return p
}

func TestLoadMissingLibrary(t *testing.T) {
	t.Parallel()
	_, err := ort.Load("/nonexistent/libonnxruntime.so.1")
	if !errors.Is(err, ort.ErrUnavailable) {
		t.Fatalf("Load(missing) = %v, want ErrUnavailable", err)
	}
}

func TestSessionShapes(t *testing.T) {
	t.Parallel()
	rt := load(t)
	if rt.Version() == "" {
		t.Error("empty version")
	}
	for _, tc := range []struct {
		model   string
		input   []int64
		outputs map[string][]int64
	}{
		{"yolox_s.onnx", []int64{1, 3, 640, 640}, map[string][]int64{"output": {1, 8400, 85}}},
		{"face_detection_yunet_2023mar.onnx", []int64{1, 3, 640, 640}, map[string][]int64{
			"cls_8": {1, 6400, 1}, "cls_16": {1, 1600, 1}, "cls_32": {1, 400, 1},
			"obj_8": {1, 6400, 1}, "obj_16": {1, 1600, 1}, "obj_32": {1, 400, 1},
			"bbox_8": {1, 6400, 4}, "bbox_16": {1, 1600, 4}, "bbox_32": {1, 400, 4},
			"kps_8": {1, 6400, 10}, "kps_16": {1, 1600, 10}, "kps_32": {1, 400, 10},
		}},
	} {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			s, err := rt.NewSession(model(t, tc.model), ort.SessionOptions{IntraOpThreads: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if in := s.Inputs(); len(in) != 1 || !in[0].Float || !slices.Equal(in[0].Shape, tc.input) {
				t.Fatalf("inputs = %+v, want one float32 %v", in, tc.input)
			}
			n := int64(4)
			for _, d := range tc.input {
				n *= d
			}
			buf, err := vips.NewBuffer(int(n))
			if err != nil {
				t.Fatal(err)
			}
			defer buf.Release()
			clear(buf.Bytes())
			out, err := s.Run(t.Context(), buf.Bytes(), tc.input)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]int64{}
			for i := range out {
				got[out[i].Name] = out[i].Shape
				if int64(len(out[i].Data)) != product(out[i].Shape) {
					t.Errorf("%s: %d values for shape %v", out[i].Name, len(out[i].Data), out[i].Shape)
				}
				out[i].Release()
			}
			if diff := cmp.Diff(tc.outputs, got); diff != "" {
				t.Errorf("output shapes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	t.Parallel()
	rt := load(t)
	s, err := rt.NewSession(model(t, "yolox_s.onnx"), ort.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	buf, err := vips.NewBuffer(16)
	if err != nil {
		t.Fatal(err)
	}
	defer buf.Release()
	if _, err := s.Run(t.Context(), buf.Bytes(), []int64{1, 3, 640, 640}); err == nil {
		t.Error("Run accepted a short input")
	}
	if _, err := s.Run(t.Context(), buf.Bytes(), []int64{1, 1, 2, 2}); !isStatus(err) {
		t.Errorf("Run with a wrong shape = %v, want an ORT status", err)
	}
}

func TestNewSessionMissingModel(t *testing.T) {
	t.Parallel()
	rt := load(t)
	_, err := rt.NewSession(filepath.Join(t.TempDir(), "missing.onnx"), ort.SessionOptions{})
	if st, ok := errors.AsType[*ort.StatusError](err); !ok || st.Code == 0 {
		t.Fatalf("NewSession(missing) = %v, want an ORT status", err)
	}
}

func isStatus(err error) bool {
	_, ok := errors.AsType[*ort.StatusError](err)
	return ok
}

func product(shape []int64) int64 {
	n := int64(1)
	for _, d := range shape {
		n *= d
	}
	return n
}
