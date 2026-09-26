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
			err := run(t.Context(), tc.args, nil, &out)
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

	env := []string{
		"XI_DEV_MODE=true",
		"XI_SERVER_ADDR=127.0.0.1:0",
		"XI_ADMIN_ADDR=",
		"XI_SERVER_SHUTDOWN_DELAY=0s",
	}
	var out bytes.Buffer
	err := run(ctx, nil, env, &out)
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
			err := run(t.Context(), []string{"health", "--url", srv.URL}, nil, &out)
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
			{Key: "vcs.time", Value: "2026-09-01T10:00:00Z"},
		},
	}
	tests := []struct {
		name                      string
		ldVersion, ldSHA, ldDate  string
		bi                        *debug.BuildInfo
		wantVer, wantSHA, wantDay string
		wantGo                    string
	}{
		{name: "ldflags win", ldVersion: "v9.9.9", ldSHA: "deadbeef", ldDate: "2026-09-26T00:00:00Z", bi: embedded, wantVer: "v9.9.9", wantSHA: "deadbeef", wantDay: "2026-09-26T00:00:00Z", wantGo: "go1.27.1"},
		{name: "build info fallback", bi: embedded, wantVer: "v1.2.3", wantSHA: "abc123-dirty", wantDay: "2026-09-01T10:00:00Z", wantGo: "go1.27.1"},
		{name: "nothing known", bi: &debug.BuildInfo{GoVersion: "go1.27.1"}, wantVer: "(devel)", wantSHA: "unknown", wantDay: "unknown", wantGo: "go1.27.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveBuildInfo(tc.ldVersion, tc.ldSHA, tc.ldDate, tc.bi)
			want := buildInfo{Version: tc.wantVer, Commit: tc.wantSHA, Date: tc.wantDay, GoVersion: tc.wantGo}
			if got != want {
				t.Errorf("resolveBuildInfo() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestConfigPrint(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	env := []string{"XI_DEV_MODE=true", "XI_SECURITY_SECRET=hunter2hunter2", "XI_SERVER_ADDR=:9999"}
	if err := run(t.Context(), []string{"config", "print", "--config", "/dev/null"}, env, &out); err != nil {
		t.Fatalf("config print: %v", err)
	}
	if !strings.Contains(out.String(), ":9999") || strings.Contains(out.String(), "hunter2") {
		t.Errorf("config print output lacks the address or leaks the secret:\n%s", out.String())
	}

	out.Reset()
	if err := run(t.Context(), []string{"config", "print", "--config", "/dev/null"}, nil, &out); err == nil {
		t.Error("invalid config (no sources, no dev mode) printed without error")
	}
	if !strings.Contains(out.String(), "server:") {
		t.Errorf("invalid config not printed:\n%s", out.String())
	}

	if err := run(t.Context(), []string{"config", "dump"}, nil, &out); !errors.Is(err, errUnknownCommand) {
		t.Errorf("config dump error = %v, want unknown command", err)
	}
}
