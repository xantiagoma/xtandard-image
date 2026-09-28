package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// The user documentation's examples (docs/examples, docs/SOURCES.md and
// the README) go through the real loader here, so they cannot rot.

const (
	examplesDir = "../../docs/examples"
	sourcesDoc  = "../../docs/SOURCES.md"
	readmeDoc   = "../../README.md"
)

// fakeFS reads every absolute path as a fake secret (hex, which is
// also a valid secret string) unless strict lists the files that exist.
type fakeFS struct {
	files  files
	strict bool
}

func (f fakeFS) read(name string) ([]byte, error) {
	if v, ok := f.files[name]; ok {
		return []byte(v), nil
	}
	if !f.strict && filepath.IsAbs(name) && !strings.HasPrefix(name, defaultConfigDir+"/config.") {
		return []byte(testHexKey + "\n"), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func loadWith(t *testing.T, f fakeFS, configFile string, environ []string) Config {
	t.Helper()
	cfg, _, err := Load(LoadOptions{Environ: environ, ConfigFile: configFile, ReadFile: f.read, GOMAXPROCS: 4})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(examplesDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// placeholders replaces the documentation's placeholders with values the
// loader accepts: <hex> is `openssl rand -hex 32`.
func placeholders(s string) string {
	return strings.ReplaceAll(s, "<hex>", testHexKey)
}

// parseEnvLines reads KEY=value lines (dotenv / --env-file syntax without
// quoting); blank lines and # comments are skipped.
func parseEnvLines(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			t.Fatalf("env line %q is not KEY=value", line)
		}
		out = append(out, placeholders(line))
	}
	return out
}

// TestExampleConfigFiles loads every docs/examples config file, checks it
// against the JSON Schema, and checks that the YAML and TOML multi-source
// examples are the same configuration and cover every source type.
func TestExampleConfigFiles(t *testing.T) {
	t.Parallel()
	sch := compileSchema(t, Schema())
	names, err := filepath.Glob(filepath.Join(examplesDir, "config.*"))
	if err != nil || len(names) < 2 {
		t.Fatalf("config examples: %v %v", names, err)
	}
	loaded := map[string]Config{}
	for _, p := range names {
		name := filepath.Base(p)
		b := readExample(t, name)
		if err := sch.Validate(fileAsJSON(t, name, b)); err != nil {
			t.Errorf("%s: schema: %v", name, err)
		}
		virtual := "/etc/xtandard/" + name
		loaded[name] = loadWith(t, fakeFS{files: files{virtual: string(b)}}, virtual, nil)
	}
	yml, toml := loaded["config.multi-source.yaml"], loaded["config.multi-source.toml"]
	if d := diff(yml, toml); d != "" {
		t.Errorf("config.multi-source.yaml and .toml differ (-yaml +toml):\n%s", d)
	}
	types := map[SourceType]bool{}
	for _, s := range yml.Sources {
		types[s.Type] = true
	}
	for _, typ := range []SourceType{SourceHTTP, SourceS3, SourceGCS, SourceAzure, SourceFS, SourceFTP, SourceSFTP} {
		if !types[typ] {
			t.Errorf("config.multi-source.yaml has no %s source", typ)
		}
	}
}

// TestExampleEnvFiles loads every docs/examples *.env file as the whole
// environment.
func TestExampleEnvFiles(t *testing.T) {
	t.Parallel()
	names, err := filepath.Glob(filepath.Join(examplesDir, "*.env"))
	if err != nil || len(names) == 0 {
		t.Fatalf("env examples: %v %v", names, err)
	}
	for _, p := range names {
		cfg := loadWith(t, fakeFS{}, "", parseEnvLines(t, string(readExample(t, filepath.Base(p)))))
		if len(cfg.Sources) < 5 {
			t.Errorf("%s: only %d sources", filepath.Base(p), len(cfg.Sources))
		}
	}
}

// composeInterp matches ${VAR}, ${VAR:-default}, ${VAR-default},
// ${VAR:?message} and ${VAR?message}.
var composeInterp = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?])([^}]*))?\}`)

// interpolate expands compose variables: a default is used as is; a
// required or bare variable gets a placeholder value.
func interpolate(s string) string {
	return composeInterp.ReplaceAllStringFunc(s, func(m string) string {
		sub := composeInterp.FindStringSubmatch(m)
		if strings.HasSuffix(sub[2], "-") {
			return sub[3]
		}
		return strings.ToLower(strings.ReplaceAll(sub[1], "_", "-"))
	})
}

// composeService is the part of a compose service the loader sees.
type composeService struct {
	Image       string `yaml:"image"`
	Environment any    `yaml:"environment"`
	Volumes     []any  `yaml:"volumes"`
	Secrets     []any  `yaml:"secrets"`
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

// environ returns the service environment (map or KEY=value list form).
func (s *composeService) environ(t *testing.T) []string {
	t.Helper()
	var out []string
	switch env := s.Environment.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(env)) {
			out = append(out, k+"="+interpolate(scalar(env[k])))
		}
	case []any:
		for _, kv := range env {
			out = append(out, interpolate(scalar(kv)))
		}
	case nil:
	default:
		t.Fatalf("environment of type %T", env)
	}
	return out
}

func scalar(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		b, _ := yaml.Marshal(v)
		return strings.TrimSpace(string(b))
	}
}

// TestExampleCompose loads the xtandard-image service of every
// docs/examples compose file: its environment, the files its bind mounts
// place (from docs/examples) and its secrets under /run/secrets. Only
// those files exist, so a {file: …} or _FILE path that no secret provides
// fails the test.
func TestExampleCompose(t *testing.T) {
	t.Parallel()
	names, err := filepath.Glob(filepath.Join(examplesDir, "compose.*.yml"))
	if err != nil || len(names) < 2 {
		t.Fatalf("compose examples: %v %v", names, err)
	}
	for _, p := range names {
		name := filepath.Base(p)
		var cf composeFile
		if err := yaml.Unmarshal(readExample(t, name), &cf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		svc, ok := cf.Services["xtandard-image"]
		if !ok {
			t.Fatalf("%s: no xtandard-image service", name)
		}
		vfs := fakeFS{files: files{}, strict: true}
		for _, v := range svc.Volumes {
			src, dst, _ := strings.Cut(scalar(v), ":")
			if strings.HasPrefix(src, "./") {
				dst, _, _ = strings.Cut(dst, ":")
				vfs.files[dst] = string(readExample(t, strings.TrimPrefix(src, "./")))
			}
		}
		for _, s := range svc.Secrets {
			target := scalar(s)
			if m, ok := s.(map[string]any); ok {
				target = scalar(m["source"])
				if tg := scalar(m["target"]); tg != "" {
					target = tg
				}
			}
			if !path.IsAbs(target) {
				target = "/run/secrets/" + target
			}
			vfs.files[target] = testHexKey + "\n"
		}
		cfg := loadWith(t, vfs, "", svc.environ(t))
		if len(cfg.Sources) < 3 {
			t.Errorf("%s: only %d sources", name, len(cfg.Sources))
		}
	}
}

// k8sObject is the part of a Kubernetes manifest the loader sees.
type k8sObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Data       map[string]string `yaml:"data"`
	StringData map[string]string `yaml:"stringData"`
	Spec       struct {
		Template struct {
			Spec struct {
				Containers []k8sContainer `yaml:"containers"`
				Volumes    []k8sVolume    `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type k8sKeyPath struct {
	Key  string `yaml:"key"`
	Path string `yaml:"path"`
}

