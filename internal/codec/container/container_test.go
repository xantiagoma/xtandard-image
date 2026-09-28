package container_test

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/container"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// file is one archive entry of a test ZIP.
type file struct {
	name   string
	data   []byte
	stored bool
}

// makeZip writes files with archive/zip (deflate unless stored).
func makeZip(tb testing.TB, files ...file) []byte {
	tb.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, f := range files {
		m := zip.Deflate
		if f.stored {
			m = zip.Store
		}
		fw, err := w.CreateHeader(&zip.FileHeader{Name: f.name, Method: m})
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := fw.Write(f.data); err != nil {
			tb.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

// rawEntry is an entry written with CreateRaw: the sizes and CRC are what
// the test says, whatever the data.
type rawEntry struct {
	name      string
	method    uint16
	flags     uint16
	data      []byte // already compressed
	size, crc uint32
}

func makeRawZip(tb testing.TB, entries ...rawEntry) []byte {
	tb.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		fw, err := w.CreateRaw(&zip.FileHeader{
			Name: e.name, Method: e.method, Flags: e.flags, CRC32: e.crc,
			CompressedSize64: uint64(len(e.data)), UncompressedSize64: uint64(e.size),
		})
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := fw.Write(e.data); err != nil {
			tb.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func deflate(tb testing.TB, p []byte) []byte {
	tb.Helper()
	var b bytes.Buffer
	fw, err := flate.NewWriter(&b, flate.BestCompression)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := fw.Write(p); err != nil {
		tb.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func pngOf(tb testing.TB, w, h int) []byte {
	tb.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{uint8(x * 7), uint8(y * 5), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

const svgDoc = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`

func gz(tb testing.TB, p []byte) []byte {
	tb.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(p); err != nil {
		tb.Fatal(err)
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func pdn(xmlHeader string) []byte {
	n := len(xmlHeader)
	b := append([]byte("PDN3"), byte(n), byte(n>>8), byte(n>>16)) //nolint:gosec // a 24-bit length
	return append(append(b, xmlHeader...), 0, 1, 0xDE, 0xAD)      // a stand-in body
}

func pdnWithThumb(thumb []byte) []byte {
	return pdn(`<pdnImage width="640" height="480" layers="2" savedWithVersion="5.0.13"><custom><thumb png="` +
		base64.StdEncoding.EncodeToString(thumb) + `" /></custom></pdnImage>`)
}

// extract runs Open and ReadInto.
func extract(tb testing.TB, data []byte, lim container.Limits) ([]byte, container.Source, error) {
	tb.Helper()
	emb, err := container.Open(data, lim)
	if err != nil || emb == nil {
		return nil, container.Source{}, err
	}
	dst := make([]byte, emb.Size())
	if err := emb.ReadInto(dst); err != nil {
		return nil, container.Source{}, err
	}
	return dst, emb.Source, nil
}

func TestOpen(t *testing.T) {
	t.Parallel()
	merged, thumb := pngOf(t, 40, 30), pngOf(t, 8, 6)
	tests := []struct {
		name  string
		data  []byte
		want  container.Source
		image []byte
	}{
		{
			"ora merged",
			makeZip(t, file{"mimetype", []byte("image/openraster"), true}, file{"stack.xml", []byte("<image/>"), false},
				file{"data/layer0.png", thumb, false}, file{"Thumbnails/thumbnail.png", thumb, false},
				file{"mergedimage.png", merged, false}),
			container.Source{Kind: container.KindORA, Entry: "mergedimage.png", Content: container.ContentComposite},
			merged,
		},
		{
			"ora without merged image",
			makeZip(t, file{"mimetype", []byte("image/openraster"), true}, file{"Thumbnails/thumbnail.png", thumb, true}),
			container.Source{Kind: container.KindORA, Entry: "Thumbnails/thumbnail.png", Content: container.ContentThumbnail},
			thumb,
		},
		{
			"ora with an empty merged image",
			makeZip(t, file{"mimetype", []byte("image/openraster"), true}, file{"mergedimage.png", nil, false},
				file{"Thumbnails/thumbnail.png", thumb, false}),
			container.Source{Kind: container.KindORA, Entry: "Thumbnails/thumbnail.png", Content: container.ContentThumbnail},
			thumb,
		},
		{
			"kra",
			makeZip(t, file{"mimetype", []byte("application/x-krita"), true}, file{"maindoc.xml", []byte("<DOC/>"), false},
				file{"preview.png", thumb, false}, file{"mergedimage.png", merged, false}),
			container.Source{Kind: container.KindKRA, Entry: "mergedimage.png", Content: container.ContentComposite},
			merged,
		},
		{
			"kra preview only",
			makeZip(t, file{"mimetype", []byte("application/x-krita"), true}, file{"preview.png", thumb, false}),
			container.Source{Kind: container.KindKRA, Entry: "preview.png", Content: container.ContentThumbnail},
			thumb,
		},
		{
			"procreate",
			makeZip(t, file{"Document.archive", []byte("bplist00"), false}, file{"QuickLook/Thumbnail.png", thumb, true},
				file{"8C1E/0~0.chunk", []byte{1, 2, 3}, true}),
			container.Source{Kind: container.KindProcreate, Entry: "QuickLook/Thumbnail.png", Content: container.ContentThumbnail},
			thumb,
		},
		{
			"sketch",
			makeZip(t, file{"document.json", []byte(`{"_class":"document"}`), false}, file{"meta.json", []byte("{}"), false},
				file{"pages/1.json", []byte("{}"), false}, file{"previews/preview.png", merged, false}),
			container.Source{Kind: container.KindSketch, Entry: "previews/preview.png", Content: container.ContentPreview},
			merged,
		},
		{
			"svgz",
			gz(t, []byte(svgDoc)),
			container.Source{Kind: container.KindSVGZ, Entry: "gzip", Content: container.ContentDocument},
			[]byte(svgDoc),
		},
		{
			"paint.net",
			pdnWithThumb(thumb),
			container.Source{Kind: container.KindPDN, Entry: "header thumbnail", Content: container.ContentThumbnail},
			thumb,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, src, err := extract(t, tt.data, container.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, src); diff != "" {
				t.Errorf("source (-want +got):\n%s", diff)
			}
			if !bytes.Equal(got, tt.image) {
				t.Errorf("extracted %d bytes, want the %d-byte embedded image", len(got), len(tt.image))
			}
			if k, ok := container.Detect(tt.data); !ok || k != tt.want.Kind {
				t.Errorf("Detect = %v, %v; want %v", k, ok, tt.want.Kind)
			}
		})
	}
}

// TestNotAContainer: other ZIPs, gzip streams and files are left alone.
func TestNotAContainer(t *testing.T) {
	t.Parallel()
	tests := map[string][]byte{
		"docx":             makeZip(t, file{"[Content_Types].xml", []byte("<Types/>"), false}, file{"word/document.xml", []byte("<w/>"), false}),
		"epub mimetype":    makeZip(t, file{"mimetype", []byte("application/epub+zip"), true}),
		"procreate half":   makeZip(t, file{"Document.archive", []byte("bplist00"), false}),
		"gzip of text":     gz(t, []byte("hello, world")),
		"png":              pngOf(t, 4, 4),
		"empty":            nil,
		"svg uncompressed": []byte(svgDoc),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			emb, err := container.Open(data, container.Limits{})
			if err != nil || emb != nil {
				t.Fatalf("Open = %v, %v; want nil, nil", emb, err)
			}
			if k, ok := container.Detect(data); ok {
				t.Errorf("Detect = %v", k)
			}
		})
	}
}

func TestOpenErrors(t *testing.T) {
	t.Parallel()
	thumb := pngOf(t, 8, 6)
	ora := func(extra ...file) []byte {
		return makeZip(t, append([]file{{"mimetype", []byte("image/openraster"), true}}, extra...)...)
	}
	zeros := make([]byte, 8<<20)
	bomb := deflate(t, zeros)
	lying := deflate(t, append(thumb, thumb...))
	thumbLen := uint32(len(thumb)) //nolint:gosec // a small PNG
	tests := []struct {
		name string
		data []byte
		lim  container.Limits
		kind errs.Kind
	}{
		{"traversal", ora(file{"../../etc/passwd", []byte("x"), false}, file{"mergedimage.png", thumb, false}), container.Limits{}, errs.KindCorrupt},
		{"traversal inside", ora(file{"data/../../x", []byte("x"), false}), container.Limits{}, errs.KindCorrupt},
		{"absolute", ora(file{"/etc/passwd", []byte("x"), false}), container.Limits{}, errs.KindCorrupt},
		{"drive letter", ora(file{"C:/x", []byte("x"), false}), container.Limits{}, errs.KindCorrupt},
		{"backslash", ora(file{`data\..\x`, []byte("x"), false}), container.Limits{}, errs.KindCorrupt},
		{"duplicate target", ora(file{"mergedimage.png", thumb, false}, file{"mergedimage.png", thumb, false}), container.Limits{}, errs.KindCorrupt},
		{"no image", ora(file{"stack.xml", []byte("<image/>"), false}), container.Limits{}, errs.KindUnsupported},
		{"not a png", ora(file{"mergedimage.png", []byte("GIF89a....."), false}), container.Limits{}, errs.KindCorrupt},
		{"entry too large", ora(file{"mergedimage.png", thumb, false}), container.Limits{MaxBytes: 64}, errs.KindLimitExceeded},
		{
			"too many entries",
			ora(file{"a", nil, true}, file{"b", nil, true}, file{"mergedimage.png", thumb, false}),
			container.Limits{Zip: container.ZipLimits{MaxEntries: 3}},
			errs.KindLimitExceeded,
		},
		{
			"declared total too large",
			ora(file{"mergedimage.png", thumb, false}, file{"big", zeros[:4096], false}),
			container.Limits{Zip: container.ZipLimits{MaxTotalSize: 4096}},
			errs.KindLimitExceeded,
		},
		{
			"zip bomb (ratio)",
			makeRawZip(t, rawEntry{name: "mimetype", data: []byte("image/openraster"), size: 16, crc: crc32.ChecksumIEEE([]byte("image/openraster"))},
				rawEntry{name: "mergedimage.png", method: zip.Deflate, data: bomb, size: uint32(len(zeros)), crc: crc32.ChecksumIEEE(zeros)}), //nolint:gosec // 8 MiB
			container.Limits{},
			errs.KindLimitExceeded,
		},
		{
			"declared size lies",
			makeRawZip(t, rawEntry{name: "mimetype", data: []byte("image/openraster"), size: 16, crc: crc32.ChecksumIEEE([]byte("image/openraster"))},
				rawEntry{name: "mergedimage.png", method: zip.Deflate, data: lying, size: thumbLen, crc: crc32.ChecksumIEEE(thumb)}),
			container.Limits{},
			errs.KindCorrupt,
		},
		{
			"bad crc",
			makeRawZip(t, rawEntry{name: "mimetype", data: []byte("image/openraster"), size: 16, crc: crc32.ChecksumIEEE([]byte("image/openraster"))},
				rawEntry{name: "mergedimage.png", data: thumb, size: thumbLen, crc: 1}),
			container.Limits{},
			errs.KindCorrupt,
		},
		{
			"encrypted",
			makeRawZip(t, rawEntry{name: "mimetype", data: []byte("image/openraster"), size: 16, crc: crc32.ChecksumIEEE([]byte("image/openraster"))},
				rawEntry{name: "mergedimage.png", flags: 1, data: thumb, size: thumbLen, crc: crc32.ChecksumIEEE(thumb)}),
			container.Limits{},
			errs.KindUnsupported,
		},
		{
			"bzip2 method",
			makeRawZip(t, rawEntry{name: "mimetype", data: []byte("image/openraster"), size: 16, crc: crc32.ChecksumIEEE([]byte("image/openraster"))},
				rawEntry{name: "mergedimage.png", method: 12, data: thumb, size: thumbLen, crc: crc32.ChecksumIEEE(thumb)}),
			container.Limits{},
			errs.KindUnsupported,
		},
		{"truncated", ora(file{"mergedimage.png", thumb, false})[:200], container.Limits{}, errs.KindCorrupt},
		{"svgz too large", gz(t, []byte(svgDoc+strings.Repeat("<!-- -->", 200))), container.Limits{MaxSVGBytes: 256}, errs.KindLimitExceeded},
		{"svgz bomb", gz(t, append([]byte(svgDoc), make([]byte, 4<<20)...)), container.Limits{}, errs.KindLimitExceeded},
		{"svgz truncated", gz(t, []byte(svgDoc+strings.Repeat(" ", 4096)))[:60], container.Limits{}, errs.KindCorrupt},
		{"pdn without thumbnail", pdn(`<pdnImage width="1" height="1" layers="1"><custom></custom></pdnImage>`), container.Limits{}, errs.KindUnsupported},
		{"pdn bad xml", pdn(`<pdnImage width="1"`), container.Limits{}, errs.KindCorrupt},
		{"pdn bad base64", pdn(`<pdnImage><custom><thumb png="!!!!" /></custom></pdnImage>`), container.Limits{}, errs.KindCorrupt},
		{"pdn thumb not png", pdn(`<pdnImage><custom><thumb png="R0lGODlh" /></custom></pdnImage>`), container.Limits{}, errs.KindCorrupt},
		{"pdn header past the end", []byte("PDN3\xff\xff\x00<pdnImage/>"), container.Limits{}, errs.KindCorrupt},
		{"pdn thumb too large", pdnWithThumb(thumb), container.Limits{MaxBytes: 16}, errs.KindLimitExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := extract(t, tt.data, tt.lim)
			if err == nil {
				t.Fatal("no error")
			}
			if k := errs.KindOf(err); k != tt.kind {
				t.Errorf("kind = %v, want %v (%v)", k, tt.kind, err)
			}
		})
	}
}

// TestZipStructure covers the directory checks archive/zip would not make.
func TestZipStructure(t *testing.T) {
	t.Parallel()
	good := makeZip(t, file{"mimetype", []byte("image/openraster"), true}, file{"mergedimage.png", pngOf(t, 4, 4), false})
	eocd := bytes.LastIndex(good, []byte("PK\x05\x06"))
	mutate := func(f func(b []byte)) []byte {
		b := bytes.Clone(good)
		f(b)
		return b
	}
	tests := []struct {
		name string
		data []byte
		kind errs.Kind
	}{
		{"trailing bytes", append(bytes.Clone(good), "junk"...), errs.KindCorrupt},
		{"zip64 count", mutate(func(b []byte) { binary.LittleEndian.PutUint16(b[eocd+10:], 0xFFFF) }), errs.KindUnsupported},
		{"multi-disk", mutate(func(b []byte) { binary.LittleEndian.PutUint16(b[eocd+4:], 1) }), errs.KindUnsupported},
		{"directory past the end", mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[eocd+16:], 1<<30) }), errs.KindCorrupt},
		{"more entries than the directory holds", mutate(func(b []byte) {
			binary.LittleEndian.PutUint16(b[eocd+8:], 200)
			binary.LittleEndian.PutUint16(b[eocd+10:], 200)
		}), errs.KindCorrupt},
		{"too short", []byte("PK\x03\x04"), errs.KindCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := container.OpenZip(tt.data, container.ZipLimits{})
			if k := errs.KindOf(err); err == nil || k != tt.kind {
				t.Fatalf("OpenZip error %v (kind %v), want kind %v", err, k, tt.kind)
			}
		})
	}
	z, err := container.OpenZip(good, container.ZipLimits{})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(z.Entries()))
	for _, e := range z.Entries() {
		names = append(names, e.Name)
	}
	if diff := cmp.Diff([]string{"mimetype", "mergedimage.png"}, names); diff != "" {
		t.Errorf("entries (-want +got):\n%s", diff)
	}
}

