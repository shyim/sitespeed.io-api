package observability

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "sitespeed-api"

func Setup(ctx context.Context) (func(context.Context) error, error) {
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
			semconv.ServiceName(defaultString(os.Getenv("OTEL_SERVICE_NAME"), serviceName)),
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

	log.Printf("OpenTelemetry tracing enabled for service %s", defaultString(os.Getenv("OTEL_SERVICE_NAME"), serviceName))

	return provider.Shutdown, nil
}

func Tracer(name string) trace.Tracer {
	return otel.Tracer(serviceName + "/" + name)
}

func Printf(ctx context.Context, format string, args ...any) {
	log.Printf(withTraceContext(ctx, format), args...)
}

func Errorf(ctx context.Context, format string, args ...any) {
	log.Printf(withTraceContext(ctx, "ERROR: "+format), args...)
}

func AttributesFromContext(ctx context.Context) []attribute.KeyValue {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return nil
	}

	return []attribute.KeyValue{
		attribute.String("trace.id", spanContext.TraceID().String()),
		attribute.String("span.id", spanContext.SpanID().String()),
	}
}

func withTraceContext(ctx context.Context, format string) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return format
	}

	return strings.Join([]string{
		fmt.Sprintf("trace_id=%s", spanContext.TraceID().String()),
		fmt.Sprintf("span_id=%s", spanContext.SpanID().String()),
		format,
	}, " ")
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

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}
