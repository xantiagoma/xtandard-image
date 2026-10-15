// Package imgproxy builds URLs of xtandard-image's imgproxy dialect
// (docs/URL-DIALECTS.md §3.1):
//
//	{BaseURL}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
//	{BaseURL}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)
//
// Sources are base64url (the default), plain/ (percent-escaped, @ext) or enc/
// (AES-CBC); base64 and enc sources take .ext.
//
// A [Builder] has two forms. A call with a params struct takes typed options
// (the structs of packages processing, info and video) or untyped
// [xtandard.Option] values:
//
//	b.URL("s3://bucket/cat.jpg", imgproxy.Params{
//		Options:   []xtandard.ProcessingOption{processing.Resize{Type: processing.ResizeTypeFill, Width: new(300)}, xtandard.Opt("q", 80)},
//		Extension: "webp",
//	})
//
// A fluent chain has one method per option name and alias:
//
//	b.Image("s3://bucket/cat.jpg").Resize("fill", 300, 400).Q(80).Extension("webp").Build()
//
// A Builder is immutable and safe for concurrent use; a chain is for one URL
// and is not.
package imgproxy

import (
	"strings"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/internal/core"
)

// Config holds the settings shared by every URL a [Builder] makes. The zero
// value builds unsigned, root-relative URLs with base64 sources.
type Config struct {
	// BaseURL is prepended to every path: the server origin and the dialect's
	// mount prefix, if any. Trailing slashes are dropped. Default "": root-relative paths.
	BaseURL string
	// Signing signs URLs with the imgproxy scheme; nil leaves them unsigned.
	Signing *xtandard.SigningKey
	// UnsignedSignature is the signature segment of unsigned URLs: insecure
	// (the default when empty), unsafe or _.
	UnsignedSignature string
	// Encryption is the key of encrypted sources and PDF passwords.
	Encryption *xtandard.EncryptionKey
	// SourceEncoding is the default source encoding (base64 when empty).
	SourceEncoding xtandard.SourceEncoding
	// SplitSource, when positive, splits base64 and encrypted sources into
	// "/"-separated chunks of that many characters.
	SplitSource int
	// Separator is the argument separator (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR); ":" when empty.
	Separator string
	// OptionNames is how option names are written: short (the default when empty), canonical or given.
	OptionNames xtandard.OptionNames
}

// Builder builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server.
type Builder struct {
	cfg Config
}

// New returns a Builder. An invalid separator is an [xtandard.ErrInvalidSeparator] error.
func New(cfg Config) (*Builder, error) {
	if cfg.Separator == "" {
		cfg.Separator = xtandard.DefaultSeparator
	}
	if cfg.UnsignedSignature == "" {
		cfg.UnsignedSignature = xtandard.DefaultUnsigned
	}
	if cfg.SourceEncoding == "" {
		cfg.SourceEncoding = xtandard.SourceEncodingBase64
	}
	if cfg.OptionNames == "" {
		cfg.OptionNames = xtandard.ShortNames
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if err := core.CheckSeparator(cfg.Separator, xtandard.ForbiddenSeparators()); err != nil {
		return nil, err
	}
	return &Builder{cfg: cfg}, nil
}

// Config returns the builder's settings, with the defaults filled in.
func (b *Builder) Config() Config { return b.cfg }

// Params are the inputs of a processing URL besides its source.
type Params struct {
	// Options are the options of the first pipeline, in URL order.
	Options []xtandard.ProcessingOption
	// Pipelines are chained pipelines written after Options, each after a
	// "/-/" segment. Empty pipelines are dropped.
	Pipelines [][]xtandard.ProcessingOption
	// Extension is the output format extension (.webp, or @webp for plain sources).
	Extension string
	// SourceEncoding overrides the configured source encoding.
	SourceEncoding xtandard.SourceEncoding
	// PDFPassword is the plain-text password of a protected PDF; it needs
	// Encryption and Signing and is written as the first option (pdp).
	PDFPassword *string
}

// InfoParams are the inputs of an /info URL besides its source.
type InfoParams struct {
	// Options are the /info options, in URL order (one pipeline).
	Options []xtandard.InfoOption
	// SourceEncoding overrides the configured source encoding.
	SourceEncoding xtandard.SourceEncoding
	// PDFPassword is as in [Params].
	PDFPassword *string
}

// VideoParams are the inputs of a /video URL besides its source.
type VideoParams struct {
	// Options are the /video options, in URL order (one pipeline).
	Options []xtandard.VideoOption
	// Extension is the output container (webm, mp4, mp3, png for a waveform, …).
	Extension string
	// SourceEncoding overrides the configured source encoding.
	SourceEncoding xtandard.SourceEncoding
}

// StreamParams are the inputs of an HLS or DASH manifest URL besides its source.
type StreamParams struct {
	// Protocol is hls or dash.
	Protocol xtandard.StreamProtocol
	// Renditions are the rungs by the short side of the frame (at most
	// [xtandard.MaxRenditions]); none means the server's ladder.
	Renditions []int
	// SegmentDuration is the segment length in whole seconds (1 to 30); 0 means the server's default.
	SegmentDuration int
	// SegmentFormat is fmp4 (CMAF, the default) or ts (MPEG-TS, HLS only); "" means the default.
	SegmentFormat xtandard.SegmentFormat
	// Options are other /video options (trim, crop, quality, audio, …) applied to every rendition.
	Options []xtandard.VideoOption
	// SourceEncoding overrides the configured source encoding.
	SourceEncoding xtandard.SourceEncoding
}

type endpoint int

const (
	process endpoint = iota
	info
	video
)

// URL builds a processing URL.
func (b *Builder) URL(source string, p Params) (string, error) {
	pipelines := make([][]xtandard.Option, 0, 1+len(p.Pipelines))
	pipelines = append(pipelines, core.ProcessingOptions(p.Options))
	for _, pl := range p.Pipelines {
		pipelines = append(pipelines, core.ProcessingOptions(pl))
	}
	return b.build(process, pipelines, source, p.SourceEncoding, p.Extension, p.PDFPassword)
}

// InfoURL builds an /info URL.
func (b *Builder) InfoURL(source string, p InfoParams) (string, error) {
	return b.build(info, [][]xtandard.Option{core.InfoOptions(p.Options)}, source, p.SourceEncoding, "", p.PDFPassword)
}

// VideoURL builds a /video URL: a video transformation, audio or a waveform.
func (b *Builder) VideoURL(source string, p VideoParams) (string, error) {
	return b.build(video, [][]xtandard.Option{core.VideoOptions(p.Options)}, source, p.SourceEncoding, p.Extension, nil)
}

// StreamURL builds an HLS or DASH manifest URL (/video/…@m3u8 or .mpd). The
// stream, segment_duration, segment_format and format options cannot be given
// as Options ([xtandard.ErrInvalidRequest]).
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
	return b.build(video, [][]xtandard.Option{opts}, source, p.SourceEncoding, string(container), nil)
}

