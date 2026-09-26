package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func noEnv(string) string { return "" }

func TestRunCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantErr    error
		wantOutput string
	}{
		{name: "version", args: []string{"version"}, wantOutput: "xtandard-image "},
		{name: "help", args: []string{"help"}, wantOutput: "usage: xtandard-image"},
		{name: "version help flag", args: []string{"version", "-h"}, wantOutput: "Usage of version"},
		{name: "unknown", args: []string{"nope"}, wantErr: errUnknownCommand},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := run(t.Context(), tc.args, noEnv, &out)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("run(%q) error = %v, want %v", tc.args, err, tc.wantErr)
			}
			if !strings.Contains(out.String(), tc.wantOutput) {
				t.Errorf("run(%q) output = %q, want it to contain %q", tc.args, out.String(), tc.wantOutput)
			}
		})
	}
}

func TestServeStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("test shutdown"))

	env := map[string]string{
		"XI_SERVER_ADDR":           "127.0.0.1:0",
		"XI_ADMIN_ADDR":            "off",
		"XI_SERVER_SHUTDOWN_DELAY": "0s",
	}
	var out bytes.Buffer
	err := run(ctx, nil, func(k string) string { return env[k] }, &out)
	if errors.Is(err, vips.ErrUnavailable) {
		t.Skip("libvips not linked (CGO_ENABLED=0); run with task docker:test")
	}
	if err != nil {
		t.Fatalf("run(serve) error = %v", err)
	}
	if !strings.Contains(out.String(), `"cause":"test shutdown"`) {
		t.Errorf("serve log = %q, want shutdown cause", out.String())
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "ok", status: http.StatusOK},
		{name: "unavailable", status: http.StatusServiceUnavailable, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)

			var out bytes.Buffer
			err := run(t.Context(), []string{"health", "--url", srv.URL}, noEnv, &out)
			if (err != nil) != tc.wantErr {
				t.Fatalf("health error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveBuildInfo(t *testing.T) {
	t.Parallel()

	embedded := &debug.BuildInfo{
		GoVersion: "go1.27.1",
		Main:      debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	tests := []struct {
		name             string
		ldVersion, ldSHA string
		bi               *debug.BuildInfo
		wantVer, wantSHA string
		wantGo           string
	}{
		{name: "ldflags win", ldVersion: "v9.9.9", ldSHA: "deadbeef", bi: embedded, wantVer: "v9.9.9", wantSHA: "deadbeef", wantGo: "go1.27.1"},
		{name: "build info fallback", bi: embedded, wantVer: "v1.2.3", wantSHA: "abc123-dirty", wantGo: "go1.27.1"},
		{name: "nothing known", bi: &debug.BuildInfo{GoVersion: "go1.27.1"}, wantVer: "(devel)", wantSHA: "unknown", wantGo: "go1.27.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveBuildInfo(tc.ldVersion, tc.ldSHA, tc.bi)
			want := buildInfo{Version: tc.wantVer, Commit: tc.wantSHA, GoVersion: tc.wantGo}
			if got != want {
				t.Errorf("resolveBuildInfo() = %+v, want %+v", got, want)
			}
		})
	}
}
