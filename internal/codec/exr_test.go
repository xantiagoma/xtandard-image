package codec_test

import (
	"encoding/binary"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func TestParseEXRHeader(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]codec.EXRHeader{
		"exr/rgba-half.exr": {Width: 80, Height: 60, Channels: []string{"A", "B", "G", "R"}},
		"exr/rgb-float.exr": {Width: 48, Height: 32, Channels: []string{"B", "G", "R"}, Float: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := codec.ParseEXRHeader(readFixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("header (-want +got):\n%s", diff)
			}
			if got.HasAlpha() != (len(want.Channels) == 4) {
				t.Errorf("HasAlpha = %v", got.HasAlpha())
			}
		})
	}
	t.Run("tiled luminance", func(t *testing.T) {
		t.Parallel()
		got, err := codec.ParseEXRHeader(synthEXR(7, 5, 0x200, "Y"))
		if err != nil {
			t.Fatal(err)
		}
		if want := (codec.EXRHeader{Width: 7, Height: 5, Tiled: true, Channels: []string{"Y"}}); !cmp.Equal(want, got) {
			t.Errorf("header %+v, want %+v", got, want)
		}
	})
}

func TestParseEXRHeaderErrors(t *testing.T) {
	t.Parallel()
	good := synthEXR(4, 4, 0, "B", "G", "R")
	edit := func(off int, v uint32) []byte {
		b := append([]byte(nil), good...)
		binary.LittleEndian.PutUint32(b[off:], v)
		return b
	}
	// Offsets in synthEXR's layout: the version field at 4, the channels
	// attribute's size at 8+len("channels\x00chlist\x00").
	chSize := 8 + len("channels\x00chlist\x00")
	for name, tc := range map[string]struct {
		data []byte
		want errs.Kind
	}{
		"empty":             {nil, errs.KindCorrupt},
		"magic":             {append([]byte("v/1\x02"), good[4:]...), errs.KindCorrupt},
		"version 1":         {edit(4, 1), errs.KindCorrupt},
		"truncated":         {good[:20], errs.KindCorrupt},
		"no terminator":     {good[:len(good)-1], errs.KindCorrupt},
		"attribute too big": {edit(chSize, 1<<30), errs.KindCorrupt},
		"no data window":    {synthEXRWithout(t, "dataWindow"), errs.KindCorrupt},
		"inverted window":   {synthEXR(0, 4, 0, "R"), errs.KindCorrupt},
		"huge window":       {synthEXR(1<<25, 1, 0, "R"), errs.KindCorrupt},
		"deep":              {synthEXR(4, 4, 0x800, "R"), errs.KindUnsupported},
		"multi-part":        {synthEXR(4, 4, 0x1000, "R"), errs.KindUnsupported},
		"no colour channel": {synthEXR(4, 4, 0, "Z", "depth"), errs.KindUnsupported},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.ParseEXRHeader(tc.data); errs.KindOf(err) != tc.want {
				t.Errorf("error %v (kind %s), want %s", err, errs.KindOf(err), tc.want)
			}
		})
	}
}

// synthEXRWithout is synthEXR(4, 4, 0, "R") with the attribute name renamed
// so the parser does not know it.
func synthEXRWithout(tb testing.TB, name string) []byte {
	tb.Helper()
	b := synthEXR(4, 4, 0, "R")
	for i := 0; i+len(name) <= len(b); i++ {
		if string(b[i:i+len(name)]) == name {
			b[i] = 'x'
			return b
		}
	}
	tb.Fatalf("attribute %q not found", name)
	return nil
}

// FuzzParseEXRHeader checks that the OpenEXR header parser never panics and
// that what it accepts is sane.
func FuzzParseEXRHeader(f *testing.F) {
	f.Add(readFixture(f, "exr/rgba-half.exr"))
	f.Add(readFixture(f, "exr/rgb-float.exr"))
	f.Add(synthEXR(3, 2, 0x200, "Y", "RY", "BY"))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := codec.ParseEXRHeader(data)
		if err != nil {
			if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindUnsupported {
				t.Fatalf("kind %s", k)
			}
			return
		}
		if h.Width < 1 || h.Height < 1 || h.Width > 1<<24 || h.Height > 1<<24 || len(h.Channels) == 0 {
			t.Fatalf("accepted %+v", h)
		}
	})
}
