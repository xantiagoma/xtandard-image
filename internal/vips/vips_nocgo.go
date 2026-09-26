//go:build !cgo

package vips

import (
	"context"
	"image"
	"io"
)

// Stubs for CGO_ENABLED=0 builds: the module keeps compiling and every
// operation fails with ErrUnavailable. The real implementation is in the
// cgo files of this package; the pure-Go types (options, enums, Header,
// Arena) are shared.

// Init fails: libvips is not linked.
func Init(*Config) error { return ErrUnavailable }

// Shutdown does nothing.
func Shutdown() {}

// SetLeakCheck does nothing.
func SetLeakCheck(bool) {}

// Run fails without calling fn.
func Run(context.Context, func() error) error { return ErrUnavailable }

// ReadMemStats returns zeros.
func ReadMemStats() MemStats { return MemStats{} }

// LiveObjects returns 0.
func LiveObjects() int { return 0 }

// KillOnCancel returns a no-op.
func KillOnCancel(context.Context, *Image) func() { return func() {} }

// Buffer is unavailable without cgo.
type Buffer struct{}

// NewBuffer fails.
func NewBuffer(int) (*Buffer, error) { return nil, ErrUnavailable }

// NewBufferFromReader fails.
func NewBufferFromReader(io.Reader, int64) (*Buffer, error) { return nil, ErrUnavailable }

// Bytes returns nil.
func (*Buffer) Bytes() []byte { return nil }

// Len returns 0.
func (*Buffer) Len() int { return 0 }

// Retain does nothing.
func (*Buffer) Retain() {}

// Release does nothing.
func (*Buffer) Release() {}

// Blob is unavailable without cgo.
type Blob struct{}

// Bytes returns nil.
func (*Blob) Bytes() []byte { return nil }

// Len returns 0.
func (*Blob) Len() int { return 0 }

// Retain does nothing.
func (*Blob) Retain() {}

// Release does nothing.
func (*Blob) Release() {}

// Image is unavailable without cgo.
type Image struct{}

// Probe fails.
func Probe(context.Context, *Buffer) (Header, error) { return Header{}, ErrUnavailable }

// ProbeDensity returns ErrUnavailable.
func ProbeDensity(context.Context, *Buffer, float64) (Header, error) {
	return Header{}, ErrUnavailable
}

// Load fails.
func Load(context.Context, *Buffer, *LoadOptions) (*Image, error) { return nil, ErrUnavailable }

// Thumbnail fails.
func Thumbnail(context.Context, *Buffer, *ThumbnailOptions) (*Image, error) {
	return nil, ErrUnavailable
}

// LoadThumbnail fails.
func LoadThumbnail(context.Context, *Buffer, int, int) (*Image, error) {
	return nil, ErrUnavailable
}

// Black fails.
func Black(context.Context, int, int, int) (*Image, error) { return nil, ErrUnavailable }

// NewFromMemory fails.
func NewFromMemory(context.Context, []byte, int, int, int, BandFormat) (*Image, error) {
	return nil, ErrUnavailable
}

// Arrayjoin fails.
func Arrayjoin(context.Context, []*Image, int) (*Image, error) { return nil, ErrUnavailable }

// ProfileLoad fails.
func ProfileLoad(context.Context, string) ([]byte, error) { return nil, ErrUnavailable }

// Bandjoin fails.
func Bandjoin(context.Context, []*Image) (*Image, error) { return nil, ErrUnavailable }

// Close does nothing.
func (*Image) Close() {}

// Width returns 0.
func (*Image) Width() int { return 0 }

// Height returns 0.
func (*Image) Height() int { return 0 }

// Bands returns 0.
func (*Image) Bands() int { return 0 }

// BandFormat returns BandFormatNotSet.
func (*Image) BandFormat() BandFormat { return BandFormatNotSet }

// Interpretation returns InterpretationError.
func (*Image) Interpretation() Interpretation { return InterpretationError }

// HasAlpha returns false.
func (*Image) HasAlpha() bool { return false }

// Orientation returns 1.
func (*Image) Orientation() int { return 1 }

// PageHeight returns 0.
func (*Image) PageHeight() int { return 0 }

