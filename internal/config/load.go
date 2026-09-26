package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// DefaultConfigFile is read when neither --config nor XI_CONFIG_FILE is
// set, if it exists.
const DefaultConfigFile = "/etc/xtandard/config.yaml"

// fileSuffix marks a variable whose value is a path to read the value from.
const fileSuffix = "_FILE"

// Errors reported by Load, matched with errors.Is.
var (
	// ErrUnknownEnv wraps every unknown XI_* variable.
	ErrUnknownEnv = errors.New("unknown environment variable")
	// ErrFile wraps failures to read or decode the YAML file.
	ErrFile = errors.New("config file")
	// ErrEnv wraps failures to parse an environment variable or its _FILE.
	ErrEnv = errors.New("environment")
	// ErrInvalid wraps every Validate violation.
	ErrInvalid = errors.New("invalid config")
)

// LoadOptions are Load's inputs. Nothing is read from the process: main
// passes os.Environ() and the --config flag.
type LoadOptions struct {
	// Environ is the environment as KEY=value pairs (os.Environ()).
	Environ []string
	// ConfigFile is the --config flag; it wins over XI_CONFIG_FILE.
	ConfigFile string
	// ReadFile reads the YAML file and _FILE secrets; nil means os.ReadFile.
	ReadFile func(name string) ([]byte, error)
	// GOMAXPROCS is the default worker count; 0 means runtime.GOMAXPROCS(0).
	GOMAXPROCS int
}

// Meta describes how a Config was loaded.
type Meta struct {
	// File is the YAML file that was read, or "".
	File string
	// UnknownEnv lists unknown XI_* variables tolerated because
	// XI_ALLOW_UNKNOWN_ENV=true. Callers should log them.
	UnknownEnv []string
	// UnmappedImgproxyEnv lists set IMGPROXY_* variables that
	// XI_COMPAT_IMGPROXY_ENV could not translate. Callers should log them
	// in one line; they do not fail startup.
	UnmappedImgproxyEnv []string
	// Warnings are non-fatal findings to log at startup.
	Warnings []string
}

// Load builds the Config: defaults, then the YAML file, then XI_* variables
// (and their _FILE variants), then derived defaults, then Validate. The
// returned Config is populated as far as possible even when err is non-nil,
// so `config print` can show it.
func Load(o LoadOptions) (Config, Meta, error) {
	cfg, meta, err := load(o)
	if err != nil {
		return cfg, meta, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, meta, err
	}
	meta.Warnings = append(meta.Warnings, cfg.warnings()...)
	return cfg, meta, nil
}

// load is Load without Validate.
func load(o LoadOptions) (Config, Meta, error) {
	readFile := o.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	var (
		meta    Meta
		errList []error
	)
	cfg := Defaults()

	static := staticFields()
	staticKeys := make(map[string]bool, len(static)+3)
	for _, f := range static {
		staticKeys[f.Env] = true
	}
	for _, k := range []string{envConfigFile, envAllowUnknown, envSources} {
		staticKeys[k] = true
	}

	vars, origin, readErrs, err := resolveEnv(o.Environ, staticKeys, readFile)
	errList = append(errList, err)

	allowUnknown := false
	if v := strings.TrimSpace(vars[envAllowUnknown]); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errList = append(errList, fmt.Errorf("%w: %s: want true or false", ErrEnv, envAllowUnknown))
		}
		allowUnknown = b
	}

	path, optional := o.ConfigFile, false
	if path == "" {
		path = strings.TrimSpace(vars[envConfigFile])
	}
	if path == "" {
		path, optional = DefaultConfigFile, true
	}
	if b, err := readFile(path); err == nil {
		meta.File = path
		if err := decodeYAML(b, &cfg); err != nil {
			errList = append(errList, fmt.Errorf("%w %s: %w", ErrFile, path, err))
		}
	} else if !optional || !errors.Is(err, fs.ErrNotExist) {
		errList = append(errList, fmt.Errorf("%w %s: %w", ErrFile, path, err))
	}
	if err := foldEncryptionKey(&cfg); err != nil {
		errList = append(errList, err)
	}

	// IMGPROXY_* variables between the file and XI_* (XI_* wins).
	var alias map[string]string
	if imgproxyCompatEnabled(&cfg, vars) {
		c := translateImgproxyEnv(o.Environ)
		errList = append(errList, c.err)
		meta.UnmappedImgproxyEnv = c.unmapped
		meta.Warnings = append(meta.Warnings, c.warnings...)
		alias = applyImgproxyCompat(vars, origin, &c)
	} else if n := len(imgproxyEnviron(o.Environ)); n > 0 {
		meta.Warnings = append(meta.Warnings, fmt.Sprintf("%d IMGPROXY_* variables are set but ignored; set %s=true to translate them", n, envCompatImgproxy))
	}

	// Environment on top of the file.
	errList = append(errList, parseEnv(&cfg, vars, envPrefix, static, alias))
	if cfg.Encryption.Key != nil {
		if strings.TrimSpace(vars["XI_ENCRYPTION_KEYS"]) != "" {
			errList = append(errList, fmt.Errorf("%w: set XI_ENCRYPTION_KEYS or its deprecated alias XI_ENCRYPTION_KEY, not both", ErrEnv))
		} else {
			meta.Warnings = append(meta.Warnings, "XI_ENCRYPTION_KEY is deprecated; use XI_ENCRYPTION_KEYS")
		}
		cfg.Encryption.Keys, cfg.Encryption.Key = []HexKey{cfg.Encryption.Key}, nil
	}

	// Named sources: the file's plus those listed in XI_SOURCES.
	for name := range strings.SplitSeq(vars[envSources], ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if cfg.Sources == nil {
			cfg.Sources = map[string]Source{}
		}
		if _, ok := cfg.Sources[name]; !ok {
			cfg.Sources[name] = Source{}
		}
	}
	known := maps.Clone(staticKeys)
	for _, name := range slices.Sorted(maps.Keys(cfg.Sources)) {
		src := cfg.Sources[name]
		fields := sourceFields(name)
		errList = append(errList, parseEnv(&src, vars, sourceEnvPrefix(name), fields, alias))
		cfg.Sources[name] = src
		for _, f := range fields {
			known[f.Env] = true
		}
	}

	for _, base := range slices.Sorted(maps.Keys(readErrs)) {
		if known[base] {
			errList = append(errList, readErrs[base])
		}
	}
	unknown := unknownVars(vars, origin, known)
	if allowUnknown {
		meta.UnknownEnv = unknown
	} else {
		for _, k := range unknown {
			errList = append(errList, fmt.Errorf("%w %s%s (set %s=true to ignore)", ErrUnknownEnv, k, suggest(k, known, cfg.Sources), envAllowUnknown))
		}
	}

	cfg.resolveDerived(o.GOMAXPROCS)
	return cfg, meta, errors.Join(errList...)
}