func TestSourceHeader(t *testing.T) {
	t.Parallel()
	s := container.Source{Kind: container.KindKRA, Entry: "mergedimage.png", Content: container.ContentComposite}
	if got, want := s.Header(), "kra; entry=mergedimage.png; content=composite"; got != want {
		t.Errorf("Header() = %q, want %q", got, want)
	}
	for k := container.KindNone; k <= container.KindPDN; k++ {
		if k.String() == "" || k.MIME() == "" {
			t.Errorf("kind %d has no name or type", k)
		}
	}
}

// TestFixtures opens every designer fixture of testdata/fixtures.
func TestFixtures(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures")
	tests := []struct {
		path string
		kind container.Kind
		cont container.Content
	}{
		{"ora/layers.ora", container.KindORA, container.ContentComposite},
		{"kra/layers.kra", container.KindKRA, container.ContentComposite},
		{"procreate/thumbnail.procreate", container.KindProcreate, container.ContentThumbnail},
		{"sketch/preview.sketch", container.KindSketch, container.ContentPreview},
		{"svgz/logo.svgz", container.KindSVGZ, container.ContentDocument},
		{"pdn/thumbnail.pdn", container.KindPDN, container.ContentThumbnail},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join(root, tt.path))
			if err != nil {
				t.Fatal(err)
			}
			img, src, err := extract(t, data, container.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if src.Kind != tt.kind || src.Content != tt.cont {
				t.Errorf("source %+v, want %v %v", src, tt.kind, tt.cont)
			}
			if tt.kind == container.KindSVGZ {
				return
			}
			if _, err := png.DecodeConfig(bytes.NewReader(img)); err != nil {
				t.Errorf("embedded image: %v", err)
			}
		})
	}
}