// Pages returns 0.
func (*Image) Pages() int { return 0 }

// XRes returns 0.
func (*Image) XRes() float64 { return 0 }

// YRes returns 0.
func (*Image) YRes() float64 { return 0 }

// ImageBytes returns 0.
func (*Image) ImageBytes() int64 { return 0 }

// Int reports no value.
func (*Image) Int(MetaField) (int, bool) { return 0, false }

// Ints reports no value.
func (*Image) Ints(MetaField) ([]int, bool) { return nil, false }

// Blob reports no value.
func (*Image) Blob(MetaField) ([]byte, bool) { return nil, false }

// Text reports no value.
func (*Image) Text(MetaField) (string, bool) { return "", false }

// Fields reports no fields.
func (*Image) Fields() []MetaField { return nil }

// CopyMemory fails.
func (*Image) CopyMemory(context.Context) (*Image, error) { return nil, ErrUnavailable }

// SetInt fails.
func (*Image) SetInt(context.Context, MetaField, int) (*Image, error) { return nil, ErrUnavailable }

// SetInts fails.
func (*Image) SetInts(context.Context, MetaField, []int) (*Image, error) {
	return nil, ErrUnavailable
}

// SetBlob fails.
func (*Image) SetBlob(context.Context, MetaField, []byte) (*Image, error) {
	return nil, ErrUnavailable
}

// RemoveMetadata fails.
func (*Image) RemoveMetadata(context.Context, ...MetaField) (*Image, error) {
	return nil, ErrUnavailable
}

// SetOrientation fails.
func (*Image) SetOrientation(context.Context, int) (*Image, error) { return nil, ErrUnavailable }

// RemoveOrientation fails.
func (*Image) RemoveOrientation(context.Context) (*Image, error) { return nil, ErrUnavailable }

// SetPageHeight fails.
func (*Image) SetPageHeight(context.Context, int) (*Image, error) { return nil, ErrUnavailable }

// WriteToMemory fails.
func (*Image) WriteToMemory(context.Context) ([]byte, error) { return nil, ErrUnavailable }

// Shared is unavailable without cgo.
type Shared struct{}

// Share fails.
func Share(context.Context, *Image) (*Shared, error) { return nil, ErrUnavailable }

// Image returns nil.
func (*Shared) Image() *Image { return nil }

// Width returns 0.
func (*Shared) Width() int { return 0 }

// Height returns 0.
func (*Shared) Height() int { return 0 }

// Bands returns 0.
func (*Shared) Bands() int { return 0 }

// HasAlpha returns false.
func (*Shared) HasAlpha() bool { return false }

// Close does nothing.
func (*Shared) Close() {}

// Resize fails.
func (*Image) Resize(context.Context, float64, float64, Kernel) (*Image, error) {
	return nil, ErrUnavailable
}

// ExtractArea fails.
func (*Image) ExtractArea(context.Context, image.Rectangle) (*Image, error) {
	return nil, ErrUnavailable
}

// Crop fails.
func (*Image) Crop(context.Context, image.Rectangle) (*Image, error) { return nil, ErrUnavailable }

// Embed fails.
func (*Image) Embed(context.Context, int, int, int, int, Extend, []float64) (*Image, error) {
	return nil, ErrUnavailable
}

// Gravity fails.
func (*Image) Gravity(context.Context, Compass, int, int, Extend, []float64) (*Image, error) {
	return nil, ErrUnavailable
}

// Flip fails.
func (*Image) Flip(context.Context, Direction) (*Image, error) { return nil, ErrUnavailable }

// Rot fails.
func (*Image) Rot(context.Context, Angle) (*Image, error) { return nil, ErrUnavailable }

// Autorot fails.
func (*Image) Autorot(context.Context) (*Image, error) { return nil, ErrUnavailable }

// SmartCrop fails.
func (*Image) SmartCrop(context.Context, int, int, Interesting) (*Image, error) {
	return nil, ErrUnavailable
}

// Flatten fails.
func (*Image) Flatten(context.Context, []float64) (*Image, error) { return nil, ErrUnavailable }

// Premultiply fails.
func (*Image) Premultiply(context.Context) (*Image, error) { return nil, ErrUnavailable }

