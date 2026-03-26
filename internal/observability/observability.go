package observability

import (
	"context"
	"fmt"
	"log"
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
	msg := fmt.Sprintf(format, args...)
	log.Print(withTracePrefix(ctx, msg))
}

func Errorf(ctx context.Context, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(withTracePrefix(ctx, "ERROR: "+msg))
}

func withTracePrefix(ctx context.Context, msg string) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return msg
	}

	return fmt.Sprintf("trace_id=%s span_id=%s %s",
		spanContext.TraceID().String(),
		spanContext.SpanID().String(),
		msg,
	)
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
