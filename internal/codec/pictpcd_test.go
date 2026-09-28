package codec_test

import (
	"encoding/binary"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/synth"
)

// pict builds a PICT file: the 512-byte header, the picture size, the
// frame (top, left, bottom, right) and the opcode stream ops (after the
// version opcode, which version 1 or 2 selects).
func pict(version int, frame [4]int16, ops ...byte) []byte {
	b := make([]byte, 512, 1024)
	b = binary.BigEndian.AppendUint16(b, 0)
	for _, v := range frame {
		b = binary.BigEndian.AppendUint16(b, uint16(v)) //nolint:gosec // PICT stores int16
	}
	if version == 1 {
		b = append(b, 0x11, 0x01)
	} else {
		b = append(b, 0x00, 0x11, 0x02, 0xFF)
	}
	return append(b, ops...)
}

// clip is the Clip opcode (version 2: 0x0001) with a rectangular region
// whose left edge is 0.
func clip(v2 bool, top, bottom, right int16) []byte {
	var b []byte
	if v2 {
		b = append(b, 0x00)
	}
	b = append(b, 0x01, 0x00, 0x0A)
	for _, v := range []int16{top, 0, bottom, right} {
		b = binary.BigEndian.AppendUint16(b, uint16(v)) //nolint:gosec // PICT stores int16
	}
	return b
}

func TestParsePICTHeader(t *testing.T) {
	t.Parallel()
	header := append([]byte{0x0C, 0x00}, make([]byte, 24)...) // HeaderOp, version 2
	cat := func(parts ...[]byte) []byte {
		var b []byte
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}
	for _, tc := range []struct {
		name string
		data []byte
		want codec.PICTHeader
	}{
		{"fixture (ImageMagick's writer)", readFixture(t, "pict/photo.pct"), codec.PICTHeader{Width: 64, Height: 48, Version: 2}},
		{"version 1, frame only", pict(1, [4]int16{0, 0, 30, 40}, 0x00, 0x00, 0x00, 0xFF), codec.PICTHeader{Width: 40, Height: 30, Version: 1}},
		{
			"version 1, clip sets the canvas", pict(1, [4]int16{0, 0, 30, 40}, cat([]byte{0xA0, 0, 0}, clip(false, 0, 60, 80), []byte{0x90})...),
			codec.PICTHeader{Width: 80, Height: 60, Version: 1},
		},
		{
			"version 2, 144 DPI: the clip is the size", pict(2, [4]int16{0, 0, 24, 32}, cat(header, []byte{0x00, 0x1E}, clip(true, 0, 48, 64), []byte{0x00, 0x9A})...),
			codec.PICTHeader{Width: 64, Height: 48, Version: 2},
		},
		{
			"a clip with a negative origin is ignored", pict(2, [4]int16{0, 0, 24, 32}, cat(header, clip(true, -1, 48, 64))...),
			codec.PICTHeader{Width: 32, Height: 24, Version: 2},
		},
		{
			"a clip after drawing starts is not read", pict(2, [4]int16{0, 0, 24, 32}, cat(header, []byte{0x00, 0x9A}, clip(true, 0, 48, 64))...),
			codec.PICTHeader{Width: 32, Height: 24, Version: 2},
		},
		{
			"long comment skipped", pict(2, [4]int16{0, 0, 24, 32}, cat(header, []byte{0x00, 0xA1, 0x00, 0x64, 0x00, 0x02, 0xAB, 0xCD}, clip(true, 0, 10, 10))...),
			codec.PICTHeader{Width: 10, Height: 10, Version: 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := codec.ParsePICTHeader(tc.data)
			if err != nil {
				t.Fatalf("ParsePICTHeader: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParsePICTHeader = %+v, want %+v", got, tc.want)
			}
			if f, ok := codec.Sniff(tc.data); !ok || f != codec.FormatPICT {
				t.Errorf("Sniff = %v, %v", f, ok)
			}
		})
	}
}

func TestParsePICTHeaderErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"short", make([]byte, 520)},
		{"empty frame", pict(1, [4]int16{0, 0, 0, 40})},
		{"negative frame", pict(1, [4]int16{-5, 0, 10, 40})},
		{"no version opcode", append(make([]byte, 522), 0x12, 0x34, 0x56, 0x78, 0, 0)},
		{"empty clip", pict(1, [4]int16{0, 0, 30, 40}, clip(false, 0, 0, 10)...)},
		{"truncated clip region", pict(1, [4]int16{0, 0, 30, 40}, 0x01, 0x00, 0x40, 0x00)},
		{"truncated long comment", pict(2, [4]int16{0, 0, 30, 40}, 0x00, 0xA1, 0x00, 0x01, 0x10, 0x00)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.ParsePICTHeader(tc.data); errs.KindOf(err) != errs.KindCorrupt {
				t.Errorf("ParsePICTHeader: %v, want corrupt", err)
			}
		})
	}
	// A PICT is not taken for a TGA or anything else without a signature.
	if f, _ := codec.Sniff(pict(1, [4]int16{0, 0, 0, 40})); f == codec.FormatPICT {
		t.Error("an empty frame sniffed as PICT")
	}
}

func TestParsePCDHeader(t *testing.T) {
	t.Parallel()
	for rot, want := range []codec.PCDHeader{{768, 512}, {512, 768}, {768, 512}, {512, 768}} {
		data := synth.PCD(byte(rot))
		got, err := codec.ParsePCDHeader(data)
		if err != nil || got != want {
			t.Errorf("rotation %d: ParsePCDHeader = %+v, %v; want %+v", rot, got, err, want)
		}
		if f, ok := codec.Sniff(data); !ok || f != codec.FormatPCD {
			t.Errorf("rotation %d: Sniff = %v, %v", rot, f, ok)
		}
	}
	short := synth.PCD(0)[:synth.PCDSize-1]
	if _, err := codec.ParsePCDHeader(short); errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("truncated Base image: %v, want corrupt", err)
	}
	if f, _ := codec.Sniff(short); f == codec.FormatPCD {
		t.Error("a truncated pack sniffed as Photo CD")
	}
}

func FuzzParsePICTHeader(f *testing.F) {
	f.Add(readFixture(f, "pict/photo.pct")[512:])
	f.Add(pict(1, [4]int16{0, 0, 30, 40}, clip(false, 0, 60, 80)...)[512:])
	f.Fuzz(func(t *testing.T, body []byte) {
		data := append(make([]byte, 512), body...)
		h, err := codec.ParsePICTHeader(data)
		if err != nil {
			return
		}
		if h.Width < 1 || h.Height < 1 || h.Width > 32767 || h.Height > 32767 || (h.Version != 1 && h.Version != 2) {
			t.Fatalf("ParsePICTHeader = %+v", h)
		}
	})
}

// FuzzParsePCDHeader fuzzes the image pack header (the first 4 KiB of a
// synthetic pack; the rest is the Base image, which only has to be there).
func FuzzParsePCDHeader(f *testing.F) {
	f.Add(synth.PCD(0)[:4096])
	f.Add(synth.PCD(1)[:4096])
	f.Fuzz(func(t *testing.T, head []byte) {
		data := synth.PCD(0)
		copy(data, head)
		h, err := codec.ParsePCDHeader(data)
		if err != nil {
			return
		}
		if (h.Width != 768 || h.Height != 512) && (h.Width != 512 || h.Height != 768) {
			t.Fatalf("ParsePCDHeader = %+v", h)
		}
		if f, ok := codec.Sniff(data); !ok || f != codec.FormatPCD {
			t.Fatalf("a parsed pack sniffs as %v", f)
		}
	})
}
