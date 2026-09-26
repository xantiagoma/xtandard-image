package imageinfo

import (
	"crypto/md5"  //nolint:gosec // G501: a content checksum imgproxy reports, not a security use
	"crypto/sha1" //nolint:gosec // G505: a content checksum imgproxy reports, not a security use
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
)

// Hashsum is the lower-case hex digest of data with the named algorithm
// ("md5", "sha1", "sha256" or "sha512"; imgproxy info `calc_hashsums`).
// ok is false for any other name.
func Hashsum(algorithm string, data []byte) (sum string, ok bool) {
	var h hash.Hash
	switch algorithm {
	case "md5":
		h = md5.New() //nolint:gosec // G401: checksum, see import
	case "sha1":
		h = sha1.New() //nolint:gosec // G401: checksum, see import
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return "", false
	}
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), true
}
