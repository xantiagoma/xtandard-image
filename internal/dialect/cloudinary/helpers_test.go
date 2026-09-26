package cloudinary_test

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

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json")

// apiSecret is the secret the corpus generator signs with
// (testdata/urlgen/generate.ts).
const apiSecret = "a676b67565c6767a6767d6767f676fe1" //nolint:gosec // public test secret of the corpus

// mountSource is the source of the test mount; the demo cloud maps to
// "assets".
const mountSource = "mounted"

type fixture struct {
	parsers map[string]*cloudinary.Parser
	signers map[string]*sign.Signer // "sha1", "sha256", "long"
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

func verifier(o sign.CloudinaryOptions) (sign.Verifier, error) {
	o.Keys = sign.NewKeyring(sign.NewKey([]byte(apiSecret), nil))
	return sign.NewCloudinaryVerifier(o)
}

func buildFixture() (*fixture, error) {
	clouds := map[string]string{"demo": "assets", "other": "other_assets"}
	presets, err := preset.New(preset.Options{Definitions: map[string]string{
		"thumb":       "rs:fill:150:150/g:sm",
		"watermarked": "bl:2",
		"chained":     "w:400/-/bl:3",
	}})
	if err != nil {
		return nil, err
	}
	only, err := preset.New(preset.Options{Definitions: map[string]string{"thumb": "rs:fill:150:150"}, Only: true})
	if err != nil {
		return nil, err
	}
	withDefault, err := preset.New(preset.Options{Definitions: map[string]string{"default": "q:70/sm:0"}})
	if err != nil {
		return nil, err
	}
	sha1V, err := verifier(sign.CloudinaryOptions{})
	if err != nil {
		return nil, err
	}
	sha256V, err := verifier(sign.CloudinaryOptions{ShortSHA256: true})
	if err != nil {
		return nil, err
	}
	requiredV, err := verifier(sign.CloudinaryOptions{Required: true})
	if err != nil {
		return nil, err
	}
	longOnlyV, err := verifier(sign.CloudinaryOptions{LongOnly: true})
	if err != nil {
		return nil, err
	}
	configs := map[string]cloudinary.Options{
		"default":  {Clouds: clouds},
		"nomap":    {},
		"strict":   {Clouds: clouds, Strict: true},
		"presets":  {Clouds: clouds, Presets: presets},
		"only":     {Clouds: clouds, Presets: only},
		"defpre":   {Clouds: clouds, Presets: withDefault},
		"signed":   {Clouds: clouds, Verifier: sha1V},
		"sha256":   {Clouds: clouds, Verifier: sha256V},
		"required": {Clouds: clouds, Verifier: requiredV},
		"longonly": {Clouds: clouds, Verifier: longOnlyV},
	}
	f := &fixture{parsers: map[string]*cloudinary.Parser{}, signers: map[string]*sign.Signer{}}
	for name, o := range configs {
		p, err := cloudinary.New(&o)
		if err != nil {
			return nil, err
		}
		f.parsers[name] = p
	}
	keys := sign.NewKeyring(sign.NewKey([]byte(apiSecret), nil))
	for name, s := range map[string]sign.Scheme{
		"sha1":   sign.NewCloudinaryScheme(false, false),
		"sha256": sign.NewCloudinaryScheme(false, true),
		"long":   sign.NewCloudinaryScheme(true, false),
	} {
		if f.signers[name], err = sign.NewSigner(s, keys); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// testCase is one table row: a path parsed with a named configuration.
type testCase struct {
	name string
	cfg  string // "" = default
	path string
}

func outcome(tc testCase, res *dialect.Result, err error) map[string]any {
	out := map[string]any{"path": tc.path}
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
	default:
		return v.Interface(), true
	}
}

func parse(tb testing.TB, cfg, path string) (*dialect.Result, error) {
	tb.Helper()
	if cfg == "" {
		cfg = "default"
	}
	p, ok := getFixture(tb).parsers[cfg]
	if !ok {
		tb.Fatalf("unknown config %q", cfg)
	}
	return p.Parse(&dialect.Input{Path: path, Mount: dialect.Mount{Source: mountSource}})
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
			res, err := parse(t, tc.cfg, tc.path)
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
	if r.Dialect != cloudinary.Name {
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
