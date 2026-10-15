package client

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

// HTTPError is a non-2xx response. Kind, Message and RequestID come from the
// server's JSON error envelope. A plain-text body (the server writes one when
// Accept does not list JSON) gets the kind whose status and message match
// ([xtandard.ErrorKindFor]); any other body (a proxy's error page, say) gives
// kind unknown and the body text. It unwraps to its
// kind, so errors.Is(err, xtandard.ErrorKindNotFound) matches a missing
// source:
//
//	if he, ok := errors.AsType[*client.HTTPError](err); ok && he.Status == http.StatusForbidden { … }
type HTTPError struct {
	// Method and URL are the request's.
	Method, URL string
	// Status is the HTTP status.
	Status int
	// Kind is the error kind (invalid_url, unauthorized, not_found, …) or unknown.
	Kind xtandard.ErrorKind
	// Message is the server's client-safe message, or the body text.
	Message string
	// RequestID is the envelope's request ID, also in the server's logs.
	RequestID string
	// Body is the parsed envelope, or nil when the body was not one.
	Body *xtandard.ErrorResponse
}

// Error implements error.
func (e *HTTPError) Error() string {
	var b strings.Builder
	b.WriteString("xtandard: ")
	b.WriteString(e.Method + " " + e.URL + ": " + strconv.Itoa(e.Status) + " " + string(e.Kind))
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" (request " + e.RequestID + ")")
	}
	return b.String()
}

// Unwrap returns the error kind, so errors.Is matches the xtandard.ErrorKind constants.
func (e *HTTPError) Unwrap() error { return e.Kind }

func newHTTPError(res *http.Response, body []byte) *HTTPError {
	e := &HTTPError{Status: res.StatusCode, Kind: xtandard.ErrorKindUnknown}
	if res.Request != nil {
		e.Method, e.URL = res.Request.Method, res.Request.URL.String()
	}
	var env xtandard.ErrorResponse
	if json.Unmarshal(body, &env) == nil && env.Error != nil && env.Error.Kind != "" {
		e.Body = &env
		e.Kind = env.Error.Kind
		e.Message = env.Error.Message
		if env.Error.RequestID != nil {
			e.RequestID = *env.Error.RequestID
		}
		return e
	}
	e.Message = strings.TrimSpace(string(body))
	if k, ok := xtandard.ErrorKindFor(e.Status, e.Message); ok {
		e.Kind = k
	}
	if e.Message == "" {
		e.Message = http.StatusText(res.StatusCode)
	}
	return e
}

// TransportError is a request that got no (complete) response: a
// connection, TLS or timeout failure, a cancelled context, or an unreadable
// body. It unwraps to the cause, so errors.Is(err, context.DeadlineExceeded)
// works.
type TransportError struct {
	Method, URL string
	Err         error
}

// Error implements error.
func (e *TransportError) Error() string {
	return "xtandard: " + e.Method + " " + e.URL + ": " + e.Err.Error()
}

// Unwrap returns the cause.
func (e *TransportError) Unwrap() error { return e.Err }
