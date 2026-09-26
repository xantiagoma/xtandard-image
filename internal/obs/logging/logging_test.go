package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/trace"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, b)
	}
	return m
}

func spanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	tid, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
}

func TestHandlerInjectsContext(t *testing.T) {
	t.Parallel()

	sc := spanContext(t)
	tests := []struct {
		name string
		ctx  func(context.Context) context.Context
		want map[string]any
	}{
		{
			name: "no ids",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			want: map[string]any{"level": "INFO", "msg": "request served", "status": float64(200)},
		},
		{
			name: "request id",
			ctx:  func(ctx context.Context) context.Context { return WithRequestID(ctx, "req-1") },
			want: map[string]any{"level": "INFO", "msg": "request served", "status": float64(200), "request_id": "req-1"},
		},
		{
			name: "request and trace ids",
			ctx: func(ctx context.Context) context.Context {
				return trace.ContextWithSpanContext(WithRequestID(ctx, "req-2"), sc)
			},
			want: map[string]any{
				"level": "INFO", "msg": "request served", "status": float64(200),
				"request_id": "req-2",
				"trace_id":   "4bf92f3577b34da6a3ce929d0e0e4736",
				"span_id":    "00f067aa0ba902b7",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := New(&buf, Options{})
			logger.LogAttrs(tc.ctx(t.Context()), slog.LevelInfo, "request served", slog.Int("status", 200))

			got := decode(t, buf.Bytes())
			if _, ok := got["time"]; !ok {
				t.Errorf("missing time key in %v", got)
			}
			delete(got, "time")
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("log line mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHandlerWithAttrsAndGroupKeepInjection(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(&buf, Options{}).With(slog.String("component", "fetch")).WithGroup("src")
	ctx := WithRequestID(t.Context(), "req-3")
	logger.LogAttrs(ctx, slog.LevelWarn, "fetch retried", slog.Int("attempt", 2))

	got := decode(t, buf.Bytes())
	delete(got, "time")
	want := map[string]any{
		"level":     "WARN",
		"msg":       "fetch retried",
		"component": "fetch",
		// Record attributes land inside the open group, including injected IDs.
		"src": map[string]any{"attempt": float64(2), "request_id": "req-3"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("log line mismatch (-want +got):\n%s", diff)
	}
}

func TestLevelFiltering(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	logger := New(&buf, Options{Level: lv})

	logger.LogAttrs(t.Context(), slog.LevelInfo, "dropped")
	if buf.Len() != 0 {
		t.Fatalf("info logged at warn level: %s", buf.Bytes())
	}
	lv.Set(slog.LevelDebug)
	logger.LogAttrs(t.Context(), slog.LevelDebug, "kept")
	if buf.Len() == 0 {
		t.Fatal("debug not logged after lowering level")
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false},
		{" warn ", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"verbose", 0, true},
		{"", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLevel(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseLevel(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestWrapIsIdempotent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := NewHandler(&buf, Options{})
	if Wrap(h) != h {
		t.Fatal("Wrap re-wrapped a *Handler")
	}
}

func TestErrAttr(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(&buf, Options{})
	logger.LogAttrs(t.Context(), slog.LevelError, "fetch failed", Err(errors.New("boom")), Err(nil))

	got := decode(t, buf.Bytes())
	if got[KeyError] != "boom" {
		t.Errorf("error attr = %v, want boom", got[KeyError])
	}
	if _, ok := got[""]; ok {
		t.Error("nil error produced an empty key")
	}
}
