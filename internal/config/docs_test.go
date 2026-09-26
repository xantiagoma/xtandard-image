package config

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/caarlos0/env/v11"
)

const configDoc = "../../docs/CONFIG.md"

// docVar is one variable documented in a CONFIG.md §2 table.
type docVar struct {
	name    string // may contain <NAME> or <FMT>
	def     string // documented default literal
	hasDef  bool   // def is a single backticked literal
	defCell string // the whole Default cell
	meaning string // the Meaning cell
	rowLine int
}

func readDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(configDoc)
	if err != nil {
		t.Fatalf("read %s: %v", configDoc, err)
	}
	return string(b)
}

// section returns the text between a heading starting with from and the next
// heading of the same level.
func section(t *testing.T, doc, from string) string {
	t.Helper()
	_, rest, ok := strings.Cut(doc, "\n"+from)
	if !ok {
		t.Fatalf("section %q not found", from)
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// parseDocVars reads every XI_ row of the §2 tables, expanding the "X / _Y"
// and "X, _Y" shorthands (_Y replaces the last _-segment of X).
func parseDocVars(t *testing.T) []docVar {
	t.Helper()
	tick := regexp.MustCompile("`([^`]*)`")
	var out []docVar
	for i, line := range strings.Split(section(t, readDoc(t), "## 2."), "\n") {
		if !strings.HasPrefix(line, "| `XI_") {
			continue
		}
		cells := strings.Split(strings.ReplaceAll(line, `\|`, "\x00"), "|")
		if len(cells) < 4 {
			t.Fatalf("CONFIG.md §2 line %d: malformed row %q", i, line)
		}
		var names []string
		for _, m := range tick.FindAllStringSubmatch(cells[1], -1) {
			n := m[1]
			if strings.HasPrefix(n, "_") {
				first := names[0]
				n = first[:strings.LastIndex(first, "_")] + n
			}
			names = append(names, n)
		}
		defCell := strings.TrimSpace(strings.ReplaceAll(cells[2], "\x00", "|"))
		var defs []string
		literal := strings.HasPrefix(defCell, "`")
		if literal {
			for part := range strings.SplitSeq(defCell, " / ") {
				m := tick.FindStringSubmatch(part)
				if m == nil {
					t.Fatalf("CONFIG.md §2 line %d: default %q is not a backticked literal", i, part)
				}
				defs = append(defs, m[1])
			}
			if len(defs) != 1 && len(defs) != len(names) {
				t.Fatalf("CONFIG.md §2 line %d: %d defaults for %d variables", i, len(defs), len(names))
			}
		}
		for j, n := range names {
			v := docVar{
				name: n, rowLine: i, hasDef: literal, defCell: defCell,
				meaning: strings.TrimSpace(strings.ReplaceAll(cells[3], "\x00", "|")),
			}
			if literal {
				v.def = defs[min(j, len(defs)-1)]
			}
			out = append(out, v)
		}
	}
	if len(out) < 100 {
		t.Fatalf("parsed only %d variables from CONFIG.md; table format changed?", len(out))
	}
	return out
}

// codeVars maps every variable the loader knows to its field (nil for the
// loader's own variables). Source fields use the literal name <NAME>.
func codeVars() map[string]*field {
	out := map[string]*field{envConfigFile: nil, envAllowUnknown: nil, envSources: nil}
	for _, f := range append(staticFields(), sourceFields("<name>")...) {
		out[f.Env] = &f
	}
	return out
}

func docPattern(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	q = strings.NewReplacer("<NAME>", "[A-Z0-9_]+", "<FMT>", "[A-Z0-9]+").Replace(q)
	return regexp.MustCompile("^" + q + "$")
}

func (d *docVar) matches(key string) bool {
	if d.name == key {
		return true
	}
	if strings.HasPrefix(d.name, envSources+"_<NAME>_") || !strings.Contains(d.name, "<") {
		return false // source rows compare literally
	}
	return docPattern(d.name).MatchString(key)
}

// TestDocumentedVariablesMatchCode parses the CONFIG.md tables: every
// documented variable is parsed by the loader and every variable the loader
// parses is documented.
func TestDocumentedVariablesMatchCode(t *testing.T) {
	t.Parallel()
	docs := parseDocVars(t)
	code := codeVars()
	for _, d := range docs {
		if !slices.ContainsFunc(slices.Collect(mapKeys(code)), d.matches) {
			t.Errorf("CONFIG.md documents %s but internal/config does not parse it", d.name)
		}
	}
	for key := range code {
		if !slices.ContainsFunc(docs, func(d docVar) bool { return d.matches(key) }) {
			t.Errorf("internal/config parses %s but CONFIG.md §2 does not document it", key)
		}
	}
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// TestDocumentedDefaultsMatchCode checks each single-literal default in
// CONFIG.md: setting the variable to its documented default must not
// change the loaded config.
func TestDocumentedDefaultsMatchCode(t *testing.T) {
	t.Parallel()
	derived := map[string]string{ // documented as a formula; covered by TestDerivedDefaults
		"XI_PROCESSING_WORKERS":    "GOMAXPROCS",
		"XI_PROCESSING_QUEUE_SIZE": "workers×16",
		"XI_SIGNING_REQUIRED":      "true when keys set",
	}
	base := mustParse(t, nil)
	code := codeVars()
	checked := 0
	for _, d := range parseDocVars(t) {
		f, ok := code[d.name]
		// Source fields cannot be set without a source; their defaults are
		// the zero value or depend on the type.
		if !d.hasDef || !ok || f == nil || derived[d.name] != "" || strings.HasPrefix(d.name, envSources+"_<NAME>_") {
			continue
		}
		checked++
		if d.def == "" {
			if v := reflect.ValueOf(base).FieldByIndex(f.Index); !v.IsZero() && (v.Kind() != reflect.Slice || v.Len() != 0) {
				t.Errorf("%s: CONFIG.md default is empty, code default is %v", d.name, v.Interface())
			}
			continue
		}
		got, _, err := load(opts(nil, d.name+"="+d.def))
		if err != nil {
			t.Errorf("%s: documented default %q does not parse: %v", d.name, d.def, err)
			continue
		}
		if dd := diff(base, got); dd != "" {
			t.Errorf("%s: documented default %q differs from the code default (-code +doc):\n%s", d.name, d.def, dd)
		}
	}
	if checked < 80 {
		t.Errorf("checked only %d defaults", checked)
	}
}

// TestMappingRule proves env name == XI_ + upper(YAML path) for every field.
func TestMappingRule(t *testing.T) {
	t.Parallel()
	for _, f := range append(staticFields(), sourceFields("my-src")...) {
		if want := envNameOf(f.YAML); f.Env != want {
			t.Errorf("%s: env %s, want %s by the mapping rule", f.YAML, f.Env, want)
		}
	}
}

// TestFieldWalkerMatchesLibrary proves fieldsOf computes the same names as
// caarlos0/env, which is what unknown-variable detection relies on.
func TestFieldWalkerMatchesLibrary(t *testing.T) {
	t.Parallel()
	params, err := env.GetFieldParamsWithOptions(&Config{}, env.Options{Prefix: envPrefix})
	if err != nil {
		t.Fatal(err)
	}
	var lib []string
	for _, p := range params {
		if p.OwnKey != "" {
			lib = append(lib, p.Key)
		}
	}
	fields := staticFields()
	ours := make([]string, 0, len(fields))
	for _, f := range fields {
		ours = append(ours, f.Env)
	}
	slices.Sort(lib)
	slices.Sort(ours)
	if d := diff(lib, ours); d != "" {
		t.Errorf("walker vs caarlos0/env (-lib +ours):\n%s", d)
	}
}

// TestDocExamples loads the YAML example of CONFIG.md §3 and the Dokploy
// environment of §4, so the documented examples stay valid.
func TestDocExamples(t *testing.T) {
	t.Parallel()
	doc := readDoc(t)
	codeBlock := func(sec, lang string) string {
		_, rest, ok := strings.Cut(section(t, doc, sec), "```"+lang+"\n")
		if !ok {
			t.Fatalf("no %s block in %s", lang, sec)
		}
		block, _, _ := strings.Cut(rest, "```")
		return block
	}

	t.Run("yaml", func(t *testing.T) {
		t.Parallel()
		cfg, _, err := Load(opts(files{"/c.yaml": codeBlock("## 3.", "yaml")}, "XI_CONFIG_FILE=/c.yaml"))
		if err != nil {
			t.Fatalf("CONFIG.md §3 example: %v", err)
		}
		if cfg.Sources["assets"].Bucket != "assets" || !cfg.Dialects.Imgix.Enabled || cfg.Presets["thumb"] == "" {
			t.Errorf("example not applied: %s", cfg.Redacted())
		}
	})
	t.Run("dokploy env", func(t *testing.T) {
		t.Parallel()
		var environ []string
		for line := range strings.SplitSeq(codeBlock("## 4.", ""), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				environ = append(environ, strings.ReplaceAll(line, "<hex>", "00112233445566778899aabbccddeeff"))
			}
		}
		if _, _, err := Load(opts(nil, environ...)); err != nil {
			t.Fatalf("CONFIG.md §4 environment: %v", err)
		}
	})
}

// imgproxyDocRow renders one row of the CONFIG.md imgproxy table.
func imgproxyDocRow(iv imgproxyVar) string {
	targets := "—"
	if len(iv.Targets) > 0 {
		targets = "`" + strings.Join(iv.Targets, "`, `") + "`"
	}
	return "| `" + iv.Name + "` | " + targets + " | " + iv.Note + " |"
}

// TestDocumentedImgproxyVars checks the CONFIG.md imgproxy table row by row
// against imgproxyVars (names, targets, notes and order), and that every
// target is a variable the loader parses.
func TestDocumentedImgproxyVars(t *testing.T) {
	t.Parallel()
	_, sec, ok := strings.Cut(section(t, readDoc(t), "## 2."), "\n### imgproxy variables\n")
	if !ok {
		t.Fatal(`CONFIG.md §2 has no "### imgproxy variables" section`)
	}
	var doc []string
	for line := range strings.SplitSeq(sec, "\n") {
		if strings.HasPrefix(line, "### ") {
			break
		}
		if strings.HasPrefix(line, "| `IMGPROXY_") {
			doc = append(doc, line)
		}
	}
	vars := imgproxyVars()
	code := make([]string, 0, len(vars))
	for _, iv := range vars {
		code = append(code, imgproxyDocRow(iv))
	}
	if d := diff(code, doc); d != "" {
		t.Errorf("CONFIG.md imgproxy table differs from imgproxyVars (-code +doc):\n%s", d)
	}

	known := codeVars()
	for _, name := range []string{compatSourceHTTP, compatSourceS3, compatSourceGCS, compatSourceABS, compatSourceLocal} {
		for _, f := range sourceFields(name) {
			known[f.Env] = &f
		}
	}
	for _, iv := range vars {
		for _, tg := range iv.Targets {
			if _, ok := known[tg]; !ok {
				t.Errorf("%s targets %s, which the loader does not parse", iv.Name, tg)
			}
		}
	}
}
