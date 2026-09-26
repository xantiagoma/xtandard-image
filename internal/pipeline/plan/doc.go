// Package plan turns a normalised transform.Request and the header metadata
// of the source image into an ordered list of typed processing steps
// (docs/ARCHITECTURE.md §8.1). It is pure Go: no cgo, no I/O, deterministic.
//
// The geometry is imgproxy's (processing/prepare.go, scale_on_load.go,
// crop.go, calc_position.go, extend.go, padding.go, fix_size.go, gravity.go),
// reimplemented step for step so that the same URL yields the same pixels
// geometry: result size for every resizing type with enlarge, extend,
// extend_aspect_ratio, dpr, zoom and min-width/min-height; shrink-on-load
// factor selection; EXIF orientation; pre-crop and result crop placement
// with compass gravity, offsets and focus points; padding;
// max_result_dimension; animated inputs processed frame by frame. The test
// corpus in testdata/imgproxy_corpus.json was recorded by running imgproxy's
// own pipeline (see testdata/imgproxy_harness.go.txt).
//
// # Protocol with the executor
//
// Build predicts the whole pipeline from the header. Three things cannot be
// known from a header, and for each the executor reports what it observed
// through Continue, which returns the complete plan again with the geometry
// recomputed; steps up to and including the observed one are unchanged and
// must be skipped:
//
//   - the size a loader actually produced with shrink-on-load, when it
//     differs from LoadStep.Width/Height (loader rounding);
//   - an embedded HEIF/AVIF thumbnail (LoadStep.Thumbnail): Continue decides
//     whether it is worth using, exactly like imgproxy, and otherwise returns
//     a plan that loads the full image;
//   - the size after TrimStep.
//
// Crop and extend steps carry their gravity, so the executor places them on
// the real image size with Rect; the X/Y/W/H fields are the prediction.
package plan
