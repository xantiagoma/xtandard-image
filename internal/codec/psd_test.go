package codec_test

import (
	"encoding/binary"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func TestParsePSDHeader(t *testing.T) {
	t.Parallel()
	layered := codec.PSDHeader{Width: 96, Height: 72, Channels: 3, Depth: 8, Mode: 3, Layers: 2, Composite: true}
	psb := layered
	psb.PSB = true
	flat := layered
	flat.Composite = false
	for name, want := range map[string]codec.PSDHeader{
		"psd/layered.psd": layered, "psd/psb-layered.psb": psb, "psd/no-composite.psd": flat,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := codec.ParsePSDHeader(readFixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("header (-want +got):\n%s", diff)
			}
			if got.HasAlpha() || got.ColorSpace() != codec.ColorSpaceSRGB {
				t.Errorf("alpha %v, colour space %v", got.HasAlpha(), got.ColorSpace())
			}
		})
	}
}

func TestParsePSDHeaderErrors(t *testing.T) {
	t.Parallel()
	good := readFixture(t, "psd/layered.psd")
	edit := func(off int, v uint32, size int) []byte {
		b := append([]byte(nil), good...)
		if size == 2 {
			binary.BigEndian.PutUint16(b[off:], uint16(v)) //nolint:gosec // small test values
		} else {
			binary.BigEndian.PutUint32(b[off:], v)
		}
		return b
	}
	for name, data := range map[string][]byte{
		"empty":              nil,
		"short":              good[:20],
		"signature":          append([]byte("8BPX"), good[4:]...),
		"version 3":          edit(4, 3, 2),
		"no channels":        edit(12, 0, 2),
		"zero height":        edit(14, 0, 4),
		"huge width":         edit(18, 1<<31, 4),
		"depth 7":            edit(22, 7, 2),
		"truncated section":  good[:40],
		"resources past end": edit(30, 1<<30, 4),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.ParsePSDHeader(data); errs.KindOf(err) != errs.KindCorrupt {
				t.Errorf("error %v (kind %s), want corrupt", err, errs.KindOf(err))
			}
		})
	}
}

// FuzzParsePSDHeader checks that the Photoshop header parser never panics
// and that what it accepts is sane.
func FuzzParsePSDHeader(f *testing.F) {
	for _, name := range []string{"psd/layered.psd", "psd/psb-layered.psb", "psd/no-composite.psd"} {
		f.Add(readFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := codec.ParsePSDHeader(data)
		if err != nil {
			if errs.KindOf(err) != errs.KindCorrupt {
				t.Fatalf("kind %s", errs.KindOf(err))
			}
			return
		}
		if h.Width < 1 || h.Height < 1 || h.Channels < 1 || h.Layers < 0 {
			t.Fatalf("accepted %+v", h)
		}
	})
}
