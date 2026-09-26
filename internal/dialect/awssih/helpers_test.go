package awssih_test

import (
	"bytes"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/awssih"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json")

// secret is the SIH test suite's signing secret
// (test/image-request/setup.spec.ts, SecretString {signatureKey: "secret"}).
const secret = "secret"

// mountSource is the source of the test mount (SIH's SOURCE_BUCKETS[0]).
const mountSource = "images"

// now is the fixed clock for expires.
var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// buckets lists every bucket name the SIH test fixtures use.
var buckets = map[string]string{
	"validBucket":      "images",
	"validBucket2":     "images",
	"allowedBucket001": "images",
	"bucket-name-here": "images",
	"my-sample-bucket": "images",
	"source-bucket":    "images",
	"test":             "images",
	"bucket":           "logos",
	"sample-bucket":    "logos",
	"elasticbeanstalk-us-east-2-066784885518": "images",
}

// fakeThumbor stands in for the thumbor dialect (WP-45): it accepts
// "/unsafe/<rest>" and returns a request whose source is <rest>, so the
// golden files show exactly what was forwarded.
type fakeThumbor struct {
	mu    sync.Mutex
	calls int
	in    dialect.Input // last input
}

func (*fakeThumbor) Name() string            { return "thumbor" }
func (*fakeThumbor) Verifier() sign.Verifier { return nil }

func (f *fakeThumbor) Parse(in *dialect.Input) (*dialect.Result, error) {
	f.mu.Lock()
	f.calls++
	f.in = *in
	f.mu.Unlock()
	rest, ok := strings.CutPrefix(in.Path, "/unsafe/")
	if !ok || rest == "" {
		return nil, dialect.InvalidURL("thumbor: bad path")
	}
	if strings.HasPrefix(rest, "fail/") {
		return nil, dialect.InvalidArgument("thumbor", rest, "anything else")
	}
	r := &transform.Request{
		Dialect:   "thumbor",
		Source:    transform.SourceSpec{Raw: rest},
		Pipelines: []transform.Pipeline{{Resize: transform.Resize{Type: transform.ResizeFit, Width: transform.Dim{Px: 1}}}},
	}
	return &dialect.Result{Request: r, SourceString: rest}, nil
}

func (f *fakeThumbor) last(t *testing.T) dialect.Input {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == 0 {
		t.Fatal("thumbor not called")
	}
	return f.in
}

type fixture struct {
	parsers map[string]*awssih.Parser
	signer  *sign.Signer
	thumbor *fakeThumbor
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
	keys := sign.NewKeyring(sign.NewKey([]byte(secret), nil))
	required, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{Keys: keys, Required: true})
	if err != nil {
		return nil, err
	}
	optional, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{Keys: keys})
	if err != nil {
		return nil, err
	}
	signer, err := sign.NewSigner(sign.NewAWSSIHScheme(), keys)
	if err != nil {
		return nil, err
	}
	rewrite, err := awssih.ParseRewrite("/(filters-)/gm", "filters:")
	if err != nil {
		return nil, err
	}
	th := &fakeThumbor{}
	clock := func() time.Time { return now }
	configs := map[string]awssih.Options{
		"default":  {},
		"strict":   {Strict: true},
		"query":    {QueryParams: true},
		"signed":   {Verifier: required, QueryParams: true},
		"optional": {Verifier: optional},
		"thumbor":  {Thumbor: th, QueryParams: true},
		"custom":   {Thumbor: th, Rewrite: rewrite},
	}
	f := &fixture{parsers: map[string]*awssih.Parser{}, signer: signer, thumbor: th}
	for name, o := range configs {
		o.Buckets = buckets
		o.Now = clock
		p, err := awssih.New(&o)
		if err != nil {
			return nil, err
		}
		f.parsers[name] = p
	}
	return f, nil
}

// b64 returns "/" + standard base64 of the JSON encoding of v, the way
// SIH's documentation builds request paths.
func b64(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return "/" + base64.StdEncoding.EncodeToString(b)
}

// b64raw is b64 for literal JSON text (key order preserved).
func b64raw(s string) string { return "/" + base64.StdEncoding.EncodeToString([]byte(s)) }

// testCase is one table row: path and raw query parsed with a named
// configuration.
type testCase struct {
	name  string
	cfg   string // "" = default
	path  string
	query string
}

func outcome(tc testCase, res *dialect.Result, err error) map[string]any {
	out := map[string]any{"path": tc.path, "query": tc.query}
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

// nonZero converts v to JSON-able data, dropping zero struct fields.
// Pointers are kept, enums use MarshalText.
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
		if t, ok := reflect.TypeAssert[time.Time](v); ok {
			return t.Format(time.RFC3339), !t.IsZero()
		}
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

// runGolden parses every case and checks it against
// testdata/golden/<group>.json (regenerate with -update).
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
			res, err := p.Parse(&dialect.Input{Path: tc.path, RawQuery: tc.query, Mount: dialect.Mount{Source: mountSource}})
			if err == nil {
				checkResult(t, res)
			} else if res != nil {
				t.Fatal("result with error")
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
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
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
	if r.Dialect != awssih.Name {
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
