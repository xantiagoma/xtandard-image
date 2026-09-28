package codec_test

import (
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
)

// withPrecision returns a copy of the baseline JPEG data whose frame header
// (SOF0) is rewritten as marker sof with the given sample precision; the
// entropy-coded data is left as it is.
func withPrecision(tb testing.TB, data []byte, sof, precision byte) []byte {
	tb.Helper()
	out := slices.Clone(data)
	for i := 2; i+4 < len(out); {
		if out[i] != 0xFF {
			tb.Fatalf("jpeg marker expected at %d", i)
		}
		if out[i+1] == 0xC0 {
			out[i+1], out[i+4] = sof, precision
			return out
		}
		i += 2 + (int(out[i+2])<<8 | int(out[i+3]))
	}
	tb.Fatal("no SOF0 marker")
	return nil
}

func TestJPEGPrecision(t *testing.T) {
	t.Parallel()
	base := readFixture(t, "jpeg/baseline.jpg")
	for _, tc := range []struct {
		name string
		data []byte
		bits int
		ok   bool
	}{
		{"baseline", base, 8, true},
		{"progressive", readFixture(t, "jpeg/progressive.jpg"), 8, true},
		{"12-bit extended", withPrecision(t, base, 0xC1, 12), 12, true},
		{"16-bit lossless", withPrecision(t, base, 0xC3, 16), 16, true},
		{"8-bit lossless", withPrecision(t, base, 0xC3, 8), 8, true},
		{"header only", base[:20], 0, false},
		{"not a jpeg", []byte("GIF89a"), 0, false},
		{"scan before any frame", []byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02}, 0, false},
		{"bad segment length", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x01, 0x00}, 0, false},
		{"fill bytes", []byte{0xFF, 0xD8, 0xFF, 0xFF, 0xFF, 0xC1, 0x00, 0x08, 12, 0, 1, 0, 1, 1}, 12, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bits, ok := codec.JPEGPrecision(tc.data)
			if bits != tc.bits || ok != tc.ok {
				t.Errorf("JPEGPrecision = %d, %v; want %d, %v", bits, ok, tc.bits, tc.ok)
			}
		})
	}
}

func FuzzJPEGPrecision(f *testing.F) {
	base := readFixture(f, "jpeg/baseline.jpg")
	f.Add(base[:512])
	f.Add(withPrecision(f, base, 0xC1, 12)[:512])
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xFF, 0xFF, 0xC1, 0x00, 0x08, 12, 0, 1, 0, 1, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		bits, ok := codec.JPEGPrecision(b)
		if ok && (bits < 0 || bits > 255) {
			t.Fatalf("precision %d out of a byte", bits)
		}
		if !ok && bits != 0 {
			t.Fatalf("precision %d without ok", bits)
		}
	})
}
