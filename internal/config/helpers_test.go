package config

import (
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// files is an in-memory ReadFile.
type files map[string]string

func (f files) read(name string) ([]byte, error) {
	if v, ok := f[name]; ok {
		return []byte(v), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// opts builds LoadOptions reading from f with a fixed GOMAXPROCS of 4.
func opts(f files, environ ...string) LoadOptions {
	return LoadOptions{Environ: environ, ReadFile: f.read, GOMAXPROCS: 4}
}

// mustParse runs load (no Validate) and fails the test on error.
func mustParse(t *testing.T, f files, environ ...string) Config {
	t.Helper()
	cfg, _, err := load(opts(f, environ...))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

// with returns the smallest valid environment plus extra.
func with(extra ...string) []string {
	return append([]string{"XI_SOURCES=origin", "XI_SOURCES_ORIGIN_TYPE=http", "XI_SOURCES_ORIGIN_ALLOW=cdn.example.com"}, extra...)
}

// diff is cmp.Diff with the options Config needs.
func diff(want, got any) string {
	return cmp.Diff(want, got, cmp.Comparer(func(a, b CIDR) bool { return a == b }))
}
