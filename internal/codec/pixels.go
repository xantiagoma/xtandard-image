package codec

import "github.com/xantiagoma/xtandard-image/internal/codec/raster"

// Aliases of the pixel types of package raster, where they live so that
// codec/bmp and codec/ico can use them while codec imports those loaders.
type (
	// Raw is raster.Raw: 8-bit interleaved RGB or RGBA samples.
	Raw = raster.Raw
	// Decoded is raster.Decoded: the result of an own loader.
	Decoded = raster.Decoded
)

// DefaultMaxPixels is raster.DefaultMaxPixels.
const DefaultMaxPixels = raster.DefaultMaxPixels
