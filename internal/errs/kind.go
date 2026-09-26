// Package errs defines the error kinds of xtandard-image and the single
// mapping from kind to HTTP status and client-safe message
// (docs/ARCHITECTURE.md §12, docs/go-standards.md §6).
//
// errs is a leaf package: it imports nothing internal.
package errs

import (
	"net/http"
	"strconv"
)

// StatusClientClosedRequest is the non-standard status (nginx 499) recorded
// when the client disconnects. It is logged, never written.
const StatusClientClosedRequest = 499

// Kind classifies an error for status mapping, logging and metrics.
// The zero value is KindUnknown, which maps like KindInternal.
type Kind uint8

// Kinds, one per row of docs/ARCHITECTURE.md §12.
const (
	KindUnknown         Kind = iota // zero value; treated as internal
	KindInvalidURL                  // cannot parse path, bad base64, unknown option, bad arg type
	KindUnauthorized                // missing/bad signature, bad enc/ payload
	KindForbidden                   // source not allowlisted, SSRF block, option not allowed
	KindExpired                     // signed URL expiry in the past
	KindNotFound                    // source 404 / NoSuchKey / ENOENT
	KindSourceTooLarge              // source bytes over the limit
	KindLimitExceeded               // pixels, frames, result dimension, cost over budget
	KindUnsupported                 // undetectable or disabled format
	KindCorrupt                     // decoder failure
	KindHashMismatch                // hashsum check failed
	KindNotAcceptable               // strict Accept with no acceptable format
	KindUpstreamError               // origin 5xx, connection refused, TLS failure
	KindUpstreamTimeout             // download timeout
	KindOverloaded                  // queue full
	KindUnavailable                 // worker wait timeout, draining
	KindTimeout                     // processing exceeded our request deadline
	KindCanceled                    // client disconnected
	KindInternal                    // bug, panic, unattributable vips error
)

type kindInfo struct {
	name   string
	status int
	public string
}

// info is the single Kind → (name, status, public message) mapping. It is a
// switch rather than a package-level table so there is no mutable global.
// ok is false for out-of-range kinds, which get KindUnknown's row.
func (k Kind) info() (ki kindInfo, ok bool) {
	switch k {
	case KindUnknown:
		return kindInfo{"unknown", http.StatusInternalServerError, "internal server error"}, true
	case KindInvalidURL:
		return kindInfo{"invalid_url", http.StatusBadRequest, "invalid url"}, true
	case KindUnauthorized:
		return kindInfo{"unauthorized", http.StatusForbidden, "invalid signature"}, true
	case KindForbidden:
		return kindInfo{"forbidden", http.StatusForbidden, "forbidden"}, true
	case KindExpired:
		return kindInfo{"expired", http.StatusNotFound, "url expired"}, true
	case KindNotFound:
		return kindInfo{"not_found", http.StatusNotFound, "source image not found"}, true
	case KindSourceTooLarge:
		return kindInfo{"source_too_large", http.StatusUnprocessableEntity, "source image too large"}, true
	case KindLimitExceeded:
		return kindInfo{"limit_exceeded", http.StatusUnprocessableEntity, "image exceeds processing limits"}, true
	case KindUnsupported:
		return kindInfo{"unsupported", http.StatusUnprocessableEntity, "unsupported image format"}, true
	case KindCorrupt:
		return kindInfo{"corrupt", http.StatusUnprocessableEntity, "image cannot be decoded"}, true
	case KindHashMismatch:
		return kindInfo{"hash_mismatch", http.StatusUnprocessableEntity, "source hash mismatch"}, true
	case KindNotAcceptable:
		return kindInfo{"not_acceptable", http.StatusNotAcceptable, "no acceptable image format"}, true
	case KindUpstreamError:
		return kindInfo{"upstream_error", http.StatusBadGateway, "upstream error"}, true
	case KindUpstreamTimeout:
		return kindInfo{"upstream_timeout", http.StatusGatewayTimeout, "upstream timeout"}, true
	case KindOverloaded:
		return kindInfo{"overloaded", http.StatusTooManyRequests, "too many requests"}, true
	case KindUnavailable:
		return kindInfo{"unavailable", http.StatusServiceUnavailable, "service unavailable"}, true
	case KindTimeout:
		return kindInfo{"timeout", http.StatusGatewayTimeout, "processing timeout"}, true
	case KindCanceled:
		return kindInfo{"canceled", StatusClientClosedRequest, "request canceled"}, true
	case KindInternal:
		return kindInfo{"internal", http.StatusInternalServerError, "internal server error"}, true
	default:
		ki, _ = KindUnknown.info()
		return ki, false
	}
}

// String returns the snake_case name used in logs, metric labels and JSON
// error bodies. Out-of-range kinds render as "Kind(n)".
func (k Kind) String() string {
	ki, ok := k.info()
	if !ok {
		return "Kind(" + strconv.Itoa(int(k)) + ")"
	}
	return ki.name
}

// Status returns the HTTP status for k (ARCHITECTURE §12). Unknown and
// out-of-range kinds map to 500.
func Status(k Kind) int {
	ki, _ := k.info()
	return ki.status
}

// PublicMessage returns the client-safe default message for k.
func PublicMessage(k Kind) string {
	ki, _ := k.info()
	return ki.public
}
