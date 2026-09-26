package server

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// respond is stage 14: the encoded image is written zero-copy in one Write
// with the basic headers (Content-Type, Content-Length, ETag, nosniff).
//
// WP-34b hook: Cache-Control, Last-Modified, Vary (res.decision.Vary |
// res.hints.Vary), Content-DPR (res.hints.ContentDPR), Content-Disposition,
// expires, SVG CSP and 304 handling (before processing) are added here and in
// process from res.object, res.req and res.decision.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, res *imageResult) {
	body := res.out.Body.Bytes()
	h := w.Header()
	h.Set("Content-Type", res.out.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("ETag", res.etag)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // G705: encoded image bytes with an explicit image Content-Type and nosniff
	}
}

// etagLen is the number of base64url characters kept from the digest
// (ARCHITECTURE §9.1).
const etagLen = 22

// etag derives the strong ETag of ARCHITECTURE §9.1 without looking at the
// output bytes:
//
//	"xi1-" + base64url(sha256(canonicalKey ‖ outputFormat ‖ qualityDelta ‖ sourceValidator ‖ buster))[:22]
//
// sourceValidator is the origin ETag, else its Last-Modified, else the
// SHA-256 of the source bytes. qualityDelta (Save-Data) changes the output
// without appearing in the canonical key, so it is mixed in too.
func etag(canonicalKey string, format transform.Format, qualityDelta int, obj *source.Object, src []byte, buster string) string {
	d := sha256.New()
	field := func(b []byte) {
		var n [20]byte
		d.Write(strconv.AppendInt(n[:0], int64(len(b)), 10))
		d.Write([]byte{':'})
		d.Write(b)
	}
	field([]byte(canonicalKey))
	field([]byte(format.String()))
	field(strconv.AppendInt(nil, int64(qualityDelta), 10))
	switch {
	case obj != nil && obj.ETag != "":
		field([]byte("e" + obj.ETag))
	case obj != nil && !obj.LastModified.IsZero():
		field([]byte("m" + obj.LastModified.UTC().Format(http.TimeFormat)))
	default:
		sum := sha256.Sum256(src)
		field(append([]byte("h"), sum[:]...))
	}
	field([]byte(buster))

	var sum [sha256.Size]byte
	enc := base64.RawURLEncoding.EncodeToString(d.Sum(sum[:0]))
	b := make([]byte, 0, 2+4+etagLen)
	b = append(b, `"xi1-`...)
	b = append(b, enc[:etagLen]...)
	b = append(b, '"')
	return string(b)
}