func FuzzOpen(f *testing.F) {
	thumb := pngOf(f, 4, 4)
	f.Add(makeZip(f, file{"mimetype", []byte("image/openraster"), true}, file{"mergedimage.png", thumb, false}))
	f.Add(makeZip(f, file{"mimetype", []byte("application/x-krita"), true}, file{"preview.png", thumb, true}))
	f.Add(makeZip(f, file{"Document.archive", []byte("bplist00"), false}, file{"QuickLook/Thumbnail.png", thumb, true}))
	f.Add(makeZip(f, file{"document.json", []byte("{}"), false}, file{"meta.json", []byte("{}"), false},
		file{"previews/preview.png", thumb, false}))
	f.Add(gz(f, []byte(svgDoc)))
	f.Add(pdnWithThumb(thumb))
	for _, p := range []string{
		"ora/layers.ora", "kra/layers.kra", "mng/png.mng", "mng/palette.mng",
		"mng/jng.mng", "jng/jng.jng", "jng/jng-idat.jng", "jng/jng-jdaa.jng",
	} {
		if b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", p)); err == nil {
			f.Add(b)
		}
	}
	lim := container.Limits{MaxBytes: 1 << 20, MaxSVGBytes: 1 << 20}
	f.Fuzz(func(t *testing.T, data []byte) {
		container.Detect(data)
		emb, err := container.Open(data, lim)
		if err != nil {
			switch errs.KindOf(err) {
			case errs.KindCorrupt, errs.KindUnsupported, errs.KindLimitExceeded:
				return
			default:
				t.Fatalf("error kind %v: %v", errs.KindOf(err), err)
			}
		}
		if emb == nil {
			return
		}
		if int64(emb.Size()) > lim.MaxBytes {
			t.Fatalf("embedded image of %d bytes over the %d limit", emb.Size(), lim.MaxBytes)
		}
		dst := make([]byte, emb.Size())
		if err := emb.ReadInto(dst); err != nil {
			if k := errs.KindOf(err); k != errs.KindCorrupt && k != errs.KindLimitExceeded && k != errs.KindUnsupported {
				t.Fatalf("read error kind %v: %v", k, err)
			}
		}
	})
}

