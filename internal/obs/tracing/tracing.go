// Package tracing sets up opt-in OpenTelemetry tracing (go-standards §5).
//
// Tracing is off unless the standard OTLP endpoint variables or
// XI_OTEL_ENABLED say otherwise (see EnabledFromEnv). When off, New returns a
// no-op provider: no exporter, no batch goroutine, no network, and Middleware
// returns the handler unchanged. When on, spans are exported through
// autoexport (OTEL_TRACES_EXPORTER, OTEL_EXPORTER_OTLP_* are honoured) with
// W3C TraceContext + Baggage propagation.
//
// Nothing is installed globally (no otel.SetTracerProvider): app passes the
// Provider to whoever creates spans.
package tracing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Environment variables consulted by EnabledFromEnv.
const (
	EnvOTLPEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	EnvOTLPTracesEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	EnvEnabled            = "XI_OTEL_ENABLED"
)

// EnabledFromEnv reports whether tracing should be on: an OTLP endpoint is
// set, or XI_OTEL_ENABLED is a true boolean. getenv is injected (main passes
// os.Getenv) so this package never reads the process environment itself.
func EnabledFromEnv(getenv func(string) string) bool {
	if strings.TrimSpace(getenv(EnvOTLPEndpoint)) != "" || strings.TrimSpace(getenv(EnvOTLPTracesEndpoint)) != "" {
		return true
	}
	on, err := strconv.ParseBool(strings.TrimSpace(getenv(EnvEnabled)))
	return err == nil && on
}

// Options configures New.
type Options struct {
	// Enabled turns tracing on; see EnabledFromEnv.
	Enabled bool
	// ServiceName and ServiceVersion become the service.name and
	// service.version resource attributes. OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES, when set, take precedence.
	ServiceName    string
	ServiceVersion string
	// SampleRatio is the parent-based root sampling ratio in [0,1]; 0 means 1
	// (sample everything the parent does not decide).
	SampleRatio float64

	// exporter overrides autoexport; tests only.
	exporter sdktrace.SpanExporter
}

// Provider owns the tracer provider and its shutdown.
type Provider struct {
	tp         trace.TracerProvider
	sdk        *sdktrace.TracerProvider // nil when disabled
	propagator propagation.TextMapPropagator
}

// New returns a Provider. With tracing disabled it allocates nothing but a
// no-op provider and never fails.
func New(ctx context.Context, opts Options) (*Provider, error) {
	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	if !opts.Enabled {
		return &Provider{tp: noop.NewTracerProvider(), propagator: prop}, nil
	}

	exp := opts.exporter
	if exp == nil {
		var err error
		if exp, err = autoexport.NewSpanExporter(ctx); err != nil {
			return nil, fmt.Errorf("create span exporter: %w", err)
		}
	}

	name := opts.ServiceName
	if name == "" {
		name = "xtandard-image"
	}
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(name), semconv.ServiceVersion(opts.ServiceVersion)),
		resource.WithFromEnv(), // last: OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES win
	)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("build trace resource: %w", err), exp.Shutdown(ctx))
	}

	ratio := opts.SampleRatio
	if ratio <= 0 || ratio > 1 {
		ratio = 1
	}
	sdk := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	return &Provider{tp: sdk, sdk: sdk, propagator: prop}, nil
}

// Enabled reports whether spans are exported.
func (p *Provider) Enabled() bool { return p.sdk != nil }

// TracerProvider returns the provider (no-op when disabled).
func (p *Provider) TracerProvider() trace.TracerProvider { return p.tp }

// Propagator returns the W3C TraceContext + Baggage propagator.
func (p *Provider) Propagator() propagation.TextMapPropagator { return p.propagator }

// Tracer returns a named tracer from the provider.
func (p *Provider) Tracer(name string) trace.Tracer { return p.tp.Tracer(name) }

// Middleware wraps h with otelhttp server instrumentation named operation.
// When tracing is disabled it returns h unchanged, so the disabled path has
// zero per-request overhead. otelhttp's own metrics are disabled: request
// metrics come from internal/obs/metrics.
func (p *Provider) Middleware(h http.Handler, operation string) http.Handler {
	if !p.Enabled() {
		return h
	}
	return otelhttp.NewHandler(h, operation,
		otelhttp.WithTracerProvider(p.tp),
		otelhttp.WithPropagators(p.propagator),
		otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
	)
}

// Shutdown flushes pending spans and stops the exporter. It is a no-op when
// disabled and safe to call more than once.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.sdk == nil {
		return nil
	}
	if err := p.sdk.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown tracer provider: %w", err)
	}
	return nil
}
