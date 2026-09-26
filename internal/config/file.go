package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/pelletier/go-toml/v2"
)

// defaultConfigDir holds the config file read when neither --config nor
// XI_CONFIG_FILE is set.
const defaultConfigDir = "/etc/xtandard"

// DefaultConfigFiles describes the files Load looks for when neither
// --config nor XI_CONFIG_FILE is set; at most one of them may exist.
const DefaultConfigFiles = defaultConfigDir + "/config.{yaml,yml,json,toml}"

// maxSecretFileBytes bounds a secret read from a file ({file: …} references
// and XI_…_FILE variables).
const maxSecretFileBytes = 64 << 10

// fileRefPrefix marks a decoded {file: /path} secret reference until
// resolveFileRefs replaces it with the file's content. A literal Secret may
// not start with NUL, so for a Secret the marker is never a real value; a
// HexKey would need its first 14 decoded bytes to spell the marker.
const fileRefPrefix = "\x00xi-file-ref:"

// fileFormat is a config file syntax, chosen by extension.
type fileFormat int

const (
	formatYAML fileFormat = iota
	formatJSON
	formatTOML
)

// fileFormatOf returns the format of path by its extension.
func fileFormatOf(path string) (fileFormat, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
		return formatYAML, nil
	case ".json":
		return formatJSON, nil
	case ".toml":
		return formatTOML, nil
	default:
		return 0, fmt.Errorf("unsupported extension %q: want .yaml, .yml, .json or .toml", ext)
	}
}

// defaultConfigFiles are the candidate files of DefaultConfigFiles.
func defaultConfigFiles() []string {
	exts := []string{".yaml", ".yml", ".json", ".toml"}
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		out = append(out, defaultConfigDir+"/config"+e)
	}
	return out
}

// findDefaultConfig reads the one default config file that exists. It
// returns an empty path when none does.
func findDefaultConfig(readFile func(string) ([]byte, error)) (path string, content []byte, err error) {
	var found []string
	for _, p := range defaultConfigFiles() {
		b, rerr := readFile(p)
		switch {
		case rerr == nil:
			found = append(found, p)
			path, content = p, b
		case !errors.Is(rerr, fs.ErrNotExist):
			return "", nil, fmt.Errorf("%w %s: %w", ErrFile, p, rerr)
		}
	}
	if len(found) > 1 {
		return "", nil, fmt.Errorf("%w: found %s; keep only one", ErrFile, strings.Join(found, " and "))
	}
	return path, content, nil
}

// decodeFile decodes the config file at path (content b) over cfg. YAML
// and JSON (a subset of YAML 1.2) go straight to the strict YAML decoder;
// TOML is decoded into a map and re-encoded as YAML first, so every format
// shares one set of struct tags and one strictness.
func decodeFile(path string, b []byte, cfg *Config) error {
	if path == os.DevNull && len(bytes.TrimSpace(b)) == 0 {
		return nil // --config /dev/null: no file, and no default discovery
	}
	f, err := fileFormatOf(path)
	if err != nil {
		return err
	}
	if f == formatTOML {
		if b, err = tomlToYAML(b); err != nil {
			return err
		}
	}
	return decodeYAML(b, cfg)
}

// tomlToYAML converts a TOML document to YAML. Date and time values are
// rejected: no field takes one.
func tomlToYAML(b []byte) ([]byte, error) {
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		if de, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, col := de.Position()
			return nil, fmt.Errorf("toml: line %d column %d: %s", row, col, de.Error())
		}
		return nil, fmt.Errorf("toml: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	if err := checkTOMLValues(m, ""); err != nil {
		return nil, err
	}
	return yaml.Marshal(m)
}

