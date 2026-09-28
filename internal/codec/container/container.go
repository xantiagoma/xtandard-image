// Package container recognises designer documents that carry a ready-made
// raster (or a compressed SVG) inside another file format, and extracts it
// (FORMATS.md §1.8, WP-107):
//
//   - OpenRaster (.ora; MyPaint, Krita, GIMP) and Krita (.kra): a ZIP whose
//     first entry, "mimetype", names the format; mergedimage.png is the
//     full-size composite, the thumbnail (Thumbnails/thumbnail.png,
//     preview.png) the fallback;
//   - Procreate (.procreate): a ZIP with Document.archive and
//     QuickLook/Thumbnail.png, a screen-sized thumbnail (the full composite
//     would need the LZO tiles and the NSKeyedArchiver document);
//   - Sketch (.sketch): a ZIP with document.json, meta.json and
//     previews/preview.png, a preview of the first page (up to 2048 px);
//   - SVGZ: a gzip stream holding an SVG document, returned whole for the
//     SVG sanitiser;
//   - Paint.NET (.pdn): the PNG thumbnail (up to 256 px) that the XML
//     header carries; the layers are a .NET serialisation that is not read;
//   - MNG and JNG (mng.go): the first image of an MNG as a standalone PNG
//     (or JNG), and a JNG's JPEG stream, or a PNG when it has alpha.
//
// Detection is by magic bytes and container contents, never the file name.
// Everything is bounded: the ZIP reader (zip.go) caps entries, sizes and
// compression ratios and refuses names that traverse, gzip is read up to
// the SVG byte limit, the Paint.NET header up to maxPDNHeader. Nothing
// here uses cgo, so the server can unwrap a source before it is probed.
package container

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
)

// Kind is a container format.
type Kind uint8

// Container formats.
const (
	KindNone Kind = iota
	// KindORA is OpenRaster.
	KindORA
	// KindKRA is a Krita document.
	KindKRA
	// KindProcreate is a Procreate document.
	KindProcreate
	// KindSketch is a Sketch document.
	KindSketch
	// KindSVGZ is a gzip-compressed SVG document.
	KindSVGZ
	// KindPDN is a Paint.NET document.
	KindPDN
	// KindMNG is a Multiple-image Network Graphics animation (mng.go).
	KindMNG
	// KindJNG is a JPEG Network Graphics image (mng.go).
	KindJNG
	// KindWMF and KindEMF are Windows metafiles (metafile.go): the server
	// converts them to SVG with the gpl image's converter, not this
	// package.
	KindWMF
	KindEMF
)

// String is the format's short name ("ora", "kra", ...).
func (k Kind) String() string {
	switch k {
	case KindORA:
		return "ora"
	case KindKRA:
		return "kra"
	case KindProcreate:
		return "procreate"
	case KindSketch:
		return "sketch"
	case KindSVGZ:
		return "svgz"
	case KindPDN:
		return "pdn"
	case KindMNG:
		return "mng"
	case KindJNG:
		return "jng"
	case KindWMF:
		return "wmf"
	case KindEMF:
		return "emf"
	default:
		return "none"
	}
}

// MIME is the format's media type.
func (k Kind) MIME() string {
	switch k {
	case KindORA:
		return "image/openraster"
	case KindKRA:
		return "application/x-krita"
	case KindProcreate:
		return "application/x-procreate"
	case KindSketch:
		return "application/x-sketch"
	case KindSVGZ:
		return "image/svg+xml"
	case KindPDN:
		return "image/x-paintnet"
	case KindMNG:
		return "video/x-mng"
	case KindJNG:
		return "image/x-jng"
	case KindWMF:
		return "image/wmf"
	case KindEMF:
		return "image/emf"
	default:
		return "application/octet-stream"
	}
}

// Content is what the extracted image is to the document.
type Content uint8

