package xtandard

import "fmt"

// Arg is one option argument as written in a URL: a string (written verbatim
// and never escaped), a bool (1 or 0), an integer or a float (written like
// ECMAScript's Number::toString: 300, 1.5, 0.1, 1e+21), a named type whose
// underlying type is one of those (the generated enums), or nil (an empty
// argument: rs:fill:300::1).
type Arg = any

// Option is one option occurrence: a name (canonical or alias) and its
// arguments. It is the untyped form every option takes in the end, and it
// implements [ProcessingOption], [InfoOption] and [VideoOption]; the builders
// check the name against the catalogue they write (an unknown name is an
// [ErrUnknownOption] error). The typed structs of the processing, info and
// video packages produce Options.
type Option struct {
	Name string
	Args []Arg
}

// Opt returns an Option: Opt("rs", "fill", 300, 400), Opt("q", 80).
func Opt(name string, args ...Arg) Option { return Option{Name: name, Args: args} }

// ProcessingOption implements [ProcessingOption].
func (o Option) ProcessingOption() Option { return o }

// InfoOption implements [InfoOption].
func (o Option) InfoOption() Option { return o }

// VideoOption implements [VideoOption].
func (o Option) VideoOption() Option { return o }

// ProcessingOption is an option of the processing catalogue (image URLs and
// the query dialect's o=): an [Option] or a struct of package processing.
type ProcessingOption interface{ ProcessingOption() Option }

// InfoOption is an option of the /info catalogue: an [Option] or a struct of
// package info.
type InfoOption interface{ InfoOption() Option }

// VideoOption is an option of the /video catalogue (/video, streams and
// /q/video): an [Option] or a struct of package video.
type VideoOption interface{ VideoOption() Option }

// OptionNames says how option names are written.
type OptionNames string

// The option name styles.
const (
	// ShortNames writes each option's short name (rs, q, g), the default ("" means ShortNames).
	ShortNames OptionNames = "short"
	// CanonicalNames writes each option's canonical name (resize, quality, gravity).
	CanonicalNames OptionNames = "canonical"
	// GivenNames writes the name (or alias) as the caller gave it.
	GivenNames OptionNames = "given"
)

// Catalogue is one of the server's option catalogues.
type Catalogue int

// The catalogues.
const (
	// CatalogueProcessing: image processing options (imgproxy URLs, /q/ o=).
	CatalogueProcessing Catalogue = iota + 1
	// CatalogueInfo: /info options.
	CatalogueInfo
	// CatalogueVideo: /video options (also streams and /q/video).
	CatalogueVideo
)

// String returns the catalogue's name as error messages use it.
func (c Catalogue) String() string {
	switch c {
	case CatalogueProcessing:
		return "processing"
	case CatalogueInfo:
		return "info"
	case CatalogueVideo:
		return "video"
	}
	return fmt.Sprintf("Catalogue(%d)", int(c))
}

// Canonical returns the canonical name of an option name or alias, and false
// for a name that is not in the catalogue.
func (c Catalogue) Canonical(name string) (string, bool) { return canonicalName(c, name) }

// Short returns the name builders write by default for a canonical option
// name (the shortest alias).
func (c Catalogue) Short(canonical string) string { return shortName(c, canonical) }

// Names returns every option name and alias of the catalogue, sorted.
func (c Catalogue) Names() []string { return catalogueNames(c) }

// Written returns the name an option is written with in a style, or an
// [ErrUnknownOption] error for a name that is not in the catalogue.
func (c Catalogue) Written(name string, style OptionNames) (string, error) {
	canonical, ok := c.Canonical(name)
	if !ok {
		return "", Errorf(ErrUnknownOption, "unknown %s option %q", c, name)
	}
	switch style {
	case GivenNames:
		return name, nil
	case CanonicalNames:
		return canonical, nil
	default:
		return c.Short(canonical), nil
	}
}

// NativeVideoKey returns the /q/video key of a written /video option name:
// crop is c and flip is fl, every other name is itself.
func NativeVideoKey(name string) string { return nativeVideoKey(name) }
