package errs

import (
	"context"
	"errors"
	"fmt"
)

// Error carries a Kind, an optional internal message, an optional
// client-safe message and the wrapped cause.
//
// Error() is internal text for logs; clients only ever see PublicMessage().
type Error struct {
	Kind   Kind
	Msg    string // internal context, never sent to clients
	Public string // client-safe override; empty means the kind's default
	Err    error  // wrapped cause, may be nil
}

// New returns an error of kind k with internal message msg.
func New(k Kind, msg string) error {
	return &Error{Kind: k, Msg: msg}
}

// Newf is New with fmt formatting. It does not wrap %w operands; use Wrapf.
func Newf(k Kind, format string, args ...any) error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

// Wrap classifies err as kind k. It returns nil when err is nil.
func Wrap(k Kind, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: k, Err: err}
}

// Wrapf classifies err as kind k and adds internal context. It returns nil
// when err is nil.
func Wrapf(k Kind, err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...), Err: err}
}

// WithPublic returns err with a client-safe message that replaces the kind's
// default; the kind stays KindOf(err). It returns nil when err is nil.
// public must never contain internal details (paths, hosts, library text).
func WithPublic(err error, public string) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: KindOf(err), Public: public, Err: err}
}

// Error implements error with the internal message chain.
func (e *Error) Error() string {
	switch {
	case e.Msg != "" && e.Err != nil:
		return e.Msg + ": " + e.Err.Error()
	case e.Msg != "":
		return e.Msg
	case e.Err != nil:
		return e.Err.Error()
	default:
		return e.Kind.String()
	}
}

// Unwrap returns the wrapped cause.
func (e *Error) Unwrap() error { return e.Err }

// HTTPStatus returns Status(e.Kind).
func (e *Error) HTTPStatus() int { return Status(e.Kind) }

// PublicMessage returns the explicit public message or the kind's default.
// It never returns internal text.
func (e *Error) PublicMessage() string {
	if e.Public != "" {
		return e.Public
	}
	return PublicMessage(e.Kind)
}

// Is reports whether target was created by Match with e's kind.
func (e *Error) Is(target error) bool {
	m, ok := target.(kindMatchError)
	return ok && m.kind == e.Kind
}

// kindMatchError is the errors.Is target returned by Match.
type kindMatchError struct{ kind Kind }

func (m kindMatchError) Error() string { return "errs: kind " + m.kind.String() }

// Match returns an errors.Is target that matches any *Error of kind k
// anywhere in the chain, including every branch of errors.Join:
//
//	if errors.Is(err, errs.Match(errs.KindNotFound)) { ... }
func Match(k Kind) error { return kindMatchError{kind: k} }

// KindOf returns the kind of the first *Error in err's chain (depth-first,
// as errors.AsType walks it). Without one, context.Canceled maps to
// KindCanceled, context.DeadlineExceeded to KindTimeout and anything else to
// KindInternal. KindOf(nil) is KindUnknown.
func KindOf(err error) Kind {
	if err == nil {
		return KindUnknown
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Kind
	}
	switch {
	case errors.Is(err, context.Canceled):
		return KindCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	default:
		return KindInternal
	}
}