func (b *Builder) build(ep endpoint, pipelines [][]xtandard.Option, source string, enc xtandard.SourceEncoding, ext string, pdfPassword *string) (string, error) {
	cfg := &b.cfg
	sep, style := cfg.Separator, cfg.OptionNames
	cat := xtandard.CatalogueProcessing
	switch ep {
	case process:
	case info:
		cat = xtandard.CatalogueInfo
	case video:
		cat = xtandard.CatalogueVideo
	}
	nonEmpty := pipelines[:0:0]
	for _, p := range pipelines {
		if len(p) > 0 {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if ep != process && len(nonEmpty) > 1 {
		return "", xtandard.Errorf(xtandard.ErrInvalidRequest, "/%s URLs have no chained pipelines", cat)
	}
	if ep == video && pdfPassword != nil {
		return "", xtandard.Errorf(xtandard.ErrInvalidRequest, "/video URLs take no pdf_password")
	}
	var path strings.Builder
	if pdfPassword != nil {
		token, err := core.PasswordToken(cfg.Encryption, cfg.Signing, *pdfPassword)
		if err != nil {
			return "", err
		}
		name := xtandard.PDFPasswordShort
		if style == xtandard.CanonicalNames {
			name = xtandard.PDFPasswordName
		}
		path.WriteString("/" + name + sep + token)
	}
	for i, p := range nonEmpty {
		if i > 0 {
			path.WriteString("/" + xtandard.PipelineSeparator)
		}
		for _, o := range p {
			seg, err := core.FormatOption(cat, o, sep, style)
			if err != nil {
				return "", err
			}
			path.WriteString("/" + seg)
		}
	}
	if enc == "" {
		enc = cfg.SourceEncoding
	}
	src, err := b.sourceSegment(source, enc, ext)
	if err != nil {
		return "", err
	}
	path.WriteString("/" + src)
	return b.finish(ep, path.String()), nil
}

func split(token string, n int) string {
	if n <= 0 || len(token) <= n {
		return token
	}
	var b strings.Builder
	for i := 0; i < len(token); i += n {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(token[i:min(i+n, len(token))])
	}
	return b.String()
}

func (b *Builder) sourceSegment(source string, enc xtandard.SourceEncoding, ext string) (string, error) {
	if err := xtandard.CheckSource(source); err != nil {
		return "", err
	}
	if ext != "" {
		if err := xtandard.CheckExtension(ext); err != nil {
			return "", err
		}
	}
	suffix := func(mark string) string {
		if ext == "" {
			return ""
		}
		return mark + ext
	}
	switch enc {
	case xtandard.SourceEncodingPlain:
		return "plain/" + xtandard.EscapePlainSource(source) + suffix("@"), nil
	case xtandard.SourceEncodingBase64:
		return split(base64URL(source), b.cfg.SplitSource) + suffix("."), nil
	case xtandard.SourceEncodingEncrypted:
		if b.cfg.Encryption == nil {
			return "", xtandard.Errorf(xtandard.ErrMissingEncryption, "encrypted sources need an encryption key")
		}
		token, err := xtandard.EncryptSource(source, *b.cfg.Encryption)
		if err != nil {
			return "", err
		}
		return xtandard.EncryptedSourcePrefix + split(token, b.cfg.SplitSource) + suffix("."), nil
	}
	return "", xtandard.Errorf(xtandard.ErrInvalidRequest, "unknown source encoding %q", enc)
}

func (b *Builder) finish(ep endpoint, path string) string {
	cfg := &b.cfg
	if ep == video {
		sig := cfg.UnsignedSignature
		if cfg.Signing != nil {
			sig = xtandard.Sign("/video"+path, *cfg.Signing)
		}
		return cfg.BaseURL + "/video/" + sig + path
	}
	prefix := cfg.BaseURL
	if ep == info {
		prefix += "/info"
	}
	if cfg.Signing != nil {
		return prefix + xtandard.SignImgproxyPath(path, *cfg.Signing)
	}
	return prefix + "/" + cfg.UnsignedSignature + path
}
