package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// resultKeyVersion prefixes every result key and is hashed first. Bump it
// for any change of the encoding below; TestResultKeyGolden pins it.
const resultKeyVersion = "v1"

// ResultKey is everything that selects one cached response
// (docs/ARCHITECTURE.md §9.2). Every field is computable before the
// source is fetched, so a hit skips fetch, admission, probe and
// processing.
type ResultKey struct {
	// Canonical is transform.Request.CanonicalKey after client hints and
	// defaults were applied (it covers the source spec, every option and
	// the cache buster).
	Canonical string
	// Source is the resolved source identity: named source, bucket, path
	// and version joined by NUL (the fetch coalescing key), so a
	// reconfigured source never serves another origin's results.
	Source string
	// Format selects the output format: "f:" + the format name when the
	// URL fixes it, else "a:" + negotiate.AcceptList.Key() (the weights
	// the negotiation reads), because the negotiated format is a function
	// of the request, the Accept header and the source.
	Format string
	// QualityDelta is the Save-Data quality delta (not in the canonical
	// key).
	QualityDelta int
	// Hints is the client-hint part of the response headers that is not in
	// the canonical key: the hint Vary set and Content-DPR.
	Hints string
	// Buster is XI_CACHE_ETAG_BUSTER: changing it invalidates every
	// result, like it changes every ETag.
	Buster string
	// Build is the application version: a new release may encode
	// differently, so persistent tiers never serve an older build's bytes.
	Build string
}

// String returns the store key, without namespace:
//
//	"v1/" + hex(sha256(F("v1") F(Canonical) F(Source) F(Format)
//	                   F(QualityDelta) F(Hints) F(Buster) F(Build)))
//
// where F(x) = decimal byte length of x ‖ ":" ‖ x (the ETag encoding of
// ARCHITECTURE §9.1) and QualityDelta is decimal. Stored under NSResult,
// the full key is "res/v1/<64 hex digits>".
func (k *ResultKey) String() string {
	d := sha256.New()
	var n [20]byte
	field := func(s string) {
		d.Write(strconv.AppendInt(n[:0], int64(len(s)), 10))
		d.Write([]byte{':'})
		d.Write([]byte(s))
	}
	field(resultKeyVersion)
	field(k.Canonical)
	field(k.Source)
	field(k.Format)
	field(string(strconv.AppendInt(n[:0], int64(k.QualityDelta), 10)))
	field(k.Hints)
	field(k.Buster)
	field(k.Build)
	var sum [sha256.Size]byte
	b := make([]byte, 0, len(resultKeyVersion)+1+2*sha256.Size)
	b = append(b, resultKeyVersion...)
	b = append(b, '/')
	b = hex.AppendEncode(b, d.Sum(sum[:0]))
	return string(b)
}
