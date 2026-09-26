// Package obs groups the observability building blocks (ARCHITECTURE §13,
// go-standards §5). It holds no code itself; each concern lives in a
// subpackage that app wires together:
//
//   - logging: slog JSON handler with request_id / trace_id / span_id
//     injection from the context.
//   - metrics: the Prometheus metric set on a private registry.
//   - tracing: opt-in OpenTelemetry tracer provider and otelhttp middleware.
//   - admin: the admin ServeMux (/metrics, /healthz, /readyz, gated pprof)
//     and the readiness state.
//
// Nothing here uses package-level state: every constructor returns a value the
// caller owns, so tests and multiple instances never collide.
package obs
