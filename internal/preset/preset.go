// Copyright 2017 Sergey Alexandrovich (imgproxy), Apache License 2.0.
// Preset definition syntax and file format reimplemented from imgproxy
// options/parser/presets.go. Modifications copyright xtandard-image
// contributors, MIT. See NOTICE.

// Package preset holds the named presets of XI_PRESETS, XI_PRESETS_FILE and
// YAML "presets:" (docs/CONFIG.md), parsed once at startup.
//
// A definition is "name=options" where options use the imgproxy processing
// options syntax (docs/URL-DIALECTS.md §3): "/"-separated "name:arg:arg"
// options, with a segment that is exactly "-" starting a chained pipeline:
//
//	sharp=sharpening:0.7
//	avatar=resizing_type:fill/width:100/height:100/gravity:sm
//	chain=width:300/-/width:100
//
// This package only knows the grammar: it splits definitions into options
// and pipelines, resolves references between presets ("preset"/"pr"
// options) and rejects unknown references, cycles and definitions that
// would exceed transform.MaxPipelines. What each option means is up to the
// dialect that applies it; the imgproxy dialect validates every preset at
// construction by applying it to an empty request, so a bad option is a
// startup error too.
//
// Semantics (imgproxy options/parser, docs "Presets" and "Chained
// pipelines"): a preset is expanded in place where it is referenced, so
// options after the reference override it and options before it are
// overridden by it; the preset named "default" is applied before every
// URL's options; a preset's first pipeline applies to the pipeline it is
// referenced from and its chained pipelines merge into the following ones.
//
// Deliberate differences from imgproxy, all detected at startup:
//
//   - A cycle between presets is an error (imgproxy logs a warning at
//     request time and skips the recursive reference). Acyclic nesting is
//     allowed and expands exactly as in imgproxy.
//   - A reference to an undefined preset inside a preset is an error
//     (imgproxy fails every request that uses it).
//   - A name defined twice (for example in XI_PRESETS and XI_PRESETS_FILE)
//     is an error (imgproxy keeps the last one).
//   - The value is everything after the first "=", so option arguments may
//     contain "=" (imgproxy rejects any second "=").
package preset

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// DefaultName is the preset applied before every URL's options.
const DefaultName = "default"

// DefaultSeparator separates an option name from its arguments.
const DefaultSeparator = ":"

// Option is one parsed "name:arg:arg" option. Args has at least one
// element. Option values are shared by every request and must not be
// modified.
type Option struct {
	Name string
	Args []string
}

// IsReference reports whether o references other presets.
func (o Option) IsReference() bool { return o.Name == "preset" || o.Name == "pr" }

// Preset is a parsed definition. Pipelines[0] applies to the pipeline the
// preset is referenced from, Pipelines[i] to the i-th pipeline chained after
// it. Every pipeline has at least one option.
type Preset struct {
	Name      string
	Pipelines [][]Option
}

// Options configures New.
type Options struct {
	// Definitions maps a name to its options (XI_PRESETS, YAML presets).
	Definitions map[string]string
	// File is a presets file (XI_PRESETS_FILE), read once by New; "" means
	// none. See ParseFile for the format.
	File string
	// Separator separates option arguments; "" means ":". It must match the
	// imgproxy dialect's arguments separator.
	Separator string
	// Only restricts URLs to preset references (XI_PRESETS_ONLY).
	Only bool
}

// Store is an immutable set of presets. It is safe for concurrent use. A
// nil *Store has no presets.
type Store struct {
	presets map[string]*Preset
	names   []string // sorted
	sep     string
	only    bool
}

// New parses and cross-checks every definition. The error names the
// offending preset.
func New(o Options) (*Store, error) {
	sep := o.Separator
	if sep == "" {
		sep = DefaultSeparator
	}
	if strings.Contains(sep, "/") {
		return nil, errors.New("preset: separator must not contain '/'")
	}
	defs := make(map[string]string, len(o.Definitions))
	maps.Copy(defs, o.Definitions)
	if o.File != "" {
		fromFile, err := readFile(o.File)
		if err != nil {
			return nil, err
		}
		for name, value := range fromFile {
			if _, dup := defs[name]; dup {
				return nil, fmt.Errorf("preset %q: defined in both presets and presets_file", name)
			}
			defs[name] = value
		}
	}

	s := &Store{presets: make(map[string]*Preset, len(defs)), sep: sep, only: o.Only}
	for name, value := range defs {
		p, err := parseDefinition(name, value, sep)
		if err != nil {
			return nil, err
		}
		s.presets[name] = p
		s.names = append(s.names, name)
	}
	slices.Sort(s.names)
	if o.Only && len(s.names) == 0 {
		return nil, errors.New("preset: only-presets mode needs at least one preset")
	}
	if err := s.checkReferences(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns the named preset.
func (s *Store) Get(name string) (*Preset, bool) {
	if s == nil {
		return nil, false
	}
	p, ok := s.presets[name]
	return p, ok
}

// Default returns the "default" preset if one is defined.
func (s *Store) Default() (*Preset, bool) { return s.Get(DefaultName) }

// Names returns the preset names in sorted order.
func (s *Store) Names() []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.names)
}

