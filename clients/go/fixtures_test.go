package xtandard_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

// root is the repository root, from clients/go.
const root = "../.."

func loadJSON[T any](t *testing.T, rel string) T {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return v
}

// wantCode checks that err is a *BuildError with the code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want %s", code)
	}
	be, ok := errors.AsType[*xtandard.BuildError](err)
	if !ok {
		t.Fatalf("got %T %v, want a *xtandard.BuildError", err, err)
	}
	if string(be.Code) != code || !errors.Is(err, xtandard.ErrorCode(code)) {
		t.Fatalf("got code %s (%v), want %s", be.Code, err, code)
	}
}
