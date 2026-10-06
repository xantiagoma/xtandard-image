package server

import (
	"crypto/md5"  //nolint:gosec // G501: hashsum:md5 is a request-integrity check the URL asks for (imgproxy), not a security primitive
	"crypto/sha1" //nolint:gosec // G505: hashsum:sha1, as above
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"hash"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// verifyHashsum checks the fetched source bytes against the request's
// hashsum (imgproxy Pro `hashsum:<type>:<hex>`): the digest of the bytes
// exactly as the source returned them, before any sanitising or decoding.
// A nil or HashUnset want accepts anything. A mismatch is
// errs.KindHashMismatch (422, ARCHITECTURE §12); it is never answered with
// the fallback image.
//
// The check runs right after the fetch, for processing, raw and /info
// requests alike, so a 304 is never answered for bytes that do not match.
// The hashsum is part of Request.CanonicalKey, so a result-cache hit was
// verified when it was produced.
func verifyHashsum(want *transform.Hashsum, data []byte) error {
	return verifyHashsumBody(want, bytesBlob(data))
}

// verifyHashsumBody is verifyHashsum over a source buffer: a spooled one
// (WP-121b) is hashed from its file.
func verifyHashsumBody(want *transform.Hashsum, body interface{ Bytes() []byte }) error {
	if want == nil || want.Algorithm == transform.HashUnset {
		return nil
	}
	h, ok := newHash(want.Algorithm)
	if !ok {
		return errs.Newf(errs.KindInternal, "server: hashsum algorithm %d", want.Algorithm)
	}
	if err := cache.WriteBody(h, body); err != nil { // hash.Hash.Write never fails; a file read may
		return errs.Wrapf(errs.KindInternal, err, "server: hash the source")
	}
	var buf [sha512.Size * 2]byte
	sum := h.Sum(buf[:0])
	got := hex.AppendEncode(make([]byte, 0, len(sum)*2), sum)
	// Validate lowercased the digest; compare in constant time anyway, the
	// expected value comes from a URL that may be probed.
	if subtle.ConstantTimeCompare(got, []byte(want.Digest)) != 1 {
		return errs.Newf(errs.KindHashMismatch, "server: source %s hashsum mismatch", want.Algorithm.String())
	}
	return nil
}

func newHash(a transform.HashAlgorithm) (hash.Hash, bool) {
	switch a {
	case transform.HashMD5:
		return md5.New(), true //nolint:gosec // G401: see the import
	case transform.HashSHA1:
		return sha1.New(), true //nolint:gosec // G401: see the import
	case transform.HashSHA256:
		return sha256.New(), true
	case transform.HashSHA512:
		return sha512.New(), true
	default:
		return nil, false
	}
}
