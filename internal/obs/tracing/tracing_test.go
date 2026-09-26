package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestEnabledFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", nil, false},
		{"endpoint", map[string]string{EnvOTLPEndpoint: "http://collector:4318"}, true},
		{"traces endpoint", map[string]string{EnvOTLPTracesEndpoint: "http://collector:4318/v1/traces"}, true},
		{"blank endpoint", map[string]string{EnvOTLPEndpoint: "  "}, false},
		{"xi enabled", map[string]string{EnvEnabled: "true"}, true},
		{"xi enabled 1", map[string]string{EnvEnabled: "1"}, true},
		{"xi disabled", map[string]string{EnvEnabled: "false"}, false},
		{"xi garbage", map[string]string{EnvEnabled: "yes please"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(k string) string { return tc.env[k] }
			if got := EnabledFromEnv(getenv); got != tc.want {
				t.Errorf("EnabledFromEnv = %v, want %v", got, tc.want)
			}
		})
	}
}

type marker struct{ called bool }

func (m *marker) ServeHTTP(http.ResponseWriter, *http.Request) { m.called = true }

// TestDisabledIsNoop runs inside a synctest bubble: synctest.Test fails if
// any goroutine started in the bubble (a batch span processor, an exporter
// connection) is still alive when the function returns, so passing proves
// the disabled path starts no goroutines and opens no connections.
func TestDisabledIsNoop(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		p, err := New(t.Context(), Options{Enabled: false, ServiceName: "xi-test"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if p.Enabled() {
			t.Fatal("provider enabled without opt-in")
		}
		if _, ok := p.TracerProvider().(noop.TracerProvider); !ok {
			t.Fatalf("TracerProvider is %T, want noop.TracerProvider", p.TracerProvider())
		}
		if p.sdk != nil {
			t.Fatal("SDK provider (and exporter) created while disabled")
		}

		_, span := p.Tracer("test").Start(t.Context(), "op")
		if span.IsRecording() || span.SpanContext().IsValid() {
			t.Error("noop span records or has a valid context")
		}
		span.End()

		h := &marker{}
		if got := p.Middleware(h, "image"); got != http.Handler(h) {
			t.Errorf("Middleware wrapped the handler while disabled: %T", got)
		}
		if err := p.Shutdown(t.Context()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
}

// keepExporter keeps spans across Shutdown (InMemoryExporter resets on
// Shutdown) and counts shutdown calls.
type keepExporter struct {
	*tracetest.InMemoryExporter
	shutdowns atomic.Int32
}

func (e *keepExporter) Shutdown(context.Context) error {
	e.shutdowns.Add(1)
	return nil
}

func TestEnabledExportsServerSpans(t *testing.T) {
	t.Parallel()

	exp := &keepExporter{InMemoryExporter: tracetest.NewInMemoryExporter()}
	p, err := New(t.Context(), Options{
		Enabled:        true,
		ServiceName:    "xi-test",
		ServiceVersion: "v1.2.3",
		exporter:       exp,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !p.Enabled() {
		t.Fatal("provider disabled despite opt-in")
	}

	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	var inner trace.SpanContext
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}), "image")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/insecure/rs:fit:100/plain/x.jpg", http.NoBody)
	req.Header.Set("Traceparent", parent)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := inner.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("incoming trace context not propagated: trace_id %s", got)
	}

	if err := p.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := p.Shutdown(t.Context()); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}

	if n := exp.shutdowns.Load(); n != 1 {
		t.Errorf("exporter shut down %d times, want 1", n)
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported %d spans, want 1", len(spans))
	}
	s := spans[0]
	if s.SpanKind != trace.SpanKindServer {
		t.Errorf("span kind %v, want server", s.SpanKind)
	}
	if s.Parent.SpanID().String() != "00f067aa0ba902b7" {
		t.Errorf("parent span %s, want remote parent", s.Parent.SpanID())
	}
	attrs := map[string]string{}
	for _, kv := range s.Resource.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	if attrs[string(semconv.ServiceNameKey)] != "xi-test" || attrs[string(semconv.ServiceVersionKey)] != "v1.2.3" {
		t.Errorf("resource attributes %v lack service name/version", attrs)
	}
}
