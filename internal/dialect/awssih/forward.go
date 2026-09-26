package awssih

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// unsafePrefix is thumbor's unsigned-URL segment. SIH's thumbor URLs have
// no signature segment (SIH signs with its own query parameter), so it is
// added before the path is handed to the thumbor dialect.
const unsafePrefix = "/unsafe"

// forward handles SIH's Thumbor and Custom request types: escaped is the
// escaped path after the mount, starting with "/". The rewrite (Custom)
// runs first; an "s3:<bucket>" segment naming a listed bucket selects that
// bucket's source and is removed (SIH parseImageBucket/parseImageKey); the
// rest goes to the thumbor dialect as "/unsafe" + path with an empty
// query. The thumbor result keeps SIH's signature outcome and gets the
// query-param edits and expiry of the SIH URL.
func (p *Parser) forward(in *dialect.Input, escaped string, params []param) (*dialect.Result, error) {
	if p.o.Rewrite != nil {
		escaped = p.o.Rewrite.Apply(escaped)
		if escaped == "" || escaped[0] != '/' {
			escaped = "/" + escaped
		}
	}
	if p.o.Thumbor == nil {
		return nil, dialect.InvalidURL("path is not a base64-encoded JSON request")
	}
	path, src := p.bucketSegment(escaped, in.Mount.Source)
	if path != unsafePrefix && !strings.HasPrefix(path, unsafePrefix+"/") {
		path = unsafePrefix + path
	}
	fwd := *in
	fwd.Path, fwd.RawQuery, fwd.Query = path, "", nil
	fwd.Mount.Source = src
	res, err := p.o.Thumbor.Parse(&fwd)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Request == nil {
		return nil, errs.Newf(errs.KindInternal, "awssih: thumbor dialect returned no request")
	}
	r := res.Request
	r.Dialect = Name
	if r.Source.Mount == "" {
		r.Source.Mount = src
	}
	if len(r.Pipelines) == 0 {
		r.Pipelines = make([]transform.Pipeline, 1)
	}
	b := newBuilder(p, r)
	qe, err := p.queryEdits(b, params)
	if err != nil {
		return nil, err
	}
	for _, e := range qe {
		if err := b.applyEdit(e.key, e.raw); err != nil {
			return nil, err
		}
	}
	if b.toFormat != 0 {
		r.Output.Format = b.toFormat
	}
	return res, nil
}

// bucketSegment removes the first "s3:<bucket>" path segment whose bucket
// is listed and returns the remaining path and the source to use.
func (p *Parser) bucketSegment(path, mountSource string) (string, string) {
	if !strings.Contains(path, "/s3:") {
		return path, mountSource
	}
	segs := strings.Split(path[1:], "/")
	for i, seg := range segs {
		bucket, ok := strings.CutPrefix(seg, "s3:")
		if !ok {
			continue
		}
		if src, listed := p.o.Buckets[bucket]; listed {
			return "/" + strings.Join(append(segs[:i:i], segs[i+1:]...), "/"), src
		}
	}
	return path, mountSource
}
