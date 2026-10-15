package apidoc

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
)

// clientSpecFile is the language-neutral client specification (ADR 0013)
// that every official client's generator reads.
const clientSpecFile = "../../clients/spec/spec.json"

// TestClientSpec fails when clients/spec/spec.json drifts from the
// catalogues; go test ./internal/apidoc -run TestClientSpec -update (task
// clients:spec) rewrites it. Then `task clients:generate` regenerates the
// clients' option tables.
func TestClientSpec(t *testing.T) {
	got, err := MarshalClientSpec()
	if err != nil {
		t.Fatal(err)
	}
	again, err := MarshalClientSpec()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("MarshalClientSpec is not deterministic")
	}
	if *update {
		if err := os.WriteFile(clientSpecFile, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(clientSpecFile)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(string(want), string(got)); d != "" {
		t.Errorf("%s is stale; run go test ./internal/apidoc -run TestClientSpec -update, then task clients:generate (-committed +generated):\n%s",
			clientSpecFile, d)
	}
}

// TestClientSpecCoversCatalogues checks the spec's content against the
// catalogues directly, so a bug in the exporter cannot hide behind -update:
// every name and alias of the three catalogues, each option's short name
// among its names, and the query keys of the OpenAPI document.
func TestClientSpecCoversCatalogues(t *testing.T) {
	t.Parallel()
	s, err := BuildClientSpec()
	if err != nil {
		t.Fatal(err)
	}
	names := func(opts []imgproxy.Option) []string {
		var out []string
		for i := range opts {
			out = append(out, opts[i].Names()...)
		}
		slices.Sort(out)
		return out
	}
	specNames := func(opts []SpecOption) []string {
		var out []string
		for i := range opts {
			out = append(out, opts[i].Name)
			out = append(out, opts[i].Aliases...)
			if !slices.Contains(append([]string{opts[i].Name}, opts[i].Aliases...), opts[i].Short) {
				t.Errorf("option %s: short name %q is not one of its names", opts[i].Name, opts[i].Short)
			}
		}
		slices.Sort(out)
		return out
	}
	for _, tc := range []struct {
		kind string
		cat  []imgproxy.Option
		spec []SpecOption
	}{
		{"processing", imgproxy.ProcessingOptions(), s.Options.Processing},
		{"info", imgproxy.InfoOptions(), s.Options.Info},
		{"video", imgproxy.VideoOptions(), s.Options.Video},
	} {
		if d := cmp.Diff(names(tc.cat), specNames(tc.spec)); d != "" {
			t.Errorf("%s option names (-catalogue +spec):\n%s", tc.kind, d)
		}
	}

	var keys []string
	for _, k := range s.Query.ImageKeys {
		keys = append(keys, k.Name)
		keys = append(keys, k.Aliases...)
	}
	keys = append(keys, s.Query.BuilderKeys...)
	slices.Sort(keys)
	var documented []string
	for _, kv := range queryKeys() {
		documented = append(documented, kv[0])
	}
	slices.Sort(documented)
	if d := cmp.Diff(documented, keys); d != "" {
		t.Errorf("query keys (-openapi +spec):\n%s", d)
	}

	// The response schemas are copies of the OpenAPI components.
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(Spec(), &doc); err != nil {
		t.Fatal(err)
	}
	for name := range s.Responses {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Errorf("response schema %s is not an OpenAPI component", name)
		}
	}
	for _, want := range []string{schemaInfoResponse, schemaErrorResponse, schemaErrorBody} {
		if _, ok := s.Responses[want]; !ok {
			t.Errorf("response schemas lack %s", want)
		}
	}
}