// Unpremultiply fails.
func (*Image) Unpremultiply(context.Context) (*Image, error) { return nil, ErrUnavailable }

// Colourspace fails.
func (*Image) Colourspace(context.Context, Interpretation) (*Image, error) {
	return nil, ErrUnavailable
}

// ICCImport fails.
func (*Image) ICCImport(context.Context, string, Intent, bool) (*Image, error) {
	return nil, ErrUnavailable
}

// ICCExport fails.
func (*Image) ICCExport(context.Context, string, Intent, int) (*Image, error) {
	return nil, ErrUnavailable
}

// ICCTransform fails.
func (*Image) ICCTransform(context.Context, string, Intent, bool, int) (*Image, error) {
	return nil, ErrUnavailable
}

// Cast fails.
func (*Image) Cast(context.Context, BandFormat) (*Image, error) { return nil, ErrUnavailable }

// Copy fails.
func (*Image) Copy(context.Context, CopyOptions) (*Image, error) { return nil, ErrUnavailable }

// Gaussblur fails.
func (*Image) Gaussblur(context.Context, float64) (*Image, error) { return nil, ErrUnavailable }

// Sharpen fails.
func (*Image) Sharpen(context.Context, SharpenOptions) (*Image, error) {
	return nil, ErrUnavailable
}

// Shrink fails.
func (*Image) Shrink(context.Context, int, int) (*Image, error) { return nil, ErrUnavailable }

// Composite2 fails.
func (*Image) Composite2(context.Context, *Image, BlendMode, int, int) (*Image, error) {
	return nil, ErrUnavailable
}

// Replicate fails.
func (*Image) Replicate(context.Context, int, int) (*Image, error) { return nil, ErrUnavailable }

// Join fails.
func (*Image) Join(context.Context, *Image, Direction) (*Image, error) {
	return nil, ErrUnavailable
}

// Linear fails.
func (*Image) Linear(context.Context, []float64, []float64) (*Image, error) {
	return nil, ErrUnavailable
}

// Invert fails.
func (*Image) Invert(context.Context) (*Image, error) { return nil, ErrUnavailable }

// CastRound fails.
func (*Image) CastRound(context.Context, BandFormat) (*Image, error) { return nil, ErrUnavailable }

// MapLUT fails.
func (*Image) MapLUT(context.Context, *Image) (*Image, error) { return nil, ErrUnavailable }

// Recomb fails.
func (*Image) Recomb(context.Context, []float64, int) (*Image, error) { return nil, ErrUnavailable }

// Mix fails.
func (*Image) Mix(context.Context, *Image, float64) (*Image, error) { return nil, ErrUnavailable }

// GradientMask fails.
func GradientMask(context.Context, int, int, float64, float64, float64, float64) (*Image, error) {
	return nil, ErrUnavailable
}

// BandjoinConst fails.
func (*Image) BandjoinConst(context.Context, []float64) (*Image, error) {
	return nil, ErrUnavailable
}

// ExtractBand fails.
func (*Image) ExtractBand(context.Context, int, int) (*Image, error) { return nil, ErrUnavailable }

// AddAlpha fails.
func (*Image) AddAlpha(context.Context) (*Image, error) { return nil, ErrUnavailable }

// FindTrim fails.
func (*Image) FindTrim(context.Context, float64, []float64, bool) (image.Rectangle, error) {
	return image.Rectangle{}, ErrUnavailable
}

// Trim fails.
func (*Image) Trim(context.Context, float64, []float64, bool) (*Image, error) {
	return nil, ErrUnavailable
}

// Similarity fails.
func (*Image) Similarity(context.Context, float64, float64, []float64) (*Image, error) {
	return nil, ErrUnavailable
}

// Affine fails.
func (*Image) Affine(context.Context, float64, float64, float64, float64, []float64) (*Image, error) {
	return nil, ErrUnavailable
}

// Save fails.
func (*Image) Save(context.Context, Format, *SaveOptions) (*Blob, error) {
	return nil, ErrUnavailable
}

// SaveJPEG fails.
func (*Image) SaveJPEG(context.Context, int) (*Blob, error) { return nil, ErrUnavailable }