// Contents.
const (
	// ContentComposite is the full-size flattened image (ORA, KRA).
	ContentComposite Content = iota + 1
	// ContentThumbnail is a reduced copy (Procreate, Paint.NET, and the
	// ORA/KRA fallback).
	ContentThumbnail
	// ContentPreview is a preview of the document's first page (Sketch).
	ContentPreview
	// ContentDocument is the document itself, only compressed (SVGZ).
	ContentDocument
	// ContentFrame is the first frame of an animation (MNG).
	ContentFrame
	// ContentConverted is the document converted to SVG (WMF, EMF).
	ContentConverted
)

// String is the content's name ("composite", "thumbnail", ...).
func (c Content) String() string {
	switch c {
	case ContentComposite:
		return "composite"
	case ContentThumbnail:
		return "thumbnail"
	case ContentPreview:
		return "preview"
	case ContentDocument:
		return "document"
	case ContentFrame:
		return "frame"
	case ContentConverted:
		return "converted"
	default:
		return "unknown"
	}
}

// Source describes where the extracted image came from.
type Source struct {
	Kind Kind
	// Entry is the path inside the container ("mergedimage.png"), or a
	// description for containers without paths ("gzip", "header thumbnail").
	Entry   string
	Content Content
}

// Header is the X-Xi-Container-Source value: "ora; entry=mergedimage.png;
// content=composite".
func (s *Source) Header() string {
	return s.Kind.String() + "; entry=" + s.Entry + "; content=" + s.Content.String()
}

// Limits bound an extraction. Zero fields take defaults.
type Limits struct {
	// MaxBytes bounds the extracted raster (errs.KindLimitExceeded);
	// default DefaultMaxEntrySize.
	MaxBytes int64
	// MaxSVGBytes bounds a decompressed SVGZ document
	// (errs.KindLimitExceeded); default DefaultMaxSVGBytes.
	MaxSVGBytes int64
	// MaxPixels bounds an image decoded here (a JNG with alpha,
	// errs.KindLimitExceeded before anything is decoded); default
	// raster.DefaultMaxPixels.
	MaxPixels int64
	// Zip bounds the ZIP containers; its MaxEntrySize is MaxBytes.
	Zip ZipLimits
}

// DefaultMaxSVGBytes is the default SVGZ output bound (the default of
// XI_LIMITS_MAX_SVG_BYTES).
const DefaultMaxSVGBytes = 10 << 20

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxEntrySize
	}
	if l.MaxSVGBytes <= 0 {
		l.MaxSVGBytes = DefaultMaxSVGBytes
	}
	if l.MaxPixels <= 0 {
		l.MaxPixels = raster.DefaultMaxPixels
	}
	l.Zip.MaxEntrySize = l.MaxBytes
	l.Zip = l.Zip.withDefaults()
	return l
}

// Embedded is an image found inside a container, ready to be copied out.
type Embedded struct {
	Source Source
	size   int
	// read fills dst (Size bytes).
	read func(dst []byte) error
}

// Size is the extracted image's size in bytes.
func (e *Embedded) Size() int { return e.size }

// ReadInto writes the image into dst, which must be Size bytes (the
// caller's allocation, C memory in production). A ZIP entry is inflated
// straight into dst.
func (e *Embedded) ReadInto(dst []byte) error {
	if len(dst) != e.size {
		return errs.Newf(errs.KindInternal, "container: buffer of %d bytes for %d", len(dst), e.size)
	}
	return e.read(dst)
}

// Signatures.
const (
	magicGzip = "\x1f\x8b\x08"
	magicPDN  = "PDN3"
	magicPNG  = "\x89PNG\r\n\x1a\n"
)

// Detect reports the container kind of data from its signature and, for
// ZIP and gzip, a bounded look inside (the central directory; the first
// decompressed bytes). It does not extract anything; ok is false for
// anything that is not a container this package reads.
func Detect(data []byte) (Kind, bool) {
	switch {
	case IsZip(data):
		z, err := OpenZip(data, ZipLimits{})
		if err != nil {
			return KindNone, false
		}
		k, _ := zipKind(z)
		return k, k != KindNone
	case isSVGZ(data):
		return KindSVGZ, true
	case isPDN(data):
		return KindPDN, true
	case isMNG(data):
		return KindMNG, true
	case isJNG(data):
		return KindJNG, true
	}
	return KindNone, false
}

