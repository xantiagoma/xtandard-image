package xtandard

import "fmt"

// ErrorCode says why a URL, token or header could not be built. The codes are
// the language-neutral codes of clients/spec/conformance.json. An ErrorCode is
// an error, so the constants below are sentinels for errors.Is:
//
//	if errors.Is(err, xtandard.ErrInvalidSource) { … }
type ErrorCode string //nolint:errname // codes are constant sentinels named after the conformance codes

// Error implements error: the code itself ("invalid_source").
func (c ErrorCode) Error() string { return string(c) }

// The error codes. Each maps to one class of fault (clients/README.md).
const (
	// ErrUnknownOption: an option name that is not in the catalogue.
	ErrUnknownOption ErrorCode = "unknown_option"
	// ErrInvalidArgument: an option without arguments, a forbidden character,
	// a non-finite number, an unsupported argument type, or ":" in a /q/video argument.
	ErrInvalidArgument ErrorCode = "invalid_argument"
	// ErrInvalidSource: an empty source, or a source with control characters.
	ErrInvalidSource ErrorCode = "invalid_source"
	// ErrInvalidExtension: an extension that does not match [ExtensionPattern].
	ErrInvalidExtension ErrorCode = "invalid_extension"
	// ErrInvalidSeparator: a separator that is not one character, or is forbidden.
	ErrInvalidSeparator ErrorCode = "invalid_separator"
	// ErrMissingSigning: a PDF password without a signing key.
	ErrMissingSigning ErrorCode = "missing_signing"
	// ErrMissingEncryption: an encrypted source or a PDF password without an encryption key.
	ErrMissingEncryption ErrorCode = "missing_encryption"
	// ErrInvalidKey: key material that is not hex, a key of the wrong size, an
	// IV that is not 16 bytes, or a signature size outside 1 to 32.
	ErrInvalidKey ErrorCode = "invalid_key"
	// ErrInvalidPassword: an empty password, a password with control
	// characters, or a password over 1024 UTF-8 bytes.
	ErrInvalidPassword ErrorCode = "invalid_password"
	// ErrInvalidRequest: chained /info or /video pipelines, a stream option
	// given as an option, an unknown protocol or source encoding, ts on DASH,
	// a URL outside the mount, or two xi_sig parameters.
	ErrInvalidRequest ErrorCode = "invalid_request"
	// ErrInvalidName: a bad xi:// source name or source authority.
	ErrInvalidName ErrorCode = "invalid_name"
	// ErrInvalidEndpoint: an imaginary endpoint that is not lower-case letters, or is video.
	ErrInvalidEndpoint ErrorCode = "invalid_endpoint"
	// ErrInvalidRange: a negative range value, or an end before the start.
	ErrInvalidRange ErrorCode = "invalid_range"
)

// BuildError is the error of everything that builds a URL, token, key or
// header. It unwraps to its [ErrorCode]:
//
//	var be *xtandard.BuildError
//	if errors.As(err, &be) { log.Print(be.Code, be.Message) }
//	errors.Is(err, xtandard.ErrUnknownOption)
type BuildError struct {
	// Code is the language-neutral error code.
	Code ErrorCode
	// Message says what was wrong, for logs; it may quote the input.
	Message string
}

// Error implements error.
func (e *BuildError) Error() string { return "xtandard: " + e.Message + " (" + string(e.Code) + ")" }

// Unwrap returns the error code, so errors.Is matches the [ErrorCode] sentinels.
func (e *BuildError) Unwrap() error { return e.Code }

// Errorf returns a *BuildError with a formatted message. It is exported for
// the module's builder packages; callers rarely need it.
func Errorf(code ErrorCode, format string, args ...any) error {
	return &BuildError{Code: code, Message: fmt.Sprintf(format, args...)}
}
