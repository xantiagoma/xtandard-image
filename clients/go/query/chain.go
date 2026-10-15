package query

import xtandard "github.com/xantiagoma/xtandard-image/clients/go"

// ImageChain is a fluent /q/?url=… URL: the per-option methods (generated)
// write o=, [ImageChain.Param] a native key.
type ImageChain struct {
	b         *Builder
	source    string
	params    []Param
	pipelines [][]xtandard.Option
	ext       string
	password  *string
}

// Image starts a fluent /q/ image URL for a source.
func (b *Builder) Image(source string) *ImageChain {
	return &ImageChain{b: b, source: source, pipelines: [][]xtandard.Option{nil}}
}

// Param appends a native key (w, fit, g, …), written before o=; a nil value is skipped.
func (c *ImageChain) Param(key string, value any) *ImageChain {
	c.params = append(c.params, Param{Key: key, Value: value})
	return c
}

// Option appends one processing option occurrence to o= by name or alias.
func (c *ImageChain) Option(name string, args ...xtandard.Arg) *ImageChain {
	c.pipelines[len(c.pipelines)-1] = append(c.pipelines[len(c.pipelines)-1], xtandard.Opt(name, args...))
	return c
}

// With appends processing options to o=: typed structs of package processing or [xtandard.Option] values.
func (c *ImageChain) With(opts ...xtandard.ProcessingOption) *ImageChain {
	for _, o := range opts {
		if o != nil {
			c.pipelines[len(c.pipelines)-1] = append(c.pipelines[len(c.pipelines)-1], o.ProcessingOption())
		}
	}
	return c
}

// Pipeline starts the next chained pipeline inside o= ("/-/").
func (c *ImageChain) Pipeline() *ImageChain {
	c.pipelines = append(c.pipelines, nil)
	return c
}

// Extension sets the output format (format=).
func (c *ImageChain) Extension(ext string) *ImageChain {
	c.ext = ext
	return c
}

// WithPDFPassword opens a password-protected PDF (pdp=; it needs both keys).
func (c *ImageChain) WithPDFPassword(password string) *ImageChain {
	c.password = &password
	return c
}

// Build builds the URL.
func (c *ImageChain) Build() (string, error) {
	return c.b.image(c.source, c.params, c.pipelines, c.ext, c.password)
}

// VideoChain is a fluent /q/video URL: one method per /video option name and alias.
type VideoChain struct {
	b      *Builder
	source string
	opts   []xtandard.Option
	ext    string
}

// Video starts a fluent /q/video URL for a source.
func (b *Builder) Video(source string) *VideoChain {
	return &VideoChain{b: b, source: source}
}

// Option appends one /video option occurrence by name or alias.
func (c *VideoChain) Option(name string, args ...xtandard.Arg) *VideoChain {
	c.opts = append(c.opts, xtandard.Opt(name, args...))
	return c
}

// With appends /video options: typed structs of package video or [xtandard.Option] values.
func (c *VideoChain) With(opts ...xtandard.VideoOption) *VideoChain {
	for _, o := range opts {
		if o != nil {
			c.opts = append(c.opts, o.VideoOption())
		}
	}
	return c
}

// Extension sets the output container (format=).
func (c *VideoChain) Extension(ext string) *VideoChain {
	c.ext = ext
	return c
}

// Build builds the URL.
func (c *VideoChain) Build() (string, error) {
	return c.b.video(c.source, c.opts, c.ext)
}
