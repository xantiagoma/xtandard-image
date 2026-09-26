package bunny_test

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
	"github.com/xantiagoma/xtandard-image/internal/dialect/bunny"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json")

// bunnyKey is the key of Bunny's SDK test vectors
// (BunnyWay/BunnyCDN.TokenAuthentication e2e/inputs.json).
const bunnyKey = "SecurityKey"

// now is the fixed clock: before the SDK vectors' expiry (1598024587,
// 2020-08-21T15:43:07Z) so they verify; later expiries test the 404.
var now = time.Date(2020, 8, 21, 12, 0, 0, 0, time.UTC)

// mountSource is the named source the test mount binds.
const mountSource = "zone"

type fixture struct {
	parsers map[string]*bunny.Parser
	hs256   *sign.Signer
	sha256  *sign.Signer
	basic   *sign.Signer
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
	keys := sign.NewKeyring(sign.NewKey([]byte(bunnyKey), nil))
	optional, err := sign.NewBunnyVerifier(sign.BunnyOptions{Keys: keys})
	if err != nil {
		return nil, err
	}
	required, err := sign.NewBunnyVerifier(sign.BunnyOptions{Keys: keys, Required: true})
	if err != nil {
		return nil, err
	}
	hsOnly, err := sign.NewBunnyVerifier(sign.BunnyOptions{Keys: keys, Layouts: sign.BunnyHS256})
	if err != nil {
		return nil, err
	}
	defs := map[string]string{"thumbnail": "rs:fit:300:200/q:85/sh:1", "hero": "w:1200"}
	presets, err := preset.New(preset.Options{Definitions: defs})
	if err != nil {
		return nil, err
	}
	onlyPresets, err := preset.New(preset.Options{Definitions: defs, Only: true})
	if err != nil {
		return nil, err
	}
	withDefault, err := preset.New(preset.Options{Definitions: map[string]string{"default": "q:70", "hero": "w:1200"}})
	if err != nil {
		return nil, err
	}
	configs := map[string]bunny.Options{
		"default":      {},
		"strict":       {Strict: true},
		"signed":       {Verifier: optional},
		"required":     {Verifier: required},
		"hs256_only":   {Verifier: hsOnly},
		"presets":      {Presets: presets},
		"only_presets": {Presets: onlyPresets},
		"default_pre":  {Presets: withDefault},
	}
	f := &fixture{parsers: map[string]*bunny.Parser{}}
	for name, o := range configs {
		o.Now = func() time.Time { return now }
		p, err := bunny.New(&o)
		if err != nil {
			return nil, err
		}
		f.parsers[name] = p
	}
	if f.hs256, err = sign.NewSigner(sign.NewBunnyHS256Scheme(), keys); err != nil {
		return nil, err
	}
	if f.sha256, err = sign.NewSigner(sign.NewBunnySHA256Scheme(), keys); err != nil {
		return nil, err
	}
	f.basic, err = sign.NewSigner(sign.NewBunnyBasicScheme(), keys)
	return f, err
}

// testCase is one table row: path and raw query parsed with a named
// configuration.
type testCase struct {
	name  string
	cfg   string // "" = default
	path  string
	query string
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
	return p.Parse(&dialect.Input{Path: path, RawQuery: query, Mount: dialect.Mount{Name: "zone", Source: mountSource}})
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
	if r.Dialect != bunny.Name {
		t.Errorf("Dialect = %q", r.Dialect)
	}
	if res.SourceString == "" || res.SourceString != r.Source.Raw || r.Source.Mount != mountSource {
		t.Errorf("SourceString %q, Source %+v", res.SourceString, r.Source)
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