// Open recognises a container and locates its image without extracting it.
// It returns nil, nil for data that is not a container. A recognised but
// malformed container fails with errs.KindCorrupt; one without a usable
// image (a Paint.NET file without a thumbnail, an entry in an unsupported
// compression method) with errs.KindUnsupported; limits with
// errs.KindLimitExceeded.
func Open(data []byte, lim Limits) (*Embedded, error) {
	lim = lim.withDefaults()
	switch {
	case IsZip(data):
		return openZip(data, lim)
	case isSVGZ(data):
		return openSVGZ(data, lim)
	case isPDN(data):
		return openPDN(data, lim)
	case isMNG(data):
		return openMNG(data, lim)
	case isJNG(data):
		return openJNG(data, lim)
	}
	return nil, nil
}

// zipKind classifies an archive by its contents: the stored mimetype entry
// of OpenRaster and Krita, the entries only Procreate and Sketch write.
func zipKind(z *Zip) (Kind, error) {
	if e, ok, err := z.Lookup("mimetype"); err != nil {
		return KindNone, err
	} else if ok {
		mt, err := z.ReadSmall(e, 64)
		if err != nil {
			return KindNone, err
		}
		switch string(bytes.TrimSpace(mt)) {
		case "image/openraster":
			return KindORA, nil
		case "application/x-krita":
			return KindKRA, nil
		}
		return KindNone, nil
	}
	switch {
	case z.Has("Document.archive") && z.Has("QuickLook/Thumbnail.png"):
		return KindProcreate, nil
	case z.Has("document.json") && z.Has("meta.json") && z.Has("previews/preview.png"):
		return KindSketch, nil
	}
	return KindNone, nil
}

// zipCandidate is an entry that may hold the image, in order of
// preference.
type zipCandidate struct {
	name    string
	content Content
}

func zipCandidates(k Kind) []zipCandidate {
	switch k {
	case KindORA:
		return []zipCandidate{{"mergedimage.png", ContentComposite}, {"Thumbnails/thumbnail.png", ContentThumbnail}}
	case KindKRA:
		return []zipCandidate{{"mergedimage.png", ContentComposite}, {"preview.png", ContentThumbnail}}
	case KindProcreate:
		return []zipCandidate{{"QuickLook/Thumbnail.png", ContentThumbnail}}
	case KindSketch:
		return []zipCandidate{{"previews/preview.png", ContentPreview}}
	default:
		return nil
	}
}

func openZip(data []byte, lim Limits) (*Embedded, error) {
	z, err := OpenZip(data, lim.Zip)
	if err != nil {
		return nil, err
	}
	k, err := zipKind(z)
	if err != nil || k == KindNone {
		return nil, err
	}
	for _, c := range zipCandidates(k) {
		e, ok, err := z.Lookup(c.name)
		if err != nil {
			return nil, err
		}
		if !ok || e.Size == 0 {
			continue // an empty merged image: try the thumbnail
		}
		if _, _, err := z.Check(e); err != nil {
			return nil, err
		}
		return &Embedded{
			Source: Source{Kind: k, Entry: c.name, Content: c.content},
			size:   int(e.Size),
			read: func(dst []byte) error {
				if err := z.ReadInto(e, dst); err != nil {
					return err
				}
				if !bytes.HasPrefix(dst, []byte(magicPNG)) {
					return errs.Newf(errs.KindCorrupt, "container: %s %s is not a PNG", k, c.name)
				}
				return nil
			},
		}, nil
	}
	return nil, errs.WithPublic(errs.Newf(errs.KindUnsupported, "container: %s without a composite or thumbnail", k),
		k.String()+" file without an embedded image is not supported")
}

// isSVGZ reports a gzip stream whose first decompressed bytes are an SVG
// document. At most svg.SniffLen bytes are inflated.
func isSVGZ(data []byte) bool {
	if len(data) < 18 || string(data[:3]) != magicGzip {
		return false
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return false
	}
	head := make([]byte, svg.SniffLen)
	n, err := io.ReadFull(zr, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false
	}
	return svg.IsSVG(head[:n])
}

