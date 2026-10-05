package imgproxy_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/crypt"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json")

// Test vectors.
const (
	// imgproxy docs "Signing the URL" example (key "secret", salt "hello").
	docsKey  = "736563726574"
	docsSalt = "68656C6C6F"
	docsPath = "/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png"
	docsSig  = "oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8"

	// imgproxy examples/signature.go.
	exampleKey  = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
	exampleSalt = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
	examplePath = "/rs:fit:300:300/plain/http://img.example.com/pretty/image.jpg"
	exampleSig  = "m3k5QADfcKPDj-SDI2AIogZbC3FlAXszuwhtWXYqavc"
	exampleSig8 = "m3k5QADfcKM"

	// imgproxy examples/encrypted_source_url.go key and its token for
	// "http://img.example.com/pretty/image.jpg" with IV "0123456789abcdef".
	encKey   = "1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"
	encToken = "MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06w" //nolint:gosec // G101: public test vector from imgproxy's examples

	trustedSig = "trusted-signature"
)

// now is the fixed clock for expires.
var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// fixture holds every parser configuration the tables refer to by name.
type fixture struct {
	parsers map[string]*imgproxy.Parser
	cipher  *crypt.Cipher
	signer  *sign.Signer // exampleKey/exampleSalt, 32 bytes
}

var (
	fixtureOnce sync.Once
	fixtureVal  *fixture
	errFixture  error
)

func getFixture(tb testing.TB) *fixture {
	tb.Helper()
	fixtureOnce.Do(func() { fixtureVal, errFixture = buildFixture() })
	if errFixture != nil {
		tb.Fatal(errFixture)
	}
	return fixtureVal
}

func buildFixture() (*fixture, error) {
	keys, err := sign.ParseHexKeyring([]string{exampleKey, docsKey}, []string{exampleSalt, docsSalt})
	if err != nil {
		return nil, err
	}
	signed, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: 32, Trusted: []string{trustedSig}})
	if err != nil {
		return nil, err
	}
	required, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: 32, Required: true})
	if err != nil {
		return nil, err
	}
	keys8, err := sign.ParseHexKeyring([]string{exampleKey}, []string{exampleSalt})
	if err != nil {
		return nil, err
	}
	signed8, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys8, Size: 8})
	if err != nil {
		return nil, err
	}
	scheme, err := sign.NewImgproxyScheme(32)
	if err != nil {
		return nil, err
	}
	signer, err := sign.NewSigner(scheme, keys8)
	if err != nil {
		return nil, err
	}
	c, err := crypt.New(encKey)
	if err != nil {
		return nil, err
	}
	clock := func() time.Time { return now }
	configs := map[string]imgproxy.Options{
		"default":  {},
		"strict":   {Strict: true},
		"security": {AllowSecurityOptions: true},
		"sep":      {ArgumentsSeparator: ","},
		"filename": {Base64URLIncludesFilename: true, Cipher: c},
		"baseurl": {BaseURL: "https://cdn.example.com/", URLReplacements: []imgproxy.URLReplacement{
			{Pattern: regexp.MustCompile(`^s3://legacy-bucket/`), Replacement: "s3://new-bucket/"},
			{Pattern: regexp.MustCompile(`^old/`), Replacement: "v2/"},
		}},
		"signed":   {Verifier: signed, Cipher: c},
		"signed8":  {Verifier: signed8},
		"required": {Verifier: required, Cipher: c},
		"enc":      {Cipher: c},
	}
	f := &fixture{parsers: make(map[string]*imgproxy.Parser, len(configs)), cipher: c, signer: signer}
	for name, o := range configs {
		o.Now = clock
		p, err := imgproxy.New(&o)
		if err != nil {
			return nil, err
		}
		f.parsers[name] = p
	}
	return f, nil
}

// testCase is one table row: a path parsed with a named configuration.
// Paths may contain ENC[plaintext] (replaced by a fresh encrypted token)
// and a leading SIG segment (replaced by the signature of the rest).
type testCase struct {
	name  string
	cfg   string // "" = default
	path  string
	info  bool // parse with ParseInfo (the path after "/info")
	video bool // parse with ParseVideo (the path after "/video")
}

// expand resolves the ENC[...] and SIG placeholders of path; VSIG is the
// signature of a /video URL ("/video/" + the rest).
func (f *fixture) expand(tb testing.TB, path string) string {
	tb.Helper()
	for {
		i := strings.Index(path, "ENC[")
		if i < 0 {
			break
		}
		j := strings.IndexByte(path[i:], ']')
		tok, err := f.cipher.Encrypt([]byte(path[i+4 : i+j]))
		if err != nil {
			tb.Fatal(err)
		}
		path = path[:i] + tok + path[i+j+1:]
	}
	if rest, ok := strings.CutPrefix(path, "/SIG/"); ok {
		sig, err := f.signer.Sign("/" + rest)
		if err != nil {
			tb.Fatal(err)
		}
		path = "/" + sig + "/" + rest
	}
	if rest, ok := strings.CutPrefix(path, "/VSIG/"); ok {
		sig, err := f.signer.Sign("/video/" + rest)
		if err != nil {
			tb.Fatal(err)
		}
		path = "/" + sig + "/" + rest
	}
	return path
}

