package vips

/*
#include "shim.h"
*/
import "C"

import (
	"context"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Image is a handle on one libvips image node. Operations return new nodes
// that reference their inputs; pixels are computed lazily when a sink
// (Save, WriteToMemory) pulls them. Close every Image exactly once, inside
// the Run that created it; an Arena does that in one place.
//
// Image is not safe for concurrent use: one goroutine (the Run's locked
// thread) owns it.
type Image struct {
	p *C.VipsImage
	// src is a reference to the source Buffer's blob (nil for synthetic
	// images). Derived images carry it too, so a failed sink can be
	// attributed to a decode failure of the source (opError).
	src *C.VipsBlob
}

// newImage wraps out, taking a reference to src.
func newImage(out *C.VipsImage, src *C.VipsBlob) *Image {
	return &Image{p: out, src: C.xi_blob_ref(src)}
}

// derive wraps an operation's output that depends on img.
func (img *Image) derive(out *C.VipsImage) *Image { return newImage(out, img.src) }

// Close unrefs the image. It is idempotent and nil-safe.
func (img *Image) Close() {
	if img == nil {
		return
	}
	if img.p != nil {
		C.xi_image_unref(img.p)
		img.p = nil
	}
	if img.src != nil {
		C.xi_blob_unref(img.src)
		img.src = nil
	}
}

// Width returns the image width in pixels.
func (img *Image) Width() int { return int(C.xi_image_width(img.p)) }

// Height returns the image height in pixels: all frames for an animation
// loaded with NPages != 1 (see PageHeight).
func (img *Image) Height() int { return int(C.xi_image_height(img.p)) }

// Bands returns the number of bands, alpha included.
func (img *Image) Bands() int { return int(C.xi_image_bands(img.p)) }

// BandFormat returns the numeric type of the bands.
func (img *Image) BandFormat() BandFormat { return BandFormat(C.xi_image_band_format(img.p)) }

// Interpretation returns the (sanity-checked) colour space.
func (img *Image) Interpretation() Interpretation {
	return Interpretation(C.xi_image_interpretation(img.p))
}

// HasAlpha reports whether the last band is alpha.
func (img *Image) HasAlpha() bool { return C.xi_image_has_alpha(img.p) != 0 }

// Orientation returns the EXIF orientation 1..8 (1 when absent).
func (img *Image) Orientation() int { return int(C.xi_image_orientation(img.p)) }

// PageHeight returns the height of one frame (page-height), or Height for
// a single-frame image.
func (img *Image) PageHeight() int { return int(C.xi_image_page_height(img.p)) }

// Pages returns the number of frames in the strip (Height / PageHeight).
func (img *Image) Pages() int { return img.Height() / max(img.PageHeight(), 1) }

// XRes and YRes return the resolution in pixels per millimetre.
func (img *Image) XRes() float64 { return float64(C.xi_image_xres(img.p)) }

// YRes returns the vertical resolution in pixels per millimetre.
func (img *Image) YRes() float64 { return float64(C.xi_image_yres(img.p)) }

// ImageBytes returns the uncompressed size of the pixels in bytes, what a
// full decode to memory would take.
func (img *Image) ImageBytes() int64 { return int64(C.xi_image_sizeof(img.p)) }

// Int returns an integer metadata field.
func (img *Image) Int(field MetaField) (int, bool) {
	name := cstr(string(field))
	var v C.int
	if C.xi_meta_get_int(img.p, cptr(name), &v) != 0 {
		return 0, false
	}
	return int(v), true
}

// Ints returns an integer array metadata field, such as FieldDelay.
func (img *Image) Ints(field MetaField) ([]int, bool) {
	name := cstr(string(field))
	var p *C.int
	var n C.int
	if C.xi_meta_get_array_int(img.p, cptr(name), &p, &n) != 0 {
		return nil, false
	}
	out := make([]int, int(n))
	for i, v := range unsafe.Slice(p, int(n)) {
		out[i] = int(v)
	}
	return out, true
}

// Blob returns a copy of a binary metadata field such as FieldEXIF or
// FieldICC. Metadata is small, cold data, so it is copied (go-standards §4).
func (img *Image) Blob(field MetaField) ([]byte, bool) {
	name := cstr(string(field))
	var p unsafe.Pointer
	var n C.size_t
	if C.xi_meta_get_blob(img.p, cptr(name), &p, &n) != 0 {
		return nil, false
	}
	return C.GoBytes(p, C.int(n)), true
}

// Text returns a string metadata field, such as an "exif-ifd0-Copyright"
// tag that a loader parsed from EXIF (libvips renders it as
// "value (description, n components, m bytes)").
func (img *Image) Text(field MetaField) (string, bool) {
	name := cstr(string(field))
	var s *C.char
	if C.xi_meta_get_string(img.p, cptr(name), &s) != 0 {
		return "", false
	}
	return C.GoString(s), true
}

// Fields returns the names of every metadata field on img, in libvips order.
// Loaders expand EXIF into one "exif-ifdN-Tag" string field per tag next to
// the "exif-data" blob; savers rebuild the blob from those fields, so
// removing a tag means removing its field (see RemoveMetadata).
func (img *Image) Fields() []MetaField {
	var n C.size_t
	p := C.xi_meta_fields(img.p, &n)
	if p == nil {
		return nil
	}
	defer C.xi_free(unsafe.Pointer(p))
	raw := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))
	var out []MetaField
	for len(raw) > 0 {
		i := 0
		for raw[i] != 0 {
			i++
		}
		out = append(out, MetaField(raw[:i]))
		raw = raw[i+1:]
	}
	return out
}

