package observability

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceHandlerAddsTraceContext(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	var buf bytes.Buffer
	handler := newTraceHandler(slog.NewTextHandler(&buf, nil))
	logger := slog.New(handler)

	ctx, span := provider.Tracer("test").Start(context.Background(), "span")
	defer span.End()

	logger.InfoContext(ctx, "hello world")

	output := buf.String()
	spanCtx := trace.SpanContextFromContext(ctx)

	assert.Contains(t, output, "hello world")
	assert.Contains(t, output, spanCtx.TraceID().String())
	assert.Contains(t, output, spanCtx.SpanID().String())
}

func TestTraceHandlerWithoutTrace(t *testing.T) {
	var buf bytes.Buffer
	handler := newTraceHandler(slog.NewTextHandler(&buf, nil))
	logger := slog.New(handler)

	logger.InfoContext(context.Background(), "no trace")

	output := buf.String()

	assert.Contains(t, output, "no trace")
	assert.NotContains(t, output, "trace_id")
	assert.NotContains(t, output, "span_id")
}
