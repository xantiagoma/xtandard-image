package detect

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Benchmarks of one detection per iteration on a 960×768 fixture at the
// default settings (640×640 model input, one intra-op thread): resize,
// sRGB render and tensor packing through libvips, inference, decoding and
// NMS. Compare runs with benchstat -count=10.

func BenchmarkFaces(b *testing.B) {
	d := newTestDetector(b, testOptions())
	withImage(b, "astronaut-dogs.jpg", func(img *vips.Image) error {
		for b.Loop() {
			if _, err := d.Faces(b.Context(), img); err != nil {
				return err
			}
		}
		return nil
	})
}

func BenchmarkObjects(b *testing.B) {
	d := newTestDetector(b, testOptions())
	withImage(b, "astronaut-dogs.jpg", func(img *vips.Image) error {
		for b.Loop() {
			if _, err := d.Objects(b.Context(), img, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// BenchmarkClassify is one classification per iteration of a 640×512
// fixture with the shipped MobileNetV3-Large (one intra-op thread): cubic
// resize, centre crop, sRGB render and tensor packing through libvips,
// inference and softmax. The session is created before the timer starts.
func BenchmarkClassify(b *testing.B) {
	d := newTestDetector(b, testClassifyOptions())
	withImage(b, "astronaut-dogs.jpg", func(img *vips.Image) error {
		if _, err := d.Classify(b.Context(), img); err != nil {
			return err
		}
		for b.Loop() {
			if _, err := d.Classify(b.Context(), img); err != nil {
				return err
			}
		}
		return nil
	})
}

// BenchmarkDecodeYOLOX isolates the Go post-processing of one YOLOX output.
func BenchmarkDecodeYOLOX(b *testing.B) {
	const w, h = 640, 640
	out := make([]float32, 8400*85)
	for i := range 8400 {
		out[i*85+4] = float32(i%97) / 97 // objectness spread over [0, 1)
		out[i*85+5+i%80] = 0.9
	}
	for b.Loop() {
		c, err := decodeYOLOX(out, w, h, 80, DefaultObjectThreshold, nil)
		if err != nil {
			b.Fatal(err)
		}
		nms(c, DefaultObjectNMS, true, DefaultMaxDetections)
	}
}
