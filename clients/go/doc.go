// Package xtandard is the official Go client of xtandard-image, a self-hosted
// image and video processing server (https://github.com/xantiagoma/xtandard-image).
//
// The module has one package per concern:
//
//   - xtandard (this package): signing (imgproxy and xi_sig), enc/ source and
//     PDF password encryption, source references, escaping, byte ranges, the
//     option model ([Option], [ProcessingOption], [InfoOption], [VideoOption]),
//     the catalogue name tables, the constants of the server's URL grammar,
//     the JSON response types ([InfoResponse], [ErrorResponse]) and the
//     URL-building errors ([BuildError], matched with errors.Is on an
//     [ErrorCode] such as [ErrInvalidSource]).
//   - imgproxy: URLs of the imgproxy dialect (processing, /info, /video, HLS
//     and DASH), as a call with a params struct or as a fluent chain.
//   - query: URLs of the query dialect (/q/, /q/video, imaginary endpoints).
//   - processing, info, video: one typed struct per option of each catalogue,
//     generated from the server's catalogues (processing.Resize, info.Size,
//     video.Start, …).
//   - client: an HTTP client on net/http for /info, images, /video (Range and
//     HEAD), HLS/DASH manifests and the health endpoints.
//
// Build and sign URLs on a server: the signing key signs every URL, and the
// encryption key decrypts every enc/ source and PDF password.
//
// Every URL is byte-for-byte the URL the TypeScript and Python clients build
// for the same input; the shared fixtures in the repository (clients/spec,
// testdata/urls, testdata/security) prove it.
package xtandard
