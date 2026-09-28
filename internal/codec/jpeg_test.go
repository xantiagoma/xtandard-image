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

func TestJPEGFrame(t *testing.T) {
	t.Parallel()
	base := readFixture(t, "jpeg/baseline.jpg")
	for _, tc := range []struct {
		name string
		data []byte
		sof  byte
		bits int
		ok   bool
	}{
		{"baseline", base, 0xC0, 8, true},
		{"progressive", readFixture(t, "jpeg/progressive.jpg"), 0xC2, 8, true},
		{"12-bit extended", readFixture(t, "jpeg/rgb12.jpg"), 0xC1, 12, true},
		{"12-bit progressive", readFixture(t, "jpeg/grey12-progressive.jpg"), 0xC2, 12, true},
		{"16-bit lossless", readFixture(t, "jpeg/lossless16.jpg"), 0xC3, 16, true},
		{"6-bit lossless", readFixture(t, "jpeg/lossless6.jpg"), 0xC3, 6, true},
		{"rewritten 12-bit", withPrecision(t, base, 0xC1, 12), 0xC1, 12, true},
		{"JPEG-LS", withPrecision(t, base, 0xF7, 8), 0xF7, 8, true},
		{"header only", base[:20], 0, 0, false},
		{"not a jpeg", []byte("GIF89a"), 0, 0, false},
		{"scan before any frame", []byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02}, 0, 0, false},
		{"bad segment length", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x01, 0x00}, 0, 0, false},
		{"fill bytes", []byte{0xFF, 0xD8, 0xFF, 0xFF, 0xFF, 0xC1, 0x00, 0x08, 12, 0, 1, 0, 1, 1}, 0xC1, 12, true},
		{"DHT is no frame", []byte{0xFF, 0xD8, 0xFF, 0xC4, 0x00, 0x03, 0, 0xFF, 0xC3, 0x00, 0x03, 16}, 0xC3, 16, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sof, bits, ok := codec.JPEGFrame(tc.data)
			if sof != tc.sof || bits != tc.bits || ok != tc.ok {
				t.Errorf("JPEGFrame = %#x, %d, %v; want %#x, %d, %v", sof, bits, ok, tc.sof, tc.bits, tc.ok)
			}
		})
	}
}

func FuzzJPEGFrame(f *testing.F) {
	base := readFixture(f, "jpeg/baseline.jpg")
	f.Add(base[:512])
	f.Add(withPrecision(f, base, 0xC1, 12)[:512])
	f.Add(readFixture(f, "jpeg/lossless16.jpg")[:256])
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xFF, 0xFF, 0xC1, 0x00, 0x08, 12, 0, 1, 0, 1, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		sof, bits, ok := codec.JPEGFrame(b)
		if !ok && (sof != 0 || bits != 0) {
			t.Fatalf("frame %#x, %d bits without ok", sof, bits)
		}
		if ok && sof != 0xF7 && (sof < 0xC0 || sof > 0xCF || sof == 0xC4 || sof == 0xC8 || sof == 0xCC) {
			t.Fatalf("marker %#x is no frame header", sof)
		}
	})
}