// imgproxyCompatEnabled reports whether IMGPROXY_* variables are
// translated: XI_COMPAT_IMGPROXY_ENV when set (a malformed value is reported
// by parseEnv and counts as off), otherwise compat.imgproxy_env from the
// file.
func imgproxyCompatEnabled(cfg *Config, vars map[string]string) bool {
	if v := strings.TrimSpace(vars[envCompatImgproxy]); v != "" {
		b, _ := strconv.ParseBool(v)
		return b
	}
	return cfg.Compat.ImgproxyEnv
}

// resolveEnv returns the XI_* variables with every _FILE indirection
// replaced by the file's content. origin maps a resolved name back to the
// variable the user set (NAME_FILE) for error messages.
// Read failures are returned per variable in readErrs so that a failure
// for an unknown variable is reported as unknown (and can be tolerated).
func resolveEnv(environ []string, staticKeys map[string]bool, readFile func(string) ([]byte, error)) (vars, origin map[string]string, readErrs map[string]error, err error) {
	vars, origin, readErrs = map[string]string{}, map[string]string{}, map[string]error{}
	files := map[string]string{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(k, envPrefix) {
			continue
		}
		if base, ok := strings.CutSuffix(k, fileSuffix); ok && !staticKeys[k] {
			files[base] = v
			continue
		}
		vars[k] = v
	}
	var errList []error
	for _, base := range slices.Sorted(maps.Keys(files)) {
		path := strings.TrimSpace(files[base])
		if path == "" {
			continue
		}
		if vars[base] != "" {
			errList = append(errList, fmt.Errorf("%w: set %s or %s%s, not both", ErrEnv, base, base, fileSuffix))
			continue
		}
		origin[base] = base + fileSuffix
		b, err := readFile(path)
		if err != nil {
			vars[base] = ""
			readErrs[base] = fmt.Errorf("%w: %s%s: %w", ErrEnv, base, fileSuffix, err)
			continue
		}
		vars[base] = strings.TrimRight(string(b), "\r\n")
	}
	return vars, origin, readErrs, errors.Join(errList...)
}

