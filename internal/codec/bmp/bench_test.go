package bmp

import "testing"

func BenchmarkDecode(b *testing.B) {
	rgb, err := Encode(testRaw(1024, 768, 3))
	if err != nil {
		b.Fatal(err)
	}
	rgba, err := Encode(testRaw(1024, 768, 4))
	if err != nil {
		b.Fatal(err)
	}
	for _, bc := range []struct {
		name string
		data []byte
	}{
		{name: "24bit-1024x768", data: rgb},
		{name: "32bit-alpha-1024x768", data: rgba},
		{name: "p8-13x7", data: readFixture(b, "p8.bmp")},
		{name: "rle8-13x7", data: readFixture(b, "rle8.bmp")},
		{name: "rgb565-13x7", data: readFixture(b, "rgb565.bmp")},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(bc.data)))
			for b.Loop() {
				if _, err := Decode(bc.data, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkEncode(b *testing.B) {
	for _, bc := range []struct {
		name  string
		bands int
	}{
		{name: "rgb-1024x768", bands: 3},
		{name: "rgba-1024x768", bands: 4},
	} {
		r := testRaw(1024, 768, bc.bands)
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(r.Data)))
			for b.Loop() {
				if _, err := Encode(r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
