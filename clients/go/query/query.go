// Package query builds URLs of xtandard-image's query dialect
// (docs/URL-DIALECTS.md §4), mounted at /q (the server needs
// XI_DIALECTS_QUERY_ENABLED=true):
//
//	{BaseURL}{Mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&format=…][&pdp=…][&xi_sig=…]
//	{BaseURL}{Mount}/video?url=<source>&<video option>=<arg,arg>…[&format=…][&xi_sig=…]
//	{BaseURL}{Mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]
//
// With a signing key every URL ends in an xi_sig over the path after the
// mount and the query. A [Builder] is immutable and safe for concurrent use;
// a chain is for one URL and is not.
package query

import (
	"strings"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/internal/core"
)

// Config holds the settings of query dialect URLs. The zero value builds
// unsigned, root-relative URLs under /q.
type Config struct {
	// BaseURL is the server origin (and any path before the mount). Trailing slashes are dropped.
	BaseURL string
	// Mount is the dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX): "/q"
	// when empty; "/" mounts it at the root.
	Mount string
	// Signing signs every URL with xi_sig (the imgproxy key and salt); nil leaves them unsigned.
	Signing *xtandard.SigningKey
	// Encryption is the key of PDF passwords.
	Encryption *xtandard.EncryptionKey
	// Separator is the argument separator inside o=; ":" when empty.
	Separator string
	// OptionNames is how option names are written: short (the default when empty), canonical or given.
	OptionNames xtandard.OptionNames
}

// Builder builds query dialect URLs.
type Builder struct {
	cfg Config
}

// New returns a Builder. An invalid separator is an [xtandard.ErrInvalidSeparator] error.
func New(cfg Config) (*Builder, error) {
	if cfg.Mount == "" {
		cfg.Mount = xtandard.QueryMount
	}
	cfg.Mount = strings.TrimRight(cfg.Mount, "/")
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Separator == "" {
		cfg.Separator = xtandard.DefaultSeparator
	}
	if cfg.OptionNames == "" {
		cfg.OptionNames = xtandard.ShortNames
	}
	if err := core.CheckSeparator(cfg.Separator, xtandard.ForbiddenQuerySeparators()); err != nil {
		return nil, err
	}
	return &Builder{cfg: cfg}, nil
}

// Config returns the builder's settings, with the defaults filled in (Mount without its trailing slash).
func (b *Builder) Config() Config { return b.cfg }

// Param is one native query key (w, fit, g, f, q, preset, …; the
// xtandard.QueryKey* and xtandard.ImaginaryParam* constants) and its value: a
// string, a bool (true/false), an integer or a float, or nil to skip the key.
type Param struct {
	Key   string
	Value any
}

// P returns a Param.
func P(key string, value any) Param { return Param{Key: key, Value: value} }

// Params are the inputs of a /q/ image URL besides its source.
type Params struct {
	// Params are the native keys, in order (keys may repeat); they come before o=.
	Params []Param
	// Options are imgproxy processing options, written as o=.
	Options []xtandard.ProcessingOption
	// Pipelines are chained pipelines after Options, joined by "/-/" inside o=.
	Pipelines [][]xtandard.ProcessingOption
	// Extension is the output format (format=).
	Extension string
	// PDFPassword is the plain-text password of a protected PDF (pdp=; needs
	// Encryption and Signing).
	PDFPassword *string
}

// VideoParams are the inputs of a /q/video URL besides its source.
type VideoParams struct {
	// Options are the /video options, each a key whose arguments are joined
	// by ","; crop and flip are written c and fl. A key may repeat.
	Options []xtandard.VideoOption
	// Extension is the output container (format=).
	Extension string
}

// StreamParams are the inputs of a /q/video HLS or DASH manifest URL.
type StreamParams struct {
	// Protocol is hls or dash.
	Protocol xtandard.StreamProtocol
	// Renditions are the rungs by the short side of the frame; none means the server's ladder.
	Renditions []int
	// SegmentDuration is the segment length in whole seconds; 0 means the server's default.
	SegmentDuration int
	// SegmentFormat is fmp4 (the default) or ts (HLS only); "" means the default.
	SegmentFormat xtandard.SegmentFormat
	// Options are other /video options applied to every rendition.
	Options []xtandard.VideoOption
}

type pair struct{ k, v string }

func params(ps []Param) ([]pair, error) {
	out := make([]pair, 0, len(ps))
	for _, p := range ps {
		if p.Value == nil {
			continue
		}
		v, err := core.NativeValue(p.Key, p.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, pair{p.Key, v})
	}
	return out, nil
}

func (b *Builder) finish(path string, pairs []pair) (string, error) {
	var q strings.Builder
	for i, p := range pairs {
		if i > 0 {
			q.WriteByte('&')
		}
		q.WriteString(xtandard.EscapeQueryValue(p.k))
		q.WriteByte('=')
		q.WriteString(xtandard.EscapeQueryValue(p.v))
	}
	query := q.String()
	if b.cfg.Signing != nil {
		var err error
		if query, err = xtandard.SignXiQuery(path, query, *b.cfg.Signing); err != nil {
			return "", err
		}
	}
	return b.cfg.BaseURL + b.cfg.Mount + path + "?" + query, nil
}

