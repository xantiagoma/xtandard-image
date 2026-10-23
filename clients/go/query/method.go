package query

import (
	"strings"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

// MethodQuery is the HTTP QUERY method (RFC 10008); net/http has no constant for it.
const MethodQuery = "QUERY"

// ContentType is the Content-Type of an HTTP QUERY request's content: the
// query dialect URL's query, form-encoded as written.
const ContentType = "application/x-www-form-urlencoded"

// Request is a query dialect request sent with the HTTP QUERY method
// (RFC 10008, URL-DIALECTS §4.4).
type Request struct {
	// Method is always [MethodQuery].
	Method string
	// URL is the built URL without its query.
	URL string
	// ContentType is always [ContentType].
	ContentType string
	// Body is the URL's query exactly as built, xi_sig included: the server
	// signs, limits and caches it as the GET URL, since xi_sig does not cover
	// the method.
	Body string
}

// MethodRequest splits a built query dialect URL (/q/…?url=…) into an HTTP
// QUERY request: the parameters move from the URL to the content, so long
// option chains, watermark text or encrypted sources stay out of URLs. The
// server must have XI_SERVER_QUERY_METHOD on (the default); many CDNs and
// proxies neither cache nor forward QUERY.
//
// A URL without a query, with an empty query, or with a fragment is an
// [xtandard.ErrInvalidRequest] error.
func MethodRequest(url string) (Request, error) {
	path, query, ok := strings.Cut(url, "?")
	if !ok || query == "" || strings.Contains(url, "#") {
		return Request{}, xtandard.Errorf(xtandard.ErrInvalidRequest, "a QUERY request needs a query dialect URL with a query and no fragment")
	}
	return Request{Method: MethodQuery, URL: path, ContentType: ContentType, Body: query}, nil
}
