package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"time"
)

// Designer containers (WP-107) that no permissively licensed sample
// exists for, written to the layout the applications use; only the parts
// the reader looks at carry real data. The OpenRaster, Krita and GIMP
// fixtures come from the applications themselves (testdata/gen/designer.sh).

// zipFile is one archive entry.
type zipFile struct {
	name   string
	data   []byte
	stored bool
}

// fixedTime keeps the archives byte-for-byte reproducible.
var fixedTime = time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)

func writeZip(out string, files []zipFile) error {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, f := range files {
		m := zip.Deflate
		if f.stored {
			m = zip.Store
		}
		fw, err := w.CreateHeader(&zip.FileHeader{Name: f.name, Method: m, Modified: fixedTime})
		if err != nil {
			return err
		}
		if _, err := fw.Write(f.data); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(out, b.Bytes(), 0o644)
}

func pngBytes(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	err := enc.Encode(&b, img)
	return b.Bytes(), err
}

// writeProcreate writes a Procreate document: the NSKeyedArchiver
// Document.archive (a binary plist stub), one layer's tiles (LZO chunks in
// the real app; opaque bytes here) and the QuickLook thumbnail, a
// 160×120 photo.
func writeProcreate(out string) error {
	thumb, err := pngBytes(photo(160, 120))
	if err != nil {
		return err
	}
	return writeZip(out, []zipFile{
		{name: "Document.archive", data: []byte("bplist00\xd4\x01\x02\x03\x04\x05\x06\x07\x08X$versionX$objectsY$archiverT$top")},
		{name: "QuickLook/Thumbnail.png", data: thumb, stored: true},
		{name: "8C1E2B4A-7F3D-4E5A-9B6C-0D1E2F3A4B5C/0~0.chunk", data: bytes.Repeat([]byte{0x11, 0, 0}, 64), stored: true},
	})
}

// writeSketch writes a Sketch document: document.json, meta.json,
// user.json, one page and previews/preview.png, the flat graphic at
// 192×144 (Sketch renders the first page at up to 2048 px).
func writeSketch(out string) error {
	preview, err := pngBytes(graphic(192, 144))
	if err != nil {
		return err
	}
	page := "5E3B7C1A-2D4F-4A6B-8C9D-0E1F2A3B4C5D"
	return writeZip(out, []zipFile{
		{name: "document.json", data: []byte(`{"_class":"document","do_objectID":"1F2E3D4C-5B6A-4978-8695-A4B3C2D1E0F9","pages":[{"_class":"MSJSONFileReference","_ref_class":"MSImmutablePage","_ref":"pages/` + page + `"}]}`)},
		{name: "meta.json", data: []byte(`{"commit":"0000000","pagesAndArtboards":{},"version":146,"compatibilityVersion":99,"app":"com.bohemiancoding.sketch3","appVersion":"99.0"}`)},
		{name: "user.json", data: []byte(`{"document":{"pageListHeight":85}}`)},
		{name: "pages/" + page + ".json", data: []byte(`{"_class":"page","do_objectID":"` + page + `","name":"Page 1","layers":[]}`)},
		{name: "previews/preview.png", data: preview},
	})
}

// writePDN writes a Paint.NET document: "PDN3", the 24-bit little-endian
// length of the XML header, the header with its base64 PNG thumbnail (the
// photo at 128×96, Paint.NET's thumbnails fit 256 px), and a stand-in for
// the .NET-serialised layers.
func writePDN(out string) error {
	thumb, err := pngBytes(photo(128, 96))
	if err != nil {
		return err
	}
	hdr := fmt.Sprintf(`<pdnImage width="640" height="480" layers="2" savedWithVersion="5.0.13.0"><custom><thumb png="%s" /></custom></pdnImage>`,
		base64.StdEncoding.EncodeToString(thumb))
	n := len(hdr)
	var b bytes.Buffer
	b.WriteString("PDN3")
	b.Write([]byte{byte(n), byte(n >> 8), byte(n >> 16)})
	b.WriteString(hdr)
	b.Write([]byte{0x00, 0x01})
	b.Write(bytes.Repeat([]byte{0}, 64))
	return os.WriteFile(out, b.Bytes(), 0o644)
}

// writeSVGZ gzips an SVG document.
func writeSVGZ(in, out string) error {
	doc, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	w, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return err
	}
	w.ModTime = fixedTime
	if _, err := w.Write(doc); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(out, b.Bytes(), 0o644)
}
