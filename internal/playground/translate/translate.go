// Package translate writes one transform in every URL dialect (WP-94,
// POST /_playground/api/translate).
//
// The input is a URL of a mounted dialect, parsed with that dialect's own
// parser, or a canonical transform.Request. Each target dialect has a small
// emitter that builds its URL from the transform model; the emitted URL is
// then parsed back with the target dialect's parser and compared with the
// canonical request field by field, so the "unsupported here" notes are
// measured, never hand-maintained: whatever the target's parser does not
// reproduce is reported, by canonical field.
//
// Emitters never do I/O and never sign; the playground signs the URLs of
// mounted dialects with the server's keys.
package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/awssih"
	"github.com/xantiagoma/xtandard-image/internal/dialect/bunny"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/dialect/thumbor"
	"github.com/xantiagoma/xtandard-image/internal/dialect/wsrv"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Options configures New.
type Options struct {
	// Mounts are the playground's mounts (api/meta): the input URL's
	// dialect, the targets' prefixes and their bound sources.
	Mounts []playground.Mount
	// Separator is the imgproxy argument separator (":" when empty).
	Separator string
	// Presets expands presets in input URLs; nil means none are defined.
	Presets *preset.Store
	// Now is the clock of expiring URLs; nil means time.Now.
	Now func() time.Time
}

// target is one output dialect.
type target struct {
	name, label string
	// prefix is the documented default mount prefix, used when the
	// dialect is not mounted here.
	prefix string
	emit   func(e *emitter) (path, query string, err error)
}

// targets lists the output dialects in response order.
func targets() []target {
	return []target{
		{"imgproxy", "imgproxy", "/", emitImgproxy},
		{"query", "imgproxy options as a query (query dialect)", "/q/", emitQuery},
		{"imgix", "imgix", "/imgix/", emitImgix},
		{"cloudinary", "Cloudinary", "/cloudinary/", emitCloudinary},
		{"imagekit", "ImageKit", "/imagekit/", emitImagekit},
		{"cloudflare", "Cloudflare Images", "/cdn-cgi/image/", emitCloudflare},
		{"wsrv", "wsrv.nl (weserv)", "/wsrv/", emitWsrv},
		{"thumbor", "Thumbor", "/thumbor/", emitThumbor},
		{"bunny", "Bunny Optimizer", "/bunny/", emitBunny},
		{"awssih", "AWS Serverless Image Handler", "/sih/", emitAWSSIH},
	}
}

// Translator implements playground.Translator. It is safe for concurrent
// use.
type Translator struct {
	mounts []playground.Mount
	sep    string
	// input parses input URLs (with presets); check parses emitted URLs
	// back (without presets, which the canonical request already holds
	// expanded).
	input, check map[string]dialect.Dialect
}

var _ playground.Translator = (*Translator)(nil)

