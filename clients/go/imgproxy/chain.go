package imgproxy

import (
	"encoding/base64"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

func base64URL(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// ImageChain is a fluent processing URL: one method per option name and alias
// (generated), [ImageChain.Pipeline] for "/-/", then [ImageChain.Build]. The
// methods append and return the chain; errors surface in Build.
type ImageChain struct {
	b         *Builder
	source    string
	pipelines [][]xtandard.Option
	encoding  xtandard.SourceEncoding
	ext       string
	password  *string
}

// Image starts a fluent processing URL for a source.
func (b *Builder) Image(source string) *ImageChain {
	return &ImageChain{b: b, source: source, pipelines: [][]xtandard.Option{nil}}
}

// Option appends one option occurrence by name or alias (as the per-option methods do).
func (c *ImageChain) Option(name string, args ...xtandard.Arg) *ImageChain {
	c.pipelines[len(c.pipelines)-1] = append(c.pipelines[len(c.pipelines)-1], xtandard.Opt(name, args...))
	return c
}

// With appends options: typed structs of package processing or [xtandard.Option] values.
func (c *ImageChain) With(opts ...xtandard.ProcessingOption) *ImageChain {
	for _, o := range opts {
		if o != nil {
			c.pipelines[len(c.pipelines)-1] = append(c.pipelines[len(c.pipelines)-1], o.ProcessingOption())
		}
	}
	return c
}

// Pipeline starts the next chained pipeline ("/-/").
func (c *ImageChain) Pipeline() *ImageChain {
	c.pipelines = append(c.pipelines, nil)
	return c
}

// Extension sets the output format extension.
func (c *ImageChain) Extension(ext string) *ImageChain {
	c.ext = ext
	return c
}

// Encoding overrides the source encoding of this URL.
func (c *ImageChain) Encoding(enc xtandard.SourceEncoding) *ImageChain {
	c.encoding = enc
	return c
}

// WithPDFPassword opens a password-protected PDF: the plain-text password is
// encrypted into the pdp option (it needs both keys).
func (c *ImageChain) WithPDFPassword(password string) *ImageChain {
	c.password = &password
	return c
}

// Build builds the URL.
func (c *ImageChain) Build() (string, error) {
	return c.b.build(process, c.pipelines, c.source, c.encoding, c.ext, c.password)
}

// InfoChain is a fluent /info URL: one method per /info option name and alias.
type InfoChain struct {
	b        *Builder
	source   string
	opts     []xtandard.Option
	encoding xtandard.SourceEncoding
	password *string
}

// Info starts a fluent /info URL for a source.
func (b *Builder) Info(source string) *InfoChain {
	return &InfoChain{b: b, source: source}
}

// Option appends one option occurrence by name or alias.
func (c *InfoChain) Option(name string, args ...xtandard.Arg) *InfoChain {
	c.opts = append(c.opts, xtandard.Opt(name, args...))
	return c
}

// With appends options: typed structs of package info or [xtandard.Option] values.
func (c *InfoChain) With(opts ...xtandard.InfoOption) *InfoChain {
	for _, o := range opts {
		if o != nil {
			c.opts = append(c.opts, o.InfoOption())
		}
	}
	return c
}

// Encoding overrides the source encoding of this URL.
func (c *InfoChain) Encoding(enc xtandard.SourceEncoding) *InfoChain {
	c.encoding = enc
	return c
}

// WithPDFPassword opens a password-protected PDF (pdp; it needs both keys).
func (c *InfoChain) WithPDFPassword(password string) *InfoChain {
	c.password = &password
	return c
}

// Build builds the URL.
func (c *InfoChain) Build() (string, error) {
	return c.b.build(info, [][]xtandard.Option{c.opts}, c.source, c.encoding, "", c.password)
}

// VideoChain is a fluent /video URL: one method per /video option name and alias.
type VideoChain struct {
	b        *Builder
	source   string
	opts     []xtandard.Option
	encoding xtandard.SourceEncoding
	ext      string
}

// Video starts a fluent /video URL for a source.
func (b *Builder) Video(source string) *VideoChain {
	return &VideoChain{b: b, source: source}
}

// Option appends one option occurrence by name or alias.
func (c *VideoChain) Option(name string, args ...xtandard.Arg) *VideoChain {
	c.opts = append(c.opts, xtandard.Opt(name, args...))
	return c
}

// With appends options: typed structs of package video or [xtandard.Option] values.
func (c *VideoChain) With(opts ...xtandard.VideoOption) *VideoChain {
	for _, o := range opts {
		if o != nil {
			c.opts = append(c.opts, o.VideoOption())
		}
	}
	return c
}

// Extension sets the output container.
func (c *VideoChain) Extension(ext string) *VideoChain {
	c.ext = ext
	return c
}

// Encoding overrides the source encoding of this URL.
func (c *VideoChain) Encoding(enc xtandard.SourceEncoding) *VideoChain {
	c.encoding = enc
	return c
}

// Build builds the URL.
func (c *VideoChain) Build() (string, error) {
	return c.b.build(video, [][]xtandard.Option{c.opts}, c.source, c.encoding, c.ext, nil)
}
