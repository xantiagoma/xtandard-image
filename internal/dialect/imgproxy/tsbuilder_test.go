package imgproxy_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
)

// The cross-language fixture of the TypeScript URL builder
// (clients/typescript, WP-84). This test is its authority:
//
//  1. With -update it derives one case per successful golden table case
//     (WP-21a grammar, options, signing, jscore; WP-21b later; info): the
//     options objects (catalogue Decode of every option segment), the
//     source, its encoding and extension, the signature size and the parser
//     configuration. URLs already in the file are kept while their case is
//     unchanged.
//  2. `XI_UPDATE_FIXTURES=1 bun test` (clients/typescript) builds each case
//     with the TS builder and writes "url" (option names as given) and
//     "url_short" (the builder's default, shortest names). Without the
//     variable the TS test asserts the builder still produces those URLs.
//  3. Without -update this test checks that the cases still match the
//     golden tables and that every TS-built URL parses to the golden
//     outcome (request, source, signature) of the table case it came from.
const builderFixtureFile = "../../../testdata/urls/imgproxy-builder.json"

// builderGroups are the golden tables the fixture is derived from.
var builderGroups = []string{"grammar", "options", "signing", "jscore", "later", "info", "video"}

type builderFixture struct {
	Comment    string        `json:"comment"`
	Signing    fixtureKey    `json:"signing"`
	Encryption fixtureCipher `json:"encryption"`
	Cases      []builderCase `json:"cases"`
}

type fixtureKey struct {
	Key  string `json:"key"`
	Salt string `json:"salt"`
}

type fixtureCipher struct {
	Key string `json:"key"`
	IV  string `json:"iv"`
}