type k8sVolume struct {
	Name      string `yaml:"name"`
	ConfigMap *struct {
		Name  string       `yaml:"name"`
		Items []k8sKeyPath `yaml:"items"`
	} `yaml:"configMap"`
	Secret *struct {
		SecretName string       `yaml:"secretName"`
		Items      []k8sKeyPath `yaml:"items"`
	} `yaml:"secret"`
}

type k8sContainer struct {
	Name string `yaml:"name"`
	Env  []struct {
		Name      string `yaml:"name"`
		Value     string `yaml:"value"`
		ValueFrom *struct {
			SecretKeyRef    *k8sRef `yaml:"secretKeyRef"`
			ConfigMapKeyRef *k8sRef `yaml:"configMapKeyRef"`
		} `yaml:"valueFrom"`
	} `yaml:"env"`
	EnvFrom []struct {
		SecretRef    *k8sRef `yaml:"secretRef"`
		ConfigMapRef *k8sRef `yaml:"configMapRef"`
	} `yaml:"envFrom"`
	VolumeMounts []struct {
		Name      string `yaml:"name"`
		MountPath string `yaml:"mountPath"`
		SubPath   string `yaml:"subPath"`
	} `yaml:"volumeMounts"`
}

type k8sRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// k8sData returns the key → value data of a ConfigMap or Secret in the
// manifests (Secret data is base64). A Secret that is not in the manifests
// (created with kubectl) yields a fake value for each key asked for.
func k8sData(t *testing.T, objs map[string]k8sObject, kind, name string, keys []string) map[string]string {
	t.Helper()
	o, ok := objs[kind+"/"+name]
	if !ok {
		if kind != "Secret" || len(keys) == 0 {
			t.Fatalf("%s %q is not in the manifests and its keys are not listed", kind, name)
		}
		out := map[string]string{}
		for _, k := range keys {
			out[k] = testHexKey + "\n"
		}
		return out
	}
	out := maps.Clone(o.StringData)
	if out == nil {
		out = map[string]string{}
	}
	for k, v := range o.Data {
		if kind == "Secret" {
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				t.Fatalf("Secret %s key %s: %v", name, k, err)
			}
			v = string(b)
		}
		out[k] = v
	}
	return out
}