// outcome renders a parse result or error as the golden JSON value.
func outcome(tc testCase, res *dialect.Result, err error) map[string]any {
	out := map[string]any{"path": tc.path}
	if tc.info {
		out["endpoint"] = "info"
	}
	if tc.video {
		out["endpoint"] = "video"
	}
	if tc.cfg != "" {
		out["config"] = tc.cfg
	}
	if err != nil {
		e := map[string]any{
			"kind":   errs.KindOf(err).String(),
			"status": errs.Status(errs.KindOf(err)),
		}
		if pe, ok := errors.AsType[*errs.Error](err); ok {
			e["public"] = pe.PublicMessage()
		}
		if ae, ok := errors.AsType[*dialect.ArgumentError](err); ok {
			e["field"] = ae.Field
		}
		out["error"] = e
		return out
	}
	out["signature"] = res.Signature.String()
	out["source"] = res.SourceString
	req, _ := nonZero(reflect.ValueOf(*res.Request))
	out["request"] = req
	return out
}

var textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()

// nonZero converts v to JSON-able data, dropping zero struct fields so the
// golden files only show what the URL set. Pointers are kept (a non-nil
// *bool false is meaningful), enums and times use MarshalText.
func nonZero(v reflect.Value) (any, bool) {
	if v.Type().Implements(textMarshaler) && v.Kind() != reflect.Pointer {
		tm, _ := reflect.TypeAssert[encoding.TextMarshaler](v)
		b, err := tm.MarshalText()
		if err != nil {
			return "!" + err.Error(), true
		}
		return string(b), true
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil, false
		}
		x, _ := nonZero(v.Elem())
		if x == nil {
			x = map[string]any{}
		}
		return x, true
	case reflect.Struct:
		m := map[string]any{}
		for i := range v.NumField() {
			f := v.Field(i)
			if f.IsZero() {
				continue
			}
			if x, ok := nonZero(f); ok {
				m[v.Type().Field(i).Name] = x
			}
		}
		if len(m) == 0 {
			return nil, false
		}
		return m, true
	case reflect.Slice:
		if v.Len() == 0 {
			return nil, false
		}
		l := make([]any, v.Len())
		for i := range v.Len() {
			x, _ := nonZero(v.Index(i))
			if x == nil {
				x = map[string]any{}
			}
			l[i] = x
		}
		return l, true
	case reflect.Map:
		if v.Len() == 0 {
			return nil, false
		}
		m := map[string]any{}
		for _, k := range v.MapKeys() {
			ks, _ := nonZero(k)
			x, _ := nonZero(v.MapIndex(k))
			m[ks.(string)] = x //nolint:forcetypeassert // map keys here are enums (TextMarshaler)
		}
		return m, true
	default:
		return v.Interface(), true
	}
}

// runGolden parses every case, checks the dialect output against
// testdata/golden/<group>.json and, for successes, that the request is
// valid after ApplyDefaults.
func runGolden(t *testing.T, group string, cases []testCase) {
	t.Helper()
	f := getFixture(t)
	file := filepath.Join("testdata", "golden", group+".json")
	golden := map[string]json.RawMessage{}
	if !*update {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read golden: %v (run go test -update)", err)
		}
		if err := json.Unmarshal(b, &golden); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	got := make(map[string]any, len(cases))
	seen := map[string]bool{}
	for _, tc := range cases {
		if seen[tc.name] {
			t.Fatalf("duplicate case %q", tc.name)
		}
		seen[tc.name] = true
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			if cfg == "" {
				cfg = "default"
			}
			p, ok := f.parsers[cfg]
			if !ok {
				t.Fatalf("unknown config %q", cfg)
			}
			parse := p.Parse
			switch {
			case tc.info:
				parse = p.ParseInfo
			case tc.video:
				parse = p.ParseVideo
			}
			res, err := parse(&dialect.Input{Path: f.expand(t, tc.path)})
			if err == nil {
				checkResult(t, res)
				if isInfo := res.Request.Kind == transform.KindInfo && res.Request.Info != nil; isInfo != tc.info {
					t.Errorf("info request = %v, want %v", isInfo, tc.info)
				}
				if isVideo := res.Request.Kind == transform.KindVideo && res.Request.Video != nil; isVideo != tc.video {
					t.Errorf("video request = %v, want %v", isVideo, tc.video)
				}
			}
			out := outcome(tc, res, err)
			if *update {
				mu.Lock()
				got[tc.name] = out
				mu.Unlock()
				return
			}
			want, ok := golden[tc.name]
			if !ok {
				t.Fatalf("no golden entry (run go test -update)")
			}
			if d := cmp.Diff(indent(t, want), indent(t, mustJSON(t, out))); d != "" {
				t.Errorf("mismatch (-golden +got):\n%s", d)
			}
		})
	}
	t.Cleanup(func() {
		if !*update {
			for name := range golden {
				if !seen[name] {
					t.Errorf("stale golden entry %q", name)
				}
			}
			return
		}
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(file, append(b, '\n'), 0o600); err != nil {
			t.Error(err)
		}
	})
}

// checkResult asserts invariants every successful parse must hold.
func checkResult(t *testing.T, res *dialect.Result) {
	t.Helper()
	r := res.Request
	if r.Dialect != imgproxy.Name {
		t.Errorf("Dialect = %q", r.Dialect)
	}
	if res.SourceString == "" || res.SourceString != r.Source.Raw {
		t.Errorf("SourceString %q, Source.Raw %q", res.SourceString, r.Source.Raw)
	}
	if n := len(r.Pipelines); n < 1 || n > transform.MaxPipelines {
		t.Errorf("%d pipelines", n)
	}
	c := *r
	c.Pipelines = slices.Clone(r.Pipelines)
	c.ApplyDefaults(transform.StandardDefaults())
	if err := c.Validate(); err != nil {
		t.Errorf("Validate after ApplyDefaults: %v", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func indent(t *testing.T, b []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
