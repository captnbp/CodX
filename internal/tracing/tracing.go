// Package tracing sets up OpenTelemetry tracing for the CodX server.
//
// When tracing is enabled in the configuration, a global TracerProvider is
// created with an OTLP/HTTP exporter pointing at the configured collector.
// The provider is used by the otelhttp middleware (server spans for incoming
// requests) and the otelhttp transport (client spans for requests proxied to
// workspace pods, which propagates the W3C trace context downstream so the
// workspace Envoy sidecar and code-server join the same trace).
//
// When tracing is disabled, a no-op provider is installed and the helpers in
// this package are cheap pass-throughs.
package tracing

import (
	"context"
	"net/http"
	"time"

	"github.com/captnbp/CodX/internal/config"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Setup configures the global OpenTelemetry SDK according to cfg and returns
// a shutdown function that flushes and releases the provider. The shutdown
// function is safe to call even when tracing is disabled.
//
// The global W3C trace context propagator is always installed so that
// incoming trace context is extracted and forwarded to workspaces.
func Setup(ctx context.Context, cfg config.TracingConfig) (func(context.Context) error, error) {
	// Always propagate W3C trace context and baggage headers.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if !cfg.Enabled {
		// No-op provider: spans are not recorded.
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
		// The collector is expected to be in-cluster (plain HTTP on 4318).
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)

	return provider.Shutdown, nil
}

// Handler wraps an http.Handler with the otelhttp middleware, creating server
// spans for incoming requests and extracting trace context from headers.
func Handler(name string, h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, name)
}

// Transport wraps an http.RoundTripper with the otelhttp transport,
// creating client spans for outgoing requests and injecting the W3C trace
// context into them.
func Transport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base)
}

// timeout for the shutdown flush.
const shutdownTimeout = 5 * time.Second

// Shutdown flushes the provider with a bounded timeout. Call after the HTTP
// servers have stopped so late spans are exported.
func Shutdown(shutdown func(context.Context) error) error {
	if shutdown == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return shutdown(ctx)
}
