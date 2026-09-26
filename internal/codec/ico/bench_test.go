package ico

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
)

func BenchmarkDecode(b *testing.B) {
	multi := readFixture(b, "multi.ico")
	b.Run("multi-best-dib64", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(multi)))
		for b.Loop() {
			if _, err := Decode(multi, Options{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("entries", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := Entries(multi); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkEncode(b *testing.B) {
	// A typical favicon set: three DIB sizes and one PNG.
	frames := []*codec.Raw{testRaw(16, 16, 4), testRaw(32, 32, 4), testRaw(48, 48, 4), testRaw(256, 256, 4)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Encode(frames...); err != nil {
			b.Fatal(err)
		}
	}
}
