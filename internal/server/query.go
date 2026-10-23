package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// HTTP QUERY (RFC 10008; WP-147, ADR 0014).
//
// A QUERY request to an endpoint of the query dialect (/q/, /q/video,
// /q/<imaginary endpoint>, on every mount of that dialect) carries in its
// content the parameters a GET request carries in its URL query, as
// application/x-www-form-urlencoded or application/json
// (query.ContentQuery). The ingress turns it into the equivalent GET
// request (same path, the content's canonical query, no body) and serves
// that: one parser, one xi_sig message, one unsigned policy, one set of
// limits and one result cache key for both methods. QUERY is safe and
// idempotent like GET; the response is the GET response plus:
//
//   - Accept-Query (the media types read here), also on GET, HEAD and 405
//     responses of those endpoints;
//   - Vary: Content-Type;
//   - Location, on 2xx, with the equivalent GET URL (path + "?" +
//     canonical query) when it fits in XI_SERVER_QUERY_LOCATION_MAX_LENGTH
//     and carries no pdf_password. It echoes the client's own parameters,
//     signature included: xi_sig does not cover the method, so the GET
//     URL is exactly as authorised as the QUERY was. Nothing is signed
//     on the server's behalf. No Content-Location: the representation
//     depends on Accept and client hints, and no result resource is kept.
//
// Failures: QUERY elsewhere, or with XI_SERVER_QUERY_METHOD off, is 405
// as before; a missing or unsupported Content-Type, a charset other than
// UTF-8 or a Content-Encoding is 415 with Accept-Query; content over
// XI_SERVER_QUERY_MAX_BODY_BYTES is 413; malformed content, or a URL that
// has a query of its own (the content is the whole query), is 400.

// MethodQuery is the HTTP QUERY method (RFC 10008); net/http has no
// constant for it.
const MethodQuery = "QUERY"

// Allow field values.
const (
	allowGetHead      = "GET, HEAD"
	allowGetHeadQuery = "GET, HEAD, QUERY"
)

// QueryMethodConfig configures the QUERY method (XI_SERVER_QUERY_*).
type QueryMethodConfig struct {
	// Enabled accepts QUERY on the query dialect's endpoints
	// (XI_SERVER_QUERY_METHOD); off, QUERY is 405 everywhere.
	Enabled bool
	// MaxBodyBytes bounds the content (XI_SERVER_QUERY_MAX_BODY_BYTES).
	MaxBodyBytes int64
	// LocationMaxLength is the longest equivalent GET URL sent as
	// Location (XI_SERVER_QUERY_LOCATION_MAX_LENGTH); 0 sends none.
	LocationMaxLength int
}

// DefaultQueryMethodConfig is the documented default (docs/CONFIG.md).
func DefaultQueryMethodConfig() QueryMethodConfig {
	return QueryMethodConfig{Enabled: true, MaxBodyBytes: 64 << 10, LocationMaxLength: 2048}
}

// queryEndpoint reports whether QUERY is enabled and r targets an
// endpoint of the query dialect. hasURL stands for a query with a "url"
// key (the root ?url= rule): QUERY content always has one.
func (s *Server) queryEndpoint(r *http.Request, hasURL bool) bool {
	if !s.cfg.Query.Enabled {
		return false
	}
	rr, ok := s.stripPathPrefix(r)
	if !ok {
		return false
	}
	m := s.deps.Router.MountFor(r.Host, rr.URL.EscapedPath(), hasURL)
	return m != nil && m.Dialect == query.Name
}

// serveQuery answers a QUERY request (XI_SERVER_QUERY_METHOD on): the
// equivalent GET request is dispatched like any other.
func (s *Server) serveQuery(sw *statusWriter, r *http.Request, dispatch func(*http.Request)) {
	if !s.queryEndpoint(r, true) {
		sw.Header().Set("Allow", allowGetHead)
		s.writeError(sw, r, &httpError{status: http.StatusMethodNotAllowed, msg: "method not allowed"})
		return
	}
	sw.Header().Set("Accept-Query", query.AcceptQuery)
	sw.varyContentType = true
	get, err := s.queryRequest(r)
	if err != nil {
		s.writeError(sw, r, err)
		return
	}
	if loc := s.queryLocation(r, get.URL.RawQuery); loc != "" {
		sw.location = loc
	}
	dispatch(get)
}

// queryRequest reads r's content and returns the equivalent GET request.
func (s *Server) queryRequest(r *http.Request) (*http.Request, error) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return nil, &errs.Error{
			Kind: errs.KindInvalidURL, Msg: "server: query request with a url query",
			Public: "invalid url: a QUERY request carries its parameters in the content, not the url",
		}
	}
	if ce := r.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return nil, unsupportedMedia("content encoding not supported")
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return nil, unsupportedMedia("missing content type")
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || (mt != query.MediaTypeForm && mt != query.MediaTypeJSON) {
		return nil, unsupportedMedia("unsupported content type")
	}
	if cs, ok := params["charset"]; ok && !strings.EqualFold(cs, "utf-8") {
		return nil, unsupportedMedia("unsupported charset")
	}
	limit := s.cfg.Query.MaxBodyBytes
	if r.ContentLength > limit {
		return nil, contentTooLarge()
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, &errs.Error{Kind: errs.KindInvalidURL, Msg: "server: read query content", Public: "invalid query content: unreadable", Err: err}
	}
	if int64(len(content)) > limit {
		return nil, contentTooLarge()
	}
	raw, err := query.ContentQuery(mt, content)
	if err != nil {
		if errors.Is(err, query.ErrUnsupportedMediaType) { // unreachable: checked above
			return nil, unsupportedMedia("unsupported content type")
		}
		return nil, err
	}

	get := new(http.Request)
	*get = *r
	get.Method = http.MethodGet
	get.URL = new(url.URL)
	*get.URL = *r.URL
	get.URL.RawQuery = raw
	get.RequestURI = r.URL.EscapedPath() + "?" + raw
	get.Header = r.Header.Clone()
	for _, h := range [...]string{"Content-Type", "Content-Length", "Content-Encoding"} {
		get.Header.Del(h)
	}
	get.Body, get.ContentLength, get.GetBody = http.NoBody, 0, nil
	return get, nil
}

// queryLocation is the Location of a QUERY response: the equivalent GET
// URL, "" when it is too long, Location is off, or the query carries an
// encrypted document password (kept out of URLs, like the access log keeps
// it out of logs).
func (s *Server) queryLocation(r *http.Request, rawQuery string) string {
	maxLen := s.cfg.Query.LocationMaxLength
	if maxLen <= 0 {
		return ""
	}
	for part := range strings.SplitSeq(rawQuery, "&") {
		k, _, _ := strings.Cut(part, "=")
		if k, err := url.QueryUnescape(k); err != nil || k == "pdf_password" || k == "pdp" {
			return ""
		}
	}
	loc := r.URL.EscapedPath() + "?" + rawQuery
	if len(loc) > maxLen {
		return ""
	}
	return loc
}

func unsupportedMedia(msg string) error {
	return &httpError{status: http.StatusUnsupportedMediaType, msg: msg}
}

func contentTooLarge() error {
	return &httpError{status: http.StatusRequestEntityTooLarge, msg: "query content too large"}
}