// openSVGZ gunzips the document, up to lim.MaxSVGBytes and at most
// lim.Zip.MaxRatio times the compressed size.
func openSVGZ(data []byte, lim Limits) (*Embedded, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errs.Wrap(errs.KindCorrupt, err)
	}
	maxOut := min(lim.MaxSVGBytes, max(int64(len(data))*lim.Zip.MaxRatio, 1<<16))
	var out bytes.Buffer
	n, err := io.Copy(&out, io.LimitReader(zr, maxOut+1))
	switch {
	case err != nil:
		return nil, errs.Wrapf(errs.KindCorrupt, err, "container: gunzip svgz")
	case n > maxOut && maxOut == lim.MaxSVGBytes:
		return nil, errs.Newf(errs.KindLimitExceeded, "container: svgz inflates beyond %d bytes", lim.MaxSVGBytes)
	case n > maxOut:
		return nil, errs.Newf(errs.KindLimitExceeded, "container: svgz compression ratio exceeds %d", lim.Zip.MaxRatio)
	case !svg.IsSVG(out.Bytes()):
		return nil, errs.New(errs.KindCorrupt, "container: svgz does not hold an svg document")
	}
	doc := out.Bytes()
	return &Embedded{
		Source: Source{Kind: KindSVGZ, Entry: "gzip", Content: ContentDocument},
		size:   len(doc),
		read:   func(dst []byte) error { copy(dst, doc); return nil },
	}, nil
}

// Paint.NET: "PDN3", a 24-bit little-endian header length, then the UTF-8
// XML header <pdnImage width=".." height=".." layers=".."
// savedWithVersion=".."><custom><thumb png="base64"/></custom></pdnImage>.
const (
	pdnPrefixLen = 7
	// maxPDNHeader bounds the XML header (a 256 px PNG thumbnail in
	// base64 is well under 1 MiB).
	maxPDNHeader = 4 << 20
)

func isPDN(data []byte) bool {
	return len(data) >= pdnPrefixLen && string(data[:4]) == magicPDN
}

// pdnHeader is the part of the XML header read here.
type pdnHeader struct {
	XMLName xml.Name `xml:"pdnImage"`
	Width   int      `xml:"width,attr"`
	Height  int      `xml:"height,attr"`
	Thumb   struct {
		PNG string `xml:"png,attr"`
	} `xml:"custom>thumb"`
}

func openPDN(data []byte, lim Limits) (*Embedded, error) {
	n := int(data[4]) | int(data[5])<<8 | int(data[6])<<16
	if n == 0 || n > maxPDNHeader || pdnPrefixLen+n > len(data) {
		return nil, errs.New(errs.KindCorrupt, "container: bad paint.net header length")
	}
	var h pdnHeader
	dec := xml.NewDecoder(bytes.NewReader(data[pdnPrefixLen : pdnPrefixLen+n]))
	dec.Strict = true
	if err := dec.Decode(&h); err != nil {
		return nil, errs.Wrapf(errs.KindCorrupt, err, "container: paint.net header")
	}
	if h.Thumb.PNG == "" {
		return nil, errs.WithPublic(errs.New(errs.KindUnsupported, "container: paint.net file without a thumbnail"),
			"Paint.NET file without an embedded thumbnail is not supported")
	}
	if int64(base64.StdEncoding.DecodedLen(len(h.Thumb.PNG))) > lim.MaxBytes {
		return nil, errs.Newf(errs.KindLimitExceeded, "container: paint.net thumbnail exceeds %d bytes", lim.MaxBytes)
	}
	png, err := base64.StdEncoding.DecodeString(h.Thumb.PNG)
	if err != nil {
		return nil, errs.Wrapf(errs.KindCorrupt, err, "container: paint.net thumbnail")
	}
	if !bytes.HasPrefix(png, []byte(magicPNG)) {
		return nil, errs.New(errs.KindCorrupt, "container: paint.net thumbnail is not a PNG")
	}
	return &Embedded{
		Source: Source{Kind: KindPDN, Entry: "header thumbnail", Content: ContentThumbnail},
		size:   len(png),
		read:   func(dst []byte) error { copy(dst, png); return nil },
	}, nil
}
