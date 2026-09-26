// Package encode turns a processed vips.Image into the bytes of the response
// (ARCHITECTURE §8.3, pipeline step 12): one saver per M1 output format of
// FORMATS.md §5, the quality precedence, the metadata policy of FORMATS §4,
// animation, and the max_bytes quality search.
//
// # Quality
//
// The quality of a lossy save is chosen exactly as imgproxy does
// (processing/options.go Quality):
//
//  1. the request's quality (Target.Quality), when > 0;
//  2. the request's format_quality for the output format;
//  3. the configured per-format quality (XI_FORMATS_<FMT>_QUALITY);
//  4. the global XI_FORMATS_QUALITY.
//
// Formats without a quality knob (PNG, GIF, BMP, ICO and every lossless save)
// report Result.Quality 0.
//
// # max_bytes
//
// With Target.MaxBytes > 0 and a quality-driven format, the image is rendered
// to memory once (vips.Image.CopyMemory) and then saved at most MaxSaves
// times: first at the resolved quality, then by binary search below it. The
// largest quality whose output fits wins; when none fits, the smallest output
// is returned with Result.OverBudget set. Other formats are saved once and
// flagged the same way when they exceed the budget.
//
// # Metadata
//
// Target.Metadata applies strip_metadata, keep_copyright and
// strip_color_profile through the saver's keep flags. libvips' keep has one
// bit for all of EXIF, and savers rebuild the EXIF block from the
// "exif-ifdN-Tag" fields a loader parsed, so tags are removed field by field
// (vips.Image.Fields): keep_copyright keeps only the IFD0 Copyright and
// Artist tags (imgproxy's vips_strip), and GPS (IFD 3, plus exif:GPS* XMP
// properties) is removed from every output, even when metadata is kept.
//
// # Animation
//
// A multi-frame image keeps its frames only when Target.Animated is set and
// the format can hold an animation (GIF, WebP, JPEG XL); otherwise the first
// frame is encoded.
//
// # BMP and ICO
//
// These formats never reach libvips' savers: the image is converted to 8-bit
// sRGB with 3 or 4 bands, copied out with WriteToMemory and written by the
// pure-Go encoders of codec/bmp and codec/ico.
//
// Encode must run inside vips.Run, like every other use of a vips.Image.
package encode