// New builds the parsers.
func New(o Options) (*Translator, error) {
	sep := o.Separator
	if sep == "" {
		sep = ":"
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	input, err := parsers(sep, o.Presets, now)
	if err != nil {
		return nil, err
	}
	check, err := parsers(sep, nil, now)
	if err != nil {
		return nil, err
	}
	return &Translator{mounts: o.Mounts, sep: sep, input: input, check: check}, nil
}

// parsers builds one unsigned parser per dialect.
func parsers(sep string, presets *preset.Store, now func() time.Time) (map[string]dialect.Dialect, error) {
	var problems []error
	add := func(m map[string]dialect.Dialect, name string, d dialect.Dialect, err error) {
		if err != nil {
			problems = append(problems, fmt.Errorf("translate: %s: %w", name, err))
			return
		}
		m[name] = d
	}
	m := map[string]dialect.Dialect{}
	ip, err := imgproxy.New(&imgproxy.Options{ArgumentsSeparator: sep, Presets: presets, Now: now})
	add(m, "imgproxy", ip, err)
	q, err := query.New(&query.Options{ArgumentsSeparator: sep, Now: now})
	add(m, "query", q, err)
	ix, err := imgix.New(&imgix.Options{Now: now})
	add(m, "imgix", ix, err)
	cl, err := cloudinary.New(&cloudinary.Options{Presets: presets, ArgumentsSeparator: sep})
	add(m, "cloudinary", cl, err)
	ik, err := imagekit.New(&imagekit.Options{Presets: presets, Now: now})
	add(m, "imagekit", ik, err)
	cf, err := cloudflare.New(&cloudflare.Options{Now: now})
	add(m, "cloudflare", cf, err)
	ws, err := wsrv.New(&wsrv.Options{Now: now})
	add(m, "wsrv", ws, err)
	add(m, "thumbor", thumbor.New(&thumbor.Options{}), nil)
	bn, err := bunny.New(&bunny.Options{Presets: presets, ArgumentsSeparator: sep, Now: now})
	add(m, "bunny", bn, err)
	sih, err := awssih.New(&awssih.Options{QueryParams: true, Now: now})
	add(m, "awssih", sih, err)
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return m, nil
}

// badRequestError is a client-safe translate error.
type badRequestError struct{ msg string }

func (e *badRequestError) Error() string { return "translate: " + e.msg }

func badRequest(msg string) error { return &badRequestError{msg: msg} }

// base is the dialect of a mount dialect key ("imgix[1]" → "imgix").
func base(d string) string {
	b, _, _ := strings.Cut(d, "[")
	return b
}

// Translate implements playground.Translator.
func (t *Translator) Translate(in *playground.TranslateRequest) (*playground.Translation, error) {
	req, err := t.canonical(in)
	if err != nil {
		return nil, err
	}
	if req.Kind != transform.KindProcess {
		return nil, badRequest("only processing URLs translate (not info or raw)")
	}
	norm := normalize(req)
	canon, err := toMap(req)
	if err != nil {
		return nil, err
	}
	out := &playground.Translation{Canonical: canon}
	emit := forEmit(req)
	for _, tg := range targets() {
		out.Targets = append(out.Targets, t.target(tg, emit, norm))
	}
	return out, nil
}

// canonical parses the input URL, or decodes the input request.
func (t *Translator) canonical(in *playground.TranslateRequest) (*transform.Request, error) {
	if in.Request != nil {
		b, err := json.Marshal(in.Request)
		if err != nil {
			return nil, badRequest("invalid request")
		}
		var r transform.Request
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return nil, badRequest("request is not a transform.Request")
		}
		if r.Source.Raw == "" {
			return nil, badRequest("request has no source")
		}
		return &r, nil
	}
	i := slices.IndexFunc(t.mounts, func(m playground.Mount) bool { return m.ID == in.Mount })
	if i < 0 {
		return nil, badRequest("unknown mount")
	}
	m := t.mounts[i]
	d := t.input[base(m.Dialect)]
	if d == nil {
		return nil, badRequest("this dialect does not translate")
	}
	p := in.Path
	switch m.Scheme {
	case playground.SchemeImgproxy:
		p = "/insecure" + p
	case playground.SchemeThumbor:
		p = "/unsafe" + p
	}
	return parse(d, p, in.Query, m.Source)
}

// parse runs d on path and query as the router would for a mount bound to
// mountSource.
func parse(d dialect.Dialect, path, rawQuery, mountSource string) (*transform.Request, error) {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, badRequest("invalid query")
	}
	res, err := d.Parse(&dialect.Input{Path: path, Query: q, RawQuery: rawQuery, Mount: dialect.Mount{Source: mountSource}})
	if err != nil {
		return nil, err
	}
	r := res.Request
	if mountSource != "" && r.Source.Mount == "" {
		r.Source.Mount = mountSource
	}
	return r, nil
}

// target emits and checks one dialect.
func (t *Translator) target(tg target, req, norm *transform.Request) playground.TranslateTarget {
	out := playground.TranslateTarget{Dialect: tg.name, Label: tg.label}
	prefix := tg.prefix
	mountSource := ""
	for _, m := range t.mounts {
		if base(m.Dialect) == tg.name && m.Prefix != "" {
			out.Mount, prefix, mountSource = m.ID, m.Prefix, m.Source
			break
		}
	}
	src, err := sourceOf(req.Source)
	if err != nil {
		out.Error = "the source cannot be written in URLs"
		return out
	}
	e := &emitter{r: req, src: src, mountSource: mountSource, sep: t.sep}
	p, q, err := tg.emit(e)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Path, out.Query = p, q
	if out.Mount == "" {
		// Not mounted here: the URL is shown under the default prefix.
		out.Path = strings.TrimSuffix(prefix, "/") + p
	}
	pathForParse := p
	switch tg.name {
	case "imgproxy":
		pathForParse = "/insecure" + p
	case "thumbor":
		pathForParse = "/unsafe" + p
	}
	back, err := parse(t.check[tg.name], pathForParse, q, mountSource)
	if err != nil {
		out.Error = "the emitted URL does not parse: " + publicText(err)
		out.Path, out.Query = "", ""
		return out
	}
	out.Unsupported, out.Approximated = compare(norm, normalize(back))
	return out
}

