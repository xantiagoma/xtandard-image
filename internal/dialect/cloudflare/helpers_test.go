package cloudflare_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json")

// Signing material.
const (
	// imgproxy examples/signature.go key and salt (Apache-2.0), for xi_sig.
	xiKey  = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
	xiSalt = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
	// The Cloudflare Images signing key of the sign package's docs vector.
	cfKey = "IMAGES_SIGNING_KEY"
	// unpic cloudflare_images.test.ts account hash and image id.
	unpicAccount = "1aS6NlIe-Sc1o3NhVvy8Qw"
	unpicImage   = "2ba36ba9-69f6-41b6-8ff0-2779b41df200"
)

// now is the fixed clock (unix 1790424000).
var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

const (
	expFuture = "1790427600" // now + 1h
	expPast   = "1790420400" // now − 1h
)

type fixture struct {
	parsers map[string]dialect.Dialect
	xi      *sign.Signer
	cf      *sign.Signer
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
	clock := func() time.Time { return now }
	xiKeys, err := sign.ParseHexKeyring([]string{xiKey}, []string{xiSalt})
	if err != nil {
		return nil, err
	}
	xiV, err := sign.NewXIVerifier(sign.XIOptions{Keys: xiKeys, Size: 32})
	if err != nil {
		return nil, err
	}
	xiScheme, err := sign.NewXIScheme(32)
	if err != nil {
		return nil, err
	}
	xiSigner, err := sign.NewSigner(xiScheme, xiKeys)
	if err != nil {
		return nil, err
	}
	cfKeys := sign.NewKeyring(sign.NewKey([]byte(cfKey), nil))
	cfV, err := sign.NewCloudflareVerifier(sign.CloudflareOptions{Keys: cfKeys})
	if err != nil {
		return nil, err
	}
	cfReq, err := sign.NewCloudflareVerifier(sign.CloudflareOptions{Keys: cfKeys, Required: true})
	if err != nil {
		return nil, err
	}
	cfSigner, err := sign.NewSigner(sign.NewCloudflareScheme(), cfKeys)
	if err != nil {
		return nil, err
	}
	defs := map[string]string{
		"thumbnail": "rs:fill:200:200/g:sm",
		"mobile":    "rs:fit:640:0/q:70",
	}
	store, err := preset.New(preset.Options{Definitions: defs})
	if err != nil {
		return nil, err
	}
	only, err := preset.New(preset.Options{Definitions: defs, Only: true})
	if err != nil {
		return nil, err
	}

	f := &fixture{parsers: map[string]dialect.Dialect{}, xi: xiSigner, cf: cfSigner}
	for name, o := range map[string]cloudflare.Options{
		"default": {},
		"strict":  {Strict: true},
		"xi":      {XIVerifier: xiV},
	} {
		o.Now = clock
		p, err := cloudflare.New(&o)
		if err != nil {
			return nil, err
		}
		f.parsers[name] = p
	}
	for name, o := range map[string]cloudflare.ImagesOptions{
		"images":          {Presets: store},
		"images_bare":     {},
		"images_strict":   {Presets: store, Strict: true},
		"images_accounts": {Presets: store, Accounts: map[string]string{unpicAccount: "cfimages"}},
		"images_signed":   {Presets: store, Verifier: cfV},
		"images_required": {Presets: store, Verifier: cfReq},
		"images_only":     {Presets: only},
	} {
		o.Now = clock
		p, err := cloudflare.NewImages(&o)
		if err != nil {
			return nil, err
		}
		f.parsers[name] = p
	}
	return f, nil
}

// testCase is one table row: path and raw query parsed with a named
// configuration.
type testCase struct {
	name  string
	cfg   string // "" = default
	path  string
	query string
}

func outcome(tc testCase, res *dialect.Result, err error) map[string]any {
	out := map[string]any{"path": tc.path}
	if tc.query != "" {
		out["query"] = tc.query
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
			return t.UTC().Format(time.RFC3339), true
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
	default:
		return v.Interface(), true
	}
}

func parse(tb testing.TB, cfg, path, query string) (*dialect.Result, error) {
	tb.Helper()
	if cfg == "" {
		cfg = "default"
	}
	p, ok := getFixture(tb).parsers[cfg]
	if !ok {
		tb.Fatalf("unknown config %q", cfg)
	}
	return p.Parse(&dialect.Input{Path: path, RawQuery: query})
}

// runGolden parses every case and checks it against
// testdata/golden/<group>.json (regenerate with -update).
func runGolden(t *testing.T, group string, cases []testCase) {
	t.Helper()
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
			res, err := parse(t, tc.cfg, tc.path, tc.query)
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
	if r.Dialect != cloudflare.Name && r.Dialect != cloudflare.ImagesName {
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