func checkTOMLValues(v any, path string) error {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			if err := checkTOMLValues(t[k], joinPath(path, k)); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range t {
			if err := checkTOMLValues(e, path+"["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
	case time.Time, toml.LocalDate, toml.LocalTime, toml.LocalDateTime:
		return fmt.Errorf("%s: TOML dates and times are not accepted; quote the value", path)
	}
	return nil
}

// fileDoc is the top level of a config file: Config plus the editor's
// "$schema" key, which is ignored.
type fileDoc struct {
	Schema string `yaml:"$schema"`
	Config `yaml:",inline"`
}

// decodeYAML decodes one strict YAML (or JSON) document over cfg. An empty
// document (or one with only comments) leaves cfg untouched.
func decodeYAML(b []byte, cfg *Config) error {
	f, err := parser.ParseBytes(b, 0)
	if err != nil {
		return err
	}
	var docs int
	for _, d := range f.Docs {
		if d.Body != nil && d.Body.Type() != ast.CommentType {
			docs++
		}
	}
	switch docs {
	case 0:
		return nil
	case 1:
	default:
		return errors.New("want a single YAML document")
	}
	doc := fileDoc{Config: *cfg}
	err = yaml.UnmarshalWithOptions(b, &doc,
		yaml.Strict(),
		yaml.DisallowUnknownField(),
		yaml.CustomUnmarshaler(func(d *time.Duration, raw []byte) error {
			var s string
			if err := yaml.Unmarshal(raw, &s); err != nil {
				return err
			}
			v, err := ParseDuration(s)
			*d = v
			return err
		}),
		// Secret-typed values never fail the decode: a reference or a bad
		// value becomes a marker that resolveFileRefs reports with its key
		// path (goccy gives custom unmarshalers no position).
		yaml.CustomUnmarshaler(func(s *Secret, raw []byte) error {
			v, _ := decodeSecretNode(raw)
			*s = Secret(v)
			return nil
		}),
		yaml.CustomUnmarshaler(func(k *HexKey, raw []byte) error {
			v, literal := decodeSecretNode(raw)
			if !literal {
				*k = HexKey(v)
				return nil
			}
			if err := k.UnmarshalText([]byte(v)); err != nil {
				*k = HexKey(secretErrPrefix + err.Error())
			}
			return nil
		}),
	)
	*cfg = doc.Config
	return err
}

// secretErrPrefix marks a secret-typed value that did not decode; the rest
// is the message (never the value).
const secretErrPrefix = "\x00xi-secret-error:"

// decodeSecretNode decodes a secret-typed value. A scalar is returned as is
// with literal=true; a {file: /path} reference becomes fileRefPrefix+path
// and anything else an error marker (secretErrPrefix).
func decodeSecretNode(raw []byte) (v string, literal bool) {
	f, err := parser.ParseBytes(raw, 0)
	if err == nil && len(f.Docs) == 1 && f.Docs[0].Body != nil {
		switch f.Docs[0].Body.Type() {
		case ast.MappingType, ast.MappingValueType:
			var r struct {
				File string `yaml:"file"`
			}
			if err := yaml.UnmarshalWithOptions(raw, &r, yaml.Strict(), yaml.DisallowUnknownField()); err != nil {
				return secretErrPrefix + "a secret reference must be {file: /absolute/path}", false
			}
			if r.File == "" {
				return secretErrPrefix + "a secret reference needs a file path", false
			}
			return fileRefPrefix + r.File, false
		default:
		}
	}
	var s string
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return secretErrPrefix + "want a string or {file: /absolute/path}", false
	}
	if strings.HasPrefix(s, "\x00") {
		return secretErrPrefix + "a secret must not start with a NUL byte", false
	}
	return s, true
}

// readSecretFile reads a secret from path (an XI_…_FILE variable or a
// {file: …} reference): at most maxSecretFileBytes, trailing newlines
// trimmed. Errors never include the content.
func readSecretFile(readFile func(string) ([]byte, error), path string) (string, error) {
	b, err := readFile(path)
	if err != nil {
		return "", err
	}
	if len(b) > maxSecretFileBytes {
		return "", fmt.Errorf("%s is larger than %d KiB", path, maxSecretFileBytes>>10)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// resolveFileRefs replaces every {file: …} reference left in cfg (the
// environment may have replaced some) with the file's content, parsed like
// a literal, and reports the values that did not decode. Each error names
// the config file and the key path, never the content.
func resolveFileRefs(cfg *Config, configFile string, readFile func(string) ([]byte, error)) error {
	var errList []error
	walkSecrets(reflect.ValueOf(cfg).Elem(), "", func(path string, v reflect.Value) {
		var raw string
		if v.Type() == reflect.TypeFor[Secret]() {
			raw = v.String()
		} else {
			raw = string(v.Bytes())
		}
		if !strings.HasPrefix(raw, fileRefPrefix) && !strings.HasPrefix(raw, secretErrPrefix) {
			return
		}
		v.SetZero()
		if msg, ok := strings.CutPrefix(raw, secretErrPrefix); ok {
			errList = append(errList, fmt.Errorf("%w %s: %s (%s): %s", ErrFile, configFile, path, envNameOf(path), msg))
			return
		}
		file := strings.TrimPrefix(raw, fileRefPrefix)
		fail := func(err error) {
			errList = append(errList, fmt.Errorf("%w %s: %s (%s): {file: %s}: %w", ErrFile, configFile, path, envNameOf(path), file, err))
		}
		if !filepath.IsAbs(file) {
			fail(errors.New("the path must be absolute"))
			return
		}
		content, err := readSecretFile(readFile, file)
		if err != nil {
			fail(err)
			return
		}
		if v.Type() == reflect.TypeFor[Secret]() {
			v.SetString(content)
			return
		}
		var k HexKey
		if err := k.UnmarshalText([]byte(content)); err != nil {
			fail(err)
			return
		}
		v.SetBytes(k)
	})
	return errors.Join(errList...)
}

// walkSecrets calls fn for every Secret and HexKey in v with its YAML path.
// v must be addressable; map values are copied, visited and stored back.
// List elements and map values are written path[i] and path[key], so
// envNameOf still returns the variable of the whole list or map.
func walkSecrets(v reflect.Value, path string, fn func(path string, v reflect.Value)) {
	if t := v.Type(); t == reflect.TypeFor[Secret]() || t == reflect.TypeFor[HexKey]() {
		fn(path, v)
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(sf.Tag.Get("yaml"), ",")
			p := path
			if opts != "inline" {
				p = joinPath(path, name)
			}
			walkSecrets(v.Field(i), p, fn)
		}
	case reflect.Slice:
		for i := range v.Len() {
			walkSecrets(v.Index(i), path+"["+strconv.Itoa(i)+"]", fn)
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, k := range keys {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(v.MapIndex(k))
			p := path + "[" + k.String() + "]"
			if e.Kind() == reflect.Struct { // a named source
				p = joinPath(path, k.String())
			}
			walkSecrets(e, p, fn)
			v.SetMapIndex(k, e)
		}
	default:
	}
}
