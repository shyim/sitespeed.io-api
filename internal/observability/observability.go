package observability

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "sitespeed-api"

func Setup(ctx context.Context) (func(context.Context) error, error) {
	slog.SetDefault(slog.New(newTraceHandler(slog.NewTextHandler(os.Stderr, nil))))

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(cmp.Or(os.Getenv("OTEL_SERVICE_NAME"), serviceName)),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create otel resource: %w", err)
	}

	if !tracingConfigured() {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)

	slog.Info("OpenTelemetry tracing enabled", "service", cmp.Or(os.Getenv("OTEL_SERVICE_NAME"), serviceName))

	return provider.Shutdown, nil
}

func Tracer(name string) trace.Tracer {
	return otel.Tracer(serviceName + "/" + name)
}

func Printf(ctx context.Context, format string, args ...any) {
	slog.InfoContext(ctx, fmt.Sprintf(format, args...))
}

func Errorf(ctx context.Context, format string, args ...any) {
	slog.ErrorContext(ctx, fmt.Sprintf(format, args...))
}

// traceHandler is a slog.Handler that enriches log records with OpenTelemetry trace context.
type traceHandler struct {
	slog.Handler
}

func newTraceHandler(h slog.Handler) *traceHandler {
	return &traceHandler{Handler: h}
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanCtx.TraceID().String()),
			slog.String("span_id", spanCtx.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return newTraceHandler(h.Handler.WithAttrs(attrs))
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return newTraceHandler(h.Handler.WithGroup(name))
}

func tracingConfigured() bool {
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	} {
		if os.Getenv(key) != "" {
			return true
		}
	}

	return false
}
