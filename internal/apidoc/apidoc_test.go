package apidoc

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/admin"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/server"
)

var update = flag.Bool("update", false, "rewrite openapi.json")

// TestGolden fails on a stale openapi.json; go test -run TestGolden
// -update rewrites it.
func TestGolden(t *testing.T) {
	got, err := Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("Marshal is not deterministic")
	}
	if *update {
		if err := os.WriteFile("openapi.json", got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("openapi.json is stale; run go test ./internal/apidoc -run TestGolden -update (-committed +generated):\n%s", diff)
	}
	if !bytes.Equal(Spec(), want) {
		t.Error("Spec() differs from the committed openapi.json")
	}
}

// document is the committed spec, decoded generically.
type document struct {
	OpenAPI    string                                `json:"openapi"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

type operation struct {
	OperationID string `json:"operationId"`
	Listener    string `json:"x-xi-listener"`
	Parameters  []struct {
		Name   string `json:"name"`
		In     string `json:"in"`
		Schema struct {
			Enum     []string `json:"enum"`
			Examples []string `json:"examples"`
		} `json:"schema"`
		Example string `json:"example"`
	} `json:"parameters"`
	Responses map[string]json.RawMessage `json:"responses"`
}

func loadDoc(t *testing.T) *document {
	t.Helper()
	var d document
	if err := json.Unmarshal(Spec(), &d); err != nil {
		t.Fatal(err)
	}
	if d.OpenAPI != "3.1.0" {
		t.Fatalf("openapi %q, want 3.1.0", d.OpenAPI)
	}
	return &d
}

type op struct {
	method, path string
	operation
}

func (d *document) operations(t *testing.T) []op {
	t.Helper()
	var ops []op
	for path, item := range d.Paths {
		for method, raw := range item {
			var o operation
			if err := json.Unmarshal(raw, &o); err != nil {
				t.Fatal(err)
			}
			ops = append(ops, op{strings.ToUpper(method), path, o})
		}
	}
	slices.SortFunc(ops, func(a, b op) int { return strings.Compare(a.path+a.method, b.path+b.method) })
	return ops
}

// samplePaths expands an OpenAPI path template into request paths, one
// per enum value or example of each path parameter.
func samplePaths(o *op) []string {
	paths := []string{o.path}
	for _, p := range o.Parameters {
		if p.In != "path" {
			continue
		}
		values := append(append(slices.Clone(p.Schema.Enum), p.Schema.Examples...), p.Example)
		values = slices.DeleteFunc(values, func(s string) bool { return s == "" })
		var next []string
		for _, path := range paths {
			for _, v := range values {
				next = append(next, strings.ReplaceAll(path, "{"+p.Name+"}", v))
			}
		}
		paths = next
	}
	return paths
}

// TestRouteCoverage maps every registered public and admin pattern to a
// documented operation and every operation to a registered pattern on its
// listener (ADR 0008 decision 2.2).
func TestRouteCoverage(t *testing.T) {
	t.Parallel()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	public := http.NewServeMux()
	for _, p := range server.Patterns() {
		public.Handle(p, noop)
	}
	adminOpts := admin.Options{
		Metrics: noop, ConfigSchema: []byte("{}"), OpenAPI: []byte("{}"), Docs: true, Pprof: true,
		Playground: noop, PlaygroundPath: playground.DefaultPath,
	}
	adminMux := admin.NewMux(&adminOpts)
	muxes := map[string]*http.ServeMux{ListenerPublic: public, ListenerAdmin: adminMux}
	hit := map[string]map[string]bool{ListenerPublic: {}, ListenerAdmin: {}}

	ops := loadDoc(t).operations(t)
	if len(ops) < 10 {
		t.Fatalf("only %d operations", len(ops))
	}
	for i := range ops {
		o := &ops[i]
		var listeners []string
		switch o.Listener {
		case ListenerPublic, ListenerAdmin:
			listeners = []string{o.Listener}
		case ListenerBoth:
			listeners = []string{ListenerPublic, ListenerAdmin}
		default:
			t.Errorf("%s %s: x-xi-listener %q", o.method, o.path, o.Listener)
			continue
		}
		for _, path := range samplePaths(o) {
			for _, l := range listeners {
				req := httptest.NewRequestWithContext(t.Context(), o.method, path, http.NoBody)
				_, pattern := muxes[l].Handler(req)
				if pattern == "" {
					t.Errorf("%s %s (%s): no %s route", o.method, path, o.OperationID, l)
					continue
				}
				hit[l][pattern] = true
			}
			if o.Listener == ListenerAdmin {
				// Admin documents never get a public route of their own.
				req := httptest.NewRequestWithContext(t.Context(), o.method, path, http.NoBody)
				if _, pattern := public.Handler(req); pattern != "" && pattern != server.PatternImage {
					t.Errorf("admin %s %s has the public route %q", o.method, path, pattern)
				}
			}
		}
	}
	for l, patterns := range map[string][]string{ListenerPublic: server.Patterns(), ListenerAdmin: admin.Patterns(&adminOpts)} {
		for _, p := range patterns {
			if !hit[l][p] {
				t.Errorf("%s pattern %q has no documented operation", l, p)
			}
		}
	}
}

// schemaValidator compiles the committed document's component schema name.
func schemaValidator(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Spec()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	if err := c.AddResource("mem://openapi.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("mem://openapi.json#/components/schemas/" + name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

func validateJSON(t *testing.T, s *jsonschema.Schema, b []byte) error {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(v)
}

// TestContractInfo validates the real /info bodies of every fixture
// format and of every WP-63 Pro option (testdata/info and testdata/info/pro,
// written by internal/app's cgo info test) against InfoResponse.
func TestContractInfo(t *testing.T) {
	t.Parallel()
	s := schemaValidator(t, schemaInfoResponse)
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "info", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	pro, err := filepath.Glob(filepath.Join("..", "..", "testdata", "info", "pro", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pro) == 0 {
		t.Fatal("no Pro info bodies")
	}
	files = append(files, pro...)
	if len(files) < 20 {
		t.Fatalf("only %d info bodies", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateJSON(t, s, b); err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
	for _, bad := range []string{`{"width":"10"}`, `{"orientation":9}`, `{"unknown":1}`, `{"exif":{"a":1}}`} {
		if err := validateJSON(t, s, []byte(bad)); err == nil {
			t.Errorf("%s validates, want a schema error", bad)
		}
	}
}

// TestContractErrors validates the error body of every errs.Kind, as the
// server writes it, against ErrorResponse, and checks every kind's status
// is a documented response of the image and info operations.
func TestContractErrors(t *testing.T) {
	t.Parallel()
	s := schemaValidator(t, schemaErrorResponse)
	for _, k := range errs.Kinds() {
		for _, id := range []string{"", "01J9Z"} {
			b, err := json.Marshal(server.NewErrorResponse(k, errs.PublicMessage(k), id))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateJSON(t, s, b); err != nil {
				t.Errorf("%v: %s: %v", k, b, err)
			}
		}
	}
	if err := validateJSON(t, s, []byte(`{"error":{"kind":"nope","message":"x"}}`)); err == nil {
		t.Error("an unknown kind validates")
	}

	d := loadDoc(t)
	for _, o := range d.operations(t) {
		if o.OperationID != "getImgproxyImage" && o.OperationID != "getImgproxyInfo" {
			continue
		}
		for _, k := range errs.Kinds() {
			st := errs.Status(k)
			if st == errs.StatusClientClosedRequest {
				continue
			}
			if _, ok := o.Responses[strconv.Itoa(st)]; !ok {
				t.Errorf("%s lacks the %d response of kind %v", o.OperationID, st, k)
			}
		}
	}
}

// TestContractHealth validates real /healthz and /readyz bodies against
// the documented text patterns.
func TestContractHealth(t *testing.T) {
	t.Parallel()
	type content struct {
		Schema struct {
			Pattern string `json:"pattern"`
		} `json:"schema"`
	}
	type response struct {
		Content map[string]content `json:"content"`
	}
	d := loadDoc(t)
	pattern := func(path, status string) *regexp.Regexp {
		var o struct {
			Responses map[string]response `json:"responses"`
		}
		if err := json.Unmarshal(d.Paths[path]["get"], &o); err != nil {
			t.Fatal(err)
		}
		return regexp.MustCompile(o.Responses[status].Content["text/plain"].Schema.Pattern)
	}
	ready := admin.NewReadiness()
	mux := admin.NewMux(&admin.Options{Readiness: ready})
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
		return rec
	}
	check := func(path string) {
		rec := get(path)
		if re := pattern(path, strconv.Itoa(rec.Code)); !re.MatchString(rec.Body.String()) {
			t.Errorf("%s %d body %q does not match %s", path, rec.Code, rec.Body.String(), re)
		}
	}
	check("/healthz")
	check("/readyz") // starting
	ready.SetReady()
	check("/readyz")
	ready.SetDraining()
	check("/readyz")
}

// TestNoHumaGlobals enforces ADR 0008: huma package-level variables are
// never assigned anywhere in the module.
func TestNoHumaGlobals(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`huma\.(NewError|NewErrorWithContext|ErrorFormatter|DefaultArrayNullable|ValidateStrictCasing|DefaultFormats)\s*=[^=]`)
	fsys := os.DirFS(filepath.Join("..", ".."))
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != "." && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		if re.Match(b) {
			t.Errorf("%s assigns a huma package variable", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
