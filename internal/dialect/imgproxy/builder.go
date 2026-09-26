package imgproxy

import (
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Builder applies imgproxy processing options to a request outside of
// imgproxy path parsing. Dialects whose options are defined as "the
// equivalent imgproxy option" (the query dialect's native keys and its o=
// passthrough, docs/URL-DIALECTS.md §4) build their request through it so
// the argument rules live in one place.
//
// A Builder is single-use and not safe for concurrent use. The source is
// not handled: callers set Request().Source themselves.
type Builder struct {
	st *state
}

// NewBuilder returns a builder for one request tagged with dialectName.
// It uses p's separator, strictness, security-option policy and clock.
func (p *Parser) NewBuilder(dialectName string) *Builder {
	st := newState(p)
	st.r.Dialect = dialectName
	return &Builder{st: st}
}

// Request returns the request being built.
func (b *Builder) Request() *transform.Request { return b.st.r }

// Pipeline returns the pipeline options currently apply to.
func (b *Builder) Pipeline() *transform.Pipeline { return b.st.pipeline() }

// Apply applies one imgproxy option (full name or alias) with its
// arguments, exactly as the path segment name:args[0]:args[1]... would.
// No arguments means one empty argument.
func (b *Builder) Apply(name string, args ...string) error {
	if len(args) == 0 {
		args = []string{""}
	}
	return b.st.apply(name, args)
}

// ApplyPath applies "/"-separated imgproxy option segments such as
// "rs:fill:300:200/g:sm/-/bl:2" ("-" chains a pipeline). Anything that is
// not an option (a source, an empty segment) is a 400 naming field.
func (b *Builder) ApplyPath(field, options string) error {
	rest, err := b.st.parseOptions(options)
	if err != nil {
		return err
	}
	if rest != "" {
		return dialect.InvalidArgument(field, rest, "imgproxy processing options")
	}
	return nil
}

// NewPipeline starts a chained pipeline; later options apply to it.
func (b *Builder) NewPipeline() error { return b.st.newPipeline() }

// Ignore records an option that is understood but not applied.
func (b *Builder) Ignore(option, reason string) { b.st.ignore(option, reason) }

// Finish applies the cross-option rules imgproxy applies once every option
// is known (background alpha, watermark opacity, expiry).
func (b *Builder) Finish() error { return b.st.finish() }

// ParseBool parses a boolean argument with imgproxy's rules ("" is false;
// otherwise strconv.ParseBool, and anything else is a 400 naming field).
func ParseBool(field, s string) (bool, error) { return parseBool(field, s) }

// ParseFormat parses an output format name with imgproxy's rules
// ("jpg" is an alias; "best" is accepted).
func ParseFormat(field, s string) (transform.Format, error) { return parseFormat(field, s, true) }

// PlausibleSource reports whether s is a non-empty UTF-8 string without
// ASCII control characters, the check applied to every decoded source.
func PlausibleSource(s string) bool { return plausibleSource(s) }