// parseEnv applies vars onto dst (a *Config or *Source) with caarlos0/env.
// On failure it re-parses each variable alone so every error names its
// variable instead of a Go field, and the IMGPROXY_* variable behind it
// (alias) when the value was translated.
func parseEnv(dst any, vars map[string]string, prefix string, fields []field, alias map[string]string) error {
	opts := env.Options{Environment: vars, Prefix: prefix, FuncMap: funcMap()}
	err := env.ParseWithOptions(dst, opts)
	if err == nil {
		return nil
	}
	var errList []error
	for _, f := range fields {
		v, ok := vars[f.Env]
		if !ok || v == "" {
			continue
		}
		scratch := reflect.New(reflect.TypeOf(dst).Elem())
		one := env.Options{Environment: map[string]string{f.Env: v}, Prefix: prefix, FuncMap: funcMap()}
		if err := env.ParseWithOptions(scratch.Interface(), one); err != nil {
			name := f.Env
			if a := alias[f.Env]; a != "" {
				name += " (from " + a + ")"
			}
			errList = append(errList, fmt.Errorf("%w: %s: %w", ErrEnv, name, innerParseErr(err)))
		}
	}
	if len(errList) == 0 { // not attributable to one variable; report as is
		return fmt.Errorf("%w: %w", ErrEnv, err)
	}
	return errors.Join(errList...)
}

// innerParseErr strips caarlos0's wrapping, which names Go fields and types.
func innerParseErr(err error) error {
	if pe, ok := errors.AsType[env.ParseError](err); ok {
		return pe.Err
	}
	return err
}

// funcMap holds the env parsers for types without UnmarshalText. Values are
// trimmed so "80, 443" works.
func funcMap() map[reflect.Type]env.ParserFunc {
	return map[reflect.Type]env.ParserFunc{
		reflect.TypeFor[time.Duration](): func(v string) (any, error) { return ParseDuration(v) },
		reflect.TypeFor[string]():        func(v string) (any, error) { return strings.TrimSpace(v), nil },
		reflect.TypeFor[Secret]():        func(v string) (any, error) { return Secret(v), nil },
		reflect.TypeFor[bool]():          func(v string) (any, error) { return strconv.ParseBool(strings.TrimSpace(v)) },
		reflect.TypeFor[int](): func(v string) (any, error) {
			return strconv.Atoi(strings.TrimSpace(v))
		},
		reflect.TypeFor[int64](): func(v string) (any, error) {
			return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		},
		reflect.TypeFor[float64](): func(v string) (any, error) {
			return strconv.ParseFloat(strings.TrimSpace(v), 64)
		},
	}
}

// decodeYAML decodes one strict YAML document over cfg. An empty document
// (or one with only comments) leaves cfg untouched.
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
	return yaml.UnmarshalWithOptions(b, cfg,
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
	)
}

// foldEncryptionKey moves the deprecated encryption.key into keys.
func foldEncryptionKey(cfg *Config) error {
	e := &cfg.Encryption
	if e.Key == nil {
		return nil
	}
	if len(e.Keys) > 0 {
		return fmt.Errorf("%w: set encryption.keys or its deprecated alias encryption.key, not both", ErrFile)
	}
	e.Keys, e.Key = []HexKey{e.Key}, nil
	return nil
}

// resolveDerived fills defaults that depend on other settings.
func (c *Config) resolveDerived(gomaxprocs int) {
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = c.Server.RequestTimeout + 5*time.Second
	}
	if c.Processing.Workers == 0 {
		if gomaxprocs <= 0 {
			gomaxprocs = runtime.GOMAXPROCS(0)
		}
		c.Processing.Workers = gomaxprocs
	}
	if c.Processing.QueueSize < 0 && c.Processing.Workers > 0 {
		c.Processing.QueueSize = c.Processing.Workers * 16
	}
	if c.Signing.Required == Auto {
		c.Signing.Required = False
		if len(c.Signing.Keys) > 0 {
			c.Signing.Required = True
		}
	}
}

// unknownVars lists set XI_* variables that are not known, by the name the
// user wrote.
func unknownVars(vars, origin map[string]string, known map[string]bool) []string {
	var out []string
	for k := range vars {
		if known[k] {
			continue
		}
		if o, ok := origin[k]; ok {
			k = o
		}
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// suggest returns a " (did you mean …)" hint for an unknown variable.
func suggest(name string, known map[string]bool, sources map[string]Source) string {
	base := strings.TrimSuffix(name, fileSuffix)
	if rest, ok := strings.CutPrefix(base, envSources+"_"); ok {
		for _, f := range sourceFields("x") {
			suffix := strings.TrimPrefix(f.Env, sourceEnvPrefix("x"))
			if n, ok := strings.CutSuffix(rest, "_"+suffix); ok && n != "" {
				src := strings.ToLower(strings.ReplaceAll(n, "_", "-"))
				if _, exists := sources[src]; !exists {
					return fmt.Sprintf(" (source %q is not listed in %s or the config file)", src, envSources)
				}
			}
		}
	}
	best, bestDist := "", 4
	for k := range known {
		if d := levenshtein(base, k); d < bestDist || (d == bestDist && k < best) {
			best, bestDist = k, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %s?)", best)
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
