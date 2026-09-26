package detect

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// floats reinterprets fuzz bytes as float32s (NaN, Inf and huge values
// included), the way a hostile or broken model output would look.
func floats(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

// FuzzDecode drives both decoders and NMS with arbitrary sizes and values:
// they must never panic, and every kept box must be finite and non-empty.
func FuzzDecode(f *testing.F) {
	f.Add(uint16(64), uint16(32), []byte{0, 0, 128, 63, 0, 0, 128, 63})
	f.Add(uint16(0), uint16(0), []byte{})
	f.Add(uint16(31), uint16(64), []byte{255, 255, 255, 127})
	f.Fuzz(func(t *testing.T, w, h uint16, data []byte) {
		width, height := int(w%256), int(h%256)
		vals := floats(data)
		fill := func(n int) []float32 {
			out := make([]float32, n)
			for i := range out {
				if len(vals) > 0 {
					out[i] = vals[i%len(vals)]
				}
			}
			return out
		}
		yunet := map[string][]float32{}
		n := 0
		for stride := minStride; stride <= maxStride; stride *= 2 {
			cells := (width / stride) * (height / stride)
			cls, obj, bbox, kps := yunetOutputs(stride)
			yunet[cls], yunet[obj], yunet[bbox], yunet[kps] = fill(cells), fill(cells), fill(4*cells), fill(10*cells)
			n += cells
		}
		if c, err := decodeYuNet(yunet, width, height, 0.1); err == nil {
			check(t, nms(c, 0.3, false, 50))
		}
		if c, err := decodeYOLOX(fill(n*85), width, height, 80, 0.1, nil); err == nil {
			check(t, nms(c, 0.45, true, 50))
		}
		// Mismatched sizes must be rejected, not read out of bounds.
		if _, err := decodeYOLOX(vals, width, height, 80, 0.1, nil); err == nil && len(vals) != n*85 {
			t.Fatalf("decodeYOLOX accepted %d values for %dx%d", len(vals), width, height)
		}
	})
}

func check(t *testing.T, kept []candidate) {
	t.Helper()
	if len(kept) > 50 {
		t.Fatalf("kept %d > limit", len(kept))
	}
	for _, c := range kept {
		if !finite(c) {
			t.Fatalf("kept a non-finite or empty candidate %+v", c)
		}
	}
	in := &input{srcW: 100, srcH: 80, resW: 64, resH: 32}
	for _, d := range in.toSource(kept, true) {
		if d.Box.Empty() || d.Box.Min.X < 0 || d.Box.Max.X > 100 || d.Box.Max.Y > 80 {
			t.Fatalf("box %v outside the image", d.Box)
		}
	}
}

// FuzzParseChecksums: arbitrary SHA256SUMS content never panics and only
// yields 64-hex digests.
func FuzzParseChecksums(f *testing.F) {
	f.Add("8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4  m.onnx\n", "m.onnx")
	f.Add("x *m.onnx", "m.onnx")
	f.Fuzz(func(t *testing.T, listing, name string) {
		got, err := parseChecksums(strings.NewReader(listing), name)
		if err == nil && got != "" && len(got) != 64 {
			t.Fatalf("digest %q", got)
		}
	})
}