// TestExampleKubernetes loads the xtandard-image container of every
// Deployment in docs/examples/k8s: env (values, secretKeyRef, envFrom) and
// the files its ConfigMap and Secret volumes mount. Only those files exist.
func TestExampleKubernetes(t *testing.T) {
	t.Parallel()
	names, err := filepath.Glob(filepath.Join(examplesDir, "k8s", "*.yaml"))
	if err != nil || len(names) == 0 {
		t.Fatalf("k8s examples: %v %v", names, err)
	}
	objs := map[string]k8sObject{}
	var deployments []k8sObject
	for _, p := range names {
		dec := yaml.NewDecoder(bytes.NewReader(readExample(t, "k8s/"+filepath.Base(p))))
		for {
			var o k8sObject
			if err := dec.Decode(&o); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("%s: %v", p, err)
			}
			objs[o.Kind+"/"+o.Metadata.Name] = o
			if o.Kind == "Deployment" {
				deployments = append(deployments, o)
			}
		}
	}
	if len(deployments) == 0 {
		t.Fatal("no Deployment in docs/examples/k8s")
	}
	for _, d := range deployments {
		spec := d.Spec.Template.Spec
		i := slices.IndexFunc(spec.Containers, func(c k8sContainer) bool { return c.Name == "xtandard-image" })
		if i < 0 {
			t.Fatalf("Deployment %s: no xtandard-image container", d.Metadata.Name)
		}
		c := spec.Containers[i]
		var environ []string
		for _, ef := range c.EnvFrom {
			switch {
			case ef.SecretRef != nil:
				for k, v := range k8sData(t, objs, "Secret", ef.SecretRef.Name, nil) {
					environ = append(environ, k+"="+v)
				}
			case ef.ConfigMapRef != nil:
				for k, v := range k8sData(t, objs, "ConfigMap", ef.ConfigMapRef.Name, nil) {
					environ = append(environ, k+"="+v)
				}
			}
		}
		for _, e := range c.Env {
			v := e.Value
			if vf := e.ValueFrom; vf != nil {
				switch {
				case vf.SecretKeyRef != nil:
					v = k8sData(t, objs, "Secret", vf.SecretKeyRef.Name, []string{vf.SecretKeyRef.Key})[vf.SecretKeyRef.Key]
				case vf.ConfigMapKeyRef != nil:
					v = k8sData(t, objs, "ConfigMap", vf.ConfigMapKeyRef.Name, nil)[vf.ConfigMapKeyRef.Key]
				}
			}
			environ = append(environ, e.Name+"="+strings.TrimSpace(v))
		}
		vfs := fakeFS{files: files{}, strict: true}
		for _, m := range c.VolumeMounts {
			j := slices.IndexFunc(spec.Volumes, func(v k8sVolume) bool { return v.Name == m.Name })
			if j < 0 {
				t.Fatalf("volume %s not declared", m.Name)
			}
			vol := spec.Volumes[j]
			var data map[string]string
			var items []k8sKeyPath
			switch {
			case vol.ConfigMap != nil:
				items = vol.ConfigMap.Items
				data = k8sData(t, objs, "ConfigMap", vol.ConfigMap.Name, nil)
			case vol.Secret != nil:
				items = vol.Secret.Items
				var keys []string
				for _, it := range items {
					keys = append(keys, it.Key)
				}
				data = k8sData(t, objs, "Secret", vol.Secret.SecretName, keys)
			default:
				continue
			}
			if items == nil {
				for k := range data {
					items = append(items, k8sKeyPath{Key: k, Path: k})
				}
			}
			for _, it := range items {
				v, ok := data[it.Key]
				if !ok {
					t.Fatalf("volume %s: key %s missing", m.Name, it.Key)
				}
				switch {
				case m.SubPath == "":
					vfs.files[path.Join(m.MountPath, it.Path)] = v
				case m.SubPath == it.Path:
					vfs.files[m.MountPath] = v
				}
			}
		}
		cfg := loadWith(t, vfs, "", environ)
		if len(cfg.Sources) < 3 || len(cfg.Signing.Keys) == 0 {
			t.Errorf("Deployment %s: %d sources, %d signing keys", d.Metadata.Name, len(cfg.Sources), len(cfg.Signing.Keys))
		}
	}
}

