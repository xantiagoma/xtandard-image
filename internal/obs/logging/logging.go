// Package logging builds the process logger: log/slog JSON only, with
// request_id, trace_id and span_id taken from the context (go-standards §5).
//
// Callers log with static messages and snake_case keys through LogAttrs:
//
//	logger.LogAttrs(ctx, slog.LevelInfo, "request served",
//		slog.Int("status", 200), slog.String("dialect", "imgproxy"))
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Keys added by Handler and Err. They are snake_case like every other key.
const (
	KeyRequestID = "request_id"
	KeyTraceID   = "trace_id"
	KeySpanID    = "span_id"
	KeyError     = "error"
)

// Options configures New and NewHandler.
type Options struct {
	// Level is the minimum level; nil means slog.LevelInfo. Pass a
	// *slog.LevelVar to change it at runtime.
	Level slog.Leveler
	// AddSource adds the "source" attribute (file:line). Off in production.
	AddSource bool
}

// New returns a JSON logger writing to w with context injection.
func New(w io.Writer, opts Options) *slog.Logger {
	return slog.New(NewHandler(w, opts))
}

// NewHandler returns slog's JSON handler wrapped in Handler.
func NewHandler(w io.Writer, opts Options) *Handler {
	level := opts.Level
	if level == nil {
		level = slog.LevelInfo
	}
	return Wrap(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     level,
		AddSource: opts.AddSource,
	}))
}

// ParseLevel parses debug, info, warn or error (case-insensitive, with
// optional offsets such as "debug+2" as accepted by slog.Level).
func ParseLevel(s string) (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return 0, fmt.Errorf("parse log level %q: %w", s, err)
	}
	return l, nil
}

// Handler is a slog.Handler wrapper that adds request_id, trace_id and
// span_id from the record's context before delegating.
type Handler struct {
	next slog.Handler
}

var _ slog.Handler = (*Handler)(nil)

// Wrap returns next wrapped with context injection. Wrapping a *Handler
// again returns it unchanged, so attributes are never added twice.
func Wrap(next slog.Handler) *Handler {
	if h, ok := next.(*Handler); ok {
		return h
	}
	return &Handler{next: next}
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // hugeParam: signature fixed by slog.Handler
	if ctx != nil {
		if id := RequestID(ctx); id != "" {
			r.AddAttrs(slog.String(KeyRequestID, id))
		}
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			r.AddAttrs(
				slog.String(KeyTraceID, sc.TraceID().String()),
				slog.String(KeySpanID, sc.SpanID().String()),
			)
		}
	}
	return h.next.Handle(ctx, r)
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(attrs)}
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name)}
}

// requestIDKey is the unexported context key type for the request ID.
type requestIDKey struct{}

// WithRequestID returns ctx carrying the request ID id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID stored by WithRequestID, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Err returns the canonical attribute for an error ("error": err.Error()).
// A nil error yields an empty attribute, which slog drops.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.String(KeyError, err.Error())
}
