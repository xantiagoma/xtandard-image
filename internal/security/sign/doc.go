// Package sign verifies (and, for tests and the playground, produces) URL
// signatures for every dialect (docs/ARCHITECTURE.md §10,
// docs/URL-DIALECTS.md §2).
//
// The package has three layers:
//
//   - [Scheme] is one signing algorithm plus its wire encoding and
//     truncation: it turns (key, message) into raw signature bytes and
//     decodes a presented signature into raw bytes. Later dialects (imgix
//     MD5, thumbor HMAC-SHA1, imaginary, cloudinary, ...) add a Scheme here.
//   - [Keyring] holds the key material: one or more (secret, salt) pairs,
//     tried in order so keys can be rotated without downtime.
//   - [Verifier] is what a dialect returns from dialect.Parser.Verifier. The
//     dialect extracts the presented signature and builds the signed message
//     (its canonical form differs per dialect); the Verifier decides
//     [Unsigned], [Verified] or [Trusted], or returns an errs error of kind
//     errs.KindUnauthorized.
//
// # imgproxy scheme
//
// signature = base64url-nopad(HMAC-SHA256(key, salt ‖ path)[:size])
//
// key and salt are hex-decoded (XI_SIGNING_KEYS / XI_SIGNING_SALTS, same
// count), path is everything after the signature segment starting with "/"
// (a missing leading "/" is added, as imgproxy does), and size is
// XI_SIGNING_SIZE (1–32 bytes, default 32). A presented signature is accepted
// when it matches any key/salt pair or is listed verbatim in
// XI_SIGNING_TRUSTED. With no keys configured signing is off: every
// signature segment ("insecure", "unsafe", "_", anything) is [Unsigned],
// except one containing ":", which is a processing option in the signature
// position and is rejected like imgproxy does. See [NewImgproxyVerifier].
//
// # xi_sig scheme
//
// For dialects without a native scheme, the query parameter xi_sig carries
//
//	base64url-nopad(HMAC-SHA256(key, salt ‖ path [‖ "?" ‖ query])[:size])
//
// with the same key/salt pairs, size and trusted list as imgproxy. query is
// the raw query string with every xi_sig parameter removed, the remaining
// parameters kept in their original order and encoding; the "?" is present
// only when that remainder is non-empty. See [XIMessage] and
// [NewXIVerifier].
//
// # imaginary scheme
//
// The query dialect's native scheme is imaginary's sign parameter:
//
//	base64url-nopad(HMAC-SHA256(key, path ‖ url.Values.Encode(query without sign)))
//
// i.e. the decoded path followed directly by the re-encoded query with keys
// sorted, no "?" and no truncation. imaginary has no salt, so salts are
// ignored. See [ImaginaryMessage] and [NewImaginaryVerifier]
// (imaginary middleware.go validateURLSignature, MIT).
//
// # imgix scheme
//
// The imgix dialect's s parameter (imgix-blueprint "Securing URLs"):
//
//	lowercase-hex(MD5(token ‖ path [‖ "?" ‖ query]))
//
// token is the key's secret used verbatim (build it with NewKey, not
// ParseHexKeyring), path is the escaped path with its leading "/", and query
// is the raw query without s in its original order and encoding; the "?" is
// present only when that remainder is non-empty. Salts are ignored. See
// [ImgixMessage] and [NewImgixVerifier].
//
// # ImageKit scheme
//
// The ImageKit dialect's native scheme (imagekit-nodejs helper.ts
// getSignature, Apache-2.0):
//
//	ik-s = hex(HMAC-SHA1(private_key, path ["?" query] expiry))
//
// path is the escaped URL path relative to the URL endpoint, query the
// raw query without ik-s and ik-t, and expiry the ik-t value (unix
// seconds) or 9999999999 when the URL has no ik-t. The private key is used
// as written. See [ImageKitMessage] and [NewImageKitVerifier].
//
// # Security
//
// Signatures are compared with crypto/subtle. Key material lives in []byte
// owned by the [Keyring], never appears in error text, and renders as
// "[REDACTED]" in fmt and log/slog; [Keyring.Destroy] zeroes it.
//
// The HMAC construction and the test vectors follow imgproxy
// (security/signature.go, security/signature_test.go, docs "Signing the
// URL"; Copyright Sergey Alexandrovich, Apache License 2.0).
package sign
