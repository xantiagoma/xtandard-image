package playground

import (
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/obs/logging"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// maxSignBody bounds a sign request body.
const maxSignBody = 32 << 10

// maxURLPart bounds SignRequest.Path and SignRequest.Query.
const maxURLPart = 8 << 10

// maxPDFPassword bounds SignRequest.PDFPassword in bytes
// (transform.MaxPasswordLen).
const maxPDFPassword = 1024

// badRequestError carries a client-safe message.
type badRequestError struct{ msg string }

func (e *badRequestError) Error() string { return "playground: " + e.msg }

func badRequest(msg string) error { return &badRequestError{msg: msg} }

// ClientError is an error the API answers with 400 and msg, for
// Translator implementations: a request the translator cannot handle is
// the client's, not a server failure.
func ClientError(msg string) error { return badRequest(msg) }

// IsClientError reports whether err wraps a ClientError.
func IsClientError(err error) bool {
	_, ok := errors.AsType[*badRequestError](err)
	return ok
}

// sign answers POST api/sign: the URL for a mount, signed with the server's
// key for that mount when it holds one.
func (h *handler) sign(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	var req SignRequest
	var err error
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSignBody))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	m, ok := h.mounts[req.Mount]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown mount")
		return
	}
	m, ok = m.at(req.Prefix)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown prefix for this mount")
		return
	}
	path, query := req.Path, req.Query
	if req.PDFPassword != "" {
		if path, query, err = h.withPDFPassword(m, req.PDFPassword, path, query); err != nil {
			writeJSONError(w, http.StatusBadRequest, badRequestMessage(err))
			return
		}
	}
	res, err := SignURL(m, h.signers[m.ID], path, query)
	if err != nil {
		if e, ok := errors.AsType[*badRequestError](err); ok {
			writeJSONError(w, http.StatusBadRequest, e.msg)
			return
		}
		h.logger.LogAttrs(r.Context(), slog.LevelError, "playground sign failed", logging.Err(err))
		writeJSONError(w, http.StatusInternalServerError, "signing failed")
		return
	}
	res.URL = h.pathPrefix + res.URL
	writeJSON(w, http.StatusOK, res)
}

// withPDFPassword adds the encrypted password to the URL parts of mount m
// (WP-115): a pdp segment first in an imgproxy path, a pdf_password
// parameter on the query mount. The server accepts it on signed URLs only,
// so a mount the playground cannot sign is refused, as are other dialects.
// The plaintext is copied once and that copy wiped; it never reaches a log
// or the response.
func (h *handler) withPDFPassword(m *Mount, password, path, query string) (string, string, error) {
	if h.encrypt == nil {
		return "", "", badRequest("pdf_password needs XI_ENCRYPTION_KEYS")
	}
	if h.signers[m.ID] == nil {
		return "", "", badRequest("pdf_password needs a signed url: the server holds no signing key for this mount")
	}
	if !m.passwordCapable() {
		return "", "", badRequest("pdf_password is an imgproxy and query dialect option")
	}
	if len(password) > maxPDFPassword {
		return "", "", badRequest("pdf_password is longer than 1024 bytes")
	}
	pw := []byte(password)
	token, err := h.encrypt(pw)
	clear(pw)
	if err != nil {
		return "", "", badRequest("pdf_password must be text without control characters")
	}
	if m.Scheme == SchemeImgproxy {
		sep := m.ArgumentsSeparator
		if sep == "" {
			sep = ":"
		}
		return "/pdp" + sep + token + path, query, nil
	}
	query = strings.TrimPrefix(query, "?")
	if query != "" {
		query += "&"
	}
	return path, query + "pdf_password=" + token, nil
}

// passwordCapable reports whether m's dialect takes pdf_password: the
// imgproxy dialect (and its /info) and the query dialect.
func (m *Mount) passwordCapable() bool {
	return m.Scheme == SchemeImgproxy || (m.Scheme == SchemeXI && m.Grammar == GrammarQuery)
}

// badRequestMessage is the client-safe message of a badRequest error.
func badRequestMessage(err error) string {
	if e, ok := errors.AsType[*badRequestError](err); ok {
		return e.msg
	}
	return "invalid request"
}

// at is m answering on prefix p, one of its Prefixes; "" is m itself.
func (m *Mount) at(p string) (*Mount, bool) {
	if p == "" || p == m.Prefix {
		return m, true
	}
	if !slices.Contains(m.Prefixes, p) {
		return nil, false
	}
	c := *m
	c.Prefix = p
	return &c, true
}

// SignURL builds the URL of path and query (after the mount prefix) on
// mount m, signed with s according to m.Scheme. A nil s returns the
// unsigned URL: the imgproxy and thumbor schemes then use their
// placeholder signature segment ("insecure", "unsafe"). The URL starts
// with the mount prefix; the caller prepends the server path prefix.
func SignURL(m *Mount, s *sign.Signer, path, query string) (SignResponse, error) {
	query = strings.TrimPrefix(query, "?")
	if err := checkPart("path", path); err != nil {
		return SignResponse{}, err
	}
	if err := checkPart("query", query); err != nil {
		return SignResponse{}, err
	}
	if path == "" || path[0] != '/' {
		return SignResponse{}, badRequest("path must start with /")
	}
	prefix := strings.TrimSuffix(m.Prefix, "/")
	withQuery := func(p, q string) string {
		if q == "" {
			return p
		}
		return p + "?" + q
	}
	res := SignResponse{Scheme: m.Scheme}
	switch m.Scheme {
	case SchemeImgproxy:
		if s == nil {
			res.URL = withQuery(prefix+"/insecure"+path, query)
			return res, nil
		}
		p, err := s.SignImgproxyPath(path)
		if err != nil {
			return SignResponse{}, err
		}
		res.URL, res.Signed = withQuery(prefix+p, query), true
	case SchemeThumbor:
		rest := strings.TrimPrefix(path, "/")
		if s == nil {
			res.URL = withQuery(prefix+"/unsafe/"+rest, query)
			return res, nil
		}
		p, err := s.SignThumborPath(rest)
		if err != nil {
			return SignResponse{}, err
		}
		res.URL, res.Signed = withQuery(prefix+p, query), true
	case SchemeImgix:
		if s == nil {
			res.URL = withQuery(prefix+path, query)
			return res, nil
		}
		q, err := s.SignImgixQuery(path, query)
		if err != nil {
			return SignResponse{}, err
		}
		res.URL, res.Signed = withQuery(prefix+path, q), true
	case SchemeXI:
		if s == nil {
			res.URL = withQuery(prefix+path, query)
			return res, nil
		}
		// wsrv signs "/" plus the query whatever its mount path is; every
		// other xi_sig dialect signs the path after its mount prefix.
		msgPath := path
		if m.Dialect == "wsrv" {
			msgPath = "/"
		}
		q, err := s.SignXIQuery(msgPath, query)
		if err != nil {
			return SignResponse{}, err
		}
		res.URL, res.Signed = withQuery(prefix+path, q), true
	default:
		res.Scheme = SchemeNone
		res.URL = withQuery(prefix+path, query)
	}
	return res, nil
}

// checkPart rejects oversize parts and control characters, which no URL
// may carry unescaped.
func checkPart(name, s string) error {
	if len(s) > maxURLPart {
		return badRequest(name + " too long")
	}
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c == 0x7f || c == ' ' || c == '#' {
			return badRequest(name + " must be escaped (no spaces, control characters or #)")
		}
	}
	return nil
}
