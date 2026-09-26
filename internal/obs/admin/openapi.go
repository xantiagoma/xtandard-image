package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

// openAPIContentType is the media type of /openapi.json. The registered
// application/vnd.oai.openapi+json makes browsers download the file, so
// plain JSON is served, as huma and most frameworks do.
const openAPIContentType = "application/json"

// staticHandler serves fixed bytes with a strong ETag (the SHA-256 of the
// body) and answers a matching If-None-Match with 304.
type staticHandler struct {
	body        []byte
	etag        string
	contentType string
}

func newStatic(body []byte, contentType string) *staticHandler {
	sum := sha256.Sum256(body)
	return &staticHandler{
		body:        body,
		etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
		contentType: contentType,
	}
}

func (s *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("ETag", s.etag)
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	if etagMatch(r.Header.Get("If-None-Match"), s.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", s.contentType)
	h.Set("Content-Length", strconv.Itoa(len(s.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(s.body)
	}
}

// etagMatch implements the weak comparison of If-None-Match (RFC 9110
// §13.1.2): "*" or any listed tag equal to etag, W/ prefixes ignored.
func etagMatch(header, etag string) bool {
	for tag := range strings.SplitSeq(header, ",") {
		tag = strings.TrimPrefix(strings.TrimSpace(tag), "W/")
		if tag == "*" || tag == etag {
			return true
		}
	}
	return false
}

// Scalar API reference (MIT, https://github.com/scalar/scalar), loaded from
// jsDelivr at a pinned version with Subresource Integrity. Nothing of it is
// in the binary; /docs needs the browser to reach cdn.jsdelivr.net. To
// bump: change the version, then recompute the hash with
//
//	curl -s "$scalarScript" | openssl dgst -sha384 -binary | openssl base64 -A
const (
	scalarScript    = "https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.72.1/dist/browser/standalone.js"
	scalarIntegrity = "sha384-U11tb2XnKvmwt8RlTvnwUnYgrN+ur4Xyh9htLhjajWNR/Oyl5AX5DEz00qRmlrmK"
)

// docsCSP allows only the pinned Scalar script, the spec fetch from this
// origin and Scalar's inline styles. withDefaultFonts:false keeps Scalar
// from loading fonts.scalar.com.
const docsCSP = "default-src 'none'; script-src " + scalarScript + "; style-src 'unsafe-inline'; " +
	"connect-src 'self'; img-src 'self' data: blob:; font-src 'self' data:; worker-src blob:; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// docsPage points Scalar at openapi.json relative to /docs, so it works
// behind a path prefix. The api-reference element is JSON, not script, so
// the CSP needs no inline-script allowance.
const docsPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>xtandard-image API</title>
</head>
<body>
<script id="api-reference" type="application/json" data-url="openapi.json" data-configuration='{"withDefaultFonts":false}'></script>
<script src="` + scalarScript + `" integrity="` + scalarIntegrity + `" crossorigin="anonymous"></script>
</body>
</html>
`

// docsDisabled is the /docs body when XI_ADMIN_DOCS is off.
const docsDisabled = "API docs UI is disabled (XI_ADMIN_DOCS=false). The OpenAPI document is at /openapi.json; " +
	"point any OpenAPI viewer at it, or set XI_ADMIN_DOCS=true to serve Scalar from cdn.jsdelivr.net.\n"

// newDocs returns the /docs handler: the Scalar page when enabled, else a
// 404 naming /openapi.json.
func newDocs(enabled bool) http.Handler {
	if !enabled {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeText(w, http.StatusNotFound, strings.TrimSuffix(docsDisabled, "\n"))
		})
	}
	page := []byte(docsPage)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", docsCSP)
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(page)
		}
	})
}