type builderCase struct {
	Name          string `json:"name"` // <group>/<golden case>
	Config        string `json:"config,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	Separator     string `json:"separator,omitempty"`
	SignatureSize int    `json:"signature_size,omitempty"` // 0 = unsigned
	Encoding      string `json:"encoding"`
	Source        string `json:"source"`
	Extension     string `json:"extension,omitempty"`
	// Pipelines holds, per chained pipeline, the option occurrences in URL
	// order, each a single-key object {name as written: typed arguments}.
	Pipelines [][]map[string][]any `json:"pipelines"`
	// Query marks a case the TS builder also writes in the query dialect
	// (/q/?url=…&o=… or /q/video?url=…&key=…, signed with xi_sig):
	// QueryURL must parse to the same request (canonical key) as URL.
	Query    bool   `json:"query,omitempty"`
	URL      string `json:"url,omitempty"`
	URLShort string `json:"url_short,omitempty"`
	QueryURL string `json:"query_url,omitempty"`
}

func (c *builderCase) input() builderCase {
	in := *c
	in.URL, in.URLShort, in.QueryURL = "", "", ""
	return in
}

// fixtureIV is the fixed IV the fixture encrypts enc/ sources with.
const fixtureIV = "0123456789abcdef"

// splitImgproxyPath splits an imgproxy path into its option pipelines and
// the source part, the way the parser does (no presets): after the
// signature, "/"-separated segments containing sep are options, "-" starts
// a pipeline, and the first other segment starts the source.
func splitImgproxyPath(path, sep string) (pipelines [][]occurrence, source string) {
	path, _, _ = strings.Cut(path, "?")
	_, rest, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	// A double slash after the signature is tolerated, as in redenormalizePath.
	rest = strings.TrimPrefix(rest, "/")
	pipelines = [][]occurrence{nil}
	for rest != "" {
		seg, tail, _ := strings.Cut(rest, "/")
		if seg == "-" {
			pipelines = append(pipelines, nil)
			rest = tail
			continue
		}
		seg = strings.ReplaceAll(seg, "%3A", ":")
		name, args, ok := strings.Cut(seg, sep)
		if !ok {
			break
		}
		last := len(pipelines) - 1
		pipelines[last] = append(pipelines[last], occurrence{name, strings.Split(args, sep)})
		rest = tail
	}
	return pipelines, rest
}

// sourceExtension returns the extension of a source part.
func sourceExtension(src string) string {
	if after, ok := strings.CutPrefix(src, "plain/"); ok {
		_, ext, _ := strings.Cut(after, "@")
		return ext
	}
	src = strings.TrimPrefix(src, "enc/")
	for {
		i := strings.Index(src, "ENC[")
		if i < 0 {
			break
		}
		j := strings.IndexByte(src[i:], ']')
		src = src[:i] + "X" + src[i+j+1:]
	}
	_, ext, _ := strings.Cut(strings.ReplaceAll(src, "/", ""), ".")
	return ext
}

type goldenOutcome struct {
	Path      string `json:"path"`
	Endpoint  string `json:"endpoint"`
	Config    string `json:"config"`
	Signature string `json:"signature"`
	Source    string `json:"source"`
	Request   struct {
		Source struct {
			Encoding string `json:"Encoding"`
		} `json:"Source"`
	} `json:"request"`
	Error json.RawMessage `json:"error"`
}

func readGolden(t *testing.T, group string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", group+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// optionIndex maps every name and alias to its catalogue entry, per
// endpoint: "" (processing), "info" and "video".
func optionIndex() map[string]map[string]*imgproxy.Option {
	idx := map[string]map[string]*imgproxy.Option{}
	for endpoint, opts := range map[string][]imgproxy.Option{
		"": imgproxy.ProcessingOptions(), "info": imgproxy.InfoOptions(), "video": imgproxy.VideoOptions(),
	} {
		idx[endpoint] = map[string]*imgproxy.Option{}
		for i := range opts {
			for _, n := range opts[i].Names() {
				idx[endpoint][n] = &opts[i]
			}
		}
	}
	return idx
}

// deriveBuilderCases derives the fixture cases from the golden tables.
func deriveBuilderCases(t *testing.T) []builderCase {
	t.Helper()
	idx := optionIndex()
	var out []builderCase
	for _, group := range builderGroups {
		golden := readGolden(t, group)
	cases:
		for _, name := range slices.Sorted(maps.Keys(golden)) {
			var g goldenOutcome
			if err := json.Unmarshal(golden[name], &g); err != nil {
				t.Fatal(err)
			}
			// Errors are not builder output; the filename layout and
			// trusted signatures are server configuration, not URLs a
			// builder makes.
			if g.Error != nil || g.Config == "filename" || g.Signature == "trusted" {
				continue
			}
			c := builderCase{
				Name: group + "/" + name, Config: g.Config, Endpoint: g.Endpoint,
				Source: g.Source, Encoding: g.Request.Source.Encoding,
			}
			if c.Encoding == "" {
				c.Encoding = "plain"
			}
			sep := ":"
			if g.Config == "sep" {
				sep, c.Separator = ",", ","
			}
			if g.Signature == "verified" {
				c.SignatureSize = 32
				if g.Config == "signed8" {
					c.SignatureSize = 8
				}
			}
			pipelines, src := splitImgproxyPath(g.Path, sep)
			for _, pl := range pipelines {
				if len(pl) == 0 {
					continue
				}
				entries := make([]map[string][]any, 0, len(pl))
				for _, occ := range pl {
					o, ok := idx[g.Endpoint][occ.name]
					if !ok {
						continue cases // unknown option: nothing a typed builder emits
					}
					entries = append(entries, map[string][]any{occ.name: o.Decode(occ.args)})
				}
				c.Pipelines = append(c.Pipelines, entries)
			}
			if c.Pipelines == nil {
				c.Pipelines = [][]map[string][]any{}
			}
			c.Extension = sourceExtension(src)
			if c.Endpoint == "info" && c.Extension != "" {
				continue // ignored by /info; the builder writes none there
			}
			c.Query = queryEligible(&c)
			out = append(out, c)
		}
	}
	return out
}

func readBuilderFixture(t *testing.T) *builderFixture {
	t.Helper()
	b, err := os.ReadFile(builderFixtureFile)
	if os.IsNotExist(err) {
		return &builderFixture{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var f builderFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func writeBuilderFixture(t *testing.T, f *builderFixture) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(builderFixtureFile, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonValue(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// canonicalIgnored rewrites the Ignored option names of an outcome to
// canonical names and drops duplicates, so URLs that spell an option
// differently compare equal.
func canonicalIgnored(o map[string]any, idx map[string]*imgproxy.Option) {
	req, _ := o["request"].(map[string]any)
	list, _ := req["Ignored"].([]any)
	if list == nil {
		return
	}
	seen := map[string]bool{}
	out := list[:0]
	for _, it := range list {
		m, _ := it.(map[string]any)
		name, _ := m["Option"].(string)
		if opt, ok := idx[name]; ok {
			m["Option"] = opt.Name
			name = opt.Name
		}
		reason, _ := m["Reason"].(string)
		key := name + "\x00" + reason
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	req["Ignored"] = out
}

func TestBuilderFixture(t *testing.T) {
	t.Parallel()
	derived := deriveBuilderCases(t)
	if len(derived) < 400 {
		t.Fatalf("derived only %d cases", len(derived))
	}
	fix := readBuilderFixture(t)
	if *update {
		old := map[string]builderCase{}
		for _, c := range fix.Cases {
			old[c.Name] = c
		}
		for i := range derived {
			if o, ok := old[derived[i].Name]; ok && cmp.Equal(jsonValue(t, o.input()), jsonValue(t, derived[i])) {
				derived[i].URL, derived[i].URLShort, derived[i].QueryURL = o.URL, o.URLShort, o.QueryURL
			}
		}
		writeBuilderFixture(t, &builderFixture{
			Comment: "Cross-language fixture of the TypeScript URL builder (clients/typescript). Cases: " +
				"`go test ./internal/dialect/imgproxy -run TestBuilderFixture -update`; URLs: " +
				"`XI_UPDATE_FIXTURES=1 bun test` in clients/typescript; check: go test (tsbuilder_test.go) and bun test.",
			Signing:    fixtureKey{Key: exampleKey, Salt: exampleSalt},
			Encryption: fixtureCipher{Key: encKey, IV: hex.EncodeToString([]byte(fixtureIV))},
			Cases:      derived,
		})
		return
	}

	const regen = "regenerate: go test ./internal/dialect/imgproxy -run TestBuilderFixture -update, then XI_UPDATE_FIXTURES=1 bun test in clients/typescript"
	if fix.Signing != (fixtureKey{Key: exampleKey, Salt: exampleSalt}) || fix.Encryption.Key != encKey ||
		fix.Encryption.IV != hex.EncodeToString([]byte(fixtureIV)) {
		t.Fatalf("fixture keys differ from the test fixture; %s", regen)
	}
	inputs := make([]builderCase, len(fix.Cases))
	for i := range fix.Cases {
		inputs[i] = fix.Cases[i].input()
	}
	if d := cmp.Diff(jsonValue(t, derived), jsonValue(t, inputs)); d != "" {
		t.Fatalf("fixture cases are stale (-derived +file):\n%s\n%s", d, regen)
	}

	f := getFixture(t)
	qf := queryParsers(t)
	idx := optionIndex()
	goldens := map[string]map[string]json.RawMessage{}
	for _, g := range builderGroups {
		goldens[g] = readGolden(t, g)
	}
	for _, c := range fix.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			if c.URL == "" || c.URLShort == "" {
				t.Fatalf("no TS-built url; %s", regen)
			}
			if c.Query {
				checkQueryURL(t, f, qf, &c, regen)
			}
			group, name, _ := strings.Cut(c.Name, "/")
			var want map[string]any
			if err := json.Unmarshal(goldens[group][name], &want); err != nil {
				t.Fatal(err)
			}
			delete(want, "path")
			cfg := c.Config
			if cfg == "" {
				cfg = "default"
			}
			info, video := c.Endpoint == "info", c.Endpoint == "video"
			for _, u := range []struct {
				url       string
				canonical bool
			}{{c.URL, false}, {c.URLShort, true}} {
				path := u.url
				if info || video {
					var ok bool
					if path, ok = strings.CutPrefix(path, "/"+c.Endpoint); !ok {
						t.Fatalf("%s url %q lacks /%s", c.Endpoint, u.url, c.Endpoint)
					}
				}
				tc := testCase{name: c.Name, cfg: c.Config, path: path, info: info, video: video}
				parse := f.parsers[cfg].Parse
				switch {
				case info:
					parse = f.parsers[cfg].ParseInfo
				case video:
					parse = f.parsers[cfg].ParseVideo
				}
				res, err := parse(&dialect.Input{Path: path})
				got, _ := jsonValue(t, outcome(tc, res, err)).(map[string]any)
				delete(got, "path")
				w, _ := jsonValue(t, want).(map[string]any) // a fresh copy per URL
				if u.canonical {
					canonicalIgnored(w, idx[c.Endpoint])
					canonicalIgnored(got, idx[c.Endpoint])
				}
				if d := cmp.Diff(w, got); d != "" {
					t.Errorf("%s parses differently from the table case (-golden +got):\n%s", u.url, d)
				}
			}
		})
	}
}
