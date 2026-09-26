package imgproxy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths to every parser configuration. Parse must
// never panic, must return either a well-formed result or an errs error of
// a client kind, and argument errors must name a field.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"/unsafe/rs:fit:300:300/plain/https://example.com/a.jpg@webp",
		"/" + docsSig + docsPath,
		"/" + exampleSig + examplePath,
		"/unsafe/enc/" + encToken + ".png",
		"/unsafe/rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea/plain/s3://bucket/key.jpg",
		"/unsafe/rs,fill,1,1/-/q,80/aHR0cDovL2UuY29tL2E.png",
		"/unsafe/g:objw:a:1:b:2/c:0.5:0.5:fp:0.1:0.2/t:1:fff:1:1/pd:1::3/fq:png:1/plain/x@",
		"/unsafe/rs%3Afill/plain/local:/x",
		"/unsafe/-/-/plain/x",
		"/x/fill/1/1/aGk",
		"/unsafe/wm:1:re:1:1:1/bg:1:2:3/bga:0.5/exp:1/fn:eA:1/plain/%zz",
		"",
		"/",
		"//////",
		"/unsafe/:/:::/plain/@@",
	} {
		f.Add(s)
	}
	for _, e := range loadJSCore(f) {
		f.Add("/unsafe" + e.Path)
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path string) {
		for name, p := range fx.parsers {
			res, err := p.ParsePath(path)
			checkFuzz(t, name, path, res, err, transform.KindProcess)
		}
	})
}

// FuzzParseInfo is FuzzParse for the /info grammar: results must be info
// requests with InfoOptions set.
func FuzzParseInfo(f *testing.F) {
	for _, s := range []string{
		"/unsafe/plain/https://example.com/a.jpg",
		"/unsafe/s:1/f:1/d:1/bh:4:3/plain/https://example.com/a.jpg",
		"/unsafe/exif:1:1/a:1:1/cl:3:cat/c:10:10:obj:face/op:fp:0.1:0.2/p:16/avg:1:0/dc:1:1/chs:md5:sha512/plain/x@png",
		"/unsafe/th:1/ph:1/pn:1/cs:1/b:1/sf:1/do:1/vm:0/iptc:0/xmp:0/aGk.png",
		"/unsafe/pg:1/vts:2/vtk:1/cb:x/exp:1/pr:a/hs:md5:" + strings.Repeat("0", 32) + "/msr:1/msfs:1/bc:1/ct:a/enc/" + encToken,
		"/" + exampleSig8 + examplePath,
		"/unsafe/s:1/-/f:1/plain/x",
		"/unsafe/bh%3A4%3A3/plain/local:/x",
		"",
		"/",
		"/unsafe/:/::/plain/@@",
	} {
		f.Add(s)
	}
	for _, e := range loadJSCore(f) {
		if e.Kind == "info" {
			f.Add("/unsafe" + e.Path)
		}
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path string) {
		for name, p := range fx.parsers {
			res, err := p.ParseInfoPath(path)
			checkFuzz(t, name, path, res, err, transform.KindInfo)
		}
	})
}

// checkFuzz asserts the invariants of one fuzzed parse.
func checkFuzz(t *testing.T, name, path string, res *dialect.Result, err error, kind transform.Kind) {
	t.Helper()
	if err != nil {
		if res != nil {
			t.Fatalf("%s: result with error", name)
		}
		if _, ok := errors.AsType[*errs.Error](err); !ok {
			t.Fatalf("%s: %q: error %T is not an errs.Error", name, path, err)
		}
		switch errs.KindOf(err) {
		case errs.KindInvalidURL, errs.KindUnauthorized, errs.KindForbidden, errs.KindExpired:
		default:
			t.Fatalf("%s: %q: unexpected kind %v: %v", name, path, errs.KindOf(err), err)
		}
		if ae, ok := errors.AsType[*dialect.ArgumentError](err); ok && ae.Field == "" {
			t.Fatalf("%s: argument error without field: %v", name, err)
		}
		return
	}
	r := res.Request
	if res.SourceString == "" || res.SourceString != r.Source.Raw {
		t.Fatalf("%s: %q: bad source %q", name, path, res.SourceString)
	}
	if n := len(r.Pipelines); n < 1 || n > transform.MaxPipelines {
		t.Fatalf("%s: %q: %d pipelines", name, path, n)
	}
	if kind == transform.KindInfo && (r.Kind != transform.KindInfo || r.Info == nil || len(r.Pipelines) != 1) {
		t.Fatalf("%s: %q: not an info request", name, path)
	}
	if kind != transform.KindInfo && (r.Kind == transform.KindInfo || r.Info != nil) {
		t.Fatalf("%s: %q: info request from the processing grammar", name, path)
	}
	// The request must survive defaults and canonical encoding.
	r.ApplyDefaults(transform.StandardDefaults())
	_ = r.Validate()
	_ = r.CanonicalKey()
}