// publicText is the client-safe text of a dialect error.
func publicText(err error) string {
	type public interface {
		error
		PublicMessage() string
	}
	if p, ok := errors.AsType[public](err); ok {
		return p.PublicMessage()
	}
	return "invalid url"
}

// toMap is r as a JSON object.
func toMap(r *transform.Request) (map[string]any, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("translate: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("translate: %w", err)
	}
	return m, nil
}

// src is the source of the request in the forms emitters need.
type src struct {
	// abs is the canonical absolute source string: http(s)://, s3://,
	// xi://<source>/<key>, playground://...
	abs string
	ref source.Ref
}

// sourceOf canonicalises s: a relative key on a mount source becomes
// xi://<source>/<key>.
func sourceOf(s transform.SourceSpec) (src, error) {
	ref, err := source.ParseRef(s.Raw)
	if err != nil {
		return src{}, err
	}
	if ref.Scheme == source.SchemeRelative {
		if s.Mount == "" {
			return src{}, errors.New("relative source without a mount")
		}
		abs := "xi://" + s.Mount + "/" + ref.RawPath
		if ref, err = source.ParseRef(abs); err != nil {
			return src{}, err
		}
		return src{abs: abs, ref: ref}, nil
	}
	return src{abs: ref.String(), ref: ref}, nil
}

// isHTTP reports an http(s) source.
func (s src) isHTTP() bool {
	return s.ref.Scheme == source.SchemeHTTP || s.ref.Scheme == source.SchemeHTTPS
}

// keyOn returns the escaped key when the source is xi://<name>/<key>.
func (s src) keyOn(name string) (string, bool) {
	if name == "" || s.ref.Scheme != source.SchemeXI || s.ref.Host != name {
		return "", false
	}
	return s.ref.RawPath, true
}

// normalize returns a copy of r with defaults applied and the fields that
// never describe the output cleared, for compare.
func normalize(r *transform.Request) *transform.Request {
	c := *r
	c.Ignored = nil
	c.Dialect = ""
	c.Delivery.Presets = nil
	if s, err := sourceOf(c.Source); err == nil {
		c.Source = transform.SourceSpec{Raw: s.abs, Hashsum: c.Source.Hashsum}
	}
	c.ApplyDefaults(transform.StandardDefaults())
	for i := range c.Pipelines {
		p := &c.Pipelines[i]
		faceClass(&p.Gravity)
		if p.Crop != nil {
			cp := *p.Crop
			faceClass(&cp.Gravity)
			p.Crop = &cp
		}
		// Without a target size, dimension-less resizing modes are the
		// same: the image keeps its size.
		rs := &p.Resize
		if rs.Width.IsZero() && rs.Height.IsZero() && rs.MinWidth == 0 && rs.MinHeight == 0 && rs.AspectRatio == 0 &&
			rs.MaxWidth == 0 && rs.MaxHeight == 0 && rs.DPR == 1 && rs.ZoomX == 1 && rs.ZoomY == 1 && !p.Extend.Enabled {
			rs.Type, rs.Enlarge = transform.ResizeFit, false
		}
	}
	return &c
}

// forEmit is r with object gravity on the face class alone written as
// face gravity, the form the emitters look for.
func forEmit(r *transform.Request) *transform.Request {
	c := *r
	c.Pipelines = slices.Clone(r.Pipelines)
	for i := range c.Pipelines {
		faceClass(&c.Pipelines[i].Gravity)
	}
	return &c
}

// faceClass writes object gravity on the face class alone as face gravity
// (imgproxy obj:face and imgix crop=faces mean the same crop).
func faceClass(g *transform.Gravity) {
	if g.Type == transform.GravityObject && len(g.Classes) == 1 && g.Classes[0] == "face" {
		g.Type, g.Classes = transform.GravityFace, nil
	}
}
