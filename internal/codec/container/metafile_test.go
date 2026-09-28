package container_test

import (
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/container"
)

// TestDetectMetafileFixtures: the two metafile fixtures are recognised and
// no other fixture is taken for a metafile.
func TestDetectMetafileFixtures(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures")
	want := map[string]container.Kind{"wmf/shapes.wmf": container.KindWMF, "emf/shapes.emf": container.KindEMF}
	seen := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".md") {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // test fixtures
		if err != nil {
			return err
		}
		got, ok := container.DetectMetafile(data)
		rel = filepath.ToSlash(rel)
		if k, isMetafile := want[rel]; isMetafile {
			seen++
			if !ok || got != k {
				t.Errorf("%s: DetectMetafile = %v, %v; want %v", rel, got, ok, k)
			}
		} else if ok {
			t.Errorf("%s taken for a %v metafile", rel, got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != len(want) {
		t.Errorf("found %d of the %d metafile fixtures", seen, len(want))
	}
}

func TestDetectMetafile(t *testing.T) {
	t.Parallel()
	le := binary.LittleEndian
	wmfHeader := func(typ, size, version uint16) []byte {
		b := le.AppendUint16(nil, typ)
		b = le.AppendUint16(b, size)
		b = le.AppendUint16(b, version)
		return append(b, make([]byte, 12)...)
	}
	placeable := func(right, bottom, inch uint16) []byte {
		b := le.AppendUint32(nil, 0x9AC6CDD7)
		b = le.AppendUint16(b, 0)
		for _, v := range []uint16{0, 0, right, bottom, inch} {
			b = le.AppendUint16(b, v)
		}
		b = append(b, make([]byte, 6)...)
		return append(b, wmfHeader(1, 9, 0x0300)...)
	}
	emf := func(sig string, size uint32) []byte {
		b := le.AppendUint32(nil, 1)
		b = le.AppendUint32(b, size)
		b = append(b, make([]byte, 32)...)
		b = append(b, sig...)
		return append(b, make([]byte, 44)...)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want container.Kind
	}{
		{"bare wmf, memory", wmfHeader(1, 9, 0x0300), container.KindWMF},
		{"bare wmf, disk, version 1", wmfHeader(2, 9, 0x0100), container.KindWMF},
		{"placeable wmf", placeable(200, 150, 96), container.KindWMF},
		{"emf", emf(" EMF", 88), container.KindEMF},
		{"wmf, wrong header size", wmfHeader(1, 8, 0x0300), container.KindNone},
		{"wmf, unknown version", wmfHeader(1, 9, 0x0200), container.KindNone},
		{"placeable, empty box", placeable(0, 150, 96), container.KindNone},
		{"placeable, zero units per inch", placeable(200, 150, 0), container.KindNone},
		{"emf, no signature", emf("EMF ", 88), container.KindNone},
		{"emf, short header", emf(" EMF", 40), container.KindNone},
		{"short", []byte{1, 0, 9, 0}, container.KindNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := container.DetectMetafile(tc.data)
			if got != tc.want || ok != (tc.want != container.KindNone) {
				t.Errorf("DetectMetafile = %v, %v; want %v", got, ok, tc.want)
			}
		})
	}
}

func FuzzDetectMetafile(f *testing.F) {
	f.Add([]byte{1, 0, 9, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if k, ok := container.DetectMetafile(data); ok != (k != container.KindNone) {
			t.Fatalf("DetectMetafile = %v, %v", k, ok)
		}
	})
}