func FuzzOpenZip(f *testing.F) {
	f.Add(makeZip(f, file{"a", []byte("x"), true}, file{"b/c", bytes.Repeat([]byte("y"), 100), false}))
	f.Add([]byte("PK\x05\x06" + strings.Repeat("\x00", 18)))
	f.Fuzz(func(t *testing.T, data []byte) {
		z, err := container.OpenZip(data, container.ZipLimits{MaxEntries: 64, MaxEntrySize: 1 << 16})
		if err != nil {
			return
		}
		for _, e := range z.Entries() {
			if strings.Contains("/"+e.Name+"/", "/../") || strings.HasPrefix(e.Name, "/") {
				t.Fatalf("name %q passed", e.Name)
			}
			if _, _, err := z.Check(&e); err != nil {
				continue
			}
			dst := make([]byte, e.Size)
			_ = z.ReadInto(&e, dst) // must not panic
		}
	})
}

// BenchmarkOpenORA measures unwrapping a 512×512 composite.
func BenchmarkOpenORA(b *testing.B) {
	data := makeZip(b, file{"mimetype", []byte("image/openraster"), true}, file{"mergedimage.png", pngOf(b, 512, 512), false})
	for b.Loop() {
		if _, _, err := extract(b, data, container.Limits{}); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(len(data)))
}