// fence is one fenced code block of a Markdown document.
type fence struct {
	lang, file, body string
	line             int
}

// fences returns the ``` blocks of a Markdown file. The info string is
// "<lang> [docs/examples/<file>]".
func fences(t *testing.T, doc string) []fence {
	t.Helper()
	b, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	var (
		out  []fence
		cur  *fence
		body strings.Builder
	)
	for i, line := range strings.Split(string(b), "\n") {
		switch {
		case cur == nil && strings.HasPrefix(line, "```"):
			f := strings.Fields(strings.TrimPrefix(line, "```"))
			cur = &fence{line: i + 1}
			if len(f) > 0 {
				cur.lang = f[0]
			}
			if len(f) > 1 && strings.HasPrefix(f[1], "docs/examples/") {
				cur.file = strings.TrimPrefix(f[1], "docs/examples/")
			}
			body.Reset()
		case cur != nil && strings.TrimSpace(line) == "```":
			cur.body = body.String()
			out = append(out, *cur)
			cur = nil
		case cur != nil:
			body.WriteString(line + "\n")
		}
	}
	if cur != nil {
		t.Fatalf("%s: unterminated code block at line %d", doc, cur.line)
	}
	return out
}

// TestDocBlocks checks the code blocks of SOURCES.md and the README:
//
//   - a block tagged with a file (```yaml docs/examples/x.yml) is a copy of
//     that file, byte for byte;
//   - ```dotenv blocks load as the whole environment;
//   - in SOURCES.md, every other yaml, json and toml block loads as a config
//     file (and passes the JSON Schema).
func TestDocBlocks(t *testing.T) {
	t.Parallel()
	sch := compileSchema(t, Schema())
	for _, doc := range []string{sourcesDoc, readmeDoc} {
		checked := 0
		for _, f := range fences(t, doc) {
			where := filepath.Base(doc) + ":" + strconv.Itoa(f.line)
			switch {
			case f.file != "":
				if got := string(readExample(t, f.file)); got != f.body {
					t.Errorf("%s: block differs from docs/examples/%s; copy the file into the block", where, f.file)
				}
			case f.lang == "dotenv":
				loadWith(t, fakeFS{}, "", parseEnvLines(t, f.body))
			case doc == sourcesDoc && (f.lang == "yaml" || f.lang == "json" || f.lang == "toml"):
				name := "/etc/xtandard/config." + f.lang
				body := placeholders(f.body)
				if err := sch.Validate(fileAsJSON(t, name, []byte(body))); err != nil {
					t.Errorf("%s: schema: %v", where, err)
				}
				if _, _, err := Load(LoadOptions{ConfigFile: name, ReadFile: fakeFS{files: files{name: body}}.read, GOMAXPROCS: 4}); err != nil {
					t.Errorf("%s: %v", where, err)
				}
			default:
				continue
			}
			checked++
		}
		if doc == sourcesDoc && checked < 15 {
			t.Errorf("%s: only %d blocks checked", doc, checked)
		}
	}
}