// URL builds a native /q/ image URL: url, the native keys, o=, format= and pdp=.
func (b *Builder) URL(source string, p Params) (string, error) {
	pipelines := make([][]xtandard.Option, 0, 1+len(p.Pipelines))
	pipelines = append(pipelines, core.ProcessingOptions(p.Options))
	for _, pl := range p.Pipelines {
		pipelines = append(pipelines, core.ProcessingOptions(pl))
	}
	return b.image(source, p.Params, pipelines, p.Extension, p.PDFPassword)
}

func (b *Builder) image(source string, ps []Param, pipelines [][]xtandard.Option, ext string, pdfPassword *string) (string, error) {
	cfg := &b.cfg
	if err := xtandard.CheckSource(source); err != nil {
		return "", err
	}
	native, err := params(ps)
	if err != nil {
		return "", err
	}
	pairs := append([]pair{{"url", source}}, native...)
	var o strings.Builder
	for _, pl := range pipelines {
		if len(pl) == 0 {
			continue
		}
		if o.Len() > 0 {
			o.WriteString("/" + xtandard.PipelineSeparator + "/")
		}
		for i, opt := range pl {
			seg, err := core.FormatOption(xtandard.CatalogueProcessing, opt, cfg.Separator, cfg.OptionNames)
			if err != nil {
				return "", err
			}
			if i > 0 {
				o.WriteByte('/')
			}
			o.WriteString(seg)
		}
	}
	if o.Len() > 0 {
		pairs = append(pairs, pair{"o", o.String()})
	}
	if ext != "" {
		if err := xtandard.CheckExtension(ext); err != nil {
			return "", err
		}
		pairs = append(pairs, pair{"format", ext})
	}
	if pdfPassword != nil {
		token, err := core.PasswordToken(cfg.Encryption, cfg.Signing, *pdfPassword)
		if err != nil {
			return "", err
		}
		name := xtandard.PDFPasswordShort
		if cfg.OptionNames == xtandard.CanonicalNames {
			name = xtandard.PDFPasswordName
		}
		pairs = append(pairs, pair{name, token})
	}
	return b.finish("/", pairs)
}

// VideoURL builds a /q/video URL.
func (b *Builder) VideoURL(source string, p VideoParams) (string, error) {
	return b.video(source, core.VideoOptions(p.Options), p.Extension)
}

// StreamURL builds a /q/video HLS or DASH manifest URL (format=m3u8 or mpd).
func (b *Builder) StreamURL(source string, p StreamParams) (string, error) {
	opts, container, err := core.StreamOptions(core.VideoOptions(p.Options), core.Stream{
		Protocol:        p.Protocol,
		Renditions:      p.Renditions,
		SegmentDuration: p.SegmentDuration,
		SegmentFormat:   p.SegmentFormat,
	})
	if err != nil {
		return "", err
	}
	return b.video(source, opts, string(container))
}

func (b *Builder) video(source string, opts []xtandard.Option, ext string) (string, error) {
	if err := xtandard.CheckSource(source); err != nil {
		return "", err
	}
	pairs := []pair{{"url", source}}
	for _, o := range opts {
		key, err := xtandard.CatalogueVideo.Written(o.Name, b.cfg.OptionNames)
		if err != nil {
			return "", err
		}
		if len(o.Args) == 0 {
			return "", xtandard.Errorf(xtandard.ErrInvalidArgument, "option %s: at least one argument is required", o.Name)
		}
		values := make([]string, len(o.Args))
		for i, a := range o.Args {
			s, err := core.FormatArg(a, ",", "option "+o.Name)
			if err != nil {
				return "", err
			}
			if strings.Contains(s, ":") {
				return "", xtandard.Errorf(xtandard.ErrInvalidArgument, "option %s argument %d: %q contains ':'", o.Name, i+1, s)
			}
			values[i] = s
		}
		pairs = append(pairs, pair{xtandard.NativeVideoKey(key), strings.Join(values, ",")})
	}
	if ext != "" {
		if err := xtandard.CheckExtension(ext); err != nil {
			return "", err
		}
		pairs = append(pairs, pair{"format", ext})
	}
	return b.finish("/video", pairs)
}

// ImaginaryURL builds an imaginary endpoint URL (/q/resize?url=…&width=300).
// An endpoint that is not lower-case ASCII letters, or is video, is an
// [xtandard.ErrInvalidEndpoint] error.
func (b *Builder) ImaginaryURL(endpoint xtandard.ImaginaryEndpoint, source string, ps ...Param) (string, error) {
	ok := endpoint != "" && endpoint != "video"
	for i := range len(endpoint) {
		ok = ok && 'a' <= endpoint[i] && endpoint[i] <= 'z'
	}
	if !ok {
		return "", xtandard.Errorf(xtandard.ErrInvalidEndpoint, "invalid imaginary endpoint %q", endpoint)
	}
	if err := xtandard.CheckSource(source); err != nil {
		return "", err
	}
	native, err := params(ps)
	if err != nil {
		return "", err
	}
	return b.finish("/"+string(endpoint), append([]pair{{"url", source}}, native...))
}
