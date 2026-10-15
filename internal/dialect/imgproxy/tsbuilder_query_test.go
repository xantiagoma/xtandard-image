package imgproxy_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// The query dialect half of the TypeScript builder fixture
// (tsbuilder_test.go): the TS builder writes every eligible case a second
// time as a query dialect URL (packages/image-url src/query.ts), and the
// query parser must turn it into the request the imgproxy URL yields.

// queryEligible reports whether the TS builder also writes the case in
// the query dialect: processing and /video cases with a plain or base64
// source (the query dialect takes the URL as is), parsed with a config the
// query dialect shares (no separator, base URL or 8-byte signatures).
func queryEligible(c *builderCase) bool {
	switch c.Config {
	case "", "strict", "security", "signed", "required":
	default:
		return false
	}
	if c.Endpoint == "" && c.Extension != "" && rawOn(c) {
		return false // raw ignores a source extension; the query form writes it as format=
	}
	return c.Endpoint != "info" && (c.Encoding == "plain" || c.Encoding == "base64") &&
		(c.SignatureSize == 0 || c.SignatureSize == 32)
}

// rawOn reports whether a case's options turn raw on.
func rawOn(c *builderCase) bool {
	on := false
	for _, pl := range c.Pipelines {
		for _, e := range pl {
			if args, ok := e["raw"]; ok && len(args) > 0 {
				on = args[0] == true
			}
		}
	}
	return on
}

// queryParsers returns a query dialect parser per fixture config, with
// xi_sig verified against the fixture's signing key.
func queryParsers(t *testing.T) map[string]*query.Parser {
	t.Helper()
	keys, err := sign.ParseHexKeyring([]string{exampleKey}, []string{exampleSalt})
	if err != nil {
		t.Fatal(err)
	}
	xi, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32})
	if err != nil {
		t.Fatal(err)
	}
	xiRequired, err := sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: 32, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return now }
	out := map[string]*query.Parser{}
	for name, o := range map[string]query.Options{
		"":         {},
		"strict":   {Strict: true},
		"security": {AllowSecurityOptions: true},
		"signed":   {XIVerifier: xi},
		"required": {XIVerifier: xiRequired},
	} {
		o.Now = clock
		p, err := query.New(&o)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = p
	}
	return out
}

// queryKey is the canonical key of a parsed request with the source
// encoding cleared (a query URL carries the source as is).
func queryKey(res *dialect.Result) string {
	r := *res.Request
	r.Source.Encoding = 0
	return r.CanonicalKey()
}

// checkQueryURL parses the TS-built query URL and the imgproxy URL of a
// case and compares their sources, signature outcomes and requests.
func checkQueryURL(t *testing.T, f *fixture, qf map[string]*query.Parser, c *builderCase, regen string) {
	t.Helper()
	if c.QueryURL == "" {
		t.Fatalf("no TS-built query_url; %s", regen)
	}
	rest, ok := strings.CutPrefix(c.QueryURL, "/q")
	if !ok {
		t.Fatalf("query url %q is not under /q", c.QueryURL)
	}
	path, raw, _ := strings.Cut(rest, "?")
	wantPath := "/"
	if c.Endpoint == "video" {
		wantPath = "/video"
	}
	if path != wantPath {
		t.Fatalf("query url %q: path %q, want %q", c.QueryURL, path, wantPath)
	}
	cfg := c.Config
	if cfg == "" {
		cfg = "default"
	}
	ipPath, parse := c.URL, f.parsers[cfg].Parse
	if c.Endpoint == "video" {
		ipPath, parse = strings.TrimPrefix(ipPath, "/video"), f.parsers[cfg].ParseVideo
	}
	want, err := parse(&dialect.Input{Path: ipPath})
	if err != nil {
		t.Fatalf("%s: %v", c.URL, err)
	}
	got, err := qf[c.Config].Parse(&dialect.Input{Path: path, RawQuery: raw})
	if err != nil {
		t.Fatalf("%s: %v", c.QueryURL, err)
	}
	if got.SourceString != want.SourceString {
		t.Errorf("%s: source %q, imgproxy %q", c.QueryURL, got.SourceString, want.SourceString)
	}
	if got.Signature != want.Signature {
		t.Errorf("%s: signature %v, imgproxy %v", c.QueryURL, got.Signature, want.Signature)
	}
	if queryKey(got) != queryKey(want) {
		t.Errorf("%s parses to another request than %s (-imgproxy +query):\n%s", c.QueryURL, c.URL,
			cmp.Diff(jsonValue(t, want.Request), jsonValue(t, got.Request)))
	}
}