// SetInt returns a copy of img with an integer metadata field set. The
// input is never modified: other nodes may share it.
func (img *Image) SetInt(ctx context.Context, field MetaField, v int) (*Image, error) {
	if err := ctxError(ctx, "vips: set metadata"); err != nil {
		return nil, err
	}
	name := cstr(string(field))
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_meta_set_int(img.p, cptr(name), C.int(v), &out, &cerr)
	if err := opError(rc, "set_int", cerr, img.src); err != nil {
		return nil, err
	}
	return img.derive(out), nil
}

// SetInts returns a copy of img with an integer array metadata field set
// (for example FieldDelay).
func (img *Image) SetInts(ctx context.Context, field MetaField, v []int) (*Image, error) {
	if err := ctxError(ctx, "vips: set metadata"); err != nil {
		return nil, err
	}
	name := cstr(string(field))
	cv := make([]C.int, len(v))
	for i, x := range v {
		cv[i] = C.int(x)
	}
	var p *C.int
	if len(cv) > 0 {
		p = &cv[0]
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_meta_set_array_int(img.p, cptr(name), p, C.int(len(cv)), &out, &cerr)
	if err := opError(rc, "set_ints", cerr, img.src); err != nil {
		return nil, err
	}
	return img.derive(out), nil
}

// SetTexts returns a copy of img with the string metadata fields set, in
// order, in one step (for example the "exif-ifdN-Tag" fields libvips turns
// into an EXIF block on save). Names and values must not contain NUL.
func (img *Image) SetTexts(ctx context.Context, fields ...TextField) (*Image, error) {
	if err := ctxError(ctx, "vips: set metadata"); err != nil {
		return nil, err
	}
	var names, values []byte
	for _, f := range fields {
		if strings.IndexByte(string(f.Field), 0) >= 0 || strings.IndexByte(f.Value, 0) >= 0 {
			return nil, errs.Newf(errs.KindInternal, "vips: metadata field %q contains NUL", f.Field)
		}
		names = append(append(names, f.Field...), 0)
		values = append(append(values, f.Value...), 0)
	}
	if len(fields) == 0 {
		names, values = []byte{0}, []byte{0}
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_meta_set_strings(img.p, cptr(names), cptr(values), C.int(len(fields)), &out, &cerr)
	if err := opError(rc, "set_texts", cerr, img.src); err != nil {
		return nil, err
	}
	return img.derive(out), nil
}

// SetBlob returns a copy of img with a binary metadata field set (EXIF,
// XMP, IPTC or an ICC profile). data is copied.
func (img *Image) SetBlob(ctx context.Context, field MetaField, data []byte) (*Image, error) {
	if err := ctxError(ctx, "vips: set metadata"); err != nil {
		return nil, err
	}
	name := cstr(string(field))
	var p unsafe.Pointer
	if len(data) > 0 {
		p = unsafe.Pointer(&data[0])
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_meta_set_blob(img.p, cptr(name), p, C.size_t(len(data)), &out, &cerr)
	if err := opError(rc, "set_blob", cerr, img.src); err != nil {
		return nil, err
	}
	return img.derive(out), nil
}

// RemoveMetadata returns a copy of img without the given fields. Missing
// fields are ignored.
func (img *Image) RemoveMetadata(ctx context.Context, fields ...MetaField) (*Image, error) {
	if err := ctxError(ctx, "vips: remove metadata"); err != nil {
		return nil, err
	}
	var names []byte
	for _, f := range fields {
		names = append(names, f...)
		names = append(names, 0)
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_meta_remove(img.p, cptr(names), C.int(len(fields)), &out, &cerr)
	if err := opError(rc, "remove_metadata", cerr, img.src); err != nil {
		return nil, err
	}
	return img.derive(out), nil
}

// SetOrientation returns a copy of img tagged with EXIF orientation o.
func (img *Image) SetOrientation(ctx context.Context, o int) (*Image, error) {
	return img.SetInt(ctx, FieldOrientation, o)
}

// RemoveOrientation returns a copy of img without an orientation tag.
func (img *Image) RemoveOrientation(ctx context.Context) (*Image, error) {
	return img.RemoveMetadata(ctx, FieldOrientation)
}

// SetPageHeight returns a copy of img whose frames are h rows tall. h must
// divide Height; libvips ignores it otherwise.
func (img *Image) SetPageHeight(ctx context.Context, h int) (*Image, error) {
	return img.SetInt(ctx, FieldPageHeight, h)
}

// WriteToMemory computes the whole image and returns a copy of its pixels
// (band-interleaved, BandFormat samples, row by row). It is a sink: ctx
// cancellation kills it. Meant for tests and small images.
func (img *Image) WriteToMemory(ctx context.Context) ([]byte, error) {
	if err := ctxError(ctx, "vips: write to memory"); err != nil {
		return nil, err
	}
	stop := KillOnCancel(ctx, img)
	defer stop()
	var p unsafe.Pointer
	var n C.size_t
	var cerr *C.char
	rc := C.xi_write_to_memory(img.p, &p, &n, &cerr)
	if err := stepError(ctx, rc, "write_to_memory", cerr, img.src); err != nil {
		return nil, err
	}
	defer C.xi_free(p)
	return C.GoBytes(p, C.int(n)), nil
}

// WritePixels computes the whole image and returns its pixels
// (band-interleaved, BandFormat samples, row by row) as a Blob of C memory,
// without copying them into Go. It is a sink: ctx cancellation kills it.
func (img *Image) WritePixels(ctx context.Context) (*Blob, error) {
	if err := ctxError(ctx, "vips: write pixels"); err != nil {
		return nil, err
	}
	stop := KillOnCancel(ctx, img)
	defer stop()
	var p unsafe.Pointer
	var n C.size_t
	var cerr *C.char
	rc := C.xi_write_to_memory(img.p, &p, &n, &cerr)
	if err := stepError(ctx, rc, "write_pixels", cerr, img.src); err != nil {
		return nil, err
	}
	return newBlob(p, int(n)), nil
}

// ToRGB8 renders img as 8-bit sRGB with exactly three bands and returns the
// band-interleaved pixels (HWC, R G B) as a Blob of C memory: the input of
// the detection models (internal/detect). Alpha is flattened onto white;
// known colour spaces (grey, 16-bit, CMYK, Lab, ...) are converted to sRGB
// without ICC; one- and two-band images are replicated to grey RGB and
// extra bands are dropped.
func (img *Image) ToRGB8(ctx context.Context) (*Blob, error) {
	var a Arena
	defer a.Close()
	cur := img
	var err error
	if cur.HasAlpha() {
		if cur, err = a.Add(cur.Flatten(ctx, []float64{255, 255, 255})); err != nil {
			return nil, err
		}
	}
	switch cur.Interpretation() {
	case InterpretationSRGB, InterpretationRGB, InterpretationMultiband, InterpretationHistogram,
		InterpretationFourier, InterpretationMatrix, InterpretationError:
		// sRGB already, or no colour space libvips could convert from.
	default:
		if cur, err = a.Add(cur.Colourspace(ctx, InterpretationSRGB)); err != nil {
			return nil, err
		}
	}
	if cur.BandFormat() != BandFormatUchar {
		if cur, err = a.Add(cur.Cast(ctx, BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	switch bands := cur.Bands(); {
	case bands == 2:
		if cur, err = a.Add(cur.ExtractBand(ctx, 0, 1)); err != nil {
			return nil, err
		}
		fallthrough
	case bands == 1 || bands == 2:
		if cur, err = a.Add(Bandjoin(ctx, []*Image{cur, cur, cur})); err != nil {
			return nil, err
		}
	case bands > 3:
		if cur, err = a.Add(cur.ExtractBand(ctx, 0, 3)); err != nil {
			return nil, err
		}
	}
	return cur.WritePixels(ctx)
}

// CopyMemory computes the whole image into memory and returns it as a new
// image with random access and the same metadata (vips_image_copy_memory).
// Use it before saving the same pipeline several times (the max_bytes
// quality search): each later save then only encodes. It is a sink: ctx
// cancellation kills it.
func (img *Image) CopyMemory(ctx context.Context) (*Image, error) {
	if err := ctxError(ctx, "vips: copy memory"); err != nil {
		return nil, err
	}
	stop := KillOnCancel(ctx, img)
	defer stop()
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_copy_memory(img.p, &out, &cerr)
	if err := stepError(ctx, rc, "copy_memory", cerr, img.src); err != nil {
		return nil, err
	}
	return img.derive(out), nil
}

// Black returns a width×height image of bands zero (black) uchar bands.
func Black(ctx context.Context, width, height, bands int) (*Image, error) {
	if err := ctxError(ctx, "vips: black"); err != nil {
		return nil, err
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_black(C.int(width), C.int(height), C.int(bands), &out, &cerr)
	if err := opError(rc, "black", cerr, nil); err != nil {
		return nil, err
	}
	return newImage(out, nil), nil
}

// NewFromMemory copies band-interleaved pixels into a new libvips image
// (codec/bmp and codec/ico decode in Go and hand the pixels over here).
// len(pix) must be width × height × bands × the sample size of format.
func NewFromMemory(ctx context.Context, pix []byte, width, height, bands int, format BandFormat) (*Image, error) {
	if err := ctxError(ctx, "vips: new from memory"); err != nil {
		return nil, err
	}
	if len(pix) == 0 || width <= 0 || height <= 0 || bands <= 0 {
		return nil, errs.Newf(errs.KindInternal, "vips: new from memory: %d bytes for %dx%dx%d",
			len(pix), width, height, bands)
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_new_from_memory(unsafe.Pointer(&pix[0]), C.size_t(len(pix)), C.int(width),
		C.int(height), C.int(bands), C.int(format), &out, &cerr)
	if err := opError(rc, "new_from_memory", cerr, nil); err != nil {
		return nil, err
	}
	return newImage(out, nil), nil
}

// NewFromBuffer wraps band-interleaved pixels held in buf as a libvips
// image without copying (internal/video/ffmpeg decodes frames straight into
// a Buffer). buf must hold at least width × height × bands × the sample
// size of format bytes; it may be longer. The image keeps its own
// reference to buf's memory until libvips closes it, so the caller may
// Release buf right away.
func NewFromBuffer(ctx context.Context, buf *Buffer, width, height, bands int, format BandFormat) (*Image, error) {
	if err := ctxError(ctx, "vips: new from buffer"); err != nil {
		return nil, err
	}
	if buf == nil || buf.blob == nil || width <= 0 || height <= 0 || bands <= 0 {
		return nil, errs.Newf(errs.KindInternal, "vips: new from buffer: %dx%dx%d", width, height, bands)
	}
	var out *C.VipsImage
	var cerr *C.char
	rc := C.xi_new_from_blob(buf.blob, C.int(width), C.int(height), C.int(bands), C.int(format), &out, &cerr)
	if err := opError(rc, "new_from_buffer", cerr, nil); err != nil {
		return nil, err
	}
	return newImage(out, nil), nil
}

// Blob is encoded output in C memory allocated by libvips. Bytes exposes it
// without copying, so it can be written straight to the response. It is
// reference counted like Buffer (and satisfies the same interface): Retain
// adds a reference, and the final Release frees the memory.
type Blob struct {
	p    unsafe.Pointer
	n    int
	refs atomic.Int32
}

func newBlob(p unsafe.Pointer, n int) *Blob {
	b := &Blob{p: p, n: n}
	b.refs.Store(1)
	return b
}

// Bytes returns the encoded bytes, valid until the final Release.
func (b *Blob) Bytes() []byte {
	if b.p == nil || b.n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(b.p), b.n)
}

// Len returns the encoded size in bytes.
func (b *Blob) Len() int { return b.n }

// Retain adds a reference; each Retain needs one Release. Retaining a
// released Blob has no effect.
func (b *Blob) Retain() {
	for {
		n := b.refs.Load()
		if n <= 0 || b.refs.CompareAndSwap(n, n+1) {
			return
		}
	}
}

// Release drops a reference; the last one frees the memory. Extra
// Releases are no-ops.
func (b *Blob) Release() {
	for {
		n := b.refs.Load()
		if n <= 0 {
			return
		}
		if b.refs.CompareAndSwap(n, n-1) {
			if n == 1 {
				C.xi_free(b.p)
				b.p, b.n = nil, 0
			}
			return
		}
	}
}
