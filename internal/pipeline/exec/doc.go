// Package exec runs a pipeline/plan Plan on libvips and encodes the result
// (docs/ARCHITECTURE.md §8.2, pipeline stages 12–13).
//
// One Run is one image: everything happens inside a single vips.Run (one
// locked OS thread) and every intermediate image is tracked by one vips.Arena
// that is closed before Run returns, on success and on every error path.
// libvips is demand-driven, so the steps only build a graph; the pixels are
// computed by the encoder's save, which kills the pipeline when ctx ends
// (vips.KillOnCancel is armed by every sink). ctx is also checked between
// steps.
//
// # Order
//
//  1. Load with the plan's shrink-on-load (JPEG shrink, WebP/SVG scale,
//     density, HEIF/AVIF embedded thumbnail, page and frames). BMP and ICO
//     are decoded by codec/bmp and codec/ico and handed over with
//     vips.NewFromMemory. The EXIF orientation tag is dropped: the plan
//     applies orientation as explicit RotateStep/FlipStep.
//  2. What a header cannot predict is reported to the planner through
//     plan.Continue (loaded size, embedded thumbnail, size after trim) and
//     execution continues on the returned plan after the observed step.
//  3. Steps in plan order. Crops and extends are placed on the real image
//     size with the steps' Rect methods. Animations are split into frames by
//     page-height; Steps run on every frame, the frames are joined back with
//     vips.Arrayjoin and page-height, delays and loop count are restored before
//     the After steps run on the strip.
//  4. Chained pipelines are planned again with plan.Chain on the observed
//     result of the previous one (size, frames, alpha; orientation already
//     applied), after a trim of their own with the trimmed size too. The
//     result is computed into memory first only when the plan says so
//     (Materialize: a step reads it out of order); otherwise the chained
//     steps extend the lazy graph. Errors name the pipeline index.
//  5. codec/encode encodes the final image.
//
// The step semantics follow imgproxy's processing package (Apache-2.0):
// resize premultiplied when the image has alpha, ICC import to sRGB unless
// the profile is sRGB IEC61966-2.1, embed with a transparent black canvas that
// flatten later fills with the background, smart crop on a memory copy, and
// trim compared in sRGB with transparency flattened.
//
// Filters (blur, sharpen, pixelate; unsharp masking, blur areas and
// progressive blur in filter_extras.go), colour adjustments (colour_math.go,
// colour_adjust.go) and the configured watermark are executed per frame. Options that have no implementation yet (per-request watermark
// sources and text, chessboard tiling, the plan's Unplanned options) are
// skipped and reported in Result.Skipped.
package exec