// Len returns the number of presets.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	return len(s.names)
}

// Only reports whether URLs may contain preset references only.
func (s *Store) Only() bool { return s != nil && s.only }

// Separator returns the arguments separator the definitions were parsed
// with.
func (s *Store) Separator() string {
	if s == nil {
		return DefaultSeparator
	}
	return s.sep
}

// parseDefinition splits value into pipelines of options.
func parseDefinition(name, value, sep string) (*Preset, error) {
	if name == "" {
		return nil, errors.New("preset: empty preset name")
	}
	if strings.Contains(name, sep) || strings.Contains(name, "/") || strings.TrimSpace(name) != name {
		return nil, fmt.Errorf("preset %q: name must not contain %q, '/' or surrounding spaces", name, sep)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("preset %q: empty value", name)
	}
	p := &Preset{Name: name, Pipelines: [][]Option{nil}}
	for seg := range strings.SplitSeq(value, "/") {
		if seg == "-" {
			if len(p.Pipelines[len(p.Pipelines)-1]) == 0 {
				return nil, fmt.Errorf("preset %q: empty pipeline", name)
			}
			p.Pipelines = append(p.Pipelines, nil)
			continue
		}
		optName, argStr, ok := strings.Cut(seg, sep)
		if !ok || optName == "" {
			return nil, fmt.Errorf("preset %q: %q is not an option (name%sargs)", name, truncate(seg), sep)
		}
		o := Option{Name: optName, Args: strings.Split(argStr, sep)}
		if o.IsReference() && slices.Contains(o.Args, "") {
			return nil, fmt.Errorf("preset %q: empty preset reference", name)
		}
		last := len(p.Pipelines) - 1
		p.Pipelines[last] = append(p.Pipelines[last], o)
	}
	if len(p.Pipelines[len(p.Pipelines)-1]) == 0 {
		return nil, fmt.Errorf("preset %q: empty pipeline", name)
	}
	return p, nil
}

// checkReferences rejects references to undefined presets, cycles, and
// presets whose expansion spans more than transform.MaxPipelines.
func (s *Store) checkReferences() error {
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(s.presets))
	span := make(map[string]int, len(s.presets)) // pipelines covered from the reference point
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case visiting:
			return fmt.Errorf("preset %q: reference cycle %s", path[0], strings.Join(append(path, name), " -> "))
		case done:
			return nil
		}
		state[name] = visiting
		p := s.presets[name]
		n := len(p.Pipelines)
		for i, pl := range p.Pipelines {
			for _, o := range pl {
				if !o.IsReference() {
					continue
				}
				for _, ref := range o.Args {
					if _, ok := s.presets[ref]; !ok {
						return fmt.Errorf("preset %q: references undefined preset %q", name, ref)
					}
					if err := visit(ref, append(path, name)); err != nil {
						return err
					}
					n = max(n, i+span[ref])
				}
			}
		}
		if n > transform.MaxPipelines {
			return fmt.Errorf("preset %q: expands to %d pipelines, more than %d", name, n, transform.MaxPipelines)
		}
		span[name] = n
		state[name] = done
		return nil
	}
	for _, name := range s.names {
		if err := visit(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// maxLine bounds one line of a presets file.
const maxLine = 64 << 10

// ParseFile reads a presets file: one "name=options" definition per line;
// blank lines and lines starting with "#" (after trimming spaces) are
// skipped. A name defined twice is an error. The value is not parsed; New
// does that.
func ParseFile(r io.Reader) (map[string]string, error) {
	defs := make(map[string]string)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		switch {
		case !ok:
			return nil, fmt.Errorf("preset: line %d: expected name=options", lineNo)
		case name == "":
			return nil, fmt.Errorf("preset: line %d: empty preset name", lineNo)
		case value == "":
			return nil, fmt.Errorf("preset %q: line %d: empty value", name, lineNo)
		}
		if _, dup := defs[name]; dup {
			return nil, fmt.Errorf("preset %q: line %d: defined twice", name, lineNo)
		}
		defs[name] = value
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("preset: reading presets file: %w", err)
	}
	return defs, nil
}

func readFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("preset: opening presets file: %w", err)
	}
	defer f.Close()
	return ParseFile(f)
}

func truncate(s string) string {
	const maxQuoted = 64
	if len(s) > maxQuoted {
		return s[:maxQuoted]
	}
	return s
}
