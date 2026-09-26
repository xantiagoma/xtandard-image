// Package humaspike is the WP-82b spike (ADR 0008 decision 5): /healthz and
// /readyz as huma handlers registered by humago on our own *http.ServeMux,
// with every huma-generated error rewritten into server.ErrorResponse and
// its errs status through per-API hooks only (no huma package variable is
// assigned). It exists to be measured against the hand-written handlers in
// internal/obs/admin; nothing wires it into the binary.
package humaspike

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/server"
)

// textOutput is a plain-text response with the same headers as
// admin.Healthz: huma writes a []byte body as is when the Content-Type
// header field is set, so no content negotiation runs.
type textOutput struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	NoSniff      string `header:"X-Content-Type-Options"`
	Status       int
	Body         []byte
}

func text(status int, body string) *textOutput {
	return &textOutput{
		ContentType:  "text/plain; charset=utf-8",
		CacheControl: "no-store",
		NoSniff:      "nosniff",
		Status:       status,
		Body:         []byte(body + "\n"),
	}
}

// NewAPI returns a huma API over mux with the error hooks installed and no
// operation registered. Its OpenAPI document is not served: the committed
// internal/apidoc document stays the published one.
func NewAPI(mux *http.ServeMux) huma.API {
	api := humago.New(mux, huma.Config{
		OpenAPI: &huma.OpenAPI{OpenAPI: "3.1.0", Info: &huma.Info{Title: "xtandard-image", Version: "dev"}},
		Formats: map[string]huma.Format{
			"application/json": huma.DefaultJSONFormat,
			"json":             huma.DefaultJSONFormat,
		},
		DefaultFormat: "application/json",
		Transformers:  []huma.Transformer{errorEnvelope},
	})
	api.UseMiddleware(statusOverride)
	return api
}

// RegisterHealth registers GET /healthz and GET /readyz on api.
func RegisterHealth(api huma.API, ready *admin.Readiness) {
	huma.Register(api, huma.Operation{
		OperationID: "getHealthz", Method: http.MethodGet, Path: "/healthz", Hidden: true,
	}, func(context.Context, *struct{}) (*textOutput, error) {
		return text(http.StatusOK, "ok"), nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getReadyz", Method: http.MethodGet, Path: "/readyz", Hidden: true,
	}, func(context.Context, *struct{}) (*textOutput, error) {
		s := ready.State()
		status := http.StatusServiceUnavailable
		if s == admin.Ready {
			status = http.StatusOK
		}
		return text(status, s.String()), nil
	})
}

// StatusError adapts an errs error to huma.StatusError, so huma writes it
// with the errs status instead of its generic 500.
type StatusError struct{ Err error }

// Error returns the wrapped error's (internal) text; it is never sent.
func (e StatusError) Error() string { return e.Err.Error() }

// Unwrap returns the errs error.
func (e StatusError) Unwrap() error { return e.Err }

// GetStatus implements huma.StatusError with the errs kind's status.
func (e StatusError) GetStatus() int { return errs.Status(errs.KindOf(e.Err)) }

// statusContext lets errorEnvelope choose the status huma writes: huma
// runs the transformers before it calls SetStatus with the status it
// decided on, and a transformer cannot change that status any other way.
type statusContext struct {
	humaContext // embedded under another name: huma.Context has a Context method
	override    int
}

type humaContext = huma.Context

func (c *statusContext) SetStatus(code int) {
	if c.override != 0 {
		code = c.override
	}
	c.humaContext.SetStatus(code)
}

func statusOverride(ctx huma.Context, next func(huma.Context)) {
	next(&statusContext{humaContext: ctx})
}

// errorEnvelope rewrites huma's own error bodies (*huma.ErrorModel:
// validation, negotiation, unexpected errors) and StatusError into
// server.ErrorResponse with the errs kind's status and public message.
func errorEnvelope(ctx huma.Context, _ string, v any) (any, error) {
	var kind errs.Kind
	switch e := v.(type) {
	case *huma.ErrorModel:
		kind = kindForHumaStatus(e.Status)
	case StatusError:
		kind = errs.KindOf(e.Err)
	default:
		return v, nil
	}
	if sc, ok := ctx.(*statusContext); ok {
		sc.override = errs.Status(kind)
	}
	ctx.SetHeader("Content-Type", "application/json")
	return server.NewErrorResponse(kind, errs.PublicMessage(kind), ""), nil
}

// kindForHumaStatus maps the statuses huma generates to errs kinds: 400
// and 422 (malformed or invalid input) are invalid input, 406 is an
// unacceptable Accept, anything else is internal.
func kindForHumaStatus(status int) errs.Kind {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return errs.KindInvalidURL
	case http.StatusNotAcceptable:
		return errs.KindNotAcceptable
	default:
		return errs.KindInternal
	}
}
